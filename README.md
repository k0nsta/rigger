# rigger

Set up and sync your machines from your own setup repo, with a TUI.

You keep a (usually private) **setup repo**: `manifest.yaml`, Brewfiles, dotfiles, scripts.
rigger clones it with your machine's own credentials, shows what it will install and link,
lets you pick components and single packages, installs what is missing, links configs back
to the repo, and keeps every machine in sync. macOS and Linux.

## Install

```bash
curl -fsSL https://github.com/k0nsta/rigger/releases/latest/download/install.sh | sh
# or, later on a Mac
brew install --cask k0nsta/apps/rigger
```

## New machine

```bash
# 1. copy ~/.ssh (or have an access token ready for an HTTPS URL)
# 2. clone your setup repo and run the installer
rigger init git@github.com:you/machine-setup     # clones to ~/github.com/machine-setup
```

The TUI then asks for a machine name, a profile to start from, lets you toggle components
(`enter` on a Brewfile opens its packages), shows the plan with decisions (adopt a file
into the repo or keep the repo's), asks for missing secrets, and runs.

## Every day

```bash
rigger                 # TUI for the remembered repo
rigger status          # what is in place, exit 1 if anything is missing
rigger apply --yes     # non-interactive, uses hosts/<name>.yaml
rigger sync            # commit your changes, pull other machines', re-apply, push
rigger adopt ~/.codex/config.toml   # take a live edit of a seeded file back into the repo
rigger remote myvps    # install rigger on an ssh host, clone the repo there, run the TUI
rigger validate .      # check a setup repo (use it in its CI)
```

Flags: `--repo DIR` (or `$RIGGER_REPO`), `--host NAME`, `--profile P`, `--only a,b` (one-off: leaves the host file alone once it exists), `--dry-run`.

## The setup repo

See [docs/setup-repo.md](docs/setup-repo.md) for the layout and the `manifest.yaml`
reference, and [example/](example) for a minimal repo to copy.

## Development

```bash
make check     # lint + tests (tests/ includes end-to-end runs and a Debian container when docker is up)
make build     # bin/rigger
```

Releases: push a `v*` tag; GoReleaser publishes darwin/linux × arm64/amd64 and the cask.

## License

MIT
