package syncer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/syncer/mocks"
	"github.com/k0nsta/rigger/internal/testutil"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// machines makes a bare remote with one commit and two clones of it ("two machines").
func machines(t *testing.T) (a, b *Syncer) {
	t.Helper()
	seed := testutil.Tree(t, map[string]string{"manifest.yaml": "version: 1\n", "zsh/.zshrc": "one\n"})
	git(t, seed, "init", "-q", "-b", "main")
	git(t, seed, "add", ".")
	git(t, seed, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "clone", "-q", "--bare", seed, remote)

	clone := func(name string) *Syncer {
		dir := filepath.Join(t.TempDir(), name)
		git(t, "", "clone", "-q", remote, dir)
		git(t, dir, "config", "user.name", name)
		git(t, dir, "config", "user.email", name+"@example.com")
		git(t, dir, "config", "pull.rebase", "true")
		return &Syncer{Repo: dir, Runner: sh.NewExec(t.TempDir())}
	}
	return clone("a"), clone("b")
}

func TestChanges(t *testing.T) {
	ctx := context.Background()
	a, _ := machines(t)
	testutil.WriteFile(t, filepath.Join(a.Repo, "zsh", ".zshrc"), "two\n")
	testutil.WriteFile(t, filepath.Join(a.Repo, "hosts", "a.yaml"), "profile: x\n")
	changes, err := a.Changes(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []Change{{Path: "zsh/.zshrc", Code: " M"}, {Path: "hosts/a.yaml", Code: "??"}}, changes)
}

func TestCommitPushPullAcrossMachines(t *testing.T) {
	ctx := context.Background()
	a, b := machines(t)
	testutil.WriteFile(t, filepath.Join(a.Repo, "zsh", ".zshrc"), "from a\n")
	testutil.WriteFile(t, filepath.Join(a.Repo, "hosts", "a.yaml"), "profile: x\n")
	testutil.WriteFile(t, filepath.Join(a.Repo, "scratch.txt"), "not picked\n")

	require.NoError(t, a.Commit(ctx, "a", []string{"zsh/.zshrc", "hosts/a.yaml"}))
	assert.Equal(t, 1, a.Ahead(ctx))
	require.NoError(t, a.Push(ctx))
	assert.Contains(t, git(t, a.Repo, "log", "-1", "--format=%s"), "sync(a): zsh/.zshrc, hosts/a.yaml")
	assert.Contains(t, git(t, a.Repo, "status", "--porcelain"), "?? scratch.txt", "only picked files are committed")

	require.NoError(t, b.Pull(ctx))
	assert.Equal(t, "from a\n", testutil.ReadFile(t, filepath.Join(b.Repo, "zsh", ".zshrc")))
	assert.FileExists(t, filepath.Join(b.Repo, "hosts", "a.yaml"))
}

func TestPullConflictAborts(t *testing.T) {
	ctx := context.Background()
	a, b := machines(t)
	testutil.WriteFile(t, filepath.Join(a.Repo, "zsh", ".zshrc"), "a edit\n")
	require.NoError(t, a.Commit(ctx, "a", []string{"zsh/.zshrc"}))
	require.NoError(t, a.Push(ctx))

	testutil.WriteFile(t, filepath.Join(b.Repo, "zsh", ".zshrc"), "b edit\n")
	require.NoError(t, b.Commit(ctx, "b", []string{"zsh/.zshrc"}))
	err := b.Pull(ctx)
	require.ErrorIs(t, err, ErrConflict)
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, []string{"zsh/.zshrc"}, conflict.Files)
	assert.Contains(t, err.Error(), "in zsh/.zshrc; rigger aborted the rebase: your commits stay local")
	assert.NotContains(t, err.Error(), "git rebase --skip", "git's own hints do not apply after the abort")
	assert.Equal(t, 1, b.Ahead(ctx), "b's commit is kept")
	assert.Equal(t, "b edit\n", testutil.ReadFile(t, filepath.Join(b.Repo, "zsh", ".zshrc")), "no conflict markers left")
	assert.NoDirExists(t, filepath.Join(b.Repo, ".git", "rebase-merge"))
}

