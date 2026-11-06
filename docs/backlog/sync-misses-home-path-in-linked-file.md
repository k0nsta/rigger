---
worth: later
added: 2026-11-06
---
# sync commits a machine-specific absolute home path written into a linked file

`claude plugin marketplace add <abs path>` rewrites `extraKnownMarketplaces.<name>.source.path` in
`claude/settings.json`, which is linked into `~/.claude`. On a Mac, `/Users/<user>/…` went into git through
sync and had to be changed back to `~/…` by hand. The manifest's run step for that plugin fixes it with jq
afterwards, but a hand run of the installer skips that step.

Possible fixes: sync warns or asks when a linked file's diff swaps a tracked `~/` for an absolute `$HOME`
path, or the manifest gets a per-file normalise rule. Undecided: whether rigger should own this at all.
Other options are to fix the installer so it writes `~/`, or to treat this one file as a one-off.
The question is whether a second tool ever does the same thing.
