---
name: rigger
description: Change a rigger setup repo (manifest.yaml, brew/*.Brewfile, hosts/*.yaml, the dotfiles it links) the way rigger expects. Use when editing files in a machine-setup repo, when a config under ~ is a symlink into such a repo, or when the person mentions rigger, sync, apply or adopt.
---
<!-- rigger: skill -->

## The checkout is live

Its files are linked into `~`: an edit there is an edit to the running config. `rigger status` names the repo and what is in place.

## Edit the repo file, not the `~` path

Never `sed -i` or `perl -i` a linked path: it replaces the link with a real file. If a link became a real file, `rigger adopt <path>` takes it back and relinks it. Seeds (`seeds:` in the manifest) are rendered copies: edit the live file, then `rigger adopt <path>`.

## Never move what the links point to, never throw away edits

In the live checkout, do not run `git switch` or `checkout <branch>`, `stash`, `reset --hard`, `clean`, `rebase`, `pull`, `merge`, `cherry-pick`, `push --force`, or `restore` or `checkout -- <file>`; these discard the person's uncommitted edits. When rigger prints a git fix (switch back, stash, finish a rebase), show it to the person; do not run it unless asked.

## Feature work in a worktree

Run `cd <repo> && git worktree add ../<repo>-<topic> -b <topic>`; push the branch, the person merges it on the remote, then runs `rigger sync`. rigger keeps using the live checkout when run inside a worktree.

## Committing

Edit in place and leave committing to `rigger sync` (it commits, pulls with rebase, applies what other machines changed, and pushes). Staging is fine. A commit with a real message is fine when the person asks; run `gitleaks git --pre-commit --staged` first, because rigger's credential gate only checks what sync commits.

## New config or package

A new config: copy it into the repo, add a `links:` (or `seeds:`) entry to a component, run `rigger validate .`, then the person runs `rigger apply`. A package: add a line in a `brew/*.Brewfile` with a one-line comment above it (the TUI shows it), or the person installs it and `rigger sync` in a terminal offers it.

## Validate

After any change to `manifest.yaml` or a new top-level directory (it must be used by a component or listed in `unmanaged`), run `rigger validate .`. A broken manifest stops every rigger command.

## Secrets

Never commit a token, key, or password; never read or print the secrets file (`~/.config/rigger/secrets.env`, or under `$XDG_CONFIG_HOME`). A seed template refers to `${NAME}`, and `NAME` must be in that seed's `secrets:` list.

## Running rigger

Do not run `rigger sync` or `rigger apply` unless the person asks: they commit, push, and change the machine. Without a terminal they need `--yes`, and `sync --yes` commits every changed file and skips the package questions: check `git status` and tell the person first.
