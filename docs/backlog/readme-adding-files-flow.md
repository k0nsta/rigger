---
worth: yes
where: README.md:124
added: 2026-10-30
---
# README does not explain the two ways a new file reaches every machine

"Common tasks" has one line ("copy it into the repo, add a `links` entry, `rigger apply`"). Users ask
whether they must `git add`/push themselves, edit the manifest for every file, then sync. Explain the two
cases with one example each:

- **Under a folder that is already linked** (e.g. `~/.claude` linked whole to `<repo>/claude`): create the
  file in place, `rigger sync` commits and pushes it; other machines get it on their next `rigger sync`.
  No manifest change, no `git add`.
- **Somewhere new** (a tool in `~/.local/bin`, git hooks in `~/.config/git/hooks`, a `git config` line):
  files into the repo → a component with `links:`/`cmds:` → its id in a profile (or `add:` in
  `hosts/<name>.yaml`) → `rigger validate .` → `rigger sync` (commits, pushes, applies it here) → each
  other machine's `rigger sync` lists it under "to apply" → enter.
- Who gets it: machines whose profile lists the component (a profile including it via `@other`
  counts), or that add it in their host file. A PR to the repo instead of a sync works the same way.
