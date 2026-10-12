package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/sh"
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
	res, statuses, err := s.Plan(ctx, sel)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s · %s · profile %s · repo %s\n\n", s.Host, s.Platform, orNone(sel.Profile), sh.Tilde(s.Home, s.Repo))
	printStatuses(c.out, statuses, res)
	if missing := engine.MissingSecrets(statuses, s.Secrets); len(missing) > 0 {
		fmt.Fprintf(c.out, "\nmissing secrets (add to ~/.config/rigger/secrets.env): %s\n", strings.Join(missing, ", "))
	}
	for _, cs := range statuses {
		if !cs.Settled() && !cs.OfferOnly() {
			return exitCode(1)
		}
	}
	return nil
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
