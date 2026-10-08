// Package app wires a setup repo, local state, secrets and the engine into one session that
// the CLI and the TUI share.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/askpass"
	"github.com/k0nsta/rigger/internal/engine"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/repo"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/state"
)

// Options select the repo and host.
type Options struct {
	Repo string // --repo
	Host string // --host
	Home string // defaults to $HOME
	Cwd  string // defaults to the working dir
	// ExtraEnv is appended to every child's environment (last value wins; tests put fakes on PATH).
	ExtraEnv []string
}

// Session is everything one rigger run works with.
type Session struct {
	Home      string
	Repo      string
	Manifest  *manifest.Manifest
	Local     *state.Local
	LocalPath string
	Secrets   *action.Secrets
	Host      string
	// HostIsNew is true when no host name was remembered yet (the TUI asks for one).
	HostIsNew bool
	HostFile  *state.HostFile
	Platform  string
	Exec      *sh.Exec
	Engine    *engine.Engine
}

// Open finds the repo, loads the manifest and state, and builds the engine.
func Open(opts Options) (*Session, error) {
	home := opts.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("home dir: %w", err)
		}
		home = h
	}
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	localPath := state.LocalPath(home)
	local, err := state.LoadLocal(localPath)
	if err != nil {
		return nil, err
	}
	repoDir, err := repo.Discover(opts.Repo, os.Getenv("RIGGER_REPO"), cwd, local.Repo)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(repoDir)
	if err != nil {
		return nil, err
	}

	s := &Session{Home: home, Repo: repoDir, Manifest: m, Local: local, LocalPath: localPath, Platform: runtime.GOOS}
	s.Host, s.HostIsNew = pickHost(opts.Host, local.Host)
	if s.HostFile, err = state.LoadHost(state.HostPath(repoDir, s.Host)); err != nil {
		return nil, err
	}

	s.Exec = sh.NewExec(home, append([]string{"RIGGER_REPO=" + repoDir}, opts.ExtraEnv...)...)
	s.Secrets, err = action.LoadSecrets(filepath.Join(state.ConfigDir(home), "secrets.env"), s.opRead)
	if err != nil {
		return nil, err
	}
	s.Engine = &engine.Engine{
		Manifest: m,
		Env: &action.Env{
			Home:       home,
			Repo:       repoDir,
			BackupRoot: filepath.Join(home, ".local", "state", "rigger", "backups"),
		},
		Runner:   s.Exec,
		Secrets:  s.Secrets,
		Local:    local,
		Platform: s.Platform,
		Host:     s.Host,
		AppPaths: action.DefaultAppPaths(home),
	}
	return s, nil
}

func pickHost(flag, remembered string) (string, bool) {
	switch {
	case flag != "":
		return sanitizeHost(flag), remembered == ""
	case remembered != "":
		return remembered, false
	}
	h, _ := os.Hostname()
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	if h == "" {
		h = "machine"
	}
	return sanitizeHost(h), true
}

// sanitizeHost makes a host name safe as a file name.
func sanitizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// SetHost changes the host name (first run), reloads its host file and returns the stored name.
func (s *Session) SetHost(name string) (string, error) {
	name = sanitizeHost(name)
	if name == "" {
		return "", errors.New("host name is empty")
	}
	hf, err := state.LoadHost(state.HostPath(s.Repo, name))
	if err != nil {
		return "", err
	}
	s.Host, s.HostFile, s.Engine.Host = name, hf, name
	return name, nil
}

func (s *Session) opRead(ctx context.Context, ref string) (string, error) {
	res, err := s.Exec.Run(ctx, sh.Cmd{Path: "op", Args: []string{"read", ref}})
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", fmt.Errorf("op read failed: %s", strings.TrimSpace(res.Output))
	}
	return res.Output, nil
}

// Selection is what to run: picked components and unpicked packages.
type Selection struct {
	Profile string
	Picked  []string
	Exclude map[string]bool
}

// ErrNoProfile means neither a profile, --only, nor a host file says what to install.
var ErrNoProfile = errors.New("no selection for this host yet: pass --profile")

