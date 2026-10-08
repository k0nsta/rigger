package action

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/testutil"
)

func TestLoadSecretsParsesAndTightens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	testutil.WriteFile(t, path, "# comment\n\nA=1\nB = \"two words\"\nC='x=y'\nEMPTY=\n")
	require.NoError(t, os.Chmod(path, 0o644))

	s, err := LoadSecrets(path, nil)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), testutil.Perm(t, path))

	ctx := context.Background()
	for key, want := range map[string]string{"A": "1", "B": "two words", "C": "x=y"} {
		got, err := s.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, want, got, key)
	}
	assert.False(t, s.Has("EMPTY"))
	assert.Equal(t, []string{"EMPTY", "Z"}, s.Missing([]string{"A", "Z", "EMPTY"}))
	_, err = s.Get(ctx, "Z")
	require.ErrorContains(t, err, "secret Z is not set")
}

func TestLoadSecretsBadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	testutil.WriteFile(t, path, "A=1\nnot a pair\n")
	_, err := LoadSecrets(path, nil)
	require.ErrorContains(t, err, ":2: expected KEY=value")
}

func TestSecretsSetWrites0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rigger", "secrets.env")
	s, err := LoadSecrets(path, nil)
	require.NoError(t, err)
	require.NoError(t, s.Set("B", "2"))
	require.NoError(t, s.Set("A", "1"))
	assert.Equal(t, os.FileMode(0o600), testutil.Perm(t, path))
	assert.Contains(t, testutil.ReadFile(t, path), "A=1\nB=2\n")

	again, err := LoadSecrets(path, nil)
	require.NoError(t, err)
	assert.True(t, again.Has("A"))
}

func TestSecretsOnePassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	testutil.WriteFile(t, path, "TOKEN=op://vault/item/field\n")
	calls := 0
	s, err := LoadSecrets(path, func(_ context.Context, ref string) (string, error) {
		calls++
		assert.Equal(t, "op://vault/item/field", ref)
		return "resolved\n", nil
	})
	require.NoError(t, err)

	ctx := context.Background()
	for range 2 {
		v, err := s.Get(ctx, "TOKEN")
		require.NoError(t, err)
		assert.Equal(t, "resolved", v)
	}
	assert.Equal(t, 1, calls, "resolved once")

	env, err := s.Env(ctx, []string{"TOKEN"})
	require.NoError(t, err)
	assert.Equal(t, []string{"TOKEN=resolved"}, env)
}

func TestSecretsOnePasswordErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	testutil.WriteFile(t, path, "TOKEN=op://v/i/f\n")

	noOp, err := LoadSecrets(path, nil)
	require.NoError(t, err)
	_, err = noOp.Get(context.Background(), "TOKEN")
	require.ErrorContains(t, err, "op is not available")

	failing, err := LoadSecrets(path, func(context.Context, string) (string, error) { return "", errors.New("locked") })
	require.NoError(t, err)
	_, err = failing.Get(context.Background(), "TOKEN")
	require.ErrorContains(t, err, "resolve TOKEN: locked")
}
