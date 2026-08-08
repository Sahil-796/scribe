# scribe — build plan

*Working name. It's baked into the docs folder path, so settle it before phase 02.*

Claude Code writes a full transcript of every session. scribe reads it and keeps four
markdown docs per repo current — automatically, no manual input — using a second agent
so your own Claude session is never interrupted.

---

## The loop

```
Claude finishes a reply
      │
      ▼
Stop hook ─────────── no model, exits in ms
      │               gets session id, transcript path, cwd
      ▼
queue + per-repo lock
      │
      │  busy? ──▶ mark pending, exit
      ▼
writer agent runs
      │
      ├── reads  transcript bytes since last run
      ├── reads  the four docs as they stand
      ├── reads  the repo (read-only, to check claims)
      └── edits  whichever docs actually changed
      │
      ▼
pending set? ──▶ run again, covering what arrived
```

The docs are the memory. That's how a run knows what an earlier run claimed and can
take it back — which is the normal case, not an edge case.

---

## Locked decisions

1. **Trigger on every Claude reply, not session end.** Session end can't be caught
   reliably — crash, kill, closed window are all silent. A reply ending always fires.

2. **Writes go straight to the real docs, corrections included.** If reply 2 claims a
   thing and reply 3 scraps it, the next run pulls it from PROJECT and writes a
   DECISIONS entry saying why. Being wrong and reversing is recorded, not hidden.

3. **One run at a time per repo, with coalescing.** Queue plus a lock. Triggers landing
   during a run set a pending flag; the current run picks them up on finish. Two
   processes never edit the same file, and no reply goes uncovered.

4. **Each run reads only new transcript bytes plus the current docs.** Flat cost no
   matter how long the session runs. A 4 MB transcript is never re-read, and small
   models stay viable.

5. **CLAUDE.md is a skill you run, not part of the pipeline.** That file steers every
   future session, so it gets your main model, on demand, where you can see it.

6. **The writer is config, not a hardcoded dependency.** See *Interface* below.

7. **It can read the code, but the transcript leads.** Read-only repo access, used to
   check claims rather than source them. Stops the common failure where something was
   discussed at length, never built, and landed in the changelog anyway. Weighting is
   configurable; code access can be turned off.

8. **One command onboards a repo.** `scribe init` seeds PROJECT and DECISIONS from the
   repo, then replays that repo's past sessions into CHANGELOG and JOURNAL. Backfill
   isn't separate — it's the second half of init.

9. **Docs live in `docs/scribe/`.** Fixed folder named after the tool. Nothing to
   detect, no collision with docs you already keep.

10. **Git is the undo, and committing stays yours.** No snapshots, no auto-commit, no
    interference with your history. See *Risks* — this has a known hole.

---

## The four docs

```
docs/scribe/
  PROJECT.md      what it is, where it stands
  DECISIONS.md    what was chosen, and what was dropped
  CHANGELOG.md    dated, what happened
  JOURNAL.md      the mess, in full
  INDEX.md        one line per session
  digests/        weekly, grouped
```

**Business context — current state only, no history.** Rewritten in place as reality moves.

| File | Contents |
|---|---|
| `PROJECT.md` | What this thing is, who it's for, where it stands. |
| `DECISIONS.md` | One block per decision, marked active / dropped / superseded. Dropping carries the reason. |

**Engineering history — kept forever, append-only.**

| File | Contents |
|---|---|
| `CHANGELOG.md` | Dated lines. What was built, fixed, ripped out. |
| `JOURNAL.md` | What you were stuck on, what the AI got confidently wrong, what you tried that failed, what the fix turned out to be. |

Most engineering sessions never touch PROJECT or DECISIONS. Those two only move when the
session actually contains product-level talk.

---

## Interface

Mostly there isn't one. The system's normal state is invisible — you work, the docs
update. The CLI exists for setup and for the moments you don't trust it.

### The writer: hardcoded connectors

One connector per supported agent, each owning that tool's flags, output format and
failure modes. Not a generic command template — a template pushes every tool's quirks
onto the user, and the first thing they hit is a silent hang because they forgot the
auto-approve flag.

```toml
[writer]
agent = "opencode"
model = "opencode/deepseek-v4-flash-free"
```

Ship with `opencode` first, add `claude`, `codex`, `gemini` as they're needed. A
connector is small — build the argv, run it, interpret the exit. Keep a `custom`
escape hatch taking a raw command for anything unsupported, unsupported meaning
unsupported: if it breaks, that's yours.

This split is the point of the whole design — expensive primary agent doing your work,
cheap secondary agent doing the writing.

