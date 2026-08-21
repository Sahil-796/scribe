package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testBin = "/usr/local/bin/scribe"

func TestInstallStopHook_FreshRepo(t *testing.T) {
	repo := t.TempDir()

	res, err := InstallStopHook(repo, testBin)
	if err != nil {
		t.Fatalf("InstallStopHook: %v", err)
	}
	if !res.Added {
		t.Errorf("Added = false, want true")
	}
	if res.AlreadyPresent {
		t.Errorf("AlreadyPresent = true, want false")
	}
	if res.Backup != "" {
		t.Errorf("Backup = %q, want empty (no pre-existing file to back up)", res.Backup)
	}
	wantPath := filepath.Join(repo, ".claude", "settings.json")
	if res.SettingsPath != wantPath {
		t.Errorf("SettingsPath = %q, want %q", res.SettingsPath, wantPath)
	}

	installed, err := StopHookInstalled(repo)
	if err != nil {
		t.Fatalf("StopHookInstalled: %v", err)
	}
	if !installed {
		t.Errorf("StopHookInstalled = false after install, want true")
	}

	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), "hook") {
		t.Errorf("settings.json doesn't mention the hook command:\n%s", data)
	}
}

func TestInstallStopHook_Idempotent(t *testing.T) {
	repo := t.TempDir()

	first, err := InstallStopHook(repo, testBin)
	if err != nil {
		t.Fatalf("first InstallStopHook: %v", err)
	}
	if !first.Added || first.AlreadyPresent {
		t.Fatalf("first install: Added=%v AlreadyPresent=%v, want Added=true AlreadyPresent=false", first.Added, first.AlreadyPresent)
	}

	second, err := InstallStopHook(repo, testBin)
	if err != nil {
		t.Fatalf("second InstallStopHook: %v", err)
	}
	if second.Added {
		t.Errorf("second install: Added = true, want false")
	}
	if !second.AlreadyPresent {
		t.Errorf("second install: AlreadyPresent = false, want true")
	}

	// The real assertion: exactly one Stop hook entry invoking scribe, not two.
	path := filepath.Join(repo, ".claude", "settings.json")
	count := countScribeStopHooks(t, path)
	if count != 1 {
		t.Errorf("got %d scribe Stop hook entries after two installs, want 1", count)
	}
}

