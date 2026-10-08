package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/repo"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/state"
)

func (c *cli) init(ctx context.Context, args []string) error {
	fs := newFlagSet("init", c.err)
	dir := fs.String("dir", "", "where to clone (default ~/<git-host>/<repo-name>)")
	noRun := fs.Bool("no-run", false, "clone and remember the repo, do not start the installer")
	var f sessionFlags
	f.register(fs)
	url, rest := splitPositional(args)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if url == "" {
		return errors.New("usage: rigger init <git-url> [--dir DIR] [--profile P] [--no-run]")
	}

	home := c.home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = h
	}
	e := sh.NewExec(home)
	if err := c.ensureGit(ctx, e); err != nil {
		return err
	}
	target := *dir
	if target == "" {
		d, err := repo.DefaultDir(home, url)
		if err != nil {
			return err
		}
		target = d
	}

	fmt.Fprintf(c.out, "cloning %s into %s\n", url, target)
	if err := repo.Clone(ctx, e, url, target, c.tokenPrompt, func(l string) { fmt.Fprintln(c.out, "  "+l) }); err != nil {
		return err
	}
	if switched, err := repo.UseSSHOrigin(ctx, e, target); err == nil && switched {
		fmt.Fprintln(c.out, "origin switched to SSH")
	}

	localPath := state.LocalPath(home)
	local, err := state.LoadLocal(localPath)
	if err != nil {
		return err
	}
	local.Repo = target
	if err := local.Save(localPath); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "setup repo: %s\n", target)

	if *noRun {
		return nil
	}
	f.repo = target
	if c.isTTY {
		return c.interactive(ctx, f)
	}
	s, err := app.Open(app.Options{Repo: target, Home: c.home})
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "next: rigger apply --profile <%s>\n", strings.Join(s.Profiles(), "|"))
	return nil
}

// splitPositional takes the first non-flag argument out of args.
func splitPositional(args []string) (string, []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			if i > 0 && !strings.Contains(args[i-1], "=") && flagTakesValue(args[i-1]) {
				continue
			}
			return a, append(append([]string{}, args[:i]...), args[i+1:]...)
		}
	}
	return "", args
}

func flagTakesValue(flag string) bool {
	switch strings.TrimLeft(flag, "-") {
	case "dir", "repo", "host", "profile", "only", "binary":
		return true
	}
	return false
}

// ensureGit makes sure a working git exists; on macOS that needs the Command Line Tools.
func (c *cli) ensureGit(ctx context.Context, e *sh.Exec) error {
	if runtime.GOOS == "darwin" {
		res, err := e.Run(ctx, sh.Cmd{Path: "xcode-select", Args: []string{"-p"}})
		if err != nil || res.Code != 0 {
			_, _ = e.Run(ctx, sh.Cmd{Path: "xcode-select", Args: []string{"--install"}})
			return errors.New("git needs the Xcode Command Line Tools: finish the install dialog, then run rigger init again")
		}
	}
	res, err := e.Run(ctx, sh.Cmd{Path: "git", Args: []string{"--version"}})
	if err != nil || res.Code != 0 {
		return errors.New("git is not installed (Debian/Ubuntu: sudo apt-get install -y git)")
	}
	return nil
}

// tokenPrompt reads an access token without echo.
func (c *cli) tokenPrompt(host string) (string, error) {
	if !c.isTTY {
		return "", fmt.Errorf("%s needs a token and stdin is not a terminal (use an SSH URL or a credential helper)", host)
	}
	fmt.Fprintf(c.out, "access token for %s (used once, not stored): ", host)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(c.out)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
