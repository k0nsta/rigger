package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/app"
	"github.com/k0nsta/rigger/internal/skill"
	"github.com/k0nsta/rigger/internal/state"
	"github.com/k0nsta/rigger/internal/testutil"
)

// copyExample copies the example setup repo into a temp dir so tests can write hosts/.
func copyExample(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	require.NoError(t, os.CopyFS(dst, os.DirFS("../../example")))
	return dst
}

// newCLI returns a cli writing into buffers, with a temp HOME and XDG config.
func newCLI(t *testing.T, tty bool) (*cli, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("RIGGER_REPO", "")
	var out bytes.Buffer
	return &cli{out: &out, err: &out, in: strings.NewReader(""), isTTY: tty, home: home}, &out
}

func TestVersionAndHelp(t *testing.T) {
	c, out := newCLI(t, false)
	require.NoError(t, c.run(context.Background(), []string{"version"}))
	assert.Equal(t, "rigger dev\n", out.String())
	require.ErrorContains(t, c.run(context.Background(), nil), "usage: rigger")
	require.ErrorContains(t, c.run(context.Background(), []string{"nope"}), `unknown command "nope"`)
}

func TestNoteElsewhereWritesToErrorStream(t *testing.T) {
	c, _ := newCLI(t, false)
	var errOut bytes.Buffer
	c.err = &errOut
	c.noteElsewhere(&app.Session{Home: c.home, Repo: filepath.Join(c.home, "setup"), Elsewhere: filepath.Join(c.home, "worktree")})
	assert.Equal(t, "using the setup repo at ~/setup (this is another checkout; pass --repo to use it)\n", errOut.String())
}

func TestValidate(t *testing.T) {
	c, out := newCLI(t, false)
	require.NoError(t, c.run(context.Background(), []string{"validate", "../../example"}))
	assert.Contains(t, out.String(), "ok")
	require.ErrorContains(t, c.run(context.Background(), []string{"validate", t.TempDir()}), "read manifest")
}

func TestSkillInstallCLI(t *testing.T) {
	t.Run("prints tilde paths", func(t *testing.T) {
		c, out := newCLI(t, false)
		require.NoError(t, c.run(context.Background(), []string{"skill", "install"}))
		assert.Equal(t, "~/.claude/skills/rigger/SKILL.md\n~/.agents/skills/rigger/SKILL.md\n", out.String())
		for _, path := range skill.Paths(c.home) {
			assert.FileExists(t, path)
		}
	})

	t.Run("notes when Claude skill resolves into remembered repo", func(t *testing.T) {
		c, out := newCLI(t, false)
		repo := filepath.Join(c.home, "setup")
		claude := filepath.Join(repo, ".claude")
		require.NoError(t, os.MkdirAll(repo, 0o755))
		require.NoError(t, os.MkdirAll(claude, 0o755))
		require.NoError(t, os.Symlink(claude, filepath.Join(c.home, ".claude")))
		require.NoError(t, (&state.Local{Repo: repo}).Save(state.LocalPath(c.home)))
		require.NoError(t, c.run(context.Background(), []string{"skill", "install"}))
		assert.Equal(t, "~/.claude/skills/rigger/SKILL.md (in your setup repo: commit it with rigger sync)\n~/.agents/skills/rigger/SKILL.md\n", out.String())
		assert.FileExists(t, filepath.Join(claude, "skills", "rigger", "SKILL.md"))
	})

	t.Run("does not note sibling repo or unreadable state", func(t *testing.T) {
		c, out := newCLI(t, false)
		repo := filepath.Join(c.home, "setup")
		sibling := filepath.Join(c.home, "setup-old")
		claude := filepath.Join(repo, ".claude")
		require.NoError(t, os.MkdirAll(repo, 0o755))
		require.NoError(t, os.MkdirAll(claude, 0o755))
		require.NoError(t, os.MkdirAll(sibling, 0o755))
		require.NoError(t, os.Symlink(claude, filepath.Join(c.home, ".claude")))
		require.NoError(t, (&state.Local{Repo: sibling}).Save(state.LocalPath(c.home)))
		require.NoError(t, c.run(context.Background(), []string{"skill", "install"}))
		assert.NotContains(t, out.String(), "in your setup repo")

		c, out = newCLI(t, false)
		require.NoError(t, os.MkdirAll(filepath.Dir(state.LocalPath(c.home)), 0o755))
		require.NoError(t, os.WriteFile(state.LocalPath(c.home), []byte(": [invalid\n"), 0o600))
		require.NoError(t, c.run(context.Background(), []string{"skill", "install"}))
		assert.NotContains(t, out.String(), "in your setup repo")
	})

	t.Run("usage errors", func(t *testing.T) {
		for _, args := range [][]string{{"skill"}, {"skill", "other"}, {"skill", "install", "extra"}} {
			c, _ := newCLI(t, false)
			require.EqualError(t, c.run(context.Background(), args), "usage: rigger skill install [--force]")
		}
	})

	t.Run("foreign path is printed with tilde", func(t *testing.T) {
		c, _ := newCLI(t, false)
		foreignPath := skill.Paths(c.home)[0]
		testutil.WriteFile(t, foreignPath, "user file\n")
		err := c.run(context.Background(), []string{"skill", "install"})
		require.ErrorContains(t, err, "~/.claude/skills/rigger/SKILL.md: differs from the skill")
		assert.NoFileExists(t, skill.Paths(c.home)[1])
	})
}

