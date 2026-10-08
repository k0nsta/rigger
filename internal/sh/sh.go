// Package sh runs external commands for rigger with a controlled environment.
package sh

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Cmd is one command. Either Script (run with `sh -c`) or Path+Args.
type Cmd struct {
	Path   string
	Args   []string
	Script string
	Dir    string
	// Env is appended to the runner's base environment.
	Env []string
	// Stdin defaults to empty input (never the terminal).
	Stdin io.Reader
	// OnLine receives combined stdout/stderr lines as they arrive.
	OnLine func(line string)
}

// String is a short human form for logs.
func (c Cmd) String() string {
	if c.Script != "" {
		return c.Script
	}
	return strings.TrimSpace(c.Path + " " + strings.Join(c.Args, " "))
}

// Result is the outcome of a command that started.
type Result struct {
	// Output is combined stdout/stderr.
	Output string
	// Code is the exit code; non-zero is not an error for Run.
	Code int
}

// Exec runs commands on this machine.
type Exec struct {
	base []string
}

// NewExec builds a runner whose children get env plus a PATH that finds tools installed
// earlier in the same run (Homebrew, ~/.local/bin, ~/go/bin), no terminal prompts, and no TTY.
func NewExec(home string, extra ...string) *Exec {
	pathDirs := []string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, "go", "bin")}
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "HOME=") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"HOME="+home,
		"PATH="+strings.Join(pathDirs, ":")+":"+os.Getenv("PATH"),
		"NONINTERACTIVE=1",
		"GIT_TERMINAL_PROMPT=0",
		"HOMEBREW_NO_ENV_HINTS=1",
	)
	env = append(env, extra...)
	return &Exec{base: env}
}

// Env returns the base environment children get.
func (e *Exec) Env() []string { return append([]string(nil), e.base...) }

// Run starts c and waits. The error is non-nil only when the command could not run or ctx
// was cancelled; a non-zero exit is reported in Result.Code.
func (e *Exec) Run(ctx context.Context, c Cmd) (Result, error) {
	env := append(e.Env(), c.Env...)
	var cmd *exec.Cmd
	if c.Script != "" {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", c.Script)
	} else {
		path, err := LookPath(c.Path, env)
		if err != nil {
			return Result{Code: 127}, fmt.Errorf("run %s: %w", c, err)
		}
		cmd = exec.CommandContext(ctx, path, c.Args...)
	}
	cmd.Dir = c.Dir
	cmd.Env = env
	cmd.Stdin = c.Stdin
	if cmd.Stdin == nil {
		cmd.Stdin = bytes.NewReader(nil)
	}

	var buf bytes.Buffer
	w := &lineWriter{buf: &buf, onLine: c.OnLine}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	w.flush()

	res := Result{Output: buf.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return res, ctx.Err()
	case errors.As(err, &exitErr):
		res.Code = exitErr.ExitCode()
	default:
		return res, fmt.Errorf("run %s: %w", c, err)
	}
	return res, nil
}

// LookPath finds name in the PATH of env (the last PATH= wins, as for the child), so tools
// installed earlier in the same run are found even though rigger's own PATH lacks them.
func LookPath(name string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	pathEnv := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			pathEnv = v
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
}

// Interactive runs c attached to the real terminal (for steps that must prompt, like sudo -v).
func (e *Exec) Interactive(c Cmd) *exec.Cmd {
	var cmd *exec.Cmd
	if c.Script != "" {
		cmd = exec.Command("/bin/sh", "-c", c.Script)
	} else {
		cmd = exec.Command(c.Path, c.Args...)
	}
	cmd.Dir = c.Dir
	cmd.Env = append(e.Env(), c.Env...)
	return cmd
}

// lineWriter collects output and forwards complete lines.
type lineWriter struct {
	mu      sync.Mutex
	buf     *bytes.Buffer
	pending []byte
	onLine  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if w.onLine == nil {
		return len(p), nil
	}
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		w.onLine(strings.TrimRight(string(w.pending[:i]), "\r"))
		w.pending = w.pending[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.onLine != nil && len(w.pending) > 0 {
		w.onLine(string(w.pending))
	}
	w.pending = nil
}

// Lines splits command output into trimmed non-empty lines.
func Lines(out string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
