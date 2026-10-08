// Package testutil holds helpers shared by rigger's tests.
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// WriteFile writes content to path, creating parent dirs.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// ReadFile returns the content of path.
func ReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// Readlink returns the target of the symlink at path.
func Readlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	require.NoError(t, err)
	return target
}

// Tree writes files (relative path -> content) under a new temp dir and returns it.
func Tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		WriteFile(t, filepath.Join(dir, rel), body)
	}
	return dir
}

// Perm returns the permission bits of path.
func Perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Mode().Perm()
}
