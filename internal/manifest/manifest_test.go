package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exampleRepo is the example setup repo shipped with rigger.
func exampleRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../example")
	require.NoError(t, err)
	return dir
}

// fakeRepo writes files (relative path -> content) into a temp repo dir.
func fakeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	return dir
}

func parse(t *testing.T, src string) *Manifest {
	t.Helper()
	m, err := Parse([]byte(src))
	require.NoError(t, err)
	return m
}

const sample = `
version: 1
profiles:
  base:
    components: [a, b]
    exclude_packages: [p1]
  more:
    components: ["@base", c]
    exclude: [b]
    exclude_packages: [p2]
components:
  - {id: a, group: base, cmds: [{run: echo a}]}
  - {id: b, group: shell, requires: [a], links: [{src: dots/rc, dst: ~/.rc}]}
  - {id: c, group: shell, platforms: [linux], requires: [a], seeds: [{src: dots/seed, dst: ~/.seed}]}
unmanaged: [docs]
`

func sampleRepo(t *testing.T) string {
	t.Helper()
	return fakeRepo(t, map[string]string{"dots/rc": "x", "dots/seed": "y", "docs/readme": "z"})
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte("components:\n  - {id: a, group: base, colour: red}\n"))
	require.ErrorContains(t, err, "colour")
}

func TestValidateSample(t *testing.T) {
	m := parse(t, sample)
	require.NoError(t, m.Validate(sampleRepo(t)))
}

func TestExpandProfile(t *testing.T) {
	m := parse(t, sample)

	got, err := m.ExpandProfile("more")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, got)

	assert.Equal(t, []string{"p1", "p2"}, m.ProfileExcludePackages("more"))

	_, err = m.ExpandProfile("nope")
	require.ErrorContains(t, err, "unknown profile")
}

