package action

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/testutil"
)

// secretsWith writes a secrets file with values and loads it.
func secretsWith(t *testing.T, values map[string]string) *Secrets {
	t.Helper()
	s, err := LoadSecrets(filepath.Join(t.TempDir(), "secrets.env"), nil)
	require.NoError(t, err)
	for k, v := range values {
		require.NoError(t, s.Set(k, v))
	}
	return s
}

func TestSeedRenderOnlyListedSecrets(t *testing.T) {
	env := testEnv(t, map[string]string{"tool/config.toml": `token = "${TOKEN}"` + "\nhome = \"${HOME}\"\n"})
	s := NewSeed(env, manifest.Seed{Src: "tool/config.toml", Dst: "~/.tool", Secrets: []string{"TOKEN"}})
	out, err := s.Render(context.Background(), secretsWith(t, map[string]string{"TOKEN": "abc"}))
	require.NoError(t, err)
	assert.Equal(t, "token = \"abc\"\nhome = \"${HOME}\"\n", string(out))
}

func TestSeedLifecycle(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t, map[string]string{"tool/config": "v1 ${TOKEN}"})
	seed := NewSeed(env, manifest.Seed{Src: "tool/config", Dst: "~/.config/tool/config", Secrets: []string{"TOKEN"}})
	secrets := secretsWith(t, map[string]string{"TOKEN": "t"})

	probe := func(last string) SeedProbe {
		t.Helper()
		p, err := seed.Probe(ctx, secrets, last)
		require.NoError(t, err)
		return p
	}

	// missing -> rendered with 0600 because it holds a secret
	p := probe("")
	assert.Equal(t, Missing, p.Status)
	last, err := seed.Apply(env, p, Overwrite)
	require.NoError(t, err)
	assert.Equal(t, "v1 t", testutil.ReadFile(t, seed.Dst))
	assert.Equal(t, os.FileMode(0o600), testutil.Perm(t, seed.Dst))
	assert.Equal(t, OK, probe(last).Status)

	// template changes, live untouched -> update re-renders
	testutil.WriteFile(t, seed.Src, "v2 ${TOKEN}")
	p = probe(last)
	assert.Equal(t, Update, p.Status)
	last, err = seed.Apply(env, p, Keep) // mode does not matter for update
	require.NoError(t, err)
	assert.Equal(t, "v2 t", testutil.ReadFile(t, seed.Dst))

	// tool edits the live file -> local edit, left alone
	testutil.WriteFile(t, seed.Dst, "v2 t + local")
	p = probe(last)
	assert.Equal(t, LocalEdit, p.Status)
	assert.True(t, p.Status.Settled())

	// template changes too -> both changed; keep leaves live and records the render
	testutil.WriteFile(t, seed.Src, "v3 ${TOKEN}")
	p = probe(last)
	assert.Equal(t, BothChanged, p.Status)
	last, err = seed.Apply(env, p, Keep)
	require.NoError(t, err)
	assert.Equal(t, "v2 t + local", testutil.ReadFile(t, seed.Dst))
	assert.Equal(t, LocalEdit, probe(last).Status)

	// overwrite after both changed backs the live file up
	testutil.WriteFile(t, seed.Src, "v4 ${TOKEN}")
	p = probe(last)
	require.Equal(t, BothChanged, p.Status)
	_, err = seed.Apply(env, p, Overwrite)
	require.NoError(t, err)
	assert.Equal(t, "v4 t", testutil.ReadFile(t, seed.Dst))
	backup := filepath.Join(env.BackupRoot, "20261008-010203", ".config", "tool", "config")
	assert.Equal(t, "v2 t + local", testutil.ReadFile(t, backup))
}

func TestSeedExistingFileFirstRun(t *testing.T) {
	env := testEnv(t, map[string]string{"c": "template"})
	seed := NewSeed(env, manifest.Seed{Src: "c", Dst: "~/c"})
	testutil.WriteFile(t, seed.Dst, "pre-existing")
	p, err := seed.Probe(context.Background(), secretsWith(t, nil), "")
	require.NoError(t, err)
	assert.Equal(t, BothChanged, p.Status)
	assert.Equal(t, "existing file differs from the template", p.Detail)
}

func TestSeedNeedsSecret(t *testing.T) {
	env := testEnv(t, map[string]string{"c": "${A} ${B}"})
	seed := NewSeed(env, manifest.Seed{Src: "c", Dst: "~/c", Secrets: []string{"B", "A"}})
	p, err := seed.Probe(context.Background(), secretsWith(t, map[string]string{"A": "1"}), "")
	require.NoError(t, err)
	assert.Equal(t, SeedProbe{Status: NeedsSecret, Detail: "needs B"}, p)
	_, err = seed.Apply(env, p, Overwrite)
	require.ErrorContains(t, err, "needs B")
}

func TestSeedKeepsPermsWithoutSecrets(t *testing.T) {
	env := testEnv(t, map[string]string{"c": "new"})
	seed := NewSeed(env, manifest.Seed{Src: "c", Dst: "~/c"})
	testutil.WriteFile(t, seed.Dst, "old")
	require.NoError(t, os.Chmod(seed.Dst, 0o640))
	p, err := seed.Probe(context.Background(), secretsWith(t, nil), "")
	require.NoError(t, err)
	_, err = seed.Apply(env, p, Overwrite)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), testutil.Perm(t, seed.Dst))
}
