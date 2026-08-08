# Phase 00 — Prove it before building it

**Status: complete. Gate PASSED.**

Three unknowns, all cheap to settle, all fatal if wrong. All three were tested
empirically on this machine — nothing here is reasoning about what probably happens.

Full write-ups: [`00-hook.md`](../findings/00-hook.md),
[`00-writer.md`](../findings/00-writer.md), [`00-models.md`](../findings/00-models.md).

---

## 1. Does the Stop hook fire, and hand over what we need?

**Yes.** Verified against real headless `claude -p` sessions.

Fires once per assistant reply. Payload carries `session_id`, `transcript_path` and
`cwd` every time. The transcript file exists on disk and its internal `sessionId`
matches the hook's. Continuing a session fires it again with the same `session_id`
and a new `prompt_id`.

Timing clears the 50 ms budget with room to spare: a Go binary ran in **29–293 µs**
warm, bash in ~10 ms.

The payload also carries fields the plan didn't anticipate — `last_assistant_message`,
`permission_mode`, `stop_hook_active`, `background_tasks`.

**Unverified:** whether Stop fires mid-subagent. Only one Stop event was seen for a
`Task` exchange, but the run couldn't be confirmed to have delegated at all.

**Open:** a Stop hook exiting non-zero doesn't visibly break the session — but nobody
checked whether that failure is logged anywhere. `scribe doctor` needs somewhere to
look, or silent hook death means docs quietly stop updating with no signal.

## 2. Does the writer run unattended with no terminal?

**Yes — but the plan's threat model was wrong, in a worse direction.**

The plan predicts that forgetting the auto-approve flag causes a silent hang. It
doesn't. `opencode run` without `--auto` **silently auto-rejects** the tool call and
still exits **0** with normal-looking JSON:

```
! permission requested: bash (echo OK); auto-rejecting
```

A hang is loud and you'd notice within a minute. This fails open and quietly: scribe
would report success on every run while writing nothing at all.

**Consequences, both load-bearing:**

- The phase 01 worker must not trust exit 0. It has to confirm the docs actually
  changed.
- `scribe doctor` (phase 04) must exercise the *approval path*, not just check that
  the command runs.

Confounder worth knowing: this machine's opencode already has permissions opened up
globally via an `oh-my-openagent` plugin config. A naive test passes here for the
wrong reason. The probe forced a local `ask` override to get a real answer.

Cold starts ran 6–26 s with high variance — too noisy to size a production timeout
from. `claude -p` also cleared cleanly (~7 s, exit 0).

**`codex` is unverified.** It hung on first invocation with no TTY, and killing it
left the local install broken (`ENOENT` on its vendor binary). Repair needs
`sudo npm install -g @openai/codex`. This was probe damage, not a pre-existing fault.

## 3. Can a cheap model write prose worth keeping? — THE GATE

**PASS.**

Graded against a deliberately hard case: a real, messy 5.5 MB `scout` debugging
session containing a dead end, a repeated question, and — the trap — a detailed,
costed architecture rewrite that ends on *"want me to build it?"* and was never built.
Ground truth was written by hand from the cleaned transcript before any model ran.

| Model | Verdict |
|---|---|
| `longcat-2.0-free` | **Recommended default.** Accurate, clean prose, correctly separated built from proposed |
| `mimo-v2.5-free` | Also passed on the same criteria |
| `deepseek-v4-flash-free` | Substance right, prose wrong — garbled phrases, misspelled a company name |

**Change to the plan:** default to `longcat-2.0-free`, not the plan's example
`deepseek-v4-flash-free`. Deepseek's output needs an editing pass, which defeats the
zero-input premise.

Two of three models refused to report the unbuilt proposal as done — the exact
hallucination failure mode the plan warns about. That's the capability everything
downstream is plumbing around, and it's there.

Compression on the graded transcript: **81.5x** (5,509,375 → 67,581 bytes).

---

## Transcript format facts confirmed

Both traps named in the plan are real:

- tool results are recorded as `type: "user"` messages — not real user turns
- subagent turns carry `isSidechain: true`

Two further traps found empirically, neither in the plan:

- **`"model":"<synthetic>"`** assistant lines are CLI-injected placeholders ("No
  response requested.", rate-limit and auth notices). Ingesting them puts fabricated
  assistant statements into the journal.
- **slash-command invocations and task notifications** also arrive as `type:"user"`
  with string content, and read as real prompts to a naive parser.

**A plan correction:** the Risks section states the `isSidechain` trap as confirmed.
Across 68 real transcripts, **zero** such lines were found. The filter is implemented
and tested against synthetic fixtures, but it is unverified against reality. Combined
with the unresolved subagent question in §1, subagent handling is the softest evidence
in this phase.

---

## Carried into later phases

| Item | Where it lands |
|---|---|
| Don't trust writer exit 0 — verify docs changed | Phase 01 worker |
| `doctor` must test the approval path, not just the command | Phase 04 |
| Default model → `longcat-2.0-free` | Phase 04 config |
| Confirm `isSidechain` and subagent Stop behaviour against real data | Before phase 03 |
| Repair `codex` before writing its connector | Whenever codex is needed |

## Privacy note

Transcripts are secrets — they contain whatever was on screen. The cleaned transcript
used for grading was **not** committed; it was a real session from another private
repo. `experiments/00-model-bakeoff/README.md` explains how to regenerate it locally.
The models' outputs are committed as evidence and are derived from that session.
