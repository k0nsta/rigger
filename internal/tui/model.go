// Package tui is rigger's interactive installer: pick a profile, toggle components and
// packages, resolve conflicts and secrets, then watch the run.
package tui

import (
	"context"
	"os/exec"
	"sort"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
)

// Profile is a profile the user can start from.
type Profile struct {
	Name, Description string
}

// Config is the static data the TUI shows.
type Config struct {
	Host      string
	HostIsNew bool
	Platform  string
	Repo      string
	Profiles  []Profile
	// Current is the selection saved for this host, if any.
	Current    *app.Selection
	Components []*manifest.Component
	Groups     []string
	Manual     func() []app.ManualItem
	// SudoCmd returns `sudo -v` to run on the terminal before applying; nil skips the prompt.
	SudoCmd func() *exec.Cmd
	Running func(process string) bool
}

type screen int

const (
	scrHost screen = iota
	scrProfile
	scrLoading
	scrComponents
	scrPackages
	scrReview
	scrSecret
	scrRunning
	scrFinish
	scrError
)

// row is a line of the component list: a group header or a component.
type row struct {
	group string
	comp  *manifest.Component
}

// decision is a review line the user can change.
type decision struct {
	comp   string
	action engine.ActionStatus
}

// Model is the Bubble Tea model.
type Model struct {
	cfg     Config
	planner Planner
	applier Applier
	store   Store
	ctx     context.Context
	cancel  context.CancelFunc

	width, height int
	screen        screen
	loadingText   string
	err           error
	spinner       spinner.Model
	input         textinput.Model

	profileCursor int
	profileName   string
	exclude       map[string]bool

	rows     []row
	cursor   int
	offset   int
	picked   map[string]bool
	statuses map[string]engine.ComponentStatus
	res      manifest.Resolution

	pkgComp   string
	pkgList   []action.BrewPackage
	pkgState  map[string]string
	pkgCursor int

	plan        []engine.ComponentStatus
	decisions   []decision
	revCursor   int
	adopt       map[string]bool
	overwrite   map[string]bool
	importFrom  map[string]string
	secretQueue []string

	events    chan engine.Event
	done      chan []engine.Outcome
	runState  map[string]engine.EventKind
	runErr    map[string]error
	current   string
	logTail   []string
	outcomes  []engine.Outcome
	saveError error
}

// New builds the model.
func New(ctx context.Context, cfg Config, p Planner, a Applier, s Store) *Model {
	ctx, cancel := context.WithCancel(ctx)
	ti := textinput.New()
	ti.CharLimit = 4096
	m := &Model{
		cfg: cfg, planner: p, applier: a, store: s, ctx: ctx, cancel: cancel,
		spinner:    spinner.New(spinner.WithSpinner(spinner.Dot)),
		input:      ti,
		picked:     map[string]bool{},
		exclude:    map[string]bool{},
		statuses:   map[string]engine.ComponentStatus{},
		adopt:      map[string]bool{},
		overwrite:  map[string]bool{},
		importFrom: map[string]string{},
		runState:   map[string]engine.EventKind{},
		runErr:     map[string]error{},
		width:      100,
		height:     30,
	}
	m.rows = buildRows(cfg.Components, cfg.Groups)
	if cfg.HostIsNew {
		m.screen = scrHost
		m.input.SetValue(cfg.Host)
		m.input.Placeholder = "laptop"
	} else {
		m.screen = scrProfile
	}
	return m
}

// Init starts the spinner and focuses the host input on first run.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if m.screen == scrHost {
		cmds = append(cmds, m.input.Focus())
	}
	return tea.Batch(cmds...)
}

// buildRows lists components under their groups in group order.
func buildRows(comps []*manifest.Component, groups []string) []row {
	byGroup := map[string][]*manifest.Component{}
	for _, c := range comps {
		byGroup[c.Group] = append(byGroup[c.Group], c)
	}
	order := append([]string(nil), groups...)
	seen := map[string]bool{}
	for _, g := range order {
		seen[g] = true
	}
	var extra []string
	for g := range byGroup {
		if !seen[g] {
			extra = append(extra, g)
		}
	}
	sort.Strings(extra)
	order = append(order, extra...)

	var rows []row
	for _, g := range order {
		if len(byGroup[g]) == 0 {
			continue
		}
		rows = append(rows, row{group: g})
		for _, c := range byGroup[g] {
			rows = append(rows, row{comp: c})
		}
	}
	return rows
}

// profileOptions are the entries of the profile screen; "" means the saved selection.
func (m *Model) profileOptions() []string {
	var opts []string
	if m.cfg.Current != nil {
		opts = append(opts, "")
	}
	for _, p := range m.cfg.Profiles {
		opts = append(opts, p.Name)
	}
	return opts
}

// pickedIDs returns the picked component ids in manifest order.
func (m *Model) pickedIDs() []string {
	var ids []string
	for _, c := range m.cfg.Components {
		if m.picked[c.ID] {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// allIDs are all platform components (for the first probe).
func (m *Model) allIDs() []string {
	ids := make([]string, 0, len(m.cfg.Components))
	for _, c := range m.cfg.Components {
		ids = append(ids, c.ID)
	}
	return ids
}

// selection is what gets saved and applied.
func (m *Model) selection() app.Selection {
	ex := map[string]bool{}
	for k, v := range m.exclude {
		if v {
			ex[k] = true
		}
	}
	return app.Selection{Profile: m.profileName, Picked: m.pickedIDs(), Exclude: ex}
}

// Outcomes after the run (for the caller's exit code).
func (m *Model) Outcomes() []engine.Outcome { return m.outcomes }

// Err is the error that stopped the TUI, if any.
func (m *Model) Err() error { return m.err }
