package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/writer"
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

// fakeDoctorWriter is a scribe.Writer double for the checks below. No test
// in this file may let doctor's "writer answers a trivial prompt" check
// reach a real opencode/codex/claude process — see doctor.go's
// newDoctorWriter doc comment — so every test that exercises that check
// substitutes this, or a constructor that errors, via newDoctorWriter.
type fakeDoctorWriter struct {
	response string
	err      error
}

func (f fakeDoctorWriter) Name() string { return "fake" }
func (f fakeDoctorWriter) Run(string) (string, error) {
	return f.response, f.err
}

// withFakeDoctorWriter substitutes newDoctorWriter for the duration of the
// test and restores it on cleanup, the same seam pattern init_test.go uses
// for newWriter.
func withFakeDoctorWriter(t *testing.T, w scribe.Writer, err error) {
	t.Helper()
	orig := newDoctorWriter
	newDoctorWriter = func(writer.Config) (scribe.Writer, error) { return w, err }
	t.Cleanup(func() { newDoctorWriter = orig })
}

// healthyDoctorRepo builds a repo that should pass every doctor check:
// git-initialised, .scribe/config.json naming the "custom" agent (so the
// $PATH check is skipped rather than depending on a real binary being
// installed on the machine running these tests), a Stop hook pointing at
// an actual file on disk, and all four docs present.
func healthyDoctorRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := install.WriteConfig(repo, install.Config{Agent: "custom", Model: "m", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	binPath := filepath.Join(repo, "scribe-fake-bin")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writing fake bin: %v", err)
	}
	if _, err := install.InstallStopHook(repo, binPath); err != nil {
		t.Fatalf("InstallStopHook: %v", err)
	}

	docsDir := filepath.Join(repo, scribe.DocsDir)
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs dir: %v", err)
	}
	for _, doc := range scribe.AllDocs {
		if err := os.WriteFile(filepath.Join(docsDir, string(doc)), []byte("# x\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", doc, err)
		}
	}
	return repo
}

func TestDoctor_NotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor outside a git repo: got nil error, want non-nil (exit non-zero)")
	}
	if !strings.Contains(out.String(), "git repository") {
		t.Errorf("doctor output missing the git-repo check: %q", out.String())
	}
}

func TestDoctor_NeverInitialised(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor in an uninitialised repo: got nil error, want non-nil")
	}
	got := out.String()
	if !strings.Contains(got, "scribe init") {
		t.Errorf("doctor output doesn't point at `scribe init`: %q", got)
	}
	if !strings.Contains(got, "SKIP") {
		t.Errorf("doctor output has no skipped checks for an uninitialised repo: %q", got)
	}
}

func TestDoctor_FullyHealthyRepoPassesEveryCheck(t *testing.T) {
	repo := healthyDoctorRepo(t)
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	var out bytes.Buffer
	if err := runDoctor(&out); err != nil {
		t.Fatalf("runDoctor on a healthy repo: %v\noutput:\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "FAIL") {
		t.Errorf("doctor output contains a FAIL on a healthy repo:\n%s", out.String())
	}
}

func TestDoctor_HookBinaryMissing(t *testing.T) {
	repo := healthyDoctorRepo(t)
	// The binary InstallStopHook pointed at existed at setup time; remove
	// it now to simulate the "moved or rebuilt elsewhere" failure mode the
	// spec calls out by name.
	if err := os.Remove(filepath.Join(repo, "scribe-fake-bin")); err != nil {
		t.Fatalf("removing fake bin: %v", err)
	}
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor with a missing hook binary: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "no longer exists") {
		t.Errorf("doctor output missing the moved-binary explanation: %q", out.String())
	}
}

func TestDoctor_MissingDocs(t *testing.T) {
	repo := healthyDoctorRepo(t)
	if err := os.Remove(filepath.Join(repo, scribe.DocsDir, string(scribe.DocJournal))); err != nil {
		t.Fatalf("removing JOURNAL.md: %v", err)
	}
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor with a missing doc: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "JOURNAL.md") {
		t.Errorf("doctor output doesn't name the missing doc: %q", out.String())
	}
}

