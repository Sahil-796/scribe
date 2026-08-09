# Phase 03 — make the writing good

**Status: shipped and reviewed. The prompts are better reasoned; they are not yet
better *proven*. Two live writer runs stand behind the whole phase.**

Four parallel units on `phase-03-writing`, merged as
[#7](https://github.com/Sahil-796/scribe/pull/7): `internal/worker` (`b4ed1af`,
`313f814`, `ce44e7f`, `032532c`, `20eb2fa`), `internal/docs` (`ec799cc`, `9a4e61c`,
`4a36687`), `internal/seed` + `internal/replay` (`e20390a`, `2662315`), and
`experiments/03-prompt-eval` (`b0caf75`, `cc036dc`). Plus `c1c6628` wiring the
end-to-end test to the new call shape and `24a8383` fixing three defects the review
found.

---

## The one change everything else follows from

Phase 01 sent **one prompt covering all four docs** and got back a JSON object of
edits. Phase 03 sends **one focused prompt per doc** and gets back either that doc's
new content or the literal `NO_CHANGE`.

That is the whole phase in one sentence, and nearly every consequence below — good and
bad — traces to it.

| | Phase 01 | Phase 03 |
|---|---|---|
| Writer calls per run | 1 | 2, or 4 when the gate opens |
| Output format | JSON envelope | plain content, or `NO_CHANGE` |
| Guidance per call | four jobs at once | one job, with its own examples |
| Context per call | all four docs | that doc only |

## What shipped

| Package | Does |
|---|---|
| `internal/worker` | Per-doc prompts (`docPrompts`, `buildDocPrompt`), the product-level gate, the correction path, journal guidance, and the `CodeWeight` knob |
| `internal/docs` | Size caps, rotation of the append-only docs into `docs/scribe/archive/`, and a `Size` read path |
| `internal/seed` | PROJECT and DECISIONS split into two calls, each with its own prompt; the JSON envelope dropped |
| `internal/replay` | CHANGELOG and JOURNAL split into two calls per chunk, with chunk-continuity and "discussion is not shipment" guardrails |
| `experiments/03-prompt-eval` | Byte-pinned corpus, a five-axis rubric, and a runner for diffing prompt variants |

**The gate.** Most engineering sessions never touch PROJECT or DECISIONS, so attempting
them every run was two wasted calls. A free local keyword prefilter runs first; only if
it hits does a single cheap classification call give the real YES/NO. A pure
engineering run now costs two calls, not four. This is the direct fix for item 24 — the
live run that wrote a JOURNAL entry about "what go version is this repo on?".

**The correction path.** Pulling an entry from PROJECT must write the reason into
DECISIONS. Per `PLAN.md`, being wrong and reversing is recorded, not hidden — removal
without a recorded reason is a bug, not a tidy-up.

**Journal guidance.** Problems actually hit, what the AI got confidently wrong, dead
ends, what the fix turned out to be. Explicitly *not* a narration of tool calls and not
a restatement of the changelog. Concrete good and bad examples live in the prompt text
rather than in adjectives.

