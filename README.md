# rigger

Set up a new Mac or Linux box from your own setup repo, pick what each machine gets in a TUI,
and keep every machine in sync.

![rigger init on a new Mac: name the machine, pick a profile, untick a package, review, run](docs/demo/rigger.gif)

## Why rigger

Your setup repo stays plain: Brewfiles, dotfiles and scripts, plus one `manifest.yaml` that
groups them into components and profiles. rigger adds what those files cannot do alone:

- **Pick per machine.** A profile (laptop, work, server) is the starting point; untick any
  component or single Brewfile package. The choice is saved as a small diff in
  `hosts/<machine>.yaml`, so later profile changes still reach every machine.
- **Links, not copies.** Configs are symlinked into the repo, so an edit on any machine is a
  git change (a tool that rewrites its own config gets a seeded copy instead). `rigger sync`
  commits it, pulls the other machines' changes and re-applies them.
- **Safe on a machine you already use.** It shows the plan first, backs up what it replaces,
  offers to adopt a live file into the repo, and never uninstalls anything.
- **Secrets stay out of git.** Templates take `${TOKEN}` from a local secrets file (1Password
  `op://` references work), and sync refuses to commit anything that looks like a credential.

| | rigger | chezmoi | dotbot | nix-darwin |
|---|---|---|---|---|
| Your files | plain Brewfiles and dotfiles | its source dir (`dot_` names, templates) | plain | Nix modules |
| Dotfiles | symlinked | copied (symlink mode optional) | symlinked | Nix (home-manager) |
| Packages | Brewfile / apt, per-package picks | your scripts | your shell commands | declarative |
| Per machine | profile + TUI picks in a host file | template conditions | one config per machine | Nix config per host |
| Edits on a machine | already in the repo; `rigger sync` | `chezmoi re-add` | already in the repo | edit Nix, rebuild |
| What you learn | one YAML file | its naming + Go templates | one YAML file | the Nix language |

## Install

```bash
curl -fsSL https://github.com/k0nsta/rigger/releases/latest/download/install.sh | sh
# or on a Mac
brew install --cask k0nsta/apps/rigger
```

## Quickstart

```bash
# 1. a private repo of your own, started from the example
git clone --depth 1 https://github.com/k0nsta/rigger /tmp/rigger
cp -R /tmp/rigger/example ~/machine-setup && cd ~/machine-setup
#    edit manifest.yaml and brew/core.Brewfile, then check it
rigger validate .
git init -b main && git add . && git commit -m "my setup"
git remote add origin git@github.com:you/machine-setup && git push -u origin main

# 2. on the new machine (copy ~/.ssh first, or use an HTTPS URL with a token)
rigger init git@github.com:you/machine-setup     # clones to ~/github.com/machine-setup, opens the TUI
```

The TUI asks for a machine name and a profile, lets you toggle components (`enter` on a
Brewfile opens its packages), shows the plan with its decisions, asks for missing secrets and
runs. [docs/setup-repo.md](docs/setup-repo.md) is the `manifest.yaml` reference.

## Every day

```bash
rigger                 # TUI for the remembered repo
rigger status          # what is in place, exit 1 if anything is missing
rigger apply --yes     # non-interactive, uses hosts/<name>.yaml; log in ~/.local/state/rigger/logs
rigger sync            # commit your changes, pull other machines', re-apply, push
rigger adopt ~/.codex/config.toml   # take a live edit of a seeded file back into the repo
rigger remote myvps    # install rigger on an ssh host, clone the repo there, run the TUI
rigger validate .      # check a setup repo (use it in its CI)
```

Flags: `--repo DIR` (or `$RIGGER_REPO`), `--host NAME`, `--profile P`, `--only a,b` (one-off:
leaves the host file alone once it exists), `--dry-run`. Moved the setup repo? Run rigger once
from its new directory; it relinks without making backups.

## Development

```bash
make check     # lint + tests (tests/ includes end-to-end runs and a Debian container when docker is up)
make build     # bin/rigger
tests/vm/run.sh golden && tests/vm/run.sh --repo DIR slim=slim   # fresh-Mac installs in tart VMs
docs/demo/record.sh   # re-record the GIF above (needs expect, asciinema, agg)
```

Releases: push a `v*` tag; GoReleaser publishes darwin/linux × arm64/amd64 and the cask.

## License

MIT