func TestPullStopsOnUncommittedChanges(t *testing.T) {
	ctx := context.Background()
	a, b := machines(t)
	testutil.WriteFile(t, filepath.Join(a.Repo, "zsh", ".zshrc"), "a edit\n")
	require.NoError(t, a.Commit(ctx, "a", []string{"zsh/.zshrc"}))
	require.NoError(t, a.Push(ctx))

	// an untracked file does not stop the pull
	testutil.WriteFile(t, filepath.Join(b.Repo, "scratch.txt"), "new\n")
	require.NoError(t, b.Pull(ctx))

	// a tracked file left out of the sync commit does
	testutil.WriteFile(t, filepath.Join(b.Repo, "zsh", ".zshrc"), "b edit, not committed\n")
	err := b.Pull(ctx)
	require.ErrorIs(t, err, ErrUncommitted)
	var uncommitted *UncommittedError
	require.ErrorAs(t, err, &uncommitted)
	assert.Equal(t, []string{"zsh/.zshrc"}, uncommitted.Files)
	assert.Contains(t, err.Error(), "1 local change(s) were not committed (zsh/.zshrc)")
	assert.NotContains(t, err.Error(), "cannot pull with rebase", "not git's own message")
	assert.Equal(t, "b edit, not committed\n", testutil.ReadFile(t, filepath.Join(b.Repo, "zsh", ".zshrc")), "the edit is untouched")
}

func TestGateRefusesCredentials(t *testing.T) {
	ctx := context.Background()
	// fake tokens, split so secret scanners do not flag the source
	tests := map[string]string{
		"misc/gh/hosts.yml":        "github.com:\n    oauth_token: gho_" + "abcdefghijklmnopqrstuvwxyz0123456789\n",
		"claude/.credentials.json": "{}\n",
		"tool/config":              "token = \"sk-" + "abcdefghijklmnopqrstuvwxyz012345\"\n",
		"ralphex/config":           "notify_telegram_token = 1234567890:" + "AAabcdefghijklmnopqrstuvwxyz0123456\n",
	}
	for path, body := range tests {
		t.Run(path, func(t *testing.T) {
			a, _ := machines(t)
			testutil.WriteFile(t, filepath.Join(a.Repo, path), body)
			before := git(t, a.Repo, "rev-parse", "HEAD")
			err := a.Commit(ctx, "a", []string{path})
			require.ErrorIs(t, err, ErrLeak)
			assert.Equal(t, before, git(t, a.Repo, "rev-parse", "HEAD"), "nothing committed")
			assert.Empty(t, strings.TrimSpace(git(t, a.Repo, "diff", "--cached", "--name-only")), "unstaged again")
		})
	}
}

func TestCommitNothing(t *testing.T) {
	a, _ := machines(t)
	require.NoError(t, a.Commit(context.Background(), "a", nil))
	assert.Equal(t, 0, a.Ahead(context.Background()))
}

// caskroom fakes `brew --caskroom` with an install receipt naming each cask's tap.
func caskroom(t *testing.T, taps map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for token, tap := range taps {
		testutil.WriteFile(t, filepath.Join(dir, token, ".metadata", "INSTALL_RECEIPT.json"), `{"source":{"tap":"`+tap+`","path":"x"}}`)
	}
	return dir
}

func TestStrays(t *testing.T) {
	room := caskroom(t, map[string]string{"goreleaser": "goreleaser/tap", "steam": "homebrew/cask"})
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		switch c.String() {
		case "brew leaves --installed-on-request":
			return sh.Result{Output: "\x1b[32m==>\x1b[0m \x1b[1mDownloading Homebrew API data\x1b[0m\ngit\nhttpie\nowner/apps/tool\nlocal-only\ngcc@14\n"}, nil
		case "brew list --cask -1":
			return sh.Result{Output: "slack\nsteam\ngoreleaser\nignored-tap-cask\n"}, nil
		case "brew --caskroom":
			return sh.Result{Output: room + "\n"}, nil
		case "brew info --json=v2 --cask goreleaser/tap/goreleaser steam":
			return sh.Result{Output: `{"formulae":[],"casks":[{"token":"goreleaser","full_token":"goreleaser/tap/goreleaser","desc":"Release engineering, simplified"},{"token":"steam","full_token":"steam","desc":"Video game platform"}]}`}, nil
		case "brew info --json=v2 --formula gcc@14 httpie":
			return sh.Result{Output: `{"formulae":[{"name":"httpie","full_name":"httpie","desc":"User-friendly cURL replacement"}],"casks":[]}`}, nil
		}
		t.Fatalf("unexpected %s", c)
		return sh.Result{}, nil
	}).Times(5)

	brewfiles := []action.Brew{{Packages: action.ParseBrewfile([]byte("brew \"git\"\nbrew \"owner/apps/tool\"\ncask \"slack\"\n"))}}
	strays, err := Strays(context.Background(), r, brewfiles, []string{"local-only", "other/tap/ignored-tap-cask"})
	require.NoError(t, err)
	assert.Equal(t, []Stray{
		{Kind: "brew", Name: "gcc@14"},
		{Kind: "brew", Name: "httpie", Desc: "User-friendly cURL replacement"},
		// a tap cask keeps its tap, so a fresh machine can install the Brewfile line
		{Kind: "cask", Name: "goreleaser/tap/goreleaser", Desc: "Release engineering, simplified"},
		{Kind: "cask", Name: "steam", Desc: "Video game platform"},
	}, strays)
}

