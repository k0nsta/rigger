package remote

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func titles(steps []Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.Title)
	}
	return out
}

func TestPlanDefault(t *testing.T) {
	steps, err := Plan(Options{Host: "box", Origin: "https://github.com/you/setup.git"})
	require.NoError(t, err)
	assert.Equal(t, []string{"git and curl", "install rigger", "clone setup repo", "installer"}, titles(steps))

	prereq := steps[0]
	assert.True(t, prereq.Interactive, "sudo may prompt")
	assert.Equal(t, []string{"-t", "box"}, prereq.Cmd.Args[:2])

	install := steps[1].Cmd.Args
	assert.Contains(t, install[len(install)-1], "curl -fsSL "+InstallURL+" | sh")

	clone := steps[2].Cmd
	assert.Equal(t, "ssh", clone.Path)
	assert.Equal(t, []string{"-A", "box"}, clone.Args[:2], "agent forwarded for the clone only")
	assert.Contains(t, clone.Args[2], "init 'git@github.com:you/setup.git' --no-run", "https origin becomes ssh on the host")

	tui := steps[3]
	assert.True(t, tui.Interactive)
	assert.Equal(t, []string{"-t", "box", "$HOME/.local/bin/rigger --profile 'server'"}, tui.Cmd.Args)
	for _, s := range steps {
		if s.Title != "clone setup repo" {
			assert.NotContains(t, s.Cmd.Args, "-A", s.Title)
		}
	}
}

func TestPlanBinaryProfileTerminfo(t *testing.T) {
	steps, err := Plan(Options{Host: "me@vps", Origin: "git@github.com:you/setup.git", Profile: "edge", Binary: "/tmp/rigger-linux", Terminfo: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"terminfo", "git and curl", "upload rigger", "install rigger", "clone setup repo", "installer"}, titles(steps))
	assert.Equal(t, "agtermctl terminfo install me@vps", steps[0].Cmd.String())
	assert.Equal(t, "scp -q /tmp/rigger-linux me@vps:.rigger.upload", steps[2].Cmd.String())
	assert.True(t, strings.HasSuffix(steps[5].Cmd.Args[2], "--profile 'edge'"))
}

func TestPlanErrors(t *testing.T) {
	_, err := Plan(Options{Origin: "git@github.com:you/setup.git"})
	require.ErrorContains(t, err, "no host")
	_, err = Plan(Options{Host: "box", Origin: "/srv/setup"})
	require.ErrorContains(t, err, "is a local path")
	_, err = Plan(Options{Host: "box", Origin: "nonsense"})
	require.Error(t, err)
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
}
