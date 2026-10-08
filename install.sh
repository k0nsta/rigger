#!/bin/sh
# Install the latest rigger release into ~/.local/bin (or $RIGGER_DEST).
#   curl -fsSL https://github.com/k0nsta/rigger/releases/latest/download/install.sh | sh
set -eu

REPO="k0nsta/rigger"
DEST="${RIGGER_DEST:-$HOME/.local/bin}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *) echo "rigger: unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *) echo "rigger: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

tag="${RIGGER_VERSION:-}"
if [ -z "$tag" ]; then
  tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
fi
[ -n "$tag" ] || { echo "rigger: could not resolve the latest release" >&2; exit 1; }
ver="${tag#v}"
asset="rigger_${ver}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"; else sum="shasum -a 256"; fi
(cd "$tmp" && grep " $asset\$" checksums.txt | $sum -c -) >/dev/null
tar -xzf "$tmp/$asset" -C "$tmp" rigger

mkdir -p "$DEST"
install -m 0755 "$tmp/rigger" "$DEST/rigger"
echo "installed rigger $ver to $DEST/rigger"
case ":$PATH:" in
  *":$DEST:"*) ;;
  *) echo "add $DEST to PATH, or run $DEST/rigger" ;;
esac
echo "next: rigger init <your setup repo, e.g. git@github.com:you/machine-setup>"
