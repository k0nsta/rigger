package tui

import (
	"slices"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
)

type (
	probedMsg struct {
		statuses []engine.ComponentStatus
		err      error
	}
	plannedMsg struct {
		statuses []engine.ComponentStatus
		err      error
	}
	replannedMsg struct {
		statuses []engine.ComponentStatus
		err      error
	}
	eventMsg    engine.Event
	passwordMsg PasswordRequest
	runDoneMsg  struct{ outcomes []engine.Outcome }
)

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case probedMsg:
		return m.onProbed(msg)
	case plannedMsg:
		return m.onPlanned(msg)
	case replannedMsg:
		if msg.err != nil {
			return m.fail(msg.err)
		}
		m.plan = msg.statuses
		return m.startRun()
	case passwordMsg:
		req := PasswordRequest(msg)
		m.pwReq = &req
		m.input.SetValue("")
		m.input.EchoMode = textinput.EchoPassword
		m.input.Placeholder = ""
		return m, tea.Batch(m.input.Focus(), m.waitEvent())
	case eventMsg:
		return m.onEvent(engine.Event(msg))
	case runDoneMsg:
		return m.onRunDone(msg)
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.cancel()
			return m, tea.Quit
		}
		return m.onKey(msg)
	}
	if m.screen == scrHost || m.screen == scrSecret || m.pwReq != nil {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) fail(err error) (tea.Model, tea.Cmd) {
	m.err, m.screen = err, scrError
	return m, nil
}

func isSpace(k string) bool { return k == "space" || k == " " }

func (m *Model) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if m.pwReq != nil {
		return m.onPasswordKey(k)
	}
	switch m.screen {
	case scrHost:
		if key == "enter" {
			name, err := m.store.SetHost(m.input.Value())
			if err != nil {
				return m.fail(err)
			}
			m.cfg.Host = name
			m.input.Blur()
			m.screen = scrProfile
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(k)
		return m, cmd

	case scrProfile:
		opts := m.profileOptions()
		switch key {
		case "up", "k":
			m.profileCursor = max(0, m.profileCursor-1)
		case "down", "j":
			m.profileCursor = min(len(opts)-1, m.profileCursor+1)
		case "q", "esc":
			return m, tea.Quit
		case "enter":
			return m.chooseProfile(opts[m.profileCursor])
		}

	case scrComponents:
		return m.onComponentsKey(key)

	case scrPackages:
		switch key {
		case "up", "k":
			m.pkgCursor = max(0, m.pkgCursor-1)
		case "down", "j":
			m.pkgCursor = min(len(m.pkgList)-1, m.pkgCursor+1)
		case "a":
			for _, p := range m.pkgList {
				delete(m.exclude, p.Name)
			}
		case "n":
			for _, p := range m.pkgList {
				m.exclude[p.Name] = true
			}
		case "esc", "enter", "left", "h":
			m.screen = scrComponents
		default:
			if isSpace(key) && len(m.pkgList) > 0 {
				name := m.pkgList[m.pkgCursor].Name
				m.exclude[name] = !m.exclude[name]
			}
		}

	case scrReview:
		switch key {
		case "up", "k":
			m.revCursor = max(0, m.revCursor-1)
		case "down", "j":
			m.revCursor = min(len(m.decisions)-1, m.revCursor+1)
		case "esc", "left", "h":
			m.screen = scrComponents
		case "enter":
			return m.confirm()
		default:
			if isSpace(key) && len(m.decisions) > 0 {
				m.toggleDecision(m.decisions[m.revCursor])
			}
		}

	case scrSecret:
		if key == "enter" {
			name := m.secretQueue[0]
			if v := m.input.Value(); v != "" {
				if err := m.store.SetSecret(name, v); err != nil {
					return m.fail(err)
				}
			}
			m.secretQueue = m.secretQueue[1:]
			m.input.SetValue("")
			if len(m.secretQueue) > 0 {
				return m, nil
			}
			m.input.Blur()
			return m.replanThenRun()
		}
		if key == "esc" {
			m.input.Blur()
			m.screen = scrReview
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(k)
		return m, cmd

	case scrFinish, scrError:
		if key == "q" || key == "enter" || key == "esc" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *Model) chooseProfile(name string) (tea.Model, tea.Cmd) {
	sel, err := m.planner.Select(name, nil)
	if err != nil {
		return m.fail(err)
	}
	m.profileName = sel.Profile
	m.picked = map[string]bool{}
	for _, id := range sel.Picked {
		m.picked[id] = true
	}
	m.exclude = map[string]bool{}
	for k := range sel.Exclude {
		m.exclude[k] = true
	}
	m.screen, m.loadingText = scrLoading, "checking this machine"
	all := app.Selection{Picked: m.allIDs(), Exclude: m.exclude}
	return m, m.planCmd(all, true)
}

func (m *Model) planCmd(sel app.Selection, all bool) tea.Cmd {
	return func() tea.Msg {
		_, statuses, err := m.planner.Plan(m.ctx, sel)
		if all {
			return probedMsg{statuses: statuses, err: err}
		}
		return plannedMsg{statuses: statuses, err: err}
	}
}

func (m *Model) onProbed(msg probedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err)
	}
	for _, cs := range msg.statuses {
		m.statuses[cs.ID()] = cs
	}
	m.screen = scrComponents
	m.cursor = m.firstComponentRow()
	m.resolve()
	return m, nil
}

