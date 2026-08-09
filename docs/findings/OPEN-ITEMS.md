# Open items — as of the phase 03 close-out pass

Everything that is broken, unproven, or waiting on a decision. Nothing here is
covered by a passing test, which is precisely why it's written down.

Kept current as phases land. Delete lines when they're genuinely done, not when
they're merely worked on.

**Resolved and deleted:** the name (locked to `scribe`), items 2, 3, 4, 5, 7, 9, 11,
12, 16, 17, 18, 19, 21, 23, 24, 25, and — in the close-out pass — 10, 15, 22, 26, 27,
29 and 30. Numbers are never reused: a gap means something was closed, and
`docs/phases/*.md` plus `docs/findings/*.md` carry the write-ups.

---

## Needs you — decisions nobody else can make

### 32. Should Esc back out of the init wizard?

`wizard.go` claimed Ctrl+C **and** Esc aborted the form. Only Ctrl+C does: huh v1.0.0
binds `Quit` to `ctrl+c` alone, verified against the vendored source and live through a
pty. The comment is corrected and a test documents the real behaviour.

What's undecided is whether that's a gap or the intent. Esc-to-cancel is a common
terminal convention and its absence is the kind of thing an operator discovers by
pressing Esc twice and then killing the terminal. Adding the binding is small; it's a
product call, not a bug fix.

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

### 13. opencode permissions are pre-opened on this machine — mitigated, not gone

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

### 14. `scribe init` — run for real only on scratch repos

`scribe init --apply` has been run against throwaway git repos with a real `opencode`
writer. It installs the Stop hook, writes the config, and produces docs judged
genuinely good rather than filler.

Still unproven: `init` against a repo with substantial existing history (the replay
pass at real scale), and against a repo whose README and manifests are messier than a
scratch fixture's. The seed pass has only ever seen a small, tidy repo. Phase 03
doubled replay's calls per chunk, so a large history now costs twice what it did.

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
