// Package repo finds and clones the setup repo.
package repo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/k0nsta/rigger/internal/manifest"
)

// ErrNotFound means no setup repo was given, found from the working dir, or remembered.
var ErrNotFound = errors.New("no setup repo: run `rigger init <git-url>` or pass --repo")

// Discover picks the setup repo: the --repo flag, then $RIGGER_REPO, then cwd or a parent
// holding manifest.yaml, then the path remembered in local state.
func Discover(flag, env, cwd, remembered string) (string, error) {
	for _, candidate := range []string{flag, env} {
		if candidate != "" {
			return checkRepo(candidate)
		}
	}
	if cwd != "" {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			if hasManifest(dir) {
				return dir, nil
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if remembered != "" {
		return checkRepo(remembered)
	}
	return "", ErrNotFound
}

func checkRepo(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	if !hasManifest(abs) {
		return "", fmt.Errorf("%s has no %s", abs, manifest.FileName)
	}
	return abs, nil
}

func hasManifest(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, manifest.FileName))
	return err == nil && !info.IsDir()
}
