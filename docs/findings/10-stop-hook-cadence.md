# Item 10 — does one Stop hook fire per turn, regardless of subagent delegations?

**Answered on 44 observations instead of 1. Yes for the original question — and the
survey turned up a second, more useful fact that nobody had asked about.**

Method: every top-level transcript on this machine (121 files across 18 project
directories), segmented into turns by genuine user messages, counting `Agent` tool
uses (subagent delegations) and `system` / `stop_hook_summary` records per segment.
`isSidechain` lines excluded. Subagent transcripts live in separate `subagents/`
directories (194 files) and were never in scope — see `09-sidechain.md`.

Note for anyone re-running this: the delegation tool is named **`Agent`**, not `Task`.
The first pass of this survey counted `Task`, found zero, and would have concluded
nonsense had it been trusted.

## The original question: settled

| Delegations in one turn | Turns | Stop hooks seen |
|---|---|---|
| 1 | 55 | never more than 1 |
| 2 | 18 | never more than 1 |
| 3 | 10 | never more than 1 |
| 4–6 | 12 | never more than 1 |
| 7, 9, 13 | 4 | never more than 1 |

**44 turns delegated two or more subagents. The maximum in a single turn was 13. Not
one of them produced more than one `stop_hook_summary`.**

Phase 01's single observation (7 delegations, 1 Stop) was right, and it now rests on 44
turns rather than one. scribe will not be triggered N times for a turn that spawned N
subagents, and does not need to defend against that.

## The thing worth knowing: a Stop is not guaranteed on every turn

Of those 44 multi-delegation turns, **11 recorded no Stop hook at all**:

- **5** were in sessions with no Stop hook installed at all — expected, not interesting.
- **2** occurred before the session's first `stop_hook_summary`: the hook was installed
  partway through the session, so earlier turns predate it.
- **1** is the final, still-running segment of the session that produced this document.
- **3** happened mid-session in sessions where the hook was demonstrably firing both
  before and after.

Those last three are the real result. None contained an interruption marker. The most
likely cause is a user message arriving *mid-turn* — before the reply ended — which
this survey's segmentation counts as a turn boundary even though no reply finished
there. In one of the three the following segment carries the Stop, which fits that
explanation; in the other two it does not, so the cause is not fully pinned down.

## Why this doesn't threaten the design, and why that's worth saying out loud

It would be easy to read "a Stop can be missed" as "a reply can go undocumented." It
can't, and the reason is locked decision 4: **each run reads all new transcript bytes
since the saved offset**, not just the bytes belonging to the triggering reply.

A missed Stop therefore delays coverage to the next Stop rather than dropping it. The
bytes stay on disk, the offset stays where it was, and the following run picks up
everything that accumulated. Coalescing (decision 3) does the same job for the opposite
problem — several triggers arriving at once.

This is a property scribe gets for free from reading by offset rather than by event,
and it's the reason the "trigger on every reply" design tolerates an unreliable
trigger. Worth stating explicitly because the natural assumption — one trigger per
reply, each covering its own reply — is wrong in both directions, and the code is
correct only because it never relied on that assumption.

## What is still not proven

- The exact cause of the three mid-session misses. The mid-turn-message theory fits
  one cleanly and the other two only partially.
- Whether a Stop fires when a session ends by crash, kill, or closed window. `PLAN.md`
  assumes not, which is precisely why the trigger is per-reply rather than
  per-session; nothing here tests it.
