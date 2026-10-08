// Package manifest loads a setup repo's manifest.yaml: the components a machine can pick, the
// profiles that preselect them, and the per-host and local state that remember what it picked.
package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the manifest location relative to the repo root.
const FileName = "manifest.yaml"

// Platforms a component can target; an empty list means every platform.
const (
	Darwin = "darwin"
	Linux  = "linux"
)

// SchemaVersion is the manifest format this rigger reads.
const SchemaVersion = 1

// Manifest is the parsed manifest.yaml.
type Manifest struct {
	Version int `yaml:"version"`
	// Groups orders the component list; when set, every component group must be listed.
	Groups     []string           `yaml:"groups"`
	Profiles   map[string]Profile `yaml:"profiles"`
	Components []Component        `yaml:"components"`
	Manual     []Manual           `yaml:"manual"`
	// Unmanaged lists top-level repo dirs that are not configs (docs, scripts, ...).
	Unmanaged []string `yaml:"unmanaged"`
}

// Profile preselects components for a kind of machine.
type Profile struct {
	Description string `yaml:"description"`
	// Components may contain "@other" to include another profile's components.
	Components []string `yaml:"components"`
	// Exclude drops components after "@" expansion.
	Exclude []string `yaml:"exclude"`
	// ExcludePackages are Brewfile entries unpicked by default.
	ExcludePackages []string `yaml:"exclude_packages"`
}

// Component is one selectable unit: packages, links, seeds and commands.
type Component struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title"`
	Group       string   `yaml:"group"`
	Description string   `yaml:"description"`
	Platforms   []string `yaml:"platforms"`
	Requires    []string `yaml:"requires"`
	Brewfile    string   `yaml:"brewfile"`
	Apt         []string `yaml:"apt"`
	Links       []Link   `yaml:"links"`
	Seeds       []Seed   `yaml:"seeds"`
	Layouts     *Layouts `yaml:"layouts"`
	Cmds        []Cmd    `yaml:"cmds"`
}

// Link symlinks Dst to the repo path Src.
type Link struct {
	Src string `yaml:"src"`
	Dst string `yaml:"dst"`
}

// Seed renders the repo template Src into Dst; tools that rewrite their config get seeds, not links.
type Seed struct {
	Src string `yaml:"src"`
	Dst string `yaml:"dst"`
	// Secrets are the only ${VAR}s rendered; other ${...} stay as written.
	Secrets []string `yaml:"secrets"`
}

// Layouts snapshots an app's window/workspace state per host under Src/<host>/ and restores
// it into Dst. Absolute home paths inside the files are stored as a placeholder.
type Layouts struct {
	Src string `yaml:"src"`
	Dst string `yaml:"dst"`
	// Files are globs relative to Dst, e.g. [windows.json, "windows/*.json"].
	Files []string `yaml:"files"`
	// Process, when set, is the app process name; import is refused while it runs.
	Process string `yaml:"process"`
}

// Cmd runs a shell command from the repo root unless Check (when set) already succeeds.
type Cmd struct {
	Name  string `yaml:"name"`
	Check string `yaml:"check"`
	Run   string `yaml:"run"`
	// TTY hands the terminal to the command (interactive installers).
	TTY bool `yaml:"tty"`
	// Sudo marks commands that need a primed sudo ticket.
	Sudo bool `yaml:"sudo"`
	// Secrets are exported into the command environment from the secrets file.
	Secrets []string `yaml:"secrets"`
}

// Manual is a file the user moves by hand; the finish screen shows whether it exists.
type Manual struct {
	Path string `yaml:"path"`
	Note string `yaml:"note"`
}

// Load reads and validates the manifest in repoDir.
func Load(repoDir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(repoDir, FileName))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(repoDir); err != nil {
		return nil, err
	}
	return m, nil
}

// Parse decodes manifest YAML, rejecting unknown fields.
func Parse(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// Component returns the component with id, or nil.
func (m *Manifest) Component(id string) *Component {
	for i := range m.Components {
		if m.Components[i].ID == id {
			return &m.Components[i]
		}
	}
	return nil
}

// Supports reports whether the component runs on platform.
func (c *Component) Supports(platform string) bool {
	return len(c.Platforms) == 0 || slices.Contains(c.Platforms, platform)
}

// Name is the title, falling back to the id.
func (c *Component) Name() string {
	if c.Title != "" {
		return c.Title
	}
	return c.ID
}

// ExpandProfile resolves "@other" references and Exclude into a flat, de-duplicated component list.
func (m *Manifest) ExpandProfile(name string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	if err := m.expand(name, map[string]bool{}, seen, &out); err != nil {
		return nil, err
	}
	excluded := map[string]bool{}
	for _, id := range m.Profiles[name].Exclude {
		excluded[id] = true
	}
	res := out[:0]
	for _, id := range out {
		if !excluded[id] {
			res = append(res, id)
		}
	}
	return res, nil
}

func (m *Manifest) expand(name string, visiting, seen map[string]bool, out *[]string) error {
	p, ok := m.Profiles[name]
	if !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	if visiting[name] {
		return fmt.Errorf("profile %q includes itself", name)
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, ref := range p.Components {
		if inner, ok := strings.CutPrefix(ref, "@"); ok {
			if err := m.expand(inner, visiting, seen, out); err != nil {
				return err
			}
			continue
		}
		if !seen[ref] {
			seen[ref] = true
			*out = append(*out, ref)
		}
	}
	return nil
}

// ProfileExcludePackages returns the profile's default package excludes, including "@" parents.
func (m *Manifest) ProfileExcludePackages(name string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(string, map[string]bool)
	walk = func(n string, visiting map[string]bool) {
		p, ok := m.Profiles[n]
		if !ok || visiting[n] {
			return
		}
		visiting[n] = true
		for _, ref := range p.Components {
			if inner, ok := strings.CutPrefix(ref, "@"); ok {
				walk(inner, visiting)
			}
		}
		for _, pkg := range p.ExcludePackages {
			if !seen[pkg] {
				seen[pkg] = true
				out = append(out, pkg)
			}
		}
	}
	walk(name, map[string]bool{})
	return out
}

// ExpandHome turns a leading "~" into home.
func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

// GroupOrder returns the declared groups, or the groups in order of first use.
func (m *Manifest) GroupOrder() []string {
	if len(m.Groups) > 0 {
		return m.Groups
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range m.Components {
		if !seen[c.Group] {
			seen[c.Group] = true
			out = append(out, c.Group)
		}
	}
	return out
}
