package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate checks ids, references, paths and dst nesting; repoDir is used for path checks.
func (m *Manifest) Validate(repoDir string) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if m.Version != SchemaVersion {
		add("manifest version %d is not supported (this rigger reads version %d)", m.Version, SchemaVersion)
	}

	ids := map[string]bool{}
	for _, c := range m.Components {
		switch {
		case !idPattern.MatchString(c.ID):
			add("component %q: id must match %s", c.ID, idPattern)
		case ids[c.ID]:
			add("component %q: duplicate id", c.ID)
		}
		ids[c.ID] = true
	}

	groups := map[string]bool{}
	for _, g := range m.Groups {
		groups[g] = true
	}
	for _, c := range m.Components {
		switch {
		case c.Group == "":
			add("component %q: needs a group", c.ID)
		case len(m.Groups) > 0 && !groups[c.Group]:
			add("component %q: unknown group %q", c.ID, c.Group)
		}
		for _, p := range c.Platforms {
			if p != Darwin && p != Linux {
				add("component %q: unknown platform %q", c.ID, p)
			}
		}
		for _, r := range c.Requires {
			if !ids[r] {
				add("component %q: requires unknown component %q", c.ID, r)
			}
		}
		for _, src := range c.requiredPaths() {
			if _, err := os.Stat(filepath.Join(repoDir, src)); err != nil {
				add("component %q: missing repo path %s", c.ID, src)
			}
		}
		for _, l := range c.Links {
			if l.Src == "" || l.Dst == "" {
				add("component %q: link needs src and dst", c.ID)
			}
		}
		for _, s := range c.Seeds {
			if s.Src == "" || s.Dst == "" {
				add("component %q: seed needs src and dst", c.ID)
			}
		}
		if c.Layouts != nil && (c.Layouts.Src == "" || c.Layouts.Dst == "" || len(c.Layouts.Files) == 0) {
			add("component %q: layouts needs src, dst and files", c.ID)
		}
		for _, cmd := range c.Cmds {
			if cmd.Run == "" {
				add("component %q: cmd needs run", c.ID)
			}
		}
		if c.Brewfile == "" && len(c.Apt) == 0 && len(c.Links) == 0 && len(c.Seeds) == 0 && c.Layouts == nil && len(c.Cmds) == 0 {
			add("component %q: does nothing", c.ID)
		}
	}

	if cycle := m.findCycle(); cycle != "" {
		add("requires cycle: %s", cycle)
	} else {
		errs = append(errs, m.checkRequiredPackages(repoDir)...)
	}

	for name, p := range m.Profiles {
		for _, ref := range append(append([]string{}, p.Components...), p.Exclude...) {
			if inner, ok := strings.CutPrefix(ref, "@"); ok {
				if _, exists := m.Profiles[inner]; !exists {
					add("profile %q: unknown profile %q", name, inner)
				}
				continue
			}
			if !ids[ref] {
				add("profile %q: unknown component %q", name, ref)
			}
		}
		if _, err := m.ExpandProfile(name); err != nil {
			add("profile %q: %v", name, err)
		}
	}

	errs = append(errs, m.checkNestedDst()...)
	errs = append(errs, m.checkUnreferencedDirs(repoDir)...)
	return errors.Join(errs...)
}

// repoPaths are the repo-relative paths a component uses.
func (c *Component) repoPaths() []string {
	out := c.requiredPaths()
	if c.Layouts != nil {
		out = append(out, c.Layouts.Src)
	}
	return out
}

// requiredPaths must exist in the repo; the layouts dir is created by the first export.
func (c *Component) requiredPaths() []string {
	var out []string
	if c.Brewfile != "" {
		out = append(out, c.Brewfile)
	}
	for _, l := range c.Links {
		out = append(out, l.Src)
	}
	for _, s := range c.Seeds {
		out = append(out, s.Src)
	}
	for _, cmd := range c.Cmds {
		if script := repoScript(cmd.Run); script != "" {
			out = append(out, script)
		}
	}
	return out
}

// repoScript returns the first word of run when it names a repo-relative file (contains "/",
// not absolute, not ~, not a variable or URL).
func repoScript(run string) string {
	fields := strings.Fields(run)
	if len(fields) == 0 {
		return ""
	}
	first := fields[0]
	if !strings.Contains(first, "/") || strings.ContainsAny(first[:1], "/~$\"'(") || strings.Contains(first, "://") {
		return ""
	}
	return first
}

