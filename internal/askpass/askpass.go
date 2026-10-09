// Package askpass routes sudo password prompts of child processes to rigger.
//
// Children run without a controlling terminal, so sudo cannot prompt on the TTY the TUI owns.
// They get SUDO_ASKPASS pointing at a helper script that runs `rigger askpass`, which asks this
// server over a unix socket. The server asks the user once (TUI overlay or terminal prompt),
// checks the password with `sudo -S -v`, keeps it in memory for the rest of the run and answers
// later requests without asking again. Nothing is written to disk.
package askpass

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvSocket is the variable the helper reads to find the server.
const EnvSocket = "RIGGER_ASKPASS_SOCK"

// Ask gets a password from the user; message explains why (and says when a retry follows a
// wrong password). ok=false means the user cancelled.
type Ask func(message string) (password string, ok bool)

// Validate reports whether password is the sudo password.
type Validate func(password string) bool

// Server answers askpass helpers.
type Server struct {
	dir      string
	ln       net.Listener
	ask      Ask
	validate Validate

	mu     sync.Mutex
	cached string
	wg     sync.WaitGroup
}

// Start listens in a new private temp dir and writes the helper and a sudo wrapper there.
// rigger is the path of the rigger binary the helper runs.
func Start(rigger string, ask Ask, validate Validate) (*Server, error) {
	dir, err := os.MkdirTemp("", "rigger-askpass-")
	if err != nil {
		return nil, fmt.Errorf("askpass dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("askpass dir: %w", err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "sock"))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("askpass socket: %w", err)
	}
	s := &Server{dir: dir, ln: ln, ask: ask, validate: validate}

	// the socket is baked in: brew runs its children with a filtered env that drops it
	sock := filepath.Join(dir, "sock")
	helper := "#!/bin/sh\n" + EnvSocket + "=" + shellQuote(sock) + " exec " + shellQuote(rigger) + " askpass \"$@\"\n"
	// manifest cmds call plain `sudo`; route it through the helper too
	wrapper := "#!/bin/sh\nexec /usr/bin/sudo -A \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "askpass"), []byte(helper), 0o700); err != nil {
		s.Close()
		return nil, fmt.Errorf("askpass helper: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		s.Close()
		return nil, fmt.Errorf("askpass bin: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "sudo"), []byte(wrapper), 0o700); err != nil {
		s.Close()
		return nil, fmt.Errorf("sudo wrapper: %w", err)
	}

	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Env is what children need: SUDO_ASKPASS, the socket, and the sudo wrapper first on PATH.
func (s *Server) Env(path string) []string {
	return []string{
		"SUDO_ASKPASS=" + filepath.Join(s.dir, "askpass"),
		EnvSocket + "=" + filepath.Join(s.dir, "sock"),
		"PATH=" + filepath.Join(s.dir, "bin") + ":" + path,
	}
}

// Close stops serving, forgets the password and removes the temp dir.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
	s.mu.Lock()
	s.cached = ""
	s.mu.Unlock()
	_ = os.RemoveAll(s.dir)
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.handle(conn)
	}
}

// handle serves one helper; requests are handled one at a time so the user is asked once.
func (s *Server) handle(conn net.Conn) {
	defer conn.Close() //nolint:errcheck // best effort
	prompt, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && prompt == "" {
		return
	}
	if pw, ok := s.password(strings.TrimSpace(prompt)); ok {
		_, _ = fmt.Fprintf(conn, "OK %s\n", pw)
		return
	}
	_, _ = fmt.Fprint(conn, "CANCEL\n")
}

func (s *Server) password(prompt string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != "" {
		return s.cached, true
	}
	msg := "sudo password (asked once for this run)"
	if prompt != "" && !strings.EqualFold(strings.TrimSuffix(prompt, ":"), "password") {
		msg += " — " + prompt
	}
	for range 3 {
		pw, ok := s.ask(msg)
		if !ok {
			return "", false
		}
		if s.validate(pw) {
			s.cached = pw
			return pw, true
		}
		msg = "wrong password, try again"
	}
	return "", false
}

// Request is the helper side: ask the server at socket for a password.
func Request(socket, prompt string) (string, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return "", fmt.Errorf("askpass: %w", err)
	}
	defer conn.Close() //nolint:errcheck // best effort
	if _, err := fmt.Fprintf(conn, "%s\n", strings.ReplaceAll(prompt, "\n", " ")); err != nil {
		return "", fmt.Errorf("askpass: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("askpass: %w", err)
	}
	pw, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), "OK ")
	if !ok {
		return "", errors.New("askpass: cancelled")
	}
	return pw, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
