package docs

import (
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestLastRunAbsentBeforeAnyRun(t *testing.T) {
	s := mustOpen(t)

	if _, ok, err := s.LastRun(); err != nil || ok {
		t.Fatalf("LastRun on a fresh store: ok=%v err=%v, want ok=false, err=nil", ok, err)
	}
}

func TestWritesUntrackedWithoutResetRun(t *testing.T) {
	s := mustOpen(t)

	if err := s.WriteState(scribe.DocProject, "# Project\n\nhello\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	if _, ok, err := s.LastRun(); err != nil || ok {
		t.Fatalf("LastRun after an untracked write: ok=%v err=%v, want ok=false, err=nil", ok, err)
	}
}

func TestResetRunThenWriteStateRecordsBeforeAfter(t *testing.T) {
	s := mustOpen(t)

	if err := s.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := s.WriteState(scribe.DocProject, "# Project\n\nnew content\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	snap, ok, err := s.LastRun()
	if err != nil || !ok {
		t.Fatalf("LastRun: ok=%v err=%v", ok, err)
	}
	change, seen := snap.Docs[scribe.DocProject]
	if !seen {
		t.Fatalf("PROJECT.md missing from run snapshot: %+v", snap.Docs)
	}
	if change.Before != "# Project\n\n_Nothing recorded yet._\n" {
		t.Fatalf("unexpected Before: %q", change.Before)
	}
	if change.After != "# Project\n\nnew content\n" {
		t.Fatalf("unexpected After: %q", change.After)
	}
	if _, seen := snap.Docs[scribe.DocDecisions]; seen {
		t.Fatalf("DECISIONS.md should not be in the snapshot: it was never written")
	}
}

func TestResetRunClearsPreviousSnapshot(t *testing.T) {
	s := mustOpen(t)

	if err := s.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := s.WriteState(scribe.DocProject, "# Project\n\nfirst run\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	if err := s.ResetRun(); err != nil {
		t.Fatalf("ResetRun (second): %v", err)
	}
	snap, ok, err := s.LastRun()
	if err != nil || !ok {
		t.Fatalf("LastRun: ok=%v err=%v", ok, err)
	}
	if len(snap.Docs) != 0 {
		t.Fatalf("expected an empty snapshot right after ResetRun, got %+v", snap.Docs)
	}
}

func TestSecondWriteInSameRunKeepsFirstBefore(t *testing.T) {
	s := mustOpen(t)

	if err := s.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := s.WriteState(scribe.DocProject, "# Project\n\nfirst\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := s.WriteState(scribe.DocProject, "# Project\n\nsecond\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	snap, _, err := s.LastRun()
	if err != nil {
		t.Fatalf("LastRun: %v", err)
	}
	change := snap.Docs[scribe.DocProject]
	if change.Before != "# Project\n\n_Nothing recorded yet._\n" {
		t.Fatalf("Before should stay the pre-run content across two writes, got %q", change.Before)
	}
	if change.After != "# Project\n\nsecond\n" {
		t.Fatalf("After should be the latest write, got %q", change.After)
	}
}

func TestAppendHistoryRecordsChange(t *testing.T) {
	s := mustOpen(t)

	if err := s.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := s.AppendHistory(scribe.DocChangelog, "2026-08-10: did a thing"); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}

	snap, ok, err := s.LastRun()
	if err != nil || !ok {
		t.Fatalf("LastRun: ok=%v err=%v", ok, err)
	}
	change, seen := snap.Docs[scribe.DocChangelog]
	if !seen {
		t.Fatalf("CHANGELOG.md missing from run snapshot")
	}
	if change.Before != "# Changelog\n" {
		t.Fatalf("unexpected Before: %q", change.Before)
	}
	if change.After == change.Before {
		t.Fatalf("After should reflect the appended entry, got identical to Before")
	}
}

func TestWriteStateNoOpChangeStillRecorded(t *testing.T) {
	s := mustOpen(t)

	if err := s.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	// Seed the doc first (outside the tracked run's before/after — this
	// happens via Read, not WriteState, so it isn't itself tracked) so the
	// WriteState below is a genuine no-op rewrite.
	if _, err := s.Read(scribe.DocProject); err != nil {
		t.Fatalf("Read: %v", err)
	}
	seeded, err := s.Read(scribe.DocProject)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := s.WriteState(scribe.DocProject, seeded); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	snap, _, err := s.LastRun()
	if err != nil {
		t.Fatalf("LastRun: %v", err)
	}
	change, seen := snap.Docs[scribe.DocProject]
	if !seen {
		t.Fatalf("a no-op rewrite should still be recorded as a touched doc")
	}
	if change.Before != change.After {
		t.Fatalf("expected Before == After for a no-op rewrite, got %q vs %q", change.Before, change.After)
	}
}
