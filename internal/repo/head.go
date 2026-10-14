package repo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/k0nsta/rigger/internal/sh"
)

// Head is what the setup repo has checked out. Links point into this checkout, so its
// branch is what the machine's configs come from.
type Head struct {
	Dir string
	// Branch is the checked-out branch, empty when HEAD is detached.
	Branch string
	// Default is the remote's default branch (main when origin/HEAD is not set), empty when unknown.
	Default string
}

// ReadHead reads the checked-out and default branch of the repo in dir.
func ReadHead(ctx context.Context, r Runner, dir string) (Head, error) {
	h := Head{Dir: dir}
	git := func(args ...string) (string, bool, error) {
		res, err := r.Run(ctx, sh.Cmd{Path: "git", Args: append([]string{"-C", dir}, args...)})
		if err != nil {
			return "", false, err
		}
		return strings.TrimSpace(res.Output), res.Code == 0, nil
	}
	branch, ok, err := git("symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return h, err
	}
	if ok {
		h.Branch = branch
	}
	if ref, ok, err := git("symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err != nil {
		return h, err
	} else if ok {
		h.Default = strings.TrimPrefix(ref, "origin/")
		return h, nil
	}
	for _, name := range []string{"main", "master"} {
		if _, ok, err := git("rev-parse", "-q", "--verify", "refs/heads/"+name); err != nil {
			return h, err
		} else if ok {
			h.Default = name
			return h, nil
		}
	}
	return h, nil
}

// OffDefault reports whether the checkout is not on the default branch (a detached HEAD counts).
func (h Head) OffDefault() bool {
	return h.Default != "" && h.Branch != h.Default
}

// Problem describes an off-default checkout, or returns "" when it is on the default branch.
func (h Head) Problem() string {
	if !h.OffDefault() {
		return ""
	}
	name := filepath.Base(h.Dir)
	if h.Branch == "" {
		return fmt.Sprintf("%s has a detached HEAD, not %s: your linked configs come from that commit", name, h.Default)
	}
	return fmt.Sprintf("%s is on branch %s, not %s: your linked configs come from that branch", name, h.Branch, h.Default)
}

// Fix tells how to get back to the default branch.
func (h Head) Fix(home string) string {
	return fmt.Sprintf("switch back with `git -C %s switch %s`; keep feature work in a git worktree", sh.Tilde(home, h.Dir), h.Default)
}
