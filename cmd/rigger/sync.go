package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/repo"
	"github.com/k0nsta/rigger/internal/sh"
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
	// sync commits and pushes the checkout's branch: a session working on a feature branch
	// in the live checkout must not get this machine's changes mixed into it
	if head, err := repo.ReadHead(ctx, s.Exec, s.Repo); err == nil && head.OffDefault() {
		return fmt.Errorf("%s, and sync would commit to it.\n%s", head.Problem(), head.Fix(s.Home))
	}
	sy := &syncer.Syncer{Repo: s.Repo, Runner: s.Exec}
	in := bufio.NewReader(c.in)

	c.exportLayouts(s, sel)
	if s.Platform == manifest.Darwin {
		if err := c.handleStrays(ctx, s, sel, in, yes); err != nil {
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
		if errors.Is(err, syncer.ErrConflict) {
			return fmt.Errorf("%w\nresolve it by hand:\n%s", err, conflictFix(sh.Tilde(s.Home, s.Repo)))
		}
		if errors.Is(err, syncer.ErrUncommitted) {
			return fmt.Errorf("%w\n  run rigger sync again and commit them, or set them aside: git -C %s stash, rigger sync, git -C %s stash pop",
				err, sh.Tilde(s.Home, s.Repo), sh.Tilde(s.Home, s.Repo))
		}
		return err
	}

	// other machines may have added links, packages or changed seed templates
	s, sel, err = f.open(c.home)
	if err != nil {
		return err
	}
	if err := c.applyFromRepo(ctx, s, sel, sy, in, yes); err != nil {
		return err
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

// applyFromRepo lists what the pulled repo would change here and why each component is
// picked, applies the chosen ones, and lets the others be dropped from this host for good.
func (c *cli) applyFromRepo(ctx context.Context, s *app.Session, sel app.Selection, sy *syncer.Syncer, in *bufio.Reader, yes bool) error {
	res, statuses, err := s.Plan(ctx, sel)
	if err != nil {
		return err
	}
	var pending []engine.ComponentStatus
	for _, cs := range statuses {
		if !cs.Settled() && !cs.OfferOnly() && cs.State != action.NeedsSecret {
			pending = append(pending, cs)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	fmt.Fprintf(c.out, "\n%d component(s) to apply from the repo:\n", len(pending))
	tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	for i, cs := range pending {
		why := s.Origin(sel, res, cs.ID())
		if why != "" {
			why = "(" + why + ")"
		}
		fmt.Fprintf(tw, "  [%d] %s\t%s\t%s\t%s\n", i+1, cs.ID(), cs.State, cs.Summary, why)
	}
	_ = tw.Flush()

	apply := pending
	if !yes {
		var drop []int
		apply, drop = c.askApply(in, pending)
		var dropped []string
		for _, i := range drop {
			id := pending[i].ID()
			if err := s.DropFromHost(id); err != nil {
				fmt.Fprintf(c.out, "  %s stays: %v\n", id, err)
				continue
			}
			dropped = append(dropped, id)
		}
		if len(dropped) > 0 {
			hostFile := filepath.ToSlash(filepath.Join("hosts", s.Host+".yaml"))
			fmt.Fprintf(c.out, "dropped %s from %s (rigger never uninstalls: remove what is already there by hand)\n", strings.Join(dropped, ", "), hostFile)
			if err := sy.Commit(ctx, s.Host, []string{hostFile}); err != nil {
				return err
			}
		}
	}
	if len(apply) == 0 {
		return nil
	}

	stop, err := s.StartAskpass(c.terminalPassword)
	if err != nil {
		return err
	}
	defer stop()
	d := c.decideReplacedLinks(in, apply, yes, s.Home)
	d.Running = processRunning(ctx, s.Exec)
	outcomes := s.Engine.Apply(ctx, apply, d, c.printEvent)
	if err := s.Local.Save(s.LocalPath); err != nil {
		return err
	}
	for _, o := range outcomes {
		if o.Kind == engine.Failed {
			fmt.Fprintf(c.out, "warning: %s failed: %v\n", o.Component, o.Err)
		}
	}
	return nil
}

// decideReplacedLinks asks what to do with each link whose target a tool replaced with a real
// file of other content (sed -i, an editor's atomic save): relinking it silently would move the
// user's edit to a backup. With --yes the file is left as it is and a warning says how to adopt it.
func (c *cli) decideReplacedLinks(in *bufio.Reader, apply []engine.ComponentStatus, yes bool, home string) engine.Decisions {
	d := engine.Decisions{Adopt: map[string]bool{}, Leave: map[string]bool{}}
	for _, cs := range apply {
		for _, a := range cs.Actions {
			if a.Kind != engine.KindLink || a.Status != action.Differs {
				continue
			}
			target := sh.Tilde(home, a.Target)
			if yes {
				d.Leave[a.Key()] = true
				fmt.Fprintf(c.out, "warning: %s is a real file now, with other content than the repo's; left as it is.\n"+
					"  take it into the repo: rigger adopt %s\n", target, target)
				continue
			}
			fmt.Fprintf(c.out, "\n%s (%s) is a real file now, with other content than the repo's:\n", target, cs.ID())
			fmt.Fprintln(c.out, "  a      adopt it: copy it into the repo, then link it")
			fmt.Fprintln(c.out, "  k      keep the repo version: your file goes to a backup")
			fmt.Fprintln(c.out, "  enter  leave it as it is for now")
			switch c.askChoice(in, "  choice [a/k/enter]: ", "a", "k", "") {
			case "a":
				d.Adopt[a.Key()] = true
			case "k":
			default:
				d.Leave[a.Key()] = true
			}
		}
	}
	return d
}

// askChoice reads one answer from choices, the last of which is "" (just enter), asking again
// until it is one; input that ends returns "".
func (c *cli) askChoice(in *bufio.Reader, prompt string, choices ...string) string {
	for {
		fmt.Fprint(c.out, prompt)
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(c.out)
			return ""
		}
		answer := strings.TrimSpace(line)
		for _, ch := range choices {
			if answer == ch {
				return answer
			}
		}
		fmt.Fprintf(c.out, "  type one of %s, or just enter\n", strings.Join(choices[:len(choices)-1], ", "))
	}
}

// askApply asks which pending components to apply and which to drop from this host,
// asking again until the answer parses. Prerequisites of an applied one are applied too.
func (c *cli) askApply(in *bufio.Reader, pending []engine.ComponentStatus) (apply []engine.ComponentStatus, drop []int) {
	for {
		fmt.Fprint(c.out, "apply which? (enter = all, none, numbers like 1 3; -2 drops 2 from this host): ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(c.out)
			return nil, nil // input closed: change nothing
		}
		picks, drops, ok := parseApplyAnswer(line, len(pending))
		if !ok {
			fmt.Fprintf(c.out, "  numbers go from 1 to %d; prefix one with - to drop it\n", len(pending))
			continue
		}
		chosen := map[string]bool{}
		for _, i := range picks {
			chosen[pending[i].ID()] = true
		}
		// a pending prerequisite of a chosen component has to run first
		byID := map[string]engine.ComponentStatus{}
		for _, cs := range pending {
			byID[cs.ID()] = cs
		}
		for changed := true; changed; {
			changed = false
			for id := range chosen {
				for _, req := range byID[id].Component.Requires {
					if _, ok := byID[req]; ok && !chosen[req] {
						chosen[req], changed = true, true
						fmt.Fprintf(c.out, "  also applying %s: %s needs it\n", req, id)
					}
				}
			}
		}
		for _, cs := range pending {
			if chosen[cs.ID()] {
				apply = append(apply, cs)
			}
		}
		return apply, drops
	}
}

// parseApplyAnswer reads "", "all", "none", and numbers (0-based in the result); "-n" drops
// n from this host. Numbers apply only those; drops alone apply the rest.
func parseApplyAnswer(line string, n int) (apply, drop []int, ok bool) {
	answer := strings.ToLower(strings.TrimSpace(line))
	all := func(skip map[int]bool) []int {
		var out []int
		for i := range n {
			if !skip[i] {
				out = append(out, i)
			}
		}
		return out
	}
	switch answer {
	case "", "all", "y", "yes":
		return all(nil), nil, true
	case "none", "n", "no":
		return nil, nil, true
	}
	none, dropped := false, map[int]bool{}
	for _, f := range strings.Fields(answer) {
		if f == "none" || f == "n" {
			none = true
			continue
		}
		neg := strings.HasPrefix(f, "-")
		k, err := strconv.Atoi(strings.TrimPrefix(f, "-"))
		if err != nil || k < 1 || k > n {
			return nil, nil, false
		}
		if neg {
			drop, dropped[k-1] = append(drop, k-1), true
		} else {
			apply = append(apply, k-1)
		}
	}
	for _, i := range apply {
		if dropped[i] {
			return nil, nil, false // both applied and dropped
		}
	}
	if len(apply) == 0 && !none {
		apply = all(dropped)
	}
	return apply, drop, true
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

// warnHead warns when the setup repo is not on its default branch.
func (c *cli) warnHead(ctx context.Context, s *app.Session) {
	if head, err := repo.ReadHead(ctx, s.Exec, s.Repo); err == nil && head.OffDefault() {
		fmt.Fprintf(c.out, "warning: %s\n  %s\n\n", head.Problem(), head.Fix(s.Home))
	}
}

// handleStrays offers to add Homebrew packages installed outside any Brewfile.
func (c *cli) handleStrays(ctx context.Context, s *app.Session, sel app.Selection, in *bufio.Reader, yes bool) error {
	var brewfiles []action.Brew
	var names []string
	var comps []*manifest.Component
	for i := range s.Manifest.Components {
		comp := &s.Manifest.Components[i]
		if comp.Brewfile == "" || !comp.Supports(s.Platform) {
			continue
		}
		b, err := action.NewBrew(s.Engine.Env, comp.Brewfile)
		if err != nil {
			return err
		}
		brewfiles = append(brewfiles, b)
		names = append(names, comp.Brewfile)
		comps = append(comps, comp)
	}
	if len(brewfiles) == 0 {
		return nil
	}
	strays, err := syncer.Strays(ctx, s.Exec, brewfiles, s.HostFile.IgnorePackages)
	if err != nil || len(strays) == 0 {
		return nil //nolint:nilerr // no Homebrew here: nothing to report
	}
	fmt.Fprintf(c.out, "\n%d Homebrew package(s) installed here are in no Brewfile:\n", len(strays))
	width := 0
	for _, st := range strays {
		width = max(width, len(st.Kind)+1+len(st.Name))
	}
	for _, st := range strays {
		fmt.Fprintln(c.out, strings.TrimRight(fmt.Sprintf("  %-*s  %s", width, st.Kind+" "+st.Name, st.Desc), " "))
	}
	if yes {
		fmt.Fprintln(c.out, "(run rigger sync in a terminal to add or ignore them)")
		return nil
	}
	hostFile := state.HostPath(s.Repo, s.Host)
	if rel, err := filepath.Rel(s.Repo, hostFile); err == nil {
		hostFile = rel
	}
	last := len(brewfiles)
	fmt.Fprintln(c.out, "\nFor each package, type where it goes:")
	c.printTargets(s, sel, comps)
	fmt.Fprintf(c.out, "  %-*s keep it on %s only (ignore_packages in %s)\n", targetPad, "i", s.Host, hostFile)
	fmt.Fprintf(c.out, "  %-*s decide later; asked again on the next sync\n", targetPad, "enter")
	fmt.Fprintf(c.out, "  %-*s a trailing * gives the rest the same answer\n\n", targetPad, "2*")

	var ignored []string
	added := map[string]int{}
	answer, all := "", false
	for _, st := range strays {
		prompt := fmt.Sprintf("%s [1-%d/i/enter]: ", st.Name, last)
		if all {
			fmt.Fprintln(c.out, prompt+answer)
		} else {
			var ok bool
			if answer, all, ok = c.askStray(in, prompt, len(brewfiles)); !ok {
				break // input closed: leave the rest for later
			}
		}
		switch answer {
		case "":
		case "i":
			ignored = append(ignored, st.Name)
		default:
			n, _ := strconv.Atoi(answer)
			if err := syncer.AddToBrewfile(brewfiles[n-1].File, st); err != nil {
				return err
			}
			added[names[n-1]]++
		}
	}
	if len(ignored) > 0 {
		s.HostFile.IgnorePackages = append(s.HostFile.IgnorePackages, ignored...)
		if err := s.HostFile.Save(state.HostPath(s.Repo, s.Host)); err != nil {
			return err
		}
	}
	for _, n := range names {
		if added[n] > 0 {
			fmt.Fprintf(c.out, "added %d package(s) to %s\n", added[n], n)
		}
	}
	if len(ignored) > 0 {
		fmt.Fprintf(c.out, "ignored %d package(s) on %s\n", len(ignored), s.Host)
	}
	fmt.Fprintln(c.out)
	return nil
}

// targetPad is the width of the answer column in the stray legend.
const targetPad = 6

// printTargets lists the Brewfiles a stray can go to, with the profiles that install each one,
// so picking a Brewfile reads as picking which machines get the package.
func (c *cli) printTargets(s *app.Session, sel app.Selection, comps []*manifest.Component) {
	profiles, err := s.Manifest.ProfilesOf(s.Platform)
	if err != nil {
		profiles = nil // a broken profile only costs the hints; validate reports it
	}
	here := map[string]bool{}
	if res, err := s.Manifest.Resolve(s.Platform, sel.Picked); err == nil {
		for _, id := range res.Order {
			here[id] = true
		}
	}
	fileW, titleW := 0, 0
	for _, comp := range comps {
		fileW = max(fileW, len(brewfileLabel(comp.Brewfile)))
		titleW = max(titleW, len(comp.Name()))
	}
	for i, comp := range comps {
		who := "no profile"
		if ps := profiles[comp.ID]; len(ps) > 0 {
			who = strings.Join(ps, ", ")
		}
		if !here[comp.ID] {
			who += fmt.Sprintf(" (not on %s)", s.Host)
		}
		fmt.Fprintf(c.out, "  %-*d %-*s  %-*s  → %s\n", targetPad, i+1, fileW, brewfileLabel(comp.Brewfile), titleW, comp.Name(), who)
	}
}

// brewfileLabel shortens brew/dev.Brewfile to dev.
func brewfileLabel(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".Brewfile")
}

// askStray reads one answer, asking again until it is valid; ok is false when input ends.
func (c *cli) askStray(in *bufio.Reader, prompt string, brewfiles int) (answer string, all, ok bool) {
	for {
		fmt.Fprint(c.out, prompt)
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(c.out)
			return "", false, false
		}
		answer, all = strings.CutSuffix(strings.TrimSpace(line), "*")
		if validStrayAnswer(answer, brewfiles) {
			return answer, all, true
		}
		fmt.Fprintf(c.out, "  type a number from 1 to %d, i, or just enter\n", brewfiles)
	}
}

// validStrayAnswer accepts a Brewfile number, i, or nothing.
func validStrayAnswer(answer string, brewfiles int) bool {
	if answer == "" || answer == "i" {
		return true
	}
	n, err := strconv.Atoi(answer)
	return err == nil && n >= 1 && n <= brewfiles
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

// conflictFix is how to finish a pull that rigger aborted on a conflict.
func conflictFix(dir string) string {
	return fmt.Sprintf("  git -C %s pull --rebase\n"+
		"  fix the conflicting files, then git -C %s add <file> && git -C %s rebase --continue\n"+
		"  rigger sync", dir, dir, dir)
}
