package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/testutil"
)

const testManifest = `
version: 1
groups: [packages, shell, configs]
profiles:
  laptop:
    description: a test laptop
    components: [pkgs, zsh, tool]
  minimal:
    components: [zsh]
components:
  - {id: pkgs, title: Packages, group: packages, brewfile: Brewfile}
  - id: zsh
    title: zsh
    group: shell
    links: [{src: zsh/.zshrc, dst: ~/.zshrc}]
  - id: tool
    title: Tool config
    group: configs
    requires: [helper]
    seeds: [{src: tool/config, dst: ~/.config/tool/config, secrets: [TOOL_TOKEN]}]
  - id: helper
    group: configs
    cmds: [{name: marker, check: test -f "$HOME/.helper", run: touch "$HOME/.helper"}]
`

// fakeBrew is a brew on PATH that knows git is installed and records bundle runs.
const fakeBrew = `#!/bin/sh
case "$1 $2" in
  "list --formula") echo git ;;
  "list --cask") ;;
  "tap ") ;;
  "info --cask") echo '{"casks":[]}' ;;
  "bundle install") echo "bundle $HOMEBREW_BUNDLE_BREW_SKIP" > "$HOME/.brew-bundle"; echo "Installing jq" ;;
  *) [ "$1" = tap ] && exit 0; echo "unexpected brew $*" >&2; exit 1 ;;
esac
`

// session opens a real session over a temp repo and HOME with a fake brew first on PATH.
func session(t *testing.T) *app.Session {
	t.Helper()
	repo := testutil.Tree(t, map[string]string{
		"manifest.yaml": testManifest,
		"Brewfile":      "brew \"git\"\n# JSON tool\nbrew \"jq\"\nbrew \"fx\"\n",
		"zsh/.zshrc":    "repo zshrc\n",
		"tool/config":   "token=${TOOL_TOKEN}\n",
	})
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "brew"), []byte(fakeBrew), 0o755))
	testutil.WriteFile(t, filepath.Join(home, ".zshrc"), "my own zshrc\n")

	s, err := app.Open(app.Options{
		Repo: repo, Host: "box", Home: home,
		ExtraEnv: []string{"PATH=" + bin + ":/usr/bin:/bin"},
	})
	require.NoError(t, err)
	return s
}

func newModel(t *testing.T, s *app.Session) *Model {
	t.Helper()
	cfg := Config{
		Host: s.Host, HostIsNew: s.HostIsNew, Platform: s.Platform, Repo: s.Repo,
		Components: s.PlatformComponents(), Groups: s.Manifest.GroupOrder(), Manual: s.Manual,
	}
	for _, name := range s.Profiles() {
		cfg.Profiles = append(cfg.Profiles, Profile{Name: name, Description: s.Manifest.Profiles[name].Description})
	}
	m := New(context.Background(), cfg, s, s, s)
	pump(t, m, nil, m.Init())
	return m
}

// pump feeds msg to the model and keeps running the commands it returns. Timer-driven
// messages (spinner ticks, cursor blinks) are dropped so the loop ends.
func pump(t *testing.T, m *Model, msg tea.Msg, first tea.Cmd) {
	t.Helper()
	var queue []tea.Msg
	if msg != nil {
		queue = append(queue, msg)
	}
	queue = append(queue, run(t, first)...)
	for steps := 0; len(queue) > 0; steps++ {
		require.Less(t, steps, 500, "message loop did not settle")
		next := queue[0]
		queue = queue[1:]
		_, cmd := m.Update(next)
		queue = append(queue, run(t, cmd)...)
	}
}

func run(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(5 * time.Second):
		return nil // a timer that never fires in tests
	}
	switch v := msg.(type) {
	case nil, tea.QuitMsg, spinner.TickMsg:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range v {
			out = append(out, run(t, c)...)
		}
		return out
	}
	if strings.Contains(reflect.TypeOf(msg).PkgPath(), "cursor") {
		return nil
	}
	return []tea.Msg{msg}
}

func key(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		pump(t, m, key(k), nil)
	}
}

func typeText(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		press(t, m, string(r))
	}
}

func view(m *Model) string { return m.View().Content }

