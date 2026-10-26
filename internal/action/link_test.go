package action

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/testutil"
)

// testEnv is a temp HOME and repo with a fixed backup timestamp.
func testEnv(t *testing.T, repoFiles map[string]string) *Env {
	t.Helper()
	return &Env{
		Home:       t.TempDir(),
		Repo:       testutil.Tree(t, repoFiles),
		BackupRoot: filepath.Join(t.TempDir(), "backups"),
		Now:        func() time.Time { return time.Date(2026, 10, 8, 1, 2, 3, 0, time.UTC) },
	}
}

func TestLinkProbeStatuses(t *testing.T) {
	env := testEnv(t, map[string]string{"zsh/.zshrc": "repo", "nvim/init.lua": "x"})
	file := NewLink(env, manifest.Link{Src: "zsh/.zshrc", Dst: "~/.zshrc"})
	dir := NewLink(env, manifest.Link{Src: "nvim", Dst: "~/.config/nvim"})

	probe := func(l Link) LinkProbe {
		t.Helper()
		p, err := l.Probe()
		require.NoError(t, err)
		return p
	}

	assert.Equal(t, Missing, probe(file).Status)

	testutil.WriteFile(t, file.Dst, "repo")
	assert.Equal(t, LinkProbe{Status: Conflict, Detail: "identical copy"}, probe(file))

	testutil.WriteFile(t, file.Dst, "live edit")
	assert.Equal(t, Differs, probe(file).Status)

	require.NoError(t, os.Remove(file.Dst))
	require.NoError(t, os.Symlink("/elsewhere", file.Dst))
	assert.Equal(t, LinkProbe{Status: Conflict, Detail: "links to /elsewhere"}, probe(file))

	require.NoError(t, os.Remove(file.Dst))
	require.NoError(t, os.Symlink(file.Src, file.Dst))
	assert.Equal(t, OK, probe(file).Status)

	testutil.WriteFile(t, filepath.Join(dir.Dst, "init.lua"), "x")
	assert.Equal(t, LinkProbe{Status: Conflict, Detail: "real directory"}, probe(dir))
}

func TestLinkProbeRelativeSymlink(t *testing.T) {
	env := testEnv(t, map[string]string{"a": "x"})
	l := NewLink(env, manifest.Link{Src: "a", Dst: "~/a"})
	rel, err := filepath.Rel(env.Home, l.Src)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(rel, l.Dst))
	p, err := l.Probe()
	require.NoError(t, err)
	assert.Equal(t, OK, p.Status)
}

func TestLinkProbeAfterRepoMove(t *testing.T) {
	env := testEnv(t, map[string]string{"zsh/.zshrc": "repo"})
	old := filepath.Join(t.TempDir(), "old")
	other := testutil.Tree(t, map[string]string{"zsh/.zshrc": "another clone"})

	cases := []struct {
		name     string
		prevRepo string
		target   string
		want     Status
	}{
		{"dangling link into a moved repo", "", filepath.Join(old, "zsh", ".zshrc"), Relink},
		{"link into the remembered old repo", other, filepath.Join(other, "zsh", ".zshrc"), Relink},
		{"link into another existing clone", "", filepath.Join(other, "zsh", ".zshrc"), Conflict},
		{"dangling link to another path", "", filepath.Join(old, "zsh", ".bashrc"), Conflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env.PrevRepo = tc.prevRepo
			l := NewLink(env, manifest.Link{Src: "zsh/.zshrc", Dst: "~/.zshrc"})
			_ = os.Remove(l.Dst)
			require.NoError(t, os.Symlink(tc.target, l.Dst))
			p, err := l.Probe()
			require.NoError(t, err)
			assert.Equal(t, tc.want, p.Status, p.Detail)
		})
	}
}

func TestLinkApplyRelinkSkipsBackup(t *testing.T) {
	env := testEnv(t, map[string]string{"zsh/.zshrc": "repo"})
	l := NewLink(env, manifest.Link{Src: "zsh/.zshrc", Dst: "~/.zshrc"})
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "gone", "zsh", ".zshrc"), l.Dst))

	backup, err := l.Apply(env, KeepRepo)
	require.NoError(t, err)
	assert.Empty(t, backup)
	assert.Equal(t, l.Src, testutil.Readlink(t, l.Dst))
	assert.NoDirExists(t, env.BackupRoot)
}

func TestLinkApplyKeepRepoBacksUp(t *testing.T) {
	env := testEnv(t, map[string]string{"git/.gitconfig": "repo"})
	l := NewLink(env, manifest.Link{Src: "git/.gitconfig", Dst: "~/.config/git/config"})
	testutil.WriteFile(t, l.Dst, "live")

	backup, err := l.Apply(env, KeepRepo)
	require.NoError(t, err)
	assert.Equal(t, l.Src, testutil.Readlink(t, l.Dst))
	assert.Equal(t, "repo", testutil.ReadFile(t, l.Src))
	assert.Equal(t, filepath.Join(env.BackupRoot, "20261008-010203", ".config", "git", "config"), backup)
	assert.Equal(t, "live", testutil.ReadFile(t, backup))

	// rerun is a no-op
	backup, err = l.Apply(env, KeepRepo)
	require.NoError(t, err)
	assert.Empty(t, backup)
	assert.Equal(t, l.Src, testutil.Readlink(t, l.Dst))
}

func TestLinkApplyMissingCreatesParents(t *testing.T) {
	env := testEnv(t, map[string]string{"tmux/tmux.conf": "x"})
	l := NewLink(env, manifest.Link{Src: "tmux/tmux.conf", Dst: "~/.config/tmux/tmux.conf"})
	_, err := l.Apply(env, KeepRepo)
	require.NoError(t, err)
	assert.Equal(t, l.Src, testutil.Readlink(t, l.Dst))
	_, err = os.Stat(env.BackupRoot)
	assert.True(t, os.IsNotExist(err), "nothing backed up")
}

func TestLinkApplyAdoptLive(t *testing.T) {
	env := testEnv(t, map[string]string{"zsh/.zshrc": "repo"})
	l := NewLink(env, manifest.Link{Src: "zsh/.zshrc", Dst: "~/.zshrc"})
	testutil.WriteFile(t, l.Dst, "live wins")

	_, err := l.Apply(env, AdoptLive)
	require.NoError(t, err)
	assert.Equal(t, "live wins", testutil.ReadFile(t, l.Src))
	assert.Equal(t, l.Src, testutil.Readlink(t, l.Dst))
}

func TestLinkApplyAdoptRefusesDirectories(t *testing.T) {
	env := testEnv(t, map[string]string{"nvim/init.lua": "repo"})
	l := NewLink(env, manifest.Link{Src: "nvim", Dst: "~/.config/nvim"})
	testutil.WriteFile(t, filepath.Join(l.Dst, "init.lua"), "live")

	_, err := l.Apply(env, AdoptLive)
	require.ErrorContains(t, err, "only real files that differ can be adopted")
	assert.Equal(t, "repo", testutil.ReadFile(t, filepath.Join(l.Src, "init.lua")))
}

func TestBackupOutsideHome(t *testing.T) {
	env := testEnv(t, nil)
	outside := filepath.Join(t.TempDir(), "etc", "thing")
	testutil.WriteFile(t, outside, "x")
	dst, err := env.Backup(outside)
	require.NoError(t, err)
	assert.Contains(t, dst, filepath.Join("20261008-010203", outside[1:]))
}
