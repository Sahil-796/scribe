# Finding: does `isSidechain: true` appear in real transcripts, and how are subagent turns actually recorded? (open items 9 & 10)

**Unknown under test:** `PLAN.md` states as confirmed that transcripts contain inline
lines with `"isSidechain": true` marking subagent turns, and that `internal/transcript`
must filter them out. Phase 00 found zero such lines across 68 real transcripts.
Settle whether the trap is rare, relocated, or wrong — and, related, whether a
`Task`-delegating turn fires one Stop event or several (item 10).

## What I surveyed

All `.jsonl` files under `~/.claude/projects/`, read-only, aggregate counts only (file
sizes, line counts, JSON key names, `type` values, timestamps) — never printed or
copied any prompt/response text. This is the user's full local Claude Code history:
15 project directories, not just this repo, giving a much larger and more varied
sample than phase 00's 68. At this scale (317 files) reading everything was cheaper
and more conclusive than sampling.

- **317 `.jsonl` files total.**
- **120 are top-level session transcripts** — `<project>/<sessionId>.jsonl`, the exact
  path shape the Stop hook hands to scribe as `transcript_path` (confirmed in
  `00-hook.md`). Together: 38,279 lines.
- **197 are under `<project>/<sessionId>/subagents/*.jsonl`** — a directory next to
  the main file, one file per delegated subagent run. Together: 31,581 lines.
- No other file shapes found (no loose `tool-results/*.jsonl` despite that directory
  existing on disk in some session folders — it holds non-JSONL content).

## What I found

**In the 120 main session files** (38,279 lines): `isSidechain` is present on 29,934
lines and is **`false` on every single one**. Zero occurrences of `true`, in any of
the 120 files, across all 15 projects. This isn't a sampling artifact — it's the full
population on this machine.

**In the 197 subagent files** (31,581 lines): `isSidechain` is present and **`true` on
every single line**, no exceptions, no file with a mix.

The split is exact and structural, not statistical: subagent turns are not interleaved
into the main transcript at all in the current CLI version. They are written to a
wholly separate file, in a `subagents/` subdirectory that sits next to the main
transcript, keyed by the delegating session's ID. `isSidechain` still exists as a
field and still means "this is a subagent turn" — but it only ever shows up in files
scribe's Stop hook is never handed a path to.

Distinct top-level keys and `type` values observed (counts, main + subagent files
combined): `type` values are `assistant` (36,556), `user` (21,646), plus
metadata-only types already known to `internal/transcript`
(`attachment`, `last-prompt`, `queue-operation`, `ai-title`, `custom-title`, `system`,
`mode`, `pr-link`, `file-history-snapshot`, `permission-mode`, `frame-link`,
`agent-name`, `file-history-delta`) — nothing unrecognised. Notable keys beyond what
`transcript.go` reads: `parentUuid` (present on every conversation line, `null` only
for the first line of a session — a thread-linkage field, not needed since the
subagent split already does the job), and a cluster of `attribution*` /
`sourceToolAssistantUUID` fields that tag which MCP tool/skill/subagent a
tool-result line came from. Those live on `tool_use`/`tool_result` lines, which
`extractText` already drops for having no `"text"` block — so they don't change
scribe's behaviour and I didn't chase them further.

### Item 10, opportunistically: one real example, not exhaustive proof

One `type: "system", subtype: "stop_hook_summary"` line is recorded in the main
transcript each time *some* Stop hook fires and completes (this is Claude Code's own
bookkeeping, unrelated to scribe — these sessions have other tools' hooks installed,
not scribe's — but "a Stop event fired" is a CLI-level fact independent of which hook
is registered). In this repo's own session `cc677ff6-866b-40d6-92bd-614fe67ac2f8`, 7
`Agent`-tool calls fired back-to-back between 14:35:49 and 14:37:47 (matching 7 files
in that session's `subagents/` directory), followed by exactly **one**
`stop_hook_summary` at 14:38:14 — not seven. That's a clean, real data point for "one
Stop event covers a whole burst of subagent delegations, not one per subagent,"
matching phase 00's hypothesis. I tried to replicate the pattern in three `tms`
project sessions and got noisier results (multiple Stop summaries following an agent
burst) — but that measurement conflated the burst's own Stop with unrelated later
turns in the same long session, so it neither confirms nor refutes; I did not build a
tighter time-windowed correlation to fix it. Treat item 10 as **supported by one clean
example, not closed.**

## What it means for `internal/transcript`

The `isSidechain` filter (`transcript.go`, trap #2) is **verified dead code against
every real transcript observed** — it has never once matched `true` in 29,934
opportunities. But it is not wrong, and not harmful: it costs one field read and one
branch, and if a future CLI version ever reverts to inlining subagent turns (or some
other client writes an inline sidechain the way `PLAN.md` originally assumed), the
filter is already there to catch it. What actually keeps subagent chatter out of
scribe's docs today is not this filter — it's that `Read()` only ever opens the
`transcript_path` the Stop hook hands over, and that path is structurally never the
`subagents/*.jsonl` files. scribe never lists or opens that directory, so the content
never has a chance to reach `extractText`.

**Recommendation: leave the filter in place, but fix the comment.** The current
package doc and inline comment both assert `isSidechain: true` is a live trap that
was hit and handled. It should instead say what this finding shows: the field exists,
means what's documented, has never been observed `true` in the file scribe reads, and
the real reason subagent content doesn't leak in is file separation
(`transcript_path` never points at a `subagents/` file), with the field check kept as
cheap insurance in case that changes. This is a doc-only change to a Go file's
comments — out of my file allowlist for this task, so I'm not making the edit; noting
it here for whoever picks up item 9's closeout.

## Confidence and what's still unproven

High confidence on the headline claim: `isSidechain: true` does not appear in any
transcript file scribe would ever read, on this machine, across 120 real sessions and
15 different projects — that's the full population, not a sample. Same confidence on
the mechanism: subagent turns live in separate `subagents/*.jsonl` files.

Lower confidence on generalising beyond this machine/CLI version — same caveat phase
00 already logged (CLI 2.1.223, one machine). If the format changes again the filter's
insurance value becomes real, so keeping it costs nothing.

Item 10 remains only lightly settled: one clean example says "one Stop per main-thread
turn regardless of delegation count," but I did not confirm this holds when subagents
run one at a time rather than in a rapid burst, when a subagent itself uses `Task`
recursively, or across enough sessions to rule out coincidence. A dedicated probe
(phase 00's approach: a controlled prompt that reliably delegates, with a hook of
scribe's own registered) would close this properly; reading existing transcripts only
got partway there.
