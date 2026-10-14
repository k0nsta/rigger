package repo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/testutil"
)

// seedRepo makes a repo with one commit on branch.
func seedRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := testutil.Tree(t, map[string]string{"manifest.yaml": "version: 1\n"})
	gitRun(t, dir, "init", "-q", "-b", branch)
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	return dir
}

func TestReadHead(t *testing.T) {
	ctx := context.Background()
	r := sh.NewExec(t.TempDir())

	t.Run("clone on its default branch", func(t *testing.T) {
		clone := filepath.Join(t.TempDir(), "machine-setup")
		gitRun(t, "", "clone", "-q", seedRepo(t, "trunk"), clone)
		h, err := ReadHead(ctx, r, clone)
		require.NoError(t, err)
		assert.Equal(t, Head{Dir: clone, Branch: "trunk", Default: "trunk"}, h, "origin/HEAD names the default")
		assert.Empty(t, h.Problem())

		gitRun(t, clone, "switch", "-q", "-c", "feat/upwork-plugin")
		h, err = ReadHead(ctx, r, clone)
		require.NoError(t, err)
		assert.True(t, h.OffDefault())
		assert.Equal(t, "machine-setup is on branch feat/upwork-plugin, not trunk: your linked configs come from that branch", h.Problem())
		assert.Equal(t, "switch back with `git -C "+clone+" switch trunk`; keep feature work in a git worktree", h.Fix("/nonexistent"))
	})

	t.Run("detached HEAD without a remote", func(t *testing.T) {
		dir := seedRepo(t, "main")
		gitRun(t, dir, "switch", "-q", "--detach")
		h, err := ReadHead(ctx, r, dir)
		require.NoError(t, err)
		assert.Equal(t, Head{Dir: dir, Default: "main"}, h)
		assert.Contains(t, h.Problem(), "has a detached HEAD, not main")
	})

	t.Run("no default branch to compare with", func(t *testing.T) {
		dir := seedRepo(t, "mine")
		h, err := ReadHead(ctx, r, dir)
		require.NoError(t, err)
		assert.False(t, h.OffDefault())
		assert.Empty(t, h.Problem())
	})
}
