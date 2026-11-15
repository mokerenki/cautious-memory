#!/bin/sh
# ARTEX supervisor startup script (Linux / macOS / Docker ENTRYPOINT)
#
# Usage:
#   ./start.sh                       run in the foreground (Ctrl-C to stop)
#   nohup ./start.sh >artex.log 2>&1 &   run persistently in the background
#   ./start.sh -addr :9000           pass additional arguments through to artex unchanged
#
# Its only job is to start artex and decide whether to relaunch it based on its exit code.
#
#   0      user-requested stop        → exit the loop
#   75     program-requested restart        → relaunch immediately (the page requested "one-click update" or "rollback")
#   other  crash                → relaunch after backoff (1→2→4…up to 60 seconds)
#
# Downloads, SHA256 verification, and binary replacement are intentionally handled elsewhere: implementing that logic in both sh and bat would duplicate it,
# and those are precisely the steps that must not fail—if an unstartable binary were installed, this script would faithfully
# keep relaunching it, leaving the user to recover the machine manually. Verification and replacement are therefore handled in Go (the selfupdate package),
# by artex itself at startup, keeping this script simple.
set -u

cd "$(dirname "$0")" || exit 1

BIN=./artex
[ -x "$BIN" ] || { echo "[artex] executable not found $BIN" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0

# Forward stop signals to the artex process.
#
# This is required in Docker: docker stop sends SIGTERM only to PID 1 (this script),
# not to child processes. Without forwarding it, artex cannot shut down gracefully and is killed with SIGKILL after 10 seconds,
# abruptly interrupting any tasks in progress.
forward() {
	stopping=1
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	# A signal interrupts wait and makes it return >128. The child process may still be shutting down gracefully,
	# so wait again to get its actual exit code.
	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[artex] stopped"
		exit 0
	fi

	case "$code" in
		0)
			echo "[artex] exited normally"
			exit 0
			;;
		"$RESTART_CODE")
			# Update/rollback is ready: relaunch artex to complete the replacement at startup (see selfupdate.Bootstrap).
			echo "[artex] restart requested (applying the new version)…"
			delay=1
			;;
		*)
			echo "[artex] exited unexpectedly (code=$code); restarting in ${delay}s" >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
