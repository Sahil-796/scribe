# Phase 00 finding: Stop hook — does it fire, and what does it hand over?

**Unknown under test:** "Confirm the Stop hook fires here and hands over session id,
transcript path, cwd."

**Environment:** Claude Code CLI 2.1.223, macOS (Darwin 25.5.0), `claude` on PATH at
`/Users/sahil/.local/bin/claude`.

## What I tested

1. Built a probe hook (`experiments/00-hook-probe/probe.sh`) that reads the hook JSON
   payload from stdin, appends it verbatim with a UTC timestamp to a log file
   (path from `$SCRIBE_PROBE_LOG`), and exits 0. No stdout/stderr, no network, no
   blocking.
2. Created a throwaway scratch project outside this repo, at
   `/private/tmp/claude-501/.../scratchpad/hook-test/`, with `.claude/settings.json`
   registering the probe as a `Stop` hook (config committed here for reference, at
   `experiments/00-hook-probe/settings.example.json`).
3. Ran headless sessions against it with `claude -p "<prompt>" --output-format text`
   and read back the probe's log after each run:
   - A single-turn session (`claude -p "say hi, one word"`).
   - A second turn on the *same* session via `claude -p --continue "now say bye, one word"`.
   - A session where the prompt asked the model to use the `Task` tool to spawn a
     subagent, then reply itself (`--allowedTools "Task"`).
   - A session with a hook variant (`probe_fail.sh`) that logs and then exits 1, to
     see how a failing hook affects the overall `claude -p` process.
4. Cross-checked the `transcript_path` and `session_id` from the captured payload
   against the actual file on disk.
5. Timed the probe two ways: wall-clock around the whole `claude -p` invocation
   (dominated by model latency, not useful for the 50 ms budget), and directly piping
   a synthetic payload into the probe script/binary with `time -p`, which isolates
   hook startup + stdin read + file append from model latency. Also built and timed
   a trivial static Go binary doing the same job (`experiments/00-hook-probe/probe.go`),
   since the plan specifies Go for the real hook.

## What I observed

### The hook fires and hands over the documented fields, plus more

Real captured payload (from the first single-turn run, reformatted for readability —
the log file has it as one line, exactly as received):

```json
{
  "session_id": "f26ff3c0-9388-4a2f-8fed-71a3c7c55ff2",
  "transcript_path": "/Users/sahil/.claude/projects/-private-tmp-claude-501--Users-sahil-work-pa-cc677ff6-866b-40d6-92bd-614fe67ac2f8-scratchpad-hook-test/f26ff3c0-9388-4a2f-8fed-71a3c7c55ff2.jsonl",
  "cwd": "/private/tmp/claude-501/-Users-sahil-work-pa/cc677ff6-866b-40d6-92bd-614fe67ac2f8/scratchpad/hook-test",
  "prompt_id": "9b96d74e-1e53-4f97-a455-425dd90cf15a",
  "permission_mode": "default",
  "effort": { "level": "medium" },
  "hook_event_name": "Stop",
  "stop_hook_active": false,
  "last_assistant_message": "Hi",
  "background_tasks": [],
  "session_crons": []
}
```

Field-by-field, as observed across all four runs (all fields present in every run,
types stable):