func cursorOn(t *testing.T, m *Model, id string) {
	t.Helper()
	for i, r := range m.rows {
		if r.comp != nil && r.comp.ID == id {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no row %s", id)
}

func TestFullFlow(t *testing.T) {
	s := session(t)
	m := newModel(t, s)

	// first run: name the machine
	require.Equal(t, scrHost, m.screen)
	assert.Contains(t, view(m), "Name this machine")
	m.input.SetValue("Test Mac")
	press(t, m, "enter")
	assert.Equal(t, "test-mac", s.Host)

	// pick the laptop profile
	require.Equal(t, scrProfile, m.screen)
	assert.Contains(t, view(m), "a test laptop")
	press(t, m, "enter")
	require.Equal(t, scrComponents, m.screen, view(m))
	out := view(m)
	assert.Contains(t, out, "PACKAGES")
	assert.Contains(t, out, "2 to install")
	assert.Contains(t, out, "[+]", "helper is pulled in by tool")
	assert.Contains(t, out, "needed by tool")

	// unpick fx inside the Brewfile
	cursorOn(t, m, "pkgs")
	press(t, m, "enter")
	require.Equal(t, scrPackages, m.screen)
	assert.Contains(t, view(m), "JSON tool", "Brewfile comments are shown")
	press(t, m, "down", "down", "space", "esc")
	assert.True(t, m.exclude["fx"])
	assert.Contains(t, view(m), "1 unpicked")

	// review: the existing ~/.zshrc differs; adopt it
	press(t, m, "c")
	require.Equal(t, scrReview, m.screen, view(m))
	out = view(m)
	assert.Contains(t, out, "Plan: 4 component(s) will change")
	assert.Contains(t, out, "keep the repo version")
	assert.Contains(t, out, "Will ask for secrets: TOOL_TOKEN")
	press(t, m, "space")
	assert.Contains(t, view(m), "adopt this machine's file into the repo")

	// apply: asked for the secret, then everything runs
	press(t, m, "enter")
	require.Equal(t, scrSecret, m.screen)
	typeText(t, m, "s3cret")
	press(t, m, "enter")
	require.Equal(t, scrFinish, m.screen, view(m))

	out = view(m)
	assert.Contains(t, out, "4 done")
	assert.Contains(t, out, "hosts/test-mac.yaml")
	for _, o := range m.Outcomes() {
		require.NoError(t, o.Err, o.Component)
	}

	home := s.Home
	assert.Equal(t, filepath.Join(s.Repo, "zsh", ".zshrc"), testutil.Readlink(t, filepath.Join(home, ".zshrc")))
	assert.Equal(t, "my own zshrc\n", testutil.ReadFile(t, filepath.Join(s.Repo, "zsh", ".zshrc")), "adopted")
	assert.Equal(t, "token=s3cret\n", testutil.ReadFile(t, filepath.Join(home, ".config", "tool", "config")))
	assert.FileExists(t, filepath.Join(home, ".helper"))
	assert.Equal(t, "bundle fx\n", testutil.ReadFile(t, filepath.Join(home, ".brew-bundle")), "fx skipped")

	hostFile := testutil.ReadFile(t, filepath.Join(s.Repo, "hosts", "test-mac.yaml"))
	assert.Contains(t, hostFile, "profile: laptop")
	assert.Contains(t, hostFile, "- fx")
	assert.NotContains(t, hostFile, "- helper", "auto-added prerequisites are not saved as picks")
}

func TestToggleAndNavigation(t *testing.T) {
	s := session(t)
	_, err := s.SetHost("nav")
	require.NoError(t, err)
	m := newModel(t, s)
	m.screen = scrProfile
	press(t, m, "down", "enter") // minimal
	require.Equal(t, scrComponents, m.screen)
	assert.Equal(t, []string{"zsh"}, m.pickedIDs())

	press(t, m, "a")
	assert.Len(t, m.pickedIDs(), 4)
	press(t, m, "n")
	assert.Empty(t, m.pickedIDs())
	press(t, m, "c")
	assert.Equal(t, scrComponents, m.screen, "nothing picked: stay")

	cursorOn(t, m, "zsh")
	press(t, m, "space")
	assert.Equal(t, []string{"zsh"}, m.pickedIDs())
	press(t, m, "esc")
	assert.Equal(t, scrProfile, m.screen)
}

func TestNothingToDo(t *testing.T) {
	s := session(t)
	require.NoError(t, os.Remove(filepath.Join(s.Home, ".zshrc")))
	require.NoError(t, os.Symlink(filepath.Join(s.Repo, "zsh", ".zshrc"), filepath.Join(s.Home, ".zshrc")))
	_, err := s.SetHost("done")
	require.NoError(t, err)
	m := newModel(t, s)
	m.screen = scrProfile
	press(t, m, "down", "enter", "c")
	require.Equal(t, scrReview, m.screen)
	assert.Contains(t, view(m), "Everything picked is already in place")
	press(t, m, "enter")
	assert.Equal(t, scrFinish, m.screen)
	assert.Contains(t, view(m), "nothing to change")
	assert.FileExists(t, filepath.Join(s.Repo, "hosts", "done.yaml"))
}

func TestBuildRowsGroupOrder(t *testing.T) {
	s := session(t)
	rows := buildRows(s.PlatformComponents(), []string{"configs", "packages"})
	var groups []string
	for _, r := range rows {
		if r.comp == nil {
			groups = append(groups, r.group)
		}
	}
	assert.Equal(t, []string{"configs", "packages", "shell"}, groups, "undeclared groups go last")
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "ab…", truncate("abcdef", 3))
}
