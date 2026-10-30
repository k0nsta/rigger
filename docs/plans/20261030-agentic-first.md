# Agentic first

## Overview
- An agent in a harness can steer rigger end to end: read state as JSON, get rigger's questions as JSON, answer them, and let rigger commit, pull, apply and push. Today output is text for people, an agent's only way through `sync` is `--yes` (commits every change, skips the package questions), and the agent skill tells agents not to run `sync` or `apply`.
- `--json` on `status`, `apply`, `sync`, `adopt` and `validate`: one record on stdout, a versioned schema, stable exit codes, progress in a run log.
- `sync --json` works in rounds: it runs until it needs an answer, exits with a `needs_answers` record, and the agent calls again with `--answers`. Nothing stays running.
- Ships as v0.2.0, before the sync TUI (now v0.3.0).

## Decisions
- The agent drives the whole loop; the person steps in only for sudo and secrets (2026-10-30).
- Rounds, not a held-open process or an MCP server: an agent's shell tool runs a command and reads what it printed. State comes from git, so a killed session loses nothing and a repeat call is safe (2026-10-30).
- Part of the work needing sudo or a secret: finish the rest, push, and end with `needs_person` listing each component, what it needs and the exact command. The sudo password and secret values never appear in a record (2026-10-30).
- No new permission setting: asking the agent to sync is the permission; the skill tells the agent to ask the person for any pick the request does not cover (2026-10-30).
- Agentic before the sync TUI: both need `internal/syncflow`, and JSON is the smaller front end (2026-10-30).
- A component that fails for any other reason also ends the run in `needs_person`, never in `done`: exit 0 means everything asked for happened.
- `apply --json` asks about replaced links; text `apply` keeps replacing them with a backup. The divergence is deliberate: an agent must not move a person's edit to a backup unasked.
- `sync` keeps both-changed seeds without asking, in both modes, as today.

## Context (from discovery)
- `cmd/rigger/sync.go:23-105` `sync`: guards (host file, `repo.ReadHead` branch guard), `exportLayouts`, `handleStrays` (darwin), `pickFiles`, `sy.Commit`, `sy.Pull` (`ErrConflict`, `ErrUncommitted`, `ErrNoAnswer`), reopen session, `applyFromRepo` (`askApply`, then `DropFromHost` and a commit of the host file, then `decideReplacedLinks` over the picked components), `Push`. Askpass starts before those answers (sync.go:158). A failed component only prints a warning and the push goes on (sync.go:169-173). It refuses without a terminal or `--yes` (sync.go:33).
- `exportLayouts` rewrites the snapshot from live files on every run (`internal/action/layouts.go:86-99`) and prints to stdout (sync.go:347-349).
- stdout writers to move under `--json`: `warnHead` (sync.go:357), `exportLayouts`, `printEvent` and the prompts, the deferred `log:` line (apply.go:76), `origin switched` (apply.go:116), adopt (adopt.go:41,43). `noteElsewhere` is on stderr. Flag errors, positional stops (`sync foo --json`), `-h` and usage errors produce no record today (main.go:70, skill.go:18). `validate --json` reads the flag as a dir (main.go:111-114).
- `cmd/rigger/apply.go`: `--yes`, `--dry-run`, a y/n `confirm`, no decisions passed (apply.go:88); `status.go` exits 1 when anything is pending, the one machine signal today.
- `docs/plans/20261027-sync-tui.md` designs `internal/syncflow`; its Task 1 moves here. The repo lock designed in `docs/plans/20261008-background-sync.md` (`syscall.Flock`) moves here too.
- Sudo: `Session.StartAskpass(ask)` takes the callback; `terminalPassword` returns false without a terminal. A refused helper is called once, sudo exits 1, and the failure text is generic (`internal/action/cmd.go:60-62`, `brew.go:398`), so only the callback can tell a refusal from a real failure. `/usr/bin/sudo` by absolute path, or `sudo -n`, never reaches the helper. As root `StartAskpass` is a no-op (app.go:531). `engine.NeedsSudo` says what *may* need sudo; most casks do not.
- One failed action ends its component: a refused cask means that component's links and seeds never ran (`internal/engine/apply.go:99-108`).
- Secrets: `engine.MissingSecrets` gives names. sync filters needs-secret components out of `pending` (sync.go:115), so dependents of one run today. An `op://` value counts as set (`internal/action/secrets.go:66-69`); seed probing runs `op read` with no timeout (seed.go:44, app.go:165), so a signed-out `op` fails `Plan`.
- Leaks through text: a cmd's `name` is optional and `Label()` falls back to `Run` (cmd.go:19-24), so `run` text reaches `Summary` (engine.go:183,207) and errors; `Line` events carry `"$ " + Run` and command output; `op read failed: <output>` (app.go:171) and git output reach errors. `ErrLeak` keeps up to 8 characters of a match (`redact`, syncer.go), and gitleaks' default output has no file or rule.
- Children run in their own session (`internal/sh/sh.go:116`) and only `os.Interrupt` is handled (main.go:49): SIGTERM ends rigger with no record and leaves `brew` running.
- No lock exists: `Local.Save` and `Session.Save` write from a stale snapshot (app.go:304,317).
- `internal/skill/SKILL.md` "Running rigger" and "New config or package" are the rules to replace.

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully; **all tests must pass before the next task**; update this plan when scope changes
- conventions: consumer interfaces in `dependency.go` + mockgen; test helpers take `*testing.T` first and call `t.Helper()`; shared helpers in `internal/testutil`; e2e in `tests/`.

