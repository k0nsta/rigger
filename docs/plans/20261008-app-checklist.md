# Apps as a checklist

## Overview
- GUI apps (Zoom, Slack, Spotify…) carry no config worth tracking, update themselves and often come from IT, the App Store or a vendor installer. Treating them like CLI tools makes runs fail (pkg casks, sudo, vendor quirks) and fills `sync` with stray noise.
- A component marked `best_effort: true` (Brewfile components only) still installs its Brewfile, but packages that fail become an "install manually" checklist: the component ends **warned**, not failed, its other actions and its dependents still run, and `rigger apply` / the TUI exit 0.
- `sync` stops offering casks the user installed by hand, except casks matching the manifest's `strays.casks` globs (e.g. `font-*`). Formulae are still offered.
- Configs, CLI tools, fonts and apps with tracked config (agterm) are unchanged.

## Context (from discovery)
- `internal/manifest/manifest.go`: `Component` (no per-component failure policy), top-level `Manifest`; `validate.go` checks fields.
- `internal/action/brew.go`: `BrewPackage{Kind, Name}` + `Short()`; `Brew.Probe(ctx, r, inv, exclude, paths)` → `BrewProbe{Missing, Present, Excluded, Installed}`; `Brew.Apply` runs `brew bundle install --no-upgrade` and errors on non-zero exit (brew bundle installs the rest, then exits 1).
- `internal/engine/engine.go`: probe order per component is brew, apt, links, seeds, layouts, cmds; `e.inventory` is cached, `ResetInventory()` only after a successful `Apply`; without Homebrew `probeBrew` returns a synthetic all-missing probe.
- `internal/engine/apply.go`: `applyComponent` returns on the first action error; outcomes `Done|Failed|Skipped`; `firstFailed` skips dependents of a failed component; a cancelled context breaks the loop.
- `cmd/rigger/apply.go`: any failed/skipped → "not finished" error; summary `done: %d component(s); host file hosts/%s.yaml updated`.
- `cmd/rigger/sync.go`: `printEvent` and a `Failed`-only warning loop; `cmd/rigger/tui.go:59-62` exits 1 on any outcome that is not `Done`.
- `internal/tui/update.go:472` sets run state only for `Done|Failed|Skipped`; `view.go` `viewFinish` prints a red bullet for every outcome with `Err != nil`.
- `internal/syncer/syncer.go` `Strays` (short names from `brew list --cask -1`), called from `sync.go` `handleStrays`; `TestStrays` in `syncer_test.go`.
- Tests: engine in `internal/engine/engine_test.go` (`newEngine`, `fakeShell`, counting fakes as in `TestBrewInventoryLoadedOnce`); TUI in `internal/tui/tui_test.go`; e2e `tests/e2e_test.go` (`newMachine`, `machine.env()`, no fake brew yet).

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully; **all tests must pass before the next task**; update this plan when scope changes
- conventions: consumer interfaces in `dependency.go` + mockgen; test helpers take `*testing.T` first and call `t.Helper()`; shared helpers in `internal/testutil`; e2e in `tests/`.

## Testing Strategy
- **unit**: manifest fields and validation; engine warned path with a sequence-aware fake brew (first `brew list --cask -1` without the cask, second still without it); install-command formatting table; stray filter table.
- **e2e** (`tests/e2e_test.go`, fake `brew` on PATH, test repo with a cask component not limited to darwin so Ubuntu CI runs it): `apply --yes` with a best-effort component whose cask install fails → exit 0, checklist printed, link in the same component created, host file saved; same component without the flag → exit 1 as today.
- **tui**: warned outcome → yellow mark, "Install manually" section, not in the failed list, exit code 0.

## Progress Tracking
- mark completed items with `[x]` immediately; ➕ for new tasks; ⚠️ for blockers

## Solution Overview
- **Policy lives on the component**: one flag on an "apps" component; no per-package syntax in Brewfiles. Only Brewfile packages are covered: `apt-get install` is one batch where one bad package installs nothing, so the model does not fit apt.
- **Keep going after the brew action**: in a best-effort component a `KindBrew` error is recorded and the remaining actions (links, seeds, cmds) run; any later error fails the component (wrapping both).
- **Re-probe exactly what was missing**: `ResetInventory()`, then `action.Brew{File, Packages: before.Missing}.Probe(ctx, runner, freshInventory, nil, appPaths)`. Excluded and present-on-disk packages never enter the checklist; a cask that became present on disk drops out.
- **Checklist** = the re-probed `Missing`, carried in `Outcome.Missing`; one shared formatter `BrewPackage.InstallCommand()` (`brew tap x/y`, `brew install owner/tap/formula`, `brew install --cask name`) used by apply, sync and the TUI.
- **Strays**: casks are filtered by `strays.casks` globs (`path.Match` on the short token); unset keeps today's behaviour (all casks), `[]` offers none.

