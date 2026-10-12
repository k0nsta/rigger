package repo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRepo makes a dir with an empty manifest.yaml.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("version: 1\n"), 0o644))
	return dir
}

func TestDiscoverPrecedence(t *testing.T) {
	flag, env, cwdRepo, remembered := setupRepo(t), setupRepo(t), setupRepo(t), setupRepo(t)
	nested := filepath.Join(cwdRepo, "zsh", "deep")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	elsewhere := t.TempDir()

	tests := []struct {
		name                       string
		flag, env, cwd, remembered string
		want                       string
	}{
		{"flag wins", flag, env, nested, remembered, flag},
		{"env next", "", env, nested, remembered, env},
		{"cwd parent", "", "", nested, remembered, cwdRepo},
		{"remembered last", "", "", elsewhere, remembered, remembered},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Discover(tc.flag, tc.env, tc.cwd, tc.remembered)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDiscoverErrors(t *testing.T) {
	_, err := Discover("", "", t.TempDir(), "")
	require.ErrorIs(t, err, ErrNotFound)

	_, err = Discover(t.TempDir(), "", "", "")
	require.ErrorContains(t, err, "has no manifest.yaml")

	_, err = Discover("", "", "", filepath.Join(t.TempDir(), "gone"))
	require.ErrorContains(t, err, "moved it?")
}
