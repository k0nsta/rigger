---
worth: yes
where: cmd/rigger/sync.go:322
added: 2026-10-26
---
# sync asks with line prompts while the rest of rigger is a TUI

`rigger` (pick, review, apply) is Bubble Tea; `sync` asks with typed prompts: a Brewfile number, `i`,
enter, `2*` for strays, `none`/numbers for "commit which?", `-2` for "apply which?". With 85 strays
(a throwaway HOME on a shared Homebrew, release check 2026-10-26) the list scrolls away and each package
is answered blind, one at a time. The first plan deferred it ("TUI screen for `sync`",
`docs/plans/completed/20261008-rigger.md:303`). Build it before 1.0, keeping the prompt path for `--yes`,
`rigger remote` over ssh and scripts.