## Technical Details

### Manifest
```yaml
strays:
  casks: ["font-*"]        # unset = every cask; [] = none
components:
  - id: apps
    brewfile: brew/apps.Brewfile
    best_effort: true      # failed packages → checklist, component warned
```

### Engine
- `EventKind` gains `Warned`; `Outcome` gains `Missing []action.BrewPackage` and `Note string`; a warned outcome has `Err == nil`; the `Warned` event carries the same `Missing` for live output.
- Best-effort brew failure handling, in order:
  - `ctx.Err() != nil` → Failed (cancel behaves as today)
  - `ResetInventory()`; Homebrew gone (`!hasBrew`) or probe error → Failed
  - still-missing packages → Warned{Missing}
  - nothing missing but `brew bundle` exited non-zero (untracked `mas`/vscode lines) → Warned{Note: "brew bundle exited N, see log"}
- `ResetInventory()` after every best-effort re-probe; `firstFailed` lets dependents of a warned component run.
- Unchanged: a NeedsSecret skip in a best-effort component is still Skipped and blocks dependents.

### CLI and TUI
- `apply` and `sync`: `==> <component> warned` then one `install manually: <InstallCommand>` line per package (or the note); summary keeps its prefix: `done: N component(s), M warned; host file …`; exit 0 when nothing failed or was skipped.
- TUI: warned run state (yellow), finish-screen "Install manually" section, header `N done, M warned`, process exit 1 only on Failed/Skipped.
- `rigger status`: a best-effort component with missing packages shows its detail suffixed `(best effort)`.

## What Goes Where
- **Implementation Steps**: rigger code, tests, `docs/setup-repo.md`.
- **Post-Completion**: machine-setup manifest changes and a VM run.

## Implementation Steps

### Task 1: Manifest fields
**Files:**
- Modify: `internal/manifest/manifest.go`
- Modify: `internal/manifest/validate.go`
- Modify: `internal/manifest/manifest_test.go`

- [ ] add `BestEffort bool \`yaml:"best_effort"\`` to `Component` and `Strays struct{ Casks *[]string \`yaml:"casks"\` }` to `Manifest`
- [ ] validate: `best_effort` without `brewfile` → "best_effort needs a brewfile"; every `strays.casks` entry is a valid `path.Match` pattern
- [ ] add `(*Manifest).StrayCask(short string) bool` (nil → true, else any glob matches)
- [ ] write tests: parse both fields, KnownFields still rejects typos, both validation errors, `StrayCask` table (unset, `[]`, `font-*`)
- [ ] run tests - must pass before task 2

### Task 2: Install-command formatter
**Files:**
- Modify: `internal/action/brew.go`
- Modify: `internal/action/brew_test.go`

- [ ] add `func (p BrewPackage) InstallCommand() string` for tap, formula (full `Name`) and cask
- [ ] write table tests: tap, core formula, tap formula, cask
- [ ] run tests - must pass before task 3

### Task 3: Warned outcome in the engine
**Files:**
- Modify: `internal/engine/apply.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/engine_test.go`

- [ ] add `Warned` event kind, `Outcome.Missing`, `Outcome.Note`, `Event.Missing`
- [ ] `applyComponent`: in a best-effort component record a `KindBrew` error and continue; a later action error → Failed wrapping both
- [ ] after the loop: re-probe as in Technical Details (ctx check, reset, `!hasBrew` → Failed, probe only previous `Missing`, non-zero exit with nothing missing → Warned with note)
- [ ] `firstFailed` ignores warned components
- [ ] write tests: one missing cask → Warned and the component's link still created; excluded package and present-on-disk cask not in `Missing`; Homebrew gone after the failure → Failed; cancelled context → Failed; link failure after brew failure → Failed; nothing missing + exit 1 → Warned with note; dependent of a warned component runs; NeedsSecret skip unchanged
- [ ] run tests - must pass before task 4

