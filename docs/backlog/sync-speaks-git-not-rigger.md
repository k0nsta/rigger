---
worth: yes
where: cmd/rigger/sync.go:77
added: 2026-11-13
---
# sync prints git's output and git commands instead of saying what happened

A stuck sync on a second Mac printed a 5-path wall followed by `git -C <repo> stash, rigger sync, git -C <repo>
stash pop`. Nothing in it said why the sync was stuck: another machine had removed the component that
produces those files. Raw git also shows up in other places:
- errors wrap git's output verbatim (`git %s failed: %s`, internal/syncer/syncer.go:81, 289, 326);
- the commit list prints porcelain codes (` M`, `??`, cmd/rigger/sync.go:525);
- the fix hint is a bare stash/pop, which can restore another worktree's stash.

Fix: say it in rigger's words. Name the cause and the machine and date when known, show at most 3 paths
plus "and N more", and offer the action as a prompt rather than printing a command. Status codes become
"changed", "new" and "deleted". Keep git's last line only as a `--verbose` detail. Example:
`agterm layouts: removed from the repo by laptop (Oct 30); this machine still exports them → drop this
machine's 6 layout files and continue? [Y/n]`.
