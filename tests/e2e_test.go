// Package tests drives the built rigger binary end to end against temp HOMEs and repos.
package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/state"
	"github.com/k0nsta/rigger/internal/testutil"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rigger-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "rigger")
	build := exec.Command("go", "build", "-o", binary, "../cmd/rigger")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build rigger: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// machine is a fake HOME with its own config dir and git identity.
type machine struct {
	t    *testing.T
	home string
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	return &machine{t: t, home: t.TempDir()}
}

func (m *machine) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "XDG_CONFIG_HOME=") && !strings.HasPrefix(kv, "RIGGER_") {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+m.home,
		"GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.com",
		"GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null")
}

// rigger runs the binary and returns combined output; wantOK fails the test on a non-zero exit.
func (m *machine) rigger(wantOK bool, args ...string) string {
	m.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = m.env()
	cmd.Dir = m.home
	out, err := cmd.CombinedOutput()
	if wantOK {
		require.NoError(m.t, err, "rigger %v:\n%s", args, out)
	}
	return string(out)
}

func (m *machine) secret(key, value string) {
	m.t.Helper()
	path := filepath.Join(m.home, ".config", "rigger", "secrets.env")
	testutil.WriteFile(m.t, path, key+"="+value+"\n")
	require.NoError(m.t, os.Chmod(path, 0o600))
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.com", "GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// remoteFromExample publishes a copy of example/ as a bare repo and returns its path.
func remoteFromExample(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	require.NoError(t, os.CopyFS(work, os.DirFS("../example")))
	gitIn(t, work, "init", "-q", "-b", "main")
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-q", "-m", "init")
	bare := filepath.Join(t.TempDir(), "setup.git")
	gitIn(t, "", "clone", "-q", "--bare", work, bare)
	return bare
}

func TestApplyAndRerunIsNoop(t *testing.T) {
	m := newMachine(t)
	repo := filepath.Join(m.home, "setup")
	m.rigger(true, "init", remoteFromExample(t), "--dir", repo, "--no-run")
	m.secret("TOOL_TOKEN", "e2e-token")
	testutil.WriteFile(t, filepath.Join(m.home, ".gitconfig"), "[user]\n  name = before rigger\n")

	out := m.rigger(true, "apply", "--yes", "--only", "git,tool-config", "--host", "box")
	assert.Contains(t, out, "done: 2 component(s)")
	logs, err := filepath.Glob(filepath.Join(m.home, ".local", "state", "rigger", "logs", "*.log"))
	require.NoError(t, err)
	require.Len(t, logs, 1, "apply keeps a run log")
	assert.Contains(t, testutil.ReadFile(t, logs[0]), "==> git")

	assert.Equal(t, filepath.Join(repo, "git", ".gitconfig"), testutil.Readlink(t, filepath.Join(m.home, ".gitconfig")))
	backups, err := filepath.Glob(filepath.Join(m.home, ".local", "state", "rigger", "backups", "*", ".gitconfig"))
	require.NoError(t, err)
	require.Len(t, backups, 1)
	assert.Contains(t, testutil.ReadFile(t, backups[0]), "before rigger")

	seed := filepath.Join(m.home, ".config", "tool", "config.toml")
	assert.Contains(t, testutil.ReadFile(t, seed), `token = "e2e-token"`)
	assert.Equal(t, os.FileMode(0o600), testutil.Perm(t, seed))
	assert.Contains(t, testutil.ReadFile(t, filepath.Join(repo, "hosts", "box.yaml")), "- tool-config")

	// the repo is remembered and the host file holds the selection
	out = m.rigger(true, "apply", "--yes", "--host", "box")
	assert.Contains(t, out, "nothing to do")
	out = m.rigger(true, "status", "--host", "box")
	assert.NotContains(t, out, "missing")
}

func TestApplyOnlyKeepsHostSelection(t *testing.T) {
	m := newMachine(t)
	repo := filepath.Join(m.home, "setup")
	m.rigger(true, "init", remoteFromExample(t), "--dir", repo, "--no-run")
	m.secret("TOOL_TOKEN", "e2e-token")
	m.rigger(true, "apply", "--yes", "--only", "git,tool-config", "--host", "box")
	host := filepath.Join(repo, "hosts", "box.yaml")
	before := testutil.ReadFile(t, host)

	// a one-off --only run must not shrink what this machine has picked
	m.rigger(true, "apply", "--yes", "--only", "git", "--host", "box")
	assert.Equal(t, before, testutil.ReadFile(t, host))
}

func TestRepoMoveRelinksWithoutBackups(t *testing.T) {
	m := newMachine(t)
	repo := filepath.Join(m.home, "setup")
	m.rigger(true, "init", remoteFromExample(t), "--dir", repo, "--no-run")
	m.rigger(true, "apply", "--yes", "--only", "git", "--host", "box")

	moved := filepath.Join(m.home, "Dev", "setup")
	require.NoError(t, os.MkdirAll(filepath.Dir(moved), 0o755))
	require.NoError(t, os.Rename(repo, moved))

	// the remembered path is gone: say so instead of a bare "no manifest.yaml"
	out := m.rigger(false, "status")
	assert.Contains(t, out, "moved it?")

	out = m.rigger(false, "status", "--repo", moved)
	assert.Contains(t, out, "relink")

	m.rigger(true, "apply", "--yes", "--repo", moved)
	assert.Equal(t, filepath.Join(moved, "git", ".gitconfig"), testutil.Readlink(t, filepath.Join(m.home, ".gitconfig")))
	backups, err := filepath.Glob(filepath.Join(m.home, ".local", "state", "rigger", "backups", "*"))
	require.NoError(t, err)
	assert.Empty(t, backups, "a stale link into the old repo is replaced, not backed up")

	// the new location is remembered
	out = m.rigger(true, "apply", "--yes")
	assert.Contains(t, out, "nothing to do")
}

func TestWorktreeKeepsLiveLinks(t *testing.T) {
	m := newMachine(t)
	repo := filepath.Join(m.home, "setup")
	m.rigger(true, "init", remoteFromExample(t), "--dir", repo, "--no-run")
	m.rigger(true, "apply", "--yes", "--only", "git", "--host", "box")
	link := filepath.Join(m.home, ".gitconfig")
	liveTarget := filepath.Join(repo, "git", ".gitconfig")
	assert.Equal(t, liveTarget, testutil.Readlink(t, link))

	worktree := filepath.Join(m.home, "setup-feature")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feature", worktree)
	cmd := exec.Command(binary, "apply", "--yes")
	cmd.Dir = worktree
	cmd.Env = m.env()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "rigger apply from worktree: %s", out)
	assert.Equal(t, liveTarget, testutil.Readlink(t, link))
	// the binary ran without XDG_CONFIG_HOME (m.env drops it); read the state where it wrote it,
	// not where this process's XDG_CONFIG_HOME (set on CI runners) points
	t.Setenv("XDG_CONFIG_HOME", "")
	local, err := state.LoadLocal(state.LocalPath(m.home))
	require.NoError(t, err)
	assert.Equal(t, repo, local.Repo)
	assert.Equal(t, 1, strings.Count(string(out), "using the setup repo at ~/setup (this is another checkout; pass --repo to use it)"))
}

