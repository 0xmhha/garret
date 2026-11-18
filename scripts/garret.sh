#!/usr/bin/env bash
# garret.sh starts, stops and reports a garret server running in the
# background, and prints the addresses it listens on and how members join.
#
#   scripts/garret.sh start [garret serve flags...]
#   scripts/garret.sh stop [--force]
#   scripts/garret.sh restart [garret serve flags...]
#   scripts/garret.sh status
#   scripts/garret.sh log
#
# The flags of a start are, in order: the ones given on the command line,
# else GARRET_SERVE_FLAGS, else the ones of the last start, which are kept
# in GARRET_FLAGS. To start without the kept flags, pass "--".
#   scripts/garret.sh start --client-cert=off   # kept for later starts
#   scripts/garret.sh restart                   # uses --client-cert=off again
#
# GARRET_BIN    the binary (default: bin/garret in this repository)
# GARRET_LOG    the log file (default: ~/garret.log)
# GARRET_PID    the PID file (default: ~/.garret.pid)
# GARRET_FLAGS  the kept flags, one per line (default: ~/.garret.flags)
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin="${GARRET_BIN:-$root/bin/garret}"
log="${GARRET_LOG:-$HOME/garret.log}"
pidfile="${GARRET_PID:-$HOME/.garret.pid}"
flagsfile="${GARRET_FLAGS:-$HOME/.garret.flags}"

readonly start_wait=30 # tenths of a second to wait for the banner
readonly stop_wait=150 # tenths of a second; garret allows requests 10 s
readonly certs_off="client certificates are off"

# flags holds the flags of the start being prepared; source tells where
# they came from.
flags=()
source=""

die() {
	echo "garret.sh: $*" >&2
	exit 1
}

# running prints the PID of the running garret server: the one in the PID
# file, else one started some other way.
running() {
	local pid=""
	[[ -f "$pidfile" ]] && pid="$(<"$pidfile")"
	if [[ "$pid" =~ ^[0-9]+$ ]] && ps -p "$pid" -o command= 2>/dev/null | grep -q "garret serve"; then
		echo "$pid"
		return 0
	fi
	pid="$(pgrep -f "garret serve" | head -n 1)"
	[[ -n "$pid" ]] || return 1
	echo "$pid" >"$pidfile"
	echo "$pid"
}

# running_flags prints the flags the server with PID $1 was started with.
running_flags() {
	ps -p "$1" -o command= | sed -n 's/.*garret serve *//p'
}

# choose_flags fills flags and source from the arguments, the environment
# or the kept file, in that order.
choose_flags() {
	if [[ $# -gt 0 ]]; then
		[[ "$1" == "--" ]] && shift
		flags=("$@")
		source="the command line"
	elif [[ -n "${GARRET_SERVE_FLAGS:-}" ]]; then
		# Word splitting is intended: the variable holds several flags.
		# shellcheck disable=SC2206
		flags=($GARRET_SERVE_FLAGS)
		source="GARRET_SERVE_FLAGS"
	elif [[ -f "$flagsfile" ]]; then
		local f
		while IFS= read -r f || [[ -n "$f" ]]; do
			[[ -n "$f" ]] && flags+=("$f")
		done <"$flagsfile"
		source="the last start, kept in $flagsfile"
	else
		source="none given"
	fi
}

# banner prints the start-up lines of the log and how members join.
banner() {
	sed -n '/^garret listening on/,/^  CA fingerprint/p' "$log"
	if grep -q "$certs_off" "$log"; then
		echo "  members join with a browser and a password (client certificates off)"
	else
		echo "  members join with an enrolled device (client certificates required)"
	fi
}

start() {
	local pid
	choose_flags "$@"
	if pid="$(running)"; then
		local now want
		now="$(running_flags "$pid")"
		want="${flags[*]:-}"
		echo "garret is already running (PID $pid) with flags: ${now:-none}"
		if [[ "$source" != "none given" && "$want" != "$now" ]]; then
			die "requested flags differ (${want:-none}, from $source); run: $0 restart ${want}"
		fi
		banner
		return 0
	fi
	[[ -x "$bin" ]] || die "$bin not found; build it with: make release"

	: >"$log"
	nohup "$bin" serve ${flags[@]+"${flags[@]}"} >>"$log" 2>&1 </dev/null &
	pid=$!
	echo "$pid" >"$pidfile"

	local i
	for ((i = 0; i < start_wait; i++)); do
		if grep -q "^  CA fingerprint" "$log"; then
			if [[ ${#flags[@]} -gt 0 ]]; then
				printf '%s\n' "${flags[@]}" >"$flagsfile"
			else
				: >"$flagsfile"
			fi
			echo "garret started (PID $pid) with flags: ${flags[*]:-none} (from $source)"
			echo "log: $log"
			banner
			return 0
		fi
		if ! kill -0 "$pid" 2>/dev/null; then
			cat "$log" >&2
			die "garret did not start; see the lines above"
		fi
		sleep 0.1
	done
	cat "$log" >&2
	die "garret is running (PID $pid) but printed no address yet; check $log"
}

stop() {
	local force="${1:-}" pid i
	if ! pid="$(running)"; then
		echo "garret is not running"
		return 0
	fi
	kill -TERM "$pid"
	echo "stopping garret (PID $pid)..."
	for ((i = 0; i < stop_wait; i++)); do
		if ! kill -0 "$pid" 2>/dev/null; then
			rm -- "$pidfile"
			echo "garret stopped"
			return 0
		fi
		sleep 0.1
	done
	if [[ "$force" == "--force" ]]; then
		kill -KILL "$pid"
		rm -- "$pidfile"
		echo "garret killed"
		return 0
	fi
	die "garret (PID $pid) is still running; run: $0 stop --force"
}

status() {
	local pid
	if pid="$(running)"; then
		echo "garret is running (PID $pid) with flags: $(running_flags "$pid" | sed 's/^$/none/')"
		echo "log: $log"
		banner
	else
		echo "garret is not running"
		return 3
	fi
}

cmd="${1:-}"
[[ $# -gt 0 ]] && shift
case "$cmd" in
start) start "$@" ;;
stop) stop "$@" ;;
restart)
	stop
	start "$@"
	;;
status) status ;;
log) tail -n 50 -f "$log" ;;
*)
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
