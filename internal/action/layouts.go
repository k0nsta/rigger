package action

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/state"
)

// HomePlaceholder replaces the absolute home path inside exported layout files.
const HomePlaceholder = "{{RIGGER_HOME}}"

// Layouts snapshots app window/workspace files per host.
type Layouts struct {
	Src     string // repo dir holding <host>/ snapshots
	Dst     string // app state dir
	Files   []string
	Process string
}

// NewLayouts resolves a manifest layouts block against env.
func NewLayouts(env *Env, l manifest.Layouts) Layouts {
	return Layouts{Src: env.RepoPath(l.Src), Dst: env.Expand(l.Dst), Files: l.Files, Process: l.Process}
}

// LayoutsProbe is the probed state of a layouts action for one host.
type LayoutsProbe struct {
	Status Status
	Detail string
	// Hosts that have a snapshot in the repo, sorted.
	Hosts []string
}

// Probe reports whether this host has a snapshot and whether the app already has state.
func (l Layouts) Probe(host string) (LayoutsProbe, error) {
	hosts, err := l.SnapshotHosts()
	if err != nil {
		return LayoutsProbe{}, err
	}
	live, err := l.liveFiles()
	if err != nil {
		return LayoutsProbe{}, err
	}
	p := LayoutsProbe{Hosts: hosts}
	switch {
	case len(live) == 0 && len(hosts) > 0:
		p.Status, p.Detail = FreshTarget, "import from "+strings.Join(hosts, ", ")
	case len(live) > 0 && !contains(hosts, host):
		p.Status, p.Detail = NoSnapshot, "export on next sync"
	default:
		p.Status = OK
	}
	return p, nil
}

// SnapshotHosts lists hosts with a snapshot dir under Src.
func (l Layouts) SnapshotHosts() ([]string, error) {
	entries, err := os.ReadDir(l.Src)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", l.Src, err)
	}
	var hosts []string
	for _, e := range entries {
		if e.IsDir() {
			hosts = append(hosts, e.Name())
		}
	}
	sort.Strings(hosts)
	return hosts, nil
}

// Export replaces Src/<host>/ with the app's current files, home path replaced by the placeholder.
func (l Layouts) Export(home, host string) (int, error) {
	files, err := l.liveFiles()
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(l.Src, host)
	if err := os.RemoveAll(dir); err != nil {
		return 0, fmt.Errorf("clear %s: %w", dir, err)
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(l.Dst, rel))
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", rel, err)
		}
		if err := state.WriteFileAtomic(filepath.Join(dir, rel), []byte(toPlaceholder(string(data), home)), 0o644); err != nil {
			return 0, err
		}
	}
	return len(files), nil
}

// Import copies the snapshot of fromHost into Dst, backing up existing files. running reports
// whether the app process is up; import is refused while it runs because the app rewrites these files.
func (l Layouts) Import(env *Env, fromHost string, running bool) (int, error) {
	if running {
		return 0, fmt.Errorf("quit %s before importing layouts", l.Process)
	}
	dir := filepath.Join(l.Src, fromHost)
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				return relErr
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("read snapshot %s: %w", fromHost, err)
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", rel, err)
		}
		target := filepath.Join(l.Dst, rel)
		if _, err := os.Lstat(target); err == nil {
			if _, err := env.Backup(target); err != nil {
				return 0, err
			}
		}
		if err := state.WriteFileAtomic(target, []byte(fromPlaceholder(string(data), env.Home)), 0o644); err != nil {
			return 0, err
		}
	}
	return len(files), nil
}

// liveFiles lists existing files matching Files, relative to Dst, sorted.
func (l Layouts) liveFiles() ([]string, error) {
	var out []string
	for _, pattern := range l.Files {
		matches, err := filepath.Glob(filepath.Join(l.Dst, pattern))
		if err != nil {
			return nil, fmt.Errorf("bad layouts pattern %q: %w", pattern, err)
		}
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && !info.IsDir() {
				rel, err := filepath.Rel(l.Dst, m)
				if err != nil {
					return nil, err
				}
				out = append(out, rel)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// toPlaceholder replaces home, plain and JSON slash-escaped, with the placeholder.
func toPlaceholder(s, home string) string {
	s = strings.ReplaceAll(s, strings.ReplaceAll(home, "/", `\/`), HomePlaceholder)
	return strings.ReplaceAll(s, home, HomePlaceholder)
}

// fromPlaceholder puts this machine's home back; slash-escaped JSON stays valid unescaped.
func fromPlaceholder(s, home string) string {
	return strings.ReplaceAll(s, HomePlaceholder, home)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
