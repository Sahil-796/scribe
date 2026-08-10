package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	// Consumed by phase 06, which doesn't exist yet — so init no longer asks
	// for this (OPEN-ITEMS item 31, following item 17's precedent) and
	// always writes the default, "per-session". The field stays so phase 06
	// has somewhere to put the real answer once its semantics exist; an
	// answer recorded against semantics nobody has written down yet would be
	// silently wrong rather than absent, which is worse than not asking.
	Layout string `json:"layout"`

	// Code controls how much the writer may lean on reading the repo.
	Code CodeConfig `json:"code"`

	// Privacy controls what is stripped before anything leaves the machine.
	Privacy PrivacyConfig `json:"privacy"`

	// Pause is nil when scribe is running normally here. Non-nil records a
	// `scribe off` that may or may not still be in effect — ask IsPaused,
	// never the field, since a dated pause expires on its own.
	Pause *Pause `json:"pause,omitempty"`
}

// CodeConfig is docs/PLAN.md's [code] table. PLAN lists two keys, `read` and
// `weight`; this collapses them into one, because they are not independent —
// `read = false` and `weight = "off"` say the same thing, and every other
// combination of the two is a state with no meaning ("don't read the code, but
// source doc content from it"). One field cannot express the contradiction.
type CodeConfig struct {
	// Weight is worker.CodeWeight as a plain string, kept as a string here
	// so this package stays free of a dependency on internal/worker:
	// "check" (verify claims only — the default), "full" (may source doc
	// content from the code), or "off" (no repo access at all).
	Weight string `json:"weight"`
}

// PrivacyConfig is docs/PLAN.md's [privacy] table: what never leaves the
// machine. Transcripts hold whatever was on screen, and a hosted model means
// all of it goes to whoever runs that model.
type PrivacyConfig struct {
	// Redact lists key names whose values are stripped from anything sent
	// to a writer. Matching is the redactor's business, not a literal
	// substring test — see internal/redact.
	Redact []string `json:"redact"`

	// Ignore lists globs of files the writer's repo access never reads.
	Ignore []string `json:"ignore"`
}

// Pause records a `scribe off`. Kept as its own type, separate from Enabled,
// because onboarding and pausing are different questions: Enabled means this
// repo was ever set up, Pause means it is set up and quiet right now. Folding
// them together would make `scribe off` un-onboard a repo and `scribe on`
// re-run install, which is exactly what docs/PLAN.md's "pause without
// uninstalling" rules out.
type Pause struct {
	// Since is when the pause was requested, for `scribe status` to report.
	Since time.Time `json:"since"`

	// Until is when the pause lapses on its own. Nil means it does not.
	// Default is the end of the local day: a permanent pause you forget
	// about fails the same way forgetting to turn scribe on does.
	Until *time.Time `json:"until,omitempty"`

	// Stay is `scribe off --stay` — an indefinite pause, deliberately
	// chosen. Recorded as its own field rather than implied by a nil Until
	// so status can tell "paused indefinitely, on purpose" apart from a
	// malformed record with no expiry.
	Stay bool `json:"stay,omitempty"`
}

// IsPaused reports whether scribe is paused for this repo as of now. Expiry is
// evaluated on read: there is no timer, no daemon and nothing to wake up, so a
// pause that lapsed while the machine was asleep is simply over the next time
// anyone asks.
func (c Config) IsPaused(now time.Time) bool {
	p := c.Pause
	switch {
	case p == nil:
		return false
	case p.Stay:
		return true
	case p.Until != nil:
		return now.Before(*p.Until)
	default:
		// A pause record with neither an expiry nor --stay is malformed.
		// Treated as over rather than as forever: the failure mode of
		// guessing "forever" is scribe silently never running again, which
		// is the one outcome docs/PLAN.md's expiry rule exists to prevent.
		return false
	}
}

// EndOfLocalDay returns the instant `scribe off` expires by default: the last
// moment of now's day, in the machine's local zone. Local, not UTC — "end of
// day" is a human unit, and a user in UTC+13 pausing after lunch would
// otherwise find the pause already expired.
func EndOfLocalDay(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 23, 59, 59, int(time.Second-time.Nanosecond), now.Location())
}

// CodeWeight values recorded in CodeConfig.Weight. They mirror
// internal/worker's CodeWeight constants; see CodeConfig for why they are
// plain strings here.
const (
	CodeWeightCheck = "check"
	CodeWeightFull  = "full"
	CodeWeightOff   = "off"
)

// DefaultRedactKeys and DefaultIgnoreGlobs are docs/PLAN.md's [privacy]
// example values, promoted to actual defaults. They are applied by ReadConfig
// when the keys are absent — which is the case for every repo onboarded before
// phase 04 — so an old config means "the defaults", never "no redaction at
// all". The fail-safe direction matters more here than anywhere else in the
// codebase: the cost of redacting something harmless is a slightly worse
// journal entry, and the cost of the reverse is a secret in someone else's
// logs, permanently.
var (
	DefaultRedactKeys  = []string{"api_key", "token", "password", "secret"}
	DefaultIgnoreGlobs = []string{"**/.env*", "**/secrets/**"}
)

// withDefaults fills in the phase-04 fields a config may legitimately lack:
// either because it was written before phase 04 existed, or because a caller
// built a Config literal and only set what it cared about.
func withDefaults(cfg Config) Config {
	if cfg.DocsDir == "" {
		cfg.DocsDir = scribe.DocsDir
	}
	if cfg.Code.Weight == "" {
		cfg.Code.Weight = CodeWeightCheck
	}
	if cfg.Privacy.Redact == nil {
		cfg.Privacy.Redact = append([]string(nil), DefaultRedactKeys...)
	}
	if cfg.Privacy.Ignore == nil {
		cfg.Privacy.Ignore = append([]string(nil), DefaultIgnoreGlobs...)
	}
	return cfg
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
	cfg = withDefaults(cfg)

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
	// Defaulted on the way out as well as the way in: a config written
	// before phase 04 has no code or privacy keys at all, and must read
	// back as the defaults rather than as an empty redaction list.
	return withDefaults(cfg), nil
}