| Field | Type | Notes |
|---|---|---|
| `session_id` | string (UUID) | Same value across turns of the same `--continue`d session; different per new session. |
| `transcript_path` | string (absolute path) | Verified to exist on disk in every run (see below). |
| `cwd` | string (absolute path) | Matched the directory `claude -p` was launched from, exactly. |
| `prompt_id` | string (UUID) | Changed between the two turns of the same continued session — this is per-turn, not per-session. Not asked for in the unknown, but directly useful for phase 01's "new bytes since last run" offset logic. |
| `permission_mode` | string | `"default"` in all runs (didn't vary permission mode in this probe). |
| `effort` | object `{level: string}` | `{"level":"medium"}` in all runs. |
| `hook_event_name` | string | `"Stop"` — confirms which hook fired, useful if one script is registered for multiple events. |
| `stop_hook_active` | bool | `false` in all runs. Per Claude Code's hook docs this flags when a Stop hook is already continuing the session (loop-prevention flag) — not exercised further here. |
| `last_assistant_message` | string | The literal final reply text (`"Hi"`, `"Bye"`, `"main done"`). Not asked for, but means the hook can get the reply text without touching the transcript at all. |
| `background_tasks` | array | Empty in all runs — didn't spawn background tasks. |
| `session_crons` | array | Empty in all runs — didn't set up scheduled tasks. |

**Verdict on the specific ask:** `session_id`, `transcript_path`, and `cwd` are all
present, all strings, all populated on every single run. Confirmed.

### transcript_path is real and session_id matches the transcript

```
$ ls -la "$transcript_path"
-rw-------@ 1 sahil  staff  19975 Aug  8 20:06 .../f26ff3c0-9388-4a2f-8fed-71a3c7c55ff2.jsonl

$ grep -o '"sessionId":"[a-f0-9-]*"' "$transcript_path" | head -1
"sessionId":"f26ff3c0-9388-4a2f-8fed-71a3c7c55ff2"
```

The file exists, is readable by the same user, and the `sessionId` recorded inside the
transcript's own JSON lines matches the `session_id` the hook received. This closes the
loop the plan depends on: the hook's payload is enough to find and read the right
transcript file with no extra lookup.

### Fires once per reply, not per subagent turn

- `--continue`-ing the same session for a second turn fired the hook a second time,
  same `session_id`, new `prompt_id`, new `last_assistant_message` ("Bye"). Confirms
  "fires on every reply" (plan decision #1).
- Asked the model to spawn a subagent via the `Task` tool before replying itself. The
  probe log shows exactly **one** Stop event for that whole exchange (main-agent reply
  only), with `last_assistant_message: "main done"`. I could not confirm from this run
  whether the model actually invoked `Task` — the transcript for that run had no
  `isSidechain: true` entries and no `Task` tool_use block, meaning the model answered
  directly without delegating despite the instruction and `--allowedTools "Task"`. So
  this result is suggestive, not proof: I observed one Stop firing for one full
  request/response cycle that *included* subagent-capable tooling, but I did not
  observe a confirmed subagent turn to know for certain whether Stop also fires
  separately when a subagent itself finishes. Treat "Stop fires only for the main
  agent, never per-subagent" as **likely but not fully verified** — worth a cheap
  follow-up in phase 01 with a prompt that reliably triggers `Task`.

### Non-zero exit from the hook does not fail the session

Ran a variant hook that logs the payload and then `exit 1`. The probe's log still
recorded the payload (so the hook still ran and had its chance to act), and the
overall `claude -p` process still exited 0 with the model's normal reply printed.
I did not see any error surfaced to the transcript or stdout tied to the hook's
failure. This is a shallow check — it confirms a failing Stop hook doesn't visibly
break the user's session in `-p` mode, but I did not probe deeper (e.g. whether
Claude Code logs the hook failure anywhere for `scribe doctor` to find later).

### Timing: well inside the 50 ms budget, for both bash and Go

Two measurements, five runs each, synthetic payload piped directly into the hook
(isolating hook cost from model latency):

**Bash probe (`probe.sh`)**, `time -p` around the whole process:

```
real 0.01
real 0.01
real 0.01
real 0.01
real 0.01
```

Internal self-measured time (stdin read + timestamp + append, from inside the script):
4-8 ms across all captured runs.

**Static Go binary (`probe.go`)**, same test:

```
real 0.45   <- first run, cold (binary not yet in page cache / first Gatekeeper check)
real 0.00
real 0.00
real 0.00
real 0.00
```

Internal `time.Since` measurement inside the Go binary itself: 29-293 µs (fractions of
a millisecond), i.e. two to three orders of magnitude under the 50 ms budget once
warm.

**Verdict:** both bash and Go clear the 50 ms constraint by a wide margin for the
"read stdin, append to a file, exit" workload the real hook needs to do. Go's
warm-run cost is effectively noise (tens of µs); the one slow run (450 ms) was almost
certainly first-touch cost (binary not yet cached, or a one-time macOS code-signature
check on first execution of a freshly built binary) and not representative of steady
state. This matches the plan's stated reasoning for choosing Go for the hook path.

Caveat: this measures the probe's own cost, not `claude`'s cost to invoke the hook
(spawn the process, write the payload, wait for exit). I did not find a clean way to
isolate that overhead from model response latency in `-p` mode — the wall-clock
around the whole `claude -p` call was ~5s, entirely dominated by the model call itself,
so it's not informative for the hook's own budget. Phase 01, once the hook is invoked
in a real interactive session, should sanity-check that Claude Code's own log/debug
output (`claude --debug hooks`) doesn't show unexpected overhead on the invocation
side.

## Verdict

**This unknown clears.** The Stop hook fires reliably on every assistant reply in a
headless (`-p`) session, hands over `session_id`, `transcript_path`, and `cwd` — all
present, all correctly typed, all matching ground truth on disk — plus several useful
extra fields (`prompt_id`, `last_assistant_message`, `stop_hook_active`,
`hook_event_name`) that phase 01 can lean on. A minimal shell or Go hook script
comfortably meets the sub-50ms constraint with orders of magnitude to spare when
warm.

## Risks / open items for later phases

- **Subagent Stop behavior not conclusively verified.** I observed one Stop event for
  a turn that included agentic tooling, but could not confirm the model actually
  delegated to a subagent in that run. Phase 01 should re-test with a prompt that
  reliably triggers `Task` (or inspect a transcript from real usage that's known to
  contain `isSidechain: true` entries) before assuming Stop never fires mid-subagent.
- **Failing-hook visibility is shallow.** Confirmed a non-zero exit doesn't crash the
  user's `-p` session, but didn't check whether Claude Code records the failure
  anywhere `scribe doctor` could read it back from.
- **Interactive (non `-p`) sessions untested.** All runs here used `claude -p`
  headless mode, per the task's suggested approach. The payload shape may differ
  slightly for hooks fired from a normal interactive terminal session (e.g.
  `permission_mode` or `effort` could vary with real usage) — not verified here.
- **Only tested on one machine/CLI version** (2.1.223, macOS). The plan itself flags
  the transcript/hook format as internal and versioned; this finding is a snapshot,
  not a guarantee across CLI versions.

## Files

- `experiments/00-hook-probe/probe.sh` — the actual probe hook used for all captures
  above (bash, logs-and-exits-0).
- `experiments/00-hook-probe/probe_timed.sh` — near-identical variant with an
  internal-only elapsed-time measurement; kept for reference, superseded by the
  self-timing already built into `probe.sh`.
- `experiments/00-hook-probe/probe_fail.sh` — variant that exits 1, used only for the
  non-zero-exit check.
- `experiments/00-hook-probe/probe.go` — throwaway Go equivalent, used only to compare
  startup cost against bash. Not intended to become the real hook implementation.
- `experiments/00-hook-probe/settings.example.json` — the `.claude/settings.json`
  Stop-hook registration used in the scratch test project (the scratch project itself
  lived outside the repo, per instructions).
