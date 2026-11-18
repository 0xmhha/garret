#!/usr/bin/env bash
# garret.sh starts, stops and reports a garret server running in the
# background, and prints the addresses it listens on.
#
#   scripts/garret.sh start [garret serve flags...]
#   scripts/garret.sh stop [--force]
#   scripts/garret.sh restart [garret serve flags...]
#   scripts/garret.sh status
#   scripts/garret.sh log
#
# Without flags, start uses GARRET_SERVE_FLAGS, for example
#   export GARRET_SERVE_FLAGS="--client-cert=off --allow 192.168.1.0/24"
#
# GARRET_BIN   the binary (default: bin/garret in this repository)
# GARRET_LOG   the log file (default: ~/garret.log)
# GARRET_PID   the PID file (default: ~/.garret.pid)
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin="${GARRET_BIN:-$root/bin/garret}"
log="${GARRET_LOG:-$HOME/garret.log}"
pidfile="${GARRET_PID:-$HOME/.garret.pid}"

readonly start_wait=30 # tenths of a second to wait for the banner
readonly stop_wait=150 # tenths of a second; garret allows requests 10 s

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

# banner prints the start-up lines of the log: the URL, the addresses, the
# allowed networks and the CA fingerprint.
banner() {
	sed -n '/^garret listening on/,/^  CA fingerprint/p' "$log"
}

start() {
	local pid
	if pid="$(running)"; then
		echo "garret is already running (PID $pid)"
		banner
		return 0
	fi
	[[ -x "$bin" ]] || die "$bin not found; build it with: make release"
	if [[ $# -eq 0 && -n "${GARRET_SERVE_FLAGS:-}" ]]; then
		# Word splitting is intended: the variable holds several flags.
		# shellcheck disable=SC2086
		set -- $GARRET_SERVE_FLAGS
	fi

	: >"$log"
	nohup "$bin" serve "$@" >>"$log" 2>&1 </dev/null &
	pid=$!
	echo "$pid" >"$pidfile"

	local i
	for ((i = 0; i < start_wait; i++)); do
		if grep -q "^  CA fingerprint" "$log"; then
			echo "garret started (PID $pid), log: $log"
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
		echo "garret is running (PID $pid), log: $log"
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
	sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
