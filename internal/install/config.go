package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// configFileName is where per-repo config lives, under scribe.StateDir
// (.scribe/), e.g. ".scribe/config.json". .scribe/ is gitignored (local
// state — offsets, queue, lock), so this is local-machine config, not a
// team-shared one; see docs/PLAN.md, "Config" for the longer-term TOML
// layering this may grow into.
const configFileName = "config.json"

// ErrNotInitialised is returned by ReadConfig when repoRoot has no scribe
// config yet — i.e. `scribe init` was never run there. Off is the default
// everywhere (docs/PLAN.md, "On and off"), so callers should treat this as
// "scribe is off here," not as a failure.
var ErrNotInitialised = errors.New("install: repo not initialised (run `scribe init`)")

// Config is the per-repo configuration scribe init records.
//
// DocsInGit and Layout exist because both are genuinely per-repo choices
// that scribe has no business making globally (OPEN-ITEMS items 3 and 5): a
// solo repo and a shared one want different answers, and whether generated
// docs belong in git is the owner's call, not the tool's. init asks both at
// onboarding and records the answers here.
type Config struct {
	Agent   string `json:"agent"`   // e.g. "opencode"
	Model   string `json:"model"`   // e.g. "opencode/longcat-2.0-free"
	DocsDir string `json:"docsDir"` // defaults to scribe.DocsDir
	Enabled bool   `json:"enabled"`

	// DocsInGit is true when the docs are committed to the repo, false when
	// init added them to .gitignore instead.
	DocsInGit bool `json:"docsInGit"`

	// Layout is "per-session" (each session writes its own file, no write
	// conflicts) or "shared" (the four docs as single appended files).
	// Consumed by phase 06; recorded from onboarding so the answer exists
	// before the code that reads it does.
	Layout string `json:"layout"`
}

// Layout values recorded in Config.Layout. They mirror internal/wizard's
// Layout constants, kept as plain strings here so this package stays free
// of a dependency on the TUI package.
const (
	LayoutPerSession = "per-session"
	LayoutShared     = "shared"
)

// configPath returns the path WriteConfig/ReadConfig use for repoRoot.
func configPath(repoRoot string) string {
	return filepath.Join(repoRoot, scribe.StateDir, configFileName)
}

// WriteConfig persists cfg for repoRoot, atomically (temp file + rename, see
// atomicWrite in install.go) so a crash mid-write can never leave a
// truncated or half-written config behind. DocsDir is defaulted to
// scribe.DocsDir if the caller left it empty.
func WriteConfig(repoRoot string, cfg Config) error {
	if repoRoot == "" {
		return errors.New("install: repoRoot is empty")
	}
	if cfg.DocsDir == "" {
		cfg.DocsDir = scribe.DocsDir
	}

	path := configPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("install: creating %s: %w", filepath.Dir(path), err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("install: encoding config: %w", err)
	}
	data = append(data, '\n')

	if err := atomicWrite(path, data); err != nil {
		return fmt.Errorf("install: writing %s: %w", path, err)
	}
	return nil
}

// ReadConfig loads repoRoot's config. If repoRoot has never been
// initialised (no .scribe/config.json), it returns ErrNotInitialised — use
// errors.Is to test for it rather than string-matching.
func ReadConfig(repoRoot string) (Config, error) {
	if repoRoot == "" {
		return Config{}, errors.New("install: repoRoot is empty")
	}

	path := configPath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, ErrNotInitialised
		}
		return Config{}, fmt.Errorf("install: reading %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("install: parsing %s: %w", path, err)
	}
	return cfg, nil
}
