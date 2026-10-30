package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/remote"
	"github.com/k0nsta/rigger/internal/sh"
)

func (c *cli) remote(ctx context.Context, args []string) error {
	fs := newFlagSet("remote", c.err)
	repoDir := fs.String("repo", "", "setup repo dir")
	profile := fs.String("profile", "server", "profile to preselect on the host")
	binary := fs.String("binary", "", "upload this rigger binary instead of using the installer")
	host, rest := splitPositional(args)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if host == "" {
		return errors.New("usage: rigger remote <ssh-host> [--profile P] [--binary PATH]")
	}
	if !c.isTTY {
		return errors.New("rigger remote needs a terminal (it runs the installer on the host)")
	}
	s, err := app.Open(app.Options{Repo: *repoDir, Home: c.home})
	if err != nil {
		return err
	}
	c.noteElsewhere(s)
	res, err := s.Exec.Run(ctx, sh.Cmd{Path: "git", Args: []string{"-C", s.Repo, "remote", "get-url", "origin"}})
	if err != nil || res.Code != 0 {
		return fmt.Errorf("setup repo has no origin remote: %s", strings.TrimSpace(res.Output))
	}
	_, terminfoErr := sh.LookPath("agtermctl", s.Exec.Env())

	steps, err := remote.Plan(remote.Options{
		Host:     host,
		Origin:   strings.TrimSpace(res.Output),
		Profile:  *profile,
		Binary:   *binary,
		Terminfo: terminfoErr == nil,
	})
	if err != nil {
		return err
	}
	for _, step := range steps {
		fmt.Fprintf(c.out, "==> %s: %s\n", host, step.Title)
		if step.Interactive {
			cmd := s.Exec.Interactive(step.Cmd)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%s: %w", step.Title, err)
			}
			continue
		}
		step.Cmd.OnLine = func(l string) { fmt.Fprintln(c.out, "    "+l) }
		r, err := s.Exec.Run(ctx, step.Cmd)
		if err != nil {
			return fmt.Errorf("%s: %w", step.Title, err)
		}
		if r.Code != 0 {
			if step.Title == "terminfo" {
				fmt.Fprintln(c.out, "    (terminfo not installed; continuing)")
				continue
			}
			return fmt.Errorf("%s failed (exit %d)", step.Title, r.Code)
		}
	}
	return nil
}
