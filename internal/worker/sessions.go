package worker

// Phase 05 ("digest and index") adds a fifth, small writer job on top of the
// four per-doc calls: once per session per run, summarise what that session
// did in one skimmable line and classify it as a feature, a bug, or general
// work. The result is upserted into internal/sessions, from which
// internal/index renders docs/scribe/INDEX.md and internal/digest renders the
// weekly digests — both pure functions of the record list, so no writer call
// happens at render time (see cmd/scribe/run.go).
//
// The summary is its own call rather than something scraped out of the
// CHANGELOG/JOURNAL edits for the same reason phase 03 split the four docs
// apart (see prompt.go): one prompt doing one job does it better, and the
// index line wants a different shape (one terse line + a category) than any
// of the four docs produce. It is deliberately the cheapest of the run's
// calls — a single line of output — and, like every other builder here, its
// prompt goes through the redaction choke point before a writer sees it.

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/redact"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/sessions"
)

// SessionRecorder is the subset of *sessions.Store the worker needs to keep
// the phase 05 index and digests fed: one upsert per session per run. It is
// optional on Deps (a nil Sessions skips recording entirely, which is what
// this package's own unit tests do) precisely because the index and digest
// are secondary artifacts — a skimmable view of history, not history itself.
// A failure to record one must degrade to "the index is stale", never fail
// the doc-writing run it rides along with, the same way the `scribe diff`
// snapshot does in Run.
type SessionRecorder interface {
	Upsert(sessions.Record) error
}

// buildSummaryPrompt asks the writer for one index line plus a category for a
// single session's new transcript entries. The strict two-line CATEGORY/
// SUMMARY shape is what parseSummaryOutput reads back; asking for exactly
// that, and nothing else, is what keeps a cheap model's output parseable.
//
// r is the redaction choke point (phase 04): the entries embedded below are
// raw transcript content, so the assembled prompt is redacted in one pass at
// the return, exactly like buildDocPrompt and buildGatePrompt. r is required
// — a nil Redactor panics via r.Redact rather than sending content in the
// clear (see internal/redact and TestBuildSummaryPromptPanicsOnNilRedactor).
func buildSummaryPrompt(entries []scribe.Entry, r *redact.Redactor) string {
	var b strings.Builder
	b.WriteString(`Summarise the coding session excerpt below in one line for a skimmable
index, and classify it.

Respond in EXACTLY this format — two lines, nothing before or after:
CATEGORY: <feature|bug|general>
SUMMARY: <one line, plain past tense, what actually happened>

Rules:
- CATEGORY is "feature" for new capability added, "bug" for a fix to
  something broken, "general" for everything else (refactors, docs, chores,
  exploration, configuration).
- SUMMARY is a single line, roughly fifteen words or fewer, no markdown, no
  leading dash. Describe what happened, not what was discussed.

--- conversation ---
`)
	writeEntries(&b, entries)
	return r.Redact(b.String())
}

// parseSummaryOutput decodes buildSummaryPrompt's expected reply: a CATEGORY
// line and a SUMMARY line, in any order, case-insensitive on the labels, and
// tolerant of a stray code fence the way parseDocOutput is. An unrecognised
// or missing category is not an error — it defaults to general, the neutral
// bucket — because getting the one-line summary recorded matters more than a
// perfect label, and a wrong-but-present line still tells a human a session
// happened. A missing SUMMARY line, on the other hand, is a hard error: it
// means the call didn't do its one job, and recording a blank line would be
// indistinguishable from a session that genuinely produced nothing to say.
func parseSummaryOutput(out string) (summary string, cat sessions.Category, err error) {
	cleaned := stripCodeFence(strings.TrimSpace(out))
	cat = sessions.CategoryGeneral
	foundSummary := false
	var summaryParts []string
	capturing := false // inside a SUMMARY value, folding in continuation lines
	for _, line := range strings.Split(cleaned, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case hasLabel(trimmed, "CATEGORY:"):
			cat = parseCategory(strings.TrimSpace(trimmed[len("CATEGORY:"):]))
			capturing = false
		case hasLabel(trimmed, "SUMMARY:"):
			summaryParts = append(summaryParts, strings.TrimSpace(trimmed[len("SUMMARY:"):]))
			foundSummary = true
			capturing = true
		case capturing && trimmed != "":
			// A continuation line: the writer wrapped its one-line summary
			// across a newline. Fold it back in rather than dropping the
			// tail. A labelled line (handled above) ends the capture.
			summaryParts = append(summaryParts, trimmed)
		}
	}
	if !foundSummary {
		return "", "", fmt.Errorf("writer returned no SUMMARY line (want %q then %q), got: %q", "CATEGORY: ...", "SUMMARY: ...", cleaned)
	}
	return flattenLine(strings.Join(summaryParts, " ")), cat, nil
}

// hasLabel reports whether line begins with label, ignoring case on the
// label — cheap models capitalise inconsistently and the label is fixed.
func hasLabel(line, label string) bool {
	return len(line) >= len(label) && strings.EqualFold(line[:len(label)], label)
}

// parseCategory maps the writer's category word to a sessions.Category,
// defaulting anything unrecognised to general rather than erroring (see
// parseSummaryOutput's rationale).
func parseCategory(s string) sessions.Category {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(sessions.CategoryFeature):
		return sessions.CategoryFeature
	case string(sessions.CategoryBug):
		return sessions.CategoryBug
	default:
		return sessions.CategoryGeneral
	}
}

// flattenLine collapses any newline that slipped into a one-line field to a
// space, so one session can never become two index lines. index.Render does
// this too — belt and braces, since the record on disk is what the digest
// also reads.
func flattenLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// recordSession makes the one summary call for a session's new entries and
// upserts the result. Started is stamped with now on every call; the store
// preserves the earliest Started across upserts (see internal/sessions), so
// passing now each run is correct — a session's first sighting fixes its
// start, later runs only move Updated/Summary/Category.
func recordSession(deps Deps, sessionID string, entries []scribe.Entry, now time.Time) error {
	out, err := deps.Writer.Run(buildSummaryPrompt(entries, deps.Redactor))
	if err != nil {
		return fmt.Errorf("worker: summary run for session %s: %w", sessionID, err)
	}
	summary, cat, err := parseSummaryOutput(out)
	if err != nil {
		return fmt.Errorf("worker: parse summary for session %s: %w", sessionID, err)
	}
	return deps.Sessions.Upsert(sessions.Record{
		SessionID: sessionID,
		Started:   now,
		Updated:   now,
		Summary:   summary,
		Category:  cat,
	})
}
