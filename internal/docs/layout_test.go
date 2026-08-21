package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/attribution"
	"github.com/Sahil-796/scribe/internal/layout"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// aug21 is a fixed session date so file names and frontmatter are byte-stable
// across runs (per-session paths embed the calendar day).
var aug21 = time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)

func mustOpenLayout(t *testing.T, mode layout.Mode) *Store {
	t.Helper()
	s, err := OpenWithLayout(t.TempDir(), mode)
	if err != nil {
		t.Fatalf("OpenWithLayout: %v", err)
	}
	return s
}

// TestWriteHistorySharedStampsBylineAndAppends proves the shared path is
// AppendHistory with a byline: the entry lands in the single CHANGELOG.md with
// the author credited on its own trailing line, and a state doc is rejected.
func TestWriteHistorySharedStampsBylineAndAppends(t *testing.T) {
	s := mustOpenLayout(t, layout.Shared)
	author := attribution.Author{Name: "Ada Lovelace"}
	meta := layout.SessionMeta{Date: aug21, SessionID: "sess-1"}

	if err := s.WriteHistory(scribe.DocChangelog, meta, author, "- added a widget"); err != nil {
		t.Fatalf("WriteHistory: %v", err)
	}

	got := mustRead(t, s, scribe.DocChangelog)
	if !strings.Contains(got, "- added a widget") {
		t.Fatalf("entry missing from shared doc:\n%s", got)
	}
	if !strings.Contains(got, "— Ada Lovelace") {
		t.Fatalf("byline missing from shared doc:\n%s", got)
	}
	// Shared mode must not create the per-session subdir.
	if _, err := os.Stat(filepath.Join(s.dir, "changelog")); !os.IsNotExist(err) {
		t.Fatalf("shared mode should not create changelog/ subdir (err=%v)", err)
	}
}

// TestWriteHistorySharedBylineIsIdempotent proves re-writing the same already
// bylined entry (the reprocessed-transcript case) doesn't stack a second
// credit — StampShared's idempotency, exercised through the store.
func TestWriteHistorySharedBylineIsIdempotent(t *testing.T) {
	s := mustOpenLayout(t, layout.Shared)
	author := attribution.Author{Name: "Ada"}
	meta := layout.SessionMeta{Date: aug21, SessionID: "sess-1"}

	stamped := attribution.StampShared("- did a thing", author)
	if err := s.WriteHistory(scribe.DocChangelog, meta, author, stamped); err != nil {
		t.Fatalf("WriteHistory: %v", err)
	}
	got := mustRead(t, s, scribe.DocChangelog)
	if n := strings.Count(got, "— Ada"); n != 1 {
		t.Fatalf("expected exactly one byline, got %d:\n%s", n, got)
	}
}

