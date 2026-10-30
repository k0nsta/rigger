package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// RunWithOwnGitConfig runs a package's tests with git reading a global config of their own
// (and no system config), so the tests behave the same whatever the user's ~/.gitconfig or a
// tool's GIT_CONFIG_GLOBAL says, such as a push guard. Use it from TestMain.
func RunWithOwnGitConfig(m *testing.M) int {
	dir, err := os.MkdirTemp("", "rigger-gitconfig-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "gitconfig")
	conf := "[init]\n\tdefaultBranch = main\n[user]\n\tname = rigger-test\n\temail = test@rigger.invalid\n"
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": path, "GIT_CONFIG_NOSYSTEM": "1"} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return m.Run()
}
