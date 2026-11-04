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

// Discover picks the setup repo: the --repo flag, then $RIGGER_REPO, then the remembered repo
// when cwd is another checkout, then cwd or a parent holding manifest.yaml.
// Elsewhere is the cwd checkout skipped in favor of the remembered repo.
func Discover(flag, env, cwd, remembered string) (dir, elsewhere string, err error) {
	for _, candidate := range []string{flag, env} {
		if candidate != "" {
			dir, err = checkRepo(candidate)
			return dir, "", err
		}
	}
	cwdRepo := ""
	if cwd != "" {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			if hasManifest(dir) {
				cwdRepo, err = filepath.Abs(dir)
				if err != nil {
					return "", "", fmt.Errorf("resolve %s: %w", dir, err)
				}
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if remembered != "" {
		if !hasManifest(remembered) {
			if cwdRepo != "" {
				return cwdRepo, "", nil
			}
			return "", "", fmt.Errorf("the setup repo remembered at %s is gone; moved it? run rigger once from its new directory or with --repo DIR", remembered)
		}
		rememberedRepo, err := checkRepo(remembered)
		if err != nil {
			return "", "", err
		}
		if cwdRepo != "" && !samePath(rememberedRepo, cwdRepo) {
			return rememberedRepo, cwdRepo, nil
		}
		return rememberedRepo, "", nil
	}
	if cwdRepo != "" {
		return cwdRepo, "", nil
	}
	return "", "", ErrNotFound
}

func samePath(a, b string) bool {
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return filepath.Clean(resolvedA) == filepath.Clean(resolvedB)
	}
	return filepath.Clean(a) == filepath.Clean(b)
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