### On and off

Off is the default everywhere. `scribe init` turns it on for one repo, and it stays on
until you say otherwise. Nothing runs in repos you never initialised.

**The caveat you named:** you have to remember to turn it on, and the sessions you most
want documented are usually the early ones in a new project — exactly when you haven't
thought about scribe yet.

**Pausing.** `off` is the command that earns its keep — sensitive work, a thrashy
debugging session you don't want a permanent record of, or the writer misbehaving. It
expires at end of day by default, because a permanent pause you forget about fails the
same way forgetting to turn it on does. `--stay` makes it indefinite; `scribe on`
resumes now; `scribe status` always says which state you're in, so "why hasn't it
written anything" is one command away.

Cheap mitigation, doesn't break the zero-input rule: a SessionStart hook that stays
quiet unless you've had several sessions in an uninitialised repo, then prints one line
once — *"scribe isn't on here — 6 sessions so far. `scribe init` to catch up."* It's a
nudge, not a prompt, and the replay pass means turning it on late still recovers the
history you missed. Never nags twice for the same repo.

### Commands

| Command | Does |
|---|---|
| `scribe init` | Turn it on for this repo: install hook, seed docs, replay history. The only command most people ever run. |
| `scribe status` | Is it on, when did it last run, is anything queued, did anything fail. |
| `scribe diff` | What did the last run change. |
| `scribe run` | Force a run now, don't wait for a reply. |
| `scribe off` / `scribe on` | Pause for this repo without uninstalling. `off` expires at end of day unless you pass `--stay`. Off is the default state everywhere until `init` runs. |
| `scribe doctor` | Is the hook installed, does the writer command work, is the model reachable. |
| `scribe hook` | Internal. What the Stop hook calls. Never typed by a human. |

### Config

Global defaults in `~/.config/scribe/config.toml`, per-repo overrides in
`.scribe/config.toml` (gitignored — it's local state, and may hold model choices
that differ per person).

```toml
[writer]
agent = "opencode"
model = "opencode/deepseek-v4-flash-free"

[docs]
path = "docs/scribe"

[code]
read   = true      # let the writer look at the repo
weight = "check"   # "check" = verify claims only, "full" = source content from code

[privacy]
redact = ["api_key", "token", "password", "secret"]
ignore = ["**/.env*", "**/secrets/**"]
```

### The skill

`/claude-md` — a Claude Code skill, not part of the pipeline. Reads the four docs,
writes a short CLAUDE.md with your main model, shows the diff before writing.

### Stack

