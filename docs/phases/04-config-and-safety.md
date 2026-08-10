# Phase 04 — config and safety

**Status: planned, not shipped. This document is the spec the fleet builds
against; it gets rewritten as a retrospective when the phase lands, the way
`03-writing.md` was.**

`PLAN.md` scopes phase 04 as one day of work:

- off by default, opt in per repo; `scribe off` / `on` to pause without uninstalling
- config as specified; the opencode connector, plus the shape other connectors slot into
- the uninitialised-repo nudge — one line, once, after several sessions
- redaction before anything leaves the machine
- `scribe status`, `scribe diff`, `scribe doctor`

Four of the seven commands in `PLAN.md`'s table still return
`notImplemented(..., "04")`. This phase is the one that makes the CLI real, and
it is also the one that has to land before scribe points at anything that
matters — **redaction is the gate on this repo going public and on any hosted
model seeing a transcript.**

---

## The one thing that is not optional

Everything else here is ergonomics. Redaction is the only part of phase 04 that
prevents an unrecoverable outcome: a transcript holds whatever was on screen,
and the writer ships it to whoever runs the model. `PLAN.md`'s risk section
already says so, and `OPEN-ITEMS.md` records that phase 03 committed rendered
prompts containing real transcript turns and had to rewrite branch history
before merge.

So redaction is scoped as a **choke point, not a sprinkling**: no transcript
byte and no repo byte reaches a writer prompt except through one function, and
that is enforced by a test that fails if a prompt builder learns a second way to
get at content.

## Decisions taken before dispatch

**Config stays JSON, and stays where it already is.** `PLAN.md` sketches
`~/.config/scribe/config.toml` plus `.scribe/config.toml`. What shipped in phase
02 is `.scribe/config.json`, with tests and a live install path. Adding a TOML
parser to a repo whose entire dependency list is cobra + huh, in order to
introduce a second serialisation format alongside the one already on disk, buys
nothing a user can see and costs a migration. **Global defaults land at
`~/.config/scribe/config.json`, same shape, repo config wins field by field.**
This is a deliberate deviation from `PLAN.md`'s Config section; the field names
and the layering it describes are honoured, the file extension is not.

**`Enabled` and paused are different things.** `Enabled` is the master switch
`init` sets — it means "this repo was onboarded." Pausing is separate state with
its own expiry, so `scribe off` never has to un-onboard a repo and `scribe on`
never has to re-run install. `Config.Pause` carries it.

**`off` expires at the end of the local day.** Per `PLAN.md`: a permanent pause
you forget about fails exactly the way forgetting to turn scribe on does.
`--stay` opts into indefinite. `scribe status` always names the state, so "why
hasn't it written anything" is one command away.

**The nudge is opt-in and lives in the user's global Claude settings, not the
repo's.** It has to fire in repos scribe was never initialised in, which is
precisely where scribe has installed nothing — so a project-level hook cannot
reach it. That means writing to `~/.claude/settings.json`, which is the user's
file and not scribe's to touch silently. It gets its own explicit command rather
than being smuggled into `init`, and `init` does no more than mention it in one
line of output. No new wizard question: phase 03 closed item 31 by *removing* a
question, and adding one back for a nudge would be going the wrong way.

**`scribe diff` reads a recorded snapshot, not git.** Git is the undo (decision
10) but nothing commits the docs, so a git diff shows everything since the last
human commit rather than what the last run changed. The worker records
before/after itself.

---

## Work units

Four units, hard file ownership, no shared files. `cmd/scribe/run.go` and
`cmd/scribe/root.go` are the integration points and are owned by nobody — they
are wired by hand after the units land, which is what stopped phase 02 and 03
from ever producing a contested file.

### A — redaction and the code-access knob

Owns: `internal/redact/**` (new), `internal/worker/prompt.go`,
`internal/worker/worker.go`, `internal/replay/prompt.go`,
`internal/seed/prompt.go`, `internal/seed/scan.go`, and tests for those.

- `redact.Redactor`, built from the config's `redact` key patterns and `ignore`
  globs, applied at the single point where content enters a prompt
- Key-pattern matching that actually works on transcript prose — `api_key`
  should catch `api_key=sk-…`, `"apiKey": "…"`, `API_KEY: …` and an `export`
  line, and must not redact the words in a sentence about API keys
- High-signal shapes regardless of key: `sk-…`, `ghp_…`, AWS ids, PEM blocks,
  bearer tokens, `.env`-style assignment lines
- `ignore` globs drop whole files in `internal/seed/scan.go` before they are read
- `Deps.CodeWeight` honoured end to end, and a `Deps.Redactor` that is
  **required, not optional** — a nil redactor is an error, not a pass-through,
  because the failure mode of the optional version is silent and permanent
- A test that enumerates the prompt builders and fails if any of them can reach
  transcript content without going through the choke point

### B — `scribe on` / `scribe off`, and enforcement

Owns: `cmd/scribe/on_off.go` (+ tests), `internal/hook/hook.go`,
`internal/hook/hook_test.go`, `cmd/scribe/hook.go`.

- `scribe off [--stay]`, `scribe on`, writing `Config.Pause`
- End-of-local-day expiry, evaluated on read so no timer or daemon exists
- The hook exits **0, silently, without enqueueing** while paused — a pause that
  still queues work is a delay, not a pause, and the queue would drain the moment
  it expired
- `scribe on` in a repo that was never initialised says to run `init`; it does
  not half-onboard

### C — `scribe status` and `scribe diff`

Owns: `cmd/scribe/status.go`, `cmd/scribe/diff.go` (+ tests),
`internal/docs/lastrun.go` (new), `internal/docs/docs.go`.

- The docs store records a before/after snapshot of every applied run under
  `.scribe/`, bounded to the last run only
- `status`: on / off / paused-until / never-initialised, when the writer last
  ran, what is queued and pending, whether a lock is held, and the last recorded
  hook failure
- `diff`: unified diff per doc from the recorded snapshot, and a plain "the last
  run changed nothing" rather than empty output
- Both are output, not interaction — plain stdout, per `PLAN.md`'s Stack section

### D — `scribe doctor`, global config defaults, the nudge

Owns: `cmd/scribe/doctor.go` (+ test), `internal/install/install.go`,
`internal/install/global.go` (new), `internal/nudge/**` (new),
`cmd/scribe/nudge.go` (new), `cmd/scribe/init.go`, `cmd/scribe/init_test.go`.

- doctor's real checks, each a named line with a pass/fail and a fix: git repo,
  config readable and valid, Stop hook installed and pointing at a binary that
  exists, docs dir present, writer binary resolves, writer answers a trivial
  prompt within a short timeout, queue/lock not wedged. Existing hook-failure
  output stays.
- Global `~/.config/scribe/config.json`, repo config wins per field
- `scribe nudge --install` / `--remove` writing a SessionStart hook to the user's
  global Claude settings, with a backup, and the hidden entrypoint it calls
- The counter: per-repo session count in the user config dir, one line printed
  once at a threshold, never a second time for the same repo, silent everywhere
  scribe is already on

---

## Done when

`go build ./...`, `go vet ./...`, `go test ./...` pass; no command in
`PLAN.md`'s table still returns `notImplemented`; a transcript containing a
planted secret produces a writer prompt that does not contain it; and `status`,
`diff` and `doctor` each answer usefully in a repo that was never initialised,
one that is paused, and one that is live.

## Explicitly not in this phase

Phase 05's digest and index. Phase 06's layouts. Item 28 — running the phase 03
eval corpus properly — which is real and still the cheapest open question, but
is live `opencode` work rather than code and does not belong inside a fanout.
