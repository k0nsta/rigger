package action

import (
	"context"
	"fmt"
	"strings"

	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/sh"
)

// Command is a manifest cmd: run from the repo root unless its check already passes.
type Command struct {
	manifest.Cmd
	Repo string
}

// Label is the name shown in the TUI.
func (c Command) Label() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Run
}

// CommandProbe is the probed state of a command.
type CommandProbe struct {
	Status Status
	Detail string
}

// Probe runs the check (read-only by convention); no check means the command always runs.
func (c Command) Probe(ctx context.Context, r Runner, secrets *Secrets) (CommandProbe, error) {
	if missing := secrets.Missing(c.Secrets); len(missing) > 0 {
		return CommandProbe{Status: NeedsSecret, Detail: "needs " + strings.Join(missing, ", ")}, nil
	}
	if c.Check == "" {
		return CommandProbe{Status: Todo, Detail: "runs every time"}, nil
	}
	res, err := r.Run(ctx, sh.Cmd{Script: c.Check, Dir: c.Repo})
	if err != nil {
		return CommandProbe{}, err
	}
	if res.Code == 0 {
		return CommandProbe{Status: OK}, nil
	}
	return CommandProbe{Status: Todo}, nil
}

// Apply runs the command with its secrets exported.
func (c Command) Apply(ctx context.Context, r Runner, secrets *Secrets, onLine func(string)) error {
	env, err := secrets.Env(ctx, c.Secrets)
	if err != nil {
		return err
	}
	res, err := r.Run(ctx, sh.Cmd{Script: c.Run, Dir: c.Repo, Env: env, OnLine: onLine})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("%s failed (exit %d)", c.Label(), res.Code)
	}
	return nil
}
