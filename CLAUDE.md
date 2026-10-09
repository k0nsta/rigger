# rigger

Go + Bubble Tea installer that applies and syncs a setup repo (the user's is `k0nsta/machine-setup`, cloned at `~/github.com/machine-setup`). The repo is **private** for now: never tag `v*` (GoReleaser would push a cask with private URLs into the public tap); ship binaries with `make build` or `GOOS=darwin GOARCH=arm64 go build -trimpath ./cmd/rigger`.

## Layout

- `cmd/rigger`: subcommands (`init`, `apply`, `status`, `sync`, `adopt`, `remote`, `askpass`, TUI).
- `internal/manifest`: `manifest.yaml` schema and validation. `internal/engine`: probe → plan → apply. `internal/action`: links, seeds, Brewfiles, cmds.
- `internal/app`: session wiring (repo, state, secrets, askpass). `internal/syncer`: commit → pull --rebase → apply → push. `internal/tui`: the picker.
- `docs/setup-repo.md`: the setup-repo contract. `example/`: a minimal setup repo. `docs/plans/`: plans (`completed/` when done).
- `tests/`: end-to-end runs and a Debian container test; `tests/vm/run.sh`: tart fresh-Mac matrix (never run yet).

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
- Homebrew 7 refuses untrusted third-party taps. `Brew.Apply` runs `brew trust --tap` before `brew bundle`; on the first fresh Mac (2026-10-09) it still failed and the tap had to be trusted by hand. Cause open: start from that Mac's `~/.local/state/rigger/logs/` (did the `brew trust --tap` line run?). Tap trust does cover its items, and survives `brew tap`. Candidate fix: Homebrew 7's bundle trusts natively with `tap "x/y", trusted: true`.
- Homebrew resets sudo timestamps, so sudo goes through `SUDO_ASKPASS` → `rigger askpass` → the running rigger's socket.
- Linux CI pulls `debian:13` from Docker Hub; a registry timeout there is infra, not a code failure.

## Next

Land order of plans: `20261008-app-checklist` → `20261009-vault-and-machine-keys` → `20261008-background-sync`. Before going public: fake-looking token fixtures, gitleaks over history.
