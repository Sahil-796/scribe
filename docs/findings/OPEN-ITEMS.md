# Open items — as of end of phase 03

Everything that is broken, unproven, or waiting on a decision. Nothing here is
covered by a passing test, which is precisely why it's written down.

Kept current as phases land. Delete lines when they're genuinely done, not when
they're merely worked on.

**Resolved and deleted this pass:** the name (locked to `scribe`), items 2, 3, 4, 5, 7,
9, 11, 12, 16, 17, 18, 19, 21, 23, 24 and 25. Numbers are never reused — a gap means
something was closed, and `docs/phases/*.md` carries the write-up.

---

## Needs you — decisions nobody else can make

### 26. The history-doc format changed with no migration path

Phase 03 stores CHANGELOG and JOURNAL as a title plus blocks separated by
`<!-- scribe:entry -->`. Any repo onboarded before that has plain-concatenated docs,
which the new parser reads as a single enormous block — rotation would then archive
nothing, or archive everything at once.

Nothing real is onboarded yet, so today the fix costs nothing. It gets steadily more
expensive. **Decide: migrate on read, or declare the old format unsupported and say so
in the phase 04 release note.**

### 27. Committed docs plus rotation is an unexamined combination

`init` now asks whether to commit `docs/scribe/` (item 3's resolution), and phase 03
added rotation, which creates files under `docs/scribe/archive/` on its own. Answer
"commit them" and every Claude reply dirties the working tree, with new files appearing
whenever a doc crosses 32 KB.

That sits awkwardly with locked decision 10 — "git is the undo, and committing stays
yours." The two features were built in parallel branches and nobody has looked at them
together. Not broken, but somebody should decide what the intended experience is.

---

## Broken

### 6. `codex` install is damaged — needs sudo

Probing it in phase 00 hung with no TTY; killing it left `ENOENT` on its vendor
binary. Reinstall was blocked by `EACCES` on the global npm prefix.

```bash
sudo npm install -g @openai/codex
```

This was probe damage, not pre-existing. Until it's repaired, `codex` cannot be
verified as a writer connector and its connector should not be written.

---

## Unproven — claimed nowhere, but easy to assume

### 28. Phase 03 shipped without using the harness built to judge it — start here

**The most important open item now.** Phase 03's whole premise is "the writing is
better." That claim currently rests on reasoning about prompt text plus two live
`opencode` runs.

`experiments/03-prompt-eval/` exists precisely to settle it: four byte-pinned corpus
items, a five-axis rubric, a runner that diffs prompt variants. Two of the four items
were ever sent to a writer, and the two that weren't — `product` and `deadend` — are
the ones that would actually exercise the gate and the correction path.
`variants/tightened-v1.md` was written and never run at all.

Nobody has run the old prompt and the new prompt over the same corpus and compared.
Until that happens, phase 03 is unvalidated on its own terms. It is also cheap: the
harness works, and this is one afternoon of `opencode` calls.

### 8. Fail-open guard — semantics changed in phase 03, still never seen firing for real

`internal/worker` re-reads the docs after applying edits and treats "writer exited 0,
nothing changed" as a failure, without advancing the offset.

Phase 03 changed what counts: an all-`NO_CHANGE` run is now legitimate success, because
each per-doc call is allowed to decline on its own. The guard only fires when a call
claims a change and then echoes back identical content.

Across five live runs it has never fired — no false positives, but no observed real
auto-reject either, so the shape of a genuinely auto-rejected run's output is still an
assumption from phase 00's notes. The narrower definition makes a false positive less
likely and a missed true positive slightly more so.

### 29. Per-doc calls can't see each other, and the correction path depends on them agreeing

Each phase 03 writer call receives only its own doc's current content. So the
correction path — pull an entry from PROJECT, record the reason in DECISIONS — works
only if two independent calls reach the same conclusion from the same transcript slice.

No live run has exercised a real reversal end to end. If it turns out to half-fire
(PROJECT edited, DECISIONS silent) the result is worse than not trying: an entry
disappears with no recorded reason, which `PLAN.md` treats as a bug. The fix, if
needed, is passing all four docs into every call while keeping per-doc guidance.

### 30. Rotation has never fired outside a test

No real doc has reached the 32 KB cap. Archive-file creation, the pointer block, and
month-boundary grouping are covered by unit tests and nothing else. Related: archive
pointers accumulate in the live doc forever and are never themselves rotated, so a
long-lived doc slowly spends cap on pointers. Bounded and small, but unbounded in
count.

### 10. Stop-hook behaviour during subagents — better evidence, not conclusive

One real burst in this repo's own history showed 7 back-to-back subagent delegations
followed by exactly one `stop_hook_summary`. That supports "one Stop per turn
regardless of delegation count" but is a single observation, not a proof.

### 13. opencode permissions are pre-opened on this machine

This machine's opencode has permissions opened globally via an `oh-my-openagent`
plugin config. Tests here can pass for the wrong reason. Phase 00 worked around it
with a forced local `ask` override; anything testing the approval path must do the
same or it proves nothing.

### 14. `scribe init` — run for real only on scratch repos

`scribe init --apply` has been run against throwaway git repos with a real `opencode`
writer. It installs the Stop hook, writes the config, and produces docs judged
genuinely good rather than filler.

Still unproven: `init` against a repo with substantial existing history (the replay
pass at real scale), and against a repo whose README and manifests are messier than a
scratch fixture's. The seed pass has only ever seen a small, tidy repo. Phase 03
doubled replay's calls per chunk, so a large history now costs twice what it did.

### 15. The wizard's interactive path is entirely untested

No pty in this environment. Unverified: actual huh form rendering and field
navigation, validation messages, the `huh.ErrUserAborted` (Ctrl+C/Esc) branch in
`Ask`/`Review`, the interactive redraw branch of `Progress`, and `Ask`/`Review`
returning the operator's actual chosen values on the happy path. Only the
not-interactive short-circuits are covered. Phase 02's onboarding work added a
conditional model field and two more questions to this untested surface.

### 31. `Layout` is asked at onboarding but nothing reads it

Item 5's resolution records `layout: per-session | shared` in `.scribe/config.json` for
phase 06 to build to. Phase 06 doesn't exist, so today the question is decorative —
the same shape as the old docs-dir question that item 17 correctly deleted.

The cost isn't the question, it's the ordering: every repo onboarded before phase 06
bakes in an answer given against semantics that don't exist yet. If phase 06 lands on a
different meaning for `per-session`, those recorded answers are silently wrong rather
than absent. Worth deciding whether to defer the question until the code that consumes
it exists.

### 20. `install` re-encodes `settings.json` through `encoding/json`, which alphabetises top-level keys

All keys and values survive; original key order does not. Byte-exact preservation
would need a JSON AST library, which isn't a dependency.

### 22. Concurrency under real triggers is still unproven

Offset advancement across sessions, multi-trigger coalescing, and the per-repo lock
under genuine contention have only been exercised by `internal/worker`'s fakes. Live,
only a single uncontended lock acquire/release has been observed. Phase 03 made each
run longer (up to four writer calls), which widens the window for a real collision.

---

## Process notes

- **Phases 00–03 are merged to `main`.** [#1](https://github.com/Sahil-796/scribe/pull/1)
  phase 00, [#2](https://github.com/Sahil-796/scribe/pull/2) phase 01,
  [#4](https://github.com/Sahil-796/scribe/pull/4) phase 02,
  [#5](https://github.com/Sahil-796/scribe/pull/5) the phase 00/01 fixes,
  [#6](https://github.com/Sahil-796/scribe/pull/6) onboarding questions,
  [#7](https://github.com/Sahil-796/scribe/pull/7) phase 03.
- **This repo is going public.** The rule that no raw transcript content may be
  committed is now the actual requirement, not a courtesy. Phase 03 broke it once —
  two rendered eval prompts embedding real transcript turns were committed and had to
  be removed from the branch's history before merge. `.gitignore` blocks the pattern
  now. A deliberate pass over `docs/findings/` and `experiments/` before flipping the
  switch is still worth doing.
- **Phases 02 and 03 were built by Sonnet subagent fleets** with hard file-ownership
  allowlists and a scope-diff gate: zero out-of-scope files, zero contested files, both
  times. Phase 02 used twelve agents and was expensive — every cold agent pays 80–130k
  tokens to orient. Phase 03 used four and cost roughly half a million tokens total.
  Fan out on breadth; never on work that is mostly waiting on a slow external process.
- **Phase 03's units did not catch their own defects.** Three real bugs — a run
  aborting on one failed doc call, a gate failure destroying successful work, and a
  block separator that collides with ordinary markdown — were all found by review
  after the fact, and all three were consequences of a shape change nobody traced
  through. Subagent self-reports are not review.
