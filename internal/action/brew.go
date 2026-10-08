package action

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/k0nsta/rigger/internal/sh"
)

// Brewfile entry kinds rigger understands; other lines are left to brew bundle.
const (
	KindTap  = "tap"
	KindBrew = "brew"
	KindCask = "cask"
	KindMas  = "mas"
)

// BrewPackage is one Brewfile entry.
type BrewPackage struct {
	Kind string
	// Name as written in the Brewfile (e.g. "owner/tap/formula"); brew bundle skip lists match it.
	Name    string
	Comment string // the comment line right above, if any
}

// Short is the name brew list prints (last path element).
func (p BrewPackage) Short() string {
	if p.Kind == KindTap {
		return p.Name
	}
	if i := strings.LastIndex(p.Name, "/"); i >= 0 {
		return p.Name[i+1:]
	}
	return p.Name
}

var brewLine = regexp.MustCompile(`^\s*(tap|brew|cask|mas)\s+"([^"]+)"`)

// ParseBrewfile extracts tap/brew/cask/mas entries in file order.
func ParseBrewfile(data []byte) []BrewPackage {
	var out []BrewPackage
	comment := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			comment = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			continue
		}
		if m := brewLine.FindStringSubmatch(line); m != nil {
			out = append(out, BrewPackage{Kind: m[1], Name: m[2], Comment: comment})
		}
		comment = ""
	}
	return out
}

// BrewInventory is what Homebrew has installed, loaded once per run.
type BrewInventory struct {
	Formulae map[string]bool
	Casks    map[string]bool
	Taps     map[string]bool
}

// LoadBrewInventory lists installed formulae, casks and taps.
func LoadBrewInventory(ctx context.Context, r Runner) (*BrewInventory, error) {
	inv := &BrewInventory{}
	for _, q := range []struct {
		args []string
		dst  *map[string]bool
	}{
		{[]string{"list", "--formula", "-1"}, &inv.Formulae},
		{[]string{"list", "--cask", "-1"}, &inv.Casks},
		{[]string{"tap"}, &inv.Taps},
	} {
		res, err := r.Run(ctx, sh.Cmd{Path: "brew", Args: q.args})
		if err != nil {
			return nil, err
		}
		if res.Code != 0 {
			return nil, fmt.Errorf("brew %s failed: %s", strings.Join(q.args, " "), strings.TrimSpace(res.Output))
		}
		set := map[string]bool{}
		for _, l := range sh.Lines(res.Output) {
			set[l] = true
		}
		*q.dst = set
	}
	return inv, nil
}

// Brew is a Brewfile component.
type Brew struct {
	File     string // absolute
	Packages []BrewPackage
}

// NewBrew reads and parses a Brewfile from the repo.
func NewBrew(env *Env, brewfile string) (Brew, error) {
	path := env.RepoPath(brewfile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Brew{}, fmt.Errorf("read %s: %w", brewfile, err)
	}
	return Brew{File: path, Packages: ParseBrewfile(data)}, nil
}

// BrewProbe is the probed state of a Brewfile.
type BrewProbe struct {
	Status    Status
	Detail    string
	Missing   []BrewPackage
	Present   []BrewPackage // casks whose app/font is on disk but not brew-managed
	Excluded  []BrewPackage
	Installed []BrewPackage
}

// AppPaths are the places a cask's artifacts may already exist when installed by hand.
type AppPaths struct {
	Apps  []string // e.g. /Applications, ~/Applications
	Fonts []string // e.g. ~/Library/Fonts, /Library/Fonts
}

// DefaultAppPaths for macOS.
func DefaultAppPaths(home string) AppPaths {
	return AppPaths{
		Apps:  []string{"/Applications", filepath.Join(home, "Applications")},
		Fonts: []string{filepath.Join(home, "Library", "Fonts"), "/Library/Fonts"},
	}
}

