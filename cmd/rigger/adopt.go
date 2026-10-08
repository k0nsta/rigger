package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/k0nsta/rigger/internal/app"
)

// adopt takes live files back into the setup repo.
func (c *cli) adopt(ctx context.Context, args []string) error {
	fs := newFlagSet("adopt", c.err)
	repoDir := fs.String("repo", "", "setup repo dir")
	host := fs.String("host", "", "host name")
	var paths []string
	for len(args) > 0 {
		p, rest := splitPositional(args)
		if p == "" {
			break
		}
		paths = append(paths, p)
		args = rest
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("usage: rigger adopt <path> [<path>…]  (a seeded or linked file, e.g. ~/.codex/config.toml)")
	}
	s, err := app.Open(app.Options{Repo: *repoDir, Host: *host, Home: c.home})
	if err != nil {
		return err
	}
	for _, p := range paths {
		msg, err := s.Adopt(ctx, p)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, msg)
	}
	fmt.Fprintln(c.out, "share it with: rigger sync")
	return nil
}
