package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/skill"
	"github.com/k0nsta/rigger/internal/state"
)

const skillUsage = "usage: rigger skill install [--force]"

func (c *cli) skill(args []string) error {
	if len(args) == 0 || args[0] != "install" {
		return errors.New(skillUsage)
	}

	var force bool
	fs := newFlagSet("skill install", c.err)
	fs.BoolVar(&force, "force", false, "replace files not written by rigger")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(skillUsage)
	}

	home := c.home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	paths, err := skill.Install(home, force)
	if err != nil {
		var foreign *skill.ForeignError
		if errors.As(err, &foreign) {
			return fmt.Errorf("%s: differs from the skill and was not written by rigger", sh.Tilde(home, foreign.Path))
		}
		return err
	}

	repo := rememberedRepo(home)
	for i, path := range paths {
		line := sh.Tilde(home, path)
		if i == 0 && insideRememberedRepo(path, repo) {
			line += " (in your setup repo: commit it with rigger sync)"
		}
		if _, err := fmt.Fprintln(c.out, line); err != nil {
			return err
		}
	}
	return nil
}

func rememberedRepo(home string) string {
	local, err := state.LoadLocal(state.LocalPath(home))
	if err != nil || local.Repo == "" {
		return ""
	}
	return local.Repo
}

func insideRememberedRepo(skillPath, repo string) bool {
	if repo == "" {
		return false
	}
	resolvedSkill, err := filepath.EvalSymlinks(skillPath)
	if err != nil {
		return false
	}
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRepo, resolvedSkill)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return false
	}
	return true
}
