# Phase 00 finding: the model bakeoff — can any cheap model write prose worth keeping?

**Unknown under test:** "Feed one real transcript to two or three candidate models,
compare against what actually happened." **Gate:** if no model writes prose you'd
keep, stop — everything downstream is plumbing around that one capability.

**Environment:** macOS (Darwin 25.5.0). `opencode` 1.18.15 on PATH, reachable with
no login step for its free-tier models. `codex` (`@openai/codex` npm package) is
installed but its native binary is missing from the package
(`.../codex-darwin-arm64/vendor/aarch64-apple-darwin/codex/codex: ENOENT`) — every
invocation, including `--version`, fails immediately. This is a broken local
install, not an auth/network wall; I did not try to reinstall or patch it (out of
scope, and touching global npm packages isn't something this task should do
silently). So the bakeoff below is opencode-only, across three different free
models reachable through opencode's one connector — `opencode/deepseek-v4-flash-free`,
`opencode/longcat-2.0-free`, `opencode/mimo-v2.5-free` — which stands in for "two or
three candidates" per the plan's phrasing. `opencode/gpt-5-nano`,
`opencode/gemini-3.5-flash-lite`, and `opencode/claude-haiku-4-5` were also tried
first but all rejected with `No payment method` — not usable without adding
billing, which I did not do.

---

## 1. Transcript chosen

`~/.claude/projects/-Users-sahil-work-scout/eb60511f-8dbf-4b8f-b868-52114ebd0d66.jsonl`
— a real session in `/Users/sahil/work/scout` (a different repo from this one, per
instructions), titled "Fix API timeline limit validation error" but actually a
long, messy, multi-topic engineering day: 1,133 raw JSONL lines, 5,509,375 bytes,
spanning 06:43–14:57 UTC on 2026-07-31. Chosen over other candidates (an even
larger 29 MB orqestra transcript, several shorter scout sessions) because it's
long, has a real dead end and a real "user has to repeat a question" moment, and
ends in a big architectural proposal that was *not* built in-session — exactly the
shape the plan's Risks section worries about a writer hallucinating as done.

No transcript available locally (searched every `.jsonl` under
`~/.claude/projects/*/`) contains `"isSidechain":true` or a `Task` tool
invocation — none of the user's recent real sessions happened to use a subagent.
That trap is real per the plan but I could not exercise it on real data; see §2.

---

## 2. Transcript format — facts confirmed against the real file

Parser: `experiments/00-model-bakeoff/parser.py` (Python, throwaway, run
directly against a transcript path). Full reasoning and evidence is in the
docstring at the top of that file; summary here.

| # | Fact | Evidence |
|---|---|---|
| 1 | Tool results are recorded as `type: "user"` messages, `message.content` a list with a `{"type":"tool_result",...}` block. **Confirmed, common** — 360 of 389 `user`-type records in this transcript are tool results, not human turns. | counted directly, see `stats.json` below |
| 2 | Subagent turns carry `isSidechain: true` and must be filtered. **Field confirmed present** on every message record (usually `false`); **could not find a single `true` instance** in any local transcript (`grep -rl '"isSidechain":true' ~/.claude/projects/*/*.jsonl` → zero hits everywhere, not just in this transcript). No `Task` tool calls appear anywhere locally either. Implemented the filter defensively regardless — cheap, and the plan explicitly calls this trap out as confirmed. | grep across all of `~/.claude/projects` |
| 3 | **Not called out in the plan — found empirically:** slash-command invocations (e.g. `/model claude-sonnet-5`) are *also* recorded as `type:"user"` with plain **string** content, wrapped in `<command-name>...</command-name>` or preceded by `<local-command-caveat>`. These are real transcript entries but not something a human "said" — 4 of 28 string-content `user` messages in this transcript were command invocations, not prompts. | manual inspection of string-content user messages |
| 4 | Real human turns: `type:"user"`, `message.content` is a plain **string** (not a list). Real assistant replies: `type:"assistant"`, `message.content` is a **list** containing one or more `{"type":"text",...}` blocks. | 24 real user turns, 75 real assistant turns extracted this way |
| 5 | Assistant content blocks seen: `text` (kept), `thinking` (extended thinking, dropped — 169 instances), `tool_use` (dropped — 360 instances, always paired with a `tool_result` in a later user record). 529 of 604 assistant records have **no** text block at all — pure tool orchestration with nothing to show a human. | `record_type_counts` / block counts in `stats.json` |
| 6 | Non-message record types at the JSONL top level, all noise for narrative purposes: `custom-title`, `ai-title` (session title guesses), `mode`, `queue-operation` (message-queue bookkeeping — this session actually had a queued follow-up message, `"you didnt answer this"`, that arrived while the assistant was still replying to something else), `system` (hook bookkeeping, subtype `stop_hook_summary`), `attachment` (deferred-tool-list / agent-list deltas — pure session metadata), `last-prompt` (redundant duplicate of the latest user prompt). | `record_type_counts` in `stats.json` |
| 7 | One more real-but-noisy shape: a `user` message whose list content is exactly one `{"type":"text","text":"[Request interrupted by user]"}` block, no `tool_result`. Not something the user said — a system-generated interruption marker. 1 instance in this transcript. | manual inspection |
| 8 | Also found (not filtered, left in, worth flagging for phase 03): `<task-notification>` blocks — synthetic messages injected by the harness when a background `Monitor` finishes (e.g. "Wait for crawl+normalize pipeline to finish... `<event>done</event>`"), delivered as ordinary string-content `user` records. These *look* like real user turns to a naive parser (they pass the "is it a string?" test) but are machine-generated, not human. Two showed up in the cleaned output verbatim; a real scribe writer prompt should probably instruct the model to disregard them, or the parser should filter on the `<task-notification>` wrapper the way it filters `<command-name>`. | lines 463–468, 671–676, 696–701 of the cleaned transcript |

