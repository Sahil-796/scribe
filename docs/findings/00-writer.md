# Phase 00 finding — does the writer command complete unattended?

Unknown under test (from `docs/PLAN.md`, Phase 00):

> Confirm the writer command completes unattended with no terminal attached.

Named failure mode to rule out: *"the first thing they hit is a silent hang because
they forgot the auto-approve flag."*

**Verdict: mostly clears, with one correction to the plan's threat model.** `opencode
run` and `claude -p` both complete unattended with no TTY and a closed stdin. But in
this environment's opencode build (1.18.15), the failure mode when you forget the
auto-approve flag is **not a silent hang** — it's a silent auto-reject: the tool call
is denied, logged to stderr, and the run still exits 0. That's arguably worse for a
writer agent than a hang (it fails open, quietly, and you'd only notice because the
docs never changed), so the flag is still mandatory, just for a different reason than
the plan assumed. `codex` could not be verified — see below.

Environment note: this machine's opencode config is not a vanilla install (a plugin
called `oh-my-openagent` is loaded from `~/.config/opencode/`, and the default `build`
agent already had `bash`/`edit` permissions wide open before I touched anything). All
"default" behavior below was re-tested with an explicit `permission: ask` override in
a local `experiments/00-writer-probe/opencode.json` to get past that and reach the
actual no-TTY behavior. That probe config was deleted after use — it is not part of
what's committed.

---

## What was tested

All commands run via the Bash tool, which itself has no controlling TTY (`tty` →
"not a tty", confirmed). Every probe additionally redirects stdin from `/dev/null`
and runs under a hard-kill wrapper (`experiments/00-writer-probe/run_with_timeout.sh`,
since macOS ships neither `timeout` nor `gtimeout`).

1. `opencode --help`, `opencode run --help` — located the non-interactive path
   (`opencode run [message]`) and the auto-approve flag (`--auto`, "auto-approve
   permissions that are not explicitly denied (dangerous!)").
2. `opencode providers list` — confirmed which credentials exist (`GitHub Copilot`,
   `Google`, `OpenCode Zen`, `Nvidia` — all oauth/api, already configured, not touched).
3. `opencode models` — confirmed `opencode/deepseek-v4-flash-free`, the model named in
   the plan's example config, exists in this install.
4. Trivial no-tool prompt ("reply with the single word OK") — baseline.
5. Prompts that force a tool call (`write` a file, `bash echo`) with and without
   `--auto`, first under this machine's permissive default config, then again under a
   local override (`permission: {"bash": "ask", "edit": "ask"}`) to actually exercise
   the approval path headlessly.
6. Bad model name, and a model from an unauthenticated provider (`openai/gpt-4o`).
7. Three timed cold-start runs.
8. `codex --help`, `codex --version` — hung; investigated why.
9. `claude -p` — one trivial-prompt check only (real API cost, kept to a single call).

---

## opencode — exact working argv

```bash
opencode run "reply with the single word OK" \
  -m opencode/deepseek-v4-flash-free \
  --format json \
  --auto \
  </dev/null
