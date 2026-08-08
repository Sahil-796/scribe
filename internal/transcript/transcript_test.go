package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

func TestReadBasic_DropsToolResultsAndKeepsRealTurns(t *testing.T) {
	path := readTestdata(t, "basic.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	entries, newOffset, err := Read(path, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if newOffset != info.Size() {
		t.Errorf("newOffset = %d, want %d (full file consumed)", newOffset, info.Size())
	}

	want := []scribe.Entry{
		{Role: "user", Text: "hello, can you help me plan this feature?"},
		{Role: "assistant", Text: "Sure, here is the plan."},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, e := range entries {
		if e.Role != want[i].Role || e.Text != want[i].Text {
			t.Errorf("entry %d = %+v, want %+v", i, e, want[i])
		}
		if e.Timestamp.IsZero() {
			t.Errorf("entry %d has zero timestamp", i)
		}
	}
}

func TestReadIncremental_ResumesFromOffset(t *testing.T) {
	path := readTestdata(t, "basic.jsonl")

	first, off1, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("first read: got %d entries, want 2", len(first))
	}

	// Re-reading from the returned offset must yield nothing new and the
	// same offset back: decision 4, and "a re-run must not duplicate
	// content".
	second, off2, err := Read(path, off1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("second read from end offset: got %d entries, want 0: %+v", len(second), second)
	}
	if off2 != off1 {
		t.Errorf("offset drifted on a no-op read: %d != %d", off2, off1)
	}
}

func TestReadIncremental_AppendYieldsOnlyNewEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	line1 := `{"parentUuid":null,"isSidechain":false,"type":"user","timestamp":"2026-08-08T05:07:09.000Z","message":{"role":"user","content":"turn one"}}` + "\n"
	if err := os.WriteFile(path, []byte(line1), 0o644); err != nil {
		t.Fatal(err)
	}

	entries1, off1, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries1) != 1 || entries1[0].Text != "turn one" {
		t.Fatalf("unexpected first read: %+v", entries1)
	}

	// Simulate the transcript being appended to live, as another process
	// (Claude Code itself) would.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	line2 := `{"parentUuid":"a","isSidechain":false,"type":"assistant","timestamp":"2026-08-08T05:07:11.000Z","message":{"model":"claude-opus-5","role":"assistant","content":[{"type":"text","text":"turn two"}]}}` + "\n"
	if _, err := f.WriteString(line2); err != nil {
		t.Fatal(err)
	}
	f.Close()

	entries2, off2, err := Read(path, off1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries2) != 1 || entries2[0].Text != "turn two" {
		t.Fatalf("unexpected second read: %+v", entries2)
	}
	if off2 <= off1 {
		t.Errorf("offset did not advance: off1=%d off2=%d", off1, off2)
	}
}

func TestReadDropsSidechainTurns(t *testing.T) {
	path := readTestdata(t, "sidechain.jsonl")
	entries, _, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (sidechain turns dropped): %+v", len(entries), entries)
	}
	if entries[0].Text != "please investigate the failing test" {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[1].Text != "The subagent found the issue: Foo is unused." {
		t.Errorf("entries[1] = %+v", entries[1])
	}
}

func TestReadDropsSyntheticPlaceholders(t *testing.T) {
	path := readTestdata(t, "synthetic.jsonl")
	entries, _, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (synthetic placeholders dropped): %+v", len(entries), entries)
	}
	if entries[0].Text != "do the thing" {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[1].Text != "Done, the thing is complete." {
		t.Errorf("entries[1] = %+v", entries[1])
	}
}

