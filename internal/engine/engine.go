// Package engine turns a selection into per-component status (probe) and runs it (apply).
package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/state"
)

// Action kinds.
const (
	KindBrew    = "brew"
	KindApt     = "apt"
	KindLink    = "link"
	KindSeed    = "seed"
	KindLayouts = "layouts"
	KindCmd     = "cmd"
)

// Engine probes and applies components for one machine.
type Engine struct {
	Manifest *manifest.Manifest
	Env      *action.Env
	Runner   Runner
	Secrets  *action.Secrets
	Local    *state.Local
	Platform string
	Host     string
	AppPaths action.AppPaths

	inventory *action.BrewInventory
	brewKnown bool
	hasBrew   bool
}

// ActionStatus is one probed action of a component.
type ActionStatus struct {
	Kind   string
	Target string
	Status action.Status
	Detail string

	link      *action.Link
	linkProbe action.LinkProbe
	seed      *action.Seed
	seedProbe action.SeedProbe
	layouts   *action.Layouts
	layProbe  action.LayoutsProbe
	brew      *action.Brew
	brewProbe action.BrewProbe
	apt       *action.AptProbe
	cmd       *action.Command
}

// Key identifies the action for decisions.
func (a ActionStatus) Key() string { return a.Kind + ":" + a.Target }

// Brew returns the Brewfile probe for package-level UI, when this is a brew action.
func (a ActionStatus) Brew() (action.BrewProbe, bool) {
	return a.brewProbe, a.Kind == KindBrew
}

// BrewPackages lists the Brewfile entries in file order, when this is a brew action.
func (a ActionStatus) BrewPackages() []action.BrewPackage {
	if a.brew == nil {
		return nil
	}
	return a.brew.Packages
}

// Layouts returns the layouts probe, when this is a layouts action.
func (a ActionStatus) Layouts() (action.LayoutsProbe, bool) {
	return a.layProbe, a.Kind == KindLayouts
}

// ComponentStatus is the probed state of one component.
type ComponentStatus struct {
	Component *manifest.Component
	// AddedBecause names the picked component that pulled this one in, if any.
	AddedBecause string
	State        action.Status
	Summary      string
	Actions      []ActionStatus
}

// ID of the component.
func (c ComponentStatus) ID() string { return c.Component.ID }

// Settled reports whether nothing needs doing.
func (c ComponentStatus) Settled() bool { return c.State.Settled() }

// severity orders statuses; the worst action decides the component state.
func severity(s action.Status) int {
	switch s {
	case action.OK, action.LocalEdit, action.Present, action.NoSnapshot:
		return 0
	case action.Todo, action.Missing, action.Update, action.FreshTarget, action.Relink:
		return 1
	case action.Conflict:
		return 2
	case action.Differs, action.BothChanged:
		return 3
	case action.NeedsSecret:
		return 4
	default:
		return 1
	}
}

// Probe inspects every action of the resolved components without changing anything.
func (e *Engine) Probe(ctx context.Context, res manifest.Resolution, exclude map[string]bool) ([]ComponentStatus, error) {
	out := make([]ComponentStatus, 0, len(res.Order))
	for _, id := range res.Order {
		c := e.Manifest.Component(id)
		cs, err := e.probeComponent(ctx, c, exclude)
		if err != nil {
			return nil, fmt.Errorf("probe %s: %w", id, err)
		}
		cs.AddedBecause = res.AddedBecause[id]
		out = append(out, cs)
	}
	return out, nil
}

func (e *Engine) probeComponent(ctx context.Context, c *manifest.Component, exclude map[string]bool) (ComponentStatus, error) {
	cs := ComponentStatus{Component: c, State: action.OK}
	add := func(a ActionStatus) { cs.Actions = append(cs.Actions, a) }

	if c.Brewfile != "" {
		a, err := e.probeBrew(ctx, c.Brewfile, exclude)
		if err != nil {
			return cs, err
		}
		add(a)
	}
	if len(c.Apt) > 0 {
		p, err := action.Apt{Packages: c.Apt}.Probe(ctx, e.Runner)
		if err != nil {
			return cs, err
		}
		add(ActionStatus{Kind: KindApt, Target: strings.Join(c.Apt, " "), Status: p.Status, Detail: p.Detail, apt: &p})
	}
	for _, l := range c.Links {
		link := action.NewLink(e.Env, l)
		p, err := link.Probe()
		if err != nil {
			return cs, err
		}
		add(ActionStatus{Kind: KindLink, Target: l.Dst, Status: p.Status, Detail: p.Detail, link: &link, linkProbe: p})
	}
	for _, s := range c.Seeds {
		seed := action.NewSeed(e.Env, s)
		p, err := seed.Probe(ctx, e.Secrets, e.Local.Seeds[seed.Dst])
		if err != nil {
			return cs, err
		}
		add(ActionStatus{Kind: KindSeed, Target: s.Dst, Status: p.Status, Detail: p.Detail, seed: &seed, seedProbe: p})
	}
	if c.Layouts != nil {
		lay := action.NewLayouts(e.Env, *c.Layouts)
		p, err := lay.Probe(e.Host)
		if err != nil {
			return cs, err
		}
		add(ActionStatus{Kind: KindLayouts, Target: c.Layouts.Dst, Status: p.Status, Detail: p.Detail, layouts: &lay, layProbe: p})
	}
	for _, mc := range c.Cmds {
		cmd := action.Command{Cmd: mc, Repo: e.Env.Repo}
		p, err := cmd.Probe(ctx, e.Runner, e.Secrets)
		if err != nil {
			return cs, err
		}
		add(ActionStatus{Kind: KindCmd, Target: cmd.Label(), Status: p.Status, Detail: p.Detail, cmd: &cmd})
	}

	var notes []string
	for _, a := range cs.Actions {
		if severity(a.Status) > severity(cs.State) {
			cs.State = a.Status
		}
		if !a.Status.Settled() && a.Status != action.NoSnapshot {
			notes = append(notes, describe(a))
		}
	}
	if len(notes) == 0 {
		cs.Summary = "ok"
	} else {
		cs.Summary = strings.Join(notes, "; ")
	}
	return cs, nil
}

