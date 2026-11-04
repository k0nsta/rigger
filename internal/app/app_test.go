package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/state"
	"github.com/k0nsta/rigger/internal/testutil"
)

const deltaManifest = `
version: 1
profiles:
  laptop:
    components: [a, b]
    exclude_packages: [big-cask]
components:
  - {id: a, group: g, cmds: [{run: "true"}]}
  - {id: b, group: g, cmds: [{run: "true"}]}
  - {id: c, group: g, cmds: [{run: "true"}]}
  - {id: d, group: g, cmds: [{run: "true"}]}
`

// open makes a session over a temp repo with manifest yaml and host "box".
func open(t *testing.T, manifestYAML string) *Session {
	t.Helper()
	repo := testutil.Tree(t, map[string]string{"manifest.yaml": manifestYAML})
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	s, err := Open(Options{Repo: repo, Host: "box", Home: home})
	require.NoError(t, err)
	return s
}

// reopen loads the same repo again (as another run, or another machine after a pull).
func reopen(t *testing.T, s *Session) *Session {
	t.Helper()
	again, err := Open(Options{Repo: s.Repo, Host: s.Host, Home: s.Home})
	require.NoError(t, err)
	return again
}

func TestHostFileStoresOnlyTheDifference(t *testing.T) {
	s := open(t, deltaManifest)
	sel, err := s.Select("laptop", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, sel.Picked)
	assert.True(t, sel.Exclude["big-cask"])

	// this machine: drops b, adds c, unpicks fx, wants big-cask after all
	sel.Picked = []string{"a", "c"}
	sel.Exclude = map[string]bool{"fx": true}
	require.NoError(t, s.Save(sel))

	hf, err := state.LoadHost(state.HostPath(s.Repo, "box"))
	require.NoError(t, err)
	assert.Equal(t, &state.HostFile{
		Profile:         "laptop",
		Add:             []string{"c"},
		Remove:          []string{"b"},
		ExcludePackages: []string{"fx"},
		IncludePackages: []string{"big-cask"},
	}, hf)

	again, err := reopen(t, s).Select("", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, again.Picked)
	assert.Equal(t, map[string]bool{"fx": true}, again.Exclude)
}

func TestOpenRemembersRepoWhenCwdIsAnotherCheckout(t *testing.T) {
	remembered := testutil.Tree(t, map[string]string{"manifest.yaml": deltaManifest})
	other := testutil.Tree(t, map[string]string{"manifest.yaml": deltaManifest})
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	l := &state.Local{Repo: remembered, Host: "box"}
	require.NoError(t, l.Save(state.LocalPath(home)))
	s, err := Open(Options{Home: home, Cwd: other})
	require.NoError(t, err)
	assert.Equal(t, remembered, s.Repo)
	assert.Equal(t, other, s.Elsewhere)
	assert.Empty(t, s.Engine.Env.PrevRepo, "using the remembered checkout is not a repo move")
}

func TestProfileAdditionsReachSavedHosts(t *testing.T) {
	s := open(t, deltaManifest)
	sel, err := s.Select("laptop", nil)
	require.NoError(t, err)
	require.NoError(t, s.Save(sel))

	// someone adds d to the profile and pulls on this machine
	updated := `
version: 1
profiles:
  laptop:
    components: [a, b, d]
components:
  - {id: a, group: g, cmds: [{run: "true"}]}
  - {id: b, group: g, cmds: [{run: "true"}]}
  - {id: c, group: g, cmds: [{run: "true"}]}
  - {id: d, group: g, cmds: [{run: "true"}]}
`
	testutil.WriteFile(t, filepath.Join(s.Repo, "manifest.yaml"), updated)
	got, err := reopen(t, s).Select("", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "d"}, got.Picked)
	assert.Empty(t, got.Exclude, "the profile dropped its default excludes, so they are gone here too")
}

