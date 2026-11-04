// Package skill installs the Agent Skills instructions for working in a rigger setup repo.
package skill

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/k0nsta/rigger/internal/state"
)

//go:embed SKILL.md
var data []byte

// Marker identifies an installed copy that rigger may replace.
const Marker = "<!-- rigger: skill -->"

// Paths returns the skill file locations used by Claude Code and Codex.
func Paths(home string) []string {
	return []string{
		filepath.Join(home, ".claude", "skills", "rigger", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "rigger", "SKILL.md"),
	}
}

// ForeignError reports an existing file that is not marked as rigger's skill.
type ForeignError struct{ Path string }

func (e *ForeignError) Error() string {
	return fmt.Sprintf("%s: differs from the skill and was not written by rigger", e.Path)
}

// Install writes the skill to both agent paths under home and returns those paths.
// Every path is checked before writing, so a foreign file leaves both untouched.
func Install(home string, force bool) ([]string, error) {
	if home == "" || !filepath.IsAbs(home) {
		return nil, fmt.Errorf("HOME %q is not an absolute path", home)
	}

	paths := Paths(home)
	if !force {
		for _, path := range paths {
			if err := checkOwn(path); err != nil {
				return nil, err
			}
		}
	}
	for _, path := range paths {
		target, err := resolveTarget(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", path, err)
		}
		if err := state.WriteFileAtomic(target, data, 0o644); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func checkOwn(path string) error {
	old, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case string(old) == string(data) || hasMarker(old):
		return nil
	default:
		return &ForeignError{Path: path}
	}
}

func hasMarker(contents []byte) bool {
	for _, line := range strings.Split(string(contents), "\n") {
		if strings.TrimSpace(strings.TrimSuffix(line, "\r")) == Marker {
			return true
		}
	}
	return false
}

func resolveTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	return filepath.EvalSymlinks(path)
}