func TestStatusAndApply(t *testing.T) {
	ctx := context.Background()
	repo := copyExample(t)
	c, out := newCLI(t, false)
	args := []string{"--repo", repo, "--host", "test-box", "--only", "git"}

	err := c.run(ctx, append([]string{"status"}, args...))
	var code exitCode
	require.True(t, errors.As(err, &code), "status exits 1 while something is missing: %v", err)
	assert.Contains(t, out.String(), "~/.gitconfig missing")

	out.Reset()
	require.NoError(t, c.run(ctx, append([]string{"apply", "--dry-run"}, args...)))
	assert.Contains(t, out.String(), "dry run: 1 component(s) would change")
	assert.NoFileExists(t, filepath.Join(c.home, ".gitconfig"))

	require.ErrorContains(t, c.run(ctx, append([]string{"apply"}, args...)), "pass --yes")

	out.Reset()
	require.NoError(t, c.run(ctx, append([]string{"apply", "--yes"}, args...)))
	assert.Equal(t, filepath.Join(repo, "git", ".gitconfig"), testutil.Readlink(t, filepath.Join(c.home, ".gitconfig")))
	assert.Contains(t, testutil.ReadFile(t, filepath.Join(repo, "hosts", "test-box.yaml")), "- git")

	// the host file now carries the selection: plain status works and is all ok
	out.Reset()
	require.NoError(t, c.run(ctx, []string{"status", "--repo", repo, "--host", "test-box"}))
	assert.Regexp(t, `(?m)^git\s+ok`, out.String())
}

func TestApplyNeedsProfileOnFirstRun(t *testing.T) {
	c, _ := newCLI(t, false)
	err := c.run(context.Background(), []string{"status", "--repo", copyExample(t), "--host", "new"})
	require.ErrorContains(t, err, "pass --profile (one of: laptop, server)")
}

func TestApplyConfirm(t *testing.T) {
	repo := copyExample(t)
	c, out := newCLI(t, true)
	c.in = strings.NewReader("n\n")
	err := c.run(context.Background(), []string{"apply", "--repo", repo, "--host", "h", "--only", "git"})
	require.ErrorContains(t, err, "cancelled")
	assert.Contains(t, out.String(), "apply 1 component(s)? [y/N]")
	assert.NoFileExists(t, filepath.Join(c.home, ".gitconfig"))
}

