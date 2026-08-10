package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Global defaults land at ~/.config/scribe/config.json — same shape as the
// per-repo config, same file name, different directory. This is a
// deliberate deviation from docs/PLAN.md's "~/.config/scribe/config.toml":
// see docs/phases/04-config-and-safety.md for why JSON stays JSON rather
// than pulling in a TOML dependency for a second serialisation format
// alongside the one already on disk since phase 02.
//
// GlobalConfigDir is exported so internal/nudge — which needs its own
// small state file (a per-repo session counter, nothing to do with
// scribe's Config shape) — can put it next to config.json rather than
// inventing a second directory convention.
func GlobalConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "scribe"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("install: resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config", "scribe"), nil
}

func globalConfigPath() (string, error) {
	dir, err := GlobalConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// rawFields decodes path as a JSON object into its top-level fields, keyed
// by field name with each value left as raw, unparsed JSON. This is the
// representation MergeConfigFields works over: a map[string]json.RawMessage
// records exactly which fields were present in the source file, which a
// decoded Config struct cannot — an absent "agent" key and an
// explicitly-written `"agent": ""` decode to the same Go zero value once
// unmarshalled into a Config, but they mean opposite things for layering
// ("fall through to the next layer" vs "this layer's answer is empty").
// Staying in raw-JSON-map form until after merging is what keeps that
// distinction alive long enough to matter.
//
// found=false with a nil error means "the file doesn't exist", which is
// the expected, common case for the global config (most machines never
// have one) and is handled by every caller as "no fields from this layer"
// rather than as a failure.
func rawFields(path string) (fields map[string]json.RawMessage, found bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, true, fmt.Errorf("parsing %s: %w", path, err)
	}
	return fields, true, nil
}

// MergeConfigFields layers repo's fields over global's, field by field: any
// top-level key present in repo wins outright, and any key present only in
// global falls through unchanged. Neither map is mutated; the result is a
// new map.
//
// This is the pure core of the global/repo layering — no file I/O, nothing
// that can fail — so the field-by-field behaviour ("a global model must
// still apply to a repo config that only set agent") is directly testable
// against hand-built maps in global_test.go without touching a filesystem.
func MergeConfigFields(global, repo map[string]json.RawMessage) map[string]json.RawMessage {
	merged := make(map[string]json.RawMessage, len(global)+len(repo))
	for k, v := range global {
		merged[k] = v
	}
	for k, v := range repo {
		merged[k] = v
	}
	return merged
}

// LayeredConfig reads repoRoot's config layered over the user's global
// defaults (if any) and returns the result, defaulted the same way
// ReadConfig defaults a plain repo config.
//
// repoRoot having no config at all is still ErrNotInitialised — a global
// config with, say, a preferred agent does not itself turn scribe on for a
// repo that never ran `scribe init`; global config only ever fills in
// fields for a repo config that already exists. A missing *global* file,
// on the other hand, is not an error at all: it's the common case, and
// LayeredConfig treats it exactly like an empty set of global overrides.
func LayeredConfig(repoRoot string) (Config, error) {
	repoFields, repoFound, err := rawFields(configPath(repoRoot))
	if err != nil {
		return Config{}, fmt.Errorf("install: reading repo config: %w", err)
	}
	if !repoFound {
		return Config{}, ErrNotInitialised
	}

	gPath, err := globalConfigPath()
	if err != nil {
		return Config{}, fmt.Errorf("install: %w", err)
	}
	globalFields, _, err := rawFields(gPath)
	if err != nil {
		return Config{}, fmt.Errorf("install: reading global config: %w", err)
	}

	merged := MergeConfigFields(globalFields, repoFields)
	data, err := json.Marshal(merged)
	if err != nil {
		return Config{}, fmt.Errorf("install: re-encoding merged config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("install: parsing merged config: %w", err)
	}
	return withDefaults(cfg), nil
}

// GlobalDefaults returns the user's global config alone, defaulted — what
// applies to a repo that has no config of its own yet.
//
// This is `scribe init`'s case, and it exists specifically for the privacy
// keys. Init runs the seed and replay passes, which together are the
// largest volume of repo and transcript content scribe ever sends anywhere,
// and they run *before* the repo config is written. Falling back to the
// built-in defaults there would silently ignore a user who added, say,
// "customer_id" to their global redact list — on the one pass where it
// would matter most. A missing global file is not an error: the result is
// then simply the built-in defaults.
func GlobalDefaults() (Config, error) {
	gPath, err := globalConfigPath()
	if err != nil {
		return Config{}, fmt.Errorf("install: %w", err)
	}
	globalFields, _, err := rawFields(gPath)
	if err != nil {
		return Config{}, fmt.Errorf("install: reading global config: %w", err)
	}

	data, err := json.Marshal(globalFields)
	if err != nil {
		return Config{}, fmt.Errorf("install: re-encoding global config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("install: parsing global config: %w", err)
	}
	return withDefaults(cfg), nil
}