func TestSkillInstallWritesBothFiles(t *testing.T) {
	m := newMachine(t)
	m.rigger(true, "init", remoteFromExample(t), "--dir", filepath.Join(m.home, "setup"), "--no-run")

	m.rigger(true, "skill", "install")
	assert.FileExists(t, filepath.Join(m.home, ".claude", "skills", "rigger", "SKILL.md"))
	assert.FileExists(t, filepath.Join(m.home, ".agents", "skills", "rigger", "SKILL.md"))
}

func TestSyncRoundTripBetweenMachines(t *testing.T) {
	remote := remoteFromExample(t)
	a, b := newMachine(t), newMachine(t)
	repoA, repoB := filepath.Join(a.home, "setup"), filepath.Join(b.home, "setup")
	for _, m := range []*machine{a, b} {
		m.secret("TOOL_TOKEN", "tok")
	}

	a.rigger(true, "init", remote, "--dir", repoA, "--no-run")
	a.rigger(true, "apply", "--yes", "--only", "git,tool-config", "--host", "a")
	out := a.rigger(true, "sync", "--yes", "--host", "a")
	assert.Contains(t, out, "hosts/a.yaml")
	assert.Contains(t, out, "in sync")

	b.rigger(true, "init", remote, "--dir", repoB, "--no-run")
	assert.FileExists(t, filepath.Join(repoB, "hosts", "a.yaml"), "B sees A's host file")
	b.rigger(true, "apply", "--yes", "--only", "tool-config", "--host", "b")
	b.rigger(true, "sync", "--yes", "--host", "b")

	// A changes the template; B's untouched seed follows on its next sync
	testutil.WriteFile(t, filepath.Join(repoA, "tool", "config.toml"), "token = \"${TOOL_TOKEN}\"\ntheme = \"light\"\n")
	a.rigger(true, "sync", "--yes", "--host", "a")
	out = b.rigger(true, "sync", "--yes", "--host", "b")
	assert.Contains(t, out, "update")
	assert.Contains(t, testutil.ReadFile(t, filepath.Join(b.home, ".config", "tool", "config.toml")), `theme = "light"`)

	// a credential is refused by the sync gate (a fake token, split so secret scanners do not flag the source)
	testutil.WriteFile(t, filepath.Join(repoA, "git", ".gitconfig"), "[credential]\n  token = ghp_"+"abcdefghijklmnopqrstuvwxyz0123456789\n")
	out = a.rigger(false, "sync", "--yes", "--host", "a")
	assert.Contains(t, out, "possible credential staged")
	log := gitIn(t, repoA, "log", "-1", "--format=%s")
	assert.NotContains(t, log, "git/.gitconfig")
}