// TestWriteHistoryPerSessionWritesFileAndRollup is the core of the per-session
// layout: an entry becomes its own file under changelog/ (named uniquely by
// date + slug + session id, with self-describing frontmatter) and the
// top-level CHANGELOG.md is regenerated as a rollup that links to it.
func TestWriteHistoryPerSessionWritesFileAndRollup(t *testing.T) {
	s := mustOpenLayout(t, layout.PerSession)
	author := attribution.Author{Name: "Grace Hopper"}
	meta := layout.SessionMeta{Date: aug21, SessionID: "abcd1234ef", Summary: "auth refactor"}

	if err := s.WriteHistory(scribe.DocChangelog, meta, author, "- reworked the login flow"); err != nil {
		t.Fatalf("WriteHistory: %v", err)
	}

	// The session file exists at the path internal/layout dictates, with the
	// entry and a frontmatter author line.
	relPath, err := layout.SessionFilePath(scribe.DocChangelog, meta)
	if err != nil {
		t.Fatalf("SessionFilePath: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(s.dir, relPath))
	if err != nil {
		t.Fatalf("read session file %s: %v", relPath, err)
	}
	if !strings.Contains(string(body), "- reworked the login flow") {
		t.Fatalf("entry missing from session file:\n%s", body)
	}
	if !strings.Contains(string(body), "author: Grace Hopper") {
		t.Fatalf("author frontmatter missing from session file:\n%s", body)
	}

	// The top-level CHANGELOG.md is the rollup — a header plus a link to the
	// session file, not the raw entry appended.
	rollup := mustRead(t, s, scribe.DocChangelog)
	if !strings.HasPrefix(rollup, "# Changelog") {
		t.Fatalf("rollup missing header:\n%s", rollup)
	}
	if !strings.Contains(rollup, "("+relPath+")") {
		t.Fatalf("rollup missing link to %s:\n%s", relPath, rollup)
	}
	if !strings.Contains(rollup, "Grace Hopper") {
		t.Fatalf("rollup missing author:\n%s", rollup)
	}
}

// TestWriteHistoryPerSessionRollupCoversAllSessions proves the rollup is
// regenerated over every session file, newest-first, not just the last one
// written — two teammates on two days both appear, and each writes a distinct,
// non-conflicting path.
func TestWriteHistoryPerSessionRollupCoversAllSessions(t *testing.T) {
	s := mustOpenLayout(t, layout.PerSession)

	day1 := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	metaOld := layout.SessionMeta{Date: day1, SessionID: "session-old-1", Summary: "old work"}
	metaNew := layout.SessionMeta{Date: day2, SessionID: "session-new-2", Summary: "new work"}

	if err := s.WriteHistory(scribe.DocJournal, metaOld, attribution.Author{Name: "Alice"}, "- old journal note"); err != nil {
		t.Fatalf("WriteHistory old: %v", err)
	}
	if err := s.WriteHistory(scribe.DocJournal, metaNew, attribution.Author{Name: "Bob"}, "- new journal note"); err != nil {
		t.Fatalf("WriteHistory new: %v", err)
	}

	pathOld, _ := layout.SessionFilePath(scribe.DocJournal, metaOld)
	pathNew, _ := layout.SessionFilePath(scribe.DocJournal, metaNew)
	if pathOld == pathNew {
		t.Fatalf("two sessions resolved to the same path %q — per-session uniqueness broken", pathOld)
	}

	rollup := mustRead(t, s, scribe.DocJournal)
	iNew := strings.Index(rollup, pathNew)
	iOld := strings.Index(rollup, pathOld)
	if iNew < 0 || iOld < 0 {
		t.Fatalf("rollup missing one of the sessions:\n%s", rollup)
	}
	// Newest-first: the day-2 file's line precedes the day-1 file's line.
	if iNew > iOld {
		t.Fatalf("rollup not newest-first (new at %d, old at %d):\n%s", iNew, iOld, rollup)
	}

	// Both session files still exist independently — nothing was overwritten.
	for _, p := range []string{pathOld, pathNew} {
		if _, err := os.Stat(filepath.Join(s.dir, p)); err != nil {
			t.Fatalf("session file %s missing: %v", p, err)
		}
	}
}

// TestWriteHistoryPerSessionRecordsDiffSnapshot proves the rollup regeneration
// still feeds `scribe diff`: after ResetRun, the top-level doc's before/after
// is captured for the run even though the actual entry went to a session file.
func TestWriteHistoryPerSessionRecordsDiffSnapshot(t *testing.T) {
	s := mustOpenLayout(t, layout.PerSession)
	if err := s.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	meta := layout.SessionMeta{Date: aug21, SessionID: "sess-diff-1", Summary: "did work"}
	if err := s.WriteHistory(scribe.DocChangelog, meta, attribution.Author{Name: "Ada"}, "- a change"); err != nil {
		t.Fatalf("WriteHistory: %v", err)
	}

	snap, ok, err := s.LastRun()
	if err != nil {
		t.Fatalf("LastRun: %v", err)
	}
	if !ok {
		t.Fatalf("LastRun reported no snapshot after a per-session write")
	}
	change, ok := snap.Docs[scribe.DocChangelog]
	if !ok {
		t.Fatalf("no diff snapshot recorded for CHANGELOG after per-session write")
	}
	if change.Before == change.After {
		t.Fatalf("expected the rollup to change the top-level doc, before==after:\n%q", change.After)
	}
}

// TestWriteHistoryRejectsStateDoc guards the one caller mistake worth
// surfacing: a state doc has no per-session or append meaning here.
func TestWriteHistoryRejectsStateDoc(t *testing.T) {
	s := mustOpenLayout(t, layout.PerSession)
	err := s.WriteHistory(scribe.DocProject, layout.SessionMeta{SessionID: "x"}, attribution.Author{}, "content")
	if err == nil {
		t.Fatalf("expected WriteHistory on a state doc to error")
	}
}

// TestParseSessionFrontmatterRoundTrip proves the internal parser reads back
// exactly what layout.RenderSessionFile wrote — the contract the rollup
// regeneration relies on.
func TestParseSessionFrontmatterRoundTrip(t *testing.T) {
	meta := layout.SessionMeta{Date: aug21, SessionID: "sid-123", Summary: "a readable summary"}
	body := layout.RenderSessionFile(meta, "Ada Lovelace <ada@example.com>", "- the entry body")

	gotMeta, gotAuthor := parseSessionFrontmatter(body)
	if gotMeta.SessionID != meta.SessionID {
		t.Errorf("SessionID = %q, want %q", gotMeta.SessionID, meta.SessionID)
	}
	if gotMeta.Summary != meta.Summary {
		t.Errorf("Summary = %q, want %q", gotMeta.Summary, meta.Summary)
	}
	// Frontmatter records only the calendar day (YYYY-MM-DD), so the parsed
	// time is that day at midnight UTC, not the original clock time.
	wantDay := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	if !gotMeta.Date.Equal(wantDay) {
		t.Errorf("Date = %v, want %v", gotMeta.Date, wantDay)
	}
	if gotAuthor != "Ada Lovelace <ada@example.com>" {
		t.Errorf("author = %q, want %q", gotAuthor, "Ada Lovelace <ada@example.com>")
	}
}
