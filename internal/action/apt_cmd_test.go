package action

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/k0nsta/rigger/internal/action/mocks"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/testutil"
)

func TestAptProbe(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		assert.Equal(t, "dpkg-query", c.Path)
		assert.Equal(t, []string{"git", "zsh", "bat"}, c.Args[2:])
		// dpkg-query exits 1 when a package is unknown; that is not an error
		return sh.Result{Output: "git ii \nzsh:amd64 ii \nbat un \n", Code: 1}, nil
	})
	p, err := Apt{Packages: []string{"git", "zsh", "bat"}}.Probe(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, AptProbe{Status: Todo, Detail: "1 to install", Missing: []string{"bat"}}, p)

	empty, err := Apt{}.Probe(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, OK, empty.Status)
}

func TestAptInstall(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	var calls []string
	r.EXPECT().Run(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		calls = append(calls, c.String())
		assert.Contains(t, c.Env, "DEBIAN_FRONTEND=noninteractive")
		return sh.Result{}, nil
	})
	require.NoError(t, AptInstall(context.Background(), r, []string{"git", "zsh"}, nil))
	assert.Equal(t, []string{"sudo apt-get update -qq", "sudo apt-get install -y -qq -o Dpkg::Use-Pty=0 git zsh"}, calls)
	require.NoError(t, AptInstall(context.Background(), r, nil, nil))
}

func TestAptInstallAsRoot(t *testing.T) {
	orig := AsRoot
	AsRoot = func() bool { return true }
	t.Cleanup(func() { AsRoot = orig })
	r := mocks.NewMockRunner(gomock.NewController(t))
	var calls []string
	r.EXPECT().Run(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		calls = append(calls, c.String())
		return sh.Result{}, nil
	})
	require.NoError(t, AptInstall(context.Background(), r, []string{"git"}, nil))
	assert.Equal(t, []string{"apt-get update -qq", "apt-get install -y -qq -o Dpkg::Use-Pty=0 git"}, calls)
}

func TestAptInstallFailure(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), gomock.Any()).Return(sh.Result{Code: 1}, nil)
	require.ErrorContains(t, AptInstall(context.Background(), r, []string{"git"}, nil), "apt-get update failed")
}

func TestCommandProbeAndApply(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	marker := filepath.Join(repo, "done")
	r := sh.NewExec(t.TempDir())
	secrets := secretsWith(t, map[string]string{"TOKEN": "s3cret"})
	c := Command{Repo: repo, Cmd: manifest.Cmd{
		Name:    "make marker",
		Check:   "test -f done",
		Run:     `printf '%s' "$TOKEN" > done; echo wrote`,
		Secrets: []string{"TOKEN"},
	}}

	p, err := c.Probe(ctx, r, secrets)
	require.NoError(t, err)
	assert.Equal(t, Todo, p.Status)

	var lines []string
	require.NoError(t, c.Apply(ctx, r, secrets, func(l string) { lines = append(lines, l) }))
	assert.Equal(t, []string{"wrote"}, lines)
	assert.Equal(t, "s3cret", testutil.ReadFile(t, marker))

	p, err = c.Probe(ctx, r, secrets)
	require.NoError(t, err)
	assert.Equal(t, OK, p.Status)
}

func TestCommandWithoutCheckAlwaysRuns(t *testing.T) {
	c := Command{Repo: t.TempDir(), Cmd: manifest.Cmd{Run: "true"}}
	p, err := c.Probe(context.Background(), sh.NewExec(t.TempDir()), secretsWith(t, nil))
	require.NoError(t, err)
	assert.Equal(t, CommandProbe{Status: Todo, Detail: "runs every time"}, p)
	assert.Equal(t, "true", c.Label())
}

func TestCommandNeedsSecretAndFailure(t *testing.T) {
	ctx := context.Background()
	r := sh.NewExec(t.TempDir())
	c := Command{Repo: t.TempDir(), Cmd: manifest.Cmd{Name: "x", Run: "exit 4", Secrets: []string{"MISSING"}}}
	p, err := c.Probe(ctx, r, secretsWith(t, nil))
	require.NoError(t, err)
	assert.Equal(t, CommandProbe{Status: NeedsSecret, Detail: "needs MISSING"}, p)

	c.Secrets = nil
	require.ErrorContains(t, c.Apply(ctx, r, secretsWith(t, nil), nil), "x failed (exit 4)")
}
