// Package digest renders scribe's weekly digests: one markdown file per ISO
// week under <repoRoot>/docs/scribe/digests/, grouping that week's sessions by
// category (Features / Bugs / General) with one bullet per session.
//
// A digest is a pure view over the session records internal/sessions already
// keeps — NO model runs here. Phase 05 (docs/phases/05-digest-and-index.md)
// deliberately makes the digest a mechanical render so it is cheap, offline
// and byte-for-byte deterministic: the same (week, records) always produces
// the same file, which keeps git diffs and tests stable.
//
// There is no scheduler. Every scribe run calls MaybeWrite, which materializes
// any *past* week that has records and no file yet — so the first run on or
// after a week boundary writes the just-completed week, and the same pass
// backfills any older week that never got written (scribe was off, or `init`
// replayed a long history in one go). The current, still-in-progress week is
// never digested, because more sessions can still land in it.
//
// All "which week does this belong to" questions route through
// sessions.WeekKey / sessions.ByWeek — this package never reimplements ISO
// week math, so a session lands in exactly one week for the digest, the index
// and every other view alike.
package digest

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/sessions"
)

// categoryOrder fixes the section order in a digest and the mapping from a
// record's Category to its section heading. It is a slice, not a map range, so
// output order can never leak Go's random map iteration — the three sections
// always appear Features, then Bugs, then General.
var categoryOrder = []struct {
	cat   sessions.Category
	title string
}{
	{sessions.CategoryFeature, "Features"},
	{sessions.CategoryBug, "Bugs"},
	{sessions.CategoryGeneral, "General"},
}

// Render turns one week's records into that week's digest markdown body.
//
// It is a pure function with no I/O. weekKey is the "YYYY-Www" key the records
// belong to; it is passed in rather than re-derived so the header can name the
// week even when the caller has already bucketed by it, and so Render never
// has to assume the records are non-empty or agree on a week.
//
// Sections with no records that week are omitted entirely — an empty "Bugs"
// heading is noise, so a quiet week only shows the sections that happened.
// Within a section, records are listed by Started ascending (chronological
// reads naturally), each as one bullet: the YYYY-MM-DD of Started followed by
// the one-line Summary, with any stray newline in the Summary flattened to a
// space so one record stays one bullet.
func Render(weekKey string, records []sessions.Record) string {
	var b strings.Builder

	b.WriteString("# Week ")
	b.WriteString(weekKey)
	if lo, hi, ok := weekRange(weekKey); ok {
		b.WriteString(fmt.Sprintf(" (%s – %s)", lo.Format("2006-01-02"), hi.Format("2006-01-02")))
	}
	b.WriteString("\n")

	// Bucket by category once, preserving the incoming order within each
	// bucket. Callers hand us a week's slice already sorted Started-ascending
	// (that is how sessions.ByWeek returns each group); we keep that order
	// rather than re-sorting so Render stays a pure, cheap transform and the
	// sort lives in exactly one place.
	byCat := make(map[sessions.Category][]sessions.Record, len(categoryOrder))
	for _, r := range records {
		byCat[r.Category] = append(byCat[r.Category], r)
	}

	for _, section := range categoryOrder {
		rs := byCat[section.cat]
		if len(rs) == 0 {
			continue // omit empty sections to keep the digest tight
		}
		b.WriteString("\n## ")
		b.WriteString(section.title)
		b.WriteString("\n\n")
		for _, r := range rs {
			b.WriteString("- ")
			b.WriteString(r.Started.Format("2006-01-02"))
			b.WriteString(" ")
			b.WriteString(flattenLine(r.Summary))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// flattenLine collapses any embedded newlines (and the whitespace around them)
// in s to single spaces, so a Summary that slipped its one-line contract still
// renders as exactly one bullet. Records are stored as-given by
// internal/sessions (it documents but does not enforce the one-line rule), so
// the render is the last place that guarantee is honored on screen.
func flattenLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return strings.Join(fields, " ")
}

// weekRange returns the Monday and Sunday bounding the ISO week named by
// weekKey ("YYYY-Www"). It is a nice-to-have for the header; ok is false when
// weekKey does not parse, in which case the caller simply omits the range.
//
// The math anchors on the ISO rule that Jan 4 always falls in ISO week 1: from
// the Monday of Jan 4's week, week N's Monday is (N-1)*7 days later. This is
// the inverse of time.ISOWeek() and stays consistent with sessions.WeekKey —
// we do not invent a second notion of week boundaries.
func weekRange(weekKey string) (monday, sunday time.Time, ok bool) {
	var year, week int
	if _, err := fmt.Sscanf(weekKey, "%d-W%d", &year, &week); err != nil {
		return time.Time{}, time.Time{}, false
	}
	if week < 1 || week > 53 {
		return time.Time{}, time.Time{}, false
	}
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	// Weekday with Monday=0 .. Sunday=6.
	offset := (int(jan4.Weekday()) + 6) % 7
	week1Monday := jan4.AddDate(0, 0, -offset)
	monday = week1Monday.AddDate(0, 0, (week-1)*7)
	sunday = monday.AddDate(0, 0, 6)
	return monday, sunday, true
}
