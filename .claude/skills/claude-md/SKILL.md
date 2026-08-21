---
name: claude-md
description: Write a short CLAUDE.md for this repo from its scribe docs (PROJECT, DECISIONS, CHANGELOG, JOURNAL). Use when the user runs /claude-md or asks to generate/refresh the repo's CLAUDE.md from scribe's docs. Shows a diff before writing; hard length ceiling.
---

# /claude-md — distil scribe's docs into a short CLAUDE.md

scribe keeps four long, always-current docs under `docs/scribe/`. This skill
reads them and writes one **short** `CLAUDE.md` at the repo root — the file that
steers every future Claude Code session here.

This is a skill you run on demand with your main model, **not** part of the
scribe pipeline. The pipeline (the Stop hook and the cheap writer agent) never
touches `CLAUDE.md`. You do, when you want to, and you see the diff first.

The whole value is in the result being **short**. A long CLAUDE.md is skimmed
and ignored; a tight one gets read every session. Distil, don't summarise —
throw away anything a fresh session doesn't need in its first five minutes.

## Steps

1. **Locate the docs.** They live at `docs/scribe/` (the fixed folder). The four
   sources are `PROJECT.md`, `DECISIONS.md`, `CHANGELOG.md`, `JOURNAL.md`.
   - If `docs/scribe/` doesn't exist, stop and tell the user: this repo isn't
     onboarded — run `scribe init` first. Do not invent a CLAUDE.md from
     nothing.
   - `CHANGELOG.md` / `JOURNAL.md` may be **rollups** (per-session layout): a
     newest-first index linking to files under `changelog/` / `journal/`. The
     rollup itself is enough for this skill — you need the shape of recent work,
     not every entry. Only open a linked session file if a rollup line is too
     terse to place.

2. **Read all four.** Weight them by what a new session needs:
   - `PROJECT.md` → what this thing is, who it's for, where it stands. **This is
     the backbone of the output.**
   - `DECISIONS.md` → the active, load-bearing decisions a newcomer would
     otherwise re-litigate or violate. Skip dropped/superseded ones. Keep only
     the handful that actually constrain how you work here.
   - `CHANGELOG.md` → skim for current state and what's in flight. Do **not**
     transcribe history — a changelog belongs in the changelog.
   - `JOURNAL.md` → mine for live traps: the thing the AI got confidently wrong,
     the dead end still worth avoiding. One or two, only if still relevant.

3. **Draft `CLAUDE.md`** under the length ceiling below. Suggested shape:
   - One or two lines on what the project is.
   - **How to build / test / run** — the commands a session needs (e.g.
     `go build ./...`, `go test ./...`), if the docs state them.
   - **Conventions and active decisions** that change what you'd write — as a
     short bulleted list, each line load-bearing.
   - **Traps** — at most a couple, only if current.
   Omit any section the docs don't support. Never pad to fill a template.

4. **Show the diff before writing.**
   - If `CLAUDE.md` already exists, show a unified diff of your proposed change
     against it (read it first, then present old → new).
   - If it's new, show the full proposed file.
   - State the line/char count against the ceiling.
   - **Wait for the user to confirm.** Only write `CLAUDE.md` after they say yes.
     If they ask for edits, revise and show the diff again.

5. **Write** `CLAUDE.md` at the repo root on confirmation. Don't touch anything
   under `docs/scribe/` — this skill only reads those.

## The length ceiling (hard)

- **Target: under 40 lines and under ~1,500 characters.**
- **Hard cap: 60 lines.** If you're over, you're transcribing, not distilling —
  cut, don't reformat. It is always correct to leave something out; the docs
  remain the full record and a session can open them.
- Prefer terse, scannable bullets over prose paragraphs.
- No history dumps, no exhaustive decision logs, no restating the docs. If a
  line doesn't change what a new session would do, delete it.

## Notes

- Facts come from the docs. If the docs don't state a build command or a
  convention, don't guess it into `CLAUDE.md` — leave it out.
- Keep the tone plain and imperative. This file is instructions to a future
  session, not marketing.
- Re-running the skill regenerates from the current docs; that's expected — the
  docs move, so this file should be cheap to refresh, always behind a diff.
