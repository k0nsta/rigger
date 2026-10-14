package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/testutil"
)

func TestAskStray(t *testing.T) {
	tests := map[string]struct {
		input            string
		answer           string
		all, ok, reasked bool
	}{
		"brewfile":       {input: "2\n", answer: "2", ok: true},
		"ignore":         {input: " i \n", answer: "i", ok: true},
		"later":          {input: "\n", answer: "", ok: true},
		"rest too":       {input: "3*\n", answer: "3", all: true, ok: true},
		"later for rest": {input: "*\n", answer: "", all: true, ok: true},
		"out of range":   {input: "9\n2\n", answer: "2", ok: true, reasked: true},
		"garbage":        {input: "dev\ni\n", answer: "i", ok: true, reasked: true},
		"input closed":   {input: "", ok: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c, out := newCLI(t, true)
			answer, all, ok := c.askStray(bufio.NewReader(strings.NewReader(tt.input)), "agg [1-7/i/enter]: ", 7)
			assert.Equal(t, tt.answer, answer)
			assert.Equal(t, tt.all, all)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.reasked, strings.Contains(out.String(), "type a number from 1 to 7"))
		})
	}
}

func TestHandleStraysShowsProfilesAndWritesAnswers(t *testing.T) {
	repo := copyExample(t)
	mf := filepath.Join(repo, "manifest.yaml")
	data := testutil.ReadFile(t, mf)
	data = strings.Replace(data, "profiles:\n", "profiles:\n  work:\n    components: [\"@laptop\", apps]\n", 1)
	data = strings.Replace(data, "manual:", "  - {id: apps, title: Apps, group: packages, platforms: [darwin], requires: [homebrew], brewfile: brew/apps.Brewfile}\n\nmanual:", 1)
	testutil.WriteFile(t, mf, data)
	testutil.WriteFile(t, filepath.Join(repo, "brew", "apps.Brewfile"), "cask \"slack\"\n")
	testutil.WriteFile(t, filepath.Join(repo, "hosts", "test-box.yaml"), "profile: laptop\n")

	c, out := newCLI(t, true)
	testutil.WriteFile(t, filepath.Join(c.home, ".local", "bin", "brew"), `#!/bin/sh
case "$*" in
"leaves --installed-on-request") printf 'git\nagg\nvhs\nlocal-tool\n' ;;
"list --cask -1") printf 'slack\nzoom\n' ;;
"info --json=v2 --formula agg local-tool vhs") echo '{"formulae":[{"name":"agg","full_name":"agg","desc":"Asciinema gif generator"},{"name":"vhs","full_name":"vhs","desc":"Your CLI home video recorder"}]}' ;;
"info --json=v2 --cask zoom") echo '{"casks":[{"token":"zoom","full_token":"zoom","desc":"Video communication"}]}' ;;
*) echo "unexpected brew $*" >&2; exit 1 ;;
esac
`)
	require.NoError(t, os.Chmod(filepath.Join(c.home, ".local", "bin", "brew"), 0o755))
	t.Setenv("PATH", filepath.Join(c.home, ".local", "bin")+":"+os.Getenv("PATH"))

	f := sessionFlags{repo: repo, host: "test-box"}
	s, sel, err := f.open(c.home)
	require.NoError(t, err)
	s.Platform = manifest.Darwin
	// sorted: agg → core, local-tool → ignore, vhs → a typo, then apps, zoom → later
	in := bufio.NewReader(strings.NewReader("1\ni\nx\n2\n\n"))
	require.NoError(t, c.handleStrays(context.Background(), s, sel, in, false))

	got := out.String()
	assert.Contains(t, got, "  brew agg         Asciinema gif generator\n")
	assert.Contains(t, got, "  brew local-tool\n", "no description, no trailing spaces")
	assert.Regexp(t, `(?m)^  1\s+core\s+Core CLI\s+→ laptop, server, work$`, got)
	assert.Regexp(t, `(?m)^  2\s+apps\s+Apps\s+→ work \(not on test-box\)$`, got)
	assert.Contains(t, got, "keep it on test-box only (ignore_packages in hosts/test-box.yaml)")
	assert.Contains(t, got, "type a number from 1 to 2")

	assert.Contains(t, testutil.ReadFile(t, filepath.Join(repo, "brew", "core.Brewfile")), "# Asciinema gif generator\nbrew \"agg\"\n")
	assert.Contains(t, testutil.ReadFile(t, filepath.Join(repo, "brew", "apps.Brewfile")), "# Your CLI home video recorder\nbrew \"vhs\"\n")
	assert.Contains(t, testutil.ReadFile(t, filepath.Join(repo, "hosts", "test-box.yaml")), "- local-tool")
	assert.NotContains(t, testutil.ReadFile(t, filepath.Join(repo, "brew", "apps.Brewfile")), "zoom", "enter leaves it for later")
}

func TestParseApplyAnswer(t *testing.T) {
	tests := map[string]struct {
		apply, drop []int
		ok          bool
	}{
		"":        {apply: []int{0, 1, 2}, ok: true},
		"all":     {apply: []int{0, 1, 2}, ok: true},
		"none":    {ok: true},
		"1 3":     {apply: []int{0, 2}, ok: true},
		"-2":      {apply: []int{0, 2}, drop: []int{1}, ok: true},
		"3 -2":    {apply: []int{2}, drop: []int{1}, ok: true},
		"none -2": {drop: []int{1}, ok: true},
		"4":       {},
		"-0":      {},
		"2 -2":    {},
		"vim":     {},
	}
	for answer, tt := range tests {
		t.Run(answer, func(t *testing.T) {
			apply, drop, ok := parseApplyAnswer(answer+"\n", 3)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.apply, apply)
			assert.Equal(t, tt.drop, drop)
		})
	}
}

// gitT runs git in dir and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func TestSyncAppliesPickedAndDropsFromHost(t *testing.T) {
	seed := copyExample(t)
	testutil.WriteFile(t, filepath.Join(seed, "hosts", "box.yaml"), "profile: laptop\n")
	gitT(t, seed, "init", "-q", "-b", "main")
	gitT(t, seed, "add", ".")
	gitT(t, seed, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	remote := filepath.Join(t.TempDir(), "setup.git")
	gitT(t, "", "clone", "-q", "--bare", seed, remote)
	repo := filepath.Join(t.TempDir(), "setup")
	gitT(t, "", "clone", "-q", remote, repo)
	gitT(t, repo, "config", "user.name", "box")
	gitT(t, repo, "config", "user.email", "box@example.com")

	c, out := newCLI(t, true)
	// the laptop profile brings [1] zsh and [2] git here
	c.in = strings.NewReader("2 -1\n")
	require.NoError(t, c.run(context.Background(), []string{"sync", "--repo", repo, "--host", "box"}))

	got := out.String()
	assert.Regexp(t, `\[1\] zsh +missing +~/\.zshrc missing +\(in profile laptop\)`, got)
	assert.Contains(t, got, "dropped zsh from hosts/box.yaml")
	assert.Equal(t, filepath.Join(repo, "git", ".gitconfig"), testutil.Readlink(t, filepath.Join(c.home, ".gitconfig")))
	assert.NoFileExists(t, filepath.Join(c.home, ".zshrc"))
	assert.Equal(t, "profile: laptop\nremove:\n    - zsh\n", gitT(t, remote, "show", "main:hosts/box.yaml"), "the drop is committed and pushed")
}
