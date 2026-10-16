# The setup repo

rigger reads one git repo you own (usually private). Start from [`example/`](../example).

```
manifest.yaml          # required: what can be installed and linked
hosts/<name>.yaml      # written by rigger: what each machine picked
brew/*.Brewfile        # anything else is yours: Brewfiles, dotfiles, scripts …
zsh/.zshrc
scripts/install-x.sh
```

Check it any time: `rigger validate path/to/repo` (also runs on every load).

## manifest.yaml

```yaml
version: 1                      # schema version, required
groups: [base, packages, shell] # optional: display order; enforced when present

profiles:                       # preselections; everything stays toggleable in the TUI
  laptop:
    description: my Mac
    components: [homebrew, core-cli, zsh, git]
    exclude_packages: [some-cask]          # Brewfile entries unpicked by default
  work:
    components: ["@laptop", work-tools]    # "@name" includes another profile
    exclude: [apps]                        # drop components after expansion

unmanaged: [docs, scripts]      # top-level dirs that are not configs (hosts/ is implied)

components:
  - id: zsh                     # [a-z0-9-]
    title: zsh
    group: shell
    description: shown under the list
    platforms: [darwin, linux]  # empty = all
    requires: [core-cli, linux-core]
    brewfile: brew/core.Brewfile
    apt: [zsh, tmux]
    links:
      - {src: zsh/.zshrc, dst: ~/.zshrc}
    seeds:
      - {src: tool/config.toml, dst: ~/.config/tool/config.toml, secrets: [TOOL_TOKEN]}
    layouts:
      src: app-layouts
      dst: "~/Library/Application Support/SomeApp"
      files: [windows.json, "windows/*.json"]
      process: SomeApp
    cmds:
      - name: plugins
        check: test -d ~/.zsh/plugins       # exit 0 = done, run is skipped
        run: zsh/install-plugins.sh         # from the repo root, $RIGGER_REPO set
        sudo: false                         # calls sudo (plain `sudo`, never `sudo -n`)
        tty: false                          # needs the terminal
        secrets: [TOKEN]                    # exported from the secrets file

manual:                         # shown after a run with ✓/✗
  - {path: ~/.ssh/config, note: copy by hand}
```

### Actions

| field | does | when the target already exists |
|---|---|---|
| `links` | symlink `dst` → `src` in the repo | a link into the repo's old location (after you moved the repo): relinked, no backup; other file/link/dir: backed up, then linked; a **file** with other content asks: adopt it into the repo, or keep the repo version |
| `seeds` | render `src` once into `dst` for tools that rewrite their own config; only the listed `${SECRETS}` are replaced | rigger remembers the hash of its last render: template changed and file untouched → re-rendered; only the file changed → left alone; both → asks |
| `brewfile` | `brew bundle` with per-package picks (`HOMEBREW_BUNDLE_*_SKIP`) | casks whose app or font is already on disk count as present and are skipped |
| `apt` | one `apt-get install -y` batch (sudo, or direct as root) | installed packages are skipped |
| `layouts` | `rigger sync` exports app window/workspace files to `src/<host>/` with the home path replaced by `{{RIGGER_HOME}}` | import is offered only when the app has no state yet; refused while `process` runs |
| `cmds` | shell command from the repo root | skipped when `check` exits 0 |

Commands run without a terminal. When one calls `sudo` (Homebrew casks, apt, your own `cmds`),
rigger asks for the password itself, once per run (a masked prompt in the TUI, or on the
terminal for `apply`/`sync`), checks it, and answers later sudo calls from memory. Use plain
`sudo` in `cmds`: `sudo -n` refuses to ask and fails.

Backups go to `~/.local/state/rigger/backups/<timestamp>/`. Nothing is ever deleted; unpicking a component removes nothing.

### Platforms

A repo can target macOS only, Linux (Debian/Ubuntu) only, or both. For a single platform, leave
`platforms` out and use only its actions (`brewfile` on macOS, `apt` on Linux), so a Linux-only repo
needs no Homebrew. For both, mark each platform's components, as [`example/`](../example) does:
`laptop` is the Mac profile, `server` the Debian one.

### requires

Picking a component pulls in what it requires ("needed by …" in the TUI). A requirement that does not run on this platform is dropped, so `requires: [core-cli, linux-core]` works on macOS and Linux.

`requires_packages: [pipx]` names Brewfile entries (as written) the component runs. While the component is picked they are installed even if the host excludes them, and the TUI shows them locked as "needed by …".

### Validation

Unique ids, known groups/platforms, every `src`, `brewfile` and repo script exists (a layouts dir may not exist yet), `requires`/profile references exist, no cycles, every `requires_packages` entry is in a Brewfile of the component or one it requires, no `dst` inside another link's `dst` (a seed must never write into a linked repo dir), every top-level dir is used by a component or listed in `unmanaged`.

## hosts/\<name\>.yaml

Written by rigger after a run; commit it with `rigger sync`. It stores only how this machine
differs from its profile, so components and default excludes you add to the profile later reach
every machine on its next sync.

```yaml
profile: laptop
add: [work-tools]          # picked here on top of the profile
remove: [apps]             # profile components unpicked here
exclude_packages: [fx]     # Brewfile entries unpicked here
include_packages: [zoom]   # profile default excludes picked here anyway
ignore_packages: [steam]   # installed here on purpose, not in any Brewfile
```

`--profile` naming the host's own profile keeps these adds and removes; another profile starts fresh. Components that no longer exist in the manifest are ignored. The older `components: [...]`
full list is still read.

## Outside the repo

| path | holds |
|---|---|
| `~/.config/rigger/state.yaml` | repo path, host name, seed render hashes |
| `~/.config/rigger/secrets.env` | `KEY=value` (0600); a value may be `op://vault/item/field` (1Password CLI) |
| `~/.local/state/rigger/backups/` | everything rigger replaced |

## Taking local edits back

`rigger adopt <path>` copies a live file into the repo:

- a **seed** (`~/.codex/config.toml`): the template is replaced by the live file with this
  machine's secret values turned back into `${VAR}`; a secret whose value is not in the file is
  reported (its placeholder is gone)
- a **link** that became a real file: the file is copied over the repo version, then linked

Then `rigger sync` shares it.

## Sync

`rigger sync`: export layouts → offer stray Homebrew packages (add to a Brewfile or ignore) → you pick changed files → credential gate (built-in patterns, plus `gitleaks` when installed) → commit `sync(<host>): …` → `git pull --rebase` (a conflict is aborted; live configs never get conflict markers) → apply what other machines added (each listed with why it is picked: its profile, `hosts/<name>.yaml`, or `needed by`; `-n` drops one from this host for good by writing `remove`) → push.

The checkout is live: links point into it, so it stays on the default branch (`origin/HEAD`, else `main`/`master`). Off it, `sync` refuses and `status`, `apply` and the TUI warn. Do feature work in a `git worktree`.
