package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/k0nsta/rigger/internal/repo/mocks"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/testutil"
)

func TestParseRemote(t *testing.T) {
	tests := map[string]Remote{
		"git@github.com:you/machine-setup.git":     {Host: "github.com", Path: "you/machine-setup"},
		"git@github.com:you/machine-setup":         {Host: "github.com", Path: "you/machine-setup"},
		"https://github.com/you/machine-setup.git": {Host: "github.com", Path: "you/machine-setup", HTTPS: true},
		"https://user@gitlab.com/group/sub/dots":   {Host: "gitlab.com", Path: "group/sub/dots", HTTPS: true},
		"ssh://git@example.org:2222/you/dots.git":  {Host: "example.org", Path: "you/dots"},
		"/srv/git/dots.git":                        {Host: "local", Path: "/srv/git/dots"},
		"file:///srv/git/dots":                     {Host: "local", Path: "/srv/git/dots"},
	}
	for url, want := range tests {
		got, err := ParseRemote(url)
		require.NoError(t, err, url)
		assert.Equal(t, want, got, url)
	}
	_, err := ParseRemote("not a url")
	require.Error(t, err)
}

func TestRemoteHelpers(t *testing.T) {
	r, err := ParseRemote("https://github.com/you/machine-setup.git")
	require.NoError(t, err)
	assert.Equal(t, "machine-setup", r.Name())
	assert.Equal(t, "git@github.com:you/machine-setup.git", r.SSH())
	assert.True(t, Same("https://github.com/you/machine-setup", "git@github.com:you/machine-setup.git"))
	assert.False(t, Same("git@github.com:you/a", "git@github.com:you/b"))

	dir, err := DefaultDir("/home/me", "git@github.com:you/machine-setup.git")
	require.NoError(t, err)
	assert.Equal(t, "/home/me/github.com/machine-setup", dir)
}

// bareRepo makes a bare repo with one commit holding manifest.yaml and returns its path.
func bareRepo(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	testutil.WriteFile(t, filepath.Join(work, "manifest.yaml"), "version: 1\n")
	gitRun(t, work, "init", "-q", "-b", "main")
	gitRun(t, work, "add", ".")
	gitRun(t, work, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	bare := filepath.Join(t.TempDir(), "setup.git")
	gitRun(t, "", "clone", "-q", "--bare", work, bare)
	return bare
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestCloneThenPull(t *testing.T) {
	ctx := context.Background()
	bare := bareRepo(t)
	r := sh.NewExec(t.TempDir())
	dir := filepath.Join(t.TempDir(), "local", "setup")

	require.NoError(t, Clone(ctx, r, bare, dir, nil, nil))
	assert.FileExists(t, filepath.Join(dir, "manifest.yaml"))

	// second run pulls instead of failing
	require.NoError(t, Clone(ctx, r, bare, dir, nil, nil))

	err := Clone(ctx, r, bareRepo(t), dir, nil, nil)
	require.ErrorContains(t, err, "already holds another repo")
}

func TestCloneRefusesNonEmptyDir(t *testing.T) {
	dir := testutil.Tree(t, map[string]string{"junk": "x"})
	err := Clone(context.Background(), sh.NewExec(t.TempDir()), bareRepo(t), dir, nil, nil)
	require.ErrorContains(t, err, "is not empty")
}

func TestCloneHTTPSAsksForTokenOnce(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "setup")
	r := mocks.NewMockRunner(gomock.NewController(t))
	url := "https://github.com/you/private-setup.git"

	first := r.EXPECT().Run(gomock.Any(), gomock.Any()).Return(sh.Result{
		Code:   128,
		Output: "fatal: could not read Username for 'https://github.com': terminal prompts disabled\n",
	}, nil)
	r.EXPECT().Run(gomock.Any(), gomock.Any()).After(first).DoAndReturn(func(_ context.Context, c sh.Cmd) (sh.Result, error) {
		assert.Equal(t, "git clone "+url+" "+dir, c.String())
		require.Len(t, c.Env, 2)
		helper := strings.TrimPrefix(c.Env[0], "GIT_ASKPASS=")
		assert.Equal(t, "RIGGER_GIT_TOKEN=tok123", c.Env[1])
		assert.NotContains(t, testutil.ReadFile(t, helper), "tok123", "token stays out of the helper file")

		answer := func(prompt string) string {
			cmd := exec.Command(helper, prompt)
			cmd.Env = append(os.Environ(), c.Env[1])
			out, err := cmd.Output()
			require.NoError(t, err)
			return strings.TrimSpace(string(out))
		}
		assert.Equal(t, "x-access-token", answer("Username for 'https://github.com': "))
		assert.Equal(t, "tok123", answer("Password for 'https://x-access-token@github.com': "))
		return sh.Result{}, nil
	})

	asked := 0
	err := Clone(ctx, r, url, dir, func(host string) (string, error) {
		asked++
		assert.Equal(t, "github.com", host)
		return "tok123", nil
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, asked)
}

func TestCloneSSHFailureDoesNotPrompt(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), gomock.Any()).Return(sh.Result{Code: 128, Output: "git@github.com: Permission denied (publickey).\n"}, nil)
	err := Clone(context.Background(), r, "git@github.com:you/x.git", filepath.Join(t.TempDir(), "x"), func(string) (string, error) {
		t.Fatal("must not prompt for ssh URLs")
		return "", nil
	}, nil)
	require.ErrorContains(t, err, "Permission denied (publickey)")
}

func TestUseSSHOrigin(t *testing.T) {
	ctx := context.Background()
	r := mocks.NewMockRunner(gomock.NewController(t))
	gomock.InOrder(
		r.EXPECT().Run(gomock.Any(), cmdIs("git -C /r remote get-url origin")).Return(sh.Result{Output: "https://github.com/you/setup.git\n"}, nil),
		r.EXPECT().Run(gomock.Any(), gomock.Any()).Return(sh.Result{Code: 1, Output: "Hi you! You've successfully authenticated, but GitHub does not provide shell access.\n"}, nil),
		r.EXPECT().Run(gomock.Any(), cmdIs("git -C /r remote set-url origin git@github.com:you/setup.git")).Return(sh.Result{}, nil),
	)
	switched, err := UseSSHOrigin(ctx, r, "/r")
	require.NoError(t, err)
	assert.True(t, switched)
}

func TestUseSSHOriginKeepsHTTPSWithoutKey(t *testing.T) {
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), cmdIs("git -C /r remote get-url origin")).Return(sh.Result{Output: "https://github.com/you/setup\n"}, nil)
	r.EXPECT().Run(gomock.Any(), gomock.Any()).Return(sh.Result{Code: 255, Output: "Permission denied (publickey).\n"}, nil)
	switched, err := UseSSHOrigin(context.Background(), r, "/r")
	require.NoError(t, err)
	assert.False(t, switched)
}

type cmdIs string

func (m cmdIs) Matches(x any) bool { c, ok := x.(sh.Cmd); return ok && c.String() == string(m) }
func (m cmdIs) String() string     { return string(m) }
