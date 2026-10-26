# Release check before v0.1.0

## Overview
- Decide whether rigger can go public and ship `v0.1.0` to `k0nsta/homebrew-apps`.
- Linux was checked end to end on 2026-10-25 (below). macOS is checked in a later session on the Mac, with the steps in Task 2.
- Verdict on 2026-10-25: **not yet**. Fix F4 and F5 (Task 1), pass the Mac steps (Task 2), then release (Task 3).

## Linux results (2026-10-25, master 06707be)
- `make check` uncached: lint 0 issues, all packages pass, Debian container test ran (not skipped).
- Two Debian 13 containers as machines A and B, user `dev` with sudo, one bare repo as the remote, the README quickstart from `example/`:
  - validate, `init --no-run`, `apply --profile server --yes` (real apt), backup of an existing `~/.zshrc`, rerun says "nothing to do": pass
  - sync round trip A → B (host files, a `~/.zshrc` edit): pass
  - credential gate refuses a `ghp_` token: pass
  - conflicting edits on A and B: pull aborted, no markers, no rebase dir, B's edit kept: pass (message: F5)
  - branch guard: `sync` refuses on `feat/x`, `status` warns: pass
  - seed with a missing secret is skipped; with `secrets.env` it renders; `adopt` puts `${TOOL_TOKEN}` back, secret never in git: pass
  - `validate` on a broken manifest: clear errors: pass
- GoReleaser v2.12.7 `check` and `release --snapshot --skip=publish`: 4 archives, cask with the quarantine postflight, version stamped: pass
- Not covered: `rigger remote` over ssh, `install.sh` against a published release, the TUI by hand.

## Findings