**Size caps.** 32 KB for CHANGELOG/JOURNAL (~8,000 tokens of markdown), rotating the
oldest entries into `docs/scribe/archive/<DOC>-YYYY-MM.md` at entry boundaries with a
pointer left behind. 16 KB for PROJECT/DECISIONS with no rotation: those are rewritten
in place, so overflow means the writer is being verbose, not that history accumulated —
it returns `ErrStateDocTooLarge` instead. This is what keeps locked decision 4 ("flat
cost no matter how long the session runs") true once JOURNAL is large, because a cheap
model has to hold the whole doc to edit it safely.

## Design decisions worth knowing

**Separate invocations, not one call with sections.** `PLAN.md` says four small jobs
beat one big one, and sectioned output would have kept the failure mode it was meant to
remove — one response where a mistake in any section contaminates the rest. The cost is
real: 7.8–15.6 s per call, now up to four of them. The gate is what keeps the common
case at two.

**Each call sees only its own doc.** The correction path therefore relies on the
PROJECT call and the DECISIONS call independently reaching the same conclusion from the
same transcript slice, rather than on shared context. This is the phase's least
confident decision. If it underperforms live, the fix is passing all four docs' current
content into every call while keeping the per-doc guidance.

**`NO_CHANGE`, and blank is not `NO_CHANGE`.** An empty response is a hard error, not a
quiet decline. The prompt always asks for content or the sentinel, so blank means
something broke upstream — the silent auto-reject shape from `00-writer.md`. Treating
the two the same would swallow real failures, which is exactly what `PLAN.md`'s "fail
loudly rather than writing nonsense quietly" forbids.

**The block separator is an HTML comment, not `---`.** History docs are stored as a
title plus blocks. The content being split is markdown prose written by a language
model, and a horizontal rule surrounded by blank lines is an entirely ordinary thing
for one to emit. A delimiter that common inside the payload is a latent corruption, not
a delimiter. `<!-- scribe:entry -->` renders as nothing and is not typed by accident.
The archive-pointer marker is the same, for the same reason.

**`CodeWeight` is a `Deps` field, not config.** `check` (verify claims — the default),
`full` (may source content from code), `off`. Wiring it to `install.Config` is phase
04's job; putting it there now would have been building phase 04's surface area on
phase 03's schedule.

## What the review caught

Reviewed after the fact, because none of the four units flagged any of it. All three
were the same blind spot: the phase changed the *shape* of a run and nobody followed
the consequences through.

1. **One failed call discarded the whole run.** `collectEdits` returned on first error.
   A run went from one writer call to four, so the per-run failure rate multiplied —
   and because the offset only advances on success, the next run redid everything,
   including the docs that had worked.
2. **A gate failure destroyed work that had already succeeded.** Worse on its own
   terms: the gate runs *after* CHANGELOG and JOURNAL are in hand, and only decides
   whether to attempt two optional docs. It now degrades to "don't know".
3. **The separator collided with its own payload** — the `---` problem above.

Fixed in `24a8383`. Partial failures now surface through an optional `Deps.Log` rather
than vanishing, which is the invisible-failure shape of items 11 and 23.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` all pass on `main`.

Four tests pin the review fixes: a partial failure keeps the good doc and advances the
offset, a gate failure keeps the history edits, an all-calls-failed run still fails with
the offset unmoved, and an entry containing a `---` rule round-trips as one block.

**Two real `opencode` runs** back the phase. On the `reversal` corpus item the writer
correctly produced a DECISIONS entry marked as superseding an earlier claim rather than
silently writing the final state. On `engineering` it kept "phase 02 code complete"
separate from "phase 01 done-when unmet" and recorded a bug without narrating tool
calls. Both raw outputs are committed under `experiments/03-prompt-eval/runs/`.

## What is NOT proven

**Two runs is thin.** Four corpus items exist; two were sent to a writer. The `product`
and `deadend` items — the ones that would actually exercise the gate and the
correction path — were built and never run. `variants/tightened-v1.md` was written and
never tested against anything.

**No before/after comparison exists.** The rubric and the harness were built to make
phase 03 measurable, and then phase 03 shipped without using them. Nobody has run the
old prompt and the new prompt over the same corpus and compared. The claim "the writing
is better" is currently reasoning, not evidence.

**Rotation has never fired outside a test.** No real doc has reached 32 KB.

**The on-disk format changed with no migration.** CHANGELOG and JOURNAL are now title
plus separated blocks. Any repo already onboarded has the old plain-concatenated
format. Cheap to fix now, expensive later.

## Next

1. Run the corpus properly — all four items, old prompt against new — and let the
   rubric say whether this phase worked. It is the cheapest remaining question and the
   only one that settles the phase's own premise.
2. Decide the format migration before anything real is onboarded.
3. Phase 04 wires `CodeWeight` and the writer config through `internal/install`.
4. Carry forward what phase 03 didn't touch: the fail-open guard has still never fired
   on a real auto-reject (item 8), concurrency is still unproven (item 22), and the
   wizard's interactive path is still untested (item 15).
