package worker

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// buildPrompt assembles the single prompt handed to the writer this run:
// the current content of all four docs, plus the transcript entries new
// since the last run.
//
// Phase 01 keeps this to one prompt covering all four docs, per docs/PLAN.md
// ("Prompts: keep them simple in phase 01 ... phase 03 is where they get
// split per-doc and tuned"). Don't add per-doc prompts, weighting, or code
// access here — that's phase 03 work, and doing it now would be gold-plating
// against a spec that isn't written yet.
func buildPrompt(current map[scribe.Doc]string, entries []scribe.Entry) string {
	var b strings.Builder
	b.WriteString(promptInstructions)

	for _, d := range scribe.AllDocs {
		fmt.Fprintf(&b, "\n--- current %s ---\n%s\n", d, current[d])
	}

	b.WriteString("\n--- new transcript entries since the last run ---\n")
	for _, e := range entries {
		ts := e.Timestamp
		if ts.IsZero() {
			fmt.Fprintf(&b, "[%s]: %s\n", e.Role, e.Text)
		} else {
			fmt.Fprintf(&b, "[%s %s]: %s\n", ts.Format(time.RFC3339), e.Role, e.Text)
		}
	}

	return b.String()
}

// promptInstructions defines the writer's output contract: a single JSON
// object, one key per doc that actually needs to change, nothing else. This
// keeps parseEdits (parse.go) simple and unambiguous — no free-text format
// to reverse-engineer, no partial-doc diffing.
const promptInstructions = `You maintain four markdown docs that describe this repository, based on a
Claude Code transcript. You are told what changed since the last run and the
current content of all four docs. Decide which docs, if any, need to change.

PROJECT.md and DECISIONS.md hold current state only, no history — if you
change them, return the complete new file content; it replaces the old
content entirely.

CHANGELOG.md and JOURNAL.md are append-only history — if you change them,
return only the new entry (or entries) to add; it is appended to the
existing file, never replaces it. Most sessions only touch these two:
PROJECT.md and DECISIONS.md should only move when something product-level
actually happened (a new decision made, dropped, or superseded; what the
project is or who it's for changed).

If reversing an earlier decision, still write DECISIONS.md with a block for
it marked dropped/superseded and the reason — don't just delete it.

Respond with ONLY a single JSON object and nothing else: no prose, no
markdown code fence. Keys are a subset of exactly these four strings:
"PROJECT.md", "DECISIONS.md", "CHANGELOG.md", "JOURNAL.md". Omit a key
entirely if that doc doesn't need to change this run. Values are strings as
described above.
`
