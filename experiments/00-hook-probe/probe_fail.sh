#!/usr/bin/env bash
# Variant used only to observe what happens when a Stop hook exits non-zero.
# Reads stdin (so the hook payload doesn't back up a pipe), logs that it ran,
# then exits 1 deliberately.
set -uo pipefail

LOG_FILE="${SCRIBE_PROBE_LOG:-/tmp/scribe-hook-probe.log}"
payload="$(cat)"
ts="$(date -u +"%Y-%m-%dT%H:%M:%S.%NZ")"

{
  printf '=== %s (about to exit 1) ===\n' "$ts"
  printf '%s\n' "$payload"
  printf '\n'
} >> "$LOG_FILE"

exit 1
