package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// This file exercises OPEN-ITEMS item 22's third proof obligation: offset
// advancement across multiple interleaved sessions drained in one "scribe
// run". internal/worker (which owns runOnce's session loop) and
// internal/transcript (which owns offset persistence) both belong to other
// units on this fleet, so these tests stay black-box: they drive the real
// "scribe run" command with a fake writer (never a real agent process, per
// this unit's safety rules) and read the real offset files it produces
// under .scribe/offsets/, exactly as internal/transcript.SaveOffset writes
// them.

func readOffset(t *testing.T, repoRoot, sessionID string) scribe.Offset {
	t.Helper()
	path := filepath.Join(repoRoot, scribe.StateDir, "offsets", sessionID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading offset file for session %s: %v", sessionID, err)
	}
	var o scribe.Offset
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatalf("parsing offset file for session %s: %v", sessionID, err)
	}
	return o
}

func offsetFileExists(repoRoot, sessionID string) bool {
	path := filepath.Join(repoRoot, scribe.StateDir, "offsets", sessionID+".json")
	_, err := os.Stat(path)
	return err == nil
}

func seedRepoForRun(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("creating fake .git: %v", err)
	}
	if err := install.WriteConfig(dir, install.Config{
		Agent:   "opencode",
		Model:   "opencode/longcat-2.0-free",
		DocsDir: scribe.DocsDir,
		Enabled: true,
	}); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
}

func writeAssistantTranscript(t *testing.T, path, text string) {
	t.Helper()
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]},"timestamp":"2024-01-01T00:00:00Z","isSidechain":false}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("writing fake transcript: %v", err)
	}
}

// TestRun_TwoSessionsInOneDrain_AdvanceOffsetsIndependently enqueues
// triggers for two distinct sessions with two distinct transcripts, runs
// "scribe run" exactly once (one drain, both sessions processed together
// per runOnce's per-session loop), and asserts each session's offset
// lands at its own transcript's byte length — proving the offsets are
// tracked independently rather than one session's progress bleeding into
// the other's (e.g. both accidentally saved with the same byte count, or
// one clobbering the other's file).
func TestRun_TwoSessionsInOneDrain_AdvanceOffsetsIndependently(t *testing.T) {
	dir := t.TempDir()
	seedRepoForRun(t, dir)

	sessionA := "session-a"
	sessionB := "session-b"
	transcriptA := filepath.Join(dir, "transcript-a.jsonl")
	transcriptB := filepath.Join(dir, "transcript-b.jsonl")

	// Deliberately different lengths so "both sessions ended up with the
	// same offset" would be a visible failure, not a coincidence that
	// passes by accident.
	writeAssistantTranscript(t, transcriptA, "short reply from A")
	writeAssistantTranscript(t, transcriptB, "a rather longer reply from session B, padded out")

	infoA, err := os.Stat(transcriptA)
	if err != nil {
		t.Fatalf("stat transcript A: %v", err)
	}
	infoB, err := os.Stat(transcriptB)
	if err != nil {
		t.Fatalf("stat transcript B: %v", err)
	}
	if infoA.Size() == infoB.Size() {
		t.Fatalf("test setup bug: transcripts must differ in size, both are %d bytes", infoA.Size())
	}

	if err := queue.Enqueue(dir, scribe.Trigger{SessionID: sessionA, TranscriptPath: transcriptA, RepoRoot: dir}); err != nil {
		t.Fatalf("enqueue session A: %v", err)
	}
	if err := queue.Enqueue(dir, scribe.Trigger{SessionID: sessionB, TranscriptPath: transcriptB, RepoRoot: dir}); err != nil {
		t.Fatalf("enqueue session B: %v", err)
	}

	withFakeRunWriter(t, "- Updated by the fake writer.")

	stdout, _, err := runRunCmd(t, dir)
	if err != nil {
		t.Fatalf("scribe run failed: %v\nstdout: %s", err, stdout)
	}

	offA := readOffset(t, dir, sessionA)
	offB := readOffset(t, dir, sessionB)

	if offA.Bytes != infoA.Size() {
		t.Fatalf("session A offset = %d, want %d (transcript's full size)", offA.Bytes, infoA.Size())
	}
	if offB.Bytes != infoB.Size() {
		t.Fatalf("session B offset = %d, want %d (transcript's full size)", offB.Bytes, infoB.Size())
	}
	if offA.Bytes == offB.Bytes {
		t.Fatalf("both sessions ended up with the same offset (%d) despite differently-sized transcripts — offsets are not independent", offA.Bytes)
	}
	if offA.SessionID != sessionA {
		t.Fatalf("offset file for session A has SessionID %q", offA.SessionID)
	}
	if offB.SessionID != sessionB {
		t.Fatalf("offset file for session B has SessionID %q", offB.SessionID)
	}
}

