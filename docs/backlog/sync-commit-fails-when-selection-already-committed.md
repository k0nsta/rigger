---
worth: yes
where: internal/syncer/syncer.go:159
added: 2026-11-06
---
# sync aborts when the chosen files were committed while its prompt was open

`rigger sync` listed 7 untracked files in the setup repo. While the "commit which?" prompt was open,
another session committed and pushed 6 of them. The user then picked those 6, and sync stopped with
`git commit failed: … nothing added to commit but untracked files present` instead of going on to
pull/apply/push.

`Commit` re-reads `Changes`, but it still runs `git add` and `git commit -- <paths>` on paths that are no
longer in that list (`!ok`). Fix: drop picked paths that are no longer in `Changes`. If none are left,
print "already committed" and return nil, so a commit with nothing in it is a no-op rather than an error.
Add a test that commits the files between `Changes` and `Commit`.
