package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestWriteReadConfig_RoundTrip(t *testing.T) {
	repo := t.TempDir()
	until := time.Date(2026, 8, 10, 23, 59, 59, 0, time.UTC)
	cfg := Config{
		Agent:   "opencode",
		Model:   "longcat-2.0-free",
		DocsDir: "docs/scribe",
		Enabled: true,
		Code:    CodeConfig{Weight: CodeWeightFull},
		Privacy: PrivacyConfig{
			Redact: []string{"api_key", "house_key"},
			Ignore: []string{"**/*.pem"},
		},
		Pause: &Pause{Since: until.Add(-time.Hour), Until: &until},
	}

	if err := WriteConfig(repo, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got, err := ReadConfig(repo)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	// DeepEqual, not ==: Config carries slices and a pointer as of phase
	// 04, so it is no longer a comparable type.
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("ReadConfig = %+v, want %+v", got, cfg)
	}
	if got.Pause == nil || !got.Pause.Until.Equal(until) {
		t.Errorf("Pause.Until did not survive the round trip: %+v", got.Pause)
	}
}

// A config written before phase 04 has no "code" or "privacy" keys at all.
// It must read back as the defaults, never as an empty redaction list — the
// fail-safe direction, since the cost of the reverse is a secret in someone
// else's logs.
func TestReadConfig_PrePhase04_GetsPrivacyDefaults(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, scribe.StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"agent":"opencode","model":"m","docsDir":"docs/scribe","enabled":true,"docsInGit":false,"layout":"per-session"}`
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadConfig(repo)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !reflect.DeepEqual(got.Privacy.Redact, DefaultRedactKeys) {
		t.Errorf("Redact = %v, want defaults %v", got.Privacy.Redact, DefaultRedactKeys)
	}
	if !reflect.DeepEqual(got.Privacy.Ignore, DefaultIgnoreGlobs) {
		t.Errorf("Ignore = %v, want defaults %v", got.Privacy.Ignore, DefaultIgnoreGlobs)
	}
	if got.Code.Weight != CodeWeightCheck {
		t.Errorf("Code.Weight = %q, want default %q", got.Code.Weight, CodeWeightCheck)
	}
	if got.IsPaused(time.Now()) {
		t.Errorf("a config with no pause record must not read as paused")
	}
}

// The defaults are handed out as copies. A caller mutating what it got back
// must not be able to reach into the package-level default and change what
// every later repo gets.
func TestReadConfig_DefaultsAreCopies(t *testing.T) {
	repo := t.TempDir()
	if err := WriteConfig(repo, Config{Agent: "opencode", Model: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadConfig(repo)
	if err != nil {
		t.Fatal(err)
	}
	got.Privacy.Redact[0] = "clobbered"
	if DefaultRedactKeys[0] == "clobbered" {
		t.Fatal("mutating a returned config clobbered DefaultRedactKeys")
	}
}

func TestConfig_IsPaused(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	cases := []struct {
		name  string
		pause *Pause
		want  bool
	}{
		{"no record", nil, false},
		{"expires later today", &Pause{Since: past, Until: &future}, true},
		{"already lapsed", &Pause{Since: past.Add(-time.Hour), Until: &past}, false},
		{"indefinite", &Pause{Since: past, Stay: true}, true},
		{"indefinite beats a lapsed expiry", &Pause{Since: past, Until: &past, Stay: true}, true},
		// Neither expiry nor --stay: malformed. Reads as over, because
		// guessing "forever" means scribe silently never runs again.
		{"malformed, no expiry and no stay", &Pause{Since: past}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Pause: tc.pause}
			if got := cfg.IsPaused(now); got != tc.want {
				t.Errorf("IsPaused = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEndOfLocalDay(t *testing.T) {
	// A zone well east of UTC: "end of day" is a human unit, so pausing
	// after lunch here must not produce an expiry that already passed.
	zone := time.FixedZone("UTC+13", 13*60*60)
	now := time.Date(2026, 8, 10, 13, 30, 0, 0, zone)

	got := EndOfLocalDay(now)
	if !got.After(now) {
		t.Fatalf("EndOfLocalDay(%v) = %v, which is not in the future", now, got)
	}
	if y, m, d := got.Date(); y != 2026 || m != time.August || d != 10 {
		t.Errorf("EndOfLocalDay landed on %v, want the same local day", got)
	}
	if got.Location() != zone {
		t.Errorf("EndOfLocalDay lost the local zone: %v", got.Location())
	}
	if h, min, s := got.Clock(); h != 23 || min != 59 || s != 59 {
		t.Errorf("EndOfLocalDay clock = %02d:%02d:%02d, want 23:59:59", h, min, s)
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