// TestRun_OneSessionUnreadable_DoesNotCorruptTheOthersOffset enqueues one
// session with a healthy transcript and a second whose transcript path
// points at nothing (simulating a session whose transcript file vanished
// or was never created — a real failure mode, not a contrived one). It
// asserts the healthy session's offset, if written at all, is never
// wrong: either it truthfully reflects what was read (independent
// success) or it is absent entirely (the whole drain bailed out without
// saving anything) — the one outcome that would violate item 22's "a
// failure in one must not advance the other's" is a wrong, non-zero,
// non-matching offset for the broken session, or an offset for the good
// session that does not match its transcript's real size. This test
// documents which of those shapes the current implementation actually
// produces, since nothing on this fleet has ever observed it live.
func TestRun_OneSessionUnreadable_DoesNotCorruptTheOthersOffset(t *testing.T) {
	dir := t.TempDir()
	seedRepoForRun(t, dir)

	goodSession := "good-session"
	badSession := "bad-session"
	goodTranscript := filepath.Join(dir, "transcript-good.jsonl")
	badTranscript := filepath.Join(dir, "does-not-exist.jsonl") // never created

	writeAssistantTranscript(t, goodTranscript, "a perfectly fine reply")
	goodInfo, err := os.Stat(goodTranscript)
	if err != nil {
		t.Fatalf("stat good transcript: %v", err)
	}

	if err := queue.Enqueue(dir, scribe.Trigger{SessionID: goodSession, TranscriptPath: goodTranscript, RepoRoot: dir}); err != nil {
		t.Fatalf("enqueue good session: %v", err)
	}
	if err := queue.Enqueue(dir, scribe.Trigger{SessionID: badSession, TranscriptPath: badTranscript, RepoRoot: dir}); err != nil {
		t.Fatalf("enqueue bad session: %v", err)
	}

	withFakeRunWriter(t, "- Updated by the fake writer.")

	_, _, runErr := runRunCmd(t, dir)

	// Whether or not "scribe run" itself reports an error, no offset file
	// may ever be wrong. Check whichever shape actually occurred:
	if offsetFileExists(dir, goodSession) {
		off := readOffset(t, dir, goodSession)
		if off.Bytes != goodInfo.Size() {
			t.Fatalf("BUG: good session's offset (%d) does not match its transcript's real size (%d) — a broken sibling session corrupted an unrelated session's saved progress", off.Bytes, goodInfo.Size())
		}
	}
	if offsetFileExists(dir, badSession) {
		off := readOffset(t, dir, badSession)
		if off.Bytes != 0 {
			t.Fatalf("BUG: bad (unreadable-transcript) session got a non-zero offset (%d) saved despite its transcript never having been read", off.Bytes)
		}
	}

	// Document the actual current behaviour for whoever reads this test
	// next: does one unreadable session's transcript abort the whole
	// drain (both offsets absent, next run retries both — safe but
	// wasteful) or does the good session still get covered (offset
	// present and correct)? Either is a passing outcome per the
	// assertions above; this just records which one we saw.
	t.Logf("scribe run error (nil means it swallowed/logged the per-session failure): %v", runErr)
	t.Logf("good session offset file present: %v", offsetFileExists(dir, goodSession))
	t.Logf("bad session offset file present: %v", offsetFileExists(dir, badSession))
}
