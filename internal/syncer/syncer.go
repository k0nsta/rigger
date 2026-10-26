// Package syncer moves local changes of a setup repo to its remote and pulls other machines'
// changes, without ever committing credentials or leaving conflict markers in live configs.
package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/sh"
)

// Change is one changed path in the setup repo.
type Change struct {
	Path string
	// Code is git's two-letter porcelain status, e.g. " M", "??".
	Code string
}

// Syncer runs git in the setup repo.
type Syncer struct {
	Repo   string
	Runner Runner
	// NetTimeout bounds a pull or push (default 90s): long enough to approve an SSH key in an
	// SSH agent, short enough that a dead network does not hang sync.
	NetTimeout time.Duration
}

// ErrNoAnswer means a pull or push got no answer from the remote in time.
var ErrNoAnswer = errors.New("no answer from the remote")

// netGit runs a git command that talks to the remote. ssh runs in batch mode, so a passphrase
// or host-key question fails at once instead of waiting for a terminal rigger does not have;
// a configured core.sshCommand or GIT_SSH_COMMAND is left alone.
func (s *Syncer) netGit(ctx context.Context, args ...string) (sh.Result, error) {
	timeout := s.NetTimeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := sh.Cmd{Path: "git", Args: append([]string{"-C", s.Repo}, args...)}
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		if res, err := s.git(ctx, "config", "--get", "core.sshCommand"); err == nil && strings.TrimSpace(res.Output) == "" {
			c.Env = []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=15"}
		}
	}
	res, err := s.Runner.Run(ctx, c)
	if errors.Is(err, context.DeadlineExceeded) {
		return res, fmt.Errorf("git %s: %w after %s: check the network, or approve the SSH key if your SSH agent is asking; `git -C %s %s` shows what it waits for",
			args[0], ErrNoAnswer, timeout, s.Repo, args[0])
	}
	return res, err
}

func (s *Syncer) git(ctx context.Context, args ...string) (sh.Result, error) {
	return s.Runner.Run(ctx, sh.Cmd{Path: "git", Args: append([]string{"-C", s.Repo}, args...)})
}

func (s *Syncer) mustGit(ctx context.Context, args ...string) (string, error) {
	res, err := s.git(ctx, args...)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", fmt.Errorf("git %s failed: %s", args[0], strings.TrimSpace(res.Output))
	}
	return res.Output, nil
}

// Changes lists modified, added, deleted and untracked (not ignored) paths.
func (s *Syncer) Changes(ctx context.Context) ([]Change, error) {
	out, err := s.mustGit(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var changes []Change
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		code, path := e[:2], e[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // the original path of a rename follows
		}
		changes = append(changes, Change{Path: path, Code: code})
	}
	return changes, nil
}

// credentialPaths are file names that must never be committed.
var credentialPaths = regexp.MustCompile(`(^|/)(\.credentials\.json|id_(rsa|ed25519|ecdsa)|.*\.pem|.*\.p12|.*\.key|secrets\.env|\.netrc|\.pgpass)$`)

// credentialContent are tell-tale secret patterns for the built-in gate.
var credentialContent = regexp.MustCompile(`(?m)oauth_token:\s*\S+|-----BEGIN [A-Z ]*PRIVATE KEY-----|\b(ghp|gho|ghs|github_pat)_[A-Za-z0-9_]{20,}|\bsk-[A-Za-z0-9_-]{20,}|\bxox[abp]-[A-Za-z0-9-]{10,}|\b\d{8,10}:AA[A-Za-z0-9_-]{30,}`)

// ErrLeak means the gate found a credential in the staged files.
var ErrLeak = errors.New("possible credential staged")

// Commit stages exactly files, runs the credential gate and commits. On a gate hit the files
// are unstaged again and nothing is committed.
func (s *Syncer) Commit(ctx context.Context, host string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	if _, err := s.mustGit(ctx, append([]string{"add", "--"}, files...)...); err != nil {
		return err
	}
	if err := s.gate(ctx, files); err != nil {
		_, _ = s.git(ctx, append([]string{"reset", "-q", "--"}, files...)...)
		return err
	}
	msg := fmt.Sprintf("sync(%s): %s", host, summarize(files))
	_, err := s.mustGit(ctx, "commit", "-q", "-m", msg)
	return err
}

func summarize(files []string) string {
	if len(files) <= 3 {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:2], ", "), len(files)-2)
}

