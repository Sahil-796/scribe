package layout

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestRollupOrderingNewestFirst(t *testing.T) {
	files := []SessionFile{
		{Path: "journal/2026-08-19-a.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-19"), SessionID: "a", Summary: "oldest"}, Author: "alice"},
		{Path: "journal/2026-08-21-c.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "c", Summary: "newest"}, Author: "bob"},
		{Path: "journal/2026-08-20-b.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-20"), SessionID: "b", Summary: "middle"}, Author: "carol"},
	}
	got := Rollup(scribe.DocJournal, files)

	iNewest := strings.Index(got, "newest")
	iMiddle := strings.Index(got, "middle")
	iOldest := strings.Index(got, "oldest")
	if !(iNewest < iMiddle && iMiddle < iOldest) {
		t.Fatalf("rows not newest-first:\n%s", got)
	}
	if !strings.HasPrefix(got, "# Journal\n") {
		t.Fatalf("missing journal header:\n%s", got)
	}
}

func TestRollupTieBreakOnSessionID(t *testing.T) {
	date := mustDate(t, "2026-08-21")
	files := []SessionFile{
		{Path: "changelog/2026-08-21-z.md", Meta: SessionMeta{Date: date, SessionID: "zeta", Summary: "z"}},
		{Path: "changelog/2026-08-21-a.md", Meta: SessionMeta{Date: date, SessionID: "alpha", Summary: "a"}},
	}
	got := Rollup(scribe.DocChangelog, files)
	if strings.Index(got, "changelog/2026-08-21-a.md") > strings.Index(got, "changelog/2026-08-21-z.md") {
		t.Fatalf("same-date ties should sort by SessionID ascending:\n%s", got)
	}
}

func TestRollupLineContents(t *testing.T) {
	files := []SessionFile{
		{Path: "journal/2026-08-21-auth-refactor-a1b2c3d4.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "Auth refactor"}, Author: "alice"},
	}
	got := Rollup(scribe.DocJournal, files)
	wantLine := "- 2026-08-21 · [Auth refactor](journal/2026-08-21-auth-refactor-a1b2c3d4.md) · alice"
	if !strings.Contains(got, wantLine) {
		t.Fatalf("missing expected line %q in:\n%s", wantLine, got)
	}
}

func TestRollupEmptyAuthorAndSummary(t *testing.T) {
	files := []SessionFile{
		{Path: "journal/2026-08-21-a1b2c3d4.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: ""}, Author: ""},
	}
	got := Rollup(scribe.DocJournal, files)
	if !strings.Contains(got, noSummary) {
		t.Fatalf("empty summary should render placeholder %q:\n%s", noSummary, got)
	}
	if !strings.Contains(got, "· "+unknownAuthor) {
		t.Fatalf("empty author should render %q:\n%s", unknownAuthor, got)
	}
}

func TestRollupEmptySliceHeaderOnly(t *testing.T) {
	got := Rollup(scribe.DocChangelog, nil)
	if got != "# Changelog\n" {
		t.Fatalf("empty rollup should be header-only, got %q", got)
	}
}

func TestRollupUnknownDocFallbackHeader(t *testing.T) {
	got := Rollup(scribe.DocProject, nil)
	if got != "# History\n" {
		t.Fatalf("unknown doc should fall back to generic header, got %q", got)
	}
}

func TestRollupDoesNotMutateInput(t *testing.T) {
	files := []SessionFile{
		{Path: "j/2026-08-19-a.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-19"), SessionID: "a", Summary: "old"}},
		{Path: "j/2026-08-21-c.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "c", Summary: "new"}},
	}
	before := make([]SessionFile, len(files))
	copy(before, files)
	_ = Rollup(scribe.DocJournal, files)
	if !reflect.DeepEqual(files, before) {
		t.Fatalf("Rollup mutated its input slice:\n got %+v\nwant %+v", files, before)
	}
}

func TestRollupDeterministic(t *testing.T) {
	files := []SessionFile{
		{Path: "j/2026-08-21-c.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "c", Summary: "new"}, Author: "bob"},
		{Path: "j/2026-08-19-a.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-19"), SessionID: "a", Summary: "old"}, Author: "alice"},
	}
	first := Rollup(scribe.DocJournal, files)
	for i := 0; i < 50; i++ {
		if got := Rollup(scribe.DocJournal, files); got != first {
			t.Fatal("Rollup not deterministic")
		}
	}
}

func TestRollupEscapesLinkBrackets(t *testing.T) {
	files := []SessionFile{
		{Path: "j/2026-08-21-a1b2c3d4.md", Meta: SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "fix [urgent] bug"}, Author: "a"},
	}
	got := Rollup(scribe.DocJournal, files)
	if strings.Contains(got, "[urgent]") {
		t.Fatalf("brackets in summary should be escaped to keep link intact:\n%s", got)
	}
}
