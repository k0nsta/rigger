package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/state"
	"github.com/k0nsta/rigger/internal/syncer"
)

func (c *cli) sync(ctx context.Context, args []string) error {
	var f sessionFlags
	var yes, noPush bool
	fs := newFlagSet("sync", c.err)
	f.register(fs)
	fs.BoolVar(&yes, "yes", false, "commit every change and apply without asking")
	fs.BoolVar(&noPush, "no-push", false, "commit and pull, but do not push")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !yes && !c.isTTY {
		return errors.New("stdin is not a terminal: pass --yes")
	}
	s, sel, err := f.open(c.home)
	if err != nil {
		return err
	}
	if s.HostFile == nil {
		return fmt.Errorf("hosts/%s.yaml does not exist yet: run rigger (or rigger apply) on this machine first", s.Host)
	}
	sy := &syncer.Syncer{Repo: s.Repo, Runner: s.Exec}
	in := bufio.NewReader(c.in)

	c.exportLayouts(s, sel)
	if s.Platform == manifest.Darwin {
		if err := c.handleStrays(ctx, s, in, yes); err != nil {
			return err
		}
	}

	changes, err := sy.Changes(ctx)
	if err != nil {
		return err
	}
	files := c.pickFiles(changes, in, yes)
	if err := sy.Commit(ctx, s.Host, files); err != nil {
		return err
	}
	if len(files) > 0 {
		fmt.Fprintf(c.out, "committed %d file(s)\n", len(files))
	}

	fmt.Fprintln(c.out, "pulling…")
	if err := sy.Pull(ctx); err != nil {
		return err
	}

	// other machines may have added links, packages or changed seed templates
	s, sel, err = f.open(c.home)
	if err != nil {
		return err
	}
	res, statuses, err := s.Plan(ctx, sel)
	if err != nil {
		return err
	}
	pending := 0
	for _, cs := range statuses {
		if !cs.Settled() && cs.State != action.NeedsSecret {
			pending++
		}
	}
	if pending > 0 {
		printStatuses(c.out, statuses, res)
		if yes || c.confirmFrom(in, fmt.Sprintf("\napply %d component(s) from the repo? [y/N] ", pending)) {
			if engine.NeedsSudo(statuses) {
				if err := c.primeSudo(ctx, s.Exec); err != nil {
					return err
				}
			}
			outcomes := s.Engine.Apply(ctx, statuses, engine.Decisions{Running: processRunning(ctx, s.Exec)}, c.printEvent)
			if err := s.Local.Save(s.LocalPath); err != nil {
				return err
			}
			for _, o := range outcomes {
				if o.Kind == engine.Failed {
					fmt.Fprintf(c.out, "warning: %s failed: %v\n", o.Component, o.Err)
				}
			}
		}
	}

	if noPush {
		fmt.Fprintln(c.out, "not pushed (--no-push)")
		return nil
	}
	if sy.Ahead(ctx) > 0 {
		fmt.Fprintln(c.out, "pushing…")
		if err := sy.Push(ctx); err != nil {
			return err
		}
	}
	fmt.Fprintln(c.out, "in sync")
	return nil
}

func (c *cli) printEvent(ev engine.Event) {
	switch ev.Kind {
	case engine.Started:
		fmt.Fprintf(c.out, "\n==> %s\n", ev.Component)
	case engine.Line:
		fmt.Fprintf(c.out, "    %s\n", ev.Text)
	case engine.Failed:
		fmt.Fprintf(c.out, "    FAILED: %v\n", ev.Err)
	case engine.Skipped:
		fmt.Fprintf(c.out, "\n--  %s skipped: %v\n", ev.Component, ev.Err)
	}
}

