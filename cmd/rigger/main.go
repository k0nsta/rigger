// Command rigger sets a machine up from a setup repo: it shows what will be installed and
// linked, lets you pick, installs, links everything back to the repo and keeps machines in sync.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/manifest"
)

// version is set by goreleaser.
var version = "dev"

const usage = `usage: rigger [command] [flags]

  rigger     interactive installer for the remembered setup repo

commands:
  init URL   clone your setup repo (default ~/<git-host>/<repo-name>) and start the installer
  status     show what is installed and linked for this host
  apply      install and link the selection (--profile, --only, --yes, --dry-run)
  sync       commit local changes, pull other machines', re-apply, push (--yes, --no-push)
  adopt P    take a live seeded or linked file back into the repo (secrets become ${VAR})
  remote H   set up ssh host H: install rigger, clone the repo there, run the installer (--profile, --binary)
  validate   check a setup repo's manifest.yaml
  version    print the version

common flags: --repo DIR (or $RIGGER_REPO), --host NAME`

// cli carries the process streams so commands are testable.
type cli struct {
	out, err io.Writer
	in       io.Reader
	isTTY    bool
	home     string // empty = $HOME
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c := &cli{out: os.Stdout, err: os.Stderr, in: os.Stdin, isTTY: stdinIsTerminal()}
	if err := c.run(ctx, os.Args[1:]); err != nil {
		var exit exitCode
		if errors.As(err, &exit) {
			os.Exit(int(exit))
		}
		fmt.Fprintln(os.Stderr, "rigger:", err)
		os.Exit(1)
	}
}

// exitCode ends the process with a code and no message (status uses 1 for "not all ok").
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func (c *cli) run(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") && args[0] != "-h" && args[0] != "--help" && args[0] != "--version" {
		if !c.isTTY {
			return errors.New(usage)
		}
		var f sessionFlags
		fs := newFlagSet("rigger", c.err)
		f.register(fs)
		if err := fs.Parse(args); err != nil {
			return err
		}
		return c.interactive(ctx, f)
	}
	switch args[0] {
	case "version", "--version":
		_, err := fmt.Fprintln(c.out, "rigger", version)
		return err
	case "validate":
		return c.validate(args[1:])
	case "init":
		return c.init(ctx, args[1:])
	case "status":
		return c.status(ctx, args[1:])
	case "apply":
		return c.apply(ctx, args[1:])
	case "sync":
		return c.sync(ctx, args[1:])
	case "remote":
		return c.remote(ctx, args[1:])
	case "adopt":
		return c.adopt(ctx, args[1:])
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(c.out, usage)
		return err
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func (c *cli) validate(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	m, err := manifest.Load(dir)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.out, "%s: %d components, %d profiles, ok\n", manifest.FileName, len(m.Components), len(m.Profiles))
	return err
}

// sessionFlags are shared by commands that open a session.
type sessionFlags struct {
	repo, host, profile, only string
}

func (f *sessionFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.repo, "repo", "", "setup repo dir")
	fs.StringVar(&f.host, "host", "", "host name for hosts/<name>.yaml")
	fs.StringVar(&f.profile, "profile", "", "profile to preselect")
	fs.StringVar(&f.only, "only", "", "comma-separated component ids (prerequisites are added)")
}

func (f *sessionFlags) open(home string) (*app.Session, app.Selection, error) {
	s, err := app.Open(app.Options{Repo: f.repo, Host: f.host, Home: home})
	if err != nil {
		return nil, app.Selection{}, err
	}
	var only []string
	if f.only != "" {
		only = strings.Split(f.only, ",")
	}
	sel, err := s.Select(f.profile, only)
	if errors.Is(err, app.ErrNoProfile) {
		err = fmt.Errorf("%w (one of: %s)", err, strings.Join(s.Profiles(), ", "))
	}
	return s, sel, err
}

func newFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}
