package writer

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stubScript writes an executable shell script to a temp dir and returns its
// path. Used instead of the real opencode binary so tests never touch the
// network or spend money.
func stubScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub scripts are POSIX shell; skipping on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "stub.sh")
	content := "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// TestOpencodeArgv_ExactFlags pins the exact argv opencodeArgv builds, so a
// future edit can't silently reintroduce a guessed flag (--print,
// --auto-approve) or drop the real one (--auto). Verified empirically
// against `opencode run --help` (1.18.15) and docs/findings/00-writer.md /
// docs/findings/07-live-run.md: --auto is required (its absence makes
// opencode silently auto-reject every edit and still exit 0), --print does
// not exist, and --format must stay unset since the default format's stdout
// is what internal/worker.parseEdits expects (--format json would break it).
func TestOpencodeArgv_ExactFlags(t *testing.T) {
	got := opencodeArgv("opencode/longcat-2.0-free", "the prompt")
	want := []string{"run", "--model", "opencode/longcat-2.0-free", "--auto", "the prompt"}
	if len(got) != len(want) {
		t.Fatalf("opencodeArgv() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("opencodeArgv() = %#v, want %#v", got, want)
		}
	}
	for _, forbidden := range []string{"--print", "--auto-approve", "--format"} {
		for _, a := range got {
			if a == forbidden {
				t.Fatalf("opencodeArgv() contains %q, which is not a real opencode flag (or breaks parseEdits): %#v", forbidden, got)
			}
		}
	}
}

// TestOpencodeArgv_NoModel checks the model flag is omitted, not passed
// empty, when no model is configured.
func TestOpencodeArgv_NoModel(t *testing.T) {
	got := opencodeArgv("", "prompt")
	want := []string{"run", "--auto", "prompt"}
	if len(got) != len(want) {
		t.Fatalf("opencodeArgv() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("opencodeArgv() = %#v, want %#v", got, want)
		}
	}
}

func TestNewUnknownAgent(t *testing.T) {
	_, err := New(Config{Agent: "not-a-real-agent"})
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestNewRequiresAgent(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error when Agent is empty")
	}
}

func TestOpencodeWriterRunsAndCapturesStdout(t *testing.T) {
	script := stubScript(t, `echo "wrote: $*"`)

	w, err := New(Config{Agent: "opencode", Model: "test-model", Command: script, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.Name() != "opencode" {
		t.Fatalf("Name() = %q, want opencode", w.Name())
	}

	out, err := w.Run("hello prompt")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "hello prompt") {
		t.Fatalf("expected prompt to reach the connector, got %q", out)
	}
	if !strings.Contains(out, "test-model") {
		t.Fatalf("expected model flag to reach the connector, got %q", out)
	}
}

func TestOpencodeWriterStdinIsNotATTYAndIsEmpty(t *testing.T) {
	// A script that tries to read stdin should see immediate EOF, not block.
	script := stubScript(t, `
if read -r line; then
  echo "got:$line"
else
  echo "eof"
fi
`)
	w, err := New(Config{Agent: "opencode", Command: script, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := w.Run("prompt")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "eof") {
		t.Fatalf("expected stdin to read EOF immediately (no TTY, no input), got %q", out)
	}
}

func TestRunUnattendedNonZeroExit(t *testing.T) {
	script := stubScript(t, `echo "boom" >&2; exit 3`)
	w, err := New(Config{Agent: "custom", Args: []string{script}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = w.Run("x")
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr in error, got: %v", err)
	}
}

func TestRunUnattendedTimeoutKillsProcess(t *testing.T) {
	script := stubScript(t, `sleep 30`)
	w, err := New(Config{Agent: "custom", Args: []string{script}, Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	_, err = w.Run("x")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Run took %s, timeout+kill should be fast", elapsed)
	}
}

func TestRunUnattendedKillsWholeProcessGroup(t *testing.T) {
	// Parent sleeps briefly then exits; child (spawned with &) sleeps much
	// longer. If only the parent pid were killed on timeout, the child would
	// survive as an orphan. We write the child's pid to a file and check it's
	// gone shortly after the timeout fires.
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := stubScript(t, `
sleep 30 &
echo $! > `+pidFile+`
wait
`)
	w, err := New(Config{Agent: "custom", Args: []string{script}, Timeout: 1500 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, _ = w.Run("x") // expected to time out; error already covered above

	// Give the kill a moment to land, then check the child pid is gone.
	deadline := time.Now().Add(8 * time.Second)
	for {
		pidBytes, err := os.ReadFile(pidFile)
		if err == nil && len(pidBytes) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never wrote its pid file")
		}
		time.Sleep(20 * time.Millisecond)
	}

	pid := strings.TrimSpace(string(mustRead(t, pidFile)))
	if !processGone(pid, deadline) {
		t.Fatalf("child process %s survived the timeout kill (process-group kill not working)", pid)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// processGone polls until pid (as decimal string) no longer resolves to a
// running process, or deadline passes. Uses signal 0, the standard
// "does this pid exist" probe: it's delivered to no one but still
// performs the existence/permission check.
func processGone(pid string, deadline time.Time) bool {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return true
	}
	for time.Now().Before(deadline) {
		if err := syscall.Kill(n, 0); err != nil {
			return true // ESRCH: no such process
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
