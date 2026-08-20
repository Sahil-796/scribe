package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// mustOpen opens a Store rooted at a fresh temp dir.
func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestOpenDoesNotCreateFile(t *testing.T) {
	s := mustOpen(t)
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("Open created sessions.json (or unexpected stat error): %v", err)
	}
}

func TestAllMissingFileEmpty(t *testing.T) {
	s := mustOpen(t)
	got, err := s.All()
	if err != nil {
		t.Fatalf("All on missing file: %v", err)
	}
	if got == nil {
		t.Fatal("All returned nil slice, want empty non-nil")
	}
	if len(got) != 0 {
		t.Fatalf("All on missing file returned %d records, want 0", len(got))
	}
}

func TestInsertThenReadBack(t *testing.T) {
	s := mustOpen(t)
	r := Record{
		SessionID: "abc",
		Started:   ts("2026-08-20T10:00:00Z"),
		Updated:   ts("2026-08-20T10:05:00Z"),
		Summary:   "add the sessions store",
		Category:  CategoryFeature,
	}
	if err := s.Upsert(r); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	if got[0] != r {
		t.Fatalf("read back %+v, want %+v", got[0], r)
	}

	// A second Store on the same repo must see the same data (file is the
	// source of truth, no in-memory cache).
	s2, err := Open(s.repoRoot)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got2, err := s2.All()
	if err != nil {
		t.Fatalf("All after reopen: %v", err)
	}
	if len(got2) != 1 || got2[0] != r {
		t.Fatalf("reopened store read %+v, want %+v", got2, r)
	}
}

func TestUpsertPreservesStartedUpdatesRest(t *testing.T) {
	s := mustOpen(t)
	first := Record{
		SessionID: "sess-1",
		Started:   ts("2026-08-20T09:00:00Z"),
		Updated:   ts("2026-08-20T09:00:00Z"),
		Summary:   "first pass",
		Category:  CategoryGeneral,
	}
	if err := s.Upsert(first); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}

	// A later run for the same session carries a later Started, but the store
	// must keep the original.
	second := Record{
		SessionID: "sess-1",
		Started:   ts("2026-08-20T11:30:00Z"), // must be ignored
		Updated:   ts("2026-08-20T11:30:00Z"),
		Summary:   "turned into a bug fix",
		Category:  CategoryBug,
	}
	if err := s.Upsert(second); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	got, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1 (upsert must not insert a duplicate)", len(got))
	}
	want := Record{
		SessionID: "sess-1",
		Started:   first.Started, // preserved
		Updated:   second.Updated,
		Summary:   second.Summary,
		Category:  second.Category,
	}
	if got[0] != want {
		t.Fatalf("after upsert got %+v, want %+v", got[0], want)
	}
}

func TestAllSortOrder(t *testing.T) {
	s := mustOpen(t)
	// Inserted deliberately out of order, with a tied Started time between
	// "b-tie" and "a-tie" so the SessionID tiebreaker is exercised.
	recs := []Record{
		{SessionID: "late", Started: ts("2026-08-22T00:00:00Z"), Category: CategoryGeneral},
		{SessionID: "b-tie", Started: ts("2026-08-20T00:00:00Z"), Category: CategoryGeneral},
		{SessionID: "early", Started: ts("2026-08-19T00:00:00Z"), Category: CategoryGeneral},
		{SessionID: "a-tie", Started: ts("2026-08-20T00:00:00Z"), Category: CategoryGeneral},
	}
	for _, r := range recs {
		if err := s.Upsert(r); err != nil {
			t.Fatalf("Upsert %s: %v", r.SessionID, err)
		}
	}

	got, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	wantIDs := []string{"early", "a-tie", "b-tie", "late"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d records, want %d", len(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		if got[i].SessionID != id {
			t.Fatalf("position %d: got %q, want %q (full order %v)", i, got[i].SessionID, id, ids(got))
		}
	}
}

func TestUpsertAtomicLeavesValidFile(t *testing.T) {
	s := mustOpen(t)
	if err := s.Upsert(Record{SessionID: "x", Started: ts("2026-08-20T00:00:00Z"), Category: CategoryFeature}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// File decodes as the documented wrapper shape, and no temp files were
	// left behind in .scribe/.
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("file does not decode as wrapper object: %v", err)
	}
	if len(f.Sessions) != 1 || f.Sessions[0].SessionID != "x" {
		t.Fatalf("decoded %+v, want one record with SessionID x", f.Sessions)
	}

	entries, err := os.ReadDir(filepath.Join(s.repoRoot, scribe.StateDir))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != sessionsFileName {
			t.Fatalf("unexpected leftover in .scribe/: %q", e.Name())
		}
	}
}

func TestCorruptFileIsLoudError(t *testing.T) {
	s := mustOpen(t)
	if err := os.WriteFile(s.Path(), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	if _, err := s.All(); err == nil {
		t.Fatal("All on corrupt file returned nil error, want loud error")
	}
	// Upsert must also refuse rather than overwrite corrupt existing state.
	if err := s.Upsert(Record{SessionID: "y", Started: ts("2026-08-20T00:00:00Z")}); err == nil {
		t.Fatal("Upsert onto corrupt file returned nil error, want loud error")
	}
	// And the corrupt file must be untouched (not silently reset).
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "{not valid json" {
		t.Fatalf("corrupt file was modified: %q", string(data))
	}
}

func TestSummaryStoredVerbatim(t *testing.T) {
	// The one-line contract is documented but not enforced here — a Summary
	// with a newline is stored exactly as given, not mangled.
	s := mustOpen(t)
	r := Record{SessionID: "z", Started: ts("2026-08-20T00:00:00Z"), Summary: "line one\nline two", Category: CategoryGeneral}
	if err := s.Upsert(r); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if got[0].Summary != "line one\nline two" {
		t.Fatalf("Summary mangled: %q", got[0].Summary)
	}
}

func ids(recs []Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.SessionID
	}
	return out
}
