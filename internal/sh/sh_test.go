package sh

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunScriptStreamsLines(t *testing.T) {
	e := NewExec(t.TempDir(), "EXTRA=1")
	var lines []string
	res, err := e.Run(context.Background(), Cmd{
		Script: `echo one; echo two >&2; printf three; echo "$EXTRA $NONINTERACTIVE $GIT_TERMINAL_PROMPT"`,
		OnLine: func(l string) { lines = append(lines, l) },
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Code)
	assert.Equal(t, []string{"one", "two", "three1 1 0"}, lines)
}

func TestRunExitCodeIsNotAnError(t *testing.T) {
	res, err := NewExec(t.TempDir()).Run(context.Background(), Cmd{Script: "echo nope; exit 3"})
	require.NoError(t, err)
	assert.Equal(t, 3, res.Code)
	assert.Equal(t, "nope\n", res.Output)
}

func TestRunKeepsStdoutApart(t *testing.T) {
	res, err := NewExec(t.TempDir()).Run(context.Background(), Cmd{Script: "echo notice >&2; echo '{}'", SplitStdout: true})
	require.NoError(t, err)
	assert.Equal(t, "{}\n", res.Stdout)
	assert.Contains(t, res.Output, "notice")
}

func TestRunMissingBinary(t *testing.T) {
	_, err := NewExec(t.TempDir()).Run(context.Background(), Cmd{Path: "/nonexistent/tool"})
	require.Error(t, err)
}

func TestRunStdinIsEmpty(t *testing.T) {
	res, err := NewExec(t.TempDir()).Run(context.Background(), Cmd{Script: "cat; echo done"})
	require.NoError(t, err)
	assert.Equal(t, "done\n", res.Output)
}

func TestRunPathPrefix(t *testing.T) {
	home := t.TempDir()
	res, err := NewExec(home).Run(context.Background(), Cmd{Script: `echo "$PATH"`})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(res.Output, "/opt/homebrew/bin:"))
	assert.Contains(t, res.Output, home+"/.local/bin")
}

func TestRunCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := NewExec(t.TempDir()).Run(ctx, Cmd{Script: "sleep 5"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLines(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, Lines("a\n\n  b  \n"))
	assert.Nil(t, Lines(""))
}

func TestCmdString(t *testing.T) {
	assert.Equal(t, "echo hi", Cmd{Script: "echo hi"}.String())
	assert.Equal(t, "brew list --cask", Cmd{Path: "brew", Args: []string{"list", "--cask"}}.String())
}

func TestRunFindsToolsOnChildPath(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "fake-tool"), []byte("#!/bin/sh\necho fake \"$HOME\"\n"), 0o755))
	home := t.TempDir()
	e := NewExec(home, "PATH="+bin+":/usr/bin:/bin")
	res, err := e.Run(context.Background(), Cmd{Path: "fake-tool"})
	require.NoError(t, err)
	assert.Equal(t, "fake "+home+"\n", res.Output, "found on the child PATH and HOME is the session home")

	_, err = e.Run(context.Background(), Cmd{Path: "no-such-tool-anywhere"})
	require.ErrorIs(t, err, exec.ErrNotFound)
}

func TestRunHasNoControllingTerminal(t *testing.T) {
	res, err := NewExec(t.TempDir()).Run(context.Background(), Cmd{Script: "tty; exit 0"})
	require.NoError(t, err)
	assert.Equal(t, "not a tty\n", res.Output, "children cannot grab the TUI's terminal")
}

func TestAddEnvAndPath(t *testing.T) {
	e := NewExec(t.TempDir())
	e.AddEnv("PATH=/shim:" + e.Path())
	assert.True(t, strings.HasPrefix(e.Path(), "/shim:/opt/homebrew/bin"))
}

func TestTilde(t *testing.T) {
	assert.Equal(t, "~/github.com/setup", Tilde("/Users/a", "/Users/a/github.com/setup"))
	assert.Equal(t, "~", Tilde("/Users/a/", "/Users/a"))
	assert.Equal(t, "Cloning into '~/x'...", Tilde("/Users/a", "Cloning into '/Users/a/x'..."))
	assert.Equal(t, "/Users/ab/x", Tilde("/Users/a", "/Users/ab/x"), "only whole path elements")
	assert.Equal(t, "/x", Tilde("/", "/x"))
}
