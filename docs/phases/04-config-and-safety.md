# Phase 04 — config and safety

**Status: shipped on `phase-04-config-safety`, reviewed, and proven end to
end against the real binary. No command in `PLAN.md`'s table returns
`notImplemented` any more — `cmd/scribe/stub.go` is deleted, because nothing
imports it.**

Four parallel units plus hand integration. The spec this was built to is
preserved below in *What was specified*; everything above it is what actually
happened.

---

## The claim this phase is allowed to make

A transcript containing a planted `sk-ant-…` key and a `ghp_…` token, fed
through the real `scribe` binary — Stop hook, queue, worker, writer — produces
writer prompts containing neither. The sentence that carries the actual
engineering content, *"the api_key was wrong so I regenerated it"*, survives
intact.

That was verified by driving the binary, not by reasoning about the prompt
text. It is the one claim phase 03 could not make about its own premise, and
`OPEN-ITEMS.md` item 28 still records that.

## What shipped

| Unit | Package | Does |
|---|---|---|
| A | `internal/redact` | The choke point. Key-bound value stripping, high-signal shapes regardless of key, `**`-aware ignore globs |
| A | `internal/worker`, `replay`, `seed` | Every prompt builder routed through it; `CodeWeight` wired end to end |
| B | `cmd/scribe/on_off.go`, `internal/hook` | `scribe off [--stay]` / `scribe on`, enforced in the hook |
| C | `internal/docs/lastrun.go`, `cmd/scribe/{status,diff}.go` | Bounded run snapshot; the two commands you reach for when you don't trust it |
| D | `internal/install/global.go`, `internal/nudge`, `cmd/scribe/{doctor,nudge}.go` | Global config layering, the nudge, doctor's eight named checks |

**Redaction is a choke point, not a sprinkling.** A nil redactor is an error at
every entry point — `worker.Run`, `replay.Run`, `seed.Run` all refuse to start
without one — rather than a pass-through. The failure mode of the optional
version is invisible and permanent. A test enumerates the prompt builders and
fails if any of them learns a second route to transcript content.

**The redactor has to be judged in both directions.** Stripping secrets is
half the job; the other half is that `JOURNAL.md`'s entire value is sentences
like "the api_key was wrong so I regenerated it." A redactor that eats those is
one nobody leaves switched on. Matching is therefore structural — a value
*bound* to a key, never the key's every appearance.

**Pausing is separate state from `Enabled`.** Onboarding and pausing are
different questions; folding them together would make `scribe off` un-onboard a
repo. Expiry is evaluated on read, so nothing has to wake up, and a pause that
lapsed while the machine was asleep is simply over. The hook declines to
enqueue while paused — a pause that still queued would be a delay that dumped
everything into the writer the moment it lapsed.

**`scribe diff` reads a recorded snapshot, not git.** Git is the undo (decision
10) but nothing commits the docs, so `git diff` shows everything since the last
human commit. The snapshot is bounded to one run, written atomically, and
annotates a rotation from its own recorded pointer count so entries moving into
`docs/scribe/archive/` don't render as the writer having deleted the journal.

## Deviations from `PLAN.md`, taken deliberately

**Config is JSON, not TOML.** `PLAN.md` sketches `~/.config/scribe/config.toml`
plus `.scribe/config.toml`. Phase 02 shipped `.scribe/config.json` with tests
and a live install path. Adding a TOML parser to a repo whose whole dependency
list is cobra + huh, to run a second serialisation format alongside the one
already on disk, buys nothing a user can see and costs a migration. Global
defaults land at `~/.config/scribe/config.json`, same shape, repo config wins
field by field. The field names and the layering `PLAN.md` describes are
honoured; the extension is not.

**`[code]`'s two keys collapsed to one.** `PLAN.md` lists `read` and `weight`.
They are not independent: `read = false` and `weight = "off"` say the same
thing, and every other combination is a state with no meaning. One field cannot
express the contradiction.

**The nudge is opt-in and has its own command.** It must fire in repos scribe
has installed nothing in, so it needs a hook in the user's *global*
`~/.claude/settings.json` — their file, not scribe's to touch silently. No new
wizard question: phase 03 closed item 31 by *removing* one, and adding one back
for a nudge goes the wrong way. `scribe init` mentions the command in one line
and does nothing else.

## What review caught that the units did not

Consistent with phase 03's finding that subagent self-reports are not review.
All four units reported success. Four defects survived that:

1. **`ResetRun` had no caller.** `internal/docs` records a run's before/after
   only once `ResetRun` has created the file, and the call site belongs in
   `internal/worker` — a package unit C did not own and did not flag. Left
   alone, `scribe diff` would compile, pass every test it owns, and answer "no
   run has been recorded yet" forever against a live repo. It is now on the
   `DocStore` interface rather than an optional type assertion, so a store
   lacking it is a compile error rather than a silent no-op.
