package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// withWorkingDir chdirs to dir for the duration of the test, restoring the
// original cwd on cleanup. printRecentHookFailures resolves the repo from
// os.Getwd() the same way `scribe hook` itself does (see hook.FindRepoRoot),
// so the test drives it the same way a real invocation would.
func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restoring cwd: %v", err)
		}
	})
}

func TestPrintRecentHookFailures_NoRepo_PrintsNothing(t *testing.T) {
	dir := t.TempDir() // no .scribe here
	withWorkingDir(t, dir)

	var out bytes.Buffer
	printRecentHookFailures(&out)

	if out.Len() != 0 {
		t.Fatalf("expected no output outside an initialised repo, got %q", out.String())
	}
}

func TestPrintRecentHookFailures_InitialisedNoFailures_PrintsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}
	withWorkingDir(t, dir)

	var out bytes.Buffer
	printRecentHookFailures(&out)

	if out.Len() != 0 {
		t.Fatalf("expected no output when the repo has never had a failing hook, got %q", out.String())
	}
}

func TestPrintRecentHookFailures_ShowsWhatsThere(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}

	// Drive an actual hook failure through the real path (Run -> enqueue
	// error -> logFailure) rather than poking the log file directly, so
	// this test also proves doctor and the hook package agree on where
	// the log lives and what it contains.
	payload := []byte(`{"session_id":"sess-doctor","transcript_path":"/tmp/t.jsonl","cwd":"` + jsonEscape(dir) + `","hook_event_name":"Stop"}`)
	var stderr bytes.Buffer
	code := hook.Run(bytes.NewReader(payload), &stderr, func(string, scribe.Trigger) error {
		return errBoom{}
	})
	if code != hook.ExitError {
		t.Fatalf("hook.Run exit code = %d, want %d (stderr=%q)", code, hook.ExitError, stderr.String())
	}

	withWorkingDir(t, dir)

	var out bytes.Buffer
	printRecentHookFailures(&out)

	got := out.String()
	if !strings.Contains(got, "sess-doctor") {
		t.Fatalf("doctor output missing the failed session id, got %q", got)
	}
	if !strings.Contains(got, "enqueue failed") {
		t.Fatalf("doctor output missing the failure reason, got %q", got)
	}
	if !strings.Contains(got, dir) {
		t.Fatalf("doctor output missing the repo root, got %q", got)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

// jsonEscape escapes a path for inline use in the literal JSON payload
// above. Test-only; a real caller would use encoding/json.
func jsonEscape(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}
