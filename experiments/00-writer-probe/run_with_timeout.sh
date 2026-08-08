#!/bin/bash
# Portable timeout wrapper (macOS has no `timeout`/`gtimeout` by default).
# Usage: run_with_timeout.sh <seconds> <cmd...>
secs="$1"; shift
"$@" &
pid=$!
( sleep "$secs" && kill -9 "$pid" 2>/dev/null ) &
watcher=$!
wait "$pid" 2>/dev/null
status=$?
kill -9 "$watcher" 2>/dev/null
wait "$watcher" 2>/dev/null
echo "EXITCODE=$status"
exit $status