## Testing Strategy
- **unit (syncflow)**: a scripted `Asker`: every answer path `sync_test.go` covers today (pick none/some files, stray to Brewfile/ignore/later, apply some, drop one, prerequisite included, replaced link adopt/keep/leave), and conflict, uncommitted and no-answer errors unchanged.
- **unit (json)**: golden records in `cmd/rigger/testdata/json/`; stdout is exactly one JSON value on success, on every error, on flag errors and on usage errors; an unnamed cmd holding a split token (`"ghp_" + "…"`) is scrubbed from `summary`, `steps`, `error` and `person`; no secret value anywhere.
- **unit (rounds, `cmd/rigger`, with an injected env so a fake brew works on both OSes)**: strays, then files, then incoming; `later` strays not asked again within a sync; a file edited between rounds is a new question; a new file between rounds is asked; a settled component's answer is stale and ignored; a repeat call after a kill at each point (after `AddToBrewfile`, after the commit, mid-apply); a changing layout file does not change a round's questions; an invalid value gives `invalid_answers` with the questions; push failure after a partial apply keeps `applied` and `person`.
- **unit (person)**: refused and failed → `sudo`; refused and done (`sudo x || true`) → a note; failed and not refused → `failed`; a dependent of each, a dependent of a missing secret; an `op` failure.
- **e2e** (`tests/`, stray-free): an agent's loop over a bare remote with no terminal and stdout and stderr captured together as well as apart: a partial commit, an incoming component, `done`; a cmd running `"$SUDO_ASKPASS" prompt` ends in `needs_person` with the rest applied and pushed; a missing secret likewise; a SIGTERM during a slow cmd leaves a record or a clean repeat, and no surviving child.

## Progress Tracking
- mark completed items with `[x]` immediately; ➕ for new tasks; ⚠️ for blockers

