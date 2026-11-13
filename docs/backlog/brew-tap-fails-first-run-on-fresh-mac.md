---
worth: yes
where: internal/action/brew.go:391
added: 2026-11-13
---
# a third-party tap with Linux-invalid formulae fails the first apply on a fresh Mac

Homebrew 7 validates a whole tap on `brew tap`. A tap that has a formula without a Linux url fails with
"invalid syntax in tap!" the first time, so `brew bundle` reports one failed dependency and the
component fails. The same bundle re-taps without trouble later in the run, and a second apply passes.
The fresh-Mac VM matrix hit this on 2026-11-13 with a tap of Grafana's tools. The earlier agr cask
failure was the same problem (CLAUDE.md, Gotchas).

Fix: before `brew bundle`, tap each `tap` line of the Brewfile; on "invalid syntax in tap" retry once,
then let bundle run. Test with a fake brew whose first `tap` fails that way.
