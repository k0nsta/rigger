// Package remote sets up another host over ssh: install rigger there, clone the setup repo
// with a forwarded agent, then run the TUI on the host.
package remote

import (
	"fmt"
	"strings"

	"github.com/k0nsta/rigger/internal/repo"
	"github.com/k0nsta/rigger/internal/sh"
)

// InstallURL is the public installer.
const InstallURL = "https://github.com/k0nsta/rigger/releases/latest/download/install.sh"

// remoteBin is where rigger lives on the host; non-login ssh shells often lack ~/.local/bin on PATH.
const remoteBin = "$HOME/.local/bin/rigger"

// Options describe the target.
type Options struct {
	Host    string // ssh destination (alias, user@host)
	Origin  string // the setup repo's origin URL
	Profile string
	// Binary uploads this local rigger binary instead of running the installer (private
	// releases, air-gapped hosts).
	Binary string
	// Terminfo installs agterm's terminfo entry on the host first.
	Terminfo bool
}

// Step is one command; Interactive steps get the terminal (sudo prompts, the TUI).
type Step struct {
	Title       string
	Cmd         sh.Cmd
	Interactive bool
}

// Plan returns the steps for opts. The agent is forwarded (-A) only for the clone.
func Plan(opts Options) ([]Step, error) {
	if opts.Host == "" {
		return nil, fmt.Errorf("remote: no host")
	}
	origin, err := repo.ParseRemote(opts.Origin)
	if err != nil {
		return nil, fmt.Errorf("remote: setup repo origin: %w", err)
	}
	if origin.Host == "local" {
		return nil, fmt.Errorf("remote: setup repo origin %q is a local path; push it to a git host first", opts.Origin)
	}
	cloneURL := origin.SSH()
	profile := opts.Profile
	if profile == "" {
		profile = "server"
	}

	var steps []Step
	if opts.Terminfo {
		steps = append(steps, Step{Title: "terminfo", Cmd: sh.Cmd{Path: "agtermctl", Args: []string{"terminfo", "install", opts.Host}}})
	}
	prereq := "set -e; need=''; command -v git >/dev/null || need=\"$need git\"; command -v curl >/dev/null || need=\"$need curl\"; " +
		"if [ -n \"$need\" ]; then echo \"installing$need\"; sudo apt-get update -q && sudo apt-get install -y -q $need; fi"
	steps = append(steps, Step{Title: "git and curl", Cmd: ssh(opts.Host, true, false, prereq), Interactive: true})

	if opts.Binary != "" {
		steps = append(steps,
			Step{Title: "upload rigger", Cmd: sh.Cmd{Path: "scp", Args: []string{"-q", opts.Binary, opts.Host + ":.rigger.upload"}}},
			Step{Title: "install rigger", Cmd: ssh(opts.Host, false, false, "mkdir -p \"$HOME/.local/bin\" && install -m 0755 \"$HOME/.rigger.upload\" "+remoteBin+" && rm -f \"$HOME/.rigger.upload\"")},
		)
	} else {
		steps = append(steps, Step{Title: "install rigger", Cmd: ssh(opts.Host, false, false,
			"[ -x "+remoteBin+" ] || curl -fsSL "+InstallURL+" | sh")})
	}

	steps = append(steps,
		Step{Title: "clone setup repo", Cmd: ssh(opts.Host, false, true,
			"GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=accept-new' "+remoteBin+" init "+shellQuote(cloneURL)+" --no-run")},
		Step{Title: "installer", Cmd: ssh(opts.Host, true, false, remoteBin+" --profile "+shellQuote(profile)), Interactive: true},
	)
	return steps, nil
}

func ssh(host string, tty, agent bool, script string) sh.Cmd {
	var args []string
	if tty {
		args = append(args, "-t")
	}
	if agent {
		args = append(args, "-A")
	}
	args = append(args, host, script)
	return sh.Cmd{Path: "ssh", Args: args}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