func TestDoctor_UnregisteredAgent(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := install.WriteConfig(repo, install.Config{Agent: "not-a-real-agent", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	withWorkingDir(t, repo)
	// Deliberately not faking newDoctorWriter here: the point is that the
	// *real* writer.New rejects "not-a-real-agent" before this test would
	// ever risk spawning anything — writer.New never spawns a process for
	// any agent, registered or not, it only validates and builds a struct.

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor with an unregistered agent: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "unknown agent") {
		t.Errorf("doctor output missing the unknown-agent detail: %q", out.String())
	}
}

func TestDoctor_WriterConstructionFails(t *testing.T) {
	repo := healthyDoctorRepo(t)
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, nil, errors.New("boom: bad model"))

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor with a failing writer constructor: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "boom: bad model") {
		t.Errorf("doctor output missing the construction error: %q", out.String())
	}
}

func TestDoctor_TrivialPromptFails(t *testing.T) {
	repo := healthyDoctorRepo(t)
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{err: errors.New("writer: opencode timed out")}, nil)

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor with a writer that fails the trivial prompt: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "timed out") {
		t.Errorf("doctor output missing the writer's own error: %q", out.String())
	}
}

func TestDoctor_LockHeldByDeadPID(t *testing.T) {
	repo := healthyDoctorRepo(t)
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	host, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname unavailable: %v", err)
	}
	writeDoctorLock(t, repo, 999999999, host, time.Now())

	var out bytes.Buffer
	err2 := runDoctor(&out)
	if err2 == nil {
		t.Fatal("runDoctor with a lock held by a dead pid: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("doctor output missing the dead-pid explanation: %q", out.String())
	}
}

func TestDoctor_LockHeldByLiveProcessIsNotWedged(t *testing.T) {
	repo := healthyDoctorRepo(t)
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	host, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname unavailable: %v", err)
	}
	writeDoctorLock(t, repo, os.Getpid(), host, time.Now())

	var out bytes.Buffer
	if err := runDoctor(&out); err != nil {
		t.Fatalf("runDoctor with a live-held lock should not fail the queue check: %v\n%s", err, out.String())
	}
}

func TestDoctor_PendingFlagStaleWithNoLock(t *testing.T) {
	repo := healthyDoctorRepo(t)
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	origStale := queue.StaleLockAge
	queue.StaleLockAge = time.Millisecond
	t.Cleanup(func() { queue.StaleLockAge = origStale })

	pendingPath := filepath.Join(repo, scribe.StateDir, "pending")
	if err := os.WriteFile(pendingPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing pending flag: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(pendingPath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor with a stale pending flag and no lock: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "nothing draining") {
		t.Errorf("doctor output missing the stale-pending explanation: %q", out.String())
	}
}

func TestDoctor_Paused(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	until := time.Now().Add(2 * time.Hour)
	if err := install.WriteConfig(repo, install.Config{
		Agent:   "custom",
		Enabled: true,
		Pause:   &install.Pause{Since: time.Now(), Until: &until},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	binPath := filepath.Join(repo, "scribe-fake-bin")
	if err := os.WriteFile(binPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := install.InstallStopHook(repo, binPath); err != nil {
		t.Fatalf("InstallStopHook: %v", err)
	}
	docsDir := filepath.Join(repo, scribe.DocsDir)
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, doc := range scribe.AllDocs {
		if err := os.WriteFile(filepath.Join(docsDir, string(doc)), []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withWorkingDir(t, repo)
	withFakeDoctorWriter(t, fakeDoctorWriter{response: "ok"}, nil)

	var out bytes.Buffer
	err := runDoctor(&out)
	if err == nil {
		t.Fatal("runDoctor on a paused repo: got nil error, want non-nil")
	}
	if !strings.Contains(out.String(), "paused until") {
		t.Errorf("doctor output missing the pause explanation: %q", out.String())
	}
}

// writeDoctorLock writes a .scribe/lock file in the shape internal/queue
// itself writes (see queue.go's lockInfo), directly on disk — doctor's
// queue check is read-only against that documented shape, so the test
// drives it the same way.
func writeDoctorLock(t *testing.T, repo string, pid int, hostname string, startedAt time.Time) {
	t.Helper()
	stateDir := filepath.Join(repo, scribe.StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}
	info := struct {
		PID       int       `json:"pid"`
		Hostname  string    `json:"hostname"`
		StartedAt time.Time `json:"started_at"`
	}{pid, hostname, startedAt.UTC()}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal lock info: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "lock"), data, 0o644); err != nil {
		t.Fatalf("writing lock file: %v", err)
	}
}