// gate refuses credentials: gitleaks on the staged diff when installed, plus a built-in check.
func (s *Syncer) gate(ctx context.Context, files []string) error {
	for _, f := range files {
		if credentialPaths.MatchString(f) {
			return fmt.Errorf("%w: %s looks like a credential file", ErrLeak, f)
		}
		data, err := os.ReadFile(filepath.Join(s.Repo, f))
		if err != nil {
			continue // deleted files carry nothing new
		}
		if m := credentialContent.Find(data); m != nil {
			return fmt.Errorf("%w: %s contains %q…", ErrLeak, f, redact(m))
		}
	}
	res, err := s.Runner.Run(ctx, sh.Cmd{Script: "command -v gitleaks"})
	if err != nil || res.Code != 0 {
		return nil //nolint:nilerr // gitleaks is optional
	}
	scan, err := s.Runner.Run(ctx, sh.Cmd{Path: "gitleaks", Args: []string{"git", "--pre-commit", "--staged", "--no-banner", "--redact", s.Repo}})
	if err != nil {
		return err
	}
	if scan.Code != 0 {
		return fmt.Errorf("%w: gitleaks: %s", ErrLeak, lastLines(scan.Output, 6))
	}
	return nil
}

func redact(b []byte) string {
	s := string(b)
	if len(s) > 12 {
		return s[:8]
	}
	return s[:min(4, len(s))]
}

func lastLines(out string, n int) string {
	lines := sh.Lines(out)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// ErrConflict means pull --rebase hit a conflict and was aborted.
var ErrConflict = errors.New("pull hit a conflict")

// ConflictError is a pull --rebase that hit a conflict in Files and was aborted: local commits
// are kept, the checkout is as it was before the pull.
type ConflictError struct {
	Files []string
}

func (e *ConflictError) Error() string {
	in := ""
	if len(e.Files) > 0 {
		in = " in " + strings.Join(e.Files, ", ")
	}
	return fmt.Sprintf("pull hit a conflict%s; rigger aborted the rebase: your commits stay local, nothing else changed", in)
}

// Unwrap lets errors.Is match ErrConflict.
func (e *ConflictError) Unwrap() error { return ErrConflict }

// ErrUncommitted means tracked files have changes that were not committed, so pull --rebase
// cannot run.
var ErrUncommitted = errors.New("local changes were not committed")

// UncommittedError names the tracked files with changes left out of the sync commit.
type UncommittedError struct {
	Files []string
}

func (e *UncommittedError) Error() string {
	return fmt.Sprintf("%d local change(s) were not committed (%s): sync pulls with rebase, which needs them committed or stashed",
		len(e.Files), strings.Join(e.Files, ", "))
}

// Unwrap lets errors.Is match ErrUncommitted.
func (e *UncommittedError) Unwrap() error { return ErrUncommitted }

// Pull rebases local commits onto the remote; a conflict is aborted so live linked configs
// never see conflict markers. Changed tracked files stop it before git does: an autostash could
// leave conflict markers in a live config when it is popped.
func (s *Syncer) Pull(ctx context.Context) error {
	out, err := s.mustGit(ctx, "diff", "--name-only", "HEAD")
	if err != nil {
		return err
	}
	if files := sh.Lines(out); len(files) > 0 {
		return &UncommittedError{Files: files}
	}
	res, err := s.netGit(ctx, "pull", "--rebase", "--no-autostash")
	if err != nil {
		return err
	}
	if res.Code == 0 {
		return nil
	}
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(s.Repo, ".git", dir)); err != nil {
			continue
		}
		var files []string
		if out, err := s.mustGit(ctx, "diff", "--name-only", "--diff-filter=U"); err == nil {
			files = sh.Lines(out)
		}
		_, _ = s.git(ctx, "rebase", "--abort")
		return &ConflictError{Files: files}
	}
	return fmt.Errorf("git pull failed: %s", lastLines(res.Output, 4))
}

// Push publishes local commits.
func (s *Syncer) Push(ctx context.Context) error {
	res, err := s.netGit(ctx, "push", "-q")
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("git push failed: %s", strings.TrimSpace(res.Output))
	}
	return nil
}

// Ahead counts local commits not on the upstream (0 when there is no upstream).
func (s *Syncer) Ahead(ctx context.Context) int {
	res, err := s.git(ctx, "rev-list", "--count", "@{u}..HEAD")
	if err != nil || res.Code != 0 {
		return 0
	}
	n := 0
	_, _ = fmt.Sscanf(strings.TrimSpace(res.Output), "%d", &n)
	return n
}

// Stray is a Homebrew package installed here but listed in no Brewfile.
type Stray struct {
	Kind string // brew or cask
	Name string
	Desc string // Homebrew's one-line description, when known
}

