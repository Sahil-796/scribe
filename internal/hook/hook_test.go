package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/scribe"
)

func payloadJSON(t *testing.T, p scribe.HookPayload) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

func TestRun_NotInitialised_ExitsZeroQuietly(t *testing.T) {
	dir := t.TempDir() // no .scribe here

	called := false
	enqueue := func(repoRoot string, tr scribe.Trigger) error {
		called = true
		return nil
	}

	in := bytes.NewReader(payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-1",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            dir,
		HookEventName:  "Stop",
	}))
	var errBuf bytes.Buffer

	code := Run(in, &errBuf, enqueue)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if called {
		t.Fatal("enqueue was called for an uninitialised repo")
	}
	if errBuf.Len() != 0 {
		t.Fatalf("expected no stderr output for the quiet no-op path, got %q", errBuf.String())
	}
}

func TestRun_Initialised_Enqueues(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}
	sub := filepath.Join(dir, "pkg", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	var gotRoot string
	var gotTrigger scribe.Trigger
	enqueue := func(repoRoot string, tr scribe.Trigger) error {
		gotRoot = repoRoot
		gotTrigger = tr
		return nil
	}

	in := bytes.NewReader(payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-2",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            sub, // cwd is a subdirectory of the repo root
		HookEventName:  "Stop",
	}))
	var errBuf bytes.Buffer

	code := Run(in, &errBuf, enqueue)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d, stderr=%q", code, ExitOK, errBuf.String())
	}
	if gotRoot != dir {
		t.Fatalf("enqueued repo root = %q, want %q", gotRoot, dir)
	}
	if gotTrigger.SessionID != "sess-2" || gotTrigger.TranscriptPath != "/tmp/transcript.jsonl" {
		t.Fatalf("unexpected trigger: %+v", gotTrigger)
	}
	if gotTrigger.RepoRoot != dir {
		t.Fatalf("trigger repo root = %q, want %q", gotTrigger.RepoRoot, dir)
	}
	if gotTrigger.EnqueuedAt.IsZero() || time.Since(gotTrigger.EnqueuedAt) > time.Minute {
		t.Fatalf("trigger EnqueuedAt looks wrong: %v", gotTrigger.EnqueuedAt)
	}
}

// initedRepoWithConfig creates a t.TempDir() with a real .scribe/config.json
// (via install.WriteConfig, not just a bare .scribe directory), so IsPaused
// has something real to read. cfg is mutated in place by withPause before
// writing, so tests can hand it a Pause without repeating the boilerplate.
func initedRepoWithConfig(t *testing.T, cfg install.Config) string {
	t.Helper()
	dir := t.TempDir()
	if err := install.WriteConfig(dir, cfg); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return dir
}

// TestRun_Paused_ExitsZeroQuietlyWithoutEnqueueing is the enforcement half
// of this unit's job: a `scribe off` that still let triggers reach the
// queue would just delay the writer, not pause it, since the queue drains
// the moment the pause lapses. So the hook must treat "paused" exactly
// like "never initialised" — exit 0, say nothing, enqueue nothing.
func TestRun_Paused_ExitsZeroQuietlyWithoutEnqueueing(t *testing.T) {
	dir := initedRepoWithConfig(t, install.Config{
		Agent: "opencode",
		Pause: &install.Pause{Since: time.Now(), Stay: true},
	})

	called := false
	enqueue := func(repoRoot string, tr scribe.Trigger) error {
		called = true
		return nil
	}

	in := bytes.NewReader(payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-paused",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            dir,
		HookEventName:  "Stop",
	}))
	var errBuf bytes.Buffer

	code := Run(in, &errBuf, enqueue)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if called {
		t.Fatal("enqueue was called for a paused repo")
	}
	if errBuf.Len() != 0 {
		t.Fatalf("expected no stderr output while paused, got %q", errBuf.String())
	}
}

// TestRun_PauseExpired_StillEnqueues is the other side of the same check:
// a lapsed pause (Until in the past, Stay false) must not silently keep
// blocking triggers forever — IsPaused evaluates expiry on read, and the
// hook has to actually rely on that rather than caching "was paused" from
// some earlier call.
func TestRun_PauseExpired_StillEnqueues(t *testing.T) {
	past := time.Now().Add(-24 * time.Hour)
	dir := initedRepoWithConfig(t, install.Config{
		Agent: "opencode",
		Pause: &install.Pause{Since: past.Add(-time.Hour), Until: &past},
	})

	called := false
	enqueue := func(repoRoot string, tr scribe.Trigger) error {
		called = true
		return nil
	}

	in := bytes.NewReader(payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-expired-pause",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            dir,
		HookEventName:  "Stop",
	}))
	var errBuf bytes.Buffer

	code := Run(in, &errBuf, enqueue)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d, stderr=%q", code, ExitOK, errBuf.String())
	}
	if !called {
		t.Fatal("enqueue was not called for a repo whose pause already lapsed")
	}
}