**Go, [cobra](https://github.com/spf13/cobra) for the command tree,
[huh](https://github.com/charmbracelet/huh) for the init wizard.**

The hook runs on every reply, so its startup cost is the one hard constraint — a static
Go binary starts in single-digit milliseconds and has nothing to install alongside it.

huh earns its place in exactly one command. `scribe init` is the whole onboarding and
deserves to feel considered: pick your agent, pick your model, confirm the docs path,
watch the replay progress, see what it wrote before it commits to the repo. Everything
else is plain stdout — status, diff and doctor are output, not interaction.

**Not [OpenTUI](https://opentui.com/), for now.** It's genuinely good and it's what
opencode itself is built on — but it's a full-screen rendering engine, and scribe is
seven commands and one form. Using it here means adopting Bun and TypeScript for the
hook path to get a wizard huh already does in forty lines.

Where it *would* earn its place: `scribe browse` — a reader for journals and digests,
scrolling entries, jumping by date, searching for the bug you know you hit before.
That's a real TUI and worth building on a real TUI library. Not in these phases; the
docs have to be worth reading first.

---

## Phases

### 00 — Prove it before building it · half day

Three unknowns, all cheap to settle, all fatal if wrong.

- Confirm the Stop hook fires here and hands over session id, transcript path, cwd
- Confirm the writer command completes unattended with no terminal attached
- Feed one real transcript to two or three candidate models, compare against what
  actually happened

**Gate:** if no model writes prose you'd keep, stop. Everything downstream is plumbing
around that one capability.

### 01 — The loop, one repo, one person · 1–2 days

End to end and genuinely working, nothing configurable yet.

- `scribe hook` — reads hook payload on stdin, enqueues, exits. Under 50 ms, no network,
  never blocks your session
- Queue, per-repo lock, pending flag, coalescing
- The worker: new bytes in, four docs read, writer called, docs edited
- Byte offset per session so re-runs don't duplicate and resumed sessions pick up where
  they left off
- Single Go binary — the hook path has to be instant and dependency-free

**Done when:** you work for an hour, touch nothing, and the four docs are current afterwards.

### 02 — `scribe init` · 1–2 days

The onboarding story, and what makes the docs useful on day one instead of week three.
Built before prompt tuning because it produces the corpus tuning needs.

- Seed pass: README, package files, directory tree, existing docs → first PROJECT.md and
  DECISIONS.md
- Replay pass: that repo's existing transcripts → CHANGELOG and JOURNAL
- Chunked and resumable; some histories are large
- Dry run by default, writing somewhere readable before it touches the repo
- Uses the good model — it runs once per repo, wrong place to economise
- The wizard: pick agent, pick model, confirm docs path, watch replay progress, review
  before it writes. This is the whole first impression, so it gets the care

### 03 — Make the writing good · 2–3 days

The longest phase, and the one that decides whether you keep using this.

- A separate prompt per doc — four small jobs beat one big one
- PROJECT and DECISIONS gated behind "did anything product-level actually happen"
- The correction path: pulling an entry always writes the reason into DECISIONS
- Teach the journal what's worth capturing — problems hit, AI mistakes, dead ends,
  the actual fix
- Tune how hard it checks code before believing the transcript
- Size caps on the edited docs so the model can hold one whole and edit it safely
- Iterate against the phase 02 corpus

### 04 — Config and safety · 1 day

- Off by default, opt in per repo; `scribe off` / `on` to pause without uninstalling
- Config as above; the opencode connector, plus the shape other connectors slot into
- The uninitialised-repo nudge — one line, once, after several sessions
- Redaction before anything leaves the machine — transcripts hold whatever was on screen
- `scribe status`, `scribe diff`, `scribe doctor`

### 05 — Digest and index · 1 day

- Weekly digest, grouped features / bugs / general work, sessions listed under each
- Session index — one line each, dated, skimmable
- No scheduler: the first run of a new week writes last week's digest

### 06 — Teammates · 1–2 days · **blocked**

Blocked on the file layout question below.

- Pick the layout, build to it
- Attribution on entries
- Behaviour when two people's docs land in the same commit

### 07 — The CLAUDE.md skill · half day

- Slash command in Claude Code
- Hard length ceiling — the value is in it being short
- Shows the diff before it writes

---

## Open questions

### Blocking phase 06 — how teammates write to the same journal

Committed docs mean everyone's scribe appends to the same files: a merge conflict in
machine-written markdown on nearly every pull.

- **A. One file per session.** `journal/2026-08-08-auth-refactor.md` plus a generated
  index. Two people can't touch the same file, so conflicts are impossible by
  construction. Also fixes long-file readability. Costs a directory of many small files.
- **B. One file per person.** `journal/sahil.md`. No conflicts, fewer files, but each
  grows without limit and reading across people means opening several.
- **C. Shared files, live with conflicts.** Simplest layout, and the one you'll resent.

### Not blocking anything

- **The name.** Baked into `docs/scribe/`, so renaming later means moving files in every
  onboarded repo. Settle before 02.
- **Default writing model.** Settle after 00 shows which ones can do the job.
- **Journal splitting.** One file forever vs per month. Answered for free if the team
  question lands on A.
- **Committed or gitignored.** Gitignore for the first week regardless. Sits awkwardly
  with git being the only undo.
- **Monorepos.** One `docs/scribe/` per repo assumes one product per repo. Revisit if it bites.

---

## Risks

**The writing model isn't good enough.** Pulling what mattered out of a long messy
conversation is the hard version of this task. No architecture saves you. Phase 00
exists to find out in half a day instead of a week.

**The transcript format is undocumented.** An internal file that can change between CLI
versions. Two traps confirmed already: tool results are recorded as `type: "user"`
messages, and subagent turns (`isSidechain: true`) are mixed in and must be filtered.
Pin the parser to known shapes and fail loudly rather than writing nonsense quietly.

**The undo has a hole in it, by choice.** Git is the recovery path but nothing commits
the docs, so the floor only reaches your last commit — and doesn't exist in non-git
dirs. Accepted to keep the tool out of your history. Committing the docs yourself at the
end of a session closes most of it.

**Nobody reads it.** The goal is that mistakes don't repeat, which only pays off if the
journal stays readable. That's what the index, the digest, and the size caps are for.

**It ships your session contents somewhere.** Transcripts hold whatever was on screen.
A hosted free model means all of it goes to whoever runs that model. Redaction and
per-repo opt-in land in phase 04, before this points at anything real.

---

*Draft 2 — after the design grilling. Ten decisions locked; name and team layout open.*