func TestStraysWithoutBrewInfo(t *testing.T) {
	room := caskroom(t, map[string]string{"agr": "k0nsta/apps"})
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		switch c.String() {
		case "brew leaves --installed-on-request":
			return sh.Result{}, nil
		case "brew list --cask -1":
			return sh.Result{Output: "agr\nsteam\n"}, nil
		case "brew --caskroom":
			return sh.Result{Output: room + "\n"}, nil
		case "brew info --json=v2 --cask k0nsta/apps/agr steam":
			// Homebrew 7 refuses a cask of an untrusted tap, which fails the whole call
			return sh.Result{Code: 1, Output: "Error: Refusing to load cask k0nsta/apps/agr from untrusted tap k0nsta/apps."}, nil
		}
		t.Fatalf("unexpected %s", c)
		return sh.Result{}, nil
	}).Times(4)

	strays, err := Strays(context.Background(), r, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []Stray{
		{Kind: "cask", Name: "k0nsta/apps/agr"}, // the tap comes from the receipt, not brew info
		{Kind: "cask", Name: "steam"},           // no receipt: the token as listed
	}, strays, "a failing brew info only loses the descriptions")
}

func TestAddToBrewfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.Brewfile")
	testutil.WriteFile(t, path, "brew \"git\"")
	require.NoError(t, AddToBrewfile(path, Stray{Kind: "cask", Name: "steam"}))
	require.NoError(t, AddToBrewfile(path, Stray{Kind: "brew", Name: "httpie", Desc: "User-friendly cURL replacement"}))
	assert.Equal(t, "brew \"git\"\ncask \"steam\"\n# User-friendly cURL replacement\nbrew \"httpie\"\n", testutil.ReadFile(t, path))
	assert.Equal(t, "User-friendly cURL replacement", action.ParseBrewfile([]byte(testutil.ReadFile(t, path)))[2].Comment, "the TUI shows it")
}

// recorder runs commands for real and keeps them for assertions.
type recorder struct {
	Runner
	cmds []sh.Cmd
}

func (r *recorder) Run(ctx context.Context, c sh.Cmd) (sh.Result, error) {
	r.cmds = append(r.cmds, c)
	return r.Runner.Run(ctx, c)
}

// noSSHCommand clears an inherited GIT_SSH_COMMAND for the test (sandboxes set one).
func noSSHCommand(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_SSH_COMMAND", "")
	require.NoError(t, os.Unsetenv("GIT_SSH_COMMAND"))
}

func TestPullRunsSSHInBatchMode(t *testing.T) {
	noSSHCommand(t)
	a, _ := machines(t)
	rec := &recorder{Runner: a.Runner}
	a.Runner = rec
	require.NoError(t, a.Pull(context.Background()))
	pull := rec.cmds[len(rec.cmds)-1]
	assert.Contains(t, pull.Args, "pull")
	assert.Equal(t, []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=15"}, pull.Env)
}

func TestPullWithoutAnswerTimesOut(t *testing.T) {
	noSSHCommand(t)
	a, _ := machines(t)
	// a remote whose ssh never answers; the configured sshCommand is kept, so it is what runs
	git(t, a.Repo, "remote", "set-url", "origin", "ssh://git@example.invalid/x.git")
	git(t, a.Repo, "config", "core.sshCommand", "sleep 30 #")
	a.NetTimeout = 300 * time.Millisecond

	start := time.Now()
	err := a.Pull(context.Background())
	require.ErrorIs(t, err, ErrNoAnswer)
	assert.Contains(t, err.Error(), "approve the SSH key")
	assert.Less(t, time.Since(start), 10*time.Second, "the ssh grandchild is killed too")
}