// packageName matches a formula or cask name, optionally tap-qualified.
var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._+-]*(/[A-Za-z0-9][A-Za-z0-9@._+-]*)*$`)

// Strays lists `brew leaves` and installed casks that no Brewfile mentions and ignore does not cover.
func Strays(ctx context.Context, r Runner, brewfiles []action.Brew, ignore []string) ([]Stray, error) {
	known := map[string]bool{}
	for _, b := range brewfiles {
		for _, p := range b.Packages {
			known[p.Kind+":"+p.Short()] = true
		}
	}
	for _, i := range ignore {
		short := i[strings.LastIndex(i, "/")+1:] // a tap cask is ignored by its full token
		known["brew:"+short], known["cask:"+short] = true, true
	}
	var out []Stray
	for _, q := range []struct {
		kind string
		args []string
	}{
		{action.KindBrew, []string{"leaves", "--installed-on-request"}},
		{action.KindCask, []string{"list", "--cask", "-1"}},
	} {
		res, err := r.Run(ctx, sh.Cmd{Path: "brew", Args: q.args})
		if err != nil {
			return nil, err
		}
		if res.Code != 0 {
			return nil, fmt.Errorf("brew %s failed: %s", q.args[0], strings.TrimSpace(res.Output))
		}
		for _, name := range sh.Lines(res.Output) {
			if !packageName.MatchString(name) {
				continue // brew progress such as "==> Downloading Homebrew API data"
			}
			short := name[strings.LastIndex(name, "/")+1:]
			if !known[q.kind+":"+short] {
				out = append(out, Stray{Kind: q.kind, Name: name})
			}
		}
	}
	withTaps(ctx, r, out)
	sort.Slice(out, func(i, j int) bool { return out[i].Kind+out[i].Name < out[j].Kind+out[j].Name })
	describe(ctx, r, out)
	return out, nil
}

// withTaps gives a cask from a third-party tap its full token from the install receipt:
// `brew list --cask` prints only `goreleaser`, which no Brewfile on a fresh machine can install
// without `goreleaser/tap/`. The receipt needs no tap trust, unlike `brew info`.
func withTaps(ctx context.Context, r Runner, strays []Stray) {
	var caskroom string
	for i := range strays {
		if strays[i].Kind != action.KindCask || strings.Contains(strays[i].Name, "/") {
			continue
		}
		if caskroom == "" {
			res, err := r.Run(ctx, sh.Cmd{Path: "brew", Args: []string{"--caskroom"}})
			if err != nil || res.Code != 0 {
				return
			}
			lines := sh.Lines(res.Output)
			if len(lines) == 0 {
				return
			}
			caskroom = lines[len(lines)-1]
		}
		data, err := os.ReadFile(filepath.Join(caskroom, strays[i].Name, ".metadata", "INSTALL_RECEIPT.json"))
		if err != nil {
			continue
		}
		var receipt struct {
			Source struct {
				Tap string `json:"tap"`
			} `json:"source"`
		}
		if json.Unmarshal(data, &receipt) != nil {
			continue
		}
		if tap := receipt.Source.Tap; tap != "" && tap != "homebrew/cask" && packageName.MatchString(tap) {
			strays[i].Name = tap + "/" + strays[i].Name
		}
	}
}

// describe fills Desc from `brew info`; descriptions are a nicety, so failures leave them empty.
func describe(ctx context.Context, r Runner, strays []Stray) {
	for _, q := range []struct{ kind, flag string }{{action.KindBrew, "--formula"}, {action.KindCask, "--cask"}} {
		var names []string
		for _, st := range strays {
			if st.Kind == q.kind {
				names = append(names, st.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		res, err := r.Run(ctx, sh.Cmd{Path: "brew", Args: append([]string{"info", "--json=v2", q.flag}, names...)})
		if err != nil || res.Code != 0 {
			continue
		}
		var info struct {
			Formulae []struct {
				Name     string `json:"name"`
				FullName string `json:"full_name"`
				Desc     string `json:"desc"`
			} `json:"formulae"`
			Casks []struct {
				Token     string `json:"token"`
				FullToken string `json:"full_token"`
				Desc      string `json:"desc"`
			} `json:"casks"`
		}
		if json.Unmarshal([]byte(res.Output), &info) != nil {
			continue
		}
		descs := map[string]string{}
		for _, f := range info.Formulae {
			descs[f.Name], descs[f.FullName] = f.Desc, f.Desc
		}
		for _, c := range info.Casks {
			descs[c.Token], descs[c.FullToken] = c.Desc, c.Desc
		}
		for i := range strays {
			if strays[i].Kind == q.kind {
				strays[i].Desc = descs[strays[i].Name]
			}
		}
	}
}

// AddToBrewfile appends a package line to a Brewfile, under its description as a comment.
func AddToBrewfile(path string, st Stray) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	if st.Desc != "" {
		data = append(data, "# "+st.Desc+"\n"...)
	}
	data = append(data, fmt.Sprintf("%s %q\n", st.Kind, st.Name)...)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, info.Mode().Perm())
}