// Probe compares the Brewfile with the inventory; casks not managed by brew but present on disk
// count as present. exclude holds Brewfile names the user unpicked.
func (b Brew) Probe(ctx context.Context, r Runner, inv *BrewInventory, exclude map[string]bool, paths AppPaths) (BrewProbe, error) {
	var p BrewProbe
	var unmanagedCasks []BrewPackage
	for _, pkg := range b.Packages {
		if exclude[pkg.Name] {
			p.Excluded = append(p.Excluded, pkg)
			continue
		}
		var installed bool
		switch pkg.Kind {
		case KindTap:
			installed = inv.Taps[pkg.Name]
		case KindBrew:
			installed = inv.Formulae[pkg.Short()]
		case KindCask:
			installed = inv.Casks[pkg.Short()]
			if !installed {
				unmanagedCasks = append(unmanagedCasks, pkg)
				continue
			}
		case KindMas:
			installed = true // App Store entries are left to brew bundle
		}
		if installed {
			p.Installed = append(p.Installed, pkg)
		} else {
			p.Missing = append(p.Missing, pkg)
		}
	}

	if len(unmanagedCasks) > 0 {
		onDisk, err := casksOnDisk(ctx, r, unmanagedCasks, paths)
		if err != nil {
			return BrewProbe{}, err
		}
		for _, pkg := range unmanagedCasks {
			if onDisk[pkg.Name] {
				p.Present = append(p.Present, pkg)
			} else {
				p.Missing = append(p.Missing, pkg)
			}
		}
	}

	if len(p.Missing) == 0 {
		p.Status = OK
	} else {
		p.Status = Todo
		p.Detail = fmt.Sprintf("%d to install", len(p.Missing))
	}
	return p, nil
}

// casksOnDisk asks brew for the casks' artifacts in one call and checks whether their app or
// font files already exist.
func casksOnDisk(ctx context.Context, r Runner, casks []BrewPackage, paths AppPaths) (map[string]bool, error) {
	args := []string{"info", "--cask", "--json=v2"}
	for _, c := range casks {
		args = append(args, c.Name)
	}
	res, err := r.Run(ctx, sh.Cmd{Path: "brew", Args: args})
	if err != nil {
		return nil, err
	}
	onDisk := map[string]bool{}
	if res.Code != 0 {
		return onDisk, nil // unknown casks: treat as missing, brew bundle reports the real error
	}
	var info struct {
		Casks []struct {
			Token     string                       `json:"token"`
			FullToken string                       `json:"full_token"`
			Artifacts []map[string]json.RawMessage `json:"artifacts"`
		} `json:"casks"`
	}
	if err := json.Unmarshal([]byte(res.Output), &info); err != nil {
		return nil, fmt.Errorf("parse brew info: %w", err)
	}
	byToken := map[string]string{}
	for _, c := range casks {
		byToken[c.Short()] = c.Name
	}
	for _, c := range info.Casks {
		name, ok := byToken[c.Token]
		if !ok {
			continue
		}
		for _, art := range c.Artifacts {
			if found(art["app"], paths.Apps) || found(art["font"], paths.Fonts) {
				onDisk[name] = true
				break
			}
		}
	}
	return onDisk, nil
}

// found reports whether the first file named in an artifact list exists in one of dirs.
func found(raw json.RawMessage, dirs []string) bool {
	if raw == nil {
		return false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return false
	}
	var name string
	if err := json.Unmarshal(items[0], &name); err != nil {
		return false
	}
	// an explicit target ({"target": "Other.app"}) wins over the source name
	for _, it := range items[1:] {
		var opts struct {
			Target string `json:"target"`
		}
		if json.Unmarshal(it, &opts) == nil && opts.Target != "" {
			name = opts.Target
		}
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, filepath.Base(name))); err == nil {
			return true
		}
	}
	return false
}

// SkipEnv builds the HOMEBREW_BUNDLE_*_SKIP variables for excluded and present packages.
func (p BrewProbe) SkipEnv() []string {
	skip := map[string][]string{}
	for _, pkg := range append(append([]BrewPackage(nil), p.Excluded...), p.Present...) {
		skip[pkg.Kind] = append(skip[pkg.Kind], pkg.Name)
	}
	kinds := make([]string, 0, len(skip))
	for k := range skip {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var env []string
	for _, k := range kinds {
		env = append(env, fmt.Sprintf("HOMEBREW_BUNDLE_%s_SKIP=%s", strings.ToUpper(k), strings.Join(skip[k], " ")))
	}
	return env
}

// Apply runs brew bundle on the unmodified Brewfile, skipping excluded and present packages.
func (b Brew) Apply(ctx context.Context, r Runner, p BrewProbe, onLine func(string)) error {
	if len(p.Missing) == 0 {
		return nil
	}
	res, err := r.Run(ctx, sh.Cmd{
		Path:   "brew",
		Args:   []string{"bundle", "install", "--no-upgrade", "--file", b.File},
		Env:    p.SkipEnv(),
		OnLine: onLine,
	})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("brew bundle failed (exit %d)", res.Code)
	}
	return nil
}
