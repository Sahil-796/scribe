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

// runStatusIn executes `scribe status` against dir, capturing stdout. Mirrors
// runOnOffCmd in on_off_test.go — status has no flags or subcommands, so
// there's nothing else for a helper to thread through.
func runStatusIn(t *testing.T, dir string) string {
	t.Helper()
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runStatus(&out); err != nil {
		t.Fatalf("scribe status: %v", err)
	}
	return out.String()
}

func TestStatus_OutsideGitRepo_NoErrorJustSaysSo(t *testing.T) {
	dir := t.TempDir() // deliberately no .git

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "Not a git repository") {
		t.Fatalf("expected a plain statement, got %q", out)
	}
}

func TestStatus_NeverInitialised(t *testing.T) {
	dir := newTestRepo(t) // has .git, `scribe init` never ran

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "never initialised") {
		t.Fatalf("expected mention of never being initialised, got %q", out)
	}
	if !strings.Contains(out, "scribe init") {
		t.Fatalf("expected a pointer to `scribe init`, got %q", out)
	}
	// Nothing recorded yet, in a repo scribe has never touched: this must
	// read as absence, not as an error.
	if !strings.Contains(out, "last run: none recorded yet") {
		t.Fatalf("expected 'none recorded yet' for last run, got %q", out)
	}
	if !strings.Contains(out, "queue: empty") {
		t.Fatalf("expected an empty queue, got %q", out)
	}
	if !strings.Contains(out, "lock: not held") {
		t.Fatalf("expected the lock to read as not held, got %q", out)
	}
	if !strings.Contains(out, "hook failures: none recorded") {
		t.Fatalf("expected no hook failures, got %q", out)
	}
}

func TestStatus_Off_NotEnabled(t *testing.T) {
	dir := newTestRepo(t)
	if err := install.WriteConfig(dir, install.Config{Agent: "opencode", Enabled: false}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "scribe: off") {
		t.Fatalf("expected an explicit off state, got %q", out)
	}
}

func TestStatus_PausedUntilReportsExpiry(t *testing.T) {
	dir := newTestRepo(t)
	until := time.Now().Add(3 * time.Hour)
	cfg := install.Config{
		Agent:   "opencode",
		Enabled: true,
		Pause:   &install.Pause{Since: time.Now(), Until: &until},
	}
	if err := install.WriteConfig(dir, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "paused until") {
		t.Fatalf("expected a paused-until line, got %q", out)
	}
}

func TestStatus_PausedStayReportsIndefinite(t *testing.T) {
	dir := newTestRepo(t)
	cfg := install.Config{
		Agent:   "opencode",
		Enabled: true,
		Pause:   &install.Pause{Since: time.Now(), Stay: true},
	}
	if err := install.WriteConfig(dir, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "paused indefinitely") {
		t.Fatalf("expected an indefinite pause line, got %q", out)
	}
}

func TestStatus_LiveOnReportsOn(t *testing.T) {
	dir := newTestRepo(t)
	if err := install.WriteConfig(dir, install.Config{Agent: "opencode", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "scribe: on.") {
		t.Fatalf("expected an on state, got %q", out)
	}
}

func TestStatus_LiveWithRecordedRun_ReportsChange(t *testing.T) {
	dir := newTestRepo(t)
	if err := install.WriteConfig(dir, install.Config{Agent: "opencode", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	store, err := docs.Open(dir)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	if err := store.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	if err := store.WriteState(scribe.DocProject, "# Project\n\nsomething real happened\n"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "changed 1 doc") {
		t.Fatalf("expected the run to be reported as a change, got %q", out)
	}
	if !strings.Contains(out, "scribe diff") {
		t.Fatalf("expected a pointer to `scribe diff`, got %q", out)
	}
}

func TestStatus_RunThatChangedNothingReadsDifferentlyFromNoRun(t *testing.T) {
	dir := newTestRepo(t)
	if err := install.WriteConfig(dir, install.Config{Agent: "opencode", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	store, err := docs.Open(dir)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	if err := store.ResetRun(); err != nil {
		t.Fatalf("ResetRun: %v", err)
	}
	// A run that started (ResetRun called) but touched no docs.

	out := runStatusIn(t, dir)
	if !strings.Contains(out, "touched nothing") {
		t.Fatalf("expected a run-happened-but-touched-nothing message, got %q", out)
	}
	if strings.Contains(out, "none recorded yet") {
		t.Fatalf("a recorded empty run must not read the same as no run at all, got %q", out)
	}
}
