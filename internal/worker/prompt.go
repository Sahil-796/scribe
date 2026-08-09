package worker

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// Phase 03 ("make the writing good") splits what used to be one combined
// prompt into one small job per doc (docs/PLAN.md phase 03, item 1): a
// CHANGELOG call shouldn't be handed DECISIONS guidance, and a PROJECT call
// shouldn't be handed CHANGELOG examples. Each call below gets only the
// current content of the one doc it's allowed to change plus the new
// transcript entries — not the other three docs — so its prompt stays
// small and its instructions stay focused.
//
// This costs writer calls: one per doc per run instead of one per run, and
// each real call is a measured 7.8-15.6s (docs/findings). That's the trade
// this phase makes deliberately — separate invocations, not one invocation
// with sectioned output, because a model asked to do four jobs in one pass
// tends to do all four badly rather than one well.

// noChangeSentinel is what a per-doc call returns when that doc doesn't
// need to change this run. A plain empty string isn't used for this because
// it's indistinguishable from "the writer produced no output" (the
// auto-reject failure mode docs/findings/00-writer.md describes) — an
// explicit sentinel lets parseDocOutput tell "nothing to say" apart from
// "something went wrong" instead of guessing.
const noChangeSentinel = "NO_CHANGE"

// gateKeywords is the cheap prefilter in front of the (still cheap, but not
// free) classification call in gateProductLevel (worker.go). Phase 03 item
// 2 asks for a gate the writer answers itself over a brittle keyword
// heuristic, but allows a keyword prefilter in front of it — this is that
// prefilter: if none of these show up anywhere in the new entries, there's
// no point paying for a classification call, PROJECT and DECISIONS are
// skipped outright. If one does show up, that's not a verdict, just a
// reason to ask the cheap classifier for a real answer (buildGatePrompt).
var gateKeywords = []string{
	"decide", "decided", "decision", "instead of", "trade-off", "tradeoff",
	"pivot", "scrap", "abandon", "chose", "choose between", "requirement",
	"no longer", "rename the project", "who this is for", "target audience",
	"roadmap", "out of scope", "in scope", "drop the", "dropped the",
	"supersede", "superseded",
}

// keywordPrefilter reports whether any new entry looks like it might
// contain product-level talk. A false positive just costs one cheap
// classification call; a false negative silently starves PROJECT/DECISIONS
// of an update they deserved, so this errs toward matching too much rather
// than too little.
func keywordPrefilter(entries []scribe.Entry) bool {
	for _, e := range entries {
		lower := strings.ToLower(e.Text)
		for _, kw := range gateKeywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}
	return false
}

// buildGatePrompt asks the writer a single cheap yes/no question: did
// anything product-level happen in this slice of conversation? This is the
// "writer answers it in one cheap classification step" gate phase 03 item 2
// prefers over a brittle keyword heuristic alone — keywordPrefilter decides
// whether it's worth asking at all, this decides the actual answer.
func buildGatePrompt(entries []scribe.Entry) string {
	var b strings.Builder
	b.WriteString(`Answer one question about the conversation excerpt below: does it
contain product-level talk — a decision made, dropped, or superseded; a
change to what the project is, who it's for, or where it stands?

Ordinary engineering work (reading code, writing a function, fixing a bug,
running tests, refactoring) is NOT product-level by itself, even if it's
substantial. Only answer YES if something was actually chosen, dropped, or
the shape of the project itself changed.

Respond with exactly one word: YES or NO. Nothing else — no punctuation, no
explanation.

--- conversation ---
`)
	writeEntries(&b, entries)
	return b.String()
}

// parseGateOutput reads the classification call's answer. Anything that
// isn't a clear YES is treated as NO — this gate exists to keep PROJECT and
// DECISIONS from being bothered for ordinary sessions, so an ambiguous
// answer should fail closed (skip), not fail open (run two more calls that
// probably weren't needed).
func parseGateOutput(out string) bool {
	cleaned := strings.ToUpper(strings.TrimSpace(stripCodeFence(strings.TrimSpace(out))))
	return strings.HasPrefix(cleaned, "YES")
}

// writeEntries renders the new transcript entries in the shared
// "[timestamp role]: text" format every per-doc prompt uses.
func writeEntries(b *strings.Builder, entries []scribe.Entry) {
	for _, e := range entries {
		ts := e.Timestamp
		if ts.IsZero() {
			fmt.Fprintf(b, "[%s]: %s\n", e.Role, e.Text)
		} else {
			fmt.Fprintf(b, "[%s %s]: %s\n", ts.Format(time.RFC3339), e.Role, e.Text)
		}
	}
}