Full stats for this transcript:

```json
{
  "total_records": 1133,
  "sidechain_filtered": 0,
  "non_message_records": 140,
  "tool_result_user_msgs": 360,
  "command_invocations": 4,
  "interruptions": 1,
  "silent_assistant_turns": 529,
  "real_user_turns": 24,
  "real_assistant_turns": 75,
  "raw_bytes": 5509375,
  "cleaned_bytes": 67581,
  "compression_ratio": 81.52
}
```

---

## 3. Compression

**81.5x** — 5,509,375 raw bytes → 67,581 cleaned bytes (24 real user turns + 75 real
assistant replies, in order, rendered as Markdown with `### USER`/`### ASSISTANT`
headers and timestamps). This matters directly for the cost model in the plan:
decision 4 says "each run reads only new transcript bytes plus the current docs,"
and this confirms that even a long, tool-heavy, multi-hour session compresses to
something a cheap model can hold in one context window trivially — 67 KB is
~17K tokens, nowhere near a problem even for small-context free models.

The compression ratio will vary a lot by session shape — a session that's mostly
back-and-forth chat with little tool use will compress far less than this one,
which is unusually tool-heavy (604 of 1133 records are assistant turns, most of
them silent orchestration around 360 tool calls). Worth remeasuring against a
chattier session in phase 01/03, but as a worst-case-ish data point this is
reassuring, not alarming.

Cleaned transcript (eyeballed for secrets — none found; contains only public
domain names, code paths, and two test-account emails that are pre-existing
seed-script fixtures, not real credentials): `experiments/00-model-bakeoff/cleaned_transcript_scout-eb60511f.md`.

---

## 4. Ground truth — what actually happened in this session

Written by reading the full cleaned transcript before running any model, so
model output couldn't anchor this.

**Built and shipped (7 commits, all local, not pushed, on `feat/memory-and-interpretation`):**

1. Trend tab 422 error — client requested `limit=200`, server caps at 100. Fixed
   by client-side cursor pagination (`fetchTimelineWindow()`), not by raising the
   server cap.
2. Crawler skip-list — `/alternatives/`, `/alternative-to/`, `/compare/`, `/vs/`
   added to `defaultSkipPaths` so comparison-farm pages stop eating the 50-page
   crawl budget (the hub page still crawls, only per-competitor children skip).
3. Homepage-section pricing fallback — `findPricingPages` now considers the
   landing page when no dedicated `/pricing` page exists, gated on a pricing
   heading plus real price/tier signal.
4. `truncate()` changed from "first 8000 chars" to "densest 8000-char window of
   price tokens" — the appthetics pricing section sat ~20k chars in, past a wall
   of testimonials, so the LLM fallback was never actually seeing it.
5. LLM pricing arithmetic bug — the model wrote `12 * 12 * 0.75` instead of `108`
   into a JSON numeric field. **Two attempts**: first a prompt instruction alone
   (verified NOT sufficient — the real pipeline rerun failed identically), then
   `repairArithmeticLiterals`, a regex-based parser-level fix, which held.
6. Remove-company button — Base UI dropdown fires `onClick`, not `onSelect`;
   both affected menu items fixed.
7. Tab-loading UX — three stacked, independent bugs fixed together: active pill
   driven by `useSearchParams()` instead of a server prop (instant highlight),
   a `<Suspense>` boundary + skeleton fallback added around tab content, and
   `middleware.ts` stopped blocking every client-side tab navigation on a
   redundant `/api/me/limits` fetch (found via dumping real request headers —
   the documented `rsc` header/`_rsc` query param don't reach middleware in this
   Next version; the real signal is a `next-url` header). Measured: ~1.4s → ~2ms.
