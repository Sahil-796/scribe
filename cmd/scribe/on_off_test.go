package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/install"
)

// runOnOffCmd executes cmd (newOnCmd() or newOffCmd()) in dir, capturing
// stdout, the way runInitCmd does in init_test.go. Tests in this package
// run sequentially (no t.Parallel), so t.Chdir is safe here too.
func runOnOffCmd(t *testing.T, dir string, cmd *cobra.Command, args ...string) (stdout string, err error) {
	t.Helper()
	t.Chdir(dir)

	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs(args)

	err = cmd.Execute()
	return outBuf.String(), err
}

func TestOff_OutsideGitRepo_Errors(t *testing.T) {
	dir := t.TempDir() // deliberately no .git

	_, err := runOnOffCmd(t, dir, newOffCmd())
	if err == nil {
		t.Fatal("expected an error running `scribe off` outside a git repo, got nil")
	}
}

func TestOff_NeverInitialised_SaysSoAndDoesNotOnboard(t *testing.T) {
	dir := newTestRepo(t) // has .git, never ran `scribe init`

	out, err := runOnOffCmd(t, dir, newOffCmd())
	if err != nil {
		t.Fatalf("scribe off: %v", err)
	}
	if !strings.Contains(out, "scribe init") {
		t.Fatalf("expected output to point at `scribe init`, got %q", out)
	}

	if _, cfgErr := install.ReadConfig(dir); cfgErr != install.ErrNotInitialised {
		t.Fatalf("scribe off must not write a config for a never-initialised repo, got err=%v", cfgErr)
	}
}

func TestOff_Default_PausesUntilEndOfDay(t *testing.T) {
	dir := newTestRepo(t)
	writeTestConfig(t, dir)

	before := time.Now()
	out, err := runOnOffCmd(t, dir, newOffCmd())
	if err != nil {
		t.Fatalf("scribe off: %v", err)
	}

	cfg, cfgErr := install.ReadConfig(dir)
	if cfgErr != nil {
		t.Fatalf("reading config back: %v", cfgErr)
	}
	if cfg.Pause == nil {
		t.Fatal("expected Pause to be recorded")
	}
	if cfg.Pause.Stay {
		t.Fatal("default `scribe off` must not set Stay")
	}
	if cfg.Pause.Until == nil {
		t.Fatal("default `scribe off` must set an expiry")
	}
	wantUntil := install.EndOfLocalDay(before)
	if !cfg.Pause.Until.Equal(wantUntil) {
		t.Fatalf("Pause.Until = %v, want %v (end of local day)", *cfg.Pause.Until, wantUntil)
	}
	if !cfg.IsPaused(before) {
		t.Fatal("repo should read as paused immediately after `scribe off`")
	}

	if !strings.Contains(out, dir) {
		t.Fatalf("expected output to name the repo, got %q", out)
	}
	if !strings.Contains(out, "scribe on") {
		t.Fatalf("expected output to mention how to resume (`scribe on`), got %q", out)
	}
}

func TestOff_Stay_PausesIndefinitely(t *testing.T) {
	dir := newTestRepo(t)
	writeTestConfig(t, dir)

	out, err := runOnOffCmd(t, dir, newOffCmd(), "--stay")
	if err != nil {
		t.Fatalf("scribe off --stay: %v", err)
	}

	cfg, cfgErr := install.ReadConfig(dir)
	if cfgErr != nil {
		t.Fatalf("reading config back: %v", cfgErr)
	}
	if cfg.Pause == nil || !cfg.Pause.Stay {
		t.Fatalf("expected Pause.Stay = true, got %+v", cfg.Pause)
	}
	if cfg.Pause.Until != nil {
		t.Fatalf("--stay must not set an expiry, got %v", *cfg.Pause.Until)
	}
	// A pause a year from "now" would still be in effect: --stay is
	// indefinite, not a very long timer.
	if !cfg.IsPaused(time.Now().AddDate(1, 0, 0)) {
		t.Fatal("a --stay pause must still read as paused far in the future")
	}
	if !strings.Contains(out, "indefinitely") {
		t.Fatalf("expected output to say the pause is indefinite, got %q", out)
	}
}

func TestOn_OutsideGitRepo_Errors(t *testing.T) {
	dir := t.TempDir()

	_, err := runOnOffCmd(t, dir, newOnCmd())
	if err == nil {
		t.Fatal("expected an error running `scribe on` outside a git repo, got nil")
	}
}

func TestOn_NeverInitialised_SaysSoAndDoesNotOnboard(t *testing.T) {
	dir := newTestRepo(t)

	out, err := runOnOffCmd(t, dir, newOnCmd())
	if err != nil {
		t.Fatalf("scribe on: %v", err)
	}
	if !strings.Contains(out, "scribe init") {
		t.Fatalf("expected output to point at `scribe init`, got %q", out)
	}
	if _, cfgErr := install.ReadConfig(dir); cfgErr != install.ErrNotInitialised {
		t.Fatalf("scribe on must not write a config for a never-initialised repo, got err=%v", cfgErr)
	}
}

func TestOn_AlreadyOn_SaysSoRatherThanPretending(t *testing.T) {
	dir := newTestRepo(t)
	writeTestConfig(t, dir) // no pause set: scribe is already on

	out, err := runOnOffCmd(t, dir, newOnCmd())
	if err != nil {
		t.Fatalf("scribe on: %v", err)
	}
	if !strings.Contains(out, "already on") {
		t.Fatalf("expected output to say scribe is already on, got %q", out)
	}
}

func TestOn_ClearsAPause(t *testing.T) {
	dir := newTestRepo(t)
	writeTestConfig(t, dir)

	if _, err := runOnOffCmd(t, dir, newOffCmd(), "--stay"); err != nil {
		t.Fatalf("scribe off --stay: %v", err)
	}

	out, err := runOnOffCmd(t, dir, newOnCmd())
	if err != nil {
		t.Fatalf("scribe on: %v", err)
	}
	if !strings.Contains(out, "is on") {
		t.Fatalf("expected confirmation output, got %q", out)
	}

	cfg, cfgErr := install.ReadConfig(dir)
	if cfgErr != nil {
		t.Fatalf("reading config back: %v", cfgErr)
	}
	if cfg.Pause != nil {
		t.Fatalf("expected Pause to be cleared, got %+v", cfg.Pause)
	}
	if cfg.IsPaused(time.Now()) {
		t.Fatal("repo must not read as paused after `scribe on`")
	}
}

// writeTestConfig writes a minimal, valid config for dir directly via
// install.WriteConfig, standing in for a real `scribe init` run — this
// unit only owns scribe on/off, not init, and doesn't need a real writer
// or the wizard to exercise pause/resume.
func writeTestConfig(t *testing.T, dir string) {
	t.Helper()
	cfg := install.Config{
		Agent: "opencode",
		Model: "opencode/longcat-2.0-free",
	}
	if err := install.WriteConfig(dir, cfg); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
}
