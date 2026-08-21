package layout

import (
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return d
}

func TestSessionFilePath(t *testing.T) {
	date := mustDate(t, "2026-08-21")
	tests := []struct {
		name string
		doc  scribe.Doc
		meta SessionMeta
		want string
	}{
		{
			name: "journal with slug",
			doc:  scribe.DocJournal,
			meta: SessionMeta{Date: date, SessionID: "a1b2c3d4e5f6", Summary: "Auth refactor"},
			want: "journal/2026-08-21-auth-refactor-a1b2c3d4.md",
		},
		{
			name: "changelog with slug",
			doc:  scribe.DocChangelog,
			meta: SessionMeta{Date: date, SessionID: "a1b2c3d4e5f6", Summary: "Auth refactor"},
			want: "changelog/2026-08-21-auth-refactor-a1b2c3d4.md",
		},
		{
			name: "no usable slug omits it",
			doc:  scribe.DocChangelog,
			meta: SessionMeta{Date: date, SessionID: "a1b2c3d4e5f6", Summary: "🚀🔥"},
			want: "changelog/2026-08-21-a1b2c3d4.md",
		},
		{
			name: "hyphenated uuid sanitized to first 8 alnum",
			doc:  scribe.DocJournal,
			meta: SessionMeta{Date: date, SessionID: "AB12-CD34-EF56", Summary: "x"},
			want: "journal/2026-08-21-x-ab12cd34.md",
		},
		{
			name: "short session id used whole",
			doc:  scribe.DocJournal,
			meta: SessionMeta{Date: date, SessionID: "ab12", Summary: ""},
			want: "journal/2026-08-21-ab12.md",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SessionFilePath(tc.doc, tc.meta)
			if err != nil {
				t.Fatalf("SessionFilePath unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("SessionFilePath = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionFilePathRejectsStateDocs(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "x"}
	for _, doc := range []scribe.Doc{scribe.DocProject, scribe.DocDecisions} {
		if _, err := SessionFilePath(doc, m); err == nil {
			t.Errorf("SessionFilePath(%s) = nil error, want error (state docs do not split)", doc)
		}
	}
}

func TestSessionFilePathErrorsOnEmptySessionID(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "----", Summary: "x"}
	if _, err := SessionFilePath(scribe.DocJournal, m); err == nil {
		t.Fatal("SessionFilePath with all-garbage session id = nil error, want error")
	}
}

// TestSessionFilePathSuffixIsCollisionFree is the core guarantee of the
// per-session layout: two sessions that share the same date and summary — the
// exact case that would conflict in shared mode — still resolve to distinct
// paths because of the session-id suffix.
func TestSessionFilePathSuffixIsCollisionFree(t *testing.T) {
	date := mustDate(t, "2026-08-21")
	summary := "Auth refactor"
	a, err := SessionFilePath(scribe.DocJournal, SessionMeta{Date: date, SessionID: "aaaa1111zzzz", Summary: summary})
	if err != nil {
		t.Fatal(err)
	}
	b, err := SessionFilePath(scribe.DocJournal, SessionMeta{Date: date, SessionID: "bbbb2222zzzz", Summary: summary})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("distinct sessions produced the same path %q", a)
	}
	if !strings.HasSuffix(a, "-aaaa1111.md") || !strings.HasSuffix(b, "-bbbb2222.md") {
		t.Fatalf("paths missing expected sid suffix: %q, %q", a, b)
	}
}

func TestSessionFilePathDeterministic(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4e5", Summary: "Auth refactor"}
	first, err := SessionFilePath(scribe.DocJournal, m)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := SessionFilePath(scribe.DocJournal, m)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("SessionFilePath not deterministic: %q vs %q", got, first)
		}
	}
}

func TestRenderSessionFile(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "Auth refactor"}
	got := RenderSessionFile(m, "alice", "Did the thing.\n")
	want := "---\n" +
		"date: 2026-08-21\n" +
		"session: a1b2c3d4\n" +
		"author: alice\n" +
		"summary: Auth refactor\n" +
		"---\n" +
		"\n" +
		"Did the thing.\n"
	if got != want {
		t.Fatalf("RenderSessionFile mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestRenderSessionFileEmptyAuthor(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "x"}
	got := RenderSessionFile(m, "   ", "body")
	if !strings.Contains(got, "author: "+unknownAuthor+"\n") {
		t.Fatalf("empty author should render as %q, got:\n%s", unknownAuthor, got)
	}
}

func TestRenderSessionFileExactlyOneTrailingNewline(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "x"}
	for _, entry := range []string{"body", "body\n", "body\n\n\n", ""} {
		got := RenderSessionFile(m, "a", entry)
		if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
			t.Errorf("entry %q: want exactly one trailing newline, got tail %q", entry, got[len(got)-3:])
		}
	}
}

func TestRenderSessionFileFlattensSummary(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "line one\nline two"}
	got := RenderSessionFile(m, "a", "body")
	if !strings.Contains(got, "summary: line one line two\n") {
		t.Fatalf("summary should be flattened to one line, got:\n%s", got)
	}
	// The frontmatter block must remain exactly the four keys + delimiters
	// (a stray newline in summary must not add a line).
	if n := strings.Count(got, "\n---\n"); n != 1 {
		t.Fatalf("expected one closing delimiter, got %d in:\n%s", n, got)
	}
}

func TestRenderSessionFileDeterministic(t *testing.T) {
	m := SessionMeta{Date: mustDate(t, "2026-08-21"), SessionID: "a1b2c3d4", Summary: "Auth refactor"}
	first := RenderSessionFile(m, "alice", "body")
	for i := 0; i < 50; i++ {
		if got := RenderSessionFile(m, "alice", "body"); got != first {
			t.Fatal("RenderSessionFile not deterministic")
		}
	}
}
