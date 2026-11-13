---
worth: yes
where: cmd/rigger/sync.go:52
added: 2026-11-13
---
# sync exports layouts before it pulls, so a machine stays stuck after another machine removes them

`sync` runs `exportLayouts` from the local manifest before `pickFiles` and `Pull`. One machine removed the
layouts component and deleted its snapshot files. On a second machine, every sync then re-exported its
snapshot, the user left those files out, and `Pull` refused because the remote had deleted them
(ErrUncommitted). That machine could not pull for two weeks, and missed every other change made in that
time.

Fix:
- fetch before exporting, and skip the export for a component the incoming manifest drops or no longer
  gives layouts;
- when files left out of the commit also changed on the remote, offer "drop my changes to these files
  (take the repo's version)" before stopping, instead of printing a stash recipe.

Test: two clones; A removes the component and its files and pushes; B (old manifest, live layouts) syncs
and ends up pulled with no layout files left over.
