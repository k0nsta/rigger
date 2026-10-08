package action

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/k0nsta/rigger/internal/state"
)

// Resolver reads an op:// reference (1Password CLI `op read`).
type Resolver func(ctx context.Context, ref string) (string, error)

// Secrets is the local KEY=value file (0600, never in git). Values starting with op:// are
// resolved on first use.
type Secrets struct {
	path     string
	values   map[string]string
	resolved map[string]string
	resolve  Resolver
}

// LoadSecrets reads path; a missing file is empty. A file readable by others is tightened to 0600.
func LoadSecrets(path string, resolve Resolver) (*Secrets, error) {
	s := &Secrets{path: path, values: map[string]string{}, resolved: map[string]string{}, resolve: resolve}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets: %w", err)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("tighten %s to 0600: %w", path, err)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%s:%d: expected KEY=value", path, line)
		}
		s.values[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}
	return s, nil
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// Has reports whether key is set (an op:// reference counts as set).
func (s *Secrets) Has(key string) bool {
	v, ok := s.values[key]
	return ok && v != ""
}

// Missing returns the keys from want that are not set, sorted.
func (s *Secrets) Missing(want []string) []string {
	var out []string
	for _, k := range want {
		if !s.Has(k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Get returns the value for key, resolving op:// references.
func (s *Secrets) Get(ctx context.Context, key string) (string, error) {
	v, ok := s.values[key]
	if !ok || v == "" {
		return "", fmt.Errorf("secret %s is not set in %s", key, s.path)
	}
	if !strings.HasPrefix(v, "op://") {
		return v, nil
	}
	if r, ok := s.resolved[key]; ok {
		return r, nil
	}
	if s.resolve == nil {
		return "", fmt.Errorf("secret %s is a 1Password reference but op is not available", key)
	}
	r, err := s.resolve(ctx, v)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", key, err)
	}
	r = strings.TrimRight(r, "\n")
	s.resolved[key] = r
	return r, nil
}

// Set stores key=value and rewrites the file with 0600.
func (s *Secrets) Set(key, value string) error {
	s.values[key] = value
	delete(s.resolved, key)
	keys := make([]string, 0, len(s.values))
	for k := range s.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# rigger secrets: KEY=value; values may be op:// references. Never commit this file.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, s.values[k])
	}
	return state.WriteFileAtomic(s.path, []byte(b.String()), 0o600)
}

// Env returns KEY=value pairs for keys, resolved, for a command environment.
func (s *Secrets) Env(ctx context.Context, keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v, err := s.Get(ctx, k)
		if err != nil {
			return nil, err
		}
		out = append(out, k+"="+v)
	}
	return out, nil
}
