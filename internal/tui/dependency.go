package tui

//go:generate go run go.uber.org/mock/mockgen -source=dependency.go -destination=mocks/dependency_mock.go -package=mocks

import (
	"context"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
)

// Planner turns a selection into probed component statuses.
type Planner interface {
	Select(profile string, only []string) (app.Selection, error)
	Plan(ctx context.Context, sel app.Selection) (manifest.Resolution, []engine.ComponentStatus, error)
	MissingSecrets(statuses []engine.ComponentStatus) []string
}

// Applier runs the plan.
type Applier interface {
	Apply(ctx context.Context, statuses []engine.ComponentStatus, d engine.Decisions, emit func(engine.Event)) []engine.Outcome
}

// Store persists what the user decided.
type Store interface {
	SetHost(name string) (string, error)
	SetSecret(key, value string) error
	Save(sel app.Selection) error
}
