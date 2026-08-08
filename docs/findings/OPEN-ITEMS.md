# Open items — as of end of phase 02

Everything that is broken, unproven, or waiting on a decision. Nothing here is
covered by a passing test, which is precisely why it's written down.

Kept current as phases land. Delete lines when they're genuinely done, not when
they're merely worked on.

**Resolved:** the name. User has locked it to `scribe`. Phase 02 built `docs/scribe/`
and `.scribe/` on that name; no longer blocking.

---

## Needs you — decisions nobody else can make

### 2. Default writing model — plan change awaiting sign-off

`PLAN.md` uses `opencode/deepseek-v4-flash-free` as its example. Phase 00 graded it
third of three: substance right, prose visibly glitched, misspelled a company name.
**Recommendation: `longcat-2.0-free`.** Someone should confirm before it's baked into
phase 04's config defaults. See `00-models.md`.

### 3. `docs/scribe/` is gitignored — my call, not yours

I added it to `.gitignore` on day one. `PLAN.md` lists "committed or gitignored" as an
open question and says "gitignore for the first week regardless," so this matches the
plan — but it was still my unilateral decision and it sits awkwardly with git being
the only undo. Revisit before anyone relies on it.

### 4. Cross-project content in the bakeoff evidence

`experiments/00-model-bakeoff/outputs/` and parts of `00-models.md` are derived from a
real `scout` session — another private repo of yours. Small, derived summaries, not
raw transcript, and this repo is private. The full 924-line rendering was removed
before any push. **Your call whether to scrub the rest.** It's the evidence for the
gate verdict, so scrubbing costs reviewability.

### 5. Phase 06 file layout — still blocked

Unchanged from `PLAN.md`: options A (one file per session), B (one file per person),
C (shared, live with conflicts). Nothing in phases 00–01 resolved it. Note that phase
00's transcript work makes A cheaper than it looked.

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

### 7. The loop has never run live. Phase 01's done-when is NOT met

Nothing has run `opencode` end to end and produced real docs in a real repo. Every
phase 01 test uses fakes or stub executables. The plan's bar — *"work for an hour,
touch nothing, and the four docs are current afterwards"* — has not been demonstrated.

**This is the single most important thing to do next.** Everything else is built on
the assumption that it works.

### 8. No fail-open guard — the highest-value small fix on the board

Phase 00 found `opencode run` without `--auto` **silently auto-rejects and exits 0**
with normal-looking JSON. The phase 01 worker currently treats exit 0 as success.

Until this is fixed, scribe can report success on every run while writing nothing at
all, indefinitely, with no signal. Fix: compare doc state before and after; treat
"exit 0, nothing changed" as a failure.

### 9. `isSidechain` filtering is unverified against reality

`PLAN.md` states this trap as confirmed. **Zero** such lines were found across 68 real
transcripts. The filter is implemented and passes synthetic fixtures, but no real data
has ever exercised it. Either the trap is rarer than believed, or it's recorded
differently now. Settle before phase 03 relies on it.

### 10. Stop-hook behaviour during subagents is unconfirmed

Only one Stop event fired for a `Task` exchange, but the run couldn't be confirmed to
have delegated at all. Related to item 9 — both concern subagent turns, and both are
guesses right now.

### 11. Hook failures may go nowhere

A Stop hook exiting non-zero doesn't visibly break the session. Nobody checked whether
that failure is logged anywhere. If it isn't, `scribe doctor` has nothing to inspect
and silent hook death means docs stop updating with no signal — the same failure shape
as item 8, one layer up.

### 12. Writer timeout is unsized

Cold starts measured 6–26 s with high variance — too noisy to pick a production
timeout from. The current value is a guess. Needs real measurement under load before
phase 04 ships config.

### 13. opencode permissions are pre-opened on this machine

This machine's opencode has permissions opened globally via an `oh-my-openagent`
plugin config. Tests here can pass for the wrong reason. Phase 00 worked around it
with a forced local `ask` override; anything testing the approval path must do the
same or it proves nothing.

### 14. `scribe init` has never been run against a real repo with a real writer

Same shape as item 7, one phase over. `internal/seed`, `internal/replay`,
`internal/install`, `internal/wizard`, and `cmd/scribe/init_test.go` all test against
a fake `scribe.Writer` and `t.TempDir()`. Nobody has watched `init` produce a real
PROJECT.md from a real README or real CHANGELOG entries from a real transcript.

### 15. The wizard's interactive path is entirely untested

No pty in this environment. Unverified: actual huh form rendering and field
navigation, validation messages, the `huh.ErrUserAborted` (Ctrl+C/Esc) branch in
`Ask`/`Review`, the interactive redraw branch of `Progress`, and `Ask`/`Review`
returning the operator's actual chosen values on the happy path. Only the
not-interactive short-circuits are covered.

### 16. Replay resumability is unverified at the `init` integration level

`internal/replay`'s own tests cover chunk-by-chunk resume directly.
`cmd/scribe/init_test.go` doesn't exercise it — temp repos in tests have no matching
`~/.claude/projects/` sessions on disk, so an interrupted-then-resumed multi-chunk
replay has never run through `scribe init` end to end.

### 17. The docs-dir wizard question is decorative

`install.Config.DocsDir` is asked for, stored, and displayed, but
`internal/docs.Store` hardcodes `scribe.DocsDir` regardless. Matches locked Decision 9
("fixed name, nothing to detect"), so may be intentional — but as written the wizard
asks a question whose answer does nothing. Needs a call: drop the question, or wire it
through.

### 18. `init`'s empty-replay check is stringly-typed

It detects "chunks processed but nothing emitted" by matching a `" (already done)"`
suffix on `replay`'s human-readable progress labels. Works today, but coupled to a
display string rather than a typed signal on `replay.Options`.

### 19. Onboarded repos get no `.gitignore` entries from `init`

This repo's own `.gitignore` covers `.scribe/` and `docs/scribe/`, but `scribe init`
doesn't add equivalent entries to the repo it onboards. Nobody decided what `init`
should do here — leave it, warn, or write the entries.

### 20. `install` re-encodes `settings.json` through `encoding/json`, which alphabetises top-level keys

All keys and values survive; original key order does not. Byte-exact preservation
would need a JSON AST library, which isn't a dependency.

---

## Process notes

- **Neither PR is merged.** [#1](https://github.com/Sahil-796/scribe/pull/1) is phase
  00, [#2](https://github.com/Sahil-796/scribe/pull/2) is phase 01.
- **They were opened as siblings, not a stack** — no file overlap, no code dependency.
  This branch has since merged phase 00 in, so continuing from `phase-01-loop` gives
  you everything. That does mean PR #2's diff now includes phase 00's files.
- Phase 01 was built before phase 00's gate verdict was in, at the user's request.
  The gate passed, so this cost nothing — but the writer connector was being built on
  an unvalidated assumption for the duration.