func (m *Model) firstComponentRow() int {
	for i, r := range m.rows {
		if r.comp != nil {
			return i
		}
	}
	return 0
}

// resolve recomputes the auto-added prerequisites of the picked components.
func (m *Model) resolve() {
	res, err := m.cfgResolve(m.pickedIDs())
	if err == nil {
		m.res = res
	}
}

// cfgResolve resolves without the manifest by walking Requires of the known components.
func (m *Model) cfgResolve(picked []string) (manifest.Resolution, error) {
	mf := &manifest.Manifest{}
	for _, c := range m.cfg.Components {
		mf.Components = append(mf.Components, *c)
	}
	return mf.Resolve(m.cfg.Platform, picked)
}

func (m *Model) onComponentsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "a":
		for _, c := range m.cfg.Components {
			m.picked[c.ID] = true
		}
		m.resolve()
	case "n":
		m.picked = map[string]bool{}
		m.resolve()
	case "enter", "right", "l":
		if c := m.rows[m.cursor].comp; c != nil && c.Brewfile != "" {
			m.openPackages(c)
		}
	case "esc":
		m.screen = scrProfile
	case "q":
		return m, tea.Quit
	case "c", "tab":
		ids := m.pickedIDs()
		if len(ids) == 0 {
			return m, nil
		}
		m.screen, m.loadingText = scrLoading, "planning"
		return m, m.planCmd(m.selection(), false)
	default:
		if isSpace(key) {
			if c := m.rows[m.cursor].comp; c != nil {
				m.picked[c.ID] = !m.picked[c.ID]
				m.resolve()
			}
		}
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	i := m.cursor
	for {
		i += delta
		if i < 0 || i >= len(m.rows) {
			return
		}
		if m.rows[i].comp != nil {
			m.cursor = i
			return
		}
	}
}

func (m *Model) openPackages(c *manifest.Component) {
	m.pkgComp, m.pkgCursor = c.ID, 0
	m.pkgList, m.pkgState = nil, map[string]string{}
	for _, a := range m.statuses[c.ID].Actions {
		p, ok := a.Brew()
		if !ok {
			continue
		}
		for state, list := range map[string][]action.BrewPackage{"installed": p.Installed, "missing": p.Missing, "present": p.Present} {
			for _, pkg := range list {
				m.pkgState[pkg.Name] = state
			}
		}
		m.pkgList = a.BrewPackages()
	}
	m.screen = scrPackages
}

func (m *Model) onPlanned(msg plannedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err)
	}
	m.plan = msg.statuses
	m.decisions = nil
	for _, cs := range m.plan {
		for _, a := range cs.Actions {
			switch a.Status {
			case action.Differs, action.BothChanged, action.FreshTarget:
				m.decisions = append(m.decisions, decision{comp: cs.ID(), action: a})
			}
		}
	}
	m.revCursor = 0
	m.screen = scrReview
	return m, nil
}

