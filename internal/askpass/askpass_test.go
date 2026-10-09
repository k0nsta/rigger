package askpass

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// start runs a server whose user answers from answers in order and whose valid password is good.
func start(t *testing.T, good string, answers ...string) (*Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	s, err := Start("/usr/local/bin/rigger", func(msg string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, msg)
		if len(answers) == 0 {
			return "", false
		}
		a := answers[0]
		answers = answers[1:]
		return a, true
	}, func(pw string) bool { return pw == good })
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s, &asked
}

func socket(s *Server) string { return filepath.Join(s.dir, "sock") }

func TestAskOnceThenCached(t *testing.T) {
	s, asked := start(t, "s3cret", "s3cret")
	for range 3 {
		pw, err := Request(socket(s), "Password:")
		require.NoError(t, err)
		assert.Equal(t, "s3cret", pw)
	}
	assert.Equal(t, []string{"sudo password (asked once for this run)"}, *asked)
}

func TestWrongPasswordRetries(t *testing.T) {
	s, asked := start(t, "right", "wrong", "right")
	pw, err := Request(socket(s), "[sudo] password for me:")
	require.NoError(t, err)
	assert.Equal(t, "right", pw)
	require.Len(t, *asked, 2)
	assert.Contains(t, (*asked)[0], "[sudo] password for me:")
	assert.Equal(t, "wrong password, try again", (*asked)[1])
}

func TestCancelAndThreeStrikes(t *testing.T) {
	s, _ := start(t, "right")
	_, err := Request(socket(s), "Password:")
	require.ErrorContains(t, err, "cancelled")

	s2, asked := start(t, "right", "a", "b", "c", "right")
	_, err = Request(socket(s2), "Password:")
	require.ErrorContains(t, err, "cancelled")
	assert.Len(t, *asked, 3)
}

func TestFilesAndEnv(t *testing.T) {
	s, _ := start(t, "x")
	env := s.Env("/usr/bin:/bin")
	require.Len(t, env, 3)
	helper := strings.TrimPrefix(env[0], "SUDO_ASKPASS=")
	assert.Contains(t, readFile(t, helper), "exec '/usr/local/bin/rigger' askpass")
	assert.Contains(t, readFile(t, helper), EnvSocket+"='"+socket(s)+"'")
	assert.True(t, strings.HasPrefix(env[2], "PATH="+filepath.Join(s.dir, "bin")+":"))
	assert.Contains(t, readFile(t, filepath.Join(s.dir, "bin", "sudo")), "/usr/bin/sudo -A")

	info, err := os.Stat(s.dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	dir := s.dir
	s.Close()
	assert.NoDirExists(t, dir)
}

func TestHelperScriptEndToEnd(t *testing.T) {
	// a fake "rigger" that implements the helper side with the real client in a test binary
	// is overkill; check the wire protocol through a shell instead
	s, _ := start(t, "pw-123", "pw-123")
	out, err := exec.Command("/bin/sh", "-c", `printf 'Password:\n' | nc -U "$0"`, socket(s)).Output()
	if err != nil {
		t.Skip("nc -U not available")
	}
	assert.Equal(t, "OK pw-123\n", string(out))
}

func TestHelperFindsSocketWithoutEnv(t *testing.T) {
	// brew runs sudo, and so the helper, under env -i with an allowlist that keeps only
	// SUDO_ASKPASS; the helper must still reach the server
	fake := filepath.Join(t.TempDir(), "rigger")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s %s' \"$"+EnvSocket+"\" \"$*\"\n"), 0o700))
	s, err := Start(fake, func(string) (string, bool) { return "", false }, func(string) bool { return false })
	require.NoError(t, err)
	t.Cleanup(s.Close)
	out, err := exec.Command("/usr/bin/env", "-i", filepath.Join(s.dir, "askpass"), "Password:").Output()
	require.NoError(t, err)
	assert.Equal(t, socket(s)+" askpass Password:", string(out))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}
