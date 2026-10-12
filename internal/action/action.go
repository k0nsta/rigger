// Package action probes and applies the things a component does: links, seeds, layouts,
// packages and commands. Probes are read-only; Apply changes the machine.
package action

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status is the probed state of one action.
type Status string

// Statuses shared by all actions; see the plan's probe table.
const (
	OK          Status = "ok"
	Missing     Status = "missing"
	Conflict    Status = "conflict"     // something else is at dst; backed up, then replaced
	Relink      Status = "relink"       // link into the repo's old location: replaced, no backup
	Differs     Status = "differs"      // real file with other content: adopt or keep
	Update      Status = "update"       // seed template changed, live untouched: re-render
	BothChanged Status = "both-changed" // seed template and live both changed: overwrite or keep
	LocalEdit   Status = "local-edit"   // only live seed changed: left alone
	NeedsSecret Status = "needs-secret" // seed or cmd needs a secret that is not set
	Todo        Status = "todo"         // cmd check failed or packages to install
	Present     Status = "present"      // installed outside the package manager: ok
	NoSnapshot  Status = "no-snapshot"  // layouts: nothing exported for this host yet
	FreshTarget Status = "fresh-target" // layouts: app has no state yet, import possible
)

// Settled reports whether the status needs no action.
func (s Status) Settled() bool {
	return s == OK || s == LocalEdit || s == Present
}

// Env is what every action needs to resolve and back up paths.
type Env struct {
	Home string
	Repo string
	// PrevRepo is the remembered repo path when the repo now lives elsewhere.
	PrevRepo string
	// BackupRoot holds this run's backups; a timestamped dir is created on first use.
	BackupRoot string
	Now        func() time.Time

	backupDir string
}

// Expand resolves a manifest dst (with ~) to an absolute path.
func (e *Env) Expand(path string) string {
	if path == "~" {
		return e.Home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(e.Home, rest)
	}
	return path
}

// RepoPath resolves a manifest src to an absolute path inside the repo.
func (e *Env) RepoPath(src string) string { return filepath.Join(e.Repo, filepath.FromSlash(src)) }

// Backup moves path into this run's backup dir, keeping its path relative to HOME.
func (e *Env) Backup(path string) (string, error) {
	if e.backupDir == "" {
		now := time.Now
		if e.Now != nil {
			now = e.Now
		}
		e.backupDir = filepath.Join(e.BackupRoot, now().Format("20060102-150405"))
	}
	rel, err := filepath.Rel(e.Home, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = strings.TrimPrefix(path, string(filepath.Separator))
	}
	dst := filepath.Join(e.backupDir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	if err := os.Rename(path, dst); err != nil {
		return "", fmt.Errorf("back up %s: %w", path, err)
	}
	return dst, nil
}

// copyFile copies src to dst with perm, creating parent dirs.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close() //nolint:errcheck // read-only
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dst, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	return nil
}
