# Open items — as of end of phase 02, plus the phase 00/01 fixes

Everything that is broken, unproven, or waiting on a decision. Nothing here is
covered by a passing test, which is precisely why it's written down.

Kept current as phases land. Delete lines when they're genuinely done, not when
they're merely worked on.

**Resolved:** the name. User has locked it to `scribe`. Phase 02 built `docs/scribe/`
and `.scribe/` on that name; no longer blocking.

---

## Needs you — decisions nobody else can make

*All four standing decisions were answered on 2026-08-09. The answer to three of them
was the same: **stop hardcoding, ask at onboarding.** That is now what `scribe init`
does.*

### 2. RESOLVED — the model is a question, not a constant

The wizard offers the three models phase 00 actually graded, ranked, with the bakeoff's
winner pre-selected and a free-text option for anything else. `PLAN.md`'s example
config now says `opencode/longcat-2.0-free` so the plan stops contradicting the code.
`DefaultModel` remains only as the non-interactive fallback for `--yes` and CI.

### 3. RESOLVED — asked at onboarding

`init` asks whether to commit the docs and records `docsInGit`. `docs/scribe/` is
gitignored only when the answer is no. `.scribe/` is always ignored — it is local
state, not a choice.

### 4. RESOLVED — moot

`scout` is public, so the bakeoff evidence was never cross-repo-private content. The
evidence stays.

One thing this *does* change: **this repo is going public.** The standing rule that no
raw transcript content may be committed stops being a courtesy and becomes the actual
requirement. `.gitignore` already blocks the known pattern and the phase 02 survey
confirmed the findings docs carry only aggregate counts — worth one deliberate pass
over `docs/findings/` and `experiments/` before flipping the switch.

### 5. RESOLVED — build both, ask at onboarding

Per-session and shared, chosen per repo at `init` time and recorded as
`layout: per-session | shared`. Option B (one file per person) is dropped — it is A's
conflict-freedom with worse readability. Phase 06 builds to whichever the config says;
the question and the config field shipped in phase 02.

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

## Resolved this pass — kept for the record, delete once read

Items 7, 9, 11, 12, 16, 17, 18, 19, 21 and 23 are done. They are written up below
rather than deleted so the next reader can see what changed and why.

## Unproven — claimed nowhere, but easy to assume

### 7. RESOLVED — the loop runs unattended and the docs land

The loop was run end to end against real `opencode` in a throwaway repo:
`scribe init --apply`, a real Stop hook, `scribe run`, and CHANGELOG/JOURNAL genuinely
updated with accurate content. Full write-up in `07-live-run.md`.

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

**Demonstrated.** Four live runs against real `opencode` in throwaway repos: manual
`scribe run`, a trivial session, and twice fully unattended — hook fires, spawns the
detached worker, docs update, offset saves, nothing touched. Output quality is good:
one run caught a regression the transcript only implied, flagged a new dependency, and
recorded an open question.

The remaining honest caveat is scale, not mechanism: this is single sessions in scratch
repos, not an hour of real work in a repo with history. Item 24 is the quality problem
that showed up while proving it.

### 8. Fail-open guard — landed, but never seen firing on a real auto-reject

**Fixed.** `internal/worker` now re-reads the docs after applying edits and treats
"writer exited 0, nothing changed" as a failure; the offset does not advance. Pinned
by a failing test first, covering both zero parsed edits and edits identical to
existing content. A trigger that yields no new transcript entries is still a
legitimate no-op, not an error.

Unproven: the guard has only ever fired against a fake writer. Across four live runs
it never fired once — no false positives, but also no observed real auto-reject, so the
shape of a genuinely auto-rejected run's output is still an assumption from phase 00's
notes.

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

### 14. `scribe init` — run for real once, on a scratch repo

**Mostly closed.** `scribe init --apply` was run against a throwaway git repo with a
real `opencode` writer during the item-7 live run. It installed the Stop hook, wrote
the config, and produced docs the write-up judged genuinely good rather than filler.

Still unproven: `init` against a repo with substantial existing history (the replay
pass at real scale), and against a repo whose README and manifests are messier than a
scratch fixture's. The seed pass has only ever seen a small, tidy repo.

