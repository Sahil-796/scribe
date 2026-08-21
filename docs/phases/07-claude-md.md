# Phase 07 — the CLAUDE.md skill

**Status: shipped on `phase-07-claude-md`, branched off `phase-06-teammates`
(phase 06 is not yet merged to `main`). No Go code changed — this phase adds a
Claude Code skill, not pipeline code — so `go build ./...`, `go vet ./...`,
`go test ./...` are unaffected and still pass.**

The last phase in `PLAN.md`, and the smallest. Everything before it is the
pipeline that keeps `docs/scribe/` current with no input. This phase is the one
place a human deliberately turns that record into the file that steers future
sessions — `CLAUDE.md` — on demand, with the main model, behind a diff.

---

## What this phase delivers

A single Claude Code skill: **`/claude-md`**
(`.claude/skills/claude-md/SKILL.md`). Run it in a scribe-onboarded repo and it
reads the four docs, distils a **short** `CLAUDE.md`, shows the diff, and writes
only on confirmation.

It is deliberately **not** part of the pipeline (locked decision 5). The Stop
hook and the cheap writer agent never touch `CLAUDE.md`: that file changes every
future session's behaviour, so it gets the main model, on demand, where the user
can see the change before it lands. The skill is instructions to the running
session — there is no new Go code, no connector, no worker path.

## Why a skill and not a command

`PLAN.md` calls it interchangeably a "slash command" and "a Claude Code skill".
It is authored as a **skill** (`.claude/skills/<name>/SKILL.md` with
`name`/`description` frontmatter) because that is the current Claude Code
convention and a skill named `claude-md` is invoked exactly as the plan wrote
it: `/claude-md`. The frontmatter `description` is written to trigger on both the
explicit `/claude-md` and natural phrasings ("refresh the CLAUDE.md from the
scribe docs").

## The three plan bullets, each honoured

| Plan bullet | How |
|---|---|
| Slash command in Claude Code | `.claude/skills/claude-md/SKILL.md`, invoked `/claude-md`. |
| Hard length ceiling — the value is in it being short | Target **< 40 lines / ~1,500 chars**, **hard cap 60 lines**, with the skill told to cut rather than reformat when over, and that leaving things out is always correct because the docs remain the full record. |
| Shows the diff before it writes | Step 4 is explicit: read the existing `CLAUDE.md`, present old→new as a unified diff (or the full file if new), state the count against the ceiling, and **wait for confirmation** before writing. |

## How the skill reads the docs

- **Source of truth is `docs/scribe/`** — the fixed folder (`scribe.DocsDir`),
  files `PROJECT.md`, `DECISIONS.md`, `CHANGELOG.md`, `JOURNAL.md`.
- **It weights them, it doesn't concatenate them.** `PROJECT` is the backbone;
  `DECISIONS` contributes only the handful of *active* decisions that constrain
  how you work (dropped/superseded ones are skipped); `CHANGELOG` is skimmed for
  current state, never transcribed; `JOURNAL` yields at most one or two still-live
  traps. Distil, not summarise.
- **Per-session layout aware.** When phase 06's per-session layout is on,
  `CHANGELOG.md`/`JOURNAL.md` are rollups linking into `changelog/`/`journal/`.
  The skill treats the rollup as sufficient — it needs the shape of recent work,
  not every entry — and only opens a linked file when a rollup line is too terse
  to place. So the skill works unchanged under either layout.
- **Missing docs is a hard stop, not an invention.** If `docs/scribe/` doesn't
  exist the skill tells the user to run `scribe init` first rather than
  hallucinating a `CLAUDE.md` from the repo. Facts come from the docs; an absent
  build command is left out, never guessed.

## Deviations and choices worth recording

- **Authored as a skill, not a `.claude/commands/*.md` prompt-command.** A skill
  carries a triggering `description` and its own directory, which matches how the
  rest of this environment's tooling is discovered, and reads as `/claude-md`
  exactly as planned. No behavioural difference for the user; better discovery.
- **The skill lives in the scribe repo's own `.claude/skills/`.** That makes the
  repo the source of truth and makes `/claude-md` usable when working *in* scribe
  — though scribe isn't onboarded on itself yet, so running it here today hits the
  "run `scribe init` first" stop. Distributing the skill into an end user's repo
  (e.g. `scribe init` copying it into their `.claude/skills/`) is **not** built —
  see *What is NOT done*.
- **The ceiling is stated as both a soft target and a hard cap.** A single number
  invites the model to sit exactly on it; a target-plus-cap with an explicit
  "cut, don't reformat" instruction pushes toward genuinely short output and
  gives a clear line to refuse at.

## What is NOT done

- **No installer.** The skill is authored in this repo but nothing copies it into
  an end user's repository. `scribe init` installs the Stop hook into
  `.claude/settings.json`; it does not (yet) install this skill into the user's
  `.claude/skills/`. Bounded follow-up: teach `init` to drop the skill file, or
  document a one-line manual copy. Until then `/claude-md` is available to anyone
  who has the scribe checkout, not to a fresh onboarded repo automatically.
- **Not exercised against a real `docs/scribe/`.** scribe has never been
  onboarded on itself, so there is no local four-doc corpus to run the skill
  against end to end. The skill's logic is prose the main model executes, not code
  under test; a real run — onboard some repo, run `/claude-md`, judge the output
  against the ceiling and the diff gate — is the item-34-style live check this
  phase leaves open, cheap to do once a corpus exists.
- **No automated length enforcement.** The 60-line cap is an instruction to the
  model, not a validator. There's no code that rejects an over-length draft; the
  diff-before-write step is the human backstop.

## Next

1. Install path: `scribe init` (or a small `scribe` subcommand) drops
   `claude-md` into the onboarded repo's `.claude/skills/`, so the skill reaches
   the repos that actually have a `docs/scribe/` to distil.
2. One real run: onboard a repo, run `/claude-md`, check the output is genuinely
   short and the diff gate behaves.

---

**With this, every phase in `PLAN.md` (00–07) has shipped.** The remaining work
is the tracked backlog in `docs/findings/OPEN-ITEMS.md`, not new phases.
