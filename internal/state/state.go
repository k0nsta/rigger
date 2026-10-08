// Package state keeps what rigger remembers: local machine state outside the setup repo
// (repo path, host name, seed render hashes) and the per-host selection inside it.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Local is ~/.config/rigger/state.yaml; never tracked.
type Local struct {
	Repo string `yaml:"repo,omitempty"`
	Host string `yaml:"host,omitempty"`
	// Seeds maps an expanded seed dst to the sha256 of its last render.
	Seeds map[string]string `yaml:"seeds,omitempty"`
}

// ConfigDir is $XDG_CONFIG_HOME/rigger, else ~/.config/rigger.
func ConfigDir(home string) string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "rigger")
	}
	return filepath.Join(home, ".config", "rigger")
}

// LocalPath is the local state file for home.
func LocalPath(home string) string { return filepath.Join(ConfigDir(home), "state.yaml") }

// LoadLocal reads the local state; a missing file is empty state.
func LoadLocal(path string) (*Local, error) {
	var l Local
	if err := readYAML(path, &l); err != nil {
		return nil, err
	}
	if l.Seeds == nil {
		l.Seeds = map[string]string{}
	}
	return &l, nil
}

// Save writes the local state atomically.
func (l *Local) Save(path string) error { return writeYAML(path, l, 0o600) }

// HostFile is hosts/<name>.yaml in the setup repo: how this machine differs from its profile.
// Storing only the difference lets later profile changes reach every machine on its next sync.
type HostFile struct {
	Profile string `yaml:"profile,omitempty"`
	// Add are components picked here on top of the profile.
	Add []string `yaml:"add,omitempty"`
	// Remove are profile components unpicked here.
	Remove []string `yaml:"remove,omitempty"`
	// ExcludePackages are Brewfile entries unpicked here on top of the profile's default excludes.
	ExcludePackages []string `yaml:"exclude_packages,omitempty"`
	// IncludePackages are profile default excludes picked here anyway.
	IncludePackages []string `yaml:"include_packages,omitempty"`
	// IgnorePackages are installed here on purpose but belong to no Brewfile.
	IgnorePackages []string `yaml:"ignore_packages,omitempty"`
	// Components is the old full-list format; when set it wins over Profile/Add/Remove.
	Components []string `yaml:"components,omitempty"`
}

// HostPath is the host file for name in repo.
func HostPath(repo, name string) string { return filepath.Join(repo, "hosts", name+".yaml") }

// LoadHost reads a host file; a missing file returns nil without error.
func LoadHost(path string) (*HostFile, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var h HostFile
	if err := readYAML(path, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Save writes the host file atomically.
func (h *HostFile) Save(path string) error { return writeYAML(path, h, 0o644) }

func readYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeYAML(path string, v any, perm os.FileMode) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return WriteFileAtomic(path, data, perm)
}

// WriteFileAtomic writes data to a temp file next to path and renames it into place.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