| id | severity | finding |
|---|---|---|
| F4 | high | A tool that replaces a linked file with a real one (`sed -i`, some editors' atomic save) stops tracking it silently. The next `sync --yes` relinks the repo version and moves the user's edit to a backup, printing only `link ~/.zshrc (differs)`. |
| F5 | medium | A pull conflict prints git's own hints (`git rebase --skip`), which no longer apply since rigger aborted, and no way to resolve it. |
| F1 | low | `init` succeeds with an empty checkout when the remote's HEAD points to a missing branch; it should fail when `manifest.yaml` is missing. |
| F7 | high | (found by the VM matrix, 2026-10-26) `sync` writes a stray cask from a third-party tap by its bare token (`cask "goreleaser"`, tap `goreleaser/tap`): a fresh Mac has no such cask, so the whole Brewfile fails. Fixed: the stray takes its tap from the cask's install receipt (`Caskroom/<token>/.metadata/INSTALL_RECEIPT.json`; `brew info` fails as a whole when one tap is untrusted, Homebrew 7); machine-setup's `brew/dev.Brewfile` line removed (the formula is listed). |
| F12 | medium | (2026-10-26) "commit which?" answered `none` with local changes: the pull fails with git's "cannot pull with rebase: You have unstaged changes"; sync should say the changes must be committed or stashed first. |
| F8 | cosmetic | (2026-10-26) the secret screen's hint is cut at the window edge, not wrapped. |
| F9 | cosmetic | (2026-10-26) the finish screen counts components that were already ok as "done" (4 done after "3 component(s) will change"). |
| F10 | cosmetic | (2026-10-26) a package unticked in the host file shows no install state when ticked again in the TUI. |
| F11 | cosmetic | (2026-10-26) one untrusted tap fails the batched `brew info`, so every stray cask loses its description. |
| F6 | low | A missing secret is named but not where to put it (`~/.config/rigger/secrets.env`, or `op://` in it). |
| F2 | cosmetic | apt's raw progress fills the apply output. |
| F3 | cosmetic | `log:` path printed in full, not with `~`. |

## Implementation Steps

### Task 1: Fix findings
- [x] F4: in `sync`, a link that became a real file with other content is not relinked silently: interactive asks adopt / keep repo (backup); `--yes` leaves it and warns with `rigger adopt <path>`; a relink prints the backup path
- [x] F5: conflict message says rigger aborted and nothing changed, names the conflicting files, and how to resolve (`git -C <repo> pull --rebase`, fix, `git rebase --continue`, then `rigger sync`)
- [x] F1: `init` fails when the checkout has no `manifest.yaml`
- [x] F6: missing-secret message names `~/.config/rigger/secrets.env`
- [x] F2, F3 if cheap (F2: `apt-get -qq -o Dpkg::Use-Pty=0`; F3: `log:` path with `~`)
- [x] F7: a tap cask stray keeps its tap (install receipt); an ignored tap cask matches by its token
- [x] tests for each; `make check` passes

### Task 2: macOS checks (on the Mac)
Build first: `git pull && make build` in the rigger repo; use `bin/rigger`.

Fresh Mac (tart VMs, see `tests/vm/run.sh`):
- [x] `tests/vm/run.sh golden` once, then `tests/vm/run.sh --repo ~/Dev/github.com/machine-setup slim=slim personal=personal random:1 random:2`
- [x] every case: first run installs, second run is a no-op; results in `tests/vm/.work/results/` (2026-10-26: slim, random-2 pass; personal, random-1 failed on F7, pass against machine-setup with the cask line removed)

TUI and sync on the real Mac, with a throwaway HOME so the real setup is untouched (Homebrew is shared, so the example's `jq`, `ripgrep`, `git` and Fira Code may get installed):
- [x] `export H=$(mktemp -d); cp -R example $H/src; git -C $H/src init -q -b main; git -C $H/src add .; git -C $H/src commit -qm init`
- [x] `HOME=$H bin/rigger init $H/src`: host screen, `laptop` profile, enter on Core CLI, untick `jq`, review, enter; finish screen lists what ran
- [x] the sudo password is asked once (2026-10-26: nothing in the example needed sudo on a Mac that has it all; checked with a `cmds` entry running `sudo -k && sudo true`; the prompt now shows nothing while typing)
- [x] rerun `HOME=$H bin/rigger`, untick `git config`, `c`: review lists it under "Unpicked on this machine" with `~/.gitconfig`; enter saves `remove: [git]`
- [x] `brew install cowsay`, then `HOME=$H bin/rigger sync`: the stray shows its description and which profiles use each Brewfile; answer `i`; `ignore_packages: [cowsay]` lands in the host file; `brew uninstall cowsay`
- [x] `git -C $H/github.com/... switch -c feat/x` (or the clone path `init` printed): TUI header warns, `sync` refuses; switch back

Real machine (`mba`, `~/Dev/github.com/machine-setup`):
- [x] `bin/rigger status`: everything ok, vim not listed
- [x] `bin/rigger sync` with the 1Password SSH agent: approve the key, sync finishes; leave it unapproved: fails after 90 s with the "approve the SSH key" message (2026-10-26: mba has no 1Password agent; sync with its own key finished, `ed50a47`; the unanswered-ssh path is covered by `TestPullWithoutAnswerTimesOut`)
- [x] Wi-Fi off, `bin/rigger sync`: fails within ~15 s (ssh ConnectTimeout) with a clear message
- [x] agterm layouts export (`exported N layout file(s)`), nothing else unexpected in "local changes"

### Task 3: Release
- [ ] user creates `HOMEBREW_TAP_GITHUB_TOKEN` (fine-grained, Contents write on `k0nsta/homebrew-apps`, with expiry): `gh secret set HOMEBREW_TAP_GITHUB_TOKEN -R k0nsta/rigger`
- [ ] repo public: `gh repo edit k0nsta/rigger --visibility public --accept-visibility-change-consequences`; Actions runs again (public repos are not billed for standard runners)
- [ ] CLAUDE.md: replace "never tag `v*`" with the release steps
- [ ] tag `v0.1.0`, release workflow green, cask committed to `homebrew-apps`
- [ ] Mac: `brew install --cask k0nsta/apps/rigger`, `rigger version` says 0.1.0, runs without a quarantine dialog
- [ ] Linux: `curl -fsSL https://github.com/k0nsta/rigger/releases/latest/download/install.sh | sh`
