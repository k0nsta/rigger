#!/usr/bin/env bash
# Records docs/demo/rigger.gif: `rigger init` of example/ in a throwaway HOME, where
# git@github.com:you/machine-setup resolves to a local bare repo. expect types the keys,
# asciinema records, agg renders (brew install asciinema agg; no browser needed).
# It runs the real Homebrew: example/brew/core.Brewfile's git, ripgrep and jq are installed if
# missing (the font cask is unticked on camera). KEEP=1 keeps the work dir and the .cast.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d)
[ "${KEEP:-}" = 1 ] || trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/bin" "$WORK/home" "$WORK/remote"
(cd "$ROOT" && go build -o "$WORK/bin/rigger" ./cmd/rigger)

cp -R "$ROOT/example" "$WORK/setup"
git -C "$WORK/setup" init -q -b main
git -C "$WORK/setup" add .
git -C "$WORK/setup" -c user.name=demo -c user.email=demo@example.com commit -q -m init
git clone -q --bare "$WORK/setup" "$WORK/remote/machine-setup.git"
cat >"$WORK/home/.gitconfig" <<CFG
[user]
	name = Demo
	email = demo@example.com
[url "file://$WORK/remote/machine-setup.git"]
	insteadOf = git@github.com:you/machine-setup
CFG

cd "$WORK/home"
env -i HOME="$WORK/home" TERM=xterm-256color LANG=en_US.UTF-8 BASH_SILENCE_DEPRECATION_WARNING=1 \
	PATH="$WORK/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin" \
	HOMEBREW_CACHE="$HOME/Library/Caches/Homebrew" HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1 \
	asciinema rec --headless --quiet --overwrite --window-size 112x24 --idle-time-limit 4 \
	--command "expect $ROOT/docs/demo/drive.exp" "$WORK/demo.cast"
agg --font-size 15 --theme monokai "$WORK/demo.cast" "$ROOT/docs/demo/rigger.gif"
echo "wrote docs/demo/rigger.gif"
if [ "${KEEP:-}" = 1 ]; then echo "cast: $WORK/demo.cast"; fi
