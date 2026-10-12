#!/usr/bin/env bash
# Fresh-Mac install matrix in tart VMs: every case boots a throwaway clone of a golden image
# (vanilla macOS + Command Line Tools), runs `rigger apply --yes` twice (install, then
# idempotency) against a setup repo and keeps the logs.
#
#   tests/vm/run.sh golden                       build the golden image once (~15 min; DISK_GB, default 100)
#   tests/vm/run.sh --repo DIR CASE...           run cases one VM at a time
#
# CASE is name=profile[:comp,comp] (profile "-" = none, --only comps), or random:SEED for a
# seeded host-file delta on top of PROFILE (default personal): random components removed,
# random Brewfile packages excluded.
# xcode-clt itself is not covered: its install is a GUI dialog, the golden image has CLT baked in.
set -euo pipefail

BASE_IMAGE=${BASE_IMAGE:-ghcr.io/cirruslabs/macos-tahoe-vanilla:latest}
GOLDEN=${GOLDEN:-rigger-golden}
DISK_GB=${DISK_GB:-100}
RANDOM_PROFILE=${RANDOM_PROFILE:-personal}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK="$ROOT/tests/vm/.work"
KEY="$WORK/id_ed25519"
SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=10)

log() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
die() { log "error: $*"; exit 1; }

vssh() { local ip=$1; shift; ssh "${SSH_OPTS[@]}" -i "$KEY" "admin@$ip" "$@"; }

# boot VM in the background, wait for ssh, print its ip
boot() {
	local vm=$1 ip
	tart run --no-graphics "$vm" >"$WORK/$vm.console.log" 2>&1 &
	ip=$(tart ip --wait 180 "$vm") || die "$vm: no ip"
	for _ in $(seq 60); do
		if nc -z -G 2 "$ip" 22 2>/dev/null; then echo "$ip"; return; fi
		sleep 2
	done
	die "$vm: ssh did not come up"
}

halt() { tart stop "$1" >/dev/null 2>&1 || true; }

