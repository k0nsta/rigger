# Contributing

Bug reports and small, focused pull requests are welcome. For a bigger change, open an issue
first so we can agree on the shape.

## Reporting a bug

Include `rigger version`, the OS, the command you ran and its output. The full run log is in
`~/.local/state/rigger/logs/`. Remove anything private from your manifest or log before posting.

## Development

Needs Go (version in `go.mod`), golangci-lint and shellcheck.

```bash
make check     # lint + tests, the same as CI (tests/ adds a Debian container run when docker is up)
make build     # bin/rigger
tests/vm/run.sh golden && tests/vm/run.sh --repo DIR slim=slim   # fresh-Mac installs in tart VMs
docs/demo/record.sh   # re-record the README GIF (needs expect, asciinema, agg)
```

- Layout: `cmd/rigger` (commands), `internal/` (manifest, engine, actions, sync, TUI), `tests/`
  (end-to-end), `example/` (a minimal setup repo).
- Unit tests sit next to the code; end-to-end runs live in `tests/`.
- Commit messages: `type: subject` (`fix:`, `feat:`, `docs:`, `test:`), imperative, one line.
- `gitleaks git .` must stay clean: split token-shaped test fixtures (`"ghp_" + "…"`).

## Releases (maintainers)

Push a `v*` tag. GoReleaser builds darwin/linux × arm64/amd64, publishes the GitHub release
with `install.sh`, and updates the cask in `k0nsta/homebrew-apps`.
