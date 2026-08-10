# 03 — prompt eval

Phase 03's last item is "iterate against the phase 02 corpus," and until now
that meant eyeballing one live run at a time with no way to compare a prompt
change against what came before. This is the harness: a fixed corpus of real
transcript slices, a runner that plays a prompt variant over them against the
real writer, and a rubric for judging what comes out.

## Layout

```
corpus/corpus.json   the four corpus items: session + line/byte range + why
corpus/offsets.py     recompute byte offsets if a source session ever changes
variants/baseline.md      frozen copy of internal/worker/prompt.go's prompt (191cf99)
variants/tightened-v1.md  a candidate rewrite, not yet live-tested (see below)
runner.go             the harness itself (`go run .` from this directory)
rubric.md              five pass/fail axes to judge one output against
runs/                  output of each invocation, one directory per run-id
```

## The corpus

Four slices of this repo's own Claude Code sessions
(`~/.claude/projects/-Users-sahil-work-pa/*.jsonl`), picked to exercise
different shapes phase 03's prompt actually has to handle:

| item | shape | what it probes |
|---|---|---|
| `engineering` | pure engineering | a concrete bug found and fixed, no product talk |
| `product` | product-level discussion | real decisions made, zero files touched |
| `reversal` | visible correction | assistant self-corrects mid-transcript ("that was wrong") |
| `deadend` | abandoned idea | an approach proposed, then explicitly scrapped, never shipped |

Each is pinned in `corpus/corpus.json` by session filename, line range (for a
human to go re-read the exchange), and exact byte range (what the runner
actually reads). `-Users-sahil-work-pa` is Claude Code's own mangling of this
repo's absolute path — deterministic on this machine, not portable to a
clone elsewhere, same as any other local Claude Code state. See `corpus/offsets.py`
if a source session ever needs re-slicing.

**Privacy:** all four items come from this repo's own sessions, never another
project's. Per the brief, transcripts from other repos on this machine may
contain private content; none are used here. The full source `.jsonl`
session files themselves are not committed — `corpus.json` holds only the
coordinates (session id, line range, byte range), same posture as
`experiments/00-model-bakeoff`'s "transcripts are secrets" note. A run only
works on a machine that actually has `~/.claude/projects/-Users-sahil-work-pa/`
populated with these session ids. `runs/**/*.prompt.txt` (see below) *do*
contain the short excerpted entries themselves, not just coordinates — which
is why they are **not** committed. An earlier pass argued they were safe to
commit (this repo's own development conversation, 24-50KB excerpts rather
than full dumps) and two of them were; that call was reversed before the
phase 03 merge, the files were scrubbed from the branch's history, and
`.gitignore` now blocks `experiments/03-prompt-eval/runs/**/*.prompt.txt`.
A `.prompt.txt` is reproducible from `corpus.json` plus the variant on any
machine that has the sessions, so nothing is lost by keeping them local.

**Known simplification:** every corpus item is evaluated against empty
"current doc" state ("(not yet created)" for all four docs), because these
ad hoc slices don't have real accumulated PROJECT.md/DECISIONS.md history to
seed with. This matches the real worker's prompt *shape* (instructions, then
current docs, then new entries — see `buildEvalPrompt` in runner.go) but not
a real steady-state run where the docs already have content. Good enough for
judging "does the writer pick the right thing to write," not for judging
how it merges into existing doc content.

## Running it

```
go build -o /tmp/runner .          # from experiments/03-prompt-eval/
./runner -list                     # see the four corpus items
./runner -variant variants/baseline.md -dry-run -run-id smoke   # no writer call, just builds prompts
./runner -variant variants/baseline.md -item reversal -run-id my-run   # one real opencode call
./runner -variant variants/baseline.md -run-id my-run             # all four items, real calls
```

`-item` defaults to `all`; pass a single corpus item name to run (or re-run)
just that one — cheap, and the point of naming `-run-id` explicitly is that a
second invocation with the same run-id and a different `-item` merges into
the same `manifest.json` instead of clobbering it (see "Bug found while
building this" below). Each invocation writes, per item, a `.prompt.txt`
(exactly what was sent) and a `.out.txt` (exactly what the writer returned),
plus a `manifest.json` recording agent, model, entry count, duration, and
any error for every item that's been run under that run-id.

To compare two prompt variants: run each into its own `-run-id`, then diff
the `.out.txt` files for the same corpus item across the two run directories.
Nothing here automates the comparison — `rubric.md` is for a human (or a
separate LLM-judge pass, not built) to read both and score.

Default agent is `opencode` / `opencode/longcat-2.0-free`, same defaults
`internal/writer` uses. `-agent`/`-model` exist for completeness but this
unit only tested `opencode` — no `codex`, no real `claude` sessions. (codex's
install was broken when this was written; it was repaired on 2026-08-10, but
nothing here has been re-run against it and codex is still unverified as a
writer connector.)

## What was actually run

Two real `opencode` calls, both against `variants/baseline.md`, saved under
`runs/baseline-live/`:

- `reversal` (10 entries, 42s, 3824 bytes out) — the output correctly
  recorded the worktrees/stacked-PRs correction as a DECISIONS.md entry
  marked "supersedes the earlier claim that the two branches were stacked,"
  not just the final state.
- `engineering` (5 entries, 38s, 4392 bytes out) — the output correctly kept
  "phase 02 code complete and gated" separate from "phase 01 done-when
  still unmet," which is exactly the overclaiming rubric axis 3 exists to
  catch, and recorded the init reporting bug as a JOURNAL entry without
  narrating the tool calls that found it.

That's the full live budget for this unit (`product` and `deadend` were
proven with `-dry-run` only — prompt built, never sent). `variants/tightened-v1.md`
exists as a second variant to diff against once there's budget for it; it
was written directly off `rubric.md`'s axes but has not itself been run live.

## Bug found while building this

The first version of `manifest.json` writing used `os.Create` unconditionally,
so running `-item reversal` and then `-item engineering` under the same
`-run-id` silently dropped the `reversal` manifest entry (its output files
stayed on disk, just unlisted). Caught by running exactly that sequence
while proving the harness works — fixed with `mergeManifest` in runner.go,
which reads any existing `manifest.json` and folds in fresh entries keyed by
corpus item name instead of overwriting the file. `runs/baseline-live/manifest.json`
was hand-reconstructed once from the two real runs' logged output (duration,
byte count, timestamps) after the fix, rather than re-spending the two
`opencode` calls just to regenerate a manifest whose content was already
known and unchanged.
