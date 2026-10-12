package syncer

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	assert.Equal(t, "b edit\n", testutil.ReadFile(t, filepath.Join(b.Repo, "zsh", ".zshrc")), "no conflict markers left")
	assert.NoDirExists(t, filepath.Join(b.Repo, ".git", "rebase-merge"))
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

func TestStrays(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		switch c.String() {
		case "brew leaves --installed-on-request":
			return sh.Result{Output: "git\nhttpie\nowner/apps/tool\nlocal-only\n"}, nil
		case "brew list --cask -1":
			return sh.Result{Output: "slack\nsteam\n"}, nil
		}
		t.Fatalf("unexpected %s", c)
		return sh.Result{}, nil
	}).Times(2)

	brewfiles := []action.Brew{{Packages: action.ParseBrewfile([]byte("brew \"git\"\nbrew \"owner/apps/tool\"\ncask \"slack\"\n"))}}
	strays, err := Strays(context.Background(), r, brewfiles, []string{"local-only"})
	require.NoError(t, err)
	assert.Equal(t, []Stray{{Kind: "brew", Name: "httpie"}, {Kind: "cask", Name: "steam"}}, strays)
}

func TestAddToBrewfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.Brewfile")
	testutil.WriteFile(t, path, "brew \"git\"")
	require.NoError(t, AddToBrewfile(path, Stray{Kind: "cask", Name: "steam"}))
	assert.Equal(t, "brew \"git\"\ncask \"steam\"\n", testutil.ReadFile(t, path))
}
