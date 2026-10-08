package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	assert.Equal(t, "/h/.config/rigger", ConfigDir("/h"))
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	assert.Equal(t, "/xdg/rigger", ConfigDir("/h"))
}

func TestLocalRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rigger", "state.yaml")

	empty, err := LoadLocal(path)
	require.NoError(t, err)
	assert.Empty(t, empty.Repo)
	assert.NotNil(t, empty.Seeds)

	l := &Local{Repo: "/r", Host: "mac", Seeds: map[string]string{"/h/.x": "abc"}}
	require.NoError(t, l.Save(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := LoadLocal(path)
	require.NoError(t, err)
	assert.Equal(t, l, got)
}

func TestHostRoundTrip(t *testing.T) {
	repo := t.TempDir()
	path := HostPath(repo, "laptop")
	assert.Equal(t, filepath.Join(repo, "hosts", "laptop.yaml"), path)

	missing, err := LoadHost(path)
	require.NoError(t, err)
	assert.Nil(t, missing)

	h := &HostFile{Profile: "personal", Components: []string{"a", "b"}, ExcludePackages: []string{"x"}}
	require.NoError(t, h.Save(path))
	got, err := LoadHost(path)
	require.NoError(t, err)
	assert.Equal(t, h, got)
}

func TestLoadBadYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("repo: [unclosed"), 0o600))
	_, err := LoadLocal(path)
	require.ErrorContains(t, err, "parse")
	_, err = LoadHost(path)
	require.ErrorContains(t, err, "parse")
}

func TestWriteFileAtomicReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	require.NoError(t, WriteFileAtomic(path, []byte("one"), 0o644))
	require.NoError(t, WriteFileAtomic(path, []byte("two"), 0o644))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "two", string(data))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp files left behind")
}
