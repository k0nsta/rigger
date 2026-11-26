---
name: release
description: Cut and verify a rigger release (vX.Y.Z tag, GoReleaser, Homebrew cask). Use when tagging a version, publishing a release, or checking one after it ships.
---

- Tag `vX.Y.Z` on master after CI is green; `.github/workflows/release.yml` runs GoReleaser: darwin/linux × arm64/amd64, `install.sh` on the release, cask in `k0nsta/homebrew-apps` (needs the `HOMEBREW_TAP_GITHUB_TOKEN` repo secret, a fine-grained token with Contents write on that tap only).
- Check after a release: `brew install --cask k0nsta/apps/rigger` on a Mac, `install.sh` on Linux, `rigger version`.
