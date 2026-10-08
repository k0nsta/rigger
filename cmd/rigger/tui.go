package main

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/repo"
	"github.com/k0nsta/rigger/internal/tui"
)

// interactive runs the TUI for the session the flags select.
func (c *cli) interactive(ctx context.Context, f sessionFlags) error {
	s, err := app.Open(app.Options{Repo: f.repo, Host: f.host, Home: c.home})
	if err != nil {
		return err
	}
	cfg := tui.Config{
		Host:       s.Host,
		HostIsNew:  s.HostIsNew,
		Platform:   s.Platform,
		Repo:       s.Repo,
		Components: s.PlatformComponents(),
		Groups:     s.Manifest.GroupOrder(),
		Manual:     s.Manual,
		Running:    processRunning(ctx, s.Exec),
	}
	passwords := make(chan tui.PasswordRequest)
	cfg.Passwords = passwords
	stopAskpass, err := s.StartAskpass(tui.AskFunc(ctx, passwords))
	if err != nil {
		return err
	}
	defer stopAskpass()
	for _, name := range s.Profiles() {
		cfg.Profiles = append(cfg.Profiles, tui.Profile{Name: name, Description: s.Manifest.Profiles[name].Description})
	}
	if s.HostFile != nil && f.profile == "" {
		if sel, err := s.Select("", nil); err == nil {
			cfg.Current = &sel
		}
	}

	model := tui.New(ctx, cfg, s, s, s)
	if _, err := tea.NewProgram(model, tea.WithContext(ctx)).Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	if err := model.Err(); err != nil {
		return err
	}
	if switched, err := repo.UseSSHOrigin(ctx, s.Exec, s.Repo); err == nil && switched {
		fmt.Fprintln(c.out, "origin switched to SSH")
	}
	for _, o := range model.Outcomes() {
		if o.Kind != engine.Done {
			return exitCode(1)
		}
	}
	return nil
}