// TestReadPartialLine is the most important test in this package: the
// transcript is being appended to live, and a read must never land on a
// half-written line and corrupt the offset or drop a turn.
func TestReadPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	complete := `{"parentUuid":null,"isSidechain":false,"type":"user","timestamp":"2026-08-08T05:07:09.000Z","message":{"role":"user","content":"complete turn"}}` + "\n"
	partial := `{"parentUuid":"a","isSidechain":false,"type":"assistant","timestamp":"2026-08-08T05:07:11.000Z","message":{"model":"claude-opus-5","role":"assistant","content":[{"type":"text","text":"this line got cut off mid-w`
	// deliberately no trailing newline: simulates a writer that has
	// flushed the first turn and is mid-write on the second.

	if err := os.WriteFile(path, []byte(complete+partial), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, offset, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "complete turn" {
		t.Fatalf("got %+v, want only the complete turn", entries)
	}
	if int(offset) != len(complete) {
		t.Fatalf("offset = %d, want %d (must stop before the partial line, not consume any of it)", offset, len(complete))
	}

	// Now the writer finishes the line and starts a third.
	rest := `hich to write, but now finishes here."}]}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(rest); err != nil {
		t.Fatal(err)
	}
	f.Close()

	entries2, offset2, err := Read(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries2) != 1 {
		t.Fatalf("got %d entries after completing the line, want 1: %+v", len(entries2), entries2)
	}
	wantText := "this line got cut off mid-which to write, but now finishes here."
	if entries2[0].Text != wantText {
		t.Fatalf("entries2[0].Text = %q, want %q", entries2[0].Text, wantText)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if offset2 != info.Size() {
		t.Errorf("offset2 = %d, want %d (full file now consumed)", offset2, info.Size())
	}
}

// TestReadPartialLine_NoTrailingNewlineAtAll covers the degenerate case
// where the entire file so far is one unterminated line (e.g. the very
// first write of a brand-new session transcript, read at just the wrong
// moment).
func TestReadPartialLine_NoTrailingNewlineAtAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	partial := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"still typin`
	if err := os.WriteFile(path, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, offset, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
	if offset != 0 {
		t.Fatalf("offset = %d, want 0 (nothing safe to consume yet)", offset)
	}
}

// TestReadTruncation covers the file being rotated/truncated out from
// under a stale offset: offset > file size must reset, not error or
// silently return garbage.
func TestReadTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	line := `{"parentUuid":null,"isSidechain":false,"type":"user","timestamp":"2026-08-08T05:07:09.000Z","message":{"role":"user","content":"turn one"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	_, off1, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Rotate: truncate and write a brand-new, shorter transcript.
	newLine := `{"type":"user","message":{"role":"user","content":"new"}}` + "\n"
	if err := os.WriteFile(path, []byte(newLine), 0o644); err != nil {
		t.Fatal(err)
	}

	if int64(len(newLine)) >= off1 {
		t.Fatalf("test setup broken: new file (%d bytes) must be smaller than the stale offset (%d)", len(newLine), off1)
	}

	entries, offset, err := Read(path, off1)
	if err != nil {
		t.Fatalf("Read with stale offset past EOF should reset, not error: %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "new" {
		t.Fatalf("got %+v, want the reset read to see the new file's content", entries)
	}
	if offset != int64(len(newLine)) {
		t.Errorf("offset = %d, want %d", offset, len(newLine))
	}
}

func TestReadUnknownShapesAreSkippedNotFatal(t *testing.T) {
	path := readTestdata(t, "unknown_shape.jsonl")
	entries, _, err := Read(path, 0)
	if err != nil {
		t.Fatalf("an unrecognised line shape must not abort the read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (the two real turns, weird lines skipped): %+v", len(entries), entries)
	}
	if entries[0].Text != "first real prompt" {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[1].Text != "second real reply, after the weird lines" {
		t.Errorf("entries[1] = %+v", entries[1])
	}
}

func TestReadMalformedJSONLineIsSkippedNotFatal(t *testing.T) {
	path := readTestdata(t, "malformed.jsonl")
	entries, offset, err := Read(path, 0)
	if err != nil {
		t.Fatalf("a malformed JSON line must not abort the read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].Text != "before the bad line" || entries[1].Text != "after the bad line" {
		t.Errorf("entries = %+v", entries)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if offset != info.Size() {
		t.Errorf("offset = %d, want %d (malformed line's bytes still consumed, it's complete)", offset, info.Size())
	}
}

func TestReadNonexistentFile(t *testing.T) {
	_, _, err := Read(filepath.Join(t.TempDir(), "does-not-exist.jsonl"), 0)
	if err == nil {
		t.Fatal("expected an error for a nonexistent transcript path")
	}
}

func TestReadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	entries, offset, err := Read(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || offset != 0 {
		t.Fatalf("got entries=%v offset=%d, want empty/0", entries, offset)
	}
}

func TestOffsetRoundTrip(t *testing.T) {
	repoRoot := t.TempDir()
	sessionID := "session-abc-123"

	loaded, err := LoadOffset(repoRoot, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Bytes != 0 {
		t.Fatalf("fresh session should have zero offset, got %+v", loaded)
	}

	want := scribe.Offset{SessionID: sessionID, Bytes: 4096, UpdatedAt: time.Now().UTC().Truncate(time.Second)}
	if err := SaveOffset(repoRoot, want); err != nil {
		t.Fatal(err)
	}

	got, err := LoadOffset(repoRoot, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != want.SessionID || got.Bytes != want.Bytes {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// State must land under the fixed StateDir constant so the rest of the
	// system (and .gitignore) can find it.
	statePath := filepath.Join(repoRoot, scribe.StateDir, "offsets", sessionID+".json")
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("expected offset file at %s: %v", statePath, err)
	}
}

func TestOffsetCorruptFileIsLoudError(t *testing.T) {
	repoRoot := t.TempDir()
	sessionID := "session-corrupt"
	dir := filepath.Join(repoRoot, scribe.StateDir, "offsets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadOffset(repoRoot, sessionID)
	if err == nil {
		t.Fatal("expected a loud error for a corrupt offset file, got nil")
	}
}

func TestSaveOffsetRequiresSessionID(t *testing.T) {
	err := SaveOffset(t.TempDir(), scribe.Offset{Bytes: 10})
	if err == nil {
		t.Fatal("expected an error when SessionID is empty")
	}
}
