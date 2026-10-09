# Vault and machine keys

## Overview
- Today a new machine needs a hand-made bundle before rigger can run: SSH keys to reach GitHub and servers, `~/.ssh/config`, `~/.config/rigger/secrets.env`, app tokens. This plan makes that "step 0" part of rigger.
- **Vault**: files that must not be in git in the clear (`~/.ssh/config`, `secrets.env`, tool `.env` files…) live in the setup repo encrypted with [age](https://github.com/FiloSottile/age), unlocked with one passphrase, restored like seeds and re-encrypted by `sync`.
- **Machine keys**: every machine gets its own SSH key. An account machine registers its key on GitHub itself; a read-only (deploy) machine is enrolled from an account machine. Server access is granted and revoked from an enrolled machine, per machine.
- **Not carried**: private SSH keys (per-machine instead) and Claude chat sessions. AI memory syncs through the repo as plain files (setup-repo data change, Post-Completion).
- New-machine flow after this plan: install rigger → `rigger init <repo>` (HTTPS + one-time token, exists today) → passphrase → vault restored → apply → machine key → origin switches to SSH.

## Context (from discovery)
- `internal/repo/clone.go` `Clone`: private repos clone over HTTPS with a one-time token prompt (token never stored). `UseSSHOrigin` probes `ssh -T` with `BatchMode=yes`, `StrictHostKeyChecking=accept-new`; `cmd/rigger/init.go:58` calls it right after clone, `cmd/rigger/tui.go:56` after the run.
- `internal/action/seed.go`: `Seed{Src, Dst, Secrets}`; statuses ok / missing / update / local-edit / both-changed / needs-secret; `Apply` default branch writes `p.Rendered`; `Adopt` writes the live file into `Src`; last-render sha256 in `state.yaml` `Seeds`; backups via `Env.Backup` (dirs 0755, `action.go:77`).
- `internal/action/secrets.go`: `LoadSecrets` reads `secrets.env`; `Set` rewrites the whole values map to that file. Loaded once in `app.Open` (`app.go:87`).
- `internal/engine/engine.go`: `severity()` default 1; a probe error aborts the whole `Probe` (`engine.go:120`). `internal/engine/apply.go:55` skips only `NeedsSecret`; default decision for both-changed seeds is Keep (`apply.go:128`).
- `internal/app/app.go`: `Save` rebuilds the host file and carries only `IgnorePackages` (`app.go:299`); `Adopt` works on existing entries only.
- `cmd/rigger/sync.go`: commit → `pull --rebase` → re-plan/apply → push (`sync.go:49-63`); pending filter at `sync.go:77`.
- `internal/syncer/syncer.go` gate: `credentialPaths` + `credentialContent` regexes + `gitleaks git --pre-commit --staged`.
- `internal/sh/sh.go` `NewExec`: children inherit `os.Environ()` except PATH/HOME; PATH puts `~/go/bin`, `~/.local/bin` before `/usr/bin`.
- `internal/tui`: `scrSecret` screen asks for missing secrets before the run; the 🔒 overlay is fed only during a run (askpass, `update.go:423`); `replanThenRun` goes straight to the run (`update.go:398`); `dependency.go` + `mocks`.
- `cmd/rigger/init.go` `splitPositional`/`flagTakesValue` (`init.go:89-107`) decide which flags take values.
- go.mod: Go 1.26; `filippo.io/age` (+ `armor`) gives X25519 identities, scrypt passphrase recipients/identities, `Encrypt`/`Decrypt` (checked via context7); `golang.org/x/crypto/ssh` `ParseAuthorizedKey` for key validation.

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully; **all tests must pass before the next task**; update this plan when scope changes
- conventions: consumer interfaces in `dependency.go` + mockgen; test helpers take `*testing.T` first and call `t.Helper()`; shared helpers in `internal/testutil`; e2e in `tests/`.
- land after `20261008-app-checklist` (both touch `engine/apply.go`) and before `20261008-background-sync` (background mode treats `Locked` as "waiting", never prompts).

## Testing Strategy
- **unit**: crypto round trip, wrong passphrase, rewrap, rekey; identity store keyed by recipient with a fake `/usr/bin/security`; encrypted seed probe/apply/adopt; `Locked` everywhere; secrets overlay never persisted; env stripping; gate rules; pubkey validation; host sanitizing.
- **e2e** (`tests/vault_test.go`, `tests/keys_test.go`): fake `security`, `gh`, `ssh`, `ssh-keygen` on PATH (nothing touches the real Keychain or GitHub); non-interactive flow `init --no-run` → `vault unlock` (`RIGGER_VAULT_PASSPHRASE`) → `apply --yes`; two clones of a bare repo for sync conflicts. TUI unlock covered by unit tests.
- Linux CI (Ubuntu) uses the identity file store.

## Progress Tracking
- mark completed items with `[x]` immediately; ➕ for new tasks; ⚠️ for blockers

## Solution Overview
- **One vault identity per setup repo**: an age X25519 identity stored as `vault/identity.age`, encrypted to a scrypt passphrase (min 12 chars, age's default work factor); its recipient in `vault/recipient.txt`. Vault files are armored age encrypted to that recipient. `passwd` rewraps only `identity.age`; `rekey` makes a new identity and re-encrypts every `vault/*.age`. Both protect future files only: old files stay in git history with the old identity.
- **Unlocked identity cache**, keyed by the recipient: macOS Keychain item (`/usr/bin/security` by absolute path, `-U`, secret fed via `security -i` on stdin, never argv) and Linux `~/.config/rigger/identity-<recipient>` 0600. Any process of the user can read either; the threat model says so and relies on disk encryption. A cached identity whose recipient differs from `vault/recipient.txt` (after a rekey elsewhere) is dropped and the vault is `Locked`.
- **`Session.Unlock(passphrase)`** is the single entry point (CLI and TUI): unwraps the identity, caches it, sets the engine identity, decrypts `vault/secrets.env.age` into a separate **vault overlay** in `Secrets`. `Has`/`Get` consult local values, then the overlay, then `op://`; `Set` serializes only local values, so vault secrets never land in plaintext. `app.Open` loads a cached identity when present.
- **Vault files are encrypted seeds** (`encrypted: true`): probe decrypts instead of rendering; hashes are over plaintext. New status **`Locked`** (no identity, stale identity, or the file does not decrypt) is an action status, never a probe error, so one bad file does not stop the rest. `Locked` ranks next to needs-secret, is skipped by apply, and `Seed.Apply` refuses it. For encrypted seeds a both-changed or local-edit file is **never** decided by default: without an explicit choice nothing is written and no hash is recorded.
- **Adopt = re-encrypt**: `Seed.Adopt` on an encrypted seed encrypts the live file into `Src` and records its hash. `sync` uses the same function, so there is one way plaintext becomes an `.age` file.
- **Sync order for the vault**: fetch first; for each local-edit encrypted seed, if `@{u}` also changed its `.age`, decrypt the remote version and ask keep local / keep repo; otherwise re-encrypt. Then commit, pull, apply, push as today. Re-encrypt only when the plaintext hash changed (age output is random, so this is the no-churn rule).
- **Gate**: every staged path under `vault/` must be `recipient.txt` (parses as an X25519 recipient) or fully decode as armored age; anything else under `vault/` is refused. No other exemption is needed: `.age` names do not match `credentialPaths`, and armored base64 cannot match `credentialContent`; a test runs real gitleaks over an armored file to confirm.
- **Machine keys**: `rigger keys` creates `~/.ssh/id_ed25519` (comment `rigger:<host>`) when missing and refuses to treat a key as this machine's when its public part already appears in another `hosts/*.pub` (a copied shared key). Account machines register it with `gh ssh-key add` and commit `hosts/<host>.pub`. Deploy machines only generate and print `rigger keys enroll <host> --pubkey "<line>"`; an account machine runs it: validates the key, commits `hosts/<host>.pub`, adds the read-only deploy key on the setup repo and authorizes it on servers. `rigger keys` warns when `ssh -G github.com` does not resolve to `~/.ssh/id_ed25519`; it never edits the shared ssh config.
- **Enroll / revoke** (account machine, vault unlocked so `keys.authorize` aliases resolve): keys are parsed with `ssh.ParseAuthorizedKey`, options and multi-line input rejected, re-serialized, and sent over ssh stdin. Lines are matched and removed by key material (the `rigger:<host>` comment is only a fallback). Host names pass `sanitizeHost` and must equal the input. `revoke` removes the GitHub key or deploy key titled `rigger:<host>` (`gh auth refresh -s admin:public_key` on this machine only), the server lines and `hosts/<host>.pub`, and prints what it cannot do: revoke that machine's gh OAuth session, `vault rekey` + rotate secrets if the machine was lost.
- **Deploy hosts** never commit: `sync` = pull + apply. They should not receive the vault passphrase; their encrypted seeds stay `Locked` (profiles for them, like slim, carry no vault components).

## Technical Details

### Repo layout and manifest
```yaml
# manifest.yaml
keys:
  github: account                   # default: account | deploy | none
  authorize: [server-a, server-b]   # ssh aliases from the vaulted ssh config
components:
  - id: vault
    group: base
    seeds:
      - {src: vault/ssh-config.age, dst: ~/.ssh/config, encrypted: true}
      - {src: vault/tool-env.age, dst: ~/.example-tool/.env, encrypted: true}
```
```text
vault/identity.age      # X25519 identity, scrypt-encrypted to the passphrase
vault/recipient.txt     # age1… recipient
vault/secrets.env.age   # optional: secrets overlay, not a seed
vault/*.age             # armored files
hosts/<host>.yaml       # may set `github: deploy`
hosts/<host>.pub        # machine public key
```
- Validation: `encrypted: true` needs a cleaned `src` under `vault/` ending `.age` (no `..`) and `vault/recipient.txt`; no `secrets` on encrypted seeds; `vault/` is a known top-level dir; `keys.github` ∈ account|deploy|none.
- Encrypted seeds are written 0600; parent dirs created 0700 under `~/.ssh`; their backups go to 0700 dirs.
- `known_hosts` is not vaulted: not secret, changes on every machine, and would conflict constantly.

### Commands
- `rigger vault init` (refuses when `vault/identity.age` exists), `unlock`, `lock`, `status`, `passwd`, `rekey` (decrypt every `vault/*.age` first, abort on any failure, write temp files, rename at the end).
- `rigger vault add <path>`: resolves symlinks; refuses a path inside the repo, tracked now or in history (`git log --all -1 -- <rel>`), or already managed; writes `vault/<parent>-<base>.age` (refuses to overwrite) and prints the seed line to paste into the manifest. No manifest editing.
- `rigger keys`, `rigger keys enroll <host> [--pubkey "<line>"]`, `rigger keys revoke <host>`.
- `RIGGER_VAULT_PASSPHRASE`: read once, `os.Unsetenv` after, stripped by `sh.NewExec` from every child env; for tests/CI.

### Bootstrap order
1. `rigger init`: clone over HTTPS (token prompt); **no** `UseSSHOrigin` here any more.
2. TUI `scrUnlock` before the first probe when `vault/` exists and no valid identity is cached (3 tries; cancel leaves the vault locked); CLI: `rigger vault unlock`.
3. plan + apply (encrypted seeds restored; vault overlay feeds secrets).
4. after `tea.Program.Run` returns (or at the end of `apply`): `rigger keys` (interactive `gh auth login --web -s write:public_key` when needed), then `UseSSHOrigin`.

## What Goes Where
- **Implementation Steps**: rigger code, tests, `docs/setup-repo.md`, `example/`.
- **Post-Completion**: machine-setup migration, server enrollment, AI memory, retiring the AirDrop bundle.

## Implementation Steps

### Task 1: Vault crypto, identity store, passphrase env
**Files:**
- Create: `internal/vault/vault.go`, `internal/vault/store.go`, `internal/vault/dependency.go`
- Create: `internal/vault/vault_test.go`, `internal/vault/store_test.go`
- Modify: `internal/sh/sh.go`, `internal/sh/sh_test.go`
- Modify: `go.mod`, `go.sum`

- [ ] add `filippo.io/age`; `Init(passphrase)` (min 12), `Unwrap`, `Rewrap`, `Encrypt` (armored), `Decrypt`, `IsAgeArmored(data)` (full decode)
- [ ] store keyed by recipient: `/usr/bin/security -i` on stdin with `-U` (macOS), `identity-<recipient>` 0600 (Linux); `Load`, `Save`, `Delete`
- [ ] `sh.NewExec` drops `RIGGER_VAULT_*` from child envs
- [ ] write tests: round trip, wrong passphrase, short passphrase, rewrap, wrong identity, store fake (secret only on stdin, absolute path), Linux perms, child env has no `RIGGER_VAULT_PASSPHRASE`
- [ ] run tests - must pass before task 2

### Task 2: Manifest, host file and Save
**Files:**
- Modify: `internal/manifest/manifest.go`, `internal/manifest/validate.go`, `internal/manifest/manifest_test.go`
- Modify: `internal/state/state.go`, `internal/state/state_test.go`
- Modify: `internal/app/app.go`, `internal/app/app_test.go`

- [ ] `Seed.Encrypted`; top-level `Keys{GitHub, Authorize}`; host file `GitHub`
- [ ] validation rules from Technical Details (incl. `..` rejection)
- [ ] `Session.Save` carries `GitHub` like `IgnorePackages`
- [ ] write tests: parse/validate each rule; `github: deploy` survives `Save`
- [ ] run tests - must pass before task 3

### Task 3: Encrypted seeds and the `Locked` status
**Files:**
- Modify: `internal/action/action.go` (`Locked`), `internal/action/seed.go`, `internal/action/seed_test.go`
- Modify: `internal/engine/engine.go`, `internal/engine/apply.go`, `internal/engine/engine_test.go`
- Modify: `cmd/rigger/status.go`, `cmd/rigger/apply.go`, `cmd/rigger/sync.go`
- Modify: `internal/tui/styles.go`

- [ ] probe decrypts with the session identity; no/stale identity or decrypt failure → `Locked` with a detail (never a probe error)
- [ ] `severity` puts `Locked` with needs-secret; apply skip covers both; `Seed.Apply` refuses `Locked`; sync pending filter and TUI colour handle it; status/apply print "vault locked: run rigger vault unlock"
- [ ] encrypted both-changed/local-edit with no explicit decision: nothing written, no hash recorded
- [ ] write 0600, parents 0700 under `~/.ssh`, backups in 0700 dirs
- [ ] write tests: missing → restored; local-edit; update; both-changed without decision records nothing; `Locked` + Overwrite never writes; corrupt `.age` does not abort other components; stale identity → `Locked`; perms
- [ ] run tests - must pass before task 4

### Task 4: Unlock and the secrets overlay
**Files:**
- Modify: `internal/action/secrets.go`, `internal/action/secrets_test.go`
- Modify: `internal/app/app.go`, `internal/app/app_test.go`

- [ ] `Secrets.SetVault(map)`; lookup order local → vault → `op://`; `Set` writes local values only
- [ ] `Session.Unlock(passphrase)`; `Open` loads a cached identity matching `recipient.txt`
- [ ] write tests: vault-only key never written by `Set`; local beats vault; unlock mid-session shrinks `MissingSecrets`; mismatched cache ignored
- [ ] run tests - must pass before task 5

### Task 5: `rigger vault` commands
**Files:**
- Create: `cmd/rigger/vault.go`
- Modify: `cmd/rigger/main.go` (usage, dispatch), `cmd/rigger/init.go` (`flagTakesValue`, non-TTY hint mentions `vault unlock`)
- Modify: `cmd/rigger/main_test.go`
- Create: `tests/vault_test.go`

- [ ] `init`, `unlock`, `lock`, `status`, `passwd`, `rekey`, `add` as in Commands; prompts without echo
- [ ] write tests: each subcommand; `init` refuses an existing vault; `rekey` leaves every file decryptable by the new identity only; `add` refuses repo paths, symlinked repo paths, history hits, name collisions
- [ ] e2e: `init --no-run` → `vault unlock` (env) → `apply --yes` restores files 0600, `~/.ssh` 0700
- [ ] run tests - must pass before task 6

### Task 6: Adopt, sync and the gate
**Files:**
- Modify: `internal/action/seed.go` (`Adopt` branch), `internal/app/app.go` (`Adopt`, `ReencryptVault`), `cmd/rigger/adopt.go`
- Modify: `cmd/rigger/sync.go`, `internal/syncer/syncer.go`, `internal/syncer/syncer_test.go`
- Modify: `tests/vault_test.go`

- [ ] encrypted `Adopt` = encrypt live into `Src` + record hash; never plaintext in the repo
- [ ] sync: fetch → `ReencryptVault` (remote change → keep local / keep repo; `--yes` skips and reports) → commit → pull → apply → push
- [ ] gate: `vault/` paths must be `recipient.txt` or armored age
- [ ] write tests: adopt leaves valid age; no churn without edits; re-encrypt on edit; two-clone conflict prompts instead of a rebase failure; header-only fake refused; `recipient.txt` accepted; real gitleaks over an armored file passes
- [ ] run tests - must pass before task 7

### Task 7: TUI unlock screen
**Files:**
- Modify: `internal/tui/model.go`, `internal/tui/update.go`, `internal/tui/view.go`
- Modify: `internal/tui/dependency.go`, `internal/tui/mocks/` (regenerate)
- Modify: `cmd/rigger/tui.go`, `internal/tui/tui_test.go`

- [ ] `scrUnlock` before the first probe when the vault is locked; calls `Unlock`; 3 tries; skip leaves it locked
- [ ] write tests: unlock, retry, skip, no vault → screen not shown
- [ ] run tests - must pass before task 8

### Task 8: Machine keys — create and register
**Files:**
- Create: `internal/keys/keys.go`, `internal/keys/dependency.go`, `internal/keys/keys_test.go`
- Create: `cmd/rigger/keys.go`, `cmd/rigger/keys_test.go`
- Modify: `cmd/rigger/main.go`, `cmd/rigger/init.go` (drop `UseSSHOrigin`), `cmd/rigger/tui.go`, `cmd/rigger/apply.go`

- [ ] create the key when missing; refuse a key already in another `hosts/*.pub`
- [ ] account: `gh ssh-key add --title rigger:<host>` (login with `write:public_key` when needed) + commit `hosts/<host>.pub`; deploy: print the enroll command; none: skip
- [ ] warn when `ssh -G github.com` does not use `~/.ssh/id_ed25519`
- [ ] run after the TUI program exits and at the end of `apply`, then `UseSSHOrigin`
- [ ] write tests (fake `ssh-keygen`/`gh`/`ssh`): idempotent, existing key kept, shared key refused, each github mode, origin switch only after keys
- [ ] run tests - must pass before task 9

### Task 9: Enroll and revoke
**Files:**
- Modify: `internal/keys/keys.go`, `internal/keys/keys_test.go`, `cmd/rigger/keys.go`
- Modify: `go.mod` (`golang.org/x/crypto/ssh`)
- Create: `tests/keys_test.go`

- [ ] `enroll <host>`: validate key, commit `hosts/<host>.pub`, deploy hosts get `gh repo deploy-key add` (read-only), every `keys.authorize` alias gets the line via stdin (idempotent by key material)
- [ ] `revoke <host>`: remove server lines by key material, GitHub/deploy key by title (`gh auth refresh -s admin:public_key`), `hosts/<host>.pub`; continue on unreachable servers; print manual steps
- [ ] deploy hosts: `sync` pulls and applies only
- [ ] write tests: injected options / multi-line key refused, `../` host refused, idempotent enroll, revoke removes only that key, unreachable server reported, locked vault → clear error
- [ ] run tests - must pass before task 10

### Task 10: Contract docs and example
**Files:**
- Modify: `docs/setup-repo.md`, `example/manifest.yaml`
- Create: `example/vault/` (identity for a documented test passphrase, one armored fake file, `recipient.txt`)

- [ ] document layout, commands, unlock and caching, threat model (stolen unlocked laptop, readable cache, passphrase strength, history keeps old files, what rekey/passwd do), keys block, enroll/revoke, deploy hosts
- [ ] `rigger validate example` passes
- [ ] run tests - must pass before task 11

### Task 11: Verify acceptance criteria
- [ ] fresh HOME e2e: init → unlock → apply → vault restored → keys (fakes) → origin SSH
- [ ] sync after an edit commits exactly one re-encrypted file; without edits commits nothing; concurrent edits prompt
- [ ] revoke removes the machine from GitHub (fake) and servers (fake ssh) and nothing else
- [ ] run full test suite: `make check`

### Task 12: [Final] Update documentation
- [ ] README: step 0 flow, `vault` and `keys` commands
- [ ] move this plan to `docs/plans/completed/`

## Decisions
- Per-machine SSH keys; private keys are never stored in the repo, encrypted or not. (User, 2026-10-09.)
- Vault unlocked by a passphrase, identity stored encrypted in the repo; no 1Password at step 0. (User.)
- Claude chat sessions are not carried; AI memory syncs as plain files. (User.)
- Encrypted seeds reuse the seed action; no new action kind.
- One shared vault identity per repo; deploy hosts do not get the passphrase.
- `vault add` prints the manifest snippet instead of editing `manifest.yaml`.

## Open questions
- Per-machine age recipients (revoke one machine's vault access without a full rekey) — later, if one identity proves too coarse.
- Passphrase on new SSH keys: proposed yes on macOS (`ssh-keygen` on the TTY, never `-N`, then `ssh-add --apple-use-keychain`; shared config needs `IgnoreUnknown UseKeychain`), no on servers. `UseSSHOrigin` runs `BatchMode=yes`, so the key must be in the agent first. Decide before Task 8.

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**machine-setup migration** (run by the user; the agent never reads `~/.pgpass` or the pgcli config)
- `rigger vault init`; `rigger vault add` for `~/.ssh/config`, `~/.headless-browser/.env`, `~/.pgpass`, `~/.config/pgcli/config`, `~/.zshrc.local`, `~/.netrc`, `~/.docker/config.json` (as wanted); encrypt `secrets.env` as `vault/secrets.env.age`.
- `keys: {github: account, authorize: [<server aliases>]}`; `github: deploy` in the daughter's Mac host file; the shared ssh config uses `~/.ssh/id_ed25519` everywhere.
- `claude/.gitignore`: un-ignore `projects/*/memory/`.
- From an account machine: `rigger keys enroll <host>` for every existing machine, then remove the old shared `id_rsa` from servers and GitHub.

**Manual verification**
- Fresh-Mac run (VM harness or a new laptop) with only the passphrase and a one-time token: no AirDrop bundle.