// exportLayouts snapshots app layouts of selected components for this host.
func (c *cli) exportLayouts(s *app.Session, sel app.Selection) {
	res, err := s.Manifest.Resolve(s.Platform, sel.Picked)
	if err != nil {
		return
	}
	for _, id := range res.Order {
		comp := s.Manifest.Component(id)
		if comp.Layouts == nil {
			continue
		}
		l := action.NewLayouts(s.Engine.Env, *comp.Layouts)
		if n, err := l.Export(s.Home, s.Host); err != nil {
			fmt.Fprintf(c.out, "warning: layouts of %s not exported: %v\n", id, err)
		} else if n > 0 {
			fmt.Fprintf(c.out, "exported %d layout file(s) of %s\n", n, id)
		}
	}
}

// handleStrays offers to add Homebrew packages installed outside any Brewfile.
func (c *cli) handleStrays(ctx context.Context, s *app.Session, in *bufio.Reader, yes bool) error {
	var brewfiles []action.Brew
	var names []string
	for _, comp := range s.Manifest.Components {
		if comp.Brewfile == "" || !comp.Supports(s.Platform) {
			continue
		}
		b, err := action.NewBrew(s.Engine.Env, comp.Brewfile)
		if err != nil {
			return err
		}
		brewfiles = append(brewfiles, b)
		names = append(names, comp.Brewfile)
	}
	if len(brewfiles) == 0 {
		return nil
	}
	strays, err := syncer.Strays(ctx, s.Exec, brewfiles, s.HostFile.IgnorePackages)
	if err != nil || len(strays) == 0 {
		return nil //nolint:nilerr // no Homebrew here: nothing to report
	}
	fmt.Fprintf(c.out, "%d Homebrew package(s) are in no Brewfile:\n", len(strays))
	if yes {
		for _, st := range strays {
			fmt.Fprintf(c.out, "  %s %s\n", st.Kind, st.Name)
		}
		fmt.Fprintln(c.out, "(run rigger sync in a terminal to add or ignore them)")
		return nil
	}
	for i, n := range names {
		fmt.Fprintf(c.out, "  [%d] %s\n", i+1, n)
	}
	var ignored []string
	for _, st := range strays {
		fmt.Fprintf(c.out, "%s %s → Brewfile number, i ignore here, enter skip: ", st.Kind, st.Name)
		line, _ := in.ReadString('\n')
		answer := strings.TrimSpace(line)
		switch {
		case answer == "i":
			ignored = append(ignored, st.Name)
		case answer != "":
			n, err := strconv.Atoi(answer)
			if err != nil || n < 1 || n > len(brewfiles) {
				fmt.Fprintln(c.out, "  skipped")
				continue
			}
			if err := syncer.AddToBrewfile(brewfiles[n-1].File, st); err != nil {
				return err
			}
		}
	}
	if len(ignored) > 0 {
		s.HostFile.IgnorePackages = append(s.HostFile.IgnorePackages, ignored...)
		if err := s.HostFile.Save(state.HostPath(s.Repo, s.Host)); err != nil {
			return err
		}
	}
	return nil
}

// pickFiles shows the changes and returns those to commit.
func (c *cli) pickFiles(changes []syncer.Change, in *bufio.Reader, yes bool) []string {
	if len(changes) == 0 {
		fmt.Fprintln(c.out, "no local changes")
		return nil
	}
	fmt.Fprintln(c.out, "local changes:")
	all := make([]string, 0, len(changes))
	for i, ch := range changes {
		fmt.Fprintf(c.out, "  [%d] %s %s\n", i+1, ch.Code, ch.Path)
		all = append(all, ch.Path)
	}
	if yes {
		return all
	}
	fmt.Fprint(c.out, "commit which? (enter = all, none, or numbers like 1 3): ")
	line, _ := in.ReadString('\n')
	answer := strings.TrimSpace(line)
	switch answer {
	case "", "all":
		return all
	case "none", "n":
		return nil
	}
	var picked []string
	for _, f := range strings.Fields(answer) {
		if n, err := strconv.Atoi(f); err == nil && n >= 1 && n <= len(all) {
			picked = append(picked, all[n-1])
		}
	}
	return picked
}

func (c *cli) confirmFrom(in *bufio.Reader, prompt string) bool {
	fmt.Fprint(c.out, prompt)
	line, _ := in.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
