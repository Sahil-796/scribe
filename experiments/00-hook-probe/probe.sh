#!/usr/bin/env bash
# Probe Stop hook script for scribe phase 00.
# Reads the hook JSON payload on stdin, appends it verbatim with a timestamp
# to a log file, and exits 0 immediately. Must never block or print to the
# user's session (Stop hooks: stdout is not shown to the user unless the hook
# itself fails, but we avoid printing anything regardless).
#
# Log destination is controlled by SCRIBE_PROBE_LOG so the same script can be
# reused across test runs without editing it.

set -euo pipefail

LOG_FILE="${SCRIBE_PROBE_LOG:-/tmp/scribe-hook-probe.log}"

start_ns="$(date +%s%N)"
payload="$(cat)"
ts="$(date -u +"%Y-%m-%dT%H:%M:%S.%NZ")"
end_ns="$(date +%s%N)"
elapsed_ms="$(( (end_ns - start_ns) / 1000000 ))"

{
  printf '=== %s (self-measured stdin-read+timestamp: %sms) ===\n' "$ts" "$elapsed_ms"
  printf '%s\n' "$payload"
  printf '\n'
} >> "$LOG_FILE"

exit 0