func TestRemovedComponentsAreDropped(t *testing.T) {
	s := open(t, deltaManifest)
	testutil.WriteFile(t, state.HostPath(s.Repo, "box"), "profile: laptop\nadd: [c, gone]\n")
	got, err := reopen(t, s).Select("", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got.Picked)
}

func TestLegacyFullListStillWorks(t *testing.T) {
	s := open(t, deltaManifest)
	testutil.WriteFile(t, state.HostPath(s.Repo, "box"), "profile: laptop\ncomponents: [a, d]\nexclude_packages: [fx]\n")
	got, err := reopen(t, s).Select("", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "d"}, got.Picked)
	assert.True(t, got.Exclude["fx"])
	assert.True(t, got.Exclude["big-cask"])
}

func TestSelectOnlyWithoutProfileSavesAdds(t *testing.T) {
	s := open(t, deltaManifest)
	sel, err := s.Select("", []string{"c"})
	require.NoError(t, err)
	require.NoError(t, s.Save(sel))
	data, err := os.ReadFile(state.HostPath(s.Repo, "box"))
	require.NoError(t, err)
	assert.Equal(t, "add:\n    - c\n", string(data))
}

func TestSanitizeHost(t *testing.T) {
	assert.Equal(t, "work-mac", sanitizeHost(" Work Mac "))
	assert.Equal(t, "macos-c30", sanitizeHost("MacOS-C30"))
	assert.Equal(t, "a-b", sanitizeHost("a.b"))
}

func TestNamingTheHostProfileKeepsHostChanges(t *testing.T) {
	s := open(t, deltaManifest)
	testutil.WriteFile(t, state.HostPath(s.Repo, "box"), "profile: laptop\nadd: [c]\nremove: [b]\nexclude_packages: [fx]\n")
	s = reopen(t, s)

	sel, err := s.Select("laptop", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, sel.Picked, "b stays removed, c stays added")
	assert.Equal(t, map[string]bool{"fx": true, "big-cask": true}, sel.Exclude)

	require.NoError(t, s.Save(sel))
	hf, err := state.LoadHost(state.HostPath(s.Repo, "box"))
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, hf.Remove, "a save does not lose the remove")
}

const originManifest = `
version: 1
profiles:
  laptop:
    components: [a, b]
components:
  - {id: a, group: g, cmds: [{run: "true"}]}
  - {id: b, group: g, requires: [lib], cmds: [{run: "true"}]}
  - {id: c, group: g, cmds: [{run: "true"}]}
  - {id: lib, group: g, cmds: [{run: "true"}]}
`

func TestOrigin(t *testing.T) {
	s := open(t, originManifest)
	testutil.WriteFile(t, state.HostPath(s.Repo, "box"), "profile: laptop\nadd: [c]\n")
	s = reopen(t, s)
	sel, err := s.Select("", nil)
	require.NoError(t, err)
	res, err := s.Manifest.Resolve(s.Platform, sel.Picked)
	require.NoError(t, err)
	assert.Equal(t, "in profile laptop", s.Origin(sel, res, "a"))
	assert.Equal(t, "added in hosts/box.yaml", s.Origin(sel, res, "c"))
	assert.Equal(t, "needed by b", s.Origin(sel, res, "lib"))
}

func TestDropFromHost(t *testing.T) {
	s := open(t, originManifest)
	testutil.WriteFile(t, state.HostPath(s.Repo, "box"), "profile: laptop\nadd: [c]\n")
	s = reopen(t, s)

	require.NoError(t, s.DropFromHost("a"), "a profile component joins remove")
	require.NoError(t, s.DropFromHost("c"), "a host addition leaves add")
	require.ErrorContains(t, s.DropFromHost("lib"), "lib is needed by b: drop that instead")

	hf, err := state.LoadHost(state.HostPath(s.Repo, "box"))
	require.NoError(t, err)
	assert.Equal(t, &state.HostFile{Profile: "laptop", Remove: []string{"a"}}, hf)
	sel, err := reopen(t, s).Select("", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, sel.Picked)
}
