package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// This file tests the spawn behavior added to `scribe hook` in hook.go:
// after a trigger is enqueued, the hook fires a detached "scribe run" and
// returns without waiting on it, and never lets a spawn failure change its
// exit code or lose the trigger.
//
// Every test here runs the real built binary as a subprocess (matching
// hook_latency_test.go's approach) rather than calling package functions
// directly, because the behavior under test — process exit code, whether
// the parent blocks, whether a child gets left running — is only
// observable at the process boundary. None of these tests spawn a real
// "scribe run" / opencode: SCRIBE_NO_SPAWN suppresses spawning entirely,
// and SCRIBE_SPAWN_BIN redirects spawning at a stub script this file
// writes, or at a path that's guaranteed not to exist.

// initedRepo returns a t.TempDir() with a ".scribe" directory, the only
// thing hook.FindRepoRoot checks for to treat a directory as "scribe was
// initialised here" (see internal/hook/hook.go). That's enough to reach
// the enqueue+spawn path without needing a full "scribe init --apply".
func initedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, scribe.StateDir), 0o755); err != nil {
		t.Fatalf("create .scribe dir: %v", err)
	}
	return repo
}

func samplePayload(t *testing.T, repo string) []byte {
	t.Helper()
	payload, err := json.Marshal(scribe.HookPayload{
		SessionID:      "sess-spawn-test",
		TranscriptPath: "/tmp/does-not-need-to-exist.jsonl",
		CWD:            repo,
		HookEventName:  "Stop",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

// queueLineCount reads back repo/.scribe/queue.jsonl and returns how many
// trigger lines landed, so tests can confirm the trigger was actually
// enqueued (not just that the process exited 0).
func queueLineCount(t *testing.T, repo string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, scribe.StateDir, "queue.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read queue.jsonl: %v", err)
	}
	n := 0
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// sleeperStub writes an executable shell script that sleeps for the given
// duration and then exits, standing in for a slow "scribe run" the hook
// must never wait on. It's a plain shell script rather than a compiled Go
// stub: the only thing that matters is that it takes noticeably longer
// than the hook's own budget and does nothing else observable.
func sleeperStub(t *testing.T, sleepSeconds int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scribe-stub.sh")
	script := "#!/bin/sh\nsleep " + itoa(sleepSeconds) + "\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}

func itoa(n int) string {
	// Tiny local helper so this file doesn't need strconv just for one
	// single-digit conversion in a shell script template.
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestHookSpawnSuppressed confirms the escape hatch: with SCRIBE_NO_SPAWN
// set, the hook still exits 0 and still enqueues the trigger, and spawns
// nothing at all (there is no bin to even resolve in this test, since
// noSpawnEnvVar is checked before SCRIBE_SPAWN_BIN is read).
func TestHookSpawnSuppressed(t *testing.T) {
	bin := buildScribeBinary(t)
	repo := initedRepo(t)
	payload := samplePayload(t, repo)

	cmd := exec.Command(bin, "hook")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), noSpawnEnvVar+"=1")

	if err := cmd.Run(); err != nil {
		t.Fatalf("scribe hook exited non-zero with SCRIBE_NO_SPAWN=1: %v (stderr: %s)", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if n := queueLineCount(t, repo); n != 1 {
		t.Fatalf("queue.jsonl has %d trigger(s), want exactly 1", n)
	}
}

// TestHookSpawnFailureDoesNotBreakHook points the spawn at a path that is
// guaranteed not to exist (unresolvable binary). The hook must still exit
// 0 and the trigger must still be enqueued — a failed spawn must never look
// like a failed enqueue, since by the time spawnRun runs, the trigger is
// already durably on disk.
func TestHookSpawnFailureDoesNotBreakHook(t *testing.T) {
	bin := buildScribeBinary(t)
	repo := initedRepo(t)
	payload := samplePayload(t, repo)

	unresolvable := filepath.Join(t.TempDir(), "no-such-binary-here")

	cmd := exec.Command(bin, "hook")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), spawnBinEnvVar+"="+unresolvable)

	if err := cmd.Run(); err != nil {
		t.Fatalf("scribe hook exited non-zero when spawn target was unresolvable: %v (stderr: %s)", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr on a spawn failure (should be silently swallowed): %s", stderr.String())
	}
	if n := queueLineCount(t, repo); n != 1 {
		t.Fatalf("queue.jsonl has %d trigger(s), want exactly 1 (spawn failure must not lose the trigger)", n)
	}

	// OPEN-ITEMS item 23: the failure must leave a trace. A spawn that never
	// starts means the docs quietly stop updating, and the hook's own exit
	// code can't say so — it is required to stay 0 here.
	failures, err := hook.RecentFailures(repo)
	if err != nil {
		t.Fatalf("reading hook failure log: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("hook failure log has %d entries, want 1 (a silent spawn failure is invisible)", len(failures))
	}
	if !strings.Contains(failures[0].Reason, "spawning") {
		t.Errorf("logged reason doesn't mention the spawn: %q", failures[0].Reason)
	}
}

// TestHookDoesNotWaitOnChild spawns a stub that sleeps far longer than the
// hook's 50ms budget and confirms the hook process itself still returns
// quickly — proving Run/Start semantics (fire-and-forget), not Wait
// semantics. The stub is detached (Setsid) and short (2s), so it exits on
// its own shortly after the test finishes rather than lingering as a real
// stray background process.
func TestHookDoesNotWaitOnChild(t *testing.T) {
	bin := buildScribeBinary(t)
	repo := initedRepo(t)
	payload := samplePayload(t, repo)
	stub := sleeperStub(t, 2)

	cmd := exec.Command(bin, "hook")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), spawnBinEnvVar+"="+stub)

	// Generous relative to the hook's real ~ms-scale cost (see
	// hook_latency_test.go's 50ms budget) but still comfortably below the
	// stub's 2s sleep — the point is distinguishing "didn't wait on the
	// child" from "waited 2s", not re-asserting the tight latency budget,
	// and -race's instrumentation overhead alone can approach several
	// hundred ms for a process spawn.
	const mustReturnWithin = 1500 * time.Millisecond

	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("scribe hook exited non-zero: %v (stderr: %s)", err, stderr.String())
	}
	elapsed := time.Since(start)

	if elapsed > mustReturnWithin {
		t.Fatalf("scribe hook took %v to return, want under %v — it appears to be waiting on the spawned child", elapsed, mustReturnWithin)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if n := queueLineCount(t, repo); n != 1 {
		t.Fatalf("queue.jsonl has %d trigger(s), want exactly 1", n)
	}
}