func describe(a ActionStatus) string {
	switch {
	case a.Kind == KindBrew || a.Kind == KindApt:
		return a.Detail
	case a.Kind == KindCmd:
		return a.Target + ": " + string(a.Status)
	case a.Detail != "":
		return fmt.Sprintf("%s %s (%s)", a.Target, a.Status, a.Detail)
	default:
		return fmt.Sprintf("%s %s", a.Target, a.Status)
	}
}

func (e *Engine) probeBrew(ctx context.Context, brewfile string, exclude map[string]bool) (ActionStatus, error) {
	b, err := action.NewBrew(e.Env, brewfile)
	if err != nil {
		return ActionStatus{}, err
	}
	a := ActionStatus{Kind: KindBrew, Target: brewfile, brew: &b}
	if err := e.loadInventory(ctx); err != nil {
		return a, err
	}
	if !e.hasBrew {
		// fresh machine: everything is missing; Homebrew installs before this runs
		p := action.BrewProbe{Status: action.Todo}
		for _, pkg := range b.Packages {
			if exclude[pkg.Name] {
				p.Excluded = append(p.Excluded, pkg)
			} else {
				p.Missing = append(p.Missing, pkg)
			}
		}
		if len(p.Missing) == 0 {
			p.Status = action.OK
		}
		p.Detail = fmt.Sprintf("%d to install (Homebrew not installed yet)", len(p.Missing))
		a.Status, a.Detail, a.brewProbe = p.Status, p.Detail, p
		return a, nil
	}
	p, err := b.Probe(ctx, e.Runner, e.inventory, exclude, e.AppPaths)
	if err != nil {
		return a, err
	}
	a.Status, a.Detail, a.brewProbe = p.Status, p.Detail, p
	return a, nil
}

// loadInventory lists Homebrew's packages once; missing Homebrew is not an error.
func (e *Engine) loadInventory(ctx context.Context) error {
	if e.brewKnown {
		return nil
	}
	res, err := e.Runner.Run(ctx, sh.Cmd{Script: "command -v brew"})
	if err != nil {
		return err
	}
	e.brewKnown = true
	e.hasBrew = res.Code == 0
	if !e.hasBrew {
		return nil
	}
	inv, err := action.LoadBrewInventory(ctx, e.Runner)
	if err != nil {
		return err
	}
	e.inventory = inv
	return nil
}

// ResetInventory forgets the cached Homebrew inventory (after installs, before a re-probe).
func (e *Engine) ResetInventory() { e.brewKnown, e.inventory = false, nil }

// NeedsSudo reports whether applying statuses will run sudo (apt, sudo cmds, casks).
func NeedsSudo(statuses []ComponentStatus) bool {
	for _, cs := range statuses {
		for _, a := range cs.Actions {
			if a.Status.Settled() {
				continue
			}
			switch a.Kind {
			case KindApt:
				if !action.AsRoot() {
					return true
				}
			case KindCmd:
				if a.cmd.Sudo {
					return true
				}
			case KindBrew:
				for _, pkg := range a.brewProbe.Missing {
					if pkg.Kind == action.KindCask {
						return true
					}
				}
			}
		}
	}
	return false
}

// Decisions holds the user's answers for actions that need one, keyed by ActionStatus.Key.
type Decisions struct {
	// Adopt lists link keys whose live file is copied into the repo (default: keep repo).
	Adopt map[string]bool
	// Overwrite lists seed keys whose live file is replaced (default for both-changed: keep).
	Overwrite map[string]bool
	// ImportFrom maps a layouts key to the host snapshot to import (default: no import).
	ImportFrom map[string]string
	// Running reports whether an app process runs (layouts import guard).
	Running func(process string) bool
}

// MissingSecrets lists secrets selected components need that are not set, sorted, unique.
func MissingSecrets(statuses []ComponentStatus, secrets *action.Secrets) []string {
	var want []string
	for _, cs := range statuses {
		for _, s := range cs.Component.Seeds {
			want = append(want, s.Secrets...)
		}
		for _, c := range cs.Component.Cmds {
			want = append(want, c.Secrets...)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range secrets.Missing(want) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}