// TestRun_InitialisedNoConfig_TreatsMissingConfigAsNotPaused pins down
// isPaused's fail-open behavior for the case TestRun_Initialised_Enqueues
// above already exercises incidentally (a .scribe dir with no config.json
// inside it — install.ReadConfig returns ErrNotInitialised even though
// FindRepoRoot already said yes). A config read that can fail must not
// turn into the hook silently refusing to enqueue.
func TestRun_InitialisedNoConfig_TreatsMissingConfigAsNotPaused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}

	called := false
	enqueue := func(repoRoot string, tr scribe.Trigger) error {
		called = true
		return nil
	}

	in := bytes.NewReader(payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-no-config",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            dir,
		HookEventName:  "Stop",
	}))
	var errBuf bytes.Buffer

	code := Run(in, &errBuf, enqueue)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d, stderr=%q", code, ExitOK, errBuf.String())
	}
	if !called {
		t.Fatal("enqueue was not called for an initialised repo with no config.json yet")
	}
}

func TestRun_MalformedJSON_FailsLoudly(t *testing.T) {
	called := false
	enqueue := func(repoRoot string, tr scribe.Trigger) error {
		called = true
		return nil
	}

	in := strings.NewReader("{not json")
	var errBuf bytes.Buffer

	code := Run(in, &errBuf, enqueue)

	if code == ExitOK {
		t.Fatal("expected non-zero exit for malformed JSON")
	}
	if called {
		t.Fatal("enqueue must not be called on malformed input")
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected an error message on stderr")
	}
}

func TestRun_EmptyStdin_FailsLoudly(t *testing.T) {
	var errBuf bytes.Buffer
	code := Run(strings.NewReader(""), &errBuf, func(string, scribe.Trigger) error { return nil })
	if code == ExitOK {
		t.Fatal("expected non-zero exit for empty stdin")
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected an error message on stderr")
	}
}

func TestRun_MissingRequiredFields_FailsLoudly(t *testing.T) {
	cases := []scribe.HookPayload{
		{TranscriptPath: "/tmp/t.jsonl", CWD: "/tmp"},    // missing session_id
		{SessionID: "s", CWD: "/tmp"},                    // missing transcript_path
		{SessionID: "s", TranscriptPath: "/tmp/t.jsonl"}, // missing cwd
	}
	for i, p := range cases {
		var errBuf bytes.Buffer
		code := Run(bytes.NewReader(payloadJSON(t, p)), &errBuf, func(string, scribe.Trigger) error { return nil })
		if code == ExitOK {
			t.Fatalf("case %d: expected non-zero exit for missing required field, payload=%+v", i, p)
		}
		if errBuf.Len() == 0 {
			t.Fatalf("case %d: expected an error message on stderr", i)
		}
	}
}

func TestRun_EnqueueError_FailsLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}

	enqueue := func(repoRoot string, tr scribe.Trigger) error {
		return os.ErrPermission
	}

	in := bytes.NewReader(payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-3",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            dir,
	}))
	var errBuf bytes.Buffer

	code := Run(in, &errBuf, enqueue)

	if code == ExitOK {
		t.Fatal("expected non-zero exit when enqueue fails")
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected an error message on stderr")
	}
}

func TestFindRepoRoot(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, ok := FindRepoRoot(nested); ok {
		t.Fatal("expected no repo root without a .scribe dir anywhere")
	}

	if err := os.Mkdir(filepath.Join(dir, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}

	root, ok := FindRepoRoot(nested)
	if !ok || root != dir {
		t.Fatalf("FindRepoRoot(%q) = (%q, %v), want (%q, true)", nested, root, ok, dir)
	}

	// cwd == repo root itself
	root, ok = FindRepoRoot(dir)
	if !ok || root != dir {
		t.Fatalf("FindRepoRoot(%q) = (%q, %v), want (%q, true)", dir, root, ok, dir)
	}
}

// TestHookLatencyBudget exercises the part of the hard 50ms budget that this
// package owns: payload decode plus repo-root resolution, on the quiet
// no-op path (the common case — most repos never call scribe init). It
// leaves a wide margin below 50ms since queue.Enqueue (owned by
// internal/queue) and process startup also eat into that budget; see
// cmd/scribe's own latency test for the full binary's wall time.
func TestHookLatencyBudget(t *testing.T) {
	dir := t.TempDir()
	payload := payloadJSON(t, scribe.HookPayload{
		SessionID:      "sess-latency",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            dir,
		HookEventName:  "Stop",
	})

	const budget = 5 * time.Millisecond
	start := time.Now()
	code := Run(bytes.NewReader(payload), &bytes.Buffer{}, func(string, scribe.Trigger) error { return nil })
	elapsed := time.Since(start)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if elapsed > budget {
		t.Fatalf("hook.Run took %v, want under %v (hard budget for the whole `scribe hook` command is 50ms; see docs/PLAN.md)", elapsed, budget)
	}
}
