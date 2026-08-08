package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// setupRepo makes a t.TempDir() with a .scribe dir, the same shape
// FindRepoRoot expects.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}
	return dir
}

func TestLogFailure_WritesLine(t *testing.T) {
	repo := setupRepo(t)

	logFailure(repo, FailureEntry{
		Time:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		SessionID: "sess-1",
		Reason:    "enqueue failed: disk full",
	})

	entries, err := RecentFailures(repo)
	if err != nil {
		t.Fatalf("RecentFailures: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].SessionID != "sess-1" || entries[0].Reason != "enqueue failed: disk full" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
	if !entries[0].Time.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("unexpected time: %v", entries[0].Time)
	}
}

func TestRecentFailures_NoLog_ReturnsNilNoError(t *testing.T) {
	repo := setupRepo(t)

	entries, err := RecentFailures(repo)
	if err != nil {
		t.Fatalf("RecentFailures on a repo with no log: %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries, got %+v", entries)
	}
}

func TestLogFailure_StaysBounded(t *testing.T) {
	repo := setupRepo(t)

	// Log well past the cap to prove trimming actually happens, not just
	// that it doesn't grow past exactly the cap once.
	const attempts = maxFailureLines * 3
	for i := 0; i < attempts; i++ {
		logFailure(repo, FailureEntry{
			Time:      time.Now().UTC(),
			SessionID: "sess",
			Reason:    "failure " + strconv.Itoa(i),
		})
	}

	entries, err := RecentFailures(repo)
	if err != nil {
		t.Fatalf("RecentFailures: %v", err)
	}
	if len(entries) != maxFailureLines {
		t.Fatalf("got %d entries, want exactly the cap (%d)", len(entries), maxFailureLines)
	}
	// The log should hold the *most recent* maxFailureLines entries, i.e.
	// the oldest ones got trimmed off, not the newest.
	wantFirstReason := "failure " + strconv.Itoa(attempts-maxFailureLines)
	if entries[0].Reason != wantFirstReason {
		t.Fatalf("oldest surviving entry = %q, want %q (trimming should drop the oldest lines, not the newest)", entries[0].Reason, wantFirstReason)
	}
	wantLastReason := "failure " + strconv.Itoa(attempts-1)
	if entries[len(entries)-1].Reason != wantLastReason {
		t.Fatalf("newest entry = %q, want %q", entries[len(entries)-1].Reason, wantLastReason)
	}

	// Also sanity-check the file itself doesn't grow without bound: its
	// size should reflect exactly maxFailureLines entries, not the 3x
	// attempts made above. Bound generously (2x the longest single line
	// actually written, times the cap) so this isn't sensitive to
	// RFC3339Nano's variable-width fractional seconds — the point is
	// catching unbounded growth, not pinning an exact byte count.
	info, err := os.Stat(failureLogPath(repo))
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	longestLine, err := json.Marshal(FailureEntry{
		Time:      time.Now().UTC(),
		SessionID: "sess",
		Reason:    "failure " + strconv.Itoa(attempts-1),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	maxPlausibleSize := int64(len(longestLine)+1) * int64(maxFailureLines) * 2
	if info.Size() > maxPlausibleSize {
		t.Fatalf("log file size %d looks unbounded for a %d-line cap (plausible max ~%d)", info.Size(), maxFailureLines, maxPlausibleSize)
	}
}

func TestLogFailure_UnwritableDir_DoesNotPanicOrError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits don't work the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores permission bits")
	}
	repo := setupRepo(t)
	stateDir := filepath.Join(repo, scribe.StateDir)

	if err := os.Chmod(stateDir, 0o555); err != nil { // read+execute, no write
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) }) // let t.TempDir() clean up

	// Must not panic, and callers (Run) must not observe an error from this
	// — logFailure has no return value precisely so a caller can't
	// accidentally start propagating a logging failure as a hook failure.
	logFailure(repo, FailureEntry{Time: time.Now().UTC(), SessionID: "sess", Reason: "should not land anywhere"})

	entries, err := RecentFailures(repo)
	if err != nil {
		t.Fatalf("RecentFailures after a failed write: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected the write to have silently failed, got %+v", entries)
	}
}

// TestRun_EnqueueError_DoesNotChangeExitCode_WhenLoggingFails is the
// integration-level version of the guarantee above: Run's exit code must
// be identical whether or not the failure log write succeeds.
func TestRun_EnqueueError_ExitCodeUnaffectedByUnwritableLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits don't work the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores permission bits")
	}
	repo := setupRepo(t)
	stateDir := filepath.Join(repo, scribe.StateDir)
	if err := os.Chmod(stateDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })

	payload := payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-unwritable",
		TranscriptPath: "/tmp/t.jsonl",
		CWD:            repo,
		HookEventName:  "Stop",
	})

	var errBuf bytes.Buffer
	code := Run(bytes.NewReader(payload), &errBuf, func(string, scribe.Trigger) error {
		return os.ErrPermission // the actual failure under test
	})

	if code != ExitError {
		t.Fatalf("exit code = %d, want %d (unwritable log dir must not change this)", code, ExitError)
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected the original enqueue error on stderr regardless of logging outcome")
	}
}

func TestRun_EnqueueError_LogsFailure(t *testing.T) {
	repo := setupRepo(t)

	payload := payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-logged",
		TranscriptPath: "/tmp/t.jsonl",
		CWD:            repo,
		HookEventName:  "Stop",
	})

	var errBuf bytes.Buffer
	code := Run(bytes.NewReader(payload), &errBuf, func(string, scribe.Trigger) error {
		return os.ErrPermission
	})
	if code != ExitError {
		t.Fatalf("exit code = %d, want %d", code, ExitError)
	}

	entries, err := RecentFailures(repo)
	if err != nil {
		t.Fatalf("RecentFailures: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].SessionID != "sess-logged" {
		t.Fatalf("logged session id = %q, want sess-logged", entries[0].SessionID)
	}
	if !strings.Contains(entries[0].Reason, "enqueue failed") {
		t.Fatalf("logged reason = %q, want it to mention the enqueue failure", entries[0].Reason)
	}
}

func TestSplitNonEmptyLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single trailing newline", "a\n", []string{"a"}},
		{"multi", "a\nb\nc\n", []string{"a", "b", "c"}},
		{"blank lines dropped", "a\n\nb\n", []string{"a", "b"}},
		{"no trailing newline", "a\nb", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitNonEmptyLines([]byte(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("splitNonEmptyLines(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitNonEmptyLines(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestRecentFailures_SkipsUnparsableLines proves a corrupted or
// partially-written line (a real possibility given logFailure's
// best-effort, non-locked writes under concurrent hook invocations)
// degrades to "skip that one line," not "doctor shows nothing."
func TestRecentFailures_SkipsUnparsableLines(t *testing.T) {
	repo := setupRepo(t)
	good, err := json.Marshal(FailureEntry{Time: time.Now().UTC(), SessionID: "ok", Reason: "fine"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	content := string(good) + "\nnot json at all\n"
	if err := os.WriteFile(failureLogPath(repo), []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	entries, err := RecentFailures(repo)
	if err != nil {
		t.Fatalf("RecentFailures: %v", err)
	}
	if len(entries) != 1 || entries[0].SessionID != "ok" {
		t.Fatalf("expected exactly the one parsable entry, got %+v", entries)
	}
}
