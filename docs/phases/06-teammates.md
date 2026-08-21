# Phase 06 — teammates

**Status: shipped on `phase-06-teammates`, branched off
`phase-05-digest-index` (phase 05 is not yet merged to `main`).
`go build ./...`, `go vet ./...`, `go test ./...` all pass.**

Two new pure packages plus hand integration, the same shape phases 02–05 took.
`internal/layout` and `internal/attribution` were built first against a fixed
contract; this phase wired them into the live pipeline — `docs.Store`, the
worker, `scribe run`, and the onboarding wizard. Zero files touched outside the
integration surface.

---

## What this phase delivers

Multi-person docs. Two people who both worked in a repo today should not get a
git merge conflict on every pull just because scribe appended both their
sessions to the tail of the same machine-written `CHANGELOG.md`. Phase 06 gives
each repo a **history layout** choice and credits each entry to **who ran it**.

| Concern | Answer |
|---|---|
| How the two history docs are laid out | `internal/layout` — `per-session` (default) or `shared` |
| Who an entry is credited to | `internal/attribution` — git identity → env → os/user → "unknown" |

The layout choice applies **only to the two history docs** (`CHANGELOG.md`,
`JOURNAL.md`). The two state docs (`PROJECT.md`, `DECISIONS.md`) are rewritten
in place, shared by nature — one canonical copy the team keeps current — so
"one file per session" is meaningless for them and `layout.SessionFilePath`
rejects them loudly.

## The two layouts

| Layout | On disk | Conflicts |
|---|---|---|
| **per-session** (default) | Each session's entries go to their **own** file under `changelog/`/`journal/`, named `YYYY-MM-DD[-slug]-<short8sid>.md`. The top-level `CHANGELOG.md`/`JOURNAL.md` is a generated **rollup** linking to them, newest-first. | **None, by construction.** The session-id suffix makes every path unique, so two teammates never write the same file. |
| **shared** | The original single-file-per-doc behaviour: everyone appends to one `CHANGELOG.md`/`JOURNAL.md`, each entry stamped with its author's byline. | Concurrent writers conflict on the shared tail — the simpler layout, for solo repos that don't mind. |

Per-session is conflict-free **by construction**, not by locking or merge
strategy: the uniqueness is in the filename (`internal/layout`'s `sidPrefixLen`
suffix), so there is simply never a shared byte for two people to both edit. A
coalesced multi-session run writes its one entry per doc under the run's
**primary** session id — one file, still conflict-free.

## The two packages (import-only; built as pure cores)

| Package | Does | Key contract |
|---|---|---|
| `internal/layout` | Names and renders per-session files and the rollup | Pure — no filesystem, no clock, no map/caller order leaking into output. `SessionFilePath` (relative path), `RenderSessionFile` (frontmatter + entry), `Rollup` (newest-first index), `Slug`, `ParseMode` (empty → per-session). |
| `internal/attribution` | Resolves "who is running scribe" | `Resolve(repoRoot)` never errors, never panics — always a usable `Author`. `StampShared` is idempotent (a reprocessed transcript slice never stacks a second byline). |

Everything callers write to disk lives in `internal/docs`; the two packages
above only decide names and bytes, which keeps them byte-deterministic and
trivially testable — the same property phase 05's render packages rely on.

## How it wired in

- **`docs.Store` is layout-aware.** `docs.OpenWithLayout(repoRoot, mode)`
  records the mode; `docs.Open` stays **shared** for back-compat, so every
  caller that doesn't care about the split (`scribe diff`, `scribe status`,
  `init`'s seeding) keeps its old behaviour. One new mode-aware method,
  `WriteHistory(doc, meta, author, entry)`, is the single entry point the
  worker calls:
  - **shared**: `attribution.StampShared` then the existing append path —
    rotation, atomicity, and the `scribe diff` snapshot are all untouched.
  - **per-session**: atomically write the session file, then regenerate the
    top-level doc as `layout.Rollup` over every session file in that subdir.
    Both writes are atomic; the rollup regeneration still records the
    before/after so `scribe diff` sees the top-level change. To build the
    rollup input, `docs` lists the subdir and parses back the frontmatter it
    itself wrote via `RenderSessionFile` — a small internal parser, so
    `internal/layout` stays pure.