8. `apps/server/scripts/recrawl.ts` added — no "recrawl now" UI/route existed;
   this is a manual escape hatch.
9. Companies-table cleanup (a DB operation, not code) — 11 rows deleted (6 fake
   `.scout-demo.test` seed companies, an orphaned `stand.store` typo, and
   `linear.app`/`stripe.com`/`vercel.com`/`notion.so` whose only tracker was a
   different seed account) after tracing actual foreign-key ownership rather
   than guessing by name, since `companies` is shared across tenants and a
   careless delete would cascade into other users' data.
10. GitHub issue #2 filed for stan.store — an SPA whose plain-HTTP crawl returns
    an empty shell; documented, not fixed in-session.

**Explicitly discussed, proposed, and NOT built in this session — this is the
important trap:**

- Frontier/queue prioritization for the crawler (put `/pricing`, `/changelog`
  etc. ahead of blog/alternatives in the crawl queue) — proposed as "option 1,"
  the user never confirmed it, only options 2-adjacent work (the skip-list) shipped.
- Headless-rendering fallback for JS-rendered/SPA sites — proposed as the real
  fix for stan.store; only the GitHub issue was filed, no code.
- Suppressing `PAGE_ADDED`/entity-added events on a company's first crawl (to
  stop the "added today" vs. "existed a year ago per Wayback" contradiction) —
  explicitly proposed with two named options, ends on "Want me to implement it?"
  with no confirmation in the transcript.
- The big one: a full LLM-first, schema-enforced (`generateObject` + Zod)
  rewrite of all five normalizers, replacing DOM-heuristic extraction, plus
  ATS-provider API detection for jobs boards. This is the session's climax — a
  detailed cost analysis (~$2/yr at current scale), a named tradeoff
  (non-determinism vs. change-detection), and ends on "Want me to build it?" The
  session's last user message is about **committing existing work**, not about
  this proposal. **It was never implemented.** A writer model that reports this
  as shipped work has failed the plan's named failure mode outright.
- Calendly's empty tabs (careers ATS widget, missing `<h1>`) — user explicitly
  said "just address dont edit"; diagnosis only, by instruction.

**Two loose ends, named but not fixed:**
- A second recrawl of appthetics.com apparently lost its pricing entity
  (hypothesized cause: unchanged-content-hash skip interacting with snapshot
  lookup) — flagged, not chased.
- A stray `apps/server/undefined/db-backup-*.json` (900KB) and
  `docs/gap-plan.html` deliberately left uncommitted, noted as not this
  session's mess.

---

## 5. The bakeoff

Prompt used (`experiments/00-model-bakeoff/prompt.md`, prepended to the cleaned
transcript and sent as one message): asks for a CHANGELOG.md entry and a
JOURNAL.md entry, explicitly instructs the model not to list discussed-but-not-built
work in CHANGELOG and to be explicit about it in JOURNAL if mentioned at all —
i.e., the actual guardrail phase 03 will need, tested here to see if a cheap
model can follow it at all.