golden() {
	command -v tart >/dev/null || die "tart is not installed"
	mkdir -p "$WORK"
	[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N '' -C rigger-vm -f "$KEY"
	tart delete "$GOLDEN" 2>/dev/null || true
	tart clone "$BASE_IMAGE" "$GOLDEN"
	local ip pub
	ip=$(boot "$GOLDEN")
	pub=$(cat "$KEY.pub")
	log "golden: installing the ssh key (vanilla login is admin/admin)"
	expect <<EOF >/dev/null
set timeout 60
spawn ssh ${SSH_OPTS[*]} admin@$ip "mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '$pub' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
expect "assword:" { send "admin\r" }
expect eof
EOF
	vssh "$ip" true || die "golden: key login failed"
	vssh "$ip" 'sudo -n true' || die "golden: sudo needs a password; the matrix needs NOPASSWD sudo"
	log "golden: installing Command Line Tools"
	# shellcheck disable=SC2016 # expands in the VM
	vssh "$ip" 'set -e
touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
label=$(softwareupdate -l 2>/dev/null | sed -n "s/^\* Label: \(Command Line Tools.*\)/\1/p" | tail -1)
[ -n "$label" ] || { echo "no Command Line Tools update offered" >&2; exit 1; }
sudo softwareupdate -i "$label" --agree-to-license
rm -f /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
xcode-select -p'
	vssh "$ip" 'sudo shutdown -h now' || true
	halt "$GOLDEN"
	grow
	log "golden: ready ($GOLDEN)"
}

# grow the golden disk to DISK_GB: the vanilla image's 50 GB fills up on the personal profile.
# Recovery sits right after the system container and SIP forbids erasing it, so drop it from
# the GPT in the image file (as packer-plugin-tart's recovery_partition = "delete" does), then
# grow the container from inside. Runs after the CLT install, which needs softwareupdate.
grow() {
	local ip
	tart set "$GOLDEN" --disk-size "$DISK_GB"
	python3 -I "$ROOT/tests/vm/drop-recovery.py" "${TART_HOME:-$HOME/.tart}/vms/$GOLDEN/disk.img"
	ip=$(boot "$GOLDEN")
	vssh "$ip" 'yes | sudo diskutil repairDisk disk0 >/dev/null; sudo diskutil apfs resizeContainer disk0s2 0 && df -h /'
	vssh "$ip" 'sudo shutdown -h now' || true
	halt "$GOLDEN"
}

# stage the setup repo's committed tree (plus a host file for random cases) in $1
stage() {
	local dst=$1
	mkdir -p "$dst"
	if git -C "$REPO" rev-parse --git-dir >/dev/null 2>&1; then
		git -C "$REPO" archive HEAD | tar -x -C "$dst"
	else
		cp -R "$REPO/." "$dst"
	fi
}

# write hosts/<name>.yaml with a seeded random delta on top of RANDOM_PROFILE
random_host() {
	local dir=$1 name=$2 seed=$3 comps pkgs
	comps=$(rigger_local status --repo "$dir" --host "$name" --profile "$RANDOM_PROFILE" |
		awk 'NR>3 && NF {print $1}' | grep -vxE 'xcode-clt|homebrew' || true)
	pkgs=$(cat "$dir"/brew/*.Brewfile 2>/dev/null | sed -n 's/^\(brew\|cask\) "\([^"]*\)".*/\2/p' | sed 's#.*/##' | sort -u)
	python3 -I - "$seed" "$RANDOM_PROFILE" "$comps" "$pkgs" >"$dir/hosts/$name.yaml" <<'PY'
import random, sys
seed, profile, comps, pkgs = sys.argv[1], sys.argv[2], sys.argv[3].split(), sys.argv[4].split()
r = random.Random(seed)
remove = sorted(r.sample(comps, k=min(len(comps), r.randint(0, 3))))
exclude = sorted(p for p in pkgs if r.random() < 0.5)
print(f"profile: {profile}")
print(f"remove: [{', '.join(remove)}]")
print(f"exclude_packages: [{', '.join(exclude)}]")
PY
}

rigger_local() { "$WORK/rigger-host" "$@"; }

# placeholder values for every secret the case needs, as secrets.env lines: a real machine has
# them, and without them the secret-gated components never settle and every rerun looks changed
dummy_secrets() {
	local tmp keys
	tmp=$(mktemp -d)
	keys=$(HOME=$tmp rigger_local status "$@" 2>/dev/null | sed -n 's/^missing secrets[^:]*: //p' || true)
	rm -rf "$tmp"
	tr ',' '\n' <<<"$keys" | sed -n 's/^ *\([A-Z0-9_][A-Z0-9_]*\) *$/\1=rigger-vm-placeholder/p'
}

run_case() {
	local spec=$1 name profile only args=() vm ip dir out rc1 rc2
	if [[ $spec == random:* ]]; then
		name="random-${spec#random:}"
	else
		name=${spec%%=*}
		profile=${spec#*=}
		only=""
		[[ $profile == *:* ]] && only=${profile#*:} && profile=${profile%%:*}
		[ "$profile" = - ] || args+=(--profile "$profile")
		[ -z "$only" ] || args+=(--only "$only")
	fi
	out="$RESULTS/$name"
	dir="$WORK/stage-$name"
	rm -rf "$dir" && mkdir -p "$out"
	stage "$dir"
	if [[ $spec == random:* ]]; then
		mkdir -p "$dir/hosts"
		random_host "$dir" "$name" "${spec#random:}"
		cp "$dir/hosts/$name.yaml" "$out/host.yaml"
	fi

	vm="rigger-case-$name"
	tart delete "$vm" 2>/dev/null || true
	tart clone "$GOLDEN" "$vm"
	ip=$(boot "$vm")
	log "$name: $ip, applying"
	vssh "$ip" 'mkdir -p ~/.local/bin ~/setup ~/.config/rigger'
	dummy_secrets --repo "$dir" --host "$name" ${args[@]+"${args[@]}"} | vssh "$ip" 'umask 077; cat > ~/.config/rigger/secrets.env'
	scp -q "${SSH_OPTS[@]}" -i "$KEY" "$WORK/rigger-darwin" "admin@$ip:.local/bin/rigger"
	tar -C "$dir" -cf - . | vssh "$ip" 'tar -x -C ~/setup'

	local cmd=".local/bin/rigger apply --repo setup --host $name ${args[*]:-} --yes"
	set +e
	vssh "$ip" "$cmd" >"$out/apply1.log" 2>&1; rc1=$?
	vssh "$ip" "$cmd" >"$out/apply2.log" 2>&1; rc2=$?
	vssh "$ip" ".local/bin/rigger status --repo setup --host $name" >"$out/status.log" 2>&1
	set -e
	scp -q -r "${SSH_OPTS[@]}" -i "$KEY" "admin@$ip:.local/state/rigger/logs" "$out/" 2>/dev/null || true
	halt "$vm"
	tart delete "$vm"
	rm -rf "$dir"

	local summary
	summary=$(grep -hE '^(done|not finished|nothing to do)' "$out/apply1.log" | tail -1 || true)
	[ -n "$summary" ] || summary=$(tail -1 "$out/apply1.log")
	if grep -q 'nothing to do' "$out/apply2.log"; then idem=ok; else idem="CHANGED (rc $rc2)"; fi
	printf '%-22s rc=%-3s rerun=%-14s %s\n' "$name" "$rc1" "$idem" "$summary" | tee -a "$RESULTS/summary.txt"
}

main() {
	[ "${1:-}" = golden ] && { golden; return; }
	REPO=""
	[ "${1:-}" = --repo ] && { REPO=$2; shift 2; }
	[ -n "$REPO" ] && [ $# -gt 0 ] || die "usage: $0 golden | $0 --repo DIR CASE..."
	REPO=$(cd "$REPO" && pwd)
	tart list --quiet | grep -qx "$GOLDEN" || die "no golden image: run $0 golden"
	mkdir -p "$WORK"
	(cd "$ROOT" && go build -o "$WORK/rigger-host" ./cmd/rigger &&
		GOOS=darwin GOARCH=arm64 go build -trimpath -o "$WORK/rigger-darwin" ./cmd/rigger)
	RESULTS="$WORK/results/$(date +%Y%m%d-%H%M%S)"
	mkdir -p "$RESULTS"
	log "results: $RESULTS"
	for spec in "$@"; do
		run_case "$spec" || log "$spec: harness error"
	done
	cat "$RESULTS/summary.txt"
}

main "$@"