## Solution Overview
- **One flow, several front ends.** `internal/syncflow` holds the sequence; every question goes through `Asker`, progress through `Reporter`. Prompts are today's code behind `Asker`. The JSON front end is an `Asker` that answers from `--answers` and stops the flow with `ErrNeedAnswers` when it has none.
- **Stop means stop.** The flow returns at the first step with an unanswered question; everything before it has happened (a commit stays made, a stray's Brewfile line stays written, a changed mind is not undone), everything after has not. The next call re-derives its place from the repo.
- **Answers accumulate, per item.** Each call carries all answers so far, each keyed by one item: a file with its content hash, a component, a stray, a link. A settled item goes stale and is ignored; an edited or new item is a new question. Nothing left out is confused with something new.
- **`--json` is a mode.** `run` pre-scans the args and moves human output off stdout: progress goes to a run log whose path is in every record, stderr carries at most one line per step, and one writer emits the record once, after the command returns, whatever happened. It never reads a prompt and never needs a terminal. `--json` with `--yes` is an error.
- **One scrub.** The record writer passes every string through the credential patterns, URL userinfo removal and the known secret values. `Line` text never enters a record.

## Technical Details

### Record
```json
{"v": 1, "command": "sync", "state": "needs_answers", "log": "~/.local/state/rigger/logs/….log", "questions": [ … ]}
```
- `state`: `done`, `needs_answers`, `needs_person`, `error`. Every record has `v`, `command`, `state`, and `log` once a run log exists.
- exit codes: 0 `done`, 1 `error`, 3 `needs_answers`, 4 `needs_person`. `status --json` exits 0 when it could read the state and says what is pending in the record; its text form keeps exit 1. A panic stays exit 2 with no record, and SIGKILL leaves none: the skill says output that is not one JSON value is a failure.
- errors: `{"state":"error","error":{"kind":"conflict","message":"…","files":[…],"fix":["git -C … pull --rebase", …]}}`. Kinds: `conflict`, `uncommitted`, `rebase_in_progress`, `no_answer` (ssh or network timed out), `leak`, `off_default_branch`, `no_host`, `no_profile`, `invalid_answers` (with `questions`), `busy`, `usage`, `other`. `fix` is for the person: the skill says show it, do not run it. `leak` holds `file` and `rule`, never a snippet (`LeakError{File, Rule}`; gitleaks through its JSON report).
- `error` after work has happened keeps `applied` and `person`.

### sync rounds
```
$ rigger sync --json
{"v":1,"command":"sync","state":"needs_answers","step":"strays","questions":[
  {"id":"stray:brew:gifski","kind":"stray","text":"Highest-quality GIF encoder",
   "options":[{"value":"apps","file":"brew/apps.Brewfile","profiles":["personal"],"here":true},
              {"value":"ignore"},{"value":"later"}]}]}                                     # exit 3
$ rigger sync --json --answers '{"stray:brew:gifski":"apps"}'
{"…","step":"files","questions":[
  {"id":"file:brew/apps.Brewfile@9f3a1c2","kind":"file","path":"brew/apps.Brewfile","code":" M","options":["commit","leave"]},
  {"id":"file:zsh/.zshrc@77d0e41","kind":"file","path":"zsh/.zshrc","code":" M","options":["commit","leave"]}]}
$ rigger sync --json --answers '{"stray:brew:gifski":"apps","file:brew/apps.Brewfile@9f3a1c2":"commit","file:zsh/.zshrc@77d0e41":"leave"}'
{"…","step":"incoming","questions":[
  {"id":"component:dev","kind":"component","title":"Dev toolchains","why":"in profile personal, which this machine uses",
   "steps":["install node@22 (brew/dev.Brewfile)"],"options":["apply","drop","later"]},
  {"id":"link:~/.zshrc","kind":"replaced_link","options":["adopt","keep","leave"]}]}
$ rigger sync --json --answers '{…,"component:dev":"apply","link:~/.zshrc":"leave"}'
{"v":1,"command":"sync","state":"done","applied":["dev"],"left":["~/.zshrc"],"dropped":[],"uncommitted":["zsh/.zshrc"],"pushed":true,"ahead":0}   # exit 0
```
- Ids and values: `stray:<kind>:<name>` (a Brewfile value, `ignore`, `later`), `file:<path>@<short content hash>` (`commit`, `leave`), `component:<id>` (`apply`, `drop`, `later`), `link:<dst>` (`adopt`, `keep`, `leave`). A value outside its options is `invalid_answers`, with the current questions repeated.
- `--answers`: inline when the value starts with `{`, `-` for stdin.
- Layouts export only on a call with no `--answers`, so a running app cannot change the questions between rounds.
- `Asker.DecideIncoming` takes components and replaced links together, before anything is written or askpass starts. The prompt `Asker` still asks them in today's order, so its output does not change. A pending prerequisite of an applied id comes along and is reported; a `drop` that is someone's prerequisite is refused in `not_dropped`.
- `--no-push`, `--only`, `--profile`, `--host` are repeated each round; the skill says so. `pushed:false` with `ahead` says what did not go out.
- Guards are `error` records. Nothing to ask: one call goes straight to `done`.

### needs_person
```json
{"v":1,"command":"sync","state":"needs_person","pushed":true,"applied":["zsh"],
 "person":[
  {"component":"apps","needs":"sudo","run":"rigger apply --only apps","note":"its links and seeds did not run"},
  {"component":"tool-config","needs":"secret","keys":["TOOL_TOKEN"],"run":"add TOOL_TOKEN to ~/.config/rigger/secrets.env"},
  {"component":"broken","needs":"failed","error":"…","log":"~/.local/state/…"},
  {"component":"on-apps","needs":"component","on":"apps","run":"rigger apply --only on-apps"}]}
```
- With `--json` the askpass callback refuses and marks the running component, known from the `Started` event; the mark is guarded by a mutex, since askpass serves on its own goroutine. Marked and failed is `sudo`. Marked and done (`sudo x || true`) is `applied` with a note. Failed and unmarked is `failed`. `run` carries `--repo` and `--host` when they were given.
- Needs-secret components go to `Apply` so they emit Skipped and their dependents follow. Under `--json` a failed or slow `op read` (with a timeout) is a per-component `needs: secret`, not a failed plan.
- Unrelated components go on and the push still happens. No terminal-free way to give a password is added.

### status --json
`host`, `platform`, `repo`, `branch` (with `off_default`), `profile`, `profiles`, `host_file` (`add`, `remove`, `exclude_packages`, `include_packages`, `ignore_packages`), `components`: each `id`, `title`, `group`, `state`, `summary`, `needed_by`, `steps`; `missing_secrets` (names), `pending` (count).

### apply --json
`apply --json [--profile P] [--only a,b] [--dry-run] [--answers …]`. `--dry-run` gives the plan in the same shape as `status`. Without it: `link:` questions in rounds, then the run; `done` or `needs_person` with `applied`, `left`, and `log`. It needs no `--yes`.

### Readable steps
`ComponentStatus.Steps()`, one plain line per unsettled action, built from the probe: `install node@22 (brew/dev.Brewfile)` (every package named, 5 then "and N more"), `run "plugins": zsh/install-plugins.sh` (the cmd's name and `run`, cut to one line, `runs every time` when it has no `check`), `link ~/.zshrc → zsh/.zshrc`, `seed ~/.config/tool/config.toml`, `apt install …`. Reason in words: `in profile personal, which this machine uses`, `added to this machine in hosts/<host>.yaml`, `needed by <id>`. `status --json`, the JSON questions and the typed prompts use the same text.

### Repo lock
`~/.local/state/rigger/<repo-hash>.lock` with `syscall.Flock`, held for one call, not across rounds. Writers (`sync`, `apply`, `adopt`, the TUI's run) wait up to 10 s, then fail with `busy`.

## What Goes Where
- **Implementation Steps**: rigger code, tests, `internal/skill/SKILL.md`, `README.md`, `docs/setup-repo.md`, `CLAUDE.md`, `docs/plans/20261027-sync-tui.md`, `docs/plans/20261008-background-sync.md`.
- **Post-Completion**: a real sync on a machine, driven by an agent; the release.

## Acceptance Criteria
- Run: `make check`
- Run: `go test -count=1 ./tests/`
- With no terminal, an agent reaches `done` on a sync with a partial commit and an incoming component using only `--json` and `--answers`; the e2e test shows it, and the stray round's unit test covers strays.
- No record holds a secret value, a password or a credential snippet, including in `summary`, `steps`, `error` and `person`; the JSON tests show it.

## Implementation Steps

### Task 1: Move sync into internal/syncflow
**Files:**
- Create: `internal/syncflow/syncflow.go`
- Create: `internal/syncflow/dependency.go`
- Create: `internal/syncflow/syncflow_test.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `cmd/rigger/sync_test.go`

- [ ] `Run(ctx, deps, asker, report)`, `Asker` (`PickCommit`, `DecideStrays`, `DecideIncoming`), `Reporter` (`Step`, `Line`, `Event`)
- [ ] `cmd/rigger` keeps the guards and a prompt `Asker`/`Reporter` built from today's helpers; output stays byte-for-byte the same; askpass starts after `DecideIncoming`
- [ ] needs-secret components reach `Engine.Apply`, so their dependents are skipped
- [ ] write tests: scripted `Asker` for every path in Testing Strategy; a dependent of a missing secret
- [ ] run tests - must pass before task 2

### Task 2: Readable steps and reasons
**Files:**
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/engine_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/syncflow/syncflow.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `cmd/rigger/sync_test.go`

- [ ] `ComponentStatus.Steps()`; `Session.Origin` in words
- [ ] the typed prompt prints them under each item instead of `todo  1 to install`
- [ ] write tests: a missing package named, a pending cmd's `run`, a cmd with no `check`, a link, a seed, 6 packages cut; each reason; no `todo` in sync's prompt
- [ ] run tests - must pass before task 3

### Task 3: The JSON contract
**Files:**
- Create: `internal/pkg/jsonout/jsonout.go`
- Create: `internal/pkg/jsonout/jsonout_test.go`
- Create: `cmd/rigger/testdata/json/`
- Modify: `cmd/rigger/main.go`
- Modify: `cmd/rigger/main_test.go`
- Modify: `internal/syncer/syncer.go`
- Modify: `internal/syncer/syncer_test.go`

- [ ] record type, `v`, states, exit codes, error kinds; the scrub on every string
- [ ] `run` pre-scans for `--json`, sends human output away from stdout, writes one record after the command returns, also for flag and usage errors; `--json` with `--yes` refused
- [ ] `SIGTERM` joins `NotifyContext`; a cancelled run ends its children and still writes a record
- [ ] `LeakError{File, Rule}`, gitleaks through its JSON report
- [ ] `validate --json`
- [ ] write tests: golden records; one value on stdout on success, failure, a flag error and usage; the scrub with a split token; `leak` without a snippet
- [ ] run tests - must pass before task 4

### Task 4: status --json
**Files:**
- Modify: `cmd/rigger/status.go`
- Modify: `cmd/rigger/main_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/action/seed.go`

- [ ] the record in Technical Details, exit 0 when readable; text output and its exit 1 unchanged
- [ ] a failed or slow `op read` is a per-component secret need under `--json`
- [ ] write tests: golden record; a missing secret by name only; off the default branch; `no_host`; `no_profile`; signed-out `op`
- [ ] run tests - must pass before task 5

### Task 5: Repo lock
**Files:**
- Create: `internal/app/lock.go`
- Create: `internal/app/lock_test.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `cmd/rigger/apply.go`
- Modify: `cmd/rigger/adopt.go`
- Modify: `cmd/rigger/tui.go`

- [ ] the lock in Technical Details, released by the kernel when the process dies
- [ ] `busy` record; a text message for the other front ends
- [ ] write tests: a second writer waits and fails; a crashed holder does not block
- [ ] run tests - must pass before task 6

### Task 6: sync --json in rounds
**Files:**
- Create: `cmd/rigger/syncjson.go`
- Create: `cmd/rigger/syncjson_test.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `internal/syncflow/syncflow.go`
- Modify: `internal/syncer/syncer.go`

- [ ] an `Asker` that answers from `--answers` and returns `ErrNeedAnswers` with the step's questions; ids and values as in Technical Details; content hashes from `git hash-object`
- [ ] `--answers` inline or `-`; cumulative; stale ids ignored; `invalid_answers` carries the questions
- [ ] layouts export only without `--answers`; a run log for sync; `applied`, `left`, `dropped`, `not_dropped`, `uncommitted`, `pushed`, `ahead`
- [ ] guards and sync errors as `error` records; `rebase_in_progress` checked before the branch guard
- [ ] write tests: the rounds in Testing Strategy; `--no-push`
- [ ] run tests - must pass before task 7

### Task 7: needs_person
**Files:**
- Create: `cmd/rigger/person.go`
- Create: `cmd/rigger/person_test.go`
- Modify: `internal/syncflow/syncflow.go`
- Modify: `cmd/rigger/syncjson.go`

- [ ] the refusing callback with a guarded mark; failures, sudo, secrets and dependents collected into `person` with the exact command
- [ ] the push still happens; exit 4; a push failure keeps `applied` and `person`
- [ ] write tests: the person cases in Testing Strategy; values absent from the record
- [ ] run tests - must pass before task 8

### Task 8: apply --json and adopt --json
**Files:**
- Modify: `cmd/rigger/apply.go`
- Modify: `cmd/rigger/adopt.go`
- Modify: `internal/app/app.go`
- Modify: `cmd/rigger/main_test.go`

- [ ] `apply --json` with `--dry-run`, `--only`, `--profile`, `link:` rounds and `needs_person`
- [ ] `adopt --json`: `Session.Adopt` returns a result with the warning as a field; `adopted` per path
- [ ] write tests: dry run, a run, a question round, a person step; adopt of a seed and a link
- [ ] run tests - must pass before task 9

### Task 9: The agent's loop end to end
**Files:**
- Create: `tests/agent_test.go`

- [ ] e2e as in Testing Strategy, with stdout and stderr merged in one case
- [ ] run tests - must pass before task 10

### Task 10: Teach the skill and update the docs
**Files:**
- Modify: `internal/skill/SKILL.md`
- Modify: `internal/skill/skill_test.go`
- Modify: `cmd/rigger/main.go`
- Modify: `README.md`
- Modify: `docs/setup-repo.md`
- Modify: `docs/plans/20261027-sync-tui.md`
- Modify: `docs/plans/20261008-background-sync.md`
- Modify: `CLAUDE.md`

- [ ] `SKILL.md`: "Running rigger" becomes the loop: `status --json`, `sync --json`, answer, repeat, with the same flags each round; run it when the person asks; picks the request does not cover go to the person; `needs_person` and `error.fix` are shown to them, not run; output that is not one JSON value is a failure; a long call runs in the background with its output in a file; never `--yes`. "New config or package" follows
- [ ] usage text in `cmd/rigger/main.go`; `docs/setup-repo.md`: an "Agents" section with the record, states, exit codes and rounds; `README.md`: one line and a link
- [ ] `docs/plans/20261027-sync-tui.md`: ships as v0.3.0, `Asker` as here, Task 1 and the wording done here, its personal names removed; `docs/plans/20261008-background-sync.md`: the lock is done here
- [ ] `CLAUDE.md`: `internal/syncflow`, `internal/pkg/jsonout` in Layout; land order: this plan (v0.2.0), then the sync TUI (v0.3.0); the setup repo's owner and home path removed from its first line
- [ ] write tests: the skill's frontmatter and marker still hold
- [ ] run tests - must pass
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

- An agent runs a real sync on a machine through the skill: strays, a partial commit, an incoming component; check the picks and the commit it made.
- Release v0.2.0 (tag after master CI is green); upgrade on each machine and rerun `rigger skill install`, which replaces the marked file.
