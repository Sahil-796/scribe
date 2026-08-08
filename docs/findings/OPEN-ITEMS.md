# Open items — as of end of phase 02, plus the phase 00/01 fixes

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

### 7. The loop runs live now, but the done-when is still NOT met — see item 21

**Partly closed.** The loop was run end to end against real `opencode` in a throwaway
repo: `scribe init --apply`, a real Stop hook, `scribe run`, and CHANGELOG/JOURNAL were
genuinely updated with accurate content. Full write-up in `07-live-run.md`.

Three bugs were found and fixed doing it — all of them meant the loop could not have
worked at all before:

- `opencodeArgv` passed `--print --auto-approve`, neither of which is a real flag.
  The correct argv is `run [--model M] --auto <prompt>`, verified against
  `opencode run --help` (1.18.15).
- The default model lacked its provider prefix: `opencode/longcat-2.0-free`, not
  `longcat-2.0-free`.
- **`scribe run` was still `notImplemented` and nothing anywhere called
  `internal/worker`.** Phase 01 shipped a complete, tested worker with no caller.
  It is now implemented, and `scribe hook` spawns a detached `scribe run` after
  enqueueing, so the loop closes without human intervention.

Still not met: *"work for an hour, touch nothing, and the four docs are current
afterwards."* One session in a scratch repo is not an hour of real work, and see
item 21 for why the unattended path currently ends without docs landing.

### 8. Fail-open guard — landed, but never seen firing on a real auto-reject

**Fixed.** `internal/worker` now re-reads the docs after applying edits and treats
"writer exited 0, nothing changed" as a failure; the offset does not advance. Pinned
by a failing test first, covering both zero parsed edits and edits identical to
existing content. A trigger that yields no new transcript entries is still a
legitimate no-op, not an error.

Unproven: the guard has only ever fired against a fake writer. Nobody has watched it
catch a genuine `opencode` auto-reject, so the shape of a real auto-rejected run's
output is still an assumption from phase 00's notes. See item 21 — a real run did trip
the guard, but whether that was a true positive is exactly what's unclear.

### 9. `isSidechain` — settled

**Resolved.** Surveyed all 317 real transcript files on this machine. In the 120
top-level session transcripts (38,279 lines) `isSidechain: true` appears **zero**
times. Every line of the 197 files in per-session `subagents/` subdirectories has it
`true`. Subagent turns are not interleaved into the main transcript at all — they are
written to separate files, and the Stop hook's `transcript_path` only ever names the
main one, so `Read()` never opens them.

The plan was right about the field and wrong about the mechanism. What protects scribe
is file separation, not the filter. The filter is kept as cheap insurance and its
comments now say so. Evidence in `09-sidechain.md`.

### 10. Stop-hook behaviour during subagents — better evidence, not conclusive

One real burst in this repo's own history showed 7 back-to-back subagent delegations
followed by exactly one `stop_hook_summary`. That supports "one Stop per turn
regardless of delegation count" but is a single observation, not a proof.

### 11. Hook failures — Claude Code does record them; scribe now does too

**Closed.** Established empirically with a live probe (a Stop hook that exits 1, run
under a real `claude -p` session): Claude Code writes a `hook_non_blocking_error`
attachment into that session's own transcript, carrying exit code, stderr, command and
duration. So the failure was never going nowhere — but it was per-session, unaggregated,
and somewhere `scribe doctor` had no reason to look.

`scribe hook` now appends failures to a bounded (50-line) `.scribe/hook-failures.log`,
and `scribe doctor` surfaces recent entries. Logging can never change the hook's exit
code. Known limit: the log's read-modify-write is not process-locked, so two
simultaneous failures in one repo could drop a line.

### 12. Writer timeout — measured, current value supported

**Closed at the current value.** Five real `opencode` runs measured 7.8–15.6 s, mean
~10.2 s; combined with phase 00's 6–26 s, the existing 3-minute timeout has ample
headroom. Not tightened — the variance doesn't justify it.

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

### 21. The unattended loop runs, but the docs don't land — start here

**The most important open item now, and the direct successor to item 7.**

With everything above fixed, a real `claude -p` Stop hook in a throwaway repo caused
`scribe hook` to enqueue and automatically spawn `scribe run`, which took the per-repo
lock and called real `opencode` with nobody touching anything. The automation works.

But that run then failed the item-8 guard:

```
worker: writer "opencode" exited successfully but changed no docs
```

No offset file was written, and a manual re-run reproduced it. **What's unknown is
whether the guard is right.** Either `opencode` genuinely made no edit — a true
positive, and the guard doing exactly its job — or it did edit and the content
round-tripped to identical bytes, making this a false positive that will block every
run. The same command path worked earlier in a manual smoke test, which is what makes
this worth diagnosing rather than guessing.

Needs someone in `internal/worker` / `internal/docs` / `internal/writer` with the real
stdout of a failing run in hand.

### 22. Concurrency under real triggers is still unproven

Offset advancement across sessions, multi-trigger coalescing, and the per-repo lock
under genuine contention have only been exercised by `internal/worker`'s fakes. Live,
only a single uncontended lock acquire/release has been observed.

### 23. Hook spawn failures are not logged

`scribe hook` swallows a failed spawn of `scribe run` rather than logging it, because
`internal/hook`'s failure logger is unexported and was outside the allowlist of the
change that added spawning. The trigger stays safely queued, but a persistently
failing spawn is invisible — the same failure shape as item 11.

---

## Process notes

- **No PR is merged.** [#1](https://github.com/Sahil-796/scribe/pull/1) is phase 00,
  [#2](https://github.com/Sahil-796/scribe/pull/2) is phase 01. Phase 02 and these
  fixes are stacked on top as two further PRs.
- **#1 and #2 were opened as siblings, not a stack** — no file overlap, no code
  dependency. `phase-01-loop` has since merged phase 00 in, so PR #2's diff includes
  phase 00's files.
- Phase 01 was built before phase 00's gate verdict was in, at the user's request.
  The gate passed, so this cost nothing — but the writer connector was being built on
  an unvalidated assumption for the duration.
- Phase 02 and the fixes above were built by a fleet of Sonnet subagents with hard
  file-ownership allowlists and a scope-diff gate. Zero out-of-scope files, zero
  contested files. It was also expensive — twelve agents, each reading the codebase
  cold, and the agents driving real `opencode` and `claude -p` sessions dominated
  both wall-clock and token cost. Fan out on breadth; don't fan out on work that is
  mostly waiting on a slow external process.
