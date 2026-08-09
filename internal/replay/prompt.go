package replay

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// buildChangelogPrompt and buildJournalPrompt each assemble the prompt for
// one chunk of a past session's transcript, for one doc. Unlike the live
// worker prompt (internal/worker/prompt.go), replay never sees or edits
// PROJECT.md/DECISIONS.md, and never carries accumulated doc state between
// calls — each chunk is judged only on its own entries. That's what lets
// any one writer call stay small regardless of how large the overall
// session was; see chunkEntries in replay.go.
//
// CHANGELOG.md and JOURNAL.md get separate prompt calls rather than one
// combined ask. They want different things from the same material — a
// terse dated fact vs. a narrative of friction — and asking for both at
// once in a single call tends to blur into one mediocre pass over both.
// Splitting doubles the writer calls per chunk, which is the accepted
// tradeoff during init (a one-off pass, not the ongoing per-turn loop) —
// see the commit message for this change.
func buildChangelogPrompt(entries []scribe.Entry) string {
	var b strings.Builder
	b.WriteString(changelogInstructions)
	b.WriteString(renderChunk(entries))
	return b.String()
}

func buildJournalPrompt(entries []scribe.Entry) string {
	var b strings.Builder
	b.WriteString(journalInstructions)
	b.WriteString(renderChunk(entries))
	return b.String()
}

// renderChunk renders the transcript entries shared by both prompt
// builders above, after chunkContext (also shared) has set the scene.
func renderChunk(entries []scribe.Entry) string {
	var b strings.Builder
	b.WriteString(chunkContext)
	b.WriteString("\n--- transcript entries ---\n")
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

// chunkContext is the preamble both changelogInstructions and
// journalInstructions share verbatim: what a chunk is, and the one
// recurring failure mode (recording discussion as if it shipped) both docs
// must avoid. Keeping it in one place means the two prompts can't drift
// apart on this shared framing.
const chunkContext = `You are replaying a PAST Claude Code transcript segment, after the fact, to
backfill a repository's history docs so they have real content from day one
instead of starting empty. Write in the past tense, as history — this is
not a live session, and you are not narrating something happening right
now.

You are shown one chunk of entries from a session's transcript. It may be a
small slice of a much longer session; other chunks are handled in separate
calls that do not see this one, and that you will not see either. Judge
only this chunk: do not write a preamble framing this as part of a series,
do not summarize "what happened before" or guess "what happens next," and
do not restate or contradict what a reasonable earlier or later chunk would
say. Just report what this slice itself shows.

The recurring failure to avoid: a transcript can contain long discussion of
something that was never actually built, or a plan that was started and
then abandoned. Only report what the entries show was actually done — files
edited, commands run, tests passed, code that shipped. Discussion is not
shipment; something merely proposed, debated, or planned does not belong in
either doc.
`

// changelogInstructions is CHANGELOG.md's half of the split: a terse, dated
// fact, nothing more.
const changelogInstructions = `Your job for this call is CHANGELOG.md only: a terse, dated record of what
was built, fixed, or ripped out.

Write one line per concrete change, in changelog style, not prose. Only
include something if this chunk shows it actually happened. Skip pure
exploration, a false start with no resolution, or discussion that led
nowhere concrete — most chunks will have nothing to add here, and that is
the expected common case, not a failure.

Good: "- switched config loader from flag parsing to a YAML file"
Bad:  "- worked on config for a while" (not concrete, says nothing)
Bad:  "- discussed possibly moving to YAML config" (never happened)

Date entries using the entry timestamps you're given; do not invent a date
that isn't present in this chunk.

Respond with ONLY a single JSON object and nothing else: no prose, no
markdown code fence. The only key you may use is "CHANGELOG.md" — omit it
entirely, or respond with "{}", if this chunk has nothing to add. The value
is the new entry text to append: just what this chunk adds, not a full
file.
`

// journalInstructions is JOURNAL.md's half of the split. This is the
// highest-value and easiest-to-fill-with-filler doc scribe produces, so it
// gets the most explicit guidance of the four docs across both passes.
const journalInstructions = `Your job for this call is JOURNAL.md only: what was actually stuck, what
the AI got confidently wrong, dead ends tried and abandoned, and what the
fix turned out to be.

Wants:
- a bug whose real cause turned out to be something other than the first
  fix attempted
- an approach that was tried, failed, and was abandoned, and why
- a case where the AI (or the human) was confidently wrong about how
  something worked, before finding out otherwise
- a workaround adopted because the "right" fix wasn't worth doing yet

Does NOT want:
- a list of tool calls or commands run
- a restatement of what CHANGELOG.md already says, in different words
- narration of routine, friction-free work ("read the file, made the edit,
  tests passed")

Good: "assumed the timeout was in the HTTP client; it was actually the
reverse proxy in front of it silently capping requests at 30s — lost an
hour before checking the proxy config."
Bad:  "fixed a bug in the client" (no story — this belongs in CHANGELOG.md
if anywhere, not here)
Bad:  "ran the tests, they passed, then ran the build" (routine, not
friction)

Most chunks have nothing like this — pure exploration or friction-free work
is the common case, not a failure to report. Skip it rather than padding
with routine narration.

Respond with ONLY a single JSON object and nothing else: no prose, no
markdown code fence. The only key you may use is "JOURNAL.md" — omit it
entirely, or respond with "{}", if this chunk has nothing to add. The value
is the new entry text to append: just what this chunk adds, not a full
file.
`