All three models ran via `opencode run -m <model> "<prompt+transcript>"`, no
file/tool access needed or granted beyond replying with text. Raw verbatim
output (including opencode's CLI chrome) saved as `experiments/00-model-bakeoff/outputs/<model>.raw.txt`;
an ANSI-stripped copy as `<model>.txt` for readability.

### Grades

| Model | Captured what was built | Hallucinated undone work as done | Captured dead ends / failures | Prose worth keeping |
|---|---|---|---|---|
| `opencode/mimo-v2.5-free` | Yes — all 10 items, accurate detail (file names, the `next-url` vs `rsc` finding, the arithmetic-repair regex) | **No.** Both major proposals (first-crawl event suppression, LLM-first rewrite) explicitly marked "Discussed but not implemented." | Yes — cal.com's two-attempt fix, Wayback backfill quirk, calendly's two separate root causes | **Yes, close to as-is.** Clean, well-organized, correctly scoped CHANGELOG/JOURNAL split. |
| `opencode/longcat-2.0-free` | Yes — all 10 items, plus best voice/framing of *why* things mattered ("the middleware fix was the highest-leverage") | **No.** Same two proposals correctly flagged, plus a dedicated closing line: "Things discussed but not built: LLM-first normalize layer, ATS detection, first-crawl event suppression, headless SPA rendering." | Yes — best of the three at this; explicitly narrates the cal.com two-false-starts arc and draws the general lesson ("when the model can't be trusted to follow instructions, defend at the parse boundary") | **Yes, best of the three.** Reads like a human engineer wrote it. |
| `opencode/deepseek-v4-flash-free` | Yes, substantively — same 10 items covered, correct causal detail | **No** in substance — both proposals correctly marked not-built ("not built yet — awaiting green light," "user has it in their hands") | Yes, and arguably the most self-aware framing ("today's two most humbling dead-ends") | **No, not as-is.** Correct content buried in visibly glitched prose — see below. |

### deepseek-v4-flash-free's quality problem

deepseek got the substance right (including the hallucination guardrail) but its
prose has scattered, unmistakable generation artifacts that a human would have to
edit out before shipping: `"boring a window worth of history"` (nonsense verb),
`"B5: my first parse-fix didn't change anything"` (stray label from nowhere),
`"lūdīt valid"` (non-English garbage token, mid-sentence), `"D-BG staff
redirect"`, `"dex cover"`, `"seven.sixth with the"`, `"tele-only"`, and one factual
slip — it lists the surviving companies as `"cal.com…, rezi.com, groq.com..."`,
inventing a stray `cal.com` entry and misspelling `rezi.ai` as `rezi.com`. None of
this changes what it's claiming happened, but it means this specific model's raw
output is not "prose you'd keep" — it would need a real edit pass, not just a
skim, which undercuts the "cheap, unattended, no-input" premise of the whole tool.

### Sample — the load-bearing test (LLM-first proposal, correctly NOT reported as done)

mimo (JOURNAL.md): *"Proposed LLM-first normalization using `generateObject` with
Zod schemas for all five kinds — cost is negligible (~$2/year at current scale)...
**Discussed but not implemented.**"*

longcat (JOURNAL.md): *"Proposed LLM-first with schema-enforced output
(`generateObject` + Zod) for all five kinds... **Not implemented** — flagged as a
real tradeoff needing a decision."*

Both models — the two with clean prose — passed the exact test the plan calls out
as the common failure mode, on the hardest instance in the transcript (a
long, detailed, technically-specific proposal that ends on a direct
"want me to build it?" question).

---

## 6. GATE VERDICT: **PASS**

Two of three free-tier models reachable with zero setup (`opencode/mimo-v2.5-free`,
`opencode/longcat-2.0-free`) produced a CHANGELOG and a JOURNAL entry from a real,
long, messy transcript that:

- correctly separate what was actually built (10 distinct pieces of work across
  UI, crawler, normalizer, and DB layers) from what was only proposed,
- explicitly and correctly flag the session's biggest hallucination trap — a
  detailed, costed, technically fleshed-out architecture proposal that ends on
  "want me to build it?" — as not-built, unprompted beyond the general guardrail
  in the system prompt,
- capture the JOURNAL-shaped material the plan cares about: the cal.com
  two-attempt fix and the general lesson drawn from it, the `next-url`-vs-`rsc`
  debugging saga, the Wayback backfill mechanism and why it confused the user.

This is prose I would keep with light or no editing. The gate is not "does the
model get everything perfect" — it's "does a model exist that clears the bar
cheaply." One does; two do.

**Default writing model recommendation: `opencode/longcat-2.0-free`**, with
`opencode/mimo-v2.5-free` as a strong second choice / fallback if longcat is
ever unavailable. Both are free-tier through opencode's existing connector — no
config change needed beyond the model string the plan's example config already
shows the shape of. Do **not** default to `opencode/deepseek-v4-flash-free`
despite it being the plan's example default — on this transcript its output
needed a real edit pass, which is a second cost this design was trying to avoid.

**What phase 01/03 should carry forward:**
- The `<task-notification>` shape (finding #8 in §2) needs handling — it slips
  past a "is content a string?" check and could get misread as a real user turn.
  Either filter on the `<task-notification>` wrapper the way slash-commands are
  filtered, or explicitly instruct the writer model to disregard it.
- The hallucination guardrail worked here because the prompt stated it directly
  and forcefully ("do NOT list things that were only discussed... if you mention
  it in JOURNAL, be explicit that it was only discussed/proposed"). This isn't a
  capability that shows up for free — phase 03's per-doc prompts need to carry
  an equally explicit version of this instruction, not assume the model infers it.
- `codex` is currently unusable on this machine (broken npm install, not an auth
  problem) — worth fixing before phase 04 needs a second connector to exist for
  real, but not blocking phase 00's verdict since opencode alone answers the gate
  question.

---

## Files

- `experiments/00-model-bakeoff/parser.py` — the parser, with format facts documented in its docstring
- `experiments/00-model-bakeoff/cleaned_transcript_scout-eb60511f.md` — the cleaned rendering graded against (eyeballed, no secrets)
- `experiments/00-model-bakeoff/prompt.md` — the exact bakeoff prompt (instructions only, transcript appended at run time)
- `experiments/00-model-bakeoff/outputs/*.raw.txt` — verbatim raw CLI output per model
- `experiments/00-model-bakeoff/outputs/*.txt` — same, ANSI codes stripped for readability
