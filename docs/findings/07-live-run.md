# Phase 01/02 finding — does the loop actually run, end to end?

Unknown under test (`docs/findings/OPEN-ITEMS.md`, item 7, "the single most
important thing to do next"):

> Nothing has run `opencode` end to end and produced real docs in a real repo
> ... the plan's bar — "work for an hour, touch nothing, and the four docs
> are current afterwards" — has not been demonstrated.

**Verdict: no, the loop does not run end to end, and it currently cannot,
independent of the writer's correctness.** `scribe init --apply` works and
produces genuinely good docs from a real writer. But nothing after `init`
ever runs again: `scribe hook` only enqueues a trigger, `scribe run` is an
unimplemented stub, and no code path anywhere calls `internal/worker`. A
Stop hook fires, a trigger lands in `queue.jsonl`, and it sits there
forever. This is a bigger gap than "unproven" — it's structurally not
wired, on top of two argv bugs that would also have broken it.

---

## What I ran

1. `make build` → `bin/scribe`.
2. A throwaway git repo under my scratch dir (not committed, not in `pa`):
   a Go "widget sorter" package (`sort.go`, `sort_test.go`, a README with a
   documented CLI that doesn't actually exist in the code — deliberately,
   to give a writer something honest to notice), one commit.
3. `scribe init --yes` (dry run) — failed immediately, see Bug 1.
4. Diagnosed the failure by reading `internal/writer/opencode.go` and
   testing `opencode` directly. Built a **test-only shell shim** (in my
   scratch dir, never in `pa`) that intercepts calls to `opencode`, drops
   the two flags scribe passes that don't exist, and substitutes the real
   ones, so I could keep testing the rest of the loop without patching any
   Go file. Every result below that needed a working writer used this
   shim; every result about the argv itself used the real `opencode`
   directly, unshimmed.
5. `scribe init --yes --apply` with the shim on `PATH` → succeeded. Read
   the resulting `docs/scribe/PROJECT.md` and `DECISIONS.md` directly.
6. `claude -p "<one-sentence question about the repo>" --output-format
   json` in the scratch repo, with the installed Stop hook live, to get a
   real transcript and a real Stop event (headless, same approach phase 00
   used, for the same reason — no pty in this environment).
7. Timed `scribe hook` directly (not through a Claude session) with a
   synthetic-but-schema-correct payload, 8 runs.
8. Fired 20 real, concurrently-spawned `scribe hook` processes at the same
   repo to check the append lock under genuine OS concurrency, not just
   the unit tests' fakes.
9. Timed 5 direct writer calls (through the shim, real `opencode`,
   real network) with a small prompt shaped like scribe's actual contract.

I did not attempt to route around bug 3 (below) by writing a Go driver for
`internal/worker`, even in scratch — Go's internal-package rule means such
a driver can only compile from inside the `pa` module tree, which the file
allowlist rules out. That's not a workaround I skipped; it's not possible
without editing a file in `pa`.

---

## Bugs found

### Bug 1 — `opencodeArgv` passes flags that don't exist

`internal/writer/opencode.go`'s `opencodeArgv` builds `run --model <model>
--print --auto-approve <prompt>`. Neither `--print` nor `--auto-approve` is
a real `opencode run` flag (checked against `opencode run --help` on
1.18.15). The result isn't an error — `opencode` prints its own help text
to stderr and exits 1, so `scribe init` (which calls the writer directly for
seed/replay) fails on the very first call:

```
scribe: scribe init: seed pass: seed: writer run: writer: opencode exited: exit status 1 (stderr: …)
```

This is the comment in the file's own words: "a best-effort placeholder,
not a verified answer... confirm and correct once 0B reports back." 0B
(`docs/findings/00-writer.md`) did report back — real flags are `--auto`
(not `--auto-approve`) and there's no `--print` at all — but the connector
was never updated to match. **With today's code, the opencode connector
cannot make a single successful call.** This is upstream of the fail-open
guard: it fails before there's any question of silent success.

### Bug 2 — default model string is missing its provider prefix

`wizard.DefaultModel = "longcat-2.0-free"`. `opencode`'s `-m/--model` wants
`provider/model` (`opencode run --help`: "model to use in the format of
provider/model"). Passing the bare name gets a generic `UnknownError` from
opencode with no indication it's a naming problem:

```
{"type":"error","error":{"name":"UnknownError","data":{"message":"Unexpected server error...
```

Needs `opencode/longcat-2.0-free`, matching what `opencode models` lists
and what item 2's `00-models.md` actually used everywhere.

### Bug 3 — nothing ever calls `internal/worker`

This is the one that matters most. `internal/worker` is fully built and
covered by tests, but grep confirms no file outside `internal/worker` (and
its sibling `internal/seed` / `internal/replay`, which reuse its prompt/parse
conventions but not the package itself) imports it:

- `cmd/scribe/hook.go` calls `hook.Run`, which calls `queue.Enqueue` and
  returns. Nothing else.
- `cmd/scribe/run.go` — the command whose job description is "force the
  worker to run" — is `return notImplemented("scribe run", "01 (worker)")`.
- No daemon, cron, or launchd wiring installs anything that drains the
  queue on a schedule either.

So the full lifecycle today is: `scribe init --apply` installs a Stop hook
that calls `scribe hook`; every subsequent Stop event appends a valid
trigger to `queue.jsonl`; and that's the end of the line. The four docs
never update again after `init`. I confirmed this by installing the hook
for real (below) and watching `queue.jsonl` grow with no consumer.

### Bug 4 (smaller, found incidentally) — `--format json` would break `parseEdits` as currently written

Not something scribe does today (it never reaches this flag, see Bug 1),
but worth recording since 0B's writeup recommends `--format json` for
"clean parsing" and a future fix might reach for it. `internal/worker`'s
`parseEdits` calls `json.Unmarshal` on the writer's **entire raw stdout**,
expecting it to be exactly one JSON object (`{"PROJECT.md": "...", ...}`),
tolerating only a wrapping code fence. `--format json` emits one JSON
object *per event* (`step_start`, `text`, `step_finish`, ...), newline
delimited — `parseEdits` would fail to unmarshal that as a single object.
What actually works with `parseEdits` as written is *not* passing
`--format` at all (defaults to `"default"`): with no TTY attached, that
format's stdout turned out to be exactly the model's final text with no
TUI/ANSI noise (decorative "> build · model" lines go to stderr, not
stdout) — verified byte-for-byte with `cat -v`. Whoever fixes Bug 1 should
use `--auto` only, not `--format json`, or should rewrite `parseEdits` to
extract the last `text` part from the event stream if they want `--format
json`'s more parseable framing for other reasons (e.g. token/cost
accounting, which the JSON stream carries and default format doesn't).

---

## Fail-open guard (item 8) — did it fire, and did that prove anything?

**It never got exercised, because Bug 1 fails before the guard's precondition
can occur.** The guard (`internal/worker/worker.go`, `docsUnchanged`)
compares doc content before/after a writer call that *exited 0*. Bug 1
makes `opencode` exit 1 (unrecognized flags → help text → exit 1), which is
a different, already-caught error path (`writer: opencode exited: exit
status 1`), not the "exited 0, wrote nothing" case the guard exists for.

With the test shim's corrected argv, I could reach a real exit-0 call, but
I did not have a way to force a real auto-reject through `scribe`'s own
code path, because there's no CLI command that drives the worker (Bug 3) —
`docsUnchanged` only runs inside `internal/worker.runOnce`, which nothing
outside its own test suite calls. So: **the fail-open guard is still
exactly as unproven against reality as it was before this session**, for a
different reason than expected — not because auto-reject is hard to
trigger (phase 00 already showed exactly how), but because the code path
that would run the guard against a live queue doesn't exist yet. This
machine's opencode also still has permissions pre-opened globally (item
13) — even a from-scratch attempt here would need the same local
`opencode.json` override phase 00 used to reach the real ask/deny path
rather than the pre-opened one.

---

## Real timing numbers

**Hook latency** (`scribe hook`, 8 runs, synthetic-but-schema-correct
payload, subprocess wall clock measured in Python, not shell `time`'s
0.00s granularity): **2.6–3.7 ms**, mean 3.0 ms. Well inside the 50 ms
budget, and notably faster than phase 01's own 17–22 ms figure — plausibly
because that measurement included more surrounding process overhead.
Either way, hook latency is not a concern.

**Writer wall-clock**, 5 back-to-back calls through the shim (real
`opencode`, real network, small prompt shaped like scribe's actual
contract): **7.8s, 15.6s, 9.2s, 8.8s, 9.5s** — mean ~10.2s, range 7.8–15.6s.
Consistent with phase 00's wider 6–26s spread (item 12), just a tighter
sample. `internal/writer/exec.go`'s `defaultTimeout` is 3 minutes. **The
data supports that default as-is** — it's roughly 12x the slowest run
observed across both sessions combined (26s), which is a reasonable
margin for variance, not an arbitrary guess that needs tightening. If
anything, 3 minutes is generous rather than unsized; I'd call item 12
closed by this data, at the current value.

**End-to-end Stop-to-docs-updated lag: not measurable.** Bug 3 means there
is currently no "end" to measure — a trigger sits in `queue.jsonl`
indefinitely.

---

## `scribe init` (item 14) — first real exercise

Once the shim removed Bugs 1/2 from the picture, `scribe init --yes
--apply` genuinely worked, in one shot, with no other issues:

- Seed pass produced a `PROJECT.md` and `DECISIONS.md` that were, honestly,
  good — specific to the repo's actual content, not generic filler.
  `PROJECT.md` caught that the README documents a CLI (`widgetsort
  --input ...`) that doesn't exist in the code, and that there's no
  `go.mod` despite the README requiring Go 1.22+. `DECISIONS.md` correctly
  found nothing to report as a decision (one commit, no ADRs, no design
  doc) and instead listed open questions worth deciding later — exactly
  the right call given the doc's own instructions, not a hallucinated
  decision history.
- Replay pass correctly reported "No prior Claude Code sessions found for
  this repo — nothing to replay yet" and left `CHANGELOG.md`/`JOURNAL.md`
  unwritten, rather than inventing history. (They still don't exist even
  after I later ran real `claude -p` sessions in the repo — replay only
  runs once, at `init` time, and nothing since drains the queue those
  sessions built. Same root cause as Bug 3.)
- The model, once fed one JSON object as its whole task, did not reliably
  follow "no code fence" — one of my direct writer-timing calls came back
  wrapped in ```` ```json ... ``` ````. `parseEdits`'s `stripCodeFence`
  handled it fine, so this isn't a bug, just a note that the fence
  tolerance in `parse.go` is doing real work, not defending against a
  hypothetical.
- Stop hook install and `.scribe/config.json` were both written correctly
  and matched what `scribe hook` then actually read at runtime.
- Total wall time for `init --apply` (two writer calls: seed pass for the
  two state docs) was ~37s — consistent with the per-call timing above.

No issues found in `install`, the preview-staging step, or the "already
initialised" short-circuit (checked by running `init` a second time — it
correctly refused to redo seed/replay and printed the existing config
rather than clobbering it).

## Hook + real transcript

Installed via `scribe init --apply` above, then ran a real, non-trivial
`claude -p` turn in the repo (headless, per phase 00's approach — no pty
here either). Confirmed for real, not from a fake:

- A genuine session directory appeared under `~/.claude/projects/<encoded
  repo path>/` with a real transcript file.
- The Stop hook fired and `scribe hook` appended a well-formed trigger to
  `.scribe/queue.jsonl`, pointing at that real transcript path and the
  correct resolved repo root.
- 20 concurrently-spawned real `scribe hook` processes (not goroutines in
  a test binary — actual `fork`/`exec`) against the same repo all
  appended cleanly; `queue.jsonl` ended with exactly 20 lines, every one
  valid JSON. The append lock holds under real OS-level concurrency, not
  just phase 01's in-process fakes.

What I could **not** confirm, because Bug 3 removes the mechanism entirely:
whether a second session's trigger causes an append rather than a
duplicate doc entry, whether the byte offset stops re-reading old
transcript content, and whether the run lock behaves correctly when two
triggers land close together. All three are still exactly as unproven as
before this session — not because I ran out of time, but because there is
no command that exercises them against a live queue.

---

## What is still unproven after this

- **The core loop.** One `init --apply` plus one hook firing is not "work
  for an hour, touch nothing" — it's not even one full loop iteration,
  because no iteration can complete. Say this plainly: the phase 01
  done-when is further from met than the "unproven" framing in
  `OPEN-ITEMS.md` suggested, since the missing piece isn't observation, it's
  a command that doesn't exist yet.
- The fail-open guard, against a real auto-reject, through scribe's own
  code (not a raw `opencode` probe) — blocked by the same wiring gap.
- Offset advancement across multiple runs, coalescing of triggers that land
  while a run is in progress, and the run lock under real contention — all
  need a working `scribe run` (or a daemon that calls the worker on a
  timer) before they can be tested against reality instead of fakes.
- Whether `opencode`'s occasional code-fence wrapping, or other prompt
  adherence drift, shows up differently on CHANGELOG/JOURNAL-shaped prompts
  (append, not replace) — only PROJECT.md/DECISIONS.md-shaped prompts were
  exercised for real, since no replay content existed to seed the other two.
- codex is still untouched (out of scope here, per item 6).

## Suggested next step

Given items 7 and 8 both terminate at the same wall — no code path calls
`internal/worker` — implementing `scribe run` (even a minimal synchronous
version: lock, drain, read transcript, call writer, apply edits, unlock)
is the one change that unblocks re-testing all of items 7, 8, and the
offset/coalescing/lock questions in items 9–13 at once. Bugs 1 and 2 above
are small, closer to a five-line fix, and should land in the same pass
since nothing else can be verified against a real writer until they do.
