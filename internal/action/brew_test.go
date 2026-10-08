package action

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/k0nsta/rigger/internal/action/mocks"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/testutil"
)

const sampleBrewfile = `# Command-line basics
tap "owner/apps"
brew "git"
# Terminal JSON viewer
brew "fx"
brew "postgresql@14", restart_service: :changed
brew "owner/apps/tool"
  cask "raycast"
cask "font-fira-code"
cask "slack"
mas "Keynote", id: 409183694
cask_args appdir: "/Applications"
`

func TestParseBrewfile(t *testing.T) {
	got := ParseBrewfile([]byte(sampleBrewfile))
	assert.Equal(t, []BrewPackage{
		{Kind: KindTap, Name: "owner/apps", Comment: "Command-line basics"},
		{Kind: KindBrew, Name: "git"},
		{Kind: KindBrew, Name: "fx", Comment: "Terminal JSON viewer"},
		{Kind: KindBrew, Name: "postgresql@14"},
		{Kind: KindBrew, Name: "owner/apps/tool"},
		{Kind: KindCask, Name: "raycast"},
		{Kind: KindCask, Name: "font-fira-code"},
		{Kind: KindCask, Name: "slack"},
		{Kind: KindMas, Name: "Keynote"},
	}, got)
	assert.Equal(t, "tool", got[4].Short())
	assert.Equal(t, "owner/apps", got[0].Short())
}

// expectRun makes the mock answer one command (matched by its String form).
func expectRun(r *mocks.MockRunner, cmd, output string, code int) *gomock.Call {
	return r.EXPECT().Run(gomock.Any(), cmdString(cmd)).Return(sh.Result{Output: output, Code: code}, nil)
}

type cmdMatcher string

func cmdString(s string) gomock.Matcher { return cmdMatcher(s) }
func (m cmdMatcher) Matches(x any) bool { c, ok := x.(sh.Cmd); return ok && c.String() == string(m) }
func (m cmdMatcher) String() string     { return "cmd " + string(m) }

func TestLoadBrewInventory(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	expectRun(r, "brew list --formula -1", "git\nfx\n", 0)
	expectRun(r, "brew list --cask -1", "slack\n", 0)
	expectRun(r, "brew tap", "owner/apps\n", 0)
	inv, err := LoadBrewInventory(context.Background(), r)
	require.NoError(t, err)
	assert.True(t, inv.Formulae["fx"])
	assert.True(t, inv.Casks["slack"])
	assert.True(t, inv.Taps["owner/apps"])
}

func TestLoadBrewInventoryFailure(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	expectRun(r, "brew list --formula -1", "Error: boom", 1)
	_, err := LoadBrewInventory(context.Background(), r)
	require.ErrorContains(t, err, "boom")
}

func TestBrewProbe(t *testing.T) {
	home := t.TempDir()
	paths := DefaultAppPaths(home)
	paths.Apps = []string{filepath.Join(home, "Applications")}
	paths.Fonts = []string{filepath.Join(home, "Library", "Fonts")}
	testutil.WriteFile(t, filepath.Join(home, "Applications", "Raycast.app", "Contents", "Info.plist"), "x")

	env := &Env{Home: home, Repo: testutil.Tree(t, map[string]string{"brew/core.Brewfile": sampleBrewfile})}
	b, err := NewBrew(env, "brew/core.Brewfile")
	require.NoError(t, err)

	inv := &BrewInventory{
		Formulae: map[string]bool{"git": true, "tool": true},
		Casks:    map[string]bool{"slack": true},
		Taps:     map[string]bool{"owner/apps": true},
	}
	r := mocks.NewMockRunner(gomock.NewController(t))
	expectRun(r, "brew info --cask --json=v2 raycast font-fira-code", `{"casks":[
	  {"token":"raycast","artifacts":[{"uninstall":[{"quit":"x"}]},{"app":["Raycast.app"]}]},
	  {"token":"font-fira-code","artifacts":[{"font":["FiraCode-Regular.ttf"]}]}
	]}`, 0)

	p, err := b.Probe(context.Background(), r, inv, map[string]bool{"fx": true}, paths)
	require.NoError(t, err)
	assert.Equal(t, Todo, p.Status)
	assert.Equal(t, "2 to install", p.Detail)
	assert.Equal(t, []string{"postgresql@14", "font-fira-code"}, names(p.Missing))
	assert.Equal(t, []string{"raycast"}, names(p.Present))
	assert.Equal(t, []string{"fx"}, names(p.Excluded))
	assert.Equal(t, []string{
		"HOMEBREW_BUNDLE_BREW_SKIP=fx",
		"HOMEBREW_BUNDLE_CASK_SKIP=raycast",
	}, p.SkipEnv())
}

func TestBrewProbeAllInstalled(t *testing.T) {
	env := &Env{Home: t.TempDir(), Repo: testutil.Tree(t, map[string]string{"B": "brew \"git\"\ncask \"slack\"\n"})}
	b, err := NewBrew(env, "B")
	require.NoError(t, err)
	inv := &BrewInventory{Formulae: map[string]bool{"git": true}, Casks: map[string]bool{"slack": true}, Taps: map[string]bool{}}
	r := mocks.NewMockRunner(gomock.NewController(t)) // no brew info call expected
	p, err := b.Probe(context.Background(), r, inv, nil, DefaultAppPaths(env.Home))
	require.NoError(t, err)
	assert.Equal(t, OK, p.Status)
	assert.Empty(t, p.SkipEnv())
}

func TestFoundHonoursTarget(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "Renamed.app"), "x")
	assert.True(t, found([]byte(`["Original.app",{"target":"Renamed.app"}]`), []string{dir}))
	assert.False(t, found([]byte(`["Original.app"]`), []string{dir}))
	assert.False(t, found(nil, []string{dir}))
	assert.False(t, found([]byte(`[{"weird":true}]`), []string{dir}))
}

func TestBrewApply(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	b := Brew{File: "/repo/brew/core.Brewfile"}
	p := BrewProbe{
		Missing:  []BrewPackage{{Kind: KindBrew, Name: "git"}},
		Excluded: []BrewPackage{{Kind: KindCask, Name: "slack"}, {Kind: KindTap, Name: "x/y"}},
	}
	r.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		assert.Equal(t, "brew bundle install --no-upgrade --file /repo/brew/core.Brewfile", c.String())
		assert.Equal(t, []string{"HOMEBREW_BUNDLE_CASK_SKIP=slack", "HOMEBREW_BUNDLE_TAP_SKIP=x/y"}, c.Env)
		return sh.Result{Code: 1}, nil
	})
	require.ErrorContains(t, b.Apply(context.Background(), r, p, nil), "brew bundle failed (exit 1)")

	// nothing missing: brew is not called
	require.NoError(t, b.Apply(context.Background(), r, BrewProbe{}, nil))
}

func names(pkgs []BrewPackage) []string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, p.Name)
	}
	return out
}
