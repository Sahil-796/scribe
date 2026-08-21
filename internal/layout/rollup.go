package layout

import (
	"sort"
	"strings"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// SessionFile is one per-session history file as the rollup sees it: where it
// lives (Path, relative to docs/scribe/ — the same value SessionFilePath
// returns), what session it holds (Meta) and who wrote it (Author). The rollup
// needs only this much to render a link and a one-line description; it never
// reads the file's body.
type SessionFile struct {
	Path   string      // relative to docs/scribe/, e.g. "journal/2026-08-21-slug-a1b2c3d4.md"
	Meta   SessionMeta // date, session id and summary of the file
	Author string      // author recorded for the session; may be empty
}

// rollupHeaders is the top-of-file title for each history doc's rollup. The
// rollup IS the committed CHANGELOG.md / JOURNAL.md in per-session mode, so the
// title matches what a reader expects that file to be called.
var rollupHeaders = map[scribe.Doc]string{
	scribe.DocChangelog: "# Changelog\n",
	scribe.DocJournal:   "# Journal\n",
}

// Rollup renders the generated CHANGELOG.md / JOURNAL.md for per-session mode:
// a pure, deterministic markdown index over the individual session files, one
// newest-first line per file linking to it. In per-session mode nobody appends
// to CHANGELOG.md / JOURNAL.md by hand — those files are conflict-prone shared
// tails, which is exactly what this layout removes — so this rollup replaces
// them, regenerated from the session files whenever they change.
//
// It is a pure function of its input: it copies the slice before sorting, so
// the caller's order is never mutated, and orders lines newest-first by Date,
// breaking ties on SessionID ascending for a total, stable order that renders
// byte-identically every time (the property that lets the rollup be committed
// and diffed meaningfully). An empty slice still yields a valid, header-only
// file rather than a bare or zero-byte one. An unknown doc (not a history doc)
// falls back to a generic heading rather than panicking — callers pass a
// history doc, but the render must not be able to crash on a stray value.
func Rollup(doc scribe.Doc, files []SessionFile) string {
	// Copy before sorting so a caller's slice is never reordered underneath it.
	sorted := make([]SessionFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		di, dj := sorted[i].Meta.Date, sorted[j].Meta.Date
		if !di.Equal(dj) {
			return di.After(dj) // newest first
		}
		return sorted[i].Meta.SessionID < sorted[j].Meta.SessionID
	})

	header, ok := rollupHeaders[doc]
	if !ok {
		header = "# History\n"
	}

	var b strings.Builder
	b.WriteString(header)
	if len(sorted) > 0 {
		// Blank line between title and list only when there is a list; the
		// empty rollup is header-only with no trailing blank body.
		b.WriteByte('\n')
	}
	for _, f := range sorted {
		b.WriteString(renderRollupLine(f))
		b.WriteByte('\n')
	}
	return b.String()
}

// renderRollupLine renders one session file as a single rollup row:
//
//	- 2026-08-21 · [auth refactor](journal/2026-08-21-auth-refactor-a1b2c3d4.md) · alice
//
// without its trailing newline (Rollup adds that). The link text is the summary
// (or a placeholder when empty) and the link target is the file's relative Path,
// which resolves correctly because the rollup file sits at docs/scribe/ and the
// paths are relative to that same directory. The author trails the line; an
// empty author renders as the neutral unknownAuthor so every row has the same
// three fields.
func renderRollupLine(f SessionFile) string {
	summary := oneLine(f.Meta.Summary)
	if summary == "" {
		summary = noSummary
	}
	author := strings.TrimSpace(f.Author)
	if author == "" {
		author = unknownAuthor
	}
	// Guard the link text and path against characters that would break the
	// markdown link syntax if a summary or path ever carried them.
	linkText := escapeLinkText(summary)
	return "- " + f.Meta.Date.Format(dateLayout) + " · [" + linkText + "](" + f.Path + ") · " + author
}

// noSummary stands in for a file whose summary is empty, so its row still reads
// as a real, clickable entry rather than an empty link.
const noSummary = "(no summary)"

// escapeLinkText neutralises the two characters that would prematurely close a
// markdown link's bracketed text, so a summary containing "[" or "]" still
// renders as one intact link. It is deliberately minimal — summaries are short
// headlines, not arbitrary markdown.
func escapeLinkText(s string) string {
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	return s
}
