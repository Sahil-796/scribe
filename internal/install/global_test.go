package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// rm builds a map[string]json.RawMessage from plain Go values, so tests can
// write "agent": "opencode" instead of hand-quoting JSON strings.
func rm(t *testing.T, fields map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", k, err)
		}
		out[k] = b
	}
	return out
}

func TestMergeConfigFields_RepoOnly(t *testing.T) {
	repo := rm(t, map[string]any{"agent": "opencode"})
	got := MergeConfigFields(nil, repo)
	if string(got["agent"]) != `"opencode"` {
		t.Fatalf("agent = %s, want \"opencode\"", got["agent"])
	}
}

func TestMergeConfigFields_GlobalOnly(t *testing.T) {
	global := rm(t, map[string]any{"model": "opencode/longcat-2.0-free"})
	got := MergeConfigFields(global, nil)
	if string(got["model"]) != `"opencode/longcat-2.0-free"` {
		t.Fatalf("model = %s, want the global value", got["model"])
	}
}

// TestMergeConfigFields_RepoWinsPerField is the exact scenario the phase 04
// spec calls out by name: a repo config that only set agent must still
// inherit model from the global layer, and must not inherit an agent it
// set for itself.
func TestMergeConfigFields_RepoWinsPerField(t *testing.T) {
	global := rm(t, map[string]any{
		"agent": "claude",
		"model": "opencode/longcat-2.0-free",
	})
	repo := rm(t, map[string]any{
		"agent": "opencode",
	})

	got := MergeConfigFields(global, repo)

	if string(got["agent"]) != `"opencode"` {
		t.Errorf("agent = %s, want repo's \"opencode\" to win", got["agent"])
	}
	if string(got["model"]) != `"opencode/longcat-2.0-free"` {
		t.Errorf("model = %s, want global's value to fall through", got["model"])
	}
}

// TestMergeConfigFields_ExplicitZeroValueWins is the case a naive
// struct-level merge (comparing against Go zero values) gets wrong: a repo
// config that explicitly sets enabled=false must not have a global
// enabled=true leak through, because the two maps only ever compare key
// *presence*, never the decoded value.
func TestMergeConfigFields_ExplicitZeroValueWins(t *testing.T) {
	global := rm(t, map[string]any{"enabled": true})
	repo := rm(t, map[string]any{"enabled": false})

	got := MergeConfigFields(global, repo)

	if string(got["enabled"]) != "false" {
		t.Fatalf("enabled = %s, want repo's explicit false to win over global's true", got["enabled"])
	}
}

func TestMergeConfigFields_DoesNotMutateInputs(t *testing.T) {
	global := rm(t, map[string]any{"agent": "claude"})
	repo := rm(t, map[string]any{"model": "x"})

	_ = MergeConfigFields(global, repo)

	if len(global) != 1 || len(repo) != 1 {
		t.Fatalf("MergeConfigFields mutated an input map: global=%v repo=%v", global, repo)
	}
}

// withGlobalConfigDir points XDG_CONFIG_HOME at a fresh t.TempDir() for the
// duration of the test, so GlobalConfigDir (and everything built on it)
// never touches the developer's real ~/.config/scribe. Every test in this
// file that reads or writes global config uses this rather than the real
// environment — see the phase 04 spec's safety rule.
func withGlobalConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "scribe")
}

func TestGlobalConfigDir_HonoursXDG(t *testing.T) {
	want := withGlobalConfigDir(t)
	got, err := GlobalConfigDir()
	if err != nil {
		t.Fatalf("GlobalConfigDir: %v", err)
	}
	if got != want {
		t.Fatalf("GlobalConfigDir = %q, want %q", got, want)
	}
}

func TestLayeredConfig_MissingGlobalFileIsNotAnError(t *testing.T) {
	withGlobalConfigDir(t) // XDG dir exists, but no config.json inside it
	repo := t.TempDir()

	if err := WriteConfig(repo, Config{Agent: "opencode", Model: "m1", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	cfg, err := LayeredConfig(repo)
	if err != nil {
		t.Fatalf("LayeredConfig: %v", err)
	}
	if cfg.Agent != "opencode" || cfg.Model != "m1" {
		t.Fatalf("cfg = %+v, want the repo's own values unchanged", cfg)
	}
}

func TestLayeredConfig_RepoNotInitialised(t *testing.T) {
	withGlobalConfigDir(t)
	repo := t.TempDir() // no .scribe/config.json

	_, err := LayeredConfig(repo)
	if err == nil {
		t.Fatal("LayeredConfig: want ErrNotInitialised for an uninitialised repo, got nil error")
	}
	if !isNotInitialised(err) {
		t.Fatalf("LayeredConfig error = %v, want ErrNotInitialised", err)
	}
}

func TestLayeredConfig_GlobalFillsWhatRepoLeftUnset(t *testing.T) {
	globalDir := withGlobalConfigDir(t)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	globalJSON := `{"model":"opencode/longcat-2.0-free","code":{"weight":"full"}}`
	if err := os.WriteFile(filepath.Join(globalDir, "config.json"), []byte(globalJSON), 0o644); err != nil {
		t.Fatalf("writing global config: %v", err)
	}

	repo := t.TempDir()
	// Write the repo config directly (bypassing WriteConfig's own
	// defaulting) so it genuinely omits "model" and "code" — proving the
	// merge, not WriteConfig's unrelated default-filling.
	repoDir := filepath.Join(repo, ".scribe")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir .scribe: %v", err)
	}
	repoJSON := `{"agent":"opencode","enabled":true}`
	if err := os.WriteFile(filepath.Join(repoDir, "config.json"), []byte(repoJSON), 0o644); err != nil {
		t.Fatalf("writing repo config: %v", err)
	}

	cfg, err := LayeredConfig(repo)
	if err != nil {
		t.Fatalf("LayeredConfig: %v", err)
	}
	if cfg.Agent != "opencode" {
		t.Errorf("Agent = %q, want the repo's own \"opencode\"", cfg.Agent)
	}
	if cfg.Model != "opencode/longcat-2.0-free" {
		t.Errorf("Model = %q, want the global default to fill through", cfg.Model)
	}
	if cfg.Code.Weight != "full" {
		t.Errorf("Code.Weight = %q, want the global default \"full\" to fill through", cfg.Code.Weight)
	}
}

func TestLayeredConfig_MalformedGlobalFileFailsLoudly(t *testing.T) {
	globalDir := withGlobalConfigDir(t)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("writing malformed global config: %v", err)
	}

	repo := t.TempDir()
	if err := WriteConfig(repo, Config{Agent: "opencode", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	if _, err := LayeredConfig(repo); err == nil {
		t.Fatal("LayeredConfig: want an error for malformed global JSON, got nil — a broken global file must never be silently ignored")
	}
}

func isNotInitialised(err error) bool {
	return errors.Is(err, ErrNotInitialised)
}
