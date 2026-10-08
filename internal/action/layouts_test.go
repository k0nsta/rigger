package action

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/testutil"
)

func testLayouts(t *testing.T, env *Env) Layouts {
	t.Helper()
	return NewLayouts(env, manifest.Layouts{
		Src:     "app/layouts",
		Dst:     "~/Library/App",
		Files:   []string{"windows.json", "windows/*.json"},
		Process: "app",
	})
}

func TestLayoutsExportImportRoundTrip(t *testing.T) {
	src := testEnv(t, nil)
	l := testLayouts(t, src)
	homeEscaped := filepath.ToSlash(src.Home)
	testutil.WriteFile(t, filepath.Join(l.Dst, "windows.json"), `{"frontmost":"w1"}`)
	testutil.WriteFile(t, filepath.Join(l.Dst, "windows", "w1.json"),
		`{"dir":"`+jsonEscape(homeEscaped+"/code")+`","plain":"`+homeEscaped+`/notes"}`)
	testutil.WriteFile(t, filepath.Join(l.Dst, "other.json"), "not a layout file")

	p, err := l.Probe("mac-a")
	require.NoError(t, err)
	assert.Equal(t, NoSnapshot, p.Status)

	n, err := l.Export(src.Home, "mac-a")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	exported := testutil.ReadFile(t, filepath.Join(l.Src, "mac-a", "windows", "w1.json"))
	assert.Equal(t, `{"dir":"{{RIGGER_HOME}}\/code","plain":"{{RIGGER_HOME}}/notes"}`, exported)
	assert.NotContains(t, exported, src.Home)

	p, err = l.Probe("mac-a")
	require.NoError(t, err)
	assert.Equal(t, OK, p.Status)

	// a fresh machine with the same repo and an empty app dir
	dst := &Env{Home: t.TempDir(), Repo: src.Repo, BackupRoot: src.BackupRoot, Now: src.Now}
	l2 := testLayouts(t, dst)
	p, err = l2.Probe("mac-b")
	require.NoError(t, err)
	assert.Equal(t, LayoutsProbe{Status: FreshTarget, Detail: "import from mac-a", Hosts: []string{"mac-a"}}, p)

	_, err = l2.Import(dst, "mac-a", true)
	require.ErrorContains(t, err, "quit app before importing")

	n, err = l2.Import(dst, "mac-a", false)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	imported := testutil.ReadFile(t, filepath.Join(l2.Dst, "windows", "w1.json"))
	assert.Equal(t, `{"dir":"`+dst.Home+`\/code","plain":"`+dst.Home+`/notes"}`, imported)
}

func TestLayoutsImportBacksUpExisting(t *testing.T) {
	env := testEnv(t, map[string]string{"app/layouts/old/windows.json": "{}"})
	l := testLayouts(t, env)
	testutil.WriteFile(t, filepath.Join(l.Dst, "windows.json"), `{"mine":true}`)
	_, err := l.Import(env, "old", false)
	require.NoError(t, err)
	backup := filepath.Join(env.BackupRoot, "20261008-010203", "Library", "App", "windows.json")
	assert.Equal(t, `{"mine":true}`, testutil.ReadFile(t, backup))
}

func TestLayoutsProbeNothing(t *testing.T) {
	env := testEnv(t, nil)
	p, err := testLayouts(t, env).Probe("mac")
	require.NoError(t, err)
	assert.Equal(t, OK, p.Status, "no app state and no snapshots: nothing to do")
}

// jsonEscape escapes "/" the way Swift's JSONEncoder does.
func jsonEscape(s string) string {
	out := ""
	for _, r := range s {
		if r == '/' {
			out += `\/`
			continue
		}
		out += string(r)
	}
	return out
}
