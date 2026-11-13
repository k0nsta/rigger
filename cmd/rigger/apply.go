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

	"github.com/k0nsta/rigger/internal/action"
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
	in := bufio.NewReader(c.in)

	s, sel, err := f.open(c.home)
	if err != nil {
		return err
	}
	c.noteElsewhere(s)
	res, statuses, err := s.Plan(ctx, sel)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s · %s · profile %s\n\n", s.Host, s.Platform, orNone(sel.Profile))
	c.warnHead(ctx, s)
	printStatuses(c.out, statuses, res)

	if missing := engine.MissingSecrets(statuses, s.Secrets); len(missing) > 0 {
		fmt.Fprint(c.out, missingSecrets(s.Home, missing, "components needing them are skipped"))
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
	if !yes && !c.confirm(in, fmt.Sprintf("\napply %d component(s)? [y/N] ", pending)) {
		return errors.New("cancelled")
	}
	logf, err := openRunLog(s.Home)
	if err != nil {
		return err
	}
	defer func() {
		fmt.Fprintf(c.out, "log: %s\n", sh.Tilde(s.Home, logf.Name()))
		_ = logf.Close()
	}()
	printStatuses(logf, statuses, res)
	out := io.MultiWriter(c.out, logf)

	stop, err := s.StartAskpass(c.terminalPassword)
	if err != nil {
		return err
	}
	defer stop()

	d := engine.Decisions{Running: processRunning(ctx, s.Exec)}
	if s.HostFile != nil {
		linkDecisions := c.decideReplacedLinks(in, statuses, yes, s.Home)
		d.Adopt, d.Leave = linkDecisions.Adopt, linkDecisions.Leave
	}
	printKeptSeeds(c.out, statuses)
	outcomes := s.Engine.Apply(ctx, statuses, d, func(ev engine.Event) {
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

func (c *cli) confirm(in *bufio.Reader, prompt string) bool {
	return c.confirmDefault(in, prompt, false)
}

// confirmDefault asks a yes/no question; enter takes def, closed input says no.
func (c *cli) confirmDefault(in *bufio.Reader, prompt string, def bool) bool {
	fmt.Fprint(c.out, prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(c.out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
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

// printKeptSeeds explains why an apply left a both-changed seed untouched.
func printKeptSeeds(out io.Writer, statuses []engine.ComponentStatus) {
	for _, cs := range statuses {
		for _, a := range cs.Actions {
			if a.Kind == engine.KindSeed && a.Status == action.BothChanged {
				fmt.Fprintf(out, "\n%s: both the template and live file changed; kept the live file.\n", a.Target)
			}
		}
	}
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
