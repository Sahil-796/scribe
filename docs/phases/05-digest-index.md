# Phase 05 — digest and index

**Status: shipped on `phase-05-digest-index`, branched off
`phase-04-config-safety` (phase 04 is not yet merged to `main` — PR #8).
`go build ./...`, `go vet ./...`, `go test ./...` all pass.**

Three new packages plus hand integration, the same shape phases 02–04 took.
A fanout of three Sonnet units built the packages against a contract fixed up
front; the orchestrator wired them into the worker and `scribe run`. Zero
out-of-scope files, zero contested files — the fourth phase in a row at that.

---

## What this phase delivers

Two skimmable views of history, both derived — never a second source of
truth:

| Artifact | Written by | Shape |
|---|---|---|
| `docs/scribe/INDEX.md` | `internal/index` | One line per session, newest first: `- 2026-08-21 · feat · <summary>` |
| `docs/scribe/digests/YYYY-Www.md` | `internal/digest` | One ISO week, sessions grouped Features / Bugs / General |

Both are **pure renders of a list of session records** — no writer call
happens at render time. The only model work phase 05 adds is one cheap call
per session per run that produces that session's one-line summary and a
category; everything downstream is deterministic markdown assembly.

## The three packages

| Package | Does | Key contract |
|---|---|---|
| `internal/sessions` | The record store: `<repoRoot>/.scribe/sessions.json`, atomic upsert keyed by session id | `Upsert` **preserves the earliest `Started`** across runs; `WeekKey`/`ByWeek` are the one definition of "which ISO week", ISO-year-correct near Jan 1 / Dec 31 |
| `internal/index` | Renders `INDEX.md` from `[]Record` | `Render` is pure and deterministic; newlines in a summary are flattened so one session can never become two lines |
| `internal/digest` | Renders per-week digests; `MaybeWrite` backfills any complete-but-missing past week | The **current** week is never digested; existing digest files are never overwritten (a past week is immutable once written) |

## "No scheduler — the first run of a new week writes last week's digest"

`digest.MaybeWrite(repoRoot, records, now)` is called on every run. It buckets
records by ISO week and writes a digest for every week that is *strictly
earlier* than the week containing `now` and does not already have a file. So:

- The first run on or after a week boundary materialises the week that just
  ended — no cron, no timer, nothing to wake up.
- It also **backfills** older weeks that never got written — e.g. scribe was
  paused across a boundary, or `init` replayed a long history at once. Any gap
  fills itself on the next run.

The current, still-in-progress week is deliberately never digested: a digest
is a closed record of a finished week.

## How a session becomes a record

`internal/worker` gained one optional dependency, `Sessions`, and one new
prompt builder, `buildSummaryPrompt`. After a run's doc-writing finishes —
whether it wrote docs or every doc call came back `NO_CHANGE` — `finishRun`
makes one summary call per session that had new transcript entries and upserts
the result. A session that happened earns an index line even on a quiet run,
because *work occurred* is a different question from *the docs changed*.

**Recording is best-effort, exactly like the `scribe diff` snapshot.** A
failed summary call (or a nil `Sessions`) is logged and the run's offsets
still advance. The index and digests are a view of history, not history
itself; failing a doc-writing run that already succeeded because a secondary
view couldn't refresh would be the tail wagging the dog. The cost of a
transient hiccup is one missing index line, not a permanently re-read
transcript slice.

The summary prompt goes through the phase 04 redaction choke point like every
other builder, and is added **by name** to
`TestRedactionChokePointCoversEveryPromptBuilder` — the exhaustive,
non-reflective list that phase 04 built so a new builder can't quietly skip
redaction.

## Deviations and choices worth recording

- **`sessions.json` lives in `.scribe/`, not `docs/scribe/`.** It is machine
  state — the source the two views render from — not a doc a human reads.
  `.scribe/` is always gitignored (PLAN, "Config"), so the raw record set
  never enters git; the rendered `INDEX.md` and digests sit under
  `docs/scribe/` and follow that repo's `docsInGit` choice like the four docs.
  No new gitignore rule was needed — both directories are already ignored at
  directory level.
- **Empty digest sections are omitted**, not printed as "_None_" — a week with
  no bugs shows no Bugs heading. Keeps a light week's digest tight.
- **`INDEX.md` is one flat reverse-chronological list**, no weekly
  sub-headers. Grouping by week is the digest's job; the index's job is a fast
  scan for "when did I touch X".
- **Category defaults to general** when the writer returns an unrecognised or
  missing label. Getting the one-line summary recorded matters more than a
  perfect tag, and a present-but-wrong label still tells a human a session
  happened. A missing *summary* line, by contrast, is a hard error — recording
  a blank line would be indistinguishable from a real "nothing to say".

## What is NOT done

- **`scribe init`'s replay does not populate the session store.** INDEX.md and
  the digests therefore cover sessions from phase 05 forward, not a repo's
  back-history. This is the one place phase 05 falls short of the project's
  "useful on day one" principle, and it is deliberate: replay has its own
  writer-call budget and chunking, and adding a per-session summary pass there
  is its own integration. Filed as OPEN-ITEMS item 36. The digest backfill
  machinery is already built for it — once replay writes records, past-week
  digests materialise on the next run with no further work.
- **No live-agent run yet.** Everything here is proven against fakes and the
  deterministic render tests. A real `opencode` run through the summary path —
  does a cheap model actually return the two-line CATEGORY/SUMMARY shape
  reliably? — is the phase 05 equivalent of the check item 34 tracks, and
  should happen before this is leaned on.
- **`scribe status` does not yet surface the index/digest.** Small follow-up;
  the views are written and readable, `status` just doesn't mention them.

## Next

1. Replay backfill (item 36) — the highest-value follow-up, and cheap given
   the backfill machinery already exists.
2. One live `opencode` run through the summary path, per the item 34 lesson
   that every phase should touch a real agent once.
3. Phase 06 (teammates) or the still-open phase 03/04 items (28 first).