func TestInstallScript(t *testing.T) {
	if _, err := exec.LookPath("shasum"); err != nil {
		if _, err := exec.LookPath("sha256sum"); err != nil {
			t.Skip("no sha256 tool")
		}
	}
	// a fake release: tarball + checksums served by a fake curl
	release := t.TempDir()
	osName := runtime.GOOS
	asset := fmt.Sprintf("rigger_9.9.9_%s_%s.tar.gz", osName, runtime.GOARCH)
	testutil.WriteFile(t, filepath.Join(release, "rigger"), "#!/bin/sh\necho fake rigger\n")
	require.NoError(t, os.Chmod(filepath.Join(release, "rigger"), 0o755))
	tarCmd := exec.Command("tar", "-czf", asset, "rigger")
	tarCmd.Dir = release
	require.NoError(t, tarCmd.Run())
	sum := exec.Command("sh", "-c", "(sha256sum "+asset+" 2>/dev/null || shasum -a 256 "+asset+") > checksums.txt")
	sum.Dir = release
	require.NoError(t, sum.Run())

	bin := t.TempDir()
	fakeCurl := `#!/bin/sh
out=""; url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
case "$url" in
  */releases/latest) printf '{"tag_name": "v9.9.9"}' ;;
  *) cp "` + release + `/$(basename "$url")" "$out" ;;
esac
`
	testutil.WriteFile(t, filepath.Join(bin, "curl"), fakeCurl)
	require.NoError(t, os.Chmod(filepath.Join(bin, "curl"), 0o755))

	dest := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", "../install.sh")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RIGGER_DEST="+dest)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "installed rigger 9.9.9")
	assert.Contains(t, string(out), "next: rigger init")
	res, err := exec.Command(filepath.Join(dest, "rigger")).Output()
	require.NoError(t, err)
	assert.Equal(t, "fake rigger\n", string(res))

	// a tampered tarball fails the checksum
	testutil.WriteFile(t, filepath.Join(release, "checksums.txt"), "0000000000000000000000000000000000000000000000000000000000000000  "+asset+"\n")
	cmd = exec.Command("sh", "../install.sh")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RIGGER_DEST="+t.TempDir())
	out, err = cmd.CombinedOutput()
	require.Error(t, err, string(out))
}

func TestSyncRefusesFeatureBranch(t *testing.T) {
	remote := remoteFromExample(t)
	m := newMachine(t)
	repoDir := filepath.Join(m.home, "setup")
	m.rigger(true, "init", remote, "--dir", repoDir, "--no-run")
	m.rigger(true, "apply", "--yes", "--only", "git", "--host", "a")

	// another session switched the live checkout to its feature branch and left work in it
	gitIn(t, repoDir, "switch", "-q", "-c", "feat/plugin")
	testutil.WriteFile(t, filepath.Join(repoDir, "zsh", ".zshrc"), "# half done\n")
	before := gitIn(t, repoDir, "rev-parse", "HEAD")

	out := m.rigger(false, "sync", "--yes", "--host", "a")
	assert.Contains(t, out, "setup is on branch feat/plugin, not main: your linked configs come from that branch, and sync would commit to it.")
	assert.Contains(t, out, "switch back with `git -C ~/setup switch main`")
	assert.Equal(t, before, gitIn(t, repoDir, "rev-parse", "HEAD"), "nothing committed")

	out = m.rigger(false, "status", "--host", "a")
	assert.Contains(t, out, "warning: setup is on branch feat/plugin")

	gitIn(t, repoDir, "switch", "-q", "main")
	assert.Contains(t, m.rigger(true, "sync", "--yes", "--host", "a"), "in sync")
}

func TestSyncLeavesALinkReplacedByARealFile(t *testing.T) {
	remote := remoteFromExample(t)
	m := newMachine(t)
	repoDir := filepath.Join(m.home, "setup")
	m.rigger(true, "init", remote, "--dir", repoDir, "--no-run")
	m.rigger(true, "apply", "--yes", "--only", "git", "--host", "a")

	// sed -i or an editor's atomic save replaces the link with a real file
	live := filepath.Join(m.home, ".gitconfig")
	require.NoError(t, os.Remove(live))
	testutil.WriteFile(t, live, "[user]\n  name = edited in place\n")

	out := m.rigger(true, "sync", "--yes", "--host", "a")
	assert.Contains(t, out, "warning: ~/.gitconfig is a real file now, with other content than the repo's; left as it is.")
	assert.Contains(t, out, "rigger adopt ~/.gitconfig")
	assert.Contains(t, out, "left ~/.gitconfig as it is")
	assert.Equal(t, "[user]\n  name = edited in place\n", testutil.ReadFile(t, live), "the edit is not moved to a backup")
	assert.NoDirExists(t, filepath.Join(m.home, ".local", "state", "rigger", "backups"))

	// adopting it takes the edit into the repo and links it again
	m.rigger(true, "adopt", "~/.gitconfig", "--host", "a")
	assert.Equal(t, filepath.Join(repoDir, "git", ".gitconfig"), testutil.Readlink(t, live))
	assert.Contains(t, testutil.ReadFile(t, filepath.Join(repoDir, "git", ".gitconfig")), "edited in place")
}
