package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// runDiffIn executes `scribe diff` against dir, capturing stdout. Mirrors
// runStatusIn in status_test.go.
func runDiffIn(t *testing.T, dir string) string {
	t.Helper()
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runDiff(&out); err != nil {
		t.Fatalf("scribe diff: %v", err)
	}
	return out.String()
}

func TestDiff_OutsideGitRepo_NoErrorJustSaysSo(t *testing.T) {
	dir := t.TempDir()

	out := runDiffIn(t, dir)
	if !strings.Contains(out, "Not a git repository") {
		t.Fatalf("expected a plain statement, got %q", out)
	}
}

func TestDiff_NeverInitialised_NoRunRecorded(t *testing.T) {
	dir := newTestRepo(t)

	out := runDiffIn(t, dir)
	if !strings.Contains(out, "No run has been recorded yet") {
		t.Fatalf("expected the never-run message, got %q", out)
	}
}

func TestDiff_PausedRepoWithNoRunReadsSameAsUninitialised(t *testing.T) {
	dir := newTestRepo(t)
	until := time.Now().Add(time.Hour)
	if err := install.WriteConfig(dir, install.Config{
		Agent:   "opencode",
		Enabled: true,
		Pause:   &install.Pause{Since: time.Now(), Until: &until},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	out := runDiffIn(t, dir)
	if !strings.Contains(out, "No run has been recorded yet") {
		t.Fatalf("a paused repo that never ran should read the same as never-initialised for diff, got %q", out)
	}
}

func TestDiff_RunRecordedButChangedNothing(t *testing.T) {
	dir := newTestRepo(t)
	store, err := docs.Open(dir)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	if err := store.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}

	out := runDiffIn(t, dir)
	if !strings.Contains(out, "touched nothing") {
		t.Fatalf("expected a 'touched nothing' message, got %q", out)
	}
	if strings.Contains(out, "No run has been recorded") {
		t.Fatalf("a recorded no-op run must read differently from no run at all, got %q", out)
	}
}

func TestDiff_RendersUnifiedDiffForChangedDoc(t *testing.T) {
	dir := newTestRepo(t)
	store, err := docs.Open(dir)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	if err := store.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := store.WriteState(scribe.DocProject, "# Project\n\nA real thing now exists.\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	out := runDiffIn(t, dir)
	if !strings.Contains(out, "--- PROJECT.md (before)") {
		t.Fatalf("expected a unified diff header for PROJECT.md, got %q", out)
	}
	if !strings.Contains(out, "-_Nothing recorded yet._") {
		t.Fatalf("expected the removed default-header line, got %q", out)
	}
	if !strings.Contains(out, "+A real thing now exists.") {
		t.Fatalf("expected the added line, got %q", out)
	}
}

func TestDiff_OnlyListsDocsThatActuallyChanged(t *testing.T) {
	dir := newTestRepo(t)
	store, err := docs.Open(dir)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	if err := store.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := store.WriteState(scribe.DocProject, "# Project\n\nchanged\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	// DECISIONS.md untouched this run.

	out := runDiffIn(t, dir)
	if strings.Contains(out, "DECISIONS.md") {
		t.Fatalf("an untouched doc should not appear in the diff output, got %q", out)
	}
}

func TestDiff_RotationNoteAppearsForArchivedHistoryDoc(t *testing.T) {
	dir := newTestRepo(t)
	store, err := docs.Open(dir)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	store.SetCaps(0, 400) // force rotation quickly

	// Prime the doc with a few entries before the tracked run starts, so
	// the run itself is the one that pushes it over cap and rotates.
	for i := 0; i < 3; i++ {
		if err := store.AppendHistory(scribe.DocChangelog, strings.Repeat("x", 120)); err != nil {
			t.Fatalf("priming AppendHistory: %v", err)
		}
	}

	if err := store.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := store.AppendHistory(scribe.DocChangelog, strings.Repeat("y", 120)); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}

	out := runDiffIn(t, dir)
	if !strings.Contains(out, "Note: this run archived") {
		t.Fatalf("expected a rotation note ahead of the diff, got %q", out)
	}
	if !strings.Contains(out, "moved, not deleted") {
		t.Fatalf("expected the rotation note to say entries were moved, not deleted, got %q", out)
	}
}
