package digest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/sessions"
)

// rec is a tiny helper to build a Record with a Started at the given date.
func rec(id string, started time.Time, cat sessions.Category, summary string) sessions.Record {
	return sessions.Record{
		SessionID: id,
		Started:   started,
		Updated:   started,
		Summary:   summary,
		Category:  cat,
	}
}

// day builds a UTC time at noon on the given date, away from any midnight
// boundary so a date's ISO week is unambiguous.
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func TestRenderAllCategories(t *testing.T) {
	// 2026-W33 is Mon 2026-08-10 .. Sun 2026-08-16.
	recs := []sessions.Record{
		rec("s1", day(2026, 8, 10), sessions.CategoryFeature, "add digest command"),
		rec("s2", day(2026, 8, 11), sessions.CategoryBug, "fix week boundary off-by-one"),
		rec("s3", day(2026, 8, 12), sessions.CategoryGeneral, "tidy up docs"),
	}
	got := Render("2026-W33", recs)

	want := "# Week 2026-W33 (2026-08-10 – 2026-08-16)\n" +
		"\n## Features\n\n" +
		"- 2026-08-10 add digest command\n" +
		"\n## Bugs\n\n" +
		"- 2026-08-11 fix week boundary off-by-one\n" +
		"\n## General\n\n" +
		"- 2026-08-12 tidy up docs\n"

	if got != want {
		t.Fatalf("Render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderOmitsEmptySections(t *testing.T) {
	recs := []sessions.Record{
		rec("s1", day(2026, 8, 10), sessions.CategoryFeature, "only a feature this week"),
	}
	got := Render("2026-W33", recs)

	if contains(got, "## Bugs") || contains(got, "## General") {
		t.Fatalf("expected empty Bugs/General sections omitted, got:\n%s", got)
	}
	if !contains(got, "## Features") {
		t.Fatalf("expected Features section, got:\n%s", got)
	}
}

func TestRenderFlattensNewlineInSummary(t *testing.T) {
	recs := []sessions.Record{
		rec("s1", day(2026, 8, 10), sessions.CategoryFeature, "line one\nline two\r\nline three"),
	}
	got := Render("2026-W33", recs)

	want := "- 2026-08-10 line one line two line three\n"
	if !contains(got, want) {
		t.Fatalf("expected flattened bullet %q in:\n%s", want, got)
	}
	if contains(got, "\n- 2026-08-10 line one\n") {
		t.Fatalf("summary newline leaked into extra bullet:\n%s", got)
	}
}

func TestRenderDeterministic(t *testing.T) {
	recs := []sessions.Record{
		rec("s1", day(2026, 8, 10), sessions.CategoryFeature, "a"),
		rec("s2", day(2026, 8, 11), sessions.CategoryBug, "b"),
		rec("s3", day(2026, 8, 12), sessions.CategoryGeneral, "c"),
	}
	first := Render("2026-W33", recs)
	second := Render("2026-W33", recs)
	if first != second {
		t.Fatalf("Render not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMaybeWriteSkipsCurrentWeek(t *testing.T) {
	root := t.TempDir()
	now := day(2026, 8, 12) // inside 2026-W33
	recs := []sessions.Record{
		rec("s1", day(2026, 8, 11), sessions.CategoryFeature, "this week's work"),
	}
	written, err := MaybeWrite(root, recs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("current week should not be written, got %v", written)
	}
	if _, err := os.Stat(pathFor(root, "2026-W33")); !os.IsNotExist(err) {
		t.Fatalf("expected no digest file for current week")
	}
}

func TestMaybeWriteWritesPastWeek(t *testing.T) {
	root := t.TempDir()
	now := day(2026, 8, 20) // 2026-W34
	recs := []sessions.Record{
		rec("s1", day(2026, 8, 11), sessions.CategoryFeature, "last week's feature"), // 2026-W33
	}
	written, err := MaybeWrite(root, recs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %v", written)
	}
	want := pathFor(root, "2026-W33")
	if written[0] != want {
		t.Fatalf("wrote %q, want %q", written[0], want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("digest file missing: %v", err)
	}
}

func TestMaybeWriteNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	now := day(2026, 8, 20) // 2026-W34
	recs := []sessions.Record{
		rec("s1", day(2026, 8, 11), sessions.CategoryFeature, "last week's feature"),
	}
	written, err := MaybeWrite(root, recs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("first call should write 1 file, got %v", written)
	}
	path := written[0]
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Second call with different summary must NOT overwrite the existing file.
	recs2 := []sessions.Record{
		rec("s1", day(2026, 8, 11), sessions.CategoryFeature, "MUTATED summary"),
	}
	written2, err := MaybeWrite(root, recs2, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(written2) != 0 {
		t.Fatalf("second call should write nothing, got %v", written2)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("existing digest was overwritten:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestMaybeWriteBackfillsMultipleWeeks(t *testing.T) {
	root := t.TempDir()
	now := day(2026, 8, 20) // 2026-W34
	recs := []sessions.Record{
		rec("s1", day(2026, 7, 20), sessions.CategoryFeature, "week 30"),  // 2026-W30
		rec("s2", day(2026, 7, 27), sessions.CategoryBug, "week 31"),      // 2026-W31
		rec("s3", day(2026, 8, 11), sessions.CategoryGeneral, "week 33"),  // 2026-W33
		rec("s4", day(2026, 8, 18), sessions.CategoryFeature, "week 34"),  // 2026-W34 (current, skip)
	}
	written, err := MaybeWrite(root, recs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 3 {
		t.Fatalf("expected 3 past weeks backfilled, got %v", written)
	}
	// Deterministic order: ascending week keys.
	wantOrder := []string{
		pathFor(root, "2026-W30"),
		pathFor(root, "2026-W31"),
		pathFor(root, "2026-W33"),
	}
	for i, w := range wantOrder {
		if written[i] != w {
			t.Fatalf("order[%d] = %q, want %q", i, written[i], w)
		}
	}
	if _, err := os.Stat(pathFor(root, "2026-W34")); !os.IsNotExist(err) {
		t.Fatalf("current week 2026-W34 must not be written")
	}
}

func TestMaybeWriteISOYearBoundary(t *testing.T) {
	root := t.TempDir()
	// 2027-01-01 is a Friday in ISO week 53 of ISO year 2026 -> "2026-W53".
	now := day(2027, 1, 8) // 2027-W01
	recs := []sessions.Record{
		rec("s1", day(2027, 1, 1), sessions.CategoryFeature, "new year eve session"),
	}
	// Sanity-check our expectation against sessions.WeekKey itself.
	if k := sessions.WeekKey(day(2027, 1, 1)); k != "2026-W53" {
		t.Fatalf("precondition: WeekKey(2027-01-01) = %q, want 2026-W53", k)
	}
	written, err := MaybeWrite(root, recs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %v", written)
	}
	want := pathFor(root, "2026-W53")
	if written[0] != want {
		t.Fatalf("wrote %q, want %q (ISO-year boundary)", written[0], want)
	}
	if filepath.Base(written[0]) != "2026-W53.md" {
		t.Fatalf("filename = %q, want 2026-W53.md", filepath.Base(written[0]))
	}
}

func TestMaybeWriteEmptyRecords(t *testing.T) {
	root := t.TempDir()
	now := day(2026, 8, 20)
	written, err := MaybeWrite(root, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("empty records should write nothing, got %v", written)
	}
	// Directory should not even need to exist afterwards, but if created it
	// must contain no digest files.
	entries, _ := os.ReadDir(DigestsDir(root))
	if len(entries) != 0 {
		t.Fatalf("expected no files, got %d", len(entries))
	}
}

// contains is a substring check kept local to avoid pulling strings into the
// test's intent — expresses "the rendered digest includes this line".
func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
