---
worth: yes
where: cmd/rigger/status.go:38
added: 2026-10-30
---
# a machine's profile is hard to see and hard to change

As a user I can't tell what this machine's profile holds, what this machine adds or removes on top of it, or which other profiles exist. `rigger status` prints only the profile's name, and nothing says that running `rigger` is where you change it.

Wanted: one view of this machine's profile, its differences and the other profiles, with a way to change them from there.