// docPromptSpec is one doc's focused job: its output-format instructions
// (state doc = full replacement, history doc = entries to append) plus its
// own guidance, kept separate from every other doc's.
type docPromptSpec struct {
	guidance string // what this doc is for and how to write it well
	isState  bool   // true = full-file replacement, false = append-only entry
}

var docPrompts = map[scribe.Doc]docPromptSpec{
	scribe.DocProject: {
		isState: true,
		guidance: `You maintain PROJECT.md: what this thing is, who it's for, where it
stands. Current state only — no history, no dated entries, that's what
CHANGELOG.md is for.

The correction path is the normal case, not an edge case (locked decision
2): if this conversation scraps or supersedes something PROJECT.md
currently claims, remove that claim now — a later reply is allowed to be
righter than an earlier one, and PROJECT.md should never keep asserting
something that's no longer true. You don't need to explain why here; that
reason belongs in DECISIONS.md, written separately.

Keep it terse — a couple of short paragraphs, not an essay. Someone should
be able to read the whole thing in under a minute.`,
	},
	scribe.DocDecisions: {
		isState: true,
		guidance: `You maintain DECISIONS.md: one block per decision, marked active,
dropped, or superseded. Dropping always carries the reason — never delete
a decision's block outright.

The correction path is the normal case, not an edge case (locked decision
2): when this conversation drops or supersedes an earlier decision, keep
that decision's block, mark it dropped (or "superseded by <the new one>"),
and say why in a line or two. Being wrong and reversing gets recorded, not
hidden — that record is the point of this file.

Only write here when something was actually chosen, dropped, or
superseded. "We used approach X" belongs in CHANGELOG.md unless X was
genuinely weighed against an alternative and picked. Keep each block terse:
what was decided, one line why, and (when relevant) what it replaced.`,
	},
	scribe.DocChangelog: {
		isState: false,
		guidance: `You maintain CHANGELOG.md: dated, append-only lines — what was built,
fixed, or ripped out. You're shown only the new conversation since the
last run, never the whole file; return just the new entry or entries to
add, never a rewrite of history you can't see.

Good entry: "fixed: offset saves were racing with the pending-flag check"
Bad entry: a restatement of every tool call ("read worker.go, edited
worker.go, ran go test") — that's a tool log, not a changelog. One line
per notable thing; terse beats complete.`,
	},
	scribe.DocJournal: {
		isState: false,
		guidance: `You maintain JOURNAL.md — the highest-value doc of the four, and the
easiest to fill with filler. It wants: problems actually hit, things the
AI got confidently wrong, dead ends tried and abandoned, and what the fix
turned out to be.

It does NOT want a narration of every tool call, and it does NOT want a
restatement of the changelog — "added the retry loop" belongs in
CHANGELOG.md; JOURNAL.md is for *why it was hard*, if it was.

Good entry:
"Assumed the offset was a line count; it's a byte count. Wasted twenty
minutes on an off-by-one before rereading the doc comment on
transcript.Read."

Bad entry:
"Read worker.go, then read parse.go, made the requested change, and ran
the tests, which passed."

A quiet session with no real friction gets no entry — that's success, not
an omission. Don't manufacture a struggle that wasn't there.`,
	},
}

// buildDocPrompt assembles one per-doc call: that doc's own guidance, its
// output-format contract, the current content of *only this doc*, and the
// new transcript entries. No other doc's content is included — a CHANGELOG
// call has no business reading DECISIONS.md's current text, and keeping
// the prompt small keeps the job focused (phase 03 item 1).
func buildDocPrompt(doc scribe.Doc, currentContent string, entries []scribe.Entry) string {
	spec, ok := docPrompts[doc]
	if !ok {
		panic(fmt.Sprintf("worker: buildDocPrompt: no prompt spec for doc %q", doc))
	}

	var b strings.Builder
	b.WriteString(spec.guidance)
	b.WriteString("\n\n")

	if spec.isState {
		fmt.Fprintf(&b, `If %s needs to change, respond with ONLY the complete new file content —
it replaces the old file entirely. If nothing here changes what %s should
say, respond with exactly: %s

`, doc, doc, noChangeSentinel)
	} else {
		fmt.Fprintf(&b, `If %s needs a new entry, respond with ONLY the entry (or entries) to
append — never the whole file, you aren't shown the whole file. If nothing
here is worth adding, respond with exactly: %s

`, doc, noChangeSentinel)
	}
	b.WriteString(`Respond with nothing else in either case: no prose, no markdown code
fence, no explanation before or after.

`)

	fmt.Fprintf(&b, "--- current %s ---\n%s\n", doc, currentContent)
	b.WriteString("\n--- new conversation since the last run ---\n")
	writeEntries(&b, entries)

	return b.String()
}
