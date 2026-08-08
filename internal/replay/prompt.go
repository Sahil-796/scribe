package replay

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// buildReplayPrompt assembles the prompt for one chunk of a past session's
// transcript. Unlike the live worker prompt (internal/worker/prompt.go),
// replay never sees or edits PROJECT.md/DECISIONS.md, and never carries
// accumulated doc state between calls — each chunk is judged only on its
// own entries. That's what lets any one writer call stay small regardless
// of how large the overall session was; see chunkEntries in replay.go.
func buildReplayPrompt(entries []scribe.Entry) string {
	var b strings.Builder
	b.WriteString(replayPromptInstructions)
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

// replayPromptInstructions defines the writer's output contract for a
// replay chunk. Deliberately mirrors internal/worker/prompt.go's
// promptInstructions in shape (single JSON object, code-fence-free,
// omit-if-unchanged) so parseReplayEdits and internal/worker's parseEdits
// stay easy to compare — the only real difference is that replay's key set
// is the two history docs only, never PROJECT.md/DECISIONS.md.
const replayPromptInstructions = `You are replaying a PAST Claude Code transcript segment to seed a
repository's history docs, CHANGELOG.md and JOURNAL.md, so they have real
content from day one instead of starting empty. This is not a live session:
write about what happened in the past tense, as history, not as narration
of something happening right now.

You are shown one chunk of entries from a session's transcript. It may be a
small slice of a much longer session — other chunks are handled in separate
calls, so do not assume this is the whole session and do not reference
chunks you have not seen.

CHANGELOG.md is a terse, dated record of what changed. JOURNAL.md is a
narrative record of problems hit, mistakes made, dead ends, and the actual
fix — the story behind the changes, not just the outcome.

Decide, from this chunk alone, whether it contains anything worth recording
in either doc. Many chunks will not: pure exploration, a false start with no
resolution, back-and-forth that led nowhere concrete. Skip those rather than
inventing significance.

Respond with ONLY a single JSON object and nothing else: no prose, no
markdown code fence. Keys are a subset of exactly these two strings:
"CHANGELOG.md", "JOURNAL.md". Omit a key entirely if this chunk gives it
nothing to add. Respond with "{}" if this chunk has nothing worth recording
at all. Values are the new entry text to append for that doc — just what
this chunk adds, not a full file.
`