```

`--format json` gives one JSON object per line on stdout (step_start / tool_use /
step_finish / text / error) — easy to parse, no ANSI or TUI noise on stdout. Logs
(`--print-logs --log-level DEBUG`) go to stderr only, cleanly separated.

`--auto` is documented as "auto-approve permissions that are not explicitly denied
(dangerous!)" — it does not disable the `question`/`plan_enter`/`plan_exit`
permissions, which are hardcoded `deny` for every headless session regardless of
`--auto` (confirmed from the session-create log line:
`permission="[{"permission":"question","pattern":"*","action":"deny"}, ...]"`).
For a writer agent that only reads and edits docs, that's the right shape: it can
never block waiting on a question it can't ask.

## opencode — proof of the actual failure mode

With a project-local `opencode.json` setting `"permission": {"bash": "ask", "edit":
"ask"}` (to force the approval path past this machine's already-open defaults):

**Without `--auto`** (`experiments/00-writer-probe/out6.stdout` / `out6.stderr`):

```
timestamp=... message=asking id=per_... permission=bash patterns="[\"echo OK\"]"
! permission requested: bash (echo OK); auto-rejecting
```

Exit code **0**. The tool call comes back in the JSON stream as
`"state":{"status":"error","error":"The user rejected permission to use this
specific tool call."}`, the model gets that back, and the run finishes normally. No
hang, no timeout needed to kill it — it self-resolves in ~6s. This is the actual
unattended behavior in opencode 1.18.15: a denied permission in `run` mode fails the
tool call and continues, it doesn't block forever waiting on stdin.

**With `--auto`**, same config (`out7.stdout`/`out7.stderr`): the same permission is
evaluated as `action.action=allow`, the command actually executes
(`"output":"OK\n","metadata":{"exit":0}`), exit code 0.

So the risk named in the plan — "the first thing they hit is a silent hang" — does
not reproduce as a *hang* here. It reproduces as a **silent no-op**: forget `--auto`
and the writer agent still exits 0, but every edit/bash action it attempted gets
quietly rejected. For scribe this means: forgetting the flag doesn't wedge the queue
(good, no stuck lock), but it can produce a run that reports success while writing
nothing (bad — `scribe doctor` needs to check for this, not just check the exit
code). Worth carrying into phase 04 (`scribe doctor`) and phase 01 (worker should
sanity-check that the docs it expected to change actually changed, not just that the
process exited 0).

## opencode — exit codes and failure modes (table)

| Scenario | argv delta | stdout | stderr | exit | latency |
|---|---|---|---|---|---|
| Trivial prompt, no tools needed | (baseline) | JSON events, ends with `text` "OK" | logs only with `--print-logs` | 0 | 6–26s (see below) |
| Tool call, ask-permission, no `--auto` | `permission: ask`, no `--auto` | JSON events, tool `state.status="error"`, run still finishes with a `text` reply | `auto-rejecting` line | 0 | ~6s |
| Tool call, ask-permission, with `--auto` | `permission: ask`, `--auto` | JSON events, tool `state.status="completed"` | none of note | 0 | ~8s |
| Tool call, this machine's default (already-open) permissions | no `opencode.json` override, no `--auto` | tool executes anyway | `action.action=allow` even without `--auto` | 0 | 8–17s |
| Unknown model name | `-m opencode/this-model-does-not-exist` | single `{"type":"error", "error":{"name":"UnknownError", ...}}` line | (none) | 1 | ~3s |
| Model from unauthenticated provider | `-m openai/gpt-4o` | same `UnknownError` shape, ref differs | (none) | 1 | ~3s |
| `--pure` (external plugins disabled) | adds `--pure` | same as default-permissive case — the open permissions are not coming from the `oh-my-openagent` plugin | — | 0 | ~8s |

Cold-start timing, three back-to-back trivial runs on this machine/network:
6.2s, 25.9s, 20.0s. High variance — this looks like model/network latency in this
sandboxed environment rather than opencode's own startup cost (process bootstrap,
config load, and skill scan finish in under ~2s per the DEBUG logs; the rest of the
time is the `stream` call to the model). **Don't take these absolute numbers as
opencode's real-world cold start** — they're specific to whatever backend this
sandbox's `OpenCode Zen` credential points at. Re-measure against the real
`opencode.ai` Zen endpoint before using this number for anything like a hook-timeout
budget.

## opencode — stdout cleanliness

`--format json` stdout is one JSON object per event, newline-delimited, no ANSI
escapes, no TUI redraw noise — it parsed cleanly with nothing more than
`tail -1 | jq`. This is what a writer connector should use; the default
`--format default` was not tested here but is documented as "formatted" (i.e.
human/TUI-oriented) and should be avoided for a script-driven connector.

## opencode — auth / no-network

`opencode providers list` shows four already-configured credentials on this machine
(`GitHub Copilot`, `Google`, `OpenCode Zen`, `Nvidia`). I did not add, remove, or log
into anything. `opencode/deepseek-v4-flash-free` (the model the plan's example config
names) resolves against the pre-existing `OpenCode Zen` credential and worked in
every test above. I did not have a clean way to test "no network at all" without
disrupting the sandbox's own connectivity, so that specific sub-case is untested;
the unauthenticated-provider case (`openai/gpt-4o`) is the closest proxy and produced
a clean `UnknownError` JSON + exit 1, not a hang.

---

## codex — could not verify