// Select resolves the selection: --only, else --profile, else the host file, else the only profile.
// A host file is applied on top of its profile, so components later added to the profile appear.
func (s *Session) Select(profile string, only []string) (Selection, error) {
	sel := Selection{Exclude: map[string]bool{}}
	hf := s.HostFile
	switch {
	case len(only) > 0:
		sel.Picked = only
		if hf != nil {
			sel.Profile = hf.Profile
			sel.Exclude = s.hostExcludes(hf.Profile, hf)
		}
		return sel, nil
	case profile == "" && hf != nil:
		picked, err := s.hostPicked(hf)
		if err != nil {
			return sel, err
		}
		sel.Profile, sel.Picked = hf.Profile, picked
		sel.Exclude = s.hostExcludes(hf.Profile, hf)
		return sel, nil
	case profile == "" && len(s.Manifest.Profiles) == 1:
		for name := range s.Manifest.Profiles {
			profile = name
		}
	case profile == "":
		return sel, ErrNoProfile
	}
	ids, err := s.Manifest.ExpandProfile(profile)
	if err != nil {
		return sel, err
	}
	sel.Profile, sel.Picked = profile, ids
	if hf != nil && hf.Profile == profile {
		sel.Exclude = s.hostExcludes(profile, hf)
	} else {
		addAll(sel.Exclude, s.Manifest.ProfileExcludePackages(profile))
	}
	return sel, nil
}