2. **`status` and `diff` asked the wrong question about repo roots.** Both
   called `hook.FindRepoRoot`, which walks up looking for `.scribe` — a
   directory that only exists once `init` has run. Both therefore answered "Not
   a git repository" in a never-initialised repo, the exact case they exist to
   explain. Unit C's own test caught this and could not run, because
   `cmd/scribe` did not compile until integration.
3. **`applySpans` could leak a straddling span's tail.** A span starting inside
   an already-applied span but ending past it was dropped, and its tail emitted
   verbatim. Whether the current patterns can produce such a pair is not
   obvious either way — which is the reason to handle it rather than reason
   about it. Now tested against `applySpans` directly with hand-built spans, so
   the answer stops depending on what the regexes happen to emit.
4. **`seed.Run` and `replay.Run` never took the redactor** unit A threaded
   through their prompt builders, leaving both entry points able to construct a
   fully unredacted pass. That one is the orchestrator's fault, not A's: A was
   scoped to `prompt.go` and `scan.go` without being given the callers in the
   same packages.

And one found by using the thing rather than reading it:

5. **The `custom` writer connector was unreachable.** `PLAN.md` decision 6
   promises a raw-command escape hatch, and `writer.New` has implemented it
   since phase 01 — but `install.Config` had nowhere to record the command, and
   every caller built `writer.Config` from `Agent` and `Model` alone. It failed
   every time with "custom agent requires Config.Args or Config.Command" and no
   way to supply either. Surfaced by needing a prompt-recording writer for the
   acceptance run, which is precisely what `custom` is for.

## The process defect worth recording

**Four agents committing into one shared worktree corrupted commit
attribution.** `git commit` with no pathspec commits the whole index, so an
agent's `git add` was repeatedly swept into whichever other agent committed
next. Three commits carry the wrong message for their contents — `08e09eb`
(unit B's message, unit C's `internal/docs` files), `694eb86` (unit D's
message, unit C's `status.go`), and `590e779` (the orchestrator's message, plus
unit D's `internal/nudge`). No work was lost; every file landed exactly once
and the diffs were verified. History was deliberately not rewritten: agents
were still committing, and rewriting shared history under them is worse than a
wrong message.

Two of the three agents diagnosed this correctly and unprompted. The fix for
next time is one line in the dispatch prompt: **commit with an explicit
pathspec** (`git commit -- <paths>`), which ignores the index, or give each
unit its own worktree.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass.

Beyond the suite, the real binary was driven through: an uninitialised repo
(`status`, `diff`, `on` and `doctor` all answer usefully, `doctor` exits 1 with
eight named checks), a live repo (a run that changes docs, `diff` rendering the
unified diff, `status` reporting what changed), and a paused repo (`off` naming
its expiry, the hook enqueueing nothing, `run` refusing, `on` resuming). The
redaction check above used a `custom` writer that records its prompts, so what
was asserted is what an agent would actually have received.

## What is NOT proven

**No real writer agent was ever invoked.** Every check above used fakes or the
recording `custom` connector. `doctor`'s "writer answers a trivial prompt" check
has never been run against a live `opencode`.

**The nudge's hook has never fired for real.** `scribe nudge --install` writes a
SessionStart hook to the user's global Claude settings; that path is tested
against a redirected `HOME`, never against the real file, by design.

**Redaction is pattern-based, and patterns miss.** It catches what it was told
about plus a handful of shapes that are secrets by construction. It is a large
improvement on nothing and is not a guarantee. The `.env`-line heuristic also
over-matches — `PATH=` trips the "pat" substring — which is the fail-safe
direction on purpose, but it will put placeholders in journals occasionally.

**The format migration from phase 03 is still open.** Any repo onboarded before
phase 03 has the old plain-concatenated history format and nothing migrates it.

---

## What was specified

The original spec, kept for the record: off by default with per-repo opt-in and
`scribe off` / `on`; the config from `PLAN.md` plus the connector shape;
the uninitialised-repo nudge; redaction before anything leaves the machine; and
`scribe status`, `scribe diff`, `scribe doctor`. Four units with hard file
ownership, `cmd/scribe/run.go` and `root.go` left unowned as the integration
points — which is what kept this phase, like 02 and 03, at zero contested
files.

## Next

1. Item 28 is still the cheapest open question in the project: run the phase 03
   eval corpus, old prompt against new, and let the rubric say whether phase 03
   worked. Phase 04 does not touch it.
2. Decide the phase 03 format migration before anything real is onboarded.
3. Point `doctor`'s writer check at a live `opencode` once, by hand, and see
   whether it says anything useful.
4. Phase 05: the weekly digest and the session index.
