package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// buildScribeBinary compiles the real cmd/scribe binary once, the same way
// "make build" does, so the latency test below measures actual process
// startup cost — not `go run`'s compile-and-cache overhead.
func buildScribeBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "scribe")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "." // this package's own directory: cmd/scribe
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building scribe binary: %v\n%s", err, out)
	}
	return bin
}

// TestHookCommandLatencyBudget is the end-to-end check for the plan's one
// hard performance constraint (docs/PLAN.md, "Phases > 01" and "Stack"):
// `scribe hook` must run in under 50ms, covering process startup and all,
// since that's what actually blocks the user's Claude session.
//
// It exercises the quiet no-op path (repo never initialised: no .scribe
// dir), which is the common case and doesn't touch internal/queue, so this
// test's result isn't affected by that package's own performance.
func TestHookCommandLatencyBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-spawn latency test in -short mode")
	}

	bin := buildScribeBinary(t)
	repo := t.TempDir() // no .scribe here: scribe was never initialised

	payload, err := json.Marshal(scribe.HookPayload{
		SessionID:      "sess-e2e",
		TranscriptPath: "/tmp/transcript.jsonl",
		CWD:            repo,
		HookEventName:  "Stop",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	const budget = 50 * time.Millisecond

	// Warm up the OS page/exec cache with one untimed run before measuring,
	// since the budget is about the hook's own cost, not disk-cache misses.
	if err := runHookOnce(bin, payload); err != nil {
		t.Fatalf("warm-up run: %v", err)
	}

	const runs = 5
	for i := 0; i < runs; i++ {
		start := time.Now()
		if err := runHookOnce(bin, payload); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		elapsed := time.Since(start)
		if elapsed > budget {
			t.Fatalf("run %d: `scribe hook` took %v, want under %v (hard constraint, see docs/PLAN.md)", i, elapsed, budget)
		}
	}
}

func runHookOnce(bin string, payload []byte) error {
	cmd := exec.Command(bin, "hook")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return err
	}
	if stderr.Len() != 0 {
		return errString("unexpected stderr on the quiet no-op path: " + stderr.String())
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }
