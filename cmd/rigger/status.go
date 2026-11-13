package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/state"
)

func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func (c *cli) status(ctx context.Context, args []string) error {
	var f sessionFlags
	fs := newFlagSet("status", c.err)
	f.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, sel, err := f.open(c.home)
	if err != nil {
		return err
	}
	c.noteElsewhere(s)
	res, statuses, err := s.Plan(ctx, sel)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s · %s · profile %s · repo %s\n", s.Host, s.Platform, orNone(sel.Profile), sh.Tilde(s.Home, s.Repo))
	printVars(c.out, s)
	fmt.Fprintln(c.out)
	c.warnHead(ctx, s)
	printStatuses(c.out, statuses, res)
	if missing := engine.MissingSecrets(statuses, s.Secrets); len(missing) > 0 {
		fmt.Fprint(c.out, missingSecrets(s.Home, missing, ""))
	}
	for _, cs := range statuses {
		if !cs.Settled() && !cs.OfferOnly() {
			return exitCode(1)
		}
	}
	return nil
}

func printVars(out io.Writer, s *app.Session) {
	if len(s.Manifest.Vars) == 0 && (s.HostFile == nil || len(s.HostFile.Vars) == 0) {
		return
	}

	names := make([]string, 0, len(s.Manifest.Vars))
	for name := range s.Manifest.Vars {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]string, 0, len(names))
	for _, name := range names {
		value := s.Engine.Env.Vars[name]
		values = append(values, fmt.Sprintf("%s=%s (%s)", name, sh.Tilde(s.Home, value), s.Engine.Env.VarSources[name]))
	}
	if len(values) > 0 {
		fmt.Fprintf(out, "vars: %s\n", strings.Join(values, ", "))
	}

	if s.HostFile == nil {
		return
	}
	undeclared := make([]string, 0)
	for name := range s.HostFile.Vars {
		if _, ok := s.Manifest.Vars[name]; !ok {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(undeclared)
	for _, name := range undeclared {
		fmt.Fprintf(out, "warning: hosts/%s.yaml: var %s is not declared in manifest vars\n", s.Host, name)
	}
}

func printStatuses(out io.Writer, statuses []engine.ComponentStatus, res manifest.Resolution) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tSTATE\tDETAIL")
	for _, cs := range statuses {
		detail := cs.Summary
		if cs.AddedBecause != "" {
			detail += " (needed by " + cs.AddedBecause + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", cs.ID(), cs.State, detail)
	}
	for _, id := range res.Unsupported {
		fmt.Fprintf(tw, "%s\tskipped\tnot for this platform\n", id)
	}
	_ = tw.Flush()
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// missingSecrets says which secrets are not set and where to put them; note follows the list.
func missingSecrets(home string, keys []string, note string) string {
	if note != "" {
		note = " (" + note + ")"
	}
	file := sh.Tilde(home, filepath.Join(state.ConfigDir(home), "secrets.env"))
	return fmt.Sprintf("\nmissing secrets%s: %s\n  add each to %s as KEY=value (if you use 1Password, KEY=op://vault/item/field also works)\n",
		note, strings.Join(keys, ", "), file)
}
