# Keep the README's promises

## Overview
- A review of v0.1.2 against what the README and `docs/setup-repo.md` promise (2026-11-11) found four claims the code does not keep, two failures reported as success, and personal data in the public repo.
- Fix the code where the promise is right, and the docs where the code is right. Ships in v0.1.3 with per-machine variables (`docs/plans/20261028-host-vars.md`, PR #17).

## Decisions
- One plan for the six items: each is small, and together they make the README true (2026-11-11).
- `apply` asks about a linked file that holds other content, the way `sync` already does (2026-11-11).
- `apply --yes` cannot tell a file a tool replaced from a dotfile that was there before rigger (local state records no links). So on a machine with a host file it leaves the file and prints how to adopt it; on a first run (no host file) it backs up and links, as today and as the README says.
- A seed changed on both sides keeps the live file outside the TUI, as today; `apply` and `sync` now say so, and the docs say only the TUI asks.
- `sync` exits 1 when a component failed, after the push, so other work still goes out. A component waiting for a secret is listed and does not change the exit code: a machine that lacks a secret is a normal state.
- The private names stay in public git history; rewriting a published history is not worth it for these.
- Lands on one branch, `v0.1.3`, with `docs/plans/20261028-host-vars.md`, which runs first; this plan runs second on the same branch, so its line numbers in `apply.go` and `sync.go` may have moved: read the function, not the line (2026-11-12).
- Before the run, outside every task: PR #16 is merged (it removes the machine names from `docs/plans/20261027-sync-tui.md`), and docker runs (`colima start`) so the Linux container test runs instead of skipping; the run reaches it through the repository's stepwise `env_file`, which sets `DOCKER_HOST` to colima's socket (2026-11-12).
- The maintainer's own leak check over every tracked file runs outside the repo after the run, and only its result is recorded (Post-Completion); the run's own check is the repo-only grep in Acceptance Criteria (2026-11-12).

## Context (from discovery)
- `cmd/rigger/apply.go:88` passes no `Adopt`/`Leave` decisions, so the engine relinks with `KeepRepo` and moves the edit to a backup. `docs/setup-repo.md:66` says it asks. `cmd/rigger/sync.go` `decideReplacedLinks` is the prompt to share; sync asks it after `StartAskpass` (sync.go:158-163). `apply`'s `confirm` makes a new `bufio.Reader` per call (apply.go:124).
- `Differs` (internal/action/link.go:99) is any real file whose content differs from the repo's; `state.Local` keeps seed hashes only (internal/state/state.go:19). `TestApplyAndRerunIsNoop` (tests/e2e_test.go:111-132) depends on a first `apply --yes` backing up and linking an existing dotfile.
- Seeds: `docs/setup-repo.md:67` says both-changed "asks"; only the TUI asks (internal/tui/update.go:356,371). `apply` and `sync` pass no `Overwrite`, so the engine keeps the live file (internal/engine/apply.go:137-139) and records the new render's hash: the template change is dropped silently.
- `cmd/rigger/sync.go:170-173` prints `warning: … failed`, then pushes, prints `in sync` (sync.go:102) and exits 0; `--no-push` returns before the push (sync.go:92-95). `sync.go:115` drops needs-secret components with no message. `engine.MissingSecrets` and `missingSecrets` (cmd/rigger/status.go:76) already print the secrets line.
- `rigger init` clones with no terminal (internal/sh/sh.go:116, `Setsid`), so ssh cannot answer a first-connect host-key question; the `accept-new` probe in `internal/repo/clone.go:180` (`UseSSHOrigin`) runs after the clone. `internal/remote/remote.go:76` and `internal/syncer/syncer.go:58-61` already set `GIT_SSH_COMMAND` with options. `lastLine` (clone.go:146) keeps only git's last line, "and the repository exists."; `lastLines` exists in syncer.go:206.
- README:25 says sync "refuses to commit anything that looks like a credential"; the built-in check covers credential file names and common token formats (internal/syncer/syncer.go:110-113), gitleaks the rest when installed (syncer.go:184-193).
- README:116 lists `--dry-run` for every command; only `apply` has it (apply.go:27).
- Personal data in tracked files: `CLAUDE.md:3` and `CLAUDE.md:43`, `docs/plans/completed/20261008-rigger.md:16`, `docs/plans/20261025-release-check.md:54,65,67`, `docs/plans/20261008-app-checklist.md:69,175`, `docs/plans/20261009-vault-and-machine-keys.md:90,236-239`; `docs/plans/20261027-sync-tui.md` is cleaned in PR #16.
- `docs/plans/20261027-sync-tui.md` later sends interactive `apply` to the TUI's review screen, so the typed prompt here stays thin.

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully; **all tests must pass before the next task**; update this plan when scope changes
- conventions: test helpers take `*testing.T` first and call `t.Helper()`; shared helpers in `internal/testutil`; e2e in `tests/`.

## Testing Strategy
- **cli**: `apply` with a replaced link on a set-up machine: answers adopt, keep, leave through one scripted reader together with the confirm; `--yes` leaves it with the hint; a cancelled confirm asks nothing. A both-changed seed prints that the live file was kept. `sync`: a failing cmd exits 1 after the push and does not print `in sync`; with `--no-push` too; a needs-secret component is listed and the run exits 0.
- **repo**: the clone command carries `accept-new` for ssh URLs, not for HTTPS; a user's `GIT_SSH_COMMAND`, `GIT_SSH` or global `core.sshCommand` is kept; the error keeps ssh's cause line. The existing mock expectations gain the `git config` call.
- **e2e**: an `apply --yes` twin of `TestSyncLeavesALinkReplacedByARealFile`; `TestApplyAndRerunIsNoop` unchanged (first run still backs up and links).

## Progress Tracking
- mark completed items with `[x]` immediately; ➕ for new tasks; ⚠️ for blockers

## Technical Details
- `apply`: one `bufio.Reader` for `confirm` and the link questions; the question comes after the dry-run return and after the confirm; wording "is a real file with other content than the repo's"; a left link is reported as `left ~/.zshrc as it is` and the summary says it is not linked yet.
- `sync`: no `warning: … failed` loop; after the push (or at `--no-push`) the run ends with `not finished: failed [a], skipped [b]`, exit 1; a push error wins and names the failed components too; needs-secret components through `missingSecrets`.
- `init`: for a non-HTTPS URL, when neither `GIT_SSH_COMMAND` nor `GIT_SSH` is set and `git config --global --get core.sshCommand` is empty, the clone runs with `GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=accept-new` (the same precedent as remote.go:76 and clone.go:180). Errors drop git's two boilerplate lines ("Please make sure…", "and the repository exists.") and keep everything after "Cloning into". `lastLines` moves to `internal/sh` and both packages use it.
- README:25: "refuses to commit credential files and common token formats (every gitleaks rule when gitleaks is installed)"; `--dry-run` moves to `apply`'s row; `docs/setup-repo.md:66-67`: `apply` and `sync` ask about a replaced link (`--yes` leaves it once the machine is set up), and a both-changed seed is asked about in the TUI and kept elsewhere.

## What Goes Where
- **Implementation Steps**: rigger code, tests, `README.md`, `docs/setup-repo.md`, `CLAUDE.md`, the plans that hold personal data.
- **Post-Completion**: release with v0.1.3.

## Acceptance Criteria
- Run: `go test -count=1 -run 'TestApplyAsksAboutReplacedLink|TestApplyYesLeavesReplacedLink|TestApplyFirstRunLinks|TestApplySaysSeedKept|TestSyncSaysSeedKept' ./cmd/rigger/`
- Run: `go test -count=1 -run 'TestSyncNotFinished|TestSyncListsWaitingSecrets' ./cmd/rigger/`
- Run: `go test -count=1 -run 'TestCloneAcceptsNewHostKey|TestCloneErrorNamesCause' ./internal/repo/`
- Run: `go test -count=1 -run 'TestApplyLeavesALinkReplacedByARealFile|TestApplyAndRerunIsNoop' ./tests/`
- Run: `go test -count=1 -run TestLinuxServerProfile -v ./tests/ | grep -q -- '--- PASS: TestLinuxServerProfile'`
- Run: `grep -q 'common token formats' README.md && ! grep -q '^| .--dry-run. |' README.md && grep -q 'rigger apply.*--dry-run' README.md`
- Run: `grep -q 'leaves it once the machine is set up' docs/setup-repo.md && grep -q 'asked about in the TUI and kept elsewhere' docs/setup-repo.md`
- Run: `gitleaks git .`
- Run: `! git grep -n -I -E '/Users/[a-z]{3,}/|Dev/github\.com/' -- . ':!docs/plans/*public-fixes.md' ':!docs/plans/completed/*public-fixes.md'`

## Implementation Steps

### Task 1: apply asks about a replaced link
**Files:**
- Modify: `cmd/rigger/apply.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `cmd/rigger/main_test.go`
- Modify: `cmd/rigger/sync_test.go`
- Modify: `tests/e2e_test.go`

- [x] share `decideReplacedLinks` between `sync` and `apply`; one reader in `apply`; `--yes` leaves only when the host file exists
- [x] a both-changed seed prints that the live file was kept, in `apply` and `sync`
- [x] write tests: answers through one reader with the confirm (`TestApplyAsksAboutReplacedLink`); `--yes` on a set-up machine (`TestApplyYesLeavesReplacedLink`); first run unchanged (`TestApplyFirstRunLinks`); cancelled confirm asks nothing; the seed message in apply (`TestApplySaysSeedKept`) and in sync (`TestSyncSaysSeedKept`); `TestDecideReplacedLinks` still passes
- [x] e2e: `TestApplyLeavesALinkReplacedByARealFile`, the `apply --yes` twin of the sync test
- [x] run tests - must pass before task 2

### Task 2: sync says when it did not finish
**Files:**
- Modify: `cmd/rigger/sync.go`
- Modify: `cmd/rigger/sync_test.go`

- [x] failed components end the run with `not finished: …`, exit 1, after the push or at `--no-push`; no `in sync`; needs-secret components listed, exit unchanged
- [x] write tests: a failing cmd and `--no-push` (`TestSyncNotFinished`); a component waiting for a secret (`TestSyncListsWaitingSecrets`); a clean run still exits 0 with `in sync`
- [x] run tests - must pass before task 3

### Task 3: init over ssh on a fresh machine
**Files:**
- Modify: `internal/repo/clone.go`
- Modify: `internal/repo/clone_test.go`
- Modify: `internal/sh/sh.go`
- Modify: `internal/sh/sh_test.go`
- Modify: `internal/syncer/syncer.go`

- [ ] `accept-new` for an ssh clone unless the user set an ssh command; `lastLines` moved to `internal/sh`; the clone error keeps ssh's cause
- [ ] write tests: ssh vs HTTPS and the three user settings kept (`TestCloneAcceptsNewHostKey`); the error text from a host-key failure and a publickey failure (`TestCloneErrorNamesCause`); `LastLines` in `internal/sh`
- [ ] run tests - must pass before task 4

### Task 4: docs that match the code
**Files:**
- Modify: `README.md`
- Modify: `docs/setup-repo.md`

- [ ] the credential sentence (with the words "common token formats"); `--dry-run` out of the general flag table and on the `rigger apply` line of "Every day"; the link row says `--yes` "leaves it once the machine is set up", the seed row says a both-changed seed is "asked about in the TUI and kept elsewhere"
- [ ] run tests - must pass before task 5

### Task 5: no personal data in the public repo
**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/plans/completed/20261008-rigger.md`
- Modify: `docs/plans/20261025-release-check.md`
- Modify: `docs/plans/20261008-app-checklist.md`
- Modify: `docs/plans/20261009-vault-and-machine-keys.md`

- [ ] `CLAUDE.md`: "the maintainer's own setup repo" instead of its name and path; drop the archive repo's name
- [ ] the four plans: neutral machine, repo and path names; no named people or private tool paths
- [ ] the home-path grep in Acceptance Criteria finds nothing
- [ ] run tests - must pass before task 6

### Task 6: [Final] Update documentation
**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/plans/20261027-sync-tui.md`

- [ ] `CLAUDE.md` "Next": this plan and `20261028-host-vars` on one branch for v0.1.3
- [ ] `docs/plans/20261027-sync-tui.md` (already cleaned by PR #16): its Context notes that `apply` asks about replaced links

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

- The maintainer runs their own leak check over every tracked file and records only the result.
- A fresh Mac in the tart VM matrix (`tests/vm/run.sh`) runs `rigger init` over ssh against a host it has never seen.
- Release v0.1.3 from the `v0.1.3` branch once both plans are done.