func TestExpandProfileSelfReference(t *testing.T) {
	m := parse(t, `
profiles:
  loop: {components: ["@loop"]}
components:
  - {id: a, group: base, cmds: [{run: echo}]}
`)
	_, err := m.ExpandProfile("loop")
	require.ErrorContains(t, err, "includes itself")
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"bad id", `components: [{id: Bad_Id, group: base, cmds: [{run: x}]}]`, "id must match"},
		{"duplicate id", `components: [{id: a, group: base, cmds: [{run: x}]}, {id: a, group: base, cmds: [{run: x}]}]`, "duplicate id"},
		{"unknown group", "groups: [base]\ncomponents: [{id: a, group: nope, cmds: [{run: x}]}]", `unknown group "nope"`},
		{"missing group", `components: [{id: a, cmds: [{run: x}]}]`, "needs a group"},
		{"schema version", `components: [{id: a, group: base, cmds: [{run: x}]}]`, "manifest version 0 is not supported"},
		{"unknown platform", `components: [{id: a, group: base, platforms: [windows], cmds: [{run: x}]}]`, `unknown platform "windows"`},
		{"unknown requires", `components: [{id: a, group: base, requires: [ghost], cmds: [{run: x}]}]`, `unknown component "ghost"`},
		{"missing path", `components: [{id: a, group: base, links: [{src: nope/file, dst: ~/x}]}]`, "missing repo path nope/file"},
		{"missing script", `components: [{id: a, group: base, cmds: [{run: scripts/nope.sh --flag}]}]`, "missing repo path scripts/nope.sh"},
		{"empty cmd", `components: [{id: a, group: base, cmds: [{check: x}]}]`, "cmd needs run"},
		{"does nothing", `components: [{id: a, group: base}]`, "does nothing"},
		{"cycle", `components: [{id: a, group: base, requires: [b], cmds: [{run: x}]}, {id: b, group: base, requires: [a], cmds: [{run: x}]}]`, "requires cycle"},
		{"profile unknown component", "profiles: {p: {components: [ghost]}}\ncomponents: [{id: a, group: base, cmds: [{run: x}]}]", `profile "p": unknown component "ghost"`},
		{"profile unknown parent", "profiles: {p: {components: [\"@ghost\"]}}\ncomponents: [{id: a, group: base, cmds: [{run: x}]}]", `unknown profile "ghost"`},
		{
			"seed under link",
			`components: [{id: a, group: base, links: [{src: dots, dst: ~/.config/x}], seeds: [{src: dots/rc, dst: ~/.config/x/config}]}]`,
			"is inside the link ~/.config/x",
		},
		{"duplicate dst", `components: [{id: a, group: base, links: [{src: dots, dst: ~/.x}, {src: dots/rc, dst: ~/.x/}]}]`, `component "a": dst ~/.x is already used by "a"`},
		{
			"dst shared across components",
			`components: [{id: a, group: base, links: [{src: dots, dst: ~/.x}]}, {id: b, group: base, seeds: [{src: dots/rc, dst: ~/.x}]}]`,
			`component "b": dst ~/.x is already used by "a"`,
		},
		{"unreferenced dir", `components: [{id: a, group: base, cmds: [{run: x}]}]`, "repo dir dots/ is not used"},
		{
			"required package in no Brewfile",
			`components: [{id: a, group: base, links: [{src: dots/rc, dst: ~/.rc}]}, {id: b, group: base, requires: [a], requires_packages: [pipx], cmds: [{run: x}]}]`,
			`component "b": requires package "pipx", which is in no Brewfile`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := parse(t, tc.src)
			err := m.Validate(sampleRepo(t))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestRepoScript(t *testing.T) {
	tests := map[string]string{
		"zsh/install-plugins.sh":           "zsh/install-plugins.sh",
		"scripts/x.sh --flag":              "scripts/x.sh",
		"go install golang.org/x/tools@v1": "",
		"/bin/bash -c foo":                 "",
		"~/bin/thing":                      "",
		"$HOME/bin/thing":                  "",
		"https://example.com/x":            "",
		"cd ~/.x && npm install":           "",
		"":                                 "",
	}
	for run, want := range tests {
		assert.Equal(t, want, repoScript(run), run)
	}
}

func TestExpandHome(t *testing.T) {
	assert.Equal(t, "/h", ExpandHome("~", "/h"))
	assert.Equal(t, "/h/.config/x", ExpandHome("~/.config/x", "/h"))
	assert.Equal(t, "/abs", ExpandHome("/abs", "/h"))
	assert.Equal(t, "~user/x", ExpandHome("~user/x", "/h"))
}

func TestGroupOrder(t *testing.T) {
	m := parse(t, sample)
	assert.Equal(t, []string{"base", "shell"}, m.GroupOrder())
	m.Groups = []string{"shell", "base"}
	assert.Equal(t, []string{"shell", "base"}, m.GroupOrder())
}

func TestExampleManifest(t *testing.T) {
	m, err := Load(exampleRepo(t))
	require.NoError(t, err)
	for _, name := range []string{"laptop", "server"} {
		ids, err := m.ExpandProfile(name)
		require.NoError(t, err, name)
		assert.NotEmpty(t, ids, name)
	}
}

func TestRequiredPackages(t *testing.T) {
	repo := fakeRepo(t, map[string]string{"brew/dev.Brewfile": "# python apps\nbrew \"pipx\"\ncask \"k0nsta/apps/agr\"\n"})
	m := parse(t, `
version: 1
components:
  - {id: dev, group: base, brewfile: brew/dev.Brewfile}
  - {id: tools, group: base, requires: [dev], requires_packages: [pipx, k0nsta/apps/agr], cmds: [{run: x}]}
  - {id: more, group: base, requires: [tools], requires_packages: [pipx], cmds: [{run: y}]}
`)
	require.NoError(t, m.Validate(repo), "packages in a transitively required Brewfile are fine")
	assert.Equal(t, map[string]string{"pipx": "tools", "k0nsta/apps/agr": "tools"}, m.RequiredPackages([]string{"dev", "tools", "more"}))
	assert.Empty(t, m.RequiredPackages([]string{"dev"}))
}
