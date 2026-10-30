# Agentic first

## Overview
- An agent in a harness can steer rigger end to end: read state as JSON, get rigger's questions as JSON, answer them, and let rigger commit, pull, apply and push. Today output is text for people, an agent's only way through `sync` is `--yes` (commits every change, skips the package questions), and the agent skill tells agents not to run `sync` or `apply`.
- `--json` on `status`, `apply`, `sync`, `adopt`, `validate`, `skill install` and `version`: one record on stdout, progress on stderr, a versioned schema, stable exit codes.
- `sync --json` works in rounds: it runs until it needs an answer, exits with a `needs_answers` record, and the agent calls again with `--answers`. Nothing stays running.
- Ships as v0.2.0, before the sync TUI (now v0.3.0).

## Decisions
- The agent drives the whole loop; the person steps in only for sudo and secrets (2026-10-30).
- Rounds, not a held-open process or an MCP server: an agent's shell tool runs a command and reads what it printed. State comes from git, so a killed session loses nothing and a repeat call is safe (2026-10-30).
- Part of the work needing sudo or a secret: finish the rest, push, and end with `needs_person` listing each component, what it needs and the exact command. The sudo password and secret values never appear in a record (2026-10-30).
- No new permission setting: asking the agent to sync is the permission; the skill tells the agent to ask the person for any pick the request does not cover (2026-10-30).
- Agentic before the sync TUI: both need `internal/syncflow`, and JSON is the smaller front end (2026-10-30).

## Context (from discovery)
- `cmd/rigger/sync.go:23-105` `sync`: guards (host file, `repo.ReadHead` branch guard), `exportLayouts`, `handleStrays` (darwin), `pickFiles`, `sy.Commit`, `sy.Pull` (`ErrConflict`, `ErrUncommitted`, `ErrNoAnswer`), reopen session, `applyFromRepo` (`askApply`, `DropFromHost`, `decideReplacedLinks`), `Push`. Prompts and flow are one file; it refuses without a terminal or `--yes` (sync.go:33).
- `cmd/rigger/apply.go`: `--yes`, `--dry-run`, a y/n `confirm`; `status.go` exits 1 when anything is pending, the one machine signal today. No `--json` exists anywhere.
- `docs/plans/20261027-sync-tui.md` designs `internal/syncflow` (`Run(ctx, deps, asker, report)`, `Asker`, `Reporter`); its Task 1 moves here.
- Sudo: `Session.StartAskpass(ask)`; `terminalPassword` returns false without a terminal (apply.go:131), so `sudo` fails and the component fails. `engine.NeedsSudo` (engine.go:276) says what *may* need it (apt, `sudo: true` cmds, casks), but most casks do not; only the run shows. `engine.Event` tells which component is running.
- Secrets: `engine.MissingSecrets` gives key names; values stay in the secrets file.
- `internal/syncer/syncer.go` `redact` keeps up to 8 characters of a matched credential in `ErrLeak`'s message: fine for a person's terminal, not for a record an agent stores.
- `internal/skill/SKILL.md` "Running rigger" is the rule to replace.

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully; **all tests must pass before the next task**; update this plan when scope changes
- conventions: consumer interfaces in `dependency.go` + mockgen; test helpers take `*testing.T` first and call `t.Helper()`; shared helpers in `internal/testutil`; e2e in `tests/`.

## Testing Strategy
- **unit (syncflow)**: a scripted `Asker`: every answer path `sync_test.go` covers today (pick none/some files, stray to Brewfile/ignore/later, apply some, drop one, prerequisite included, replaced link adopt/keep/leave), and conflict, uncommitted and no-answer errors unchanged.
- **unit (json)**: each command's record against golden files; stdout parses as exactly one JSON value on success and on every error; progress only on stderr; `v` present; no secret value, no password and no credential snippet in any record.
- **unit (rounds)**: each stop and resume: strays, then commit, then incoming; `later` strays not asked again within a sync; a repeat call after a killed run; a stale answer id ignored; an answer outside a question's options refused with the question repeated.
- **e2e** (`tests/`): an agent's loop over a bare remote with no terminal: `status --json`, `sync --json` until `done`, the remote holds the picked commit and the other edit stays; a `sudo: true` cmd ends in `needs_person` with the rest applied and pushed; a fake missing secret likewise.