func TestInitClonesAndRemembers(t *testing.T) {
	ctx := context.Background()
	work := copyExample(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	c, out := newCLI(t, false)
	dir := filepath.Join(c.home, "setups", "mine")
	require.NoError(t, c.run(ctx, []string{"init", work, "--dir", dir}))
	assert.FileExists(t, filepath.Join(dir, "manifest.yaml"))
	assert.Contains(t, out.String(), "next: rigger apply --profile <laptop|server>")

	// the remembered repo is found from anywhere
	out.Reset()
	t.Chdir(t.TempDir())
	require.NoError(t, c.run(ctx, []string{"apply", "--dry-run", "--only", "git", "--host", "h"}))
	assert.Contains(t, out.String(), "dry run")
}

func TestInitRefusesACheckoutWithoutManifest(t *testing.T) {
	work := copyExample(t)
	bare := filepath.Join(t.TempDir(), "setup.git")
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{work, []string{"init", "-q", "-b", "main"}},
		{work, []string{"add", "."}},
		{work, []string{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"}},
		{"", []string{"init", "-q", "--bare", bare}},
		{work, []string{"push", "-q", bare, "main"}},
		// the remote's HEAD names a branch that does not exist: the clone has an empty checkout
		{bare, []string{"symbolic-ref", "HEAD", "refs/heads/gone"}},
	} {
		cmd := exec.Command("git", step.args...)
		cmd.Dir = step.dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	c, _ := newCLI(t, false)
	dir := filepath.Join(c.home, "setup")
	err := c.run(context.Background(), []string{"init", bare, "--dir", dir})
	require.ErrorContains(t, err, "~/setup has no manifest.yaml (the clone was removed)")
	assert.NoFileExists(t, filepath.Join(c.home, ".config", "rigger", "state.yaml"), "a repo that is not one is not remembered")
	assert.NoDirExists(t, dir, "a retry clones afresh")

	// once the remote's HEAD is fixed, init works
	cmd := exec.Command("git", "symbolic-ref", "HEAD", "refs/heads/main")
	cmd.Dir = bare
	require.NoError(t, cmd.Run())
	require.NoError(t, c.run(context.Background(), []string{"init", bare, "--dir", dir, "--no-run"}))
	assert.FileExists(t, filepath.Join(dir, "manifest.yaml"))
}

func TestSplitPositional(t *testing.T) {
	url, rest := splitPositional([]string{"--dir", "/x", "git@h:a/b", "--no-run"})
	assert.Equal(t, "git@h:a/b", url)
	assert.Equal(t, []string{"--dir", "/x", "--no-run"}, rest)
	url, _ = splitPositional([]string{"--no-run"})
	assert.Empty(t, url)
}

func TestAdoptSeedAndLink(t *testing.T) {
	ctx := context.Background()
	repo := copyExample(t)
	c, out := newCLI(t, false)
	secrets := filepath.Join(c.home, ".config", "rigger", "secrets.env")
	testutil.WriteFile(t, secrets, "TOOL_TOKEN=tok-123456\n")
	require.NoError(t, c.run(ctx, []string{"apply", "--yes", "--repo", repo, "--host", "h", "--only", "tool-config"}))

	live := filepath.Join(c.home, ".config", "tool", "config.toml")
	testutil.WriteFile(t, live, "token = \"tok-123456\"\ntheme = \"light\"\n")
	out.Reset()
	require.NoError(t, c.run(ctx, []string{"adopt", "~/.config/tool/config.toml", "--repo", repo, "--host", "h"}))
	assert.Contains(t, out.String(), "template tool/config.toml updated")
	assert.Equal(t, "token = \"${TOOL_TOKEN}\"\ntheme = \"light\"\n", testutil.ReadFile(t, filepath.Join(repo, "tool", "config.toml")))

	out.Reset()
	require.NoError(t, c.run(ctx, []string{"status", "--repo", repo, "--host", "h"}))
	assert.Regexp(t, `(?m)^tool-config\s+ok`, out.String())

	// a linked file that turned into a real copy is adopted and linked
	testutil.WriteFile(t, filepath.Join(c.home, ".gitconfig"), "[user]\n  name = mine\n")
	out.Reset()
	require.NoError(t, c.run(ctx, []string{"adopt", filepath.Join(c.home, ".gitconfig"), "--repo", repo, "--host", "h"}))
	assert.Contains(t, out.String(), "copied into git/.gitconfig and linked")
	assert.Contains(t, testutil.ReadFile(t, filepath.Join(repo, "git", ".gitconfig")), "name = mine")

	require.ErrorContains(t, c.run(ctx, []string{"adopt", "~/.unknown", "--repo", repo}), "not a seed or link of any component")
}

func TestMissingSecretsSaysWhere(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	got := missingSecrets("/home/u", []string{"A", "B"}, "components needing them are skipped")
	assert.Equal(t, "\nmissing secrets (components needing them are skipped): A, B\n"+
		"  add each to ~/.config/rigger/secrets.env as KEY=value (if you use 1Password, KEY=op://vault/item/field also works)\n", got)
	assert.Contains(t, missingSecrets("/home/u", []string{"A"}, ""), "\nmissing secrets: A\n")
}