func (m *Manifest) findCycle() string {
	state := map[string]int{} // 0 new, 1 visiting, 2 done
	var stack []string
	var visit func(id string) string
	visit = func(id string) string {
		switch state[id] {
		case 1:
			return strings.Join(append(stack, id), " -> ")
		case 2:
			return ""
		}
		state[id] = 1
		stack = append(stack, id)
		if c := m.Component(id); c != nil {
			for _, r := range c.Requires {
				if cyc := visit(r); cyc != "" {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return ""
	}
	for _, c := range m.Components {
		if cyc := visit(c.ID); cyc != "" {
			return cyc
		}
	}
	return ""
}

// checkNestedDst rejects a link/seed/layouts dst used twice, or inside another link's dst: writing
// there would write into the linked repo directory (e.g. a rendered secret into a tracked file).
func (m *Manifest) checkNestedDst() []error {
	type dst struct{ owner, path string }
	var links, all []dst
	for _, c := range m.Components {
		for _, l := range c.Links {
			d := dst{c.ID, filepath.Clean(l.Dst)}
			links = append(links, d)
			all = append(all, d)
		}
		for _, s := range c.Seeds {
			all = append(all, dst{c.ID, filepath.Clean(s.Dst)})
		}
		if c.Layouts != nil {
			all = append(all, dst{c.ID, filepath.Clean(c.Layouts.Dst)})
		}
	}
	var errs []error
	owners := map[string]string{}
	for _, d := range all {
		if prev, ok := owners[d.path]; ok {
			errs = append(errs, fmt.Errorf("component %q: dst %s is already used by %q", d.owner, d.path, prev))
			continue
		}
		owners[d.path] = d.owner
	}
	for _, l := range links {
		for _, d := range all {
			if strings.HasPrefix(d.path, l.path+"/") {
				errs = append(errs, fmt.Errorf("component %q: dst %s is inside the link %s of %q", d.owner, d.path, l.path, l.owner))
			}
		}
	}
	return errs
}

// checkUnreferencedDirs requires every top-level repo dir to be used by a component or listed
// as unmanaged, so a new config dir cannot silently go stale.
func (m *Manifest) checkUnreferencedDirs(repoDir string) []error {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return []error{fmt.Errorf("read repo dir: %w", err)}
	}
	used := map[string]bool{"hosts": true} // rigger writes hosts/<name>.yaml itself
	for _, u := range m.Unmanaged {
		used[u] = true
	}
	for _, c := range m.Components {
		for _, p := range c.repoPaths() {
			used[strings.SplitN(filepath.ToSlash(p), "/", 2)[0]] = true
		}
	}
	var missing []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !used[e.Name()] {
			missing = append(missing, e.Name())
		}
	}
	sort.Strings(missing)
	var errs []error
	for _, d := range missing {
		errs = append(errs, fmt.Errorf("repo dir %s/ is not used by any component (add one or list it under unmanaged)", d))
	}
	return errs
}

// checkRequiredPackages makes sure every requires_packages entry is in the Brewfile of the
// component or of one it requires, so picking the component can actually install it.
func (m *Manifest) checkRequiredPackages(repoDir string) []error {
	var errs []error
	for i := range m.Components {
		c := &m.Components[i]
		if len(c.RequiresPackages) == 0 {
			continue
		}
		var text strings.Builder
		for _, id := range m.withRequires(c.ID) {
			if r := m.Component(id); r != nil && r.Brewfile != "" {
				data, err := os.ReadFile(filepath.Join(repoDir, r.Brewfile))
				if err == nil {
					text.Write(data)
					text.WriteByte('\n')
				}
			}
		}
		for _, pkg := range c.RequiresPackages {
			entry := regexp.MustCompile(`(?m)^\s*(brew|cask|mas)\s+"` + regexp.QuoteMeta(pkg) + `"`)
			if !entry.MatchString(text.String()) {
				errs = append(errs, fmt.Errorf("component %q: requires package %q, which is in no Brewfile of it or the components it requires", c.ID, pkg))
			}
		}
	}
	return errs
}

// withRequires is id plus every component it requires, transitively (no cycles).
func (m *Manifest) withRequires(id string) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
		if c := m.Component(id); c != nil {
			for _, r := range c.Requires {
				walk(r)
			}
		}
	}
	walk(id)
	return out
}
