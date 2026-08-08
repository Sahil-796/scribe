#!/usr/bin/env bash
# Variant of probe.sh that also records the hook's own wall-clock start/end
# using `date +%s%N` measured from the very first instruction, so we can
# diff it against an external timer wrapped around the whole hook invocation
# (see run_timing_test.sh). Same behavior as probe.sh otherwise: read stdin,
# append payload + timestamp to log, exit 0, never print to stdout/stderr.

set -euo pipefail

START_NS="$(date +%s%N)"

LOG_FILE="${SCRIBE_PROBE_LOG:-/tmp/scribe-hook-probe.log}"

payload="$(cat)"
ts="$(date -u +"%Y-%m-%dT%H:%M:%S.%NZ")"
end_ns="$(date +%s%N)"
elapsed_ms="$(( (end_ns - START_NS) / 1000000 ))"

{
  printf '=== %s (internal elapsed: %sms) ===\n' "$ts" "$elapsed_ms"
  printf '%s\n' "$payload"
  printf '\n'
} >> "$LOG_FILE"

exit 0
