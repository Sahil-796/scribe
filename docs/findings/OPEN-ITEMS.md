# Open items — as of phase 04

**Phase 04 shipped** (`docs/phases/04-config-and-safety.md`): redaction,
`scribe on`/`off`, `status`, `diff`, `doctor`, global config layering and the
nudge. Every command in `PLAN.md`'s table is real; `cmd/scribe/stub.go` is
gone. Redaction was proven end to end against the real binary — a planted
`sk-ant-…` key and `ghp_…` token reach no writer prompt — which retires the
"it ships your session contents somewhere" risk from `PLAN.md` as *mitigated*,
not as solved: see item 33.

New items from phase 04: **33** (pattern-based redaction has limits), **34**
(live-agent coverage is thin), **35** (the fanout worktree defect).

**Every open item below now has a GitHub issue.** The issue is the thing you
assign or delegate; this file is the reasoning behind it. Neither is a
substitute for the other — an issue closed without the write-up landing here
loses why it mattered.

| Item | Issue |
|---|---|
| 28 — run the phase 03 eval corpus | [#9](https://github.com/Sahil-796/scribe/issues/9) |
| The phase 03 format migration | [#10](https://github.com/Sahil-796/scribe/issues/10) |
| 8 — fail-open guard never seen firing | [#11](https://github.com/Sahil-796/scribe/issues/11) |
| 33 — redaction patterns, both directions | [#12](https://github.com/Sahil-796/scribe/issues/12) |
| 34 — live-agent coverage | [#13](https://github.com/Sahil-796/scribe/issues/13) |
| 13 — opencode permissions pre-opened | [#14](https://github.com/Sahil-796/scribe/issues/14) |
| Pre-public scrub | [#15](https://github.com/Sahil-796/scribe/issues/15) |
| 35 — fanout worktree attribution | [#16](https://github.com/Sahil-796/scribe/issues/16) |

Everything that is broken, unproven, or waiting on a decision. Nothing here is
covered by a passing test, which is precisely why it's written down.

Kept current as phases land. Delete lines when they're genuinely done, not when
they're merely worked on.

**Resolved and deleted:** the name (locked to `scribe`), items 2, 3, 4, 5, 7, 9, 11,
12, 16, 17, 18, 19, 21, 23, 24, 25, and — in the close-out pass — 10, 15, 22, 26, 27,
29 and 30. Numbers are never reused: a gap means something was closed, and
`docs/phases/*.md` plus `docs/findings/*.md` carry the write-ups.

**Closed since the close-out pass:** 32 (Esc aborts the wizard — `9b0c1a8`, with the
filter trade-off documented in `withAbortKeys`), 31 (the Layout question is no longer
asked; `Config.Layout` stays for phase 06 — `6e32478`), 6 (see below), and 14 and 20,
both decided **won't-fix** rather than done: 14's real-scale `init` proving is what
item 28 covers in earnest, and 20's key reordering is cosmetic and would cost a JSON
AST dependency to avoid.

**Item 6 (`codex` install) was closed by re-diagnosing it, and the original entry was
wrong about which install was broken.** There were two: an orphaned homebrew copy at
`/opt/homebrew/lib/node_modules/@openai/codex` (0.130.0, `vendor/…/codex/` empty and
dated Aug 8 20:06 — the phase 00 kill), symlinked from `/opt/homebrew/bin/codex` and
therefore **first on `PATH`**; and `/usr/local/…` (0.147.0), which was complete and
working the whole time. So `sudo npm install -g @openai/codex` could never have fixed
it — npm's prefix is `/usr/local`, the copy that was already fine. Nothing managed the
homebrew tree (no `/opt/homebrew/bin/node`, no npm, no brew formula), so `npm
uninstall` could not reach it either; it was removed by hand, no sudo needed. `codex`
now resolves to `/usr/local/bin/codex`, `codex-cli 0.147.0`, which answers `--version`
with no TTY and no hang.

Two things that survive item 6 rather than closing with it: codex still has **never
been verified as a writer connector** — phase 00's sub-check (`docs/findings/00-writer.md`)
reached "codex is a genuine unknown" against the broken copy, and now that a working
binary exists that check is redoable but not redone. And 0.147.0 prints `WARNING:
failed to clean up stale arg0 temp dirs: Permission denied`, from root-owned temp dirs
left by the sudo install — cosmetic, but it will show up in any captured output.

---

## Unproven — claimed nowhere, but easy to assume

### 28. Phase 03 shipped without using the harness built to judge it — start here

[#9](https://github.com/Sahil-796/scribe/issues/9)

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

[#11](https://github.com/Sahil-796/scribe/issues/11)

`internal/worker` re-reads the docs after applying edits and treats "writer exited 0,
nothing changed" as a failure, without advancing the offset.

Phase 03 changed what counts: an all-`NO_CHANGE` run is now legitimate success, because
each per-doc call is allowed to decline on its own. The guard only fires when a call
claims a change and then echoes back identical content.

Across five live runs it has never fired — no false positives, but no observed real
auto-reject either, so the shape of a genuinely auto-rejected run's output is still an
assumption from phase 00's notes. The narrower definition makes a false positive less
likely and a missed true positive slightly more so.

### 13. opencode permissions are pre-opened on this machine — mitigated, not gone

[#14](https://github.com/Sahil-796/scribe/issues/14)

This machine's opencode has permissions opened globally via an `oh-my-openagent`
plugin config, so anything exercising the approval path can pass for the wrong reason.

`cmd/scribe/opencode_ask_guard_test.go` now makes phase 00's hand-rolled workaround
reusable: a helper that writes a project-local `ask` override so a test is forced
through opencode's real approval path. As of that pass, no test in the repo spawns a
real opencode process at all, so nothing is currently passing falsely — the guard
exists for whoever writes the first one.

Kept open because the environmental hazard is unchanged and applies to **live runs
done by hand**, which is exactly what item 28 involves. Anyone doing those must use
the override or their result says nothing about approval behaviour.

---

### 33. Redaction is pattern-based, and patterns miss

[#12](https://github.com/Sahil-796/scribe/issues/12)

`internal/redact` strips values bound to a configured key name, plus a handful
of shapes that are secrets by construction (`sk-`, `ghp_`, `AKIA`, bearer
tokens, PEM blocks, secret-looking `.env` lines). That is a large improvement
on nothing and it is not a guarantee. A secret in a shape nobody anticipated —
a customer record, an internal hostname, a password typed as prose — goes
through.

It also over-matches in the other direction on purpose: the `.env`-line
heuristic tests for substrings like `pat` and `key`, so `PATH=/usr/bin` is
redacted. Fail-safe is the right default, but it will put placeholders in
journals occasionally, and a redactor that eats too much prose is one people
switch off — which would be the worst outcome available.

Both directions are pinned by tests. Neither is settled.

### 34. Live-agent coverage is one run — better than zero, still thin

[#13](https://github.com/Sahil-796/scribe/issues/13)

Phase 04 closed with one real `opencode` 1.18.15 run on
`opencode/longcat-2.0-free`: a five-turn transcript with planted secrets,
through the real hook → queue → worker path. Neither secret reached the docs;
the journal entry recorded the wrong diagnosis alongside the right one.

It paid for itself immediately by finding a defect no fake could have —
`doctor`'s writer-check timeout was 8s against an 11.3s healthy round trip, so
it failed working installs. That is the argument for doing this every phase
rather than at the end of one.

A second run settled a question the first one raised. CHANGELOG had answered
`NO_CHANGE` on a session that shipped a fix, which looked like a miss; it was
not. That fixture had no code in it, and `CodeWeight: check` tells the writer
not to write up as done what it cannot confirm. Re-run against a repo
containing the claimed fix, CHANGELOG wrote the entry and cited a file path
that appears nowhere in the transcript — so it read the repo. Locked decision 7
works, and that is the first evidence for it.

Still open: one model, two transcripts. `codex` and `claude` as writer
connectors have never been exercised at all (see item 6).

### 35. Fanout in a shared worktree corrupts commit attribution

[#16](https://github.com/Sahil-796/scribe/issues/16)

Phase 04's four agents committed into one worktree. `git commit` with no
pathspec commits the whole index, so an agent's staged files were repeatedly
swept into whichever other agent committed next: `08e09eb`, `694eb86` and
`590e779` all carry a message that does not match their contents. No work was
lost and the diffs were verified; history was deliberately not rewritten while
agents were still committing.

The fix is one line in the dispatch prompt — commit with an explicit pathspec
(`git commit -- <paths>`), which ignores the index — or give each unit its own
worktree. Recorded here because it will recur on every future fanout otherwise.

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
  switch is still worth doing — [#15](https://github.com/Sahil-796/scribe/issues/15).
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
