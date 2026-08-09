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

Keep it terse — a couple of short paragraphs, not an essay. Someone should
be able to read the whole thing in under a minute.`,
	},
	scribe.DocDecisions: {
		isState: true,
		guidance: `You maintain DECISIONS.md: one block per decision, marked active,
dropped, or superseded. Only write here when something was actually
chosen, dropped, or superseded. "We used approach X" belongs in
CHANGELOG.md unless X was genuinely weighed against an alternative and
picked. Keep each block terse: what was decided, one line why.`,
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
		guidance: `You maintain JOURNAL.md: what you were stuck on, what the AI got
confidently wrong, what you tried that failed, and what the fix turned
out to be. Not a narration of every tool call.`,
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
