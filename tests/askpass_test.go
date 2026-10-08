package tests

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/askpass"
)

// TestAskpassHelper runs the helper script exactly as sudo would ($SUDO_ASKPASS "<prompt>")
// against a server, through the real `rigger askpass` subcommand.
func TestAskpassHelper(t *testing.T) {
	var asked []string
	srv, err := askpass.Start(binary, func(msg string) (string, bool) {
		asked = append(asked, msg)
		return "pw-42", true
	}, func(pw string) bool { return pw == "pw-42" })
	require.NoError(t, err)
	defer srv.Close()

	env := append(os.Environ(), srv.Env("/usr/bin:/bin")...)
	var helper string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "SUDO_ASKPASS="); ok {
			helper = v
		}
	}
	for range 2 {
		cmd := exec.Command(helper, "[sudo] password for me:")
		cmd.Env = env
		out, err := cmd.Output()
		require.NoError(t, err)
		assert.Equal(t, "pw-42\n", string(out))
	}
	assert.Len(t, asked, 1, "asked once, then answered from memory")

	// without a server the helper fails instead of hanging
	cmd := exec.Command(binary, "askpass", "Password:")
	cmd.Env = append(os.Environ(), "RIGGER_ASKPASS_SOCK=")
	require.Error(t, cmd.Run())
}