// hostPicked is the profile's components plus Add minus Remove (or the legacy full list).
func (s *Session) hostPicked(hf *state.HostFile) ([]string, error) {
	if len(hf.Components) > 0 {
		return s.known(hf.Components), nil
	}
	var base []string
	if hf.Profile != "" {
		ids, err := s.Manifest.ExpandProfile(hf.Profile)
		if err != nil {
			return nil, fmt.Errorf("hosts/%s.yaml: %w", s.Host, err)
		}
		base = ids
	}
	removed := map[string]bool{}
	for _, id := range hf.Remove {
		removed[id] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range append(base, hf.Add...) {
		if !removed[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return s.known(out), nil
}

// known drops ids the manifest no longer has (a component was renamed or removed).
func (s *Session) known(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if s.Manifest.Component(id) != nil {
			out = append(out, id)
		}
	}
	return out
}

// hostExcludes is the profile's default package excludes plus the host's, minus its includes.
func (s *Session) hostExcludes(profile string, hf *state.HostFile) map[string]bool {
	ex := map[string]bool{}
	if profile != "" {
		addAll(ex, s.Manifest.ProfileExcludePackages(profile))
	}
	addAll(ex, hf.ExcludePackages)
	for _, p := range hf.IncludePackages {
		delete(ex, p)
	}
	return ex
}

// Profiles lists profile names sorted.
func (s *Session) Profiles() []string {
	names := make([]string, 0, len(s.Manifest.Profiles))
	for n := range s.Manifest.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Plan resolves and probes a selection.
func (s *Session) Plan(ctx context.Context, sel Selection) (manifest.Resolution, []engine.ComponentStatus, error) {
	res, err := s.Manifest.Resolve(s.Platform, sel.Picked)
	if err != nil {
		return res, nil, err
	}
	statuses, err := s.Engine.Probe(ctx, res, sel.Exclude)
	return res, statuses, err
}

// Save remembers the repo, host and seed hashes locally and writes the host file as the
// difference between the selection and its profile.
func (s *Session) Save(sel Selection) error {
	s.Local.Repo, s.Local.Host = s.Repo, s.Host
	if err := s.Local.Save(s.LocalPath); err != nil {
		return err
	}
	hf, err := s.hostDelta(sel)
	if err != nil {
		return err
	}
	if s.HostFile != nil {
		hf.IgnorePackages = s.HostFile.IgnorePackages
	}
	if err := hf.Save(state.HostPath(s.Repo, s.Host)); err != nil {
		return err
	}
	s.HostFile = hf
	return nil
}

func (s *Session) hostDelta(sel Selection) (*state.HostFile, error) {
	hf := &state.HostFile{Profile: sel.Profile}
	var base []string
	var baseExclude []string
	if sel.Profile != "" {
		ids, err := s.Manifest.ExpandProfile(sel.Profile)
		if err != nil {
			return nil, err
		}
		base, baseExclude = ids, s.Manifest.ProfileExcludePackages(sel.Profile)
	}
	inBase := setOf(base)
	picked := setOf(sel.Picked)
	for _, id := range sel.Picked {
		if !inBase[id] {
			hf.Add = append(hf.Add, id)
		}
	}
	for _, id := range base {
		if !picked[id] {
			hf.Remove = append(hf.Remove, id)
		}
	}
	defaults := setOf(baseExclude)
	for pkg, on := range sel.Exclude {
		if on && !defaults[pkg] {
			hf.ExcludePackages = append(hf.ExcludePackages, pkg)
		}
	}
	for _, pkg := range baseExclude {
		if !sel.Exclude[pkg] {
			hf.IncludePackages = append(hf.IncludePackages, pkg)
		}
	}
	sort.Strings(hf.ExcludePackages)
	sort.Strings(hf.IncludePackages)
	return hf, nil
}

func setOf(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

func addAll(set map[string]bool, items []string) {
	for _, i := range items {
		set[i] = true
	}
}

// Apply runs the engine.
func (s *Session) Apply(ctx context.Context, statuses []engine.ComponentStatus, d engine.Decisions, emit func(engine.Event)) []engine.Outcome {
	return s.Engine.Apply(ctx, statuses, d, emit)
}

// MissingSecrets lists secrets the statuses need that are not set.
func (s *Session) MissingSecrets(statuses []engine.ComponentStatus) []string {
	return engine.MissingSecrets(statuses, s.Secrets)
}

// SetSecret stores a secret in the local secrets file.
func (s *Session) SetSecret(key, value string) error { return s.Secrets.Set(key, value) }

// ManualItem is a file the user moves by hand, with whether it exists here.
type ManualItem struct {
	Path, Note string
	Exists     bool
}

// Manual checks the manifest's manual list on this machine.
func (s *Session) Manual() []ManualItem {
	out := make([]ManualItem, 0, len(s.Manifest.Manual))
	for _, m := range s.Manifest.Manual {
		_, err := os.Stat(manifest.ExpandHome(m.Path, s.Home))
		out = append(out, ManualItem{Path: m.Path, Note: m.Note, Exists: err == nil})
	}
	return out
}

// PlatformComponents returns the components that run here, in manifest order.
func (s *Session) PlatformComponents() []*manifest.Component {
	var out []*manifest.Component
	for _, id := range s.Manifest.PlatformComponents(s.Platform) {
		out = append(out, s.Manifest.Component(id))
	}
	return out
}

// Adopt takes a live file back into the repo: a seed's template (secrets become ${VAR} again)
// or a link target whose real file drifted from the repo (copied in, then linked). It returns
// a one-line description of what changed.
func (s *Session) Adopt(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(manifest.ExpandHome(path, s.Home))
	if err != nil {
		return "", err
	}
	env := s.Engine.Env
	for _, c := range s.Manifest.Components {
		for _, sd := range c.Seeds {
			seed := action.NewSeed(env, sd)
			if seed.Dst != abs {
				continue
			}
			hash, unmatched, err := seed.Adopt(ctx, s.Secrets)
			if err != nil {
				return "", err
			}
			s.Local.Seeds[seed.Dst] = hash
			if err := s.Local.Save(s.LocalPath); err != nil {
				return "", err
			}
			msg := fmt.Sprintf("%s: template %s updated from %s", c.ID, sd.Src, sd.Dst)
			if len(unmatched) > 0 {
				msg += fmt.Sprintf(" (warning: %s not found in the file, its placeholder is gone)", strings.Join(unmatched, ", "))
			}
			return msg, nil
		}
		for _, l := range c.Links {
			link := action.NewLink(env, l)
			if link.Dst != abs {
				continue
			}
			p, err := link.Probe()
			if err != nil {
				return "", err
			}
			switch p.Status {
			case action.OK:
				return fmt.Sprintf("%s: %s is already linked; edits are tracked in %s", c.ID, l.Dst, l.Src), nil
			case action.Differs:
				if err := link.Apply(env, action.AdoptLive); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s: %s copied into %s and linked", c.ID, l.Dst, l.Src), nil
			default:
				return "", fmt.Errorf("%s: %s is %s (%s); only a real file can be adopted", c.ID, l.Dst, p.Status, p.Detail)
			}
		}
	}
	return "", fmt.Errorf("%s is not a seed or link of any component: add it to %s first", path, manifest.FileName)
}

// StartAskpass routes sudo prompts of children to ask (see internal/askpass). It is a no-op
// as root or without /usr/bin/sudo. The returned stop forgets the password.
func (s *Session) StartAskpass(ask askpass.Ask) (stop func(), err error) {
	if os.Geteuid() == 0 {
		return func() {}, nil
	}
	if _, err := os.Stat("/usr/bin/sudo"); err != nil {
		return func() {}, nil //nolint:nilerr // no sudo on this machine
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("askpass: %w", err)
	}
	srv, err := askpass.Start(exe, ask, askpass.SudoValidate)
	if err != nil {
		return nil, err
	}
	s.Exec.AddEnv(srv.Env(s.Exec.Path())...)
	return srv.Close, nil
}
