package manifest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const resolveSample = `
version: 1
components:
  - {id: brew, group: base, platforms: [darwin], cmds: [{run: x}]}
  - {id: core, group: base, platforms: [darwin], requires: [brew], cmds: [{run: x}]}
  - {id: apt-core, group: base, platforms: [linux], cmds: [{run: x}]}
  - {id: zsh, group: shell, requires: [core, apt-core], cmds: [{run: x}]}
  - {id: theme, group: shell, requires: [zsh], cmds: [{run: x}]}
  - {id: mac-app, group: shell, platforms: [darwin], cmds: [{run: x}]}
`

func TestResolveDarwin(t *testing.T) {
	m := parse(t, resolveSample)
	res, err := m.Resolve(Darwin, []string{"theme"})
	require.NoError(t, err)
	assert.Equal(t, []string{"brew", "core", "zsh", "theme"}, res.Order)
	assert.Equal(t, map[string]string{"zsh": "theme", "core": "zsh", "brew": "core"}, res.AddedBecause)
	assert.Empty(t, res.Unsupported)
}

func TestResolveLinuxDropsDarwinRequires(t *testing.T) {
	m := parse(t, resolveSample)
	res, err := m.Resolve(Linux, []string{"zsh", "mac-app"})
	require.NoError(t, err)
	assert.Equal(t, []string{"apt-core", "zsh"}, res.Order)
	assert.Equal(t, map[string]string{"apt-core": "zsh"}, res.AddedBecause)
	assert.Equal(t, []string{"mac-app"}, res.Unsupported)
}

func TestResolvePickedPrerequisiteIsNotAdded(t *testing.T) {
	m := parse(t, resolveSample)
	res, err := m.Resolve(Darwin, []string{"core", "brew"})
	require.NoError(t, err)
	assert.Equal(t, []string{"brew", "core"}, res.Order)
	assert.Empty(t, res.AddedBecause)
}

func TestResolveUnknown(t *testing.T) {
	m := parse(t, resolveSample)
	_, err := m.Resolve(Darwin, []string{"ghost"})
	require.ErrorContains(t, err, `unknown component "ghost"`)
}

func TestPlatformComponents(t *testing.T) {
	m := parse(t, resolveSample)
	assert.Equal(t, []string{"apt-core", "zsh", "theme"}, m.PlatformComponents(Linux))
}

func TestProfilesOf(t *testing.T) {
	m := parse(t, resolveSample+`
profiles:
  personal: {components: [theme, mac-app]}
  work: {components: ["@personal"], exclude: [mac-app]}
`)
	got, err := m.ProfilesOf(Darwin)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"brew":    {"personal", "work"}, // pulled in by theme's prerequisites
		"core":    {"personal", "work"},
		"zsh":     {"personal", "work"},
		"theme":   {"personal", "work"},
		"mac-app": {"personal"},
	}, got)
}
