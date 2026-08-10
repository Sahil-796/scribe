package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withGlobalDirs redirects both HOME and XDG_CONFIG_HOME at fresh
// t.TempDir()s for the duration of the test, and points
// globalClaudeSettingsPathFunc at a settings.json under that fake HOME. No
// test in this file may ever read or write the developer's real
// ~/.claude/settings.json or ~/.config/scribe — see the phase 04 spec's
// hardest safety rule for this unit.
func withGlobalDirs(t *testing.T) (home, settingsPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	settingsPath = filepath.Join(home, ".claude", "settings.json")
	orig := globalClaudeSettingsPathFunc
	globalClaudeSettingsPathFunc = func() (string, error) { return settingsPath, nil }
	t.Cleanup(func() { globalClaudeSettingsPathFunc = orig })
	return home, settingsPath
}

func newTestNudgeCmd() *cobra.Command {
	cmd := newNudgeCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd
}

func TestNudgeInstall_FreshHome(t *testing.T) {
	_, settingsPath := withGlobalDirs(t)

	cmd := newTestNudgeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--install"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scribe nudge --install: %v", err)
	}

	if !strings.Contains(out.String(), "Installed") {
		t.Errorf("output missing confirmation: %q", out.String())
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", settingsPath, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), "SessionStart") {
		t.Errorf("settings.json missing SessionStart event:\n%s", data)
	}
	if !strings.Contains(string(data), "nudge") {
		t.Errorf("settings.json missing the nudge command:\n%s", data)
	}
}

func TestNudgeInstall_PreservesExistingKeys(t *testing.T) {
	_, settingsPath := withGlobalDirs(t)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"model": "sonnet", "hooks": {"Stop": [{"hooks": [{"type":"command","command":"/usr/bin/other hook"}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newTestNudgeCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--install"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scribe nudge --install: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sonnet") {
		t.Errorf("existing \"model\" key lost:\n%s", data)
	}
	if !strings.Contains(string(data), "/usr/bin/other hook") {
		t.Errorf("existing Stop hook lost:\n%s", data)
	}
}

func TestNudgeInstall_Idempotent(t *testing.T) {
	withGlobalDirs(t)

	run := func() string {
		cmd := newTestNudgeCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--install"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("scribe nudge --install: %v", err)
		}
		return out.String()
	}
	run()
	second := run()
	if !strings.Contains(second, "already installed") {
		t.Errorf("second install output = %q, want it to say already installed", second)
	}
}

func TestNudgeRemove_RoundTrip(t *testing.T) {
	withGlobalDirs(t)

	install := newTestNudgeCmd()
	install.SetOut(&bytes.Buffer{})
	install.SetArgs([]string{"--install"})
	if err := install.Execute(); err != nil {
		t.Fatalf("--install: %v", err)
	}

	remove := newTestNudgeCmd()
	var out bytes.Buffer
	remove.SetOut(&out)
	remove.SetArgs([]string{"--remove"})
	if err := remove.Execute(); err != nil {
		t.Fatalf("--remove: %v", err)
	}
	if !strings.Contains(out.String(), "Removed") {
		t.Errorf("remove output = %q, want confirmation", out.String())
	}
}

func TestNudgeRemove_NothingInstalledIsNotAnError(t *testing.T) {
	withGlobalDirs(t)

	cmd := newTestNudgeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--remove"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scribe nudge --remove with nothing installed: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to remove") {
		t.Errorf("output = %q, want it to say nothing to remove", out.String())
	}
}

func TestNudgeInstallAndRemoveMutuallyExclusive(t *testing.T) {
	withGlobalDirs(t)

	cmd := newTestNudgeCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--install", "--remove"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("scribe nudge --install --remove: got nil error, want a mutually-exclusive-flags error")
	}
}

// TestNudgeBareInvocation_FiresAtThresholdInUninitialisedRepo exercises the
// hidden hook path (no flags) end to end through the real cobra command,
// proving newNudgeCmd's RunE wires cwd and internal/nudge.Run together
// correctly. It never touches ~/.claude/settings.json — that's only
// written by --install, not by a bare invocation.
func TestNudgeBareInvocation_FiresAtThresholdInUninitialisedRepo(t *testing.T) {
	withGlobalDirs(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	withWorkingDir(t, repo)

	var lastOut string
	for i := 0; i < 6; i++ {
		cmd := newTestNudgeCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("bare scribe nudge (session %d): %v", i, err)
		}
		lastOut = out.String()
	}
	if !strings.Contains(lastOut, "scribe init") {
		t.Fatalf("scribe nudge after 6 sessions in an uninitialised repo: output = %q, want the nudge line", lastOut)
	}
}

func TestNudgeBareInvocation_NeverErrorsOutsideAGitRepo(t *testing.T) {
	withGlobalDirs(t)
	withWorkingDir(t, t.TempDir()) // no .git

	cmd := newTestNudgeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bare scribe nudge outside a git repo: %v, want nil (must exit 0 no matter what)", err)
	}
	if out.Len() != 0 {
		t.Errorf("output = %q, want silence outside a git repo", out.String())
	}
}