func (m *Model) toggleDecision(d decision) {
	key := d.action.Key()
	switch d.action.Status {
	case action.Differs:
		m.adopt[key] = !m.adopt[key]
	case action.BothChanged:
		m.overwrite[key] = !m.overwrite[key]
	case action.FreshTarget:
		p, _ := d.action.Layouts()
		opts := append([]string{""}, p.Hosts...)
		i := slices.Index(opts, m.importFrom[key])
		m.importFrom[key] = opts[(i+1)%len(opts)]
	}
}

// pending counts components that will change.
func (m *Model) pending() int {
	n := 0
	for _, cs := range m.plan {
		if !cs.Settled() {
			n++
		}
	}
	return n
}

func (m *Model) confirm() (tea.Model, tea.Cmd) {
	if m.pending() == 0 && len(m.importFrom) == 0 {
		m.outcomes = nil
		return m.finish()
	}
	if missing := m.planner.MissingSecrets(m.plan); len(missing) > 0 {
		m.secretQueue = missing
		m.input.SetValue("")
		m.input.EchoMode = textinput.EchoPassword
		m.input.Placeholder = "empty skips"
		m.screen = scrSecret
		return m, m.input.Focus()
	}
	return m.startRun()
}

// replanThenRun probes again after secrets were entered, then goes straight to the run.
func (m *Model) replanThenRun() (tea.Model, tea.Cmd) {
	m.screen, m.loadingText = scrLoading, "planning"
	sel := m.selection()
	return m, func() tea.Msg {
		_, statuses, err := m.planner.Plan(m.ctx, sel)
		return replannedMsg{statuses: statuses, err: err}
	}
}

func (m *Model) startRun() (tea.Model, tea.Cmd) {
	m.screen = scrRunning
	m.openLog()
	m.events = make(chan engine.Event, 64)
	m.done = make(chan []engine.Outcome, 1)
	d := engine.Decisions{Adopt: m.adopt, Overwrite: m.overwrite, ImportFrom: m.importFrom, Running: m.cfg.Running}
	plan, events, done := m.plan, m.events, m.done
	go func() {
		out := m.applier.Apply(m.ctx, plan, d, func(ev engine.Event) { events <- ev })
		done <- out
		close(events)
	}()
	return m, m.waitEvent()
}

func (m *Model) waitEvent() tea.Cmd {
	events, done, passwords := m.events, m.done, m.cfg.Passwords
	return func() tea.Msg {
		select {
		case ev, ok := <-events:
			if !ok {
				return runDoneMsg{outcomes: <-done}
			}
			return eventMsg(ev)
		case req := <-passwords:
			return passwordMsg(req)
		}
	}
}

// onPasswordKey edits the sudo password overlay; enter answers, esc cancels the sudo step.
func (m *Model) onPasswordKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		m.pwReq.Reply <- PasswordReply{Password: m.input.Value(), OK: m.input.Value() != ""}
	case "esc":
		m.pwReq.Reply <- PasswordReply{}
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(k)
		return m, cmd
	}
	m.pwReq = nil
	m.input.SetValue("")
	m.input.Blur()
	return m, nil
}

func (m *Model) onEvent(ev engine.Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case engine.Started:
		m.current, m.logTail = ev.Component, nil
		m.runState[ev.Component] = engine.Started
		m.logLine("==> %s", ev.Component)
	case engine.Line:
		m.logTail = append(m.logTail, ev.Text)
		if len(m.logTail) > 200 {
			m.logTail = m.logTail[len(m.logTail)-200:]
		}
		lines := append(m.compLog[ev.Component], ev.Text)
		if len(lines) > 40 {
			lines = lines[len(lines)-40:]
		}
		m.compLog[ev.Component] = lines
		m.logLine("    %s", ev.Text)
	case engine.Done, engine.Failed, engine.Skipped:
		m.runState[ev.Component] = ev.Kind
		if ev.Err != nil {
			m.runErr[ev.Component] = ev.Err
			m.logLine("    %s: %v", ev.Kind, ev.Err)
		}
	}
	return m, m.waitEvent()
}

func (m *Model) onRunDone(msg runDoneMsg) (tea.Model, tea.Cmd) {
	m.outcomes = msg.outcomes
	return m.finish()
}

func (m *Model) finish() (tea.Model, tea.Cmd) {
	m.closeLog()
	m.saveError = m.store.Save(m.selection())
	m.screen = scrFinish
	return m, nil
}
