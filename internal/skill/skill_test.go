package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedSkillFrontmatterAndMarker(t *testing.T) {
	assert.True(t, strings.HasPrefix(string(data), "---\nname: rigger\ndescription: "))
	assert.Contains(t, string(data), "\n"+Marker+"\n")
}

func TestPaths(t *testing.T) {
	home := t.TempDir()
	assert.Equal(t, []string{
		filepath.Join(home, ".claude", "skills", "rigger", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "rigger", "SKILL.md"),
	}, Paths(home))
}

func TestInstallWritesBothAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	paths, err := Install(home, false)
	require.NoError(t, err)
	assert.Equal(t, Paths(home), paths)
	for _, path := range paths {
		contents, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		assert.Equal(t, string(data), string(contents))
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	}
	_, err = Install(home, false)
	require.NoError(t, err)
}

func TestInstallReplacesOlderMarkedSkill(t *testing.T) {
	home := t.TempDir()
	path := Paths(home)[0]
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("old\n"+Marker+"\n"), 0o644))
	_, err := Install(home, false)
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(contents))
}

func TestInstallRefusesForeignFileWithoutWritingEitherPath(t *testing.T) {
	home := t.TempDir()
	paths := Paths(home)
	foreign := []byte("my own skill\n")
	require.NoError(t, os.MkdirAll(filepath.Dir(paths[1]), 0o755))
	require.NoError(t, os.WriteFile(paths[1], foreign, 0o644))

	got, err := Install(home, false)
	var foreignErr *ForeignError
	require.ErrorAs(t, err, &foreignErr)
	assert.Equal(t, paths[1], foreignErr.Path)
	assert.Nil(t, got)
	_, err = os.Stat(paths[0])
	assert.ErrorIs(t, err, os.ErrNotExist)
	contents, err := os.ReadFile(paths[1])
	require.NoError(t, err)
	assert.Equal(t, string(foreign), string(contents))
}

func TestInstallForceReplacesForeignFile(t *testing.T) {
	home := t.TempDir()
	path := Paths(home)[0]
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("foreign"), 0o644))
	_, err := Install(home, true)
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(contents))
}

func TestInstallRequiresAbsoluteHome(t *testing.T) {
	for _, home := range []string{"", "relative/home"} {
		t.Run(home, func(t *testing.T) {
			paths, err := Install(home, false)
			require.Error(t, err)
			assert.Nil(t, paths)
		})
	}
}

func TestInstallFollowsDirectorySymlink(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	claude := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	require.NoError(t, os.Symlink(repo, claude))

	paths, err := Install(home, false)
	require.NoError(t, err)
	contents, err := os.ReadFile(filepath.Join(repo, "skills", "rigger", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, string(data), string(contents))
	assert.Equal(t, filepath.Join(claude, "skills", "rigger", "SKILL.md"), paths[0])
}

func TestInstallFollowsSkillFileSymlinkAndKeepsLink(t *testing.T) {
	home := t.TempDir()
	path := Paths(home)[0]
	target := filepath.Join(t.TempDir(), "tracked-skill.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("old\n"+Marker+"\n"), 0o644))
	require.NoError(t, os.Symlink(target, path))

	_, err := Install(home, false)
	require.NoError(t, err)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	contents, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(contents))
}
