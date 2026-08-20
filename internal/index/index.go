// Package index renders scribe's session index — the file
// <repoRoot>/docs/scribe/INDEX.md — from the session records owned by
// internal/sessions. Phase 05 (docs/phases/05-digest-and-index.md) adds two
// views over the same record list; this package owns the index view, whose
// whole job is to be skimmable: one short, dated line per session, newest
// first, so a human scanning the file sees "what happened recently, at a
// glance" without opening a transcript.
//
// There is no model and no intelligence here. Rendering is a pure,
// deterministic function of the record slice — the same records always
// produce byte-identical markdown, with no time.Now, no map iteration and no
// caller order leaking into the output. That determinism is what lets the
// index be committed to git and diffed meaningfully: a change in the file
// means a change in the sessions, never rendering noise.
//
// The weekly grouping and prose belong to the digest (internal/digest); the
// index deliberately stays a single flat reverse-chronological list. Keeping
// the two views separate is the point — the index is the terse lookup, the
// digest is the narrative.
package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/sessions"
)

// indexFileName is the index doc's name within scribe.DocsDir. It is not one
// of scribe.AllDocs (those are the four writer-maintained docs); the index is
// a separate, mechanically rendered artifact that lives in the same folder so
// everything scribe produces for a repo sits under docs/scribe/.
const indexFileName = "INDEX.md"

// header is the top-of-file title, emitted even for an empty record slice so
// the file is always valid, self-describing markdown rather than a bare or
// zero-byte file. The trailing blank line separates the title from the list.
const header = "# Session index\n"

// noSummaryPlaceholder stands in for a record whose Summary is empty. A
// session that ran but produced no summary is itself worth a line — dropping
// it would hide that the session happened — so it renders with the date and
// category and this placeholder rather than being skipped.
const noSummaryPlaceholder = "(no summary)"

// dateLayout is the date shown per line: calendar day only, no clock time.
// The index answers "roughly when", and a bare YYYY-MM-DD keeps every line
// the same width so the list stays scannable.
const dateLayout = "2006-01-02"

// categoryTags maps each session category to the short, fixed-width label
// shown on its line. The tags are deliberately terse and uniform in length
// (feature -> "feat", bug -> "bug ", general -> "gen ") so the summaries line
// up in a column and the eye can filter by kind at a glance. An unknown
// category (should not occur — sessions.Category is a closed set) falls back
// to categoryFallbackTag rather than rendering a ragged label.
var categoryTags = map[sessions.Category]string{
	sessions.CategoryFeature: "feat",
	sessions.CategoryBug:     "bug ",
	sessions.CategoryGeneral: "gen ",
}

// categoryFallbackTag labels a record whose Category is not one of the known
// constants. It is the same width as the real tags so a stray value can never
// break the column alignment.
const categoryFallbackTag = "?   "

// Render turns records into the full INDEX.md markdown body. It is a pure
// function of its input — no I/O, no clock, no dependence on the caller's
// slice order — so tests assert directly against its string result and Write
// persists exactly what it returns.
//
// Lines are sorted newest first (Started descending, ties broken by
// SessionID ascending for a total, deterministic order); the input is not
// mutated. Each record becomes exactly one line — any newline that slipped
// into a Summary is flattened to a space so one record can never render as
// two rows, guarding the layout even though callers are contracted to pass
// single-line summaries.
func Render(records []sessions.Record) string {
	// Copy before sorting so a caller's slice (e.g. the canonical
	// ascending order from sessions.All) is never reordered underneath it.
	sorted := make([]sessions.Record, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].Started.Equal(sorted[j].Started) {
			// Newest first: later Started sorts earlier.
			return sorted[i].Started.After(sorted[j].Started)
		}
		// Tie-break on SessionID ascending for full determinism when many
		// sessions share an identical Started.
		return sorted[i].SessionID < sorted[j].SessionID
	})

	var b strings.Builder
	b.WriteString(header)
	if len(sorted) > 0 {
		// Blank line between the title and the list, only when there is a
		// list — the empty index is header-only with no trailing blank body.
		b.WriteByte('\n')
	}
	for _, r := range sorted {
		b.WriteString(renderLine(r))
		b.WriteByte('\n')
	}
	return b.String()
}

// renderLine renders one record as a single index row:
//
//	- 2026-08-20 · feat · <summary>
//
// It returns the line without its trailing newline (Render adds that). The
// middle-dot separators are purely visual; the fields are date, category tag
// and summary in a fixed order so every line reads the same way.
func renderLine(r sessions.Record) string {
	tag, ok := categoryTags[r.Category]
	if !ok {
		tag = categoryFallbackTag
	}
	return "- " + r.Started.Format(dateLayout) + " · " + tag + " · " + summaryText(r.Summary)
}

// summaryText returns the display summary for a record: the placeholder if it
// is empty, otherwise the summary flattened to a single line. Flattening
// replaces every CR and LF with a space (then trims trailing spaces) so a
// multi-line summary collapses into one row instead of injecting extra lines
// into the file. This is the index's own defense; the caller is contracted to
// pass single-line summaries, but the render must not be able to break its own
// layout if the caller slips.
func summaryText(summary string) string {
	if summary == "" {
		return noSummaryPlaceholder
	}
	flat := strings.ReplaceAll(summary, "\r\n", " ")
	flat = strings.ReplaceAll(flat, "\n", " ")
	flat = strings.ReplaceAll(flat, "\r", " ")
	flat = strings.TrimRight(flat, " ")
	if flat == "" {
		// A summary made entirely of newline/whitespace flattens to nothing;
		// fall back to the placeholder rather than an empty tail.
		return noSummaryPlaceholder
	}
	return flat
}

// Path returns the absolute path INDEX.md would be written to under repoRoot.
// Useful to tests and callers wiring up related paths.
func Path(repoRoot string) string {
	return filepath.Join(repoRoot, scribe.DocsDir, indexFileName)
}

// Write renders records and writes docs/scribe/INDEX.md under repoRoot. It
// creates docs/scribe/ if absent and writes atomically (temp file + rename in
// the same directory), so a crash or concurrent read never observes a torn or
// empty file — the index is committed to git, and a half-written index would
// be a confusing diff. An empty record slice still writes a valid,
// header-only file.
func Write(repoRoot string, records []sessions.Record) error {
	dir := filepath.Join(repoRoot, scribe.DocsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, indexFileName), []byte(Render(records)))
}

// atomicWrite writes data to path via temp file + rename, so a reader (or a
// crash) never observes a partially written file. The temp file is created in
// the same directory as path so the rename stays on one filesystem and is
// atomic per POSIX semantics. This mirrors internal/docs' and
// internal/sessions' atomicWrite deliberately as its own copy — phase 05 asks
// for the same idiom without a cross-package dependency just to share it.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup: if we return before the rename succeeds, don't
	// leave the temp file behind.
	succeeded := false
	defer func() {
		if !succeeded {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	succeeded = true
	return nil
}
