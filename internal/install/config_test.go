package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestWriteReadConfig_RoundTrip(t *testing.T) {
	repo := t.TempDir()
	cfg := Config{
		Agent:   "opencode",
		Model:   "longcat-2.0-free",
		DocsDir: "docs/scribe",
		Enabled: true,
	}

	if err := WriteConfig(repo, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got, err := ReadConfig(repo)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if got != cfg {
		t.Errorf("ReadConfig = %+v, want %+v", got, cfg)
	}
}

func TestWriteConfig_DefaultsDocsDir(t *testing.T) {
	repo := t.TempDir()
	cfg := Config{Agent: "opencode", Model: "m", Enabled: true} // DocsDir left empty

	if err := WriteConfig(repo, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got, err := ReadConfig(repo)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if got.DocsDir != scribe.DocsDir {
		t.Errorf("DocsDir = %q, want default %q", got.DocsDir, scribe.DocsDir)
	}
}

func TestReadConfig_NotInitialised(t *testing.T) {
	repo := t.TempDir()
	_, err := ReadConfig(repo)
	if !errors.Is(err, ErrNotInitialised) {
		t.Errorf("ReadConfig on fresh repo: err = %v, want ErrNotInitialised", err)
	}
}

func TestReadConfig_Malformed(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, scribe.StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadConfig(repo)
	if err == nil {
		t.Fatalf("ReadConfig on malformed config: got nil error, want error")
	}
	if errors.Is(err, ErrNotInitialised) {
		t.Errorf("malformed config should not report as ErrNotInitialised")
	}
}

func TestWriteConfig_Idempotent(t *testing.T) {
	repo := t.TempDir()
	cfg := Config{Agent: "opencode", Model: "m1", Enabled: true}
	if err := WriteConfig(repo, cfg); err != nil {
		t.Fatalf("first WriteConfig: %v", err)
	}
	cfg.Model = "m2"
	if err := WriteConfig(repo, cfg); err != nil {
		t.Fatalf("second WriteConfig: %v", err)
	}

	got, err := ReadConfig(repo)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if got.Model != "m2" {
		t.Errorf("Model = %q, want %q (second write should win)", got.Model, "m2")
	}
}

func TestWriteConfig_AtomicNoTempFilesLeftBehind(t *testing.T) {
	repo := t.TempDir()
	if err := WriteConfig(repo, Config{Agent: "a", Model: "m", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	dir := filepath.Join(repo, scribe.StateDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != configFileName {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("dir contents = %v, want exactly [%q]", names, configFileName)
	}
}

func TestReadConfig_EmptyRepoRoot(t *testing.T) {
	if _, err := ReadConfig(""); err == nil {
		t.Errorf("ReadConfig(\"\"): got nil error, want error")
	}
}

func TestWriteConfig_EmptyRepoRoot(t *testing.T) {
	if err := WriteConfig("", Config{}); err == nil {
		t.Errorf("WriteConfig(\"\", ...): got nil error, want error")
	}
}

func TestConfigJSONFieldNames(t *testing.T) {
	cfg := Config{Agent: "opencode", Model: "m", DocsDir: "d", Enabled: true}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"agent", "model", "docsDir", "enabled"} {
		if _, ok := m[key]; !ok {
			t.Errorf("marshaled Config missing key %q: %s", key, data)
		}
	}
}
