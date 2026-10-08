package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/k0nsta/rigger/internal/sh"
)

// Remote is a parsed git URL.
type Remote struct {
	Host  string // github.com
	Path  string // owner/repo (no .git)
	HTTPS bool
}

var (
	scpURL = regexp.MustCompile(`^(?:[\w.-]+@)?([\w.-]+):([^/][^:]*)$`)
	netURL = regexp.MustCompile(`^(https?|ssh|git)://(?:[^@/]+@)?([^/:]+)(?::\d+)?/(.+)$`)
)

// ParseRemote understands scp-style (git@host:owner/repo), https://, ssh:// and file:// URLs
// and absolute local paths (host "local").
func ParseRemote(url string) (Remote, error) {
	url = strings.TrimSpace(url)
	if local, ok := strings.CutPrefix(url, "file://"); ok || filepath.IsAbs(url) {
		if !ok {
			local = url
		}
		return Remote{Host: "local", Path: strings.TrimSuffix(filepath.Clean(local), ".git")}, nil
	}
	if m := netURL.FindStringSubmatch(url); m != nil {
		return Remote{Host: m[2], Path: trimGit(m[3]), HTTPS: strings.HasPrefix(m[1], "http")}, nil
	}
	if m := scpURL.FindStringSubmatch(url); m != nil {
		return Remote{Host: m[1], Path: trimGit(m[2])}, nil
	}
	return Remote{}, fmt.Errorf("not a git URL: %q", url)
}

func trimGit(p string) string { return strings.TrimSuffix(strings.Trim(p, "/"), ".git") }

// Name is the repo name (last path element).
func (r Remote) Name() string { return filepath.Base(r.Path) }

// SSH is the scp-style SSH URL for the same repo.
func (r Remote) SSH() string { return fmt.Sprintf("git@%s:%s.git", r.Host, r.Path) }

// Same reports whether two URLs point at the same repo, regardless of protocol.
func Same(a, b string) bool {
	ra, errA := ParseRemote(a)
	rb, errB := ParseRemote(b)
	return errA == nil && errB == nil && strings.EqualFold(ra.Host, rb.Host) && ra.Path == rb.Path
}

// DefaultDir is ~/<host>/<repo-name>, e.g. ~/github.com/machine-setup.
func DefaultDir(home, url string) (string, error) {
	r, err := ParseRemote(url)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, r.Host, r.Name()), nil
}

// TokenPrompt asks the user for an access token when HTTPS needs one.
type TokenPrompt func(host string) (string, error)

// Clone clones url into dir, or pulls when dir already holds the same repo. HTTPS auth
// failures ask prompt for a token once; it reaches git through a one-shot GIT_ASKPASS helper
// via the environment and is never written to disk or into the remote URL.
func Clone(ctx context.Context, r Runner, url, dir string, prompt TokenPrompt, onLine func(string)) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		res, err := r.Run(ctx, sh.Cmd{Path: "git", Args: []string{"-C", dir, "remote", "get-url", "origin"}})
		if err != nil {
			return err
		}
		origin := strings.TrimSpace(res.Output)
		if res.Code != 0 || !Same(origin, url) {
			return fmt.Errorf("%s already holds another repo (origin %q)", dir, origin)
		}
		return run(ctx, r, sh.Cmd{Path: "git", Args: []string{"-C", dir, "pull", "--ff-only"}, OnLine: onLine}, "git pull")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s exists and is not empty", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dir), err)
	}

	clone := sh.Cmd{Path: "git", Args: []string{"clone", url, dir}, OnLine: onLine}
	res, err := r.Run(ctx, clone)
	if err != nil {
		return err
	}
	if res.Code == 0 {
		return nil
	}
	remote, perr := ParseRemote(url)
	if perr != nil || !remote.HTTPS || !authFailure(res.Output) || prompt == nil {
		return fmt.Errorf("git clone failed: %s", lastLine(res.Output))
	}

	token, err := prompt(remote.Host)
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("git clone needs a token for " + remote.Host)
	}
	helper, cleanup, err := askpassHelper()
	if err != nil {
		return err
	}
	defer cleanup()
	_ = os.RemoveAll(dir) // a failed clone may leave an empty dir
	clone.Env = []string{"GIT_ASKPASS=" + helper, "RIGGER_GIT_TOKEN=" + token}
	return run(ctx, r, clone, "git clone")
}

func run(ctx context.Context, r Runner, c sh.Cmd, what string) error {
	res, err := r.Run(ctx, c)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("%s failed: %s", what, lastLine(res.Output))
	}
	return nil
}

func authFailure(out string) bool {
	out = strings.ToLower(out)
	for _, s := range []string{"authentication failed", "could not read username", "terminal prompts disabled", "repository not found", "403"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

func lastLine(out string) string {
	lines := sh.Lines(out)
	if len(lines) == 0 {
		return "no output"
	}
	return lines[len(lines)-1]
}

// askpassHelper writes a script that answers git's username/password prompts from the
// environment; the token itself never touches the file.
func askpassHelper() (string, func(), error) {
	dir, err := os.MkdirTemp("", "rigger-askpass-")
	if err != nil {
		return "", nil, fmt.Errorf("askpass dir: %w", err)
	}
	path := filepath.Join(dir, "askpass.sh")
	script := "#!/bin/sh\ncase \"$1\" in\n  Username*) echo x-access-token ;;\n  *) printf '%s\\n' \"$RIGGER_GIT_TOKEN\" ;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("askpass helper: %w", err)
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}

// UseSSHOrigin switches an HTTPS origin to SSH when SSH auth to the host works.
func UseSSHOrigin(ctx context.Context, r Runner, dir string) (bool, error) {
	res, err := r.Run(ctx, sh.Cmd{Path: "git", Args: []string{"-C", dir, "remote", "get-url", "origin"}})
	if err != nil || res.Code != 0 {
		return false, err
	}
	remote, err := ParseRemote(strings.TrimSpace(res.Output))
	if err != nil || !remote.HTTPS {
		return false, nil //nolint:nilerr // not a URL we can switch
	}
	probe, err := r.Run(ctx, sh.Cmd{Path: "ssh", Args: []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=8", "-T", "git@" + remote.Host}})
	if err != nil {
		return false, err
	}
	// GitHub/GitLab exit 1 on -T but greet the authenticated user
	out := strings.ToLower(probe.Output)
	authenticated := probe.Code == 0 || strings.Contains(out, "successfully authenticated") || strings.Contains(out, "welcome to gitlab")
	if !authenticated {
		return false, nil
	}
	if err := run(ctx, r, sh.Cmd{Path: "git", Args: []string{"-C", dir, "remote", "set-url", "origin", remote.SSH()}}, "git remote set-url"); err != nil {
		return false, err
	}
	return true, nil
}