## Progress Tracking
- mark completed items with `[x]` immediately; ➕ for new tasks; ⚠️ for blockers

## Solution Overview
- **One flow, several front ends.** `internal/syncflow` holds the sequence; every question goes through `Asker`, progress through `Reporter`. Prompts are today's code behind `Asker`. The JSON front end is an `Asker` that answers from `--answers` and stops the flow with `ErrNeedAnswers` when it has none.
- **Stop means stop.** The flow returns at the first step with an unanswered question; everything before it has happened (a commit stays made), everything after has not. The next call re-derives its place from the repo: nothing left to commit, then the pull, then the incoming list.
- **Answers accumulate.** Each call carries all answers so far. A `later` stray has no trace in the repo, so without this it would be asked every round. An answer whose id no longer applies is ignored.
- **`--json` is a mode, not a format flag.** It never reads stdin for a prompt and never needs a terminal; progress goes to stderr; stdout is one record whatever happens. `--json` with `--yes` is an error: the agent says what it wants.

## Technical Details

### Record
```json
{"v": 1, "command": "sync", "state": "needs_answers", "questions": [ … ]}
```
- `state`: `done`, `needs_answers`, `needs_person`, `error`. Every record has `v`, `command`, `state`.
- exit codes: 0 `done`, 1 `error`, 3 `needs_answers`, 4 `needs_person`. `status --json` exits 0 when it could read the state, and says what is pending in the record (its text form keeps exit 1).
- errors: `{"state":"error","error":{"kind":"conflict","message":"…","files":[…],"fix":["git -C … pull --rebase", …]}}`. Kinds: `conflict`, `uncommitted`, `no_answer`, `offline`, `leak`, `off_default_branch`, `no_host`, `invalid_answers`, `other`. `leak` names the file and the rule, never a snippet.

### sync rounds
```
$ rigger sync --json
{"v":1,"command":"sync","state":"needs_answers","step":"strays","questions":[
  {"id":"stray:brew:gifski","kind":"stray","text":"Highest-quality GIF encoder","options":["apps","core","ignore","later"]}]}     # exit 3
$ rigger sync --json --answers '{"stray:brew:gifski":"apps"}'
{"…","step":"commit","questions":[{"id":"commit","kind":"files","options":["brew/apps.Brewfile","zsh/.zshrc"]}]}
$ rigger sync --json --answers '{"stray:brew:gifski":"apps","commit":["brew/apps.Brewfile"]}'
{"…","step":"apply","questions":[
  {"id":"apply","kind":"components","items":[{"id":"dev","title":"Dev toolchains","why":"in profile personal",
     "steps":["install node@22 (brew/dev.Brewfile)"]}],"options":["dev"]},
  {"id":"link:~/.zshrc","kind":"replaced_link","options":["adopt","keep","leave"]}]}
$ rigger sync --json --answers '{…,"apply":["dev"],"link:~/.zshrc":"leave"}'
{"v":1,"command":"sync","state":"done","committed":1,"applied":["dev"],"pushed":true}      # exit 0
```
- `--answers`: inline when the value starts with `{`, `-` for stdin, else a file path.
- `apply` answer: ids to apply; `drop` (ids) writes `remove` to the host file as `-N` does. A pending prerequisite of a picked id comes along, reported in the record.
- `--no-push` keeps working; a `pushed:false` record says so.
- Nothing to ask: one call goes straight to `done`.
- Guards are `error` records: off the default branch (`off_default_branch` with the fix text), no host file (`no_host`).

### needs_person
```json
{"v":1,"command":"sync","state":"needs_person","pushed":true,"applied":["dev","zsh"],
 "person":[
  {"component":"apps","needs":"sudo","run":"rigger apply --only apps"},
  {"component":"tool-config","needs":"secret","keys":["TOOL_TOKEN"],"run":"add TOOL_TOKEN to ~/.config/rigger/secrets.env"},
  {"component":"needs-apps","needs":"component","on":"apps","run":"rigger apply --only needs-apps"}]}
```
- With `--json` the askpass callback answers "no" and marks the running component (from the `Started` event) as needing sudo. The component fails, is reported under `person`, and its dependents follow with `needs: component`. Unrelated components go on; the push still happens.
- A missing secret is known before the run: its component is skipped as today and listed.
- No terminal-free way to give a password is added.

