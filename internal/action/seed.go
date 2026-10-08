package action

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/state"
)

// Seed renders a repo template into a file the tool then owns.
type Seed struct {
	Src     string // absolute template path
	Dst     string // absolute
	Secrets []string
}

// NewSeed resolves a manifest seed against env.
func NewSeed(env *Env, s manifest.Seed) Seed {
	return Seed{Src: env.RepoPath(s.Src), Dst: env.Expand(s.Dst), Secrets: s.Secrets}
}

// SeedProbe is the probed state of a seed.
type SeedProbe struct {
	Status   Status
	Detail   string
	Rendered []byte // nil when a secret is missing
	Hash     string // hash of Rendered
	Live     []byte // current dst content, nil when missing
}

// Probe renders the template and compares it with Dst and the hash of the last render.
func (s Seed) Probe(ctx context.Context, secrets *Secrets, lastHash string) (SeedProbe, error) {
	if missing := secrets.Missing(s.Secrets); len(missing) > 0 {
		return SeedProbe{Status: NeedsSecret, Detail: "needs " + strings.Join(missing, ", ")}, nil
	}
	rendered, err := s.Render(ctx, secrets)
	if err != nil {
		return SeedProbe{}, err
	}
	p := SeedProbe{Rendered: rendered, Hash: hashBytes(rendered)}

	live, err := os.ReadFile(s.Dst)
	if errors.Is(err, os.ErrNotExist) {
		p.Status = Missing
		return p, nil
	}
	if err != nil {
		return SeedProbe{}, fmt.Errorf("read %s: %w", s.Dst, err)
	}
	p.Live = live
	liveHash := hashBytes(live)

	switch {
	case bytes.Equal(live, rendered):
		p.Status = OK
	case lastHash == "":
		p.Status, p.Detail = BothChanged, "existing file differs from the template"
	case liveHash == lastHash:
		p.Status, p.Detail = Update, "template changed"
	case p.Hash == lastHash:
		p.Status, p.Detail = LocalEdit, "edited on this machine"
	default:
		p.Status, p.Detail = BothChanged, "template and this machine both changed"
	}
	return p, nil
}

// Render substitutes the listed secrets; any other ${...} stays as written.
func (s Seed) Render(ctx context.Context, secrets *Secrets) ([]byte, error) {
	tmpl, err := os.ReadFile(s.Src)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", s.Src, err)
	}
	out := string(tmpl)
	for _, key := range s.Secrets {
		v, err := secrets.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		out = strings.ReplaceAll(out, "${"+key+"}", v)
	}
	return []byte(out), nil
}

// SeedMode decides what happens when the live file differs.
type SeedMode int

const (
	// Overwrite writes the rendered template (backing up the live file).
	Overwrite SeedMode = iota
	// Keep leaves the live file and records the render so the choice sticks.
	Keep
)

// Apply writes or keeps the seed per mode and returns the hash to remember as the last render.
func (s Seed) Apply(env *Env, p SeedProbe, mode SeedMode) (string, error) {
	switch p.Status {
	case OK, LocalEdit:
		return p.Hash, nil
	case NeedsSecret:
		return "", errors.New(p.Detail)
	case Missing, Update:
		// write below
	default:
		if mode == Keep {
			return p.Hash, nil
		}
	}
	perm := os.FileMode(0o644)
	if len(s.Secrets) > 0 {
		perm = 0o600
	} else if info, err := os.Stat(s.Dst); err == nil {
		perm = info.Mode().Perm()
	}
	if p.Live != nil {
		if _, err := env.Backup(s.Dst); err != nil {
			return "", err
		}
	}
	if err := state.WriteFileAtomic(s.Dst, p.Rendered, perm); err != nil {
		return "", err
	}
	return p.Hash, nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// minSecretLen guards Adopt: shorter values could match unrelated text.
const minSecretLen = 6

// Adopt copies the live file back into the template, turning this machine's secret values back
// into ${VAR} placeholders. It returns the hash to remember as the last render and the secrets
// whose value did not occur in the file (their placeholder is gone from the template).
func (s Seed) Adopt(ctx context.Context, secrets *Secrets) (string, []string, error) {
	live, err := os.ReadFile(s.Dst)
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", s.Dst, err)
	}
	type pair struct{ key, value string }
	var pairs []pair
	for _, key := range s.Secrets {
		v, err := secrets.Get(ctx, key)
		if err != nil {
			return "", nil, fmt.Errorf("%w (needed to put ${%s} back into the template)", err, key)
		}
		if len(v) < minSecretLen {
			return "", nil, fmt.Errorf("secret %s is too short to put back safely; edit %s by hand", key, s.Src)
		}
		pairs = append(pairs, pair{key, v})
	}
	// longest first, so a value contained in another does not split it
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i].value) > len(pairs[j].value) })

	content := string(live)
	var unmatched []string
	for _, p := range pairs {
		if !strings.Contains(content, p.value) {
			unmatched = append(unmatched, p.key)
			continue
		}
		content = strings.ReplaceAll(content, p.value, "${"+p.key+"}")
	}
	info, err := os.Stat(s.Src)
	if err != nil {
		return "", nil, fmt.Errorf("stat %s: %w", s.Src, err)
	}
	if err := state.WriteFileAtomic(s.Src, []byte(content), info.Mode().Perm()); err != nil {
		return "", nil, err
	}
	rendered, err := s.Render(ctx, secrets)
	if err != nil {
		return "", nil, err
	}
	sort.Strings(unmatched)
	return hashBytes(rendered), unmatched, nil
}
