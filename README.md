# rigger

Set up and sync your machines from your own setup repo, with a TUI.

You keep a (usually private) **setup repo**: a `manifest.yaml`, Brewfiles, dotfiles and scripts.
rigger clones it with your machine's own credentials, shows what it will install and link,
lets you pick components and individual packages, installs missing dependencies, links
configs back to the repo, and keeps several machines in sync. It runs on macOS and Linux.

> Work in progress — see `docs/plans/` for the design.

## Install

```bash
curl -fsSL https://github.com/k0nsta/rigger/releases/latest/download/install.sh | sh
# or
brew install --cask k0nsta/apps/rigger
```

## Quick start

```bash
rigger init git@github.com:you/machine-setup   # clone your setup repo, then the TUI
rigger sync                                     # commit local changes, pull others', re-apply
rigger remote my-server --profile server        # set up a Linux host over ssh
```

`example/` is a minimal setup repo to start from.

## License

MIT