`codex --help` / `codex --version` hung with **zero output, 0% CPU, and no TTY
prompt visible** even before any of the deliberate probing above — this happened
on the very first invocation, before I'd built the timeout wrapper. `ps` showed the
process asleep for minutes. I killed it (`kill -9`), and afterward `codex` fails
immediately with `ENOENT`:

```
Error: spawn .../@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/codex/codex ENOENT
```

The vendor directory that's supposed to hold the actual native binary is empty. Most
likely explanation: this codex install lazy-fetches its platform binary on first run,
that fetch was in progress (hence the silent hang — no TTY, no progress bar, nothing
written to stderr) and killing the process left it half-installed. Reinstalling via
`npm install -g @openai/codex` was blocked by `EACCES` on `/usr/local/lib/node_modules`
(this user doesn't have write access there), and I did not attempt `sudo` or any
other privilege escalation per the task's ground rules.

**Net: codex is unverified in this environment.** The one thing I can say with
confidence is that its *first-run* behavior was itself a silent, no-output hang —
which is exactly the failure mode the plan is worried about, just at the binary-fetch
layer rather than the permission layer. A codex connector should not be trusted to
"just work" on a machine where it hasn't already been run at least once
interactively with a good network connection.

## claude — spot-checked only

Tested once, deliberately minimal (this is real API spend against the operating
account, not a free/sandboxed credential):

```bash
claude -p "reply with the single word OK" --output-format json </dev/null
```

Completed in ~7s, exit 0, clean single-line JSON on stdout
(`{"is_error":false, ..., "result":"OK", ...}`), nothing on stderr, cost
$0.082 for this one call (mostly cache-write overhead from the CLI's own system
prompt, per `total_cost_usd`/`modelUsage` in the output). No hang.

I did **not** test claude with a tool-requiring prompt and no
`--dangerously-skip-permissions`, since `claude` help documents that permission mode
explicitly (`--permission-mode`, `--dangerously-skip-permissions`,
`--allow-dangerously-skip-permissions`) and this session's own auto-mode classifier
actively blocked a nested `--dangerously-skip-permissions` call when I tried it
(`"Permission for this action was denied by the Claude Code auto mode classifier"`).
Chasing that further would mean fighting this session's own guardrails to test a
nested copy of itself — not a good use of a half-day budget, and the flag's purpose
is already unambiguous from `--help`. Take the claude row as "the happy path is
confirmed cheap and fast; the permission-flag behavior is documented, not
independently reproduced here."

---

## Risks / what this doesn't cover

- **Silent-success-with-no-edits is the real risk for opencode, not hanging.**
  Forgetting `--auto` means the writer process exits 0 having written nothing. The
  phase 01 worker must not treat "exit 0" as "docs updated" — diff the docs directory
  before/after, or check the JSON event stream for `tool_use`/`state.status` on the
  expected doc paths.
- **This machine's opencode defaults are already permissive** (bash/edit allowed
  before I set any override), sourced from `~/.config/opencode/opencode.json` plus
  the `oh-my-openagent` plugin, not from a project config in this repo. A genuinely
  fresh opencode install on a teammate's machine may behave differently — closer to
  the `permission: ask` case I forced here, where `--auto` is what separates "runs"
  from "silently no-ops." Don't assume this repo's behavior transfers to a clean
  install without `--auto` set explicitly regardless.
- **Cold-start latency numbers (6–26s) are not trustworthy as a general figure** —
  see the caveat above. Re-measure against the real hosted Zen endpoint, and budget
  for high variance, before using this to size any timeout in the hook/worker.
- **codex is a genuine unknown**, not a cleared one. If codex becomes a supported
  connector later, phase 00's codex sub-check needs to be redone on a machine where
  `codex` has a working local binary already.
- **No true "no network" test was run** for either tool — only "bad model" /
  "unauthenticated provider," which exercise a related but not identical path.
- I did not touch `~/.config/opencode/*` or any of the pre-existing credentials;
  the local `experiments/00-writer-probe/opencode.json` override used for the
  ask-permission probe was created and deleted within this session and is not part
  of the committed experiment files.

## Files

- `experiments/00-writer-probe/run_with_timeout.sh` — portable hard-timeout wrapper
  (macOS has neither `timeout` nor `gtimeout` by default).
- `experiments/00-writer-probe/out*.stdout` / `out*.stderr` — raw captured output
  from each probe above, kept as evidence.
