package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/sessions"
)

// day builds a fixed UTC timestamp for a calendar day, keeping test records
// terse and deterministic.
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func rec(id string, started time.Time, cat sessions.Category, summary string) sessions.Record {
	return sessions.Record{SessionID: id, Started: started, Updated: started, Summary: summary, Category: cat}
}

func TestRenderEmptyIsHeaderOnly(t *testing.T) {
	got := Render(nil)
	if got != header {
		t.Fatalf("empty render = %q, want header-only %q", got, header)
	}
	// Same for an explicitly empty, non-nil slice.
	if got := Render([]sessions.Record{}); got != header {
		t.Fatalf("empty slice render = %q, want %q", got, header)
	}
}

func TestRenderSingleRecord(t *testing.T) {
	got := Render([]sessions.Record{
		rec("s1", day(2026, 8, 20), sessions.CategoryFeature, "add the index renderer"),
	})
	want := "# Session index\n\n- 2026-08-20 · feat · add the index renderer\n"
	if got != want {
		t.Fatalf("render mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderNewestFirstAndTieBreak(t *testing.T) {
	// Deliberately unsorted input, with two records sharing a Started so the
	// SessionID tie-break is exercised.
	records := []sessions.Record{
		rec("a-old", day(2026, 8, 18), sessions.CategoryGeneral, "oldest"),
		rec("z-same", day(2026, 8, 20), sessions.CategoryBug, "tie z"),
		rec("newest", day(2026, 8, 21), sessions.CategoryFeature, "newest"),
		rec("a-same", day(2026, 8, 20), sessions.CategoryFeature, "tie a"),
	}
	got := Render(records)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	// header + blank + 4 rows
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines, got %d: %q", len(lines), got)
	}
	wantRows := []string{
		"- 2026-08-21 · feat · newest", // newest date first
		"- 2026-08-20 · feat · tie a",  // same date: SessionID asc, "a-same" < "z-same"
		"- 2026-08-20 · bug  · tie z",
		"- 2026-08-18 · gen  · oldest", // oldest last
	}
	for i, w := range wantRows {
		if lines[i+2] != w {
			t.Fatalf("row %d = %q, want %q", i, lines[i+2], w)
		}
	}
}

func TestRenderDoesNotMutateInput(t *testing.T) {
	records := []sessions.Record{
		rec("a", day(2026, 8, 18), sessions.CategoryGeneral, "one"),
		rec("b", day(2026, 8, 20), sessions.CategoryBug, "two"),
	}
	_ = Render(records)
	if records[0].SessionID != "a" || records[1].SessionID != "b" {
		t.Fatalf("Render reordered caller slice: %+v", records)
	}
}

func TestRenderCategoryLabels(t *testing.T) {
	cases := map[sessions.Category]string{
		sessions.CategoryFeature: "feat",
		sessions.CategoryBug:     "bug ",
		sessions.CategoryGeneral: "gen ",
	}
	for cat, tag := range cases {
		got := Render([]sessions.Record{rec("s", day(2026, 8, 20), cat, "x")})
		wantRow := "- 2026-08-20 · " + tag + " · x"
		if !strings.Contains(got, wantRow) {
			t.Fatalf("category %q: got %q, want row %q", cat, got, wantRow)
		}
	}
	// Unknown category falls back without breaking alignment.
	got := Render([]sessions.Record{rec("s", day(2026, 8, 20), sessions.Category("weird"), "x")})
	if !strings.Contains(got, "- 2026-08-20 · "+categoryFallbackTag+" · x") {
		t.Fatalf("unknown category not handled: %q", got)
	}
}

func TestRenderFlattensNewlinesInSummary(t *testing.T) {
	got := Render([]sessions.Record{
		rec("s", day(2026, 8, 20), sessions.CategoryFeature, "line one\nline two\r\nline three\rend"),
	})
	rows := strings.Split(strings.TrimRight(got, "\n"), "\n")
	// header + blank + exactly one data row: the summary must not spawn rows.
	if len(rows) != 3 {
		t.Fatalf("newline summary produced %d lines, want 3: %q", len(rows), got)
	}
	want := "- 2026-08-20 · feat · line one line two line three end"
	if rows[2] != want {
		t.Fatalf("flattened row = %q, want %q", rows[2], want)
	}
}

func TestRenderEmptySummaryPlaceholder(t *testing.T) {
	got := Render([]sessions.Record{rec("s", day(2026, 8, 20), sessions.CategoryBug, "")})
	want := "- 2026-08-20 · bug  · " + noSummaryPlaceholder
	if !strings.Contains(got, want) {
		t.Fatalf("empty summary: got %q, want row %q", got, want)
	}
	// A summary that is only newlines flattens to nothing -> placeholder too.
	got = Render([]sessions.Record{rec("s", day(2026, 8, 20), sessions.CategoryBug, "\n\n")})
	if !strings.Contains(got, want) {
		t.Fatalf("whitespace summary: got %q, want placeholder row %q", got, want)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	records := []sessions.Record{
		rec("z", day(2026, 8, 20), sessions.CategoryFeature, "a"),
		rec("a", day(2026, 8, 20), sessions.CategoryBug, "b"),
		rec("m", day(2026, 8, 19), sessions.CategoryGeneral, "c"),
	}
	first := Render(records)
	second := Render(records)
	if first != second {
		t.Fatalf("Render not deterministic:\n%q\n%q", first, second)
	}
}

func TestWriteRoundTrips(t *testing.T) {
	root := t.TempDir()
	records := []sessions.Record{
		rec("s1", day(2026, 8, 20), sessions.CategoryFeature, "first"),
		rec("s2", day(2026, 8, 21), sessions.CategoryBug, "second"),
	}
	if err := Write(root, records); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, scribe.DocsDir, "INDEX.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != Render(records) {
		t.Fatalf("on-disk content != Render output:\n disk: %q\nrender: %q", b, Render(records))
	}
	// Path() must agree with where Write put the file.
	if Path(root) != filepath.Join(root, scribe.DocsDir, "INDEX.md") {
		t.Fatalf("Path mismatch: %s", Path(root))
	}
}

func TestWriteCreatesDocsDir(t *testing.T) {
	root := t.TempDir()
	// docs/scribe/ does not exist yet.
	if _, err := os.Stat(filepath.Join(root, scribe.DocsDir)); !os.IsNotExist(err) {
		t.Fatalf("precondition: docs dir should be absent, err=%v", err)
	}
	if err := Write(root, nil); err != nil {
		t.Fatalf("Write on empty slice: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, scribe.DocsDir, "INDEX.md"))
	if err != nil {
		t.Fatalf("ReadFile after Write: %v", err)
	}
	if string(b) != header {
		t.Fatalf("empty Write content = %q, want header-only %q", b, header)
	}
}