### 15. The wizard's interactive path is entirely untested

No pty in this environment. Unverified: actual huh form rendering and field
navigation, validation messages, the `huh.ErrUserAborted` (Ctrl+C/Esc) branch in
`Ask`/`Review`, the interactive redraw branch of `Progress`, and `Ask`/`Review`
returning the operator's actual chosen values on the happy path. Only the
not-interactive short-circuits are covered.

### 16. RESOLVED — replay resume is now covered through `init`

`TestInit_ReplayResumesAfterAFailedChunk` plants a synthetic transcript under a
redirected `HOME` (never the real history) and drives three runs: chunks that failed
are retried, chunks that succeeded are skipped. It asserts the first run actually
produced chunks, so it can't pass vacuously.

### 17. RESOLVED — the question is gone

Decision 9 fixes the path, so the honest fix was to stop asking. The wizard's "Docs
directory" field and `init`'s `--docs-dir` flag are both removed; the setup note states
the path instead. `install.Config.DocsDir` still records it, so wiring it through later
remains possible if decision 9 is ever reopened.

### 18. RESOLVED — typed signal replaces the string match

`replay.ChunkStatus` (`ChunkWritten` / `ChunkSkipped` / `ChunkFailed`) is now passed to
`Options.Progress`, and `init` switches on it. No caller reads the display label to
make a decision any more.

### 19. RESOLVED — `init` writes the entries

`scribe init --apply` appends `.scribe/` and `docs/scribe/` to the onboarded repo's
`.gitignore`, preserving what's already there and never duplicating an entry. Verified
live: after onboarding, `git status` shows none of scribe's own files.

**Note this pre-empts item 3.** Ignoring `.scribe/` is uncontroversial local state, but
ignoring `docs/scribe/` follows PLAN.md's "gitignore for the first week regardless"
rather than a decision you've made. `gitignoreEntries` in `cmd/scribe/init.go` is the
single place to change if you land on committing them.

### 20. `install` re-encodes `settings.json` through `encoding/json`, which alphabetises top-level keys

All keys and values survive; original key order does not. Byte-exact preservation
would need a JSON AST library, which isn't a dependency.

### 21. RESOLVED — not reproducible

Four live runs later, this never recurred: manual `scribe run`, a trivial session, and
two fully unattended hook-driven runs all updated the docs and saved offsets. The guard
did not fire once.

The original observation stands unexplained rather than disproven — it was seen once,
during a run driven by a real `claude -p` session, and the specific stdout was not
captured. If it returns, capture the writer's raw stdout first; that's the one piece of
evidence that was missing.

### 22. Concurrency under real triggers is still unproven

Offset advancement across sessions, multi-trigger coalescing, and the per-repo lock
under genuine contention have only been exercised by `internal/worker`'s fakes. Live,
only a single uncontended lock acquire/release has been observed.

### 23. RESOLVED — spawn failures are logged

`internal/hook.LogFailure` is now exported and `cmd/scribe/hook.go` records a failed
spawn to the same bounded log `scribe doctor` reads. The hook still exits 0 — the
trigger is safely queued — but the failure is no longer invisible.

### 24. The writer documents trivial exchanges — phase 03's problem, now with evidence

A live run on a throwaway "what go version is this repo on?" exchange produced its own
JOURNAL entry. Nothing was wrong mechanically; the model simply wrote up something not
worth writing up.

This is exactly what PLAN.md's phase 03 exists to fix ("PROJECT and DECISIONS gated
behind did anything product-level actually happen", "teach the journal what's worth
capturing"). Worth keeping the reproduction: it's a two-line transcript and a 30-second
run, which makes it a cheap test case for the gating work.

### 25. `init` reported docs it never wrote — fixed, noted for the pattern

`init` printed `Wrote docs/scribe/{PROJECT.md,DECISIONS.md,CHANGELOG.md,JOURNAL.md}`
unconditionally, while a repo with no past sessions only ever gets two. Now it lists
what actually landed and says why the other two are empty. Flagged because it's the
same family as item 8: reporting success for work that didn't happen.

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
