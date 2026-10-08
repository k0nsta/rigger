# Background sync

## Overview
- `rigger sync --background`, run every 15–30 min by a launchd agent (macOS) or a systemd user timer (Linux), keeps a machine up to date with its setup repo without the user running `rigger sync`.
- It only does what cannot surprise: fetch; move to the remote when the repo is in a plain state; apply decision-free changes (new links, template updates of untouched seeds); auto-commit and push only allowlisted, host-scoped paths; post one notification for everything else ("3 local changes, 2 new packages: run rigger sync").
- Never automatic: package installs, `cmd` runs or checks, sudo, `op read`, conflict/adopt decisions, layouts import, pushing anything that is not allowlisted (including the user's own unpushed commits), touching an in-progress git operation.
- `rigger daemon install|uninstall|status` sets the schedule up and shows the last run.

## Context (from discovery)
- `cmd/rigger/sync.go`: interactive/`--yes` flow; guard `!yes && !isTTY`; requires a host file.
- `internal/syncer`: `Changes`, `Commit` (`git add` + plain `git commit` = whole index), `Pull` (`pull --rebase`, aborts any rebase dir it finds), `Push` (everything ahead), `Ahead` (0 without upstream).
- `internal/engine`: `Probe` runs cmd `check` scripts, renders seeds (→ `Secrets.Get` → `op read`), loads the brew inventory, and stops at the first error; `Apply` runs every unsettled action of a component; `severity`/`ActionStatus` internals are unexported.
- `internal/app.Session.Save` rebuilds the host file from the selection and carries over only `IgnorePackages`.
- `internal/state.Local` (`~/.config/rigger/state.yaml`) is rewritten whole by every writer (last writer wins).
- Notifications: `agtermctl notify` (needs agterm running), macOS `osascript`, Linux `notify-send` (desktop only).

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully; **all tests must pass before the next task**; update this plan when scope changes
- conventions: consumer interfaces in `dependency.go` + mockgen; test helpers take `*testing.T` first and call `t.Helper()`; shared helpers in `internal/testutil`; e2e in `tests/`.

## Testing Strategy
- **unit**: `DecideGit` (git state → plan) and `SafeSubset` (statuses → filtered statuses) are pure and table-driven; unit rendering for plist/systemd; flock; notification de-dup.
- **e2e** (`tests/background_test.go`), two clones of a temp bare repo, fake `op`, `sudo`, `brew`, `apt-get` on PATH that fail the test when called:
  - B clean → picks up A's change; a seed with a changed template and untouched live file is re-rendered
  - B has a non-allowlisted modified file → no rebase, one notification
  - B has a **staged** non-allowlisted file and an allowlisted change → only the allowlisted path is committed; the staged file stays staged, unpushed
  - B has an unpushed manual commit → nothing pushed, nothing rebased, notified
  - rebase/merge in progress, detached HEAD, no upstream → fetch only, notified once
  - rebase conflict → aborted (only because this run started it), tree as before, nothing pushed
  - gate hit on an allowlisted file → not committed, not pushed, notified
  - three failed fetches → exactly one notification
  - component with a missing link and missing brew packages → only the link is applied, sudo never started
- `rigger daemon install --dry-run` prints the unit files; real registration is Post-Completion.

## Progress Tracking
- mark completed items with `[x]` immediately; ➕ for new tasks; ⚠️ for blockers

## Solution Overview
- **One shot per tick**: the scheduler starts `rigger sync --background`; it takes the repo lock, runs under a 4-minute timeout, writes one summary line, exits. Survives sleep/reboot; launchd/systemd never overlap runs.
- **Two pure decisions**: `DecideGit(gitState) GitPlan` (commit/rebase/push or wait, with reasons) and `SafeSubset(statuses) (apply, waiting)`; the runner executes them and re-checks state between steps (a gate hit or commit failure leaves files dirty → no rebase).
- **Background probe mode**: the engine probes links, seeds and layouts only; cmds are not checked, packages are not inventoried, `op://` secrets are not resolved (→ "waiting: secrets"), and one failing action marks its component waiting instead of failing the whole probe. Package/cmd work is reported when the pulled diff touched the manifest or a Brewfile.
- **Commit and push scope**: commit with `git commit --only -- <allowlisted files>`; push only when every file changed in `@{u}..HEAD` is allowlisted; otherwise neither rebase nor push, report "N unpushed commits".
- **Preconditions** (else fetch only + notify once): on a branch, upstream set, no `rebase-merge`/`rebase-apply`/`MERGE_HEAD`/`CHERRY_PICK_HEAD`/`REVERT_HEAD`/`BISECT_LOG` (checked with `git rev-parse --git-path`, works in worktrees).
- **Move to the remote**: after `git fetch`, `git rebase @{u}` (no second network round trip, acts on exactly what was decided); a conflict aborts only this run's rebase.
- **Allowlist** (`auto_commit` in the host file): exact paths or `dir/` prefixes; recommended content is host-scoped (`hosts/<name>.yaml`, `agterm-app/layouts/<name>/`). A file is committed only after it has been quiet for one interval (mtime), and background pushes happen at most once per hour (commits wait locally). An allowlisted path that ever conflicts is dropped from auto-commit until the next foreground sync, with one notification.
- **Notify on change only**: hash of the sorted waiting reasons; pulled/applied counts alone do not notify.

## Technical Details

### Host file
```yaml
profile: work
auto_commit:              # exact paths or dir/ prefixes, relative to the repo
  - hosts/work-mac.yaml
  - agterm-app/layouts/work-mac/
```
`app.Session.Save` carries `auto_commit` over like `ignore_packages`. Pulls can change another host's allowlist (single user: acceptable; documented).

### State
- `~/.local/state/rigger/background.yaml` (separate from `state.yaml`): `last_run`, `last_result` (one line), `notified_hash`, `fetch_failures`, `blocked_paths`, `last_push`.
- Repo lock `~/.local/state/rigger/<repo-hash>.lock` with `syscall.Flock`: background uses `LOCK_NB` (skip the tick if busy); foreground writers (TUI, `apply`, `sync`, `adopt`) wait up to 10 s and say "a background sync is running". Released by the kernel when the process dies.

### Git and credentials
- Every git call: `GIT_TERMINAL_PROMPT=0`, `GIT_OPTIONAL_LOCKS=0` for status; ssh via the user's `core.sshCommand` when set, else `ssh`, plus `-o BatchMode=yes -o ConnectTimeout=10`.
- Detected and reported instead of prompting: `commit.gpgsign=true`, an `IdentityAgent` pointing at the 1Password agent, missing `SSH_AUTH_SOCK` under systemd.
- Hooks: run normally; a failing hook = "waiting: hook <name> failed" (decided).
- Seeds updated in background: backup by copy (live file never missing), live hash re-checked right before writing; a seed whose component declares `process` (new optional seed field) that is running is left for later.

### Scheduler units
- macOS `~/Library/LaunchAgents/dev.rigger.sync.plist`: `ProgramArguments [<rigger>, sync, --background, --repo, <repo>]`, `StartInterval`, `RunAtLoad false`, `EnvironmentVariables {HOME, PATH}`, stdout/stderr → `~/.local/state/rigger/sync.log`; reinstall = `launchctl bootout` then `bootstrap gui/<uid>`.
- Linux `~/.config/systemd/user/rigger-sync.service` (`Type=oneshot`, `TimeoutStartSec=5min`, `Environment=PATH=…`) + `rigger-sync.timer` (`OnBootSec=5min`, `OnUnitActiveSec=<every>`, `[Install] WantedBy=timers.target`); `install` checks `loginctl show-user -p Linger` and warns; requires a passphrase-less deploy key or a credential helper (stated in `status`).
- rigger path: the `rigger` on PATH when `EvalSymlinks` equals the running binary's `EvalSymlinks` (stable across `brew upgrade`), else `os.Executable()`.

## What Goes Where
- **Implementation Steps** (`[ ]`): code, tests, docs in rigger.
- **Post-Completion**: real registration on this Mac and the homelab; allowlist in the user's setup repo.

## Implementation Steps

### Task 1: Git decision policy

**Files:**
- Create: `internal/background/decide.go`, `internal/background/decide_test.go`

- [ ] `GitState` (branch, upstream, in-progress op, ahead files, behind count, dirty/staged changes, quiet-period mtimes, fetch error) and `DecideGit` per Solution Overview
- [ ] allowlist matching: exact path or `dir/` prefix; blocked paths excluded
- [ ] waiting reasons + summary text (≤ 120 chars)
- [ ] table tests incl. detached HEAD, no upstream, rebase in progress, staged non-allowlisted, unpushed manual commit, quiet period, push throttle
- [ ] run tests — must pass before Task 2

### Task 2: Engine background probe and safe subset

**Files:**
- Modify: `internal/engine/engine.go`, `internal/engine/engine_test.go`, `internal/action/seed.go`, `internal/manifest/manifest.go`

- [ ] `ProbeOptions{Background bool}`: skip cmds and brew/apt probes, no `op://` resolution (→ needs-secret waiting), per-component errors become waiting
- [ ] `FilterActions(statuses, keep)` rebuilding `Actions`, `State`, `Summary`; `SafeSubset` keeps link `missing` and seed `update`
- [ ] optional seed `process`; background seed apply backs up by copy and re-checks the live hash
- [ ] tests: no runner calls in background probe, filter recomputes state, brew+link component applies only the link and `NeedsSudo` is false
- [ ] run tests — must pass before Task 3

### Task 3: State, lock, host file carry-over

**Files:**
- Create: `internal/background/lock.go`, `internal/background/state.go` + tests
- Modify: `internal/state/state.go` (`HostFile.AutoCommit`), `internal/app/app.go` (`Save` carries it), `internal/app/app_test.go`

- [ ] flock lock (non-blocking for background, 10 s wait for foreground)
- [ ] `background.yaml` read/write
- [ ] `AutoCommit` field + carry-over in `Save`
- [ ] tests: lock contention and release on exit, state round-trip, Save keeps auto_commit
- [ ] run tests — must pass before Task 4

### Task 4: Syncer additions

**Files:**
- Modify: `internal/syncer/syncer.go`, `internal/syncer/syncer_test.go`

- [ ] `Fetch` (ssh options from `core.sshCommand`), `State` (branch/upstream/in-progress/ahead files/behind/staged), `CommitOnly(files)` (`git commit --only`), `RebaseUpstream` (aborts only its own rebase), `Push`
- [ ] signing / 1Password agent / SSH_AUTH_SOCK detection helpers
- [ ] real-git tests for each, incl. user rebase in progress untouched
- [ ] run tests — must pass before Task 5

### Task 5: `sync --background` runner

**Files:**
- Create: `cmd/rigger/background.go`, `internal/background/notify.go`, `internal/background/dependency.go`, `tests/background_test.go`
- Modify: `cmd/rigger/sync.go` (`--background` bypasses the TTY guard; quiet when no host file)

- [ ] lock → timeout ctx → layouts export (allowlisted only) → state → DecideGit → commit → re-check → rebase → background probe → SafeSubset apply → push → background.yaml → notify
- [ ] notifier backends (env `RIGGER_NOTIFY_CMD`, agtermctl, osascript, notify-send, none) with de-dup
- [ ] foreground commands take the lock; foreground sync resets `notified_hash` and `blocked_paths`
- [ ] e2e scenarios from Testing Strategy
- [ ] run tests — must pass before Task 6

### Task 6: `rigger daemon install|uninstall|status`

**Files:**
- Create: `internal/daemon/{daemon.go,launchd.go,systemd.go,daemon_test.go}`, `cmd/rigger/daemon.go`

- [ ] render plist / service+timer per Technical Details; `--every 20m` (min 5m); `--dry-run`
- [ ] install/uninstall (bootout before bootstrap; daemon-reload + enable --now), linger check
- [ ] status: registered, interval, last run/result, credential warnings
- [ ] tests: rendering, interval parsing, rigger path choice, install/uninstall command lines (mocked runner)
- [ ] run tests — must pass before Task 7

### Task 7: Verify acceptance criteria
- [ ] e2e negative cases from Testing Strategy green (nothing installed, run, prompted or pushed outside the policy)
- [ ] `rigger status` shows the last background result line
- [ ] `make check` green on macOS and Ubuntu CI

### Task 8: [Final] Documentation
- [ ] README: background sync; `docs/setup-repo.md`: `auto_commit`, seed `process`, what background sync never does
- [ ] move this plan to `docs/plans/completed/`

## Decisions
- Hooks run in background runs; a failing hook = "waiting: hook <name> failed" (no `--no-verify`).
- Cadence: an allowlisted file is committed after one quiet interval; at most one background push per hour.

## Open questions
- macOS notification from a LaunchAgent via `osascript` may be attributed to Script Editor and stay silent without that permission (unverified; user checks later). Fallback: agterm when running, else `rigger status` only.

## Post-Completion
**Manual verification**
- this Mac: `rigger daemon install --every 20m`; edit an allowlisted file → committed after the quiet period, pushed within the hour; edit `.zshrc` → one notification, no commit
- homelab: systemd timer with linger; a change pushed from the Mac appears within one tick
- laptop asleep for hours → one run on wake, no backlog

**Setup repo**
- add `auto_commit` (host-scoped paths) to each `hosts/<name>.yaml`
