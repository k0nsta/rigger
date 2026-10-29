# rigger

Go + Bubble Tea installer that applies and syncs a setup repo (the user's is `k0nsta/machine-setup`, cloned at `~/Dev/github.com/machine-setup`). Public since 2026-10-27; releases are `v*` tags (see Releases below). For a local binary use `make build` or `GOOS=darwin GOARCH=arm64 go build -trimpath ./cmd/rigger`.

## Layout

- `cmd/rigger`: subcommands (`init`, `apply`, `status`, `sync`, `adopt`, `remote`, `askpass`, TUI).
- `internal/manifest`: `manifest.yaml` schema and validation. `internal/engine`: probe → plan → apply. `internal/action`: links, seeds, Brewfiles, cmds.
- `internal/app`: session wiring (repo, state, secrets, askpass). `internal/syncer`: commit → pull --rebase → apply → push. `internal/tui`: the picker.
- `docs/demo/record.sh`: re-records the README GIF from `example/` in a throwaway HOME (expect types, asciinema records, agg renders; vhs needs a browser the command sandbox will not start). Check frames with `ffmpeg -i rigger.gif -fps_mode passthrough %04d.png`.
- `docs/setup-repo.md`: the setup-repo contract. `example/`: a minimal setup repo. `docs/plans/`: plans (`completed/` when done).
- `tests/`: end-to-end runs and a Debian container test (skipped unless docker is up: `colima start`); `tests/vm/run.sh`: tart fresh-Mac matrix (`golden` once, then cases; results in `tests/vm/.work/results/`). tart's Homebrew formula is broken under Homebrew 7: install it from the cirruslabs/tart GitHub release.

## Commands

```bash
make check    # golangci-lint + go test ./... + shellcheck install.sh (same as CI)
make build    # bin/rigger
```

## Model

- Catalog = `manifest.yaml` + `brew/*.Brewfile`; per-machine delta = `hosts/<name>.yaml` (`profile`, `add`, `remove`, `exclude_packages`, `include_packages`, `ignore_packages`).
- rigger never uninstalls; apps already present outside brew count as installed.
- The TUI shows a Brewfile package's description from the comment line right above it.

## Gotchas

- `brew` runs its children under `env -i` with an allowlist (`HOMEBREW_*`, `SUDO_ASKPASS`, `PATH`, `HOME`, `USER`…). Anything a brew child needs from rigger must be baked into a file, not passed as env (see `internal/askpass`).
- Homebrew 7 validates a whole tap on `brew tap`: one cask without a Linux url (and no `depends_on :macos`) fails the tap with "invalid syntax in tap!", so `brew bundle` fails on a fresh Mac and passes on a rerun. That was the agr cask in k0nsta/apps (fixed 2026-10-12). Check a tap with `brew readall --os=all --arch=all <tap>`. `Brew.Apply` also runs `brew trust --tap` before `brew bundle`.
- Homebrew resets sudo timestamps, so sudo goes through `SUDO_ASKPASS` → `rigger askpass` → the running rigger's socket.
- Linux CI pulls `debian:13` from Docker Hub; a registry timeout there is infra, not a code failure.

## Next

Land order of plans: `20261027-sync-tui` (v0.2.0) → `20261008-app-checklist` → `20261009-vault-and-machine-keys` → `20261008-background-sync`.

## Delivery board

Tracker: github — project https://github.com/users/k0nsta/projects/8 (Portfolio, shared by every repo; filter by Repository), repo k0nsta/rigger (the `delivery-board` skill)
Versions: 0.1 shipped #18, 0.1.2 #19, 0.2.0 #20
Plans: release-check #21 (done), skill-install #22 (0.1.2), sync-tui #23 (0.2.0); no version yet: app-checklist #24, vault-and-machine-keys #25, background-sync #26, host-vars #27

## Releases

- Tag `vX.Y.Z` on master after CI is green; `.github/workflows/release.yml` runs GoReleaser: darwin/linux × arm64/amd64, `install.sh` on the release, cask in `k0nsta/homebrew-apps` (needs the `HOMEBREW_TAP_GITHUB_TOKEN` repo secret, a fine-grained token with Contents write on that tap only).
- Check after a release: `brew install --cask k0nsta/apps/rigger` on a Mac, `install.sh` on Linux, `rigger version`.
- History was rewritten on 2026-10-12; the pre-cleanup repo with PRs #1–#24 is the private `k0nsta/rigger-archive`.
- Plans here are `docs/plans/YYYYMMDD-<slug>.md`, without the `PMC-0` prefix used in other repos.
- Token-shaped test fixtures stay split (`"ghp_" + "…"`) so nothing in the source looks like a real token; `gitleaks git .` must stay clean.