### Task 4: CLI apply, sync and status output
**Files:**
- Modify: `cmd/rigger/apply.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `cmd/rigger/status.go`
- Modify: `cmd/rigger/main_test.go`
- Modify: `tests/e2e_test.go`

- [ ] one checklist printer shared by `apply` and `sync` (`printEvent` handles `Warned`)
- [ ] exit 0 when only warned; summary `done: N component(s), M warned; host file hosts/<name>.yaml updated` (prefix unchanged)
- [ ] `status` detail `(best effort)` for a best-effort component with missing packages
- [ ] add a fake-brew e2e helper (takes `*testing.T`, `t.Helper()`; bin dir prepended in `machine.env()`) answering `command -v brew`, `list --formula -1`, `list --cask -1`, `tap`, `info --cask --json=v2` (non-zero), `bundle install` (exit 1)
- [ ] write e2e tests: best-effort cask failure → exit 0, checklist line `install manually: brew install --cask <name>`, link created; without the flag → exit 1; `sync --yes` prints the checklist
- [ ] run tests - must pass before task 5

### Task 5: TUI warned state and checklist
**Files:**
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/view.go`
- Modify: `internal/tui/styles.go`
- Modify: `cmd/rigger/tui.go`
- Modify: `internal/tui/tui_test.go`

- [ ] `update.go`: `Warned` sets run state; yellow mark in the progress list
- [ ] `view.go` `viewFinish`: "Install manually" section from `Outcome.Missing`/`Note`; warned outcomes not in the failed list; header counts warned
- [ ] `cmd/rigger/tui.go`: exit 1 only on Failed/Skipped
- [ ] write tests: warned outcome → section rendered, header counts, no red bullet; exit-code helper returns 0 for warned only
- [ ] run tests - must pass before task 6

### Task 6: Cask strays filter
**Files:**
- Modify: `internal/syncer/syncer.go`
- Modify: `internal/syncer/syncer_test.go`
- Modify: `cmd/rigger/sync.go`

- [ ] `Strays` takes `caskFilter func(short string) bool` applied to the short token; `handleStrays` passes `Manifest.StrayCask`
- [ ] formulae unaffected; ignore list still applies
- [ ] update `TestStrays` for the new signature; add cases: unset → all casks, `["font-*"]` → only fonts (incl. a tap font cask), `[]` → no casks, formulae always listed
- [ ] run tests - must pass before task 7

### Task 7: Contract docs
**Files:**
- Modify: `docs/setup-repo.md`

- [ ] document `best_effort` (Brewfile only, what warned means, retried on every apply until installed or excluded) and `strays.casks` with the YAML example above; `example/` stays unchanged
- [ ] run tests - must pass before task 8

### Task 8: Verify acceptance criteria
- [ ] a failing cask in a best-effort component → warned, other actions and dependents ran, exit 0, checklist shown (apply, sync, TUI)
- [ ] a failing package in a normal component behaves exactly as before
- [ ] `sync` with `strays.casks: ["font-*"]` offers a hand-installed font cask and not a hand-installed app cask
- [ ] run full test suite: `make check`

### Task 9: [Final] Update documentation
- [ ] update README.md (outcomes list, `(best effort)` status detail)
- [ ] move this plan to `docs/plans/completed/`

## Decisions
- `best_effort` covers Brewfile packages only; apt components fail as before.
- A warned component stays pending: the next `apply` retries the missing packages (and may ask for sudo again). To stop that, the user installs the app by hand (then it is reported present) or unticks the package (host `exclude_packages`). `rigger status` keeps exiting 1 while something is missing; the `(best effort)` detail says why.
- Checklist data travels in `Outcome`, not by parsing brew output.

## Open questions
- Should `rigger status` exit 0 when only best-effort packages are missing? (Kept at 1 for now: scripts rely on "1 = something missing".)

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**machine-setup**
- `apps`: `best_effort: true`.
- Split `leapp`, `postman`, `dbeaver-community` out of `work.Brewfile` into `brew/work-apps.Brewfile` + a best-effort `work-apps` component in the work profile, so kubectl/helm failures still fail.
- `strays: {casks: ["font-*"]}`.

**Manual verification**
- VM run (`tests/vm/run.sh`) of the personal and work profiles once the golden image can be pulled.
