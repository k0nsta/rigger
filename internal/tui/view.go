package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/engine"
)

// View renders the current screen.
func (m *Model) View() tea.View {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")
	switch m.screen {
	case scrHost:
		m.viewHost(&b)
	case scrProfile:
		m.viewProfile(&b)
	case scrLoading:
		fmt.Fprintf(&b, "%s %s…\n", m.spinner.View(), m.loadingText)
	case scrComponents:
		m.viewComponents(&b)
	case scrPackages:
		m.viewPackages(&b)
	case scrReview:
		m.viewReview(&b)
	case scrSecret:
		m.viewSecret(&b)
	case scrRunning:
		m.viewRun(&b)
	case scrFinish:
		m.viewFinish(&b)
	case scrError:
		fmt.Fprintf(&b, "%s %v\n\n%s", badStyle.Render("error:"), m.err, helpStyle.Render("q quit"))
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	v.WindowTitle = "rigger · " + m.cfg.Host
	return v
}

func (m *Model) header() string {
	profile := m.profileName
	if profile == "" {
		profile = "—"
	}
	return titleStyle.Render("rigger") + dimStyle.Render(fmt.Sprintf("  %s · %s · profile %s · %s", m.cfg.Host, m.cfg.Platform, profile, m.cfg.Repo))
}

func (m *Model) viewHost(b *strings.Builder) {
	b.WriteString(headerStyle.Render("Name this machine") + "\n")
	b.WriteString(dimStyle.Render("Its picks are saved as hosts/<name>.yaml in the setup repo, so reruns and other machines see them.") + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	b.WriteString(helpStyle.Render("enter continue · ctrl+c quit"))
}

func (m *Model) viewProfile(b *strings.Builder) {
	b.WriteString(headerStyle.Render("Start from") + "\n\n")
	for i, name := range m.profileOptions() {
		label, desc := name, ""
		if name == "" {
			label = "saved selection"
			desc = fmt.Sprintf("hosts/%s.yaml (profile %s)", m.cfg.Host, m.cfg.Current.Profile)
		} else {
			for _, p := range m.cfg.Profiles {
				if p.Name == name {
					desc = p.Description
				}
			}
		}
		line := fmt.Sprintf("%-16s %s", label, dimStyle.Render(desc))
		if i == m.profileCursor {
			b.WriteString(cursorStyle.Render("› ") + selectedStyle.Render(line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n" + helpStyle.Render("↑↓ move · enter choose · every component and package can be changed next · q quit"))
}

// visible returns the first and last row index that fit, keeping the cursor on screen.
func (m *Model) visible(n, cursor, reserved int) (int, int) {
	room := max(5, m.height-reserved)
	if cursor < m.offset {
		m.offset = cursor
	}
	if cursor >= m.offset+room {
		m.offset = cursor - room + 1
	}
	m.offset = max(0, min(m.offset, max(0, n-room)))
	return m.offset, min(n, m.offset+room)
}

func (m *Model) viewComponents(b *strings.Builder) {
	added := m.res.AddedBecause
	from, to := m.visible(len(m.rows), m.cursor, 12)
	for i := from; i < to; i++ {
		r := m.rows[i]
		if r.comp == nil {
			b.WriteString(headerStyle.Render(strings.ToUpper(r.group)) + "\n")
			continue
		}
		c := r.comp
		box := "[ ]"
		switch {
		case m.picked[c.ID]:
			box = "[x]"
		case added[c.ID] != "":
			box = dimStyle.Render("[+]")
		}
		cs := m.statuses[c.ID]
		state := statusStyle(cs.State).Render(fmt.Sprintf("%-13s", cs.State))
		note := cs.Summary
		if note == "ok" {
			note = ""
		}
		if added[c.ID] != "" && !m.picked[c.ID] {
			note = "needed by " + added[c.ID] + " · " + note
		}
		if c.Brewfile != "" {
			if n := m.excludedIn(cs); n > 0 {
				note += fmt.Sprintf(" · %d unpicked", n)
			}
		}
		line := fmt.Sprintf("%s %-26s %s %s", box, truncate(c.Name(), 26), state, dimStyle.Render(truncate(note, max(20, m.width-50))))
		if i == m.cursor {
			b.WriteString(cursorStyle.Render("› ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	if c := m.rows[m.cursor].comp; c != nil && c.Description != "" {
		b.WriteString("\n" + dimStyle.Render(c.Description) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("space toggle · enter packages (Brewfiles) · a all · n none · c continue · esc back · q quit"))
}

func (m *Model) excludedIn(cs engine.ComponentStatus) int {
	n := 0
	for _, a := range cs.Actions {
		for _, p := range a.BrewPackages() {
			if m.exclude[p.Name] && m.neededBy(p.Name) == "" {
				n++
			}
		}
	}
	return n
}

func (m *Model) viewPackages(b *strings.Builder) {
	b.WriteString(headerStyle.Render("Packages in "+m.pkgComp) + dimStyle.Render("  (unpicked ones are skipped and remembered for this host)") + "\n\n")
	from, to := m.visible(len(m.pkgList), m.pkgCursor, 10)
	for i := from; i < to; i++ {
		p := m.pkgList[i]
		box, comment := "[x]", p.Comment
		if by := m.neededBy(p.Name); by != "" {
			box, comment = "[■]", "needed by "+by
		} else if m.exclude[p.Name] {
			box = "[ ]"
		}
		state := m.pkgState[p.Name]
		padded := fmt.Sprintf("%-10s", state)
		styled := dimStyle.Render(padded)
		switch state {
		case "installed", "present":
			styled = okStyle.Render(padded)
		case "missing":
			styled = todoStyle.Render(padded)
		}
		line := fmt.Sprintf("%s %-5s %-34s %s %s", box, p.Kind, truncate(p.Name, 34), styled, dimStyle.Render(comment))
		if i == m.pkgCursor {
			b.WriteString(cursorStyle.Render("› ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n" + helpStyle.Render("space toggle · a all · n none · esc back"))
}

func (m *Model) viewReview(b *strings.Builder) {
	pending := m.pending()
	b.WriteString(headerStyle.Render(fmt.Sprintf("Plan: %d component(s) will change", pending)) + "\n\n")
	for _, cs := range m.plan {
		if cs.Settled() {
			continue
		}
		b.WriteString(statusStyle(cs.State).Render("● ") + cs.Component.Name())
		if cs.AddedBecause != "" {
			b.WriteString(dimStyle.Render("  needed by " + cs.AddedBecause))
		}
		b.WriteString("\n")
		for _, a := range cs.Actions {
			if a.Status.Settled() || a.Status == action.NoSnapshot {
				continue
			}
			fmt.Fprintf(b, "    %-7s %-44s %s\n", a.Kind, truncate(a.Target, 44), statusStyle(a.Status).Render(string(a.Status)))
		}
	}
	if pending == 0 {
		b.WriteString(okStyle.Render("Everything picked is already in place.") + "\n")
	}

	if len(m.decisions) > 0 {
		b.WriteString("\n" + headerStyle.Render("Decide") + "\n")
		for i, d := range m.decisions {
			line := fmt.Sprintf("%-28s %s", d.action.Target, m.decisionLabel(d))
			if i == m.revCursor {
				b.WriteString(cursorStyle.Render("› ") + line + "\n")
			} else {
				b.WriteString("  " + line + "\n")
			}
		}
	}
	if missing := m.planner.MissingSecrets(m.plan); len(missing) > 0 {
		b.WriteString("\n" + todoStyle.Render("Will ask for secrets: "+strings.Join(missing, ", ")) + "\n")
	}
	if engine.NeedsSudo(m.plan) {
		b.WriteString(dimStyle.Render("Some steps need sudo; rigger asks for the password once, when the first one does.") + "\n")
	}
	help := "enter apply · esc back · ctrl+c quit"
	if len(m.decisions) > 0 {
		help = "space change decision · " + help
	}
	b.WriteString("\n" + helpStyle.Render(help))
}

func (m *Model) decisionLabel(d decision) string {
	key := d.action.Key()
	switch d.action.Status {
	case action.Differs:
		if m.adopt[key] {
			return okStyle.Render("adopt this machine's file into the repo")
		}
		return todoStyle.Render("keep the repo version (this file is backed up)")
	case action.BothChanged:
		if m.overwrite[key] {
			return todoStyle.Render("overwrite with the template (backed up)")
		}
		return okStyle.Render("keep this machine's file")
	case action.FreshTarget:
		if from := m.importFrom[key]; from != "" {
			return okStyle.Render("import layouts from " + from)
		}
		return dimStyle.Render("do not import layouts")
	}
	return ""
}

func (m *Model) viewSecret(b *strings.Builder) {
	b.WriteString(headerStyle.Render("Secret "+m.secretQueue[0]) + "\n")
	b.WriteString(dimStyle.Render("Stored in ~/.config/rigger/secrets.env (0600, never committed). An op://vault/item/field reference works too.") + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	b.WriteString(helpStyle.Render(fmt.Sprintf("enter save · empty skips · %d left · esc back", len(m.secretQueue))))
}

func (m *Model) viewRun(b *strings.Builder) {
	if m.pwReq != nil {
		b.WriteString(todoStyle.Render("🔒 "+m.pwReq.Message) + "\n")
		b.WriteString(m.input.View() + "\n")
		b.WriteString(helpStyle.Render("enter send · esc cancel this step · kept in memory for this run only") + "\n\n")
	}
	b.WriteString(headerStyle.Render("Applying") + "\n\n")
	for _, cs := range m.plan {
		id := cs.ID()
		var mark string
		switch m.runState[id] {
		case engine.Started:
			mark = m.spinner.View()
		case engine.Done:
			mark = okStyle.Render("✓")
		case engine.Failed:
			mark = badStyle.Render("✗")
		case engine.Skipped:
			mark = todoStyle.Render("–")
		default:
			mark = dimStyle.Render("·")
		}
		line := mark + " " + cs.Component.Name()
		if err := m.runErr[id]; err != nil {
			line += dimStyle.Render("  " + truncate(err.Error(), max(20, m.width-30)))
		}
		b.WriteString(line + "\n")
	}
	if m.current != "" && len(m.logTail) > 0 {
		b.WriteString("\n" + dimStyle.Render(m.current+":") + "\n")
		tail := m.logTail
		if n := max(3, m.height-len(m.plan)-10); len(tail) > n {
			tail = tail[len(tail)-n:]
		}
		for _, l := range tail {
			b.WriteString(dimStyle.Render("  "+truncate(l, max(20, m.width-4))) + "\n")
		}
	}
}

func (m *Model) viewFinish(b *strings.Builder) {
	var done, failed, skipped int
	for _, o := range m.outcomes {
		switch o.Kind {
		case engine.Done:
			done++
		case engine.Failed:
			failed++
		case engine.Skipped:
			skipped++
		}
	}
	summary := fmt.Sprintf("%d done", done)
	if failed > 0 {
		summary += badStyle.Render(fmt.Sprintf(" · %d failed", failed))
	}
	if skipped > 0 {
		summary += todoStyle.Render(fmt.Sprintf(" · %d skipped", skipped))
	}
	if len(m.outcomes) == 0 {
		summary = "nothing to change"
	}
	b.WriteString(headerStyle.Render("Finished: ") + summary + "\n")
	for _, o := range m.outcomes {
		if o.Err == nil {
			continue
		}
		fmt.Fprintf(b, "  %s %s: %v\n", badStyle.Render("•"), o.Component, o.Err)
		if o.Kind == engine.Failed {
			lines := m.compLog[o.Component]
			if len(lines) > 8 {
				lines = lines[len(lines)-8:]
			}
			for _, l := range lines {
				b.WriteString(dimStyle.Render("      "+truncate(l, max(20, m.width-8))) + "\n")
			}
		}
	}
	if m.logPath != "" {
		b.WriteString(dimStyle.Render("Full log: "+m.logPath) + "\n")
	}
	if m.saveError != nil {
		b.WriteString(badStyle.Render("could not save the selection: "+m.saveError.Error()) + "\n")
	} else {
		b.WriteString(dimStyle.Render(fmt.Sprintf("Selection saved to hosts/%s.yaml — commit it with `rigger sync`.", m.cfg.Host)) + "\n")
	}

	if m.cfg.Manual != nil {
		if items := m.cfg.Manual(); len(items) > 0 {
			b.WriteString("\n" + headerStyle.Render("Move by hand") + "\n")
			for _, it := range items {
				mark := todoStyle.Render("✗")
				if it.Exists {
					mark = okStyle.Render("✓")
				}
				fmt.Fprintf(b, "  %s %-40s %s\n", mark, it.Path, dimStyle.Render(it.Note))
			}
		}
	}
	b.WriteString("\n" + helpStyle.Render("q quit"))
}

func truncate(s string, n int) string {
	if n <= 1 || len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
