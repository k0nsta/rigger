package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/k0nsta/rigger/internal/action"
)

// EventKind is what happened to a component during apply.
type EventKind string

// Event kinds.
const (
	Started EventKind = "started"
	Line    EventKind = "line"
	Done    EventKind = "done"
	Failed  EventKind = "failed"
	Skipped EventKind = "skipped"
)

// Event reports apply progress for one component.
type Event struct {
	Component string
	Kind      EventKind
	Text      string
	Err       error
}

// Outcome is the final result of one component.
type Outcome struct {
	Component string
	Kind      EventKind // Done, Failed or Skipped
	Err       error
}

// Apply runs every unsettled action of statuses in order. A failed component makes the
// components that require it skip; others continue. Events are sent when emit is non-nil.
func (e *Engine) Apply(ctx context.Context, statuses []ComponentStatus, d Decisions, emit func(Event)) []Outcome {
	if emit == nil {
		emit = func(Event) {}
	}
	failed := map[string]bool{}
	outcomes := make([]Outcome, 0, len(statuses))
	for _, cs := range statuses {
		id := cs.ID()
		if blocker := firstFailed(cs, failed); blocker != "" {
			failed[id] = true
			err := fmt.Errorf("needs %s", blocker)
			emit(Event{Component: id, Kind: Skipped, Err: err})
			outcomes = append(outcomes, Outcome{Component: id, Kind: Skipped, Err: err})
			continue
		}
		if cs.State == action.NeedsSecret {
			failed[id] = true
			err := fmt.Errorf("waiting for a secret: %s", cs.Summary)
			emit(Event{Component: id, Kind: Skipped, Err: err})
			outcomes = append(outcomes, Outcome{Component: id, Kind: Skipped, Err: err})
			continue
		}
		if cs.Settled() {
			emit(Event{Component: id, Kind: Done, Text: "already ok"})
			outcomes = append(outcomes, Outcome{Component: id, Kind: Done})
			continue
		}

		emit(Event{Component: id, Kind: Started})
		onLine := func(l string) { emit(Event{Component: id, Kind: Line, Text: l}) }
		if err := e.applyComponent(ctx, cs, d, onLine); err != nil {
			failed[id] = true
			emit(Event{Component: id, Kind: Failed, Err: err})
			outcomes = append(outcomes, Outcome{Component: id, Kind: Failed, Err: err})
			if errors.Is(err, context.Canceled) {
				break
			}
			continue
		}
		if cs.Component.Brewfile != "" || len(cs.Component.Cmds) > 0 {
			e.ResetInventory() // a command may have installed Homebrew or packages
		}
		emit(Event{Component: id, Kind: Done})
		outcomes = append(outcomes, Outcome{Component: id, Kind: Done})
	}
	return outcomes
}

func firstFailed(cs ComponentStatus, failed map[string]bool) string {
	for _, r := range cs.Component.Requires {
		if failed[r] {
			return r
		}
	}
	return ""
}

func (e *Engine) applyComponent(ctx context.Context, cs ComponentStatus, d Decisions, onLine func(string)) error {
	for _, a := range cs.Actions {
		if a.Status.Settled() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.applyAction(ctx, a, d, onLine); err != nil {
			return fmt.Errorf("%s %s: %w", a.Kind, a.Target, err)
		}
	}
	return nil
}

func (e *Engine) applyAction(ctx context.Context, a ActionStatus, d Decisions, onLine func(string)) error {
	switch a.Kind {
	case KindBrew:
		onLine("brew bundle " + a.Target)
		return a.brew.Apply(ctx, e.Runner, a.brewProbe, onLine)
	case KindApt:
		onLine("apt-get install " + a.Target)
		return action.AptInstall(ctx, e.Runner, a.apt.Missing, onLine)
	case KindLink:
		mode := action.KeepRepo
		if d.Adopt[a.Key()] {
			mode = action.AdoptLive
		}
		onLine(fmt.Sprintf("link %s (%s)", a.Target, a.Status))
		return a.link.Apply(e.Env, mode)
	case KindSeed:
		mode := action.Keep
		if d.Overwrite[a.Key()] {
			mode = action.Overwrite
		}
		onLine(fmt.Sprintf("seed %s (%s)", a.Target, a.Status))
		hash, err := a.seed.Apply(e.Env, a.seedProbe, mode)
		if err != nil {
			return err
		}
		e.Local.Seeds[a.seed.Dst] = hash
		return nil
	case KindLayouts:
		from := d.ImportFrom[a.Key()]
		if from == "" {
			return nil
		}
		running := d.Running != nil && a.layouts.Process != "" && d.Running(a.layouts.Process)
		n, err := a.layouts.Import(e.Env, from, running)
		if err == nil {
			onLine(fmt.Sprintf("imported %d layout files from %s", n, from))
		}
		return err
	case KindCmd:
		onLine("$ " + a.cmd.Run)
		return a.cmd.Apply(ctx, e.Runner, e.Secrets, onLine)
	default:
		return fmt.Errorf("unknown action kind %q", a.Kind)
	}
}