- **The worker stamps every history write.** `applyEdits` now calls
  `WriteHistory` (replacing the direct `AppendHistory`) with an author resolved
  **once per run** (`attribution.Resolve`) and a `SessionMeta` built from the
  run's primary session (`order[0]`, `Date = deps.now()`). Each history entry
  gets a readable filename slug derived from **its own first markdown line**;
  the session-id suffix, not the slug, is the uniqueness guarantee, so an empty
  slug is fine. State-doc writes are unchanged — no split, no byline.
- **`scribe run` builds the store from config.** `layout.ParseMode(cfg.Layout)`
  → `docs.OpenWithLayout`. Empty defaults to per-session; a genuinely invalid
  string is logged and falls back to per-session rather than crashing the run.
- **Onboarding asks again.** The wizard re-adds the layout `huh` select
  (per-session default, matching the `DocsInGit` style, abortable like every
  other step), reversing OPEN-ITEMS item 31's temporary removal now that the
  semantics it was waiting on exist. `init` stops hardcoding the value.

## The fail-open guard is preserved exactly

Attribution and layout **must never fail a run** — `Resolve` already can't, and
the per-session write path is guarded so a write **error** still surfaces the
same way an append error does today: it aborts `runOnce` **before** offsets
advance, so the next run re-derives the same content rather than losing the
transcript slice. A per-session write failure is a real write failure and is
**not** swallowed; only the best-effort secondary artifacts (the `scribe diff`
snapshot, the session index) degrade quietly, exactly as before.

## Deviations and choices worth recording

- **Multi-session runs write under the primary session id.** A run that
  coalesced several sessions produces one history file per doc, under
  `order[0]`. That is acceptable and conflict-free — the alternative (splitting
  one run's history across N session files) would need the writer to attribute
  each entry to a session, which it doesn't do. The primary session is a fair,
  deterministic choice.
- **The filename slug comes from the entry, the frontmatter summary too.** The
  phase-05 per-session summary isn't computed until `finishRun`, so the base
  `SessionMeta.Summary` is empty and each history write fills it from the
  entry's first line. Readable filenames without depending on a later pass.
- **`docs.Open` stays shared, not per-session.** Back-compat: the callers that
  still use it seed and read single files. Only `scribe run` opts into the
  configured layout via `OpenWithLayout`. A repo's live loop is the only writer
  that needs to honour per-session, and it does.
- **`Author.Display()` fills the frontmatter author line.** Zero authors render
  as the neutral `unknown` rather than a blank field, so every session file has
  the same frontmatter shape.

## What is NOT done

- **`scribe init`'s replay/seed writes do not respect the per-session layout.**
  This is the main follow-up, filed as **OPEN-ITEMS item 37**. The live worker
  loop is layout-aware, but `init`'s seed and replay passes still use the
  shared-style `AppendHistory`, so a per-session repo onboarded from history
  gets a shared-shaped back-history that only converges to per-session as new
  sessions land. No data is lost — the entries are all there, just in the
  shared file — but "conflict-free by construction" doesn't hold for the
  replayed prefix. The fix is entangled with item 36 (replay also doesn't write
  session records): both are "make replay speak the current per-session
  contracts", and the clean version routes replay's history writes through
  `WriteHistory` with a synthesised `SessionMeta` per reconstructed session.
- **No live-agent run through the per-session path yet.** Everything here is
  proven against fakes, a real `docs.Store` on a temp dir (rotation integration
  test), and the deterministic render/parse tests. The item-34 lesson — every
  phase should touch a real agent once — applies: a live `opencode` run writing
  real session files and a rollup should happen before this is leaned on.
- **`scribe status`/`diff` don't mention the layout.** Small follow-up; the
  per-session files and rollup are written and readable, the read-side commands
  just don't surface which layout a repo is on.

## Next

1. Replay layout + records (items 37 and 36 together) — the highest-value
   follow-up, and cheapest done as one pass since both need a per-session
   `SessionMeta` per reconstructed session.
2. One live `opencode` run through the per-session write path, per the item-34
   lesson.
3. Surface the layout in `scribe status`.
