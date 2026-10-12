package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/repo"
	"github.com/k0nsta/rigger/internal/sh"
)

func (c *cli) apply(ctx context.Context, args []string) error {
	var f sessionFlags
	var yes, dryRun bool
	fs := newFlagSet("apply", c.err)
	f.register(fs)
	fs.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	fs.BoolVar(&dryRun, "dry-run", false, "show the plan and stop")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !yes && !dryRun && !c.isTTY {
		return errors.New("stdin is not a terminal: pass --yes to apply without asking")
	}

	s, sel, err := f.open(c.home)
	if err != nil {
		return err
	}
	res, statuses, err := s.Plan(ctx, sel)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s · %s · profile %s\n\n", s.Host, s.Platform, orNone(sel.Profile))
	printStatuses(c.out, statuses, res)

	if missing := engine.MissingSecrets(statuses, s.Secrets); len(missing) > 0 {
		fmt.Fprintf(c.out, "\nmissing secrets (components needing them are skipped): %s\n", strings.Join(missing, ", "))
	}
	pending := 0
	for _, cs := range statuses {
		switch {
		case cs.OfferOnly():
			fmt.Fprintf(c.out, "\n%s: %s (pick it in the TUI: rigger)\n", cs.ID(), cs.Summary)
		case !cs.Settled():
			pending++
		}
	}
	if dryRun {
		fmt.Fprintf(c.out, "\ndry run: %d component(s) would change\n", pending)
		return nil
	}
	if pending == 0 {
		fmt.Fprintln(c.out, "\nnothing to do")
		return s.Save(sel)
	}
	if !yes && !c.confirm(fmt.Sprintf("\napply %d component(s)? [y/N] ", pending)) {
		return errors.New("cancelled")
	}
	logf, err := openRunLog(s.Home)
	if err != nil {
		return err
	}
	defer func() {
		fmt.Fprintf(c.out, "log: %s\n", logf.Name())
		_ = logf.Close()
	}()
	printStatuses(logf, statuses, res)
	out := io.MultiWriter(c.out, logf)

	stop, err := s.StartAskpass(c.terminalPassword)
	if err != nil {
		return err
	}
	defer stop()

	outcomes := s.Engine.Apply(ctx, statuses, engine.Decisions{Running: processRunning(ctx, s.Exec)}, func(ev engine.Event) {
		switch ev.Kind {
		case engine.Started:
			fmt.Fprintf(out, "\n==> %s\n", ev.Component)
		case engine.Line:
			fmt.Fprintf(out, "    %s\n", ev.Text)
		case engine.Failed:
			fmt.Fprintf(out, "    FAILED: %v\n", ev.Err)
		case engine.Skipped:
			fmt.Fprintf(out, "\n--  %s skipped: %v\n", ev.Component, ev.Err)
		}
	})
	if err := s.Save(sel); err != nil {
		return err
	}
	var failed, skipped []string
	for _, o := range outcomes {
		switch o.Kind {
		case engine.Failed:
			failed = append(failed, o.Component)
		case engine.Skipped:
			skipped = append(skipped, o.Component)
		}
	}
	if len(failed)+len(skipped) > 0 {
		return fmt.Errorf("not finished: failed [%s], skipped [%s]", strings.Join(failed, ", "), strings.Join(skipped, ", "))
	}
	if switched, err := repo.UseSSHOrigin(ctx, s.Exec, s.Repo); err == nil && switched {
		fmt.Fprintln(out, "origin switched to SSH")
	}
	fmt.Fprintf(out, "\ndone: %d component(s); host file hosts/%s.yaml updated\n", len(outcomes), s.Host)
	return nil
}

func (c *cli) confirm(prompt string) bool {
	fmt.Fprint(c.out, prompt)
	line, _ := bufio.NewReader(c.in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// terminalPassword asks for the sudo password on the terminal (askpass server callback).
func (c *cli) terminalPassword(msg string) (string, bool) {
	if !c.isTTY {
		return "", false
	}
	fmt.Fprintf(c.out, "\n%s: ", msg)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(c.out)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// processRunning returns a check for a running app (layouts import guard).
func processRunning(ctx context.Context, e *sh.Exec) func(string) bool {
	return func(name string) bool {
		res, err := e.Run(ctx, sh.Cmd{Path: "pgrep", Args: []string{"-x", name}})
		return err == nil && res.Code == 0
	}
}

// runLogDir holds one log file per run (TUI and apply).
func runLogDir(home string) string { return filepath.Join(home, ".local", "state", "rigger", "logs") }

// openRunLog creates this run's log file.
func openRunLog(home string) (*os.File, error) {
	dir := runLogDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, time.Now().Format("20060102-150405")+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open run log: %w", err)
	}
	return f, nil
}
