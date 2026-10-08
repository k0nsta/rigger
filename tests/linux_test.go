package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLinuxServerProfile runs the example server profile in a Debian 13 container: apt
// packages, links, host file. Skipped without docker or with -short.
func TestLinuxServerProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker is not available")
	}

	dir := t.TempDir()
	linuxBin := filepath.Join(dir, "rigger")
	build := exec.Command("go", "build", "-o", linuxBin, "../cmd/rigger")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))

	repo := filepath.Join(dir, "setup")
	require.NoError(t, os.CopyFS(repo, os.DirFS("../example")))

	// stream binary + repo in as a tar: works whatever the docker host shares
	script := `set -e
mkdir /w && tar -C /w -xf -
install -m 0755 /w/rigger /usr/local/bin/rigger && mv /w/setup /setup
rigger apply --repo /setup --profile server --host srv --yes
test "$(readlink /root/.zshrc)" = /setup/zsh/.zshrc
test "$(readlink /root/.gitconfig)" = /setup/git/.gitconfig
command -v zsh rg jq tmux >/dev/null
grep -q 'profile: server' /setup/hosts/srv.yaml
rigger apply --repo /setup --host srv --yes | grep -q 'nothing to do'
echo LINUX-OK`
	tarball := exec.Command("tar", "-C", dir, "-cf", "-", "rigger", "setup")
	tarball.Env = append(os.Environ(), "COPYFILE_DISABLE=1") // no macOS ._ files
	stream, err := tarball.Output()
	require.NoError(t, err)
	cmd := exec.Command("docker", "run", "-i", "--rm", "debian:13", "sh", "-c", script)
	cmd.Stdin = bytes.NewReader(stream)
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "LINUX-OK")
}
