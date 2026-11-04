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
		want, elsewhere            string
	}{
		{"flag wins", flag, env, nested, remembered, flag, ""},
		{"env next", "", env, nested, remembered, env, ""},
		{"cwd parent without remembered", "", "", nested, "", cwdRepo, ""},
		{"remembered wins over another checkout", "", "", cwdRepo, remembered, remembered, cwdRepo},
		{"cwd repo after remembered repo moved", "", "", cwdRepo, filepath.Join(t.TempDir(), "gone"), cwdRepo, ""},
		{"remembered when cwd has no repo", "", "", elsewhere, remembered, remembered, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, skipped, err := Discover(tc.flag, tc.env, tc.cwd, tc.remembered)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.elsewhere, skipped)
		})
	}
}

func TestDiscoverSameCheckoutThroughSymlink(t *testing.T) {
	remembered := setupRepo(t)
	link := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(remembered, link))
	got, elsewhere, err := Discover("", "", link, remembered)
	require.NoError(t, err)
	assert.Equal(t, remembered, got)
	assert.Empty(t, elsewhere)
}

func TestDiscoverErrors(t *testing.T) {
	_, _, err := Discover("", "", t.TempDir(), "")
	require.ErrorIs(t, err, ErrNotFound)

	_, _, err = Discover(t.TempDir(), "", "", "")
	require.ErrorContains(t, err, "has no manifest.yaml")

	_, _, err = Discover("", "", "", filepath.Join(t.TempDir(), "gone"))
	require.ErrorContains(t, err, "moved it?")
}