### status --json
`host`, `platform`, `repo`, `branch` (with `off_default`), `profile`, `profiles`, `host_file` (`add`, `remove`, `exclude_packages`, `include_packages`, `ignore_packages`), `components`: each `id`, `title`, `group`, `state`, `summary`, `needed_by`, `steps`; `missing_secrets` (names), `pending` (count).

### apply --json
`apply --json [--profile P] [--only a,b] [--dry-run] [--answers …]`. `--dry-run` gives the plan in the same shape as `status`. Without it: replaced-link and both-changed-seed questions in rounds as in sync, then the run; `done` or `needs_person` with `applied`, `failed`, `skipped`, and `log` (path with `~`). It needs no `--yes`.

### Readable steps
`Incoming.Steps`, one plain line per unsettled action, built from the probe: `install node@22 (brew/dev.Brewfile)` (every package named, 5 then "and N more"), `run "agentd checkout": git clone …` (the cmd's name and `run`, cut to one line), `link ~/.zshrc → zsh/.zshrc`, `seed ~/.config/tool/config.toml`, `apt install …`. Reason in words: `in profile personal, which this machine uses`, `added to this machine in hosts/<host>.yaml`, `needed by <id>`, plus the pulled commit that changed it when there is one. `status --json`, the JSON questions and the typed prompts all use them.

## What Goes Where
- **Implementation Steps**: rigger code, tests, `internal/skill/SKILL.md`, `README.md`, `docs/setup-repo.md`, `CLAUDE.md`, `docs/plans/20261027-sync-tui.md`.
- **Post-Completion**: a real sync on a machine, driven by an agent; the release.

## Acceptance Criteria
- Run: `make check`
- Run: `go test -count=1 ./tests/`
- With no terminal, an agent reaches `done` on a sync with a stray, a partial commit and an incoming component, using only `--json` and `--answers`; the e2e test shows it.
- No record holds a secret value, a password or a credential snippet; the JSON tests show it.

## Implementation Steps

### Task 1: Move sync into internal/syncflow
**Files:**
- Create: `internal/syncflow/syncflow.go`
- Create: `internal/syncflow/dependency.go`
- Create: `internal/syncflow/syncflow_test.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `cmd/rigger/sync_test.go`

- [ ] `Run(ctx, deps, asker, report)`, `Asker` (`PickCommit`, `DecideStrays`, `PickIncoming`, `DecideReplaced`), `Reporter` (`Step`, `Line`, `Event`), as in the sync TUI plan
- [ ] `cmd/rigger` keeps the guards and a prompt `Asker`/`Reporter` built from today's helpers; output stays byte-for-byte the same
- [ ] write tests: scripted `Asker` for every path in Testing Strategy
- [ ] run tests - must pass before task 2

### Task 2: Readable steps and reasons
**Files:**
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/engine_test.go`
- Modify: `internal/syncflow/syncflow.go`
- Modify: `cmd/rigger/sync.go`

- [ ] `ComponentStatus.Steps()` and the reason in words; the pulled commits that touched a component's manifest entry, Brewfile or srcs
- [ ] the typed prompt prints them under each item instead of `todo  1 to install`
- [ ] write tests: a missing package named, a pending cmd's `run`, a link, a seed, 6 packages cut; each reason; no `todo` in sync's prompt
- [ ] run tests - must pass before task 3

### Task 3: The JSON contract
**Files:**
- Create: `internal/jsonout/jsonout.go`
- Create: `internal/jsonout/jsonout_test.go`
- Modify: `cmd/rigger/main.go`
- Modify: `cmd/rigger/main_test.go`
- Modify: `cmd/rigger/skill.go`

- [ ] record type, `v`, states, exit codes, error kinds; one writer that guarantees one value on stdout, also when a command fails
- [ ] `--json` on `version`, `validate`, `skill install`; `--json` with `--yes` refused
- [ ] write tests: golden records; stdout parses as one value on success and failure; progress on stderr
- [ ] run tests - must pass before task 4

### Task 4: status --json
**Files:**
- Modify: `cmd/rigger/status.go`
- Modify: `cmd/rigger/main_test.go`

- [ ] the record in Technical Details, exit 0 when readable; text output and its exit 1 unchanged
- [ ] write tests: golden record; a missing secret by name only; off the default branch; no host file
- [ ] run tests - must pass before task 5

### Task 5: sync --json in rounds
**Files:**
- Create: `cmd/rigger/syncjson.go`
- Create: `cmd/rigger/syncjson_test.go`
- Modify: `cmd/rigger/sync.go`
- Modify: `internal/syncflow/syncflow.go`

- [ ] an `Asker` that answers from `--answers` and returns `ErrNeedAnswers` with the step's questions; stable ids (`stray:<kind>:<name>`, `commit`, `apply`, `link:<dst>`)
- [ ] `--answers` inline, `-` or a file; cumulative; stale ids ignored; an answer outside the options is `invalid_answers`
- [ ] the flow stops at the first unanswered step and resumes from the repo's state
- [ ] guards and sync errors as `error` records; `leak` without a snippet
- [ ] write tests: the rounds in Testing Strategy; a repeat call after a stop at each step; `--no-push`
- [ ] run tests - must pass before task 6

### Task 6: needs_person
**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/syncflow/syncflow.go`
- Modify: `cmd/rigger/syncjson.go`
- Modify: `cmd/rigger/syncjson_test.go`

- [ ] with `--json` the askpass callback refuses and marks the running component; failed-for-sudo, missing-secret and their dependents collected as `person` entries with the exact command
- [ ] the push still happens; exit 4
- [ ] write tests: a `sudo: true` cmd beside a link; a missing secret; a dependent; no password path added; values absent from the record
- [ ] run tests - must pass before task 7

### Task 7: apply --json and adopt --json
**Files:**
- Modify: `cmd/rigger/apply.go`
- Modify: `cmd/rigger/adopt.go`
- Modify: `cmd/rigger/main_test.go`

- [ ] `apply --json` with `--dry-run`, `--only`, `--profile`, rounds for replaced links and both-changed seeds, and `needs_person`
- [ ] `adopt --json`: `adopted` per path, with the warning about a secret placeholder as a field
- [ ] write tests: dry run, a run, a question round, a person step; adopt of a seed and a link
- [ ] run tests - must pass before task 8

### Task 8: The agent's loop end to end
**Files:**
- Create: `tests/agent_test.go`

- [ ] e2e with no terminal over a bare remote, as in Testing Strategy
- [ ] run tests - must pass before task 9

### Task 9: Teach the skill and update the docs
**Files:**
- Modify: `internal/skill/SKILL.md`
- Modify: `internal/skill/skill_test.go`
- Modify: `README.md`
- Modify: `docs/setup-repo.md`
- Modify: `docs/plans/20261027-sync-tui.md`
- Modify: `CLAUDE.md`

- [ ] `SKILL.md`: "Running rigger" becomes the loop: `status --json`, `sync --json`, answer, repeat; run it when the person asks; picks the request does not cover go to the person; `needs_person` is shown to them as is; never pass `--yes`
- [ ] `docs/setup-repo.md`: an "Agents" section with the record, the states, exit codes and the rounds; `README.md`: one line and a link
- [ ] `docs/plans/20261027-sync-tui.md`: ships as v0.3.0; Task 1 and the steps wording are done here
- [ ] `CLAUDE.md`: `internal/syncflow`, `internal/jsonout` in Layout; land order: this plan (v0.2.0), then the sync TUI (v0.3.0)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

- An agent runs a real sync on a machine through the skill: strays, a partial commit, an incoming component; check the picks and the commit it made.
- Release v0.2.0 (tag after master CI is green); upgrade on each machine; `rigger skill install --force` is not needed, the marked file is replaced.
