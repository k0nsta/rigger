# Skill for agent sessions

## Overview
- `rigger skill install [--force]` writes one Agent Skills file, `SKILL.md`, for Claude Code (`~/.claude/skills/rigger/`) and Codex (`~/.agents/skills/rigger/`). It tells an agent working in a setup repo how rigger expects that repo to be changed.
- Why: agent sessions edited `machine-setup` with plain git. Work in progress from a feature branch reached master (`agent-board-tree`, 2026-10-22), and a staged `git rm` broke `sync` (fixed in v0.1.1, PR #14).
- First, a fix the skill depends on: a rigger command run inside a worktree (or any other checkout) of the setup repo silently relinks `~` into it. The skill sends agents to worktrees, so rigger must keep using the remembered repo there.
- Ships as v0.1.2, before v0.2.0 (sync TUI).

## Decisions
- Skill only, no hook; a plain `SKILL.md` for both agents, no plugin layout (2026-10-28). A hook can come later as a rewrite of the folder rigger owns.
- v0.1.2 on its own (2026-10-28).
- A `manifest.yaml` found from the working directory replaces the remembered repo only when the remembered one is gone (moved repo); otherwise rigger uses the remembered repo and says so. `--repo`/`$RIGGER_REPO` still choose any repo.

## Context (from discovery)
- `internal/repo/discover.go:17-40` `Discover(flag, env, cwd, remembered)`: flag, env, then the first parent of cwd with `manifest.yaml`, then the remembered repo. `internal/app/app.go:97,108-113` `prevRepo` makes the remembered path the "old location"; `internal/action/link.go:31-46` `movedFrom` relinks into the new root without backups; `Session.Save` (app.go:302) remembers the new path. Run from a worktree, every link moves there. The moved-repo feature (README "Moved the setup repo?") needs this path when the remembered repo is gone.
- `cmd/rigger/main.go:22-36` usage (11-column layout), `run` dispatch; `init.go:32` returns usage as an error; `init.go:35-42` resolves an empty `c.home`.
- stepwise's `internal/skill` on this machine (`~/stepwise/internal/skill/skill.go`, commit aadbad6) is the reference for the mechanism: embedded file, marker line, check every path before writing, `ForeignError`, `--force`. Read it; the package here is written from this plan, not copied. `state.WriteFileAtomic` (`internal/state/state.go:106`) already writes through a temp file.
- Agent docs (checked 2026-10-28): Claude Code reads `~/.claude/skills/<name>/SKILL.md`; Codex reads `$HOME/.agents/skills` (https://learn.chatgpt.com/docs/build-skills.md); both need `name` and `description`.
- `machine-setup` links `~/.claude` to its `claude/` (`claude-config`): the Claude copy lands in the setup repo, sync offers it, other machines get it with their next sync. `~/.agents` is not linked: Codex on another machine needs `rigger skill install` there.
- What the skill must match: links and seeds and `adopt` for both (`docs/setup-repo.md`, app.go:476-524); a link a tool replaced with a real file (`sed -i`, atomic save; setup-repo.md "Sync"); the credential gate runs only inside `Syncer.Commit` (`internal/syncer/syncer.go`); `sync`/`apply` need a TTY or `--yes`, and `sync --yes` commits every change and skips strays (`cmd/rigger/sync.go`); secrets path follows `$XDG_CONFIG_HOME` (state.go:22-28) and `${NAME}` renders only for names in the seed's `secrets:`; every top-level dir must be used or `unmanaged`; rigger's own messages suggest `git switch`, `stash`, `pull --rebase` fixes (head.go:74-76, sync.go).

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully; **all tests must pass before the next task**; update this plan when scope changes
- conventions: test helpers take `*testing.T` first and call `t.Helper()`; shared helpers in `internal/testutil`; e2e in `tests/`.

## Testing Strategy
- **unit (discover)**: cwd in a second checkout with the remembered repo present → remembered repo, `Elsewhere` set; remembered repo gone → cwd repo (moved); flag/env win; cwd inside the remembered repo → it, also when cwd reaches it through a symlink (both sides compared after `filepath.EvalSymlinks`, so macOS `/var` vs `/private/var` prints no note).
- **unit (skill)**: frontmatter and marker; install into a temp HOME writes both; rerun identical; an older marked file replaced; a foreign file at either path refused and neither written, `--force` replaces; relative/empty HOME refused; `~/.claude` a symlink into a temp repo → file lands in the repo; a symlinked `SKILL.md` → the link's target is written, the link kept.
- **cli**: paths printed with `~`; repo note shown when the Claude path resolves inside the remembered repo (macOS `/var` → `/private/var` case), not for a sibling `repo-old`, not when `state.yaml` is unreadable; `rigger skill`, `rigger skill x`, `rigger skill install extra` → usage error; foreign file error names the path with `~`.
- **e2e**: `TestWorktreeKeepsLiveLinks`: a `git worktree` of the live checkout, `rigger apply --yes` from inside it (`status` never relinks, so it cannot show the bug) → `~/.gitconfig` still links into the live checkout, `state.yaml` still names it, the note is printed; `TestRepoMoveRelinksWithoutBackups` still passes. One assertion added to an existing e2e: `skill install` in the machine's HOME writes both files.

## Progress Tracking
- mark completed items with `[x]` immediately; ➕ for new tasks; ⚠️ for blockers

## Solution Overview
- `repo.Discover` returns the remembered repo when it still holds `manifest.yaml` and the cwd candidate differs (both compared after `filepath.EvalSymlinks`), and names the skipped candidate. `app.Open` stores it in a new `Session.Elsewhere string`; `sessionFlags.open` in `cmd/rigger/main.go` prints `using the setup repo at <remembered> (this is another checkout; pass --repo to use it)` once to `c.err` when it is set. `app` writes nothing itself. Nothing relinks.
- `internal/skill`: embedded `SKILL.md`, `Marker`, `Paths(home)`, `Install(home, force)`, `ForeignError`; writes with `state.WriteFileAtomic` to the symlink-resolved file path. Same mechanism as stepwise's `Install`/`checkOwn`/`hasMarker`/`writeFile`, written here, not a shared module: `Install` first checks every path (a file without the marker is foreign → `ForeignError` naming it, nothing written unless `force`), then writes each.
- `cmd/rigger/skill.go`: `rigger skill install [--force]`, prints each path, plus the repo note when a written path resolves inside the remembered repo (both sides `EvalSymlinks`, then `filepath.Rel` without `..`).

## Technical Details

### SKILL.md
```markdown
---
name: rigger
description: Change a rigger setup repo (manifest.yaml, brew/*.Brewfile, hosts/*.yaml, the dotfiles it links) the way rigger expects. Use when editing files in a machine-setup repo, when a config under ~ is a symlink into such a repo, or when the person mentions rigger, sync, apply or adopt.
---
<!-- rigger: skill -->
```
Body, one short section each:
- **The checkout is live.** Its files are linked into `~`: an edit there is an edit to the running config. `rigger status` names the repo and what is in place.
- **Edit the repo file, not the `~` path.** Never `sed -i`/`perl -i` a linked path: it replaces the link with a real file. If a link became a real file, `rigger adopt <path>` takes it back and relinks it. Seeds (`seeds:` in the manifest) are rendered copies: edit the live file, then `rigger adopt <path>`.
- **Never move what the links point to, never throw away edits.** In the live checkout: no `git switch`/`checkout <branch>`, `stash`, `reset --hard`, `clean`, `rebase`, `pull`, `merge`, `cherry-pick`, `push --force`, `restore`/`checkout -- <file>` (those discard the person's uncommitted edits). When rigger prints a git fix (switch back, stash, finish a rebase), show it to the person; do not run it unless asked.
- **Feature work in a worktree.** `cd <repo> && git worktree add ../<repo>-<topic> -b <topic>`; push the branch, the person merges it on the remote, then runs `rigger sync`. rigger keeps using the live checkout when run inside a worktree.
- **Committing.** Edit in place and leave committing to `rigger sync` (it commits, pulls with rebase, applies what other machines changed, pushes). Staging is fine. A commit with a real message is fine when the person asks; run `gitleaks git --pre-commit --staged` first, because rigger's credential gate only checks what sync commits.
- **New config or package.** A new config: copy it into the repo, add a `links:` (or `seeds:`) entry to a component, `rigger validate .`, then the person runs `rigger apply`. A package: a line in a `brew/*.Brewfile` with a one-line comment above it (the TUI shows it), or the person installs it and `rigger sync` in a terminal offers it.
- **Validate.** After any change to `manifest.yaml` or a new top-level directory (it must be used by a component or listed in `unmanaged`): `rigger validate .`. A broken manifest stops every rigger command.
- **Secrets.** Never commit a token, key or password; never read or print the secrets file (`~/.config/rigger/secrets.env`, or under `$XDG_CONFIG_HOME`). A seed template refers to `${NAME}`, and `NAME` must be in that seed's `secrets:` list.
- **Running rigger.** Do not run `rigger sync`/`apply` unless the person asks: they commit, push and change the machine. Without a terminal they need `--yes`, and `sync --yes` commits every changed file and skips the package questions: check `git status` and tell the person first.

### Command
```
$ rigger skill install
~/.claude/skills/rigger/SKILL.md (in your setup repo: commit it with rigger sync)
~/.agents/skills/rigger/SKILL.md
```
- usage line: `  skill      install the agent skill for Claude Code and Codex (install, --force)`
- `--force` replaces a foreign file; without it a foreign file at any path leaves all paths as they were, and the error names it with `~`.
- `rigger skill`, an unknown subcommand, or extra arguments: `errors.New("usage: rigger skill install [--force]")`.
- empty `c.home` → `os.UserHomeDir`; `state.LoadLocal` failing or no remembered repo → no note, install still succeeds.
- a marked file the person edited is replaced without `--force`; with `~/.claude` synced, machines on different rigger versions rewrite it in turn: documented.

## What Goes Where
- **Implementation Steps**: rigger code, tests, `README.md`, `docs/setup-repo.md`, `CLAUDE.md`.
- **Post-Completion**: install on `mba`, sync it into `machine-setup`, release v0.1.2.

## Implementation Steps

### Task 1: Keep the remembered repo when run from another checkout
**Files:**
- Modify: `internal/repo/discover.go`
- Modify: `internal/repo/discover_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `cmd/rigger/main.go`
- Modify: `cmd/rigger/main_test.go`
- Modify: `tests/e2e_test.go`

- [ ] `Discover` returns the remembered repo when it holds `manifest.yaml` and differs from the cwd candidate, and reports the skipped candidate (e.g. a second return value or a `Found{Dir, Elsewhere}`)
- [ ] `Session.Elsewhere` set by `app.Open`; `sessionFlags.open` prints the "another checkout" line once to `c.err`; `prevRepo` then sees no move
- [ ] write tests: the discover cases in Testing Strategy (`internal/repo/discover_test.go`), `Session.Elsewhere` in `app_test.go`, the note on `c.err` in `main_test.go`
- [ ] write e2e `TestWorktreeKeepsLiveLinks` as in Testing Strategy (`apply --yes` from the worktree); `TestRepoMoveRelinksWithoutBackups` unchanged and passing
- [ ] run tests - must pass before task 2

### Task 2: Skill package
**Files:**
- Create: `internal/skill/SKILL.md`
- Create: `internal/skill/skill.go`
- Create: `internal/skill/skill_test.go`

- [ ] `SKILL.md` as in Technical Details
- [ ] `Marker`, `Paths(home)`, `Install(home, force)`, `ForeignError`; resolve an existing `SKILL.md` symlink to its target before `state.WriteFileAtomic(target, data, 0o644)`
- [ ] write tests: the skill cases in Testing Strategy
- [ ] run tests - must pass before task 3

### Task 3: `rigger skill install`
**Files:**
- Create: `cmd/rigger/skill.go`
- Modify: `cmd/rigger/main.go`
- Modify: `cmd/rigger/main_test.go`
- Modify: `tests/e2e_test.go`

- [ ] dispatch `skill`; `newFlagSet("skill install", c.err)` with `--force`; extra args and missing/unknown subcommand → usage error; usage line as in Technical Details
- [ ] print paths with `~`; repo note by `EvalSymlinks` on both sides and `filepath.Rel` without `..`
- [ ] write tests (with `newCLI`, state written via `state.LocalPath(home)`): the cli cases in Testing Strategy
- [ ] add the e2e assertion: `skill install` writes both files in the machine's HOME
- [ ] run tests - must pass before task 4

### Task 4: [Final] Update documentation
**Files:**
- Modify: `README.md`
- Modify: `docs/setup-repo.md`
- Modify: `CLAUDE.md`

- [ ] `README.md` "Every day": `rigger skill install   # teach Claude Code and Codex how to change your setup repo`; the moved-repo note says it applies when the old checkout is gone
- [ ] `docs/setup-repo.md`: "Agent sessions" (what the skill says, both install paths, the linked `~/.claude` case, Codex needs the command on each machine, marker and `--force`); the discovery rule for other checkouts
- [ ] `CLAUDE.md`: `skill` in the subcommand list, `internal/skill` in Layout

## Acceptance Criteria
- Run: `go test ./internal/repo/ ./internal/app/ ./internal/skill/ ./cmd/rigger/`
- Run: `go test ./tests/ -run 'TestWorktreeKeepsLiveLinks|TestRepoMoveRelinksWithoutBackups'`
- Run: `make check`
- Run: `grep -q 'rigger skill install' README.md`
- Run: `grep -q 'Agent sessions' docs/setup-repo.md`
- Run: `grep -q 'internal/skill' CLAUDE.md`

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

- macOS: the Mac rigger session fetches the branch, runs `make check` and `rigger skill install` plus a worktree `apply --yes` on the Mac, and reports back over the agent bus (`/var` → `/private/var` is the macOS-only case).
- Move this plan to `docs/plans/completed/`.
- On `mba`: `rigger skill install`, then `rigger sync` commits `claude/skills/rigger/SKILL.md` to `machine-setup`; a new Claude session lists the skill, a Codex session finds it.
- Release v0.1.2 (tag after master CI is green); `brew upgrade --cask k0nsta/apps/rigger`.