func TestInstallStopHook_PreservesExistingUnrelatedStopHook(t *testing.T) {
	repo := t.TempDir()
	settingsDir := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "model": "sonnet",
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {"type": "command", "command": "/usr/local/bin/some-other-tool notify"}
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "echo hi"}
        ]
      }
    ]
  },
  "theme": "dark"
}`
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := InstallStopHook(repo, testBin)
	if err != nil {
		t.Fatalf("InstallStopHook: %v", err)
	}
	if !res.Added {
		t.Errorf("Added = false, want true")
	}
	if res.Backup == "" {
		t.Errorf("Backup path is empty, want a backup of the pre-existing file")
	} else if _, err := os.Stat(res.Backup); err != nil {
		t.Errorf("backup file missing: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json is not valid JSON after install: %v\n%s", err, data)
	}

	if parsed["model"] != "sonnet" {
		t.Errorf("model key lost: got %v", parsed["model"])
	}
	if parsed["theme"] != "dark" {
		t.Errorf("theme key lost: got %v", parsed["theme"])
	}

	hooks, ok := parsed["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing or wrong type: %v", parsed["hooks"])
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Errorf("PreToolUse hooks lost")
	}

	stop, ok := hooks["Stop"].([]any)
	if !ok {
		t.Fatalf("Stop key missing or wrong type: %v", hooks["Stop"])
	}
	if len(stop) != 2 {
		t.Fatalf("got %d Stop groups, want 2 (existing unrelated + new scribe entry): %v", len(stop), stop)
	}

	if !strings.Contains(string(data), "some-other-tool notify") {
		t.Errorf("existing unrelated Stop hook command lost:\n%s", data)
	}
	if !strings.Contains(string(data), "scribe") {
		t.Errorf("new scribe Stop hook command missing:\n%s", data)
	}
}

func TestInstallStopHook_ExistingScribeHookDetected(t *testing.T) {
	repo := t.TempDir()
	settingsDir := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A scribe hook installed at a different path than this call uses —
	// StopHookInstalled/InstallStopHook should still recognize it as scribe's.
	original := `{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {"type": "command", "command": "/opt/other/path/scribe hook"}
        ]
      }
    ]
  }
}`
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	installed, err := StopHookInstalled(repo)
	if err != nil {
		t.Fatalf("StopHookInstalled: %v", err)
	}
	if !installed {
		t.Errorf("StopHookInstalled = false, want true (existing scribe hook at different path)")
	}

	res, err := InstallStopHook(repo, testBin)
	if err != nil {
		t.Fatalf("InstallStopHook: %v", err)
	}
	if res.Added {
		t.Errorf("Added = true, want false (already installed at different path)")
	}
	if !res.AlreadyPresent {
		t.Errorf("AlreadyPresent = false, want true")
	}
	if res.Backup != "" {
		t.Errorf("Backup = %q, want empty (nothing was written)", res.Backup)
	}

	// File must be untouched: still exactly one hook entry, at the original path.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := countScribeStopHooks(t, settingsPath); count != 1 {
		t.Errorf("got %d scribe Stop hook entries, want 1 (nothing should have been written):\n%s", count, data)
	}
	if !strings.Contains(string(data), "/opt/other/path/scribe hook") {
		t.Errorf("original hook command path was lost:\n%s", data)
	}
}

func TestInstallStopHook_MalformedSettingsJSON(t *testing.T) {
	repo := t.TempDir()
	settingsDir := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	malformed := `{ "hooks": { "Stop": [ this is not json`
	if err := os.WriteFile(settingsPath, []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := InstallStopHook(repo, testBin)
	if err == nil {
		t.Fatalf("InstallStopHook on malformed JSON: got nil error, want an error")
	}

	// The original file must be left exactly as it was — no partial/corrupt write.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != malformed {
		t.Errorf("malformed settings.json was modified:\ngot:  %s\nwant: %s", data, malformed)
	}

	if _, err := StopHookInstalled(repo); err == nil {
		t.Errorf("StopHookInstalled on malformed JSON: got nil error, want an error")
	}
}

func TestInstallStopHook_MalformedHooksIsNotObject(t *testing.T) {
	repo := t.TempDir()
	settingsDir := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	// Valid JSON overall, but "hooks" is the wrong shape.
	bad := `{"hooks": "not-an-object"}`
	if err := os.WriteFile(settingsPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := InstallStopHook(repo, testBin)
	if err == nil {
		t.Fatalf("InstallStopHook: got nil error, want an error for hooks not being an object")
	}
}

func TestStopHookInstalled_NoSettingsFile(t *testing.T) {
	repo := t.TempDir()
	installed, err := StopHookInstalled(repo)
	if err != nil {
		t.Fatalf("StopHookInstalled: %v", err)
	}
	if installed {
		t.Errorf("StopHookInstalled = true in a repo with no settings.json")
	}
}

func TestInstallStopHook_EmptyArgs(t *testing.T) {
	repo := t.TempDir()
	if _, err := InstallStopHook("", testBin); err == nil {
		t.Errorf("InstallStopHook with empty repoRoot: got nil error, want error")
	}
	if _, err := InstallStopHook(repo, ""); err == nil {
		t.Errorf("InstallStopHook with empty scribeBinPath: got nil error, want error")
	}
}

// countScribeStopHooks parses path and counts hook entries under
// hooks.Stop[*].hooks whose command looks like a scribe hook invocation.
func countScribeStopHooks(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	count := 0
	for _, g := range doc.Hooks.Stop {
		for _, h := range g.Hooks {
			if strings.Contains(h.Command, "scribe") && strings.HasSuffix(strings.TrimSpace(h.Command), "hook") {
				count++
			}
		}
	}
	return count
}

func TestIsScribeHookCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`"/usr/local/bin/scribe" hook`, true},
		{`/usr/local/bin/scribe hook`, true},
		{`/opt/other/path/scribe hook`, true},
		{`"/usr/local/bin/some-other-tool notify"`, false},
		{`/usr/local/bin/some-other-tool notify`, false},
		{``, false},
		{`hook`, false},
		{`scribe`, false},
	}
	for _, c := range cases {
		got := isScribeHookCommand(c.cmd)
		if got != c.want {
			t.Errorf("isScribeHookCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestErrNotInitialisedIsDistinguishable(t *testing.T) {
	repo := t.TempDir()
	_, err := ReadConfig(repo)
	if !errors.Is(err, ErrNotInitialised) {
		t.Errorf("ReadConfig on uninitialised repo: err = %v, want errors.Is(err, ErrNotInitialised)", err)
	}
}

// The tests below cover InstallEventHook/EventHookInstalled/RemoveEventHook
// — the generalisation InstallStopHook and StopHookInstalled are now thin
// wrappers around (see internal/nudge's SessionStart hook, a different
// event and a different settings file from the project-level Stop hook the
// tests above exercise). They deliberately point settingsPath at a bare
// file, not a repo's .claude/settings.json, to prove the machinery no
// longer assumes a repoRoot at all.

func TestInstallEventHook_FreshFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	res, err := InstallEventHook(settingsPath, "SessionStart", "nudge", testBin)
	if err != nil {
		t.Fatalf("InstallEventHook: %v", err)
	}
	if !res.Added || res.AlreadyPresent {
		t.Fatalf("res = %+v, want Added=true AlreadyPresent=false", res)
	}

	installed, err := EventHookInstalled(settingsPath, "SessionStart", "nudge")
	if err != nil {
		t.Fatalf("EventHookInstalled: %v", err)
	}
	if !installed {
		t.Errorf("EventHookInstalled = false after install, want true")
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "nudge") {
		t.Errorf("settings.json doesn't mention the nudge command:\n%s", data)
	}
}

func TestInstallEventHook_PreservesUnrelatedKeysAndEvents(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	original := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "some-other-tool notify"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "/usr/local/bin/scribe hook"}]}
    ]
  },
  "model": "sonnet"
}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallEventHook(settingsPath, "SessionStart", "nudge", testBin); err != nil {
		t.Fatalf("InstallEventHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json is not valid JSON after install: %v\n%s", err, data)
	}
	if parsed["model"] != "sonnet" {
		t.Errorf("model key lost: got %v", parsed["model"])
	}
	if !strings.Contains(string(data), "some-other-tool notify") {
		t.Errorf("existing unrelated SessionStart hook lost:\n%s", data)
	}
	if !strings.Contains(string(data), "/usr/local/bin/scribe hook") {
		t.Errorf("existing Stop hook lost:\n%s", data)
	}
	if !strings.Contains(string(data), "nudge") {
		t.Errorf("new nudge hook missing:\n%s", data)
	}
}

func TestInstallEventHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if _, err := InstallEventHook(settingsPath, "SessionStart", "nudge", testBin); err != nil {
		t.Fatalf("first InstallEventHook: %v", err)
	}
	second, err := InstallEventHook(settingsPath, "SessionStart", "nudge", testBin)
	if err != nil {
		t.Fatalf("second InstallEventHook: %v", err)
	}
	if second.Added || !second.AlreadyPresent {
		t.Errorf("second install: Added=%v AlreadyPresent=%v, want Added=false AlreadyPresent=true", second.Added, second.AlreadyPresent)
	}
}

func TestInstallEventHook_TakesBackupOfExistingFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"model":"sonnet"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := InstallEventHook(settingsPath, "SessionStart", "nudge", testBin)
	if err != nil {
		t.Fatalf("InstallEventHook: %v", err)
	}
	if res.Backup == "" {
		t.Fatal("Backup is empty, want a backup of the pre-existing file")
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
}

func TestRemoveEventHook_RemovesOnlyOwnHook(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	original := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [
        {"type": "command", "command": "some-other-tool notify"},
        {"type": "command", "command": "/opt/other/path/scribe nudge"}
      ]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RemoveEventHook(settingsPath, "SessionStart", "nudge")
	if err != nil {
		t.Fatalf("RemoveEventHook: %v", err)
	}
	if !res.Removed {
		t.Fatalf("res.Removed = false, want true")
	}
	if res.Backup == "" {
		t.Errorf("Backup is empty, want a backup taken before the removal write")
	}

	installed, err := EventHookInstalled(settingsPath, "SessionStart", "nudge")
	if err != nil {
		t.Fatalf("EventHookInstalled: %v", err)
	}
	if installed {
		t.Errorf("EventHookInstalled = true after removal, want false")
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "some-other-tool notify") {
		t.Errorf("unrelated SessionStart hook lost during removal:\n%s", data)
	}
}

func TestRemoveEventHook_AbsentHookIsNoopNotError(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// No settings file at all.
	res, err := RemoveEventHook(settingsPath, "SessionStart", "nudge")
	if err != nil {
		t.Fatalf("RemoveEventHook on missing file: %v", err)
	}
	if res.Removed || res.Backup != "" {
		t.Errorf("res = %+v, want Removed=false and no backup for a missing file", res)
	}

	// File exists but has no scribe nudge hook to remove.
	if err := os.WriteFile(settingsPath, []byte(`{"model":"sonnet"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = RemoveEventHook(settingsPath, "SessionStart", "nudge")
	if err != nil {
		t.Fatalf("RemoveEventHook with nothing to remove: %v", err)
	}
	if res.Removed || res.Backup != "" {
		t.Errorf("res = %+v, want Removed=false and no backup when there's nothing to remove", res)
	}

	// Nothing was written — the file must be untouched.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"model":"sonnet"}` {
		t.Errorf("file modified despite nothing to remove: %s", data)
	}
}

func TestRemoveEventHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if _, err := InstallEventHook(settingsPath, "SessionStart", "nudge", testBin); err != nil {
		t.Fatalf("InstallEventHook: %v", err)
	}

	first, err := RemoveEventHook(settingsPath, "SessionStart", "nudge")
	if err != nil || !first.Removed {
		t.Fatalf("first RemoveEventHook: res=%+v err=%v", first, err)
	}
	second, err := RemoveEventHook(settingsPath, "SessionStart", "nudge")
	if err != nil {
		t.Fatalf("second RemoveEventHook: %v", err)
	}
	if second.Removed {
		t.Errorf("second RemoveEventHook: Removed = true, want false (nothing left to remove)")
	}
}
