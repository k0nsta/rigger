package action

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/k0nsta/rigger/internal/manifest"
)

// Link symlinks Dst to the repo path Src.
type Link struct {
	Src string // absolute, inside the repo
	Dst string // absolute
	// Rel is Src relative to the repo; PrevRepo is where the repo lived before a move.
	Rel      string
	PrevRepo string
}

// NewLink resolves a manifest link against env.
func NewLink(env *Env, l manifest.Link) Link {
	return Link{Src: env.RepoPath(l.Src), Dst: env.Expand(l.Dst), Rel: filepath.FromSlash(l.Src), PrevRepo: env.PrevRepo}
}

// movedFrom reports the old repo root when target is this link's src in a repo that has
// moved: the same repo-relative path under the remembered old root, or under any root
// when target no longer exists.
func (l Link) movedFrom(target string) (string, bool) {
	if l.Rel == "" {
		return "", false
	}
	root, ok := strings.CutSuffix(filepath.Clean(target), string(filepath.Separator)+filepath.Clean(l.Rel))
	if !ok {
		return "", false
	}
	if l.PrevRepo != "" && root == filepath.Clean(l.PrevRepo) {
		return root, true
	}
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return root, true
	}
	return "", false
}

// LinkProbe is the probed state of a link.
type LinkProbe struct {
	Status Status
	Detail string
}

// Probe inspects Dst without changing anything.
func (l Link) Probe() (LinkProbe, error) {
	info, err := os.Lstat(l.Dst)
	if errors.Is(err, os.ErrNotExist) {
		return LinkProbe{Status: Missing}, nil
	}
	if err != nil {
		return LinkProbe{}, fmt.Errorf("stat %s: %w", l.Dst, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(l.Dst)
		if err != nil {
			return LinkProbe{}, fmt.Errorf("read link %s: %w", l.Dst, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(l.Dst), target)
		}
		if filepath.Clean(target) == filepath.Clean(l.Src) {
			return LinkProbe{Status: OK}, nil
		}
		if root, ok := l.movedFrom(target); ok {
			return LinkProbe{Status: Relink, Detail: "repo moved from " + root}, nil
		}
		return LinkProbe{Status: Conflict, Detail: "links to " + target}, nil
	}

	if info.IsDir() {
		return LinkProbe{Status: Conflict, Detail: "real directory"}, nil
	}

	srcInfo, err := os.Stat(l.Src)
	if err != nil {
		return LinkProbe{}, fmt.Errorf("stat %s: %w", l.Src, err)
	}
	if srcInfo.IsDir() {
		return LinkProbe{Status: Conflict, Detail: "file where the repo has a directory"}, nil
	}
	same, err := sameContent(l.Src, l.Dst)
	if err != nil {
		return LinkProbe{}, err
	}
	if same {
		return LinkProbe{Status: Conflict, Detail: "identical copy"}, nil
	}
	return LinkProbe{Status: Differs, Detail: "real file with other content"}, nil
}

// LinkMode decides what happens to a file at Dst that differs from the repo.
type LinkMode int

const (
	// KeepRepo backs the live file up and links the repo version.
	KeepRepo LinkMode = iota
	// AdoptLive copies the live file over the repo file first, then links it (files only).
	AdoptLive
)

// Apply makes Dst a link to Src, backing up whatever was there.
func (l Link) Apply(env *Env, mode LinkMode) error {
	probe, err := l.Probe()
	if err != nil {
		return err
	}
	switch probe.Status {
	case OK:
		return nil
	case Missing:
		// nothing to back up
	case Relink:
		if err := os.Remove(l.Dst); err != nil {
			return fmt.Errorf("remove stale link %s: %w", l.Dst, err)
		}
	default:
		if mode == AdoptLive {
			if probe.Status != Differs {
				return fmt.Errorf("adopt %s: only real files that differ can be adopted (%s)", l.Dst, probe.Status)
			}
			info, err := os.Stat(l.Src)
			if err != nil {
				return fmt.Errorf("stat %s: %w", l.Src, err)
			}
			if err := copyFile(l.Dst, l.Src, info.Mode().Perm()); err != nil {
				return err
			}
		}
		if _, err := env.Backup(l.Dst); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(l.Dst), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", l.Dst, err)
	}
	if err := os.Symlink(l.Src, l.Dst); err != nil {
		return fmt.Errorf("link %s: %w", l.Dst, err)
	}
	return nil
}

func sameContent(a, b string) (bool, error) {
	da, err := os.ReadFile(a)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", a, err)
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", b, err)
	}
	return bytes.Equal(da, db), nil
}
