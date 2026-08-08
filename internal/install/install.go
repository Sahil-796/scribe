// Package install wires scribe into one repo: it writes the Claude Code
// Stop hook that drives the loop built in phase 01, and it persists the
// per-repo choices `scribe init` collects (docs/PLAN.md, "02 — scribe init").
//
// Off is the default everywhere (docs/PLAN.md, "On and off"). This package
// is the only thing that turns it on — installing the hook and writing the
// config are the two durable side effects of `scribe init`, so both are
// built to be safe to run more than once and safe to run against a real,
// human-maintained settings file.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// settingsRelPath is where Claude Code keeps project-level settings,
// relative to the repo root. This is distinct from .claude/settings.local.json
// (personal, gitignored) — project settings are the ones meant to be shared
// and committed, which is where a hook that makes scribe run for everyone
// on the repo belongs.
const settingsRelPath = ".claude/settings.json"

// stopEventName is the Claude Code hook event scribe installs into. Per the
// Claude Code hooks documentation, Stop does not support a "matcher" field —
// unlike PreToolUse/PostToolUse groups, a Stop group is just {"hooks": [...]}.
const stopEventName = "Stop"

// hookEntry is one command Claude Code runs for a matched hook event. This
// mirrors the subset of the real schema scribe cares about; unrecognized
// sibling fields on hook entries we didn't write are preserved because we
// only ever decode/encode the "Stop" array — every other event's raw JSON
// passes through untouched (see settings.go).
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout *int   `json:"timeout,omitempty"`
}

// hookGroup is one matcher group within an event's array. Stop groups never
// carry a matcher (it would be silently ignored by Claude Code), so this
// omits the field entirely when empty rather than writing `"matcher": ""`.
type hookGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

// HookResult describes what InstallStopHook did, so the caller can report it.
type HookResult struct {
	SettingsPath   string // the file written
	Added          bool   // a new hook entry was added
	AlreadyPresent bool   // an equivalent scribe hook was already installed
	Backup         string // path to the backup taken before writing, if any
}

// InstallStopHook adds a Stop hook invoking scribe to repoRoot's project-level
// Claude Code settings (.claude/settings.json). Idempotent: running it twice
// must not produce two entries. It preserves every other key and every other
// hook already in the file — it decodes only the "Stop" array and leaves
// everything else as raw JSON.
//
// A backup of the pre-existing file is written before any modification, so
// a bad merge is always one file copy away from undone.
func InstallStopHook(repoRoot, scribeBinPath string) (HookResult, error) {
	if repoRoot == "" {
		return HookResult{}, errors.New("install: repoRoot is empty")
	}
	if scribeBinPath == "" {
		return HookResult{}, errors.New("install: scribeBinPath is empty")
	}

	settingsPath := filepath.Join(repoRoot, settingsRelPath)

	doc, existed, err := readSettings(settingsPath)
	if err != nil {
		return HookResult{}, fmt.Errorf("install: reading %s: %w", settingsPath, err)
	}

	stopGroups, err := doc.stopGroups()
	if err != nil {
		return HookResult{}, fmt.Errorf("install: parsing Stop hooks in %s: %w", settingsPath, err)
	}

	for _, g := range stopGroups {
		for _, h := range g.Hooks {
			if isScribeHookCommand(h.Command) {
				return HookResult{
					SettingsPath:   settingsPath,
					Added:          false,
					AlreadyPresent: true,
				}, nil
			}
		}
	}

	stopGroups = append(stopGroups, hookGroup{
		Hooks: []hookEntry{{
			Type:    "command",
			Command: hookCommand(scribeBinPath),
		}},
	})
	doc.setStopGroups(stopGroups)

	out, err := doc.marshal()
	if err != nil {
		return HookResult{}, fmt.Errorf("install: encoding %s: %w", settingsPath, err)
	}

	var backup string
	if existed {
		backup, err = backupFile(settingsPath)
		if err != nil {
			return HookResult{}, fmt.Errorf("install: backing up %s: %w", settingsPath, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return HookResult{}, fmt.Errorf("install: creating %s: %w", filepath.Dir(settingsPath), err)
	}
	if err := atomicWrite(settingsPath, out); err != nil {
		return HookResult{}, fmt.Errorf("install: writing %s: %w", settingsPath, err)
	}

	return HookResult{
		SettingsPath: settingsPath,
		Added:        true,
		Backup:       backup,
	}, nil
}

// StopHookInstalled reports whether repoRoot already has a scribe Stop hook
// installed in its project-level Claude Code settings.
func StopHookInstalled(repoRoot string) (bool, error) {
	if repoRoot == "" {
		return false, errors.New("install: repoRoot is empty")
	}
	settingsPath := filepath.Join(repoRoot, settingsRelPath)

	doc, existed, err := readSettings(settingsPath)
	if err != nil {
		return false, fmt.Errorf("install: reading %s: %w", settingsPath, err)
	}
	if !existed {
		return false, nil
	}

	stopGroups, err := doc.stopGroups()
	if err != nil {
		return false, fmt.Errorf("install: parsing Stop hooks in %s: %w", settingsPath, err)
	}

	for _, g := range stopGroups {
		for _, h := range g.Hooks {
			if isScribeHookCommand(h.Command) {
				return true, nil
			}
		}
	}
	return false, nil
}

// hookCommand builds the shell command scribe installs as the Stop hook.
// scribeBinPath is quoted so a path containing spaces still works; the
// result is what Claude Code hands to `sh -c`.
func hookCommand(scribeBinPath string) string {
	return fmt.Sprintf("%q hook", scribeBinPath)
}

// isScribeHookCommand reports whether cmd looks like a command this package
// installed: it ends in the "hook" subcommand and the binary invoked is
// (or is named) scribe. This is deliberately loose rather than an exact
// string match against hookCommand's current output, so a scribe binary
// moved to a different path (or invoked via a wrapper) is still recognized
// as "already installed" instead of producing a duplicate entry.
func isScribeHookCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if !strings.HasSuffix(trimmed, "hook") {
		return false
	}
	binPart := strings.TrimSpace(strings.TrimSuffix(trimmed, "hook"))
	binPart = strings.Trim(binPart, `"'`)
	if binPart == "" {
		return false
	}
	// Take the first token in case flags/args were appended by a hand edit.
	fields := strings.Fields(binPart)
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(strings.Trim(fields[0], `"'`))
	return base == "scribe" || strings.HasPrefix(base, "scribe")
}

// backupFile copies path to a sibling file with a timestamped suffix and
// returns its path. Called only when a real settings file is about to be
// modified, so a bad merge is always recoverable with a plain file copy.
func backupFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	backupPath := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102T150405.000000000"))
	if err := atomicWrite(backupPath, data); err != nil {
		return "", err
	}
	return backupPath, nil
}

// atomicWrite writes data to path via temp file + rename, so a reader (or a
// crash) never observes a partially written file. Mirrors the pattern in
// internal/docs (see docs.go's atomicWrite) — duplicated here rather than
// imported so this package doesn't reach into another internal package for
// one helper function.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	succeeded = true
	return nil
}

// settingsDoc holds a parsed settings.json as a generic key/value map plus
// a lazily-decoded view of the "hooks" object, both keyed by raw JSON so
// that every key and every hook event this package doesn't understand
// round-trips unchanged. Only the "hooks"."Stop" array is ever decoded into
// a typed value and re-encoded.
type settingsDoc struct {
	root  map[string]json.RawMessage
	hooks map[string]json.RawMessage // decoded lazily from root["hooks"]
}

// readSettings loads path, returning an empty-but-valid document (existed
// = false) if the file doesn't exist yet — a repo with no .claude/settings.json
// at all is the common case for a fresh `scribe init`. A file that exists
// but isn't valid JSON is a loud error: scribe must never guess at a
// malformed settings file and silently overwrite it.
func readSettings(path string) (settingsDoc, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settingsDoc{root: map[string]json.RawMessage{}}, false, nil
		}
		return settingsDoc{}, false, err
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		// Empty file: treat like "doesn't exist yet" for structure purposes,
		// but existed=true so InstallStopHook still takes a backup — an
		// empty file the user created on purpose shouldn't vanish silently.
		return settingsDoc{root: map[string]json.RawMessage{}}, true, nil
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return settingsDoc{}, false, fmt.Errorf("invalid JSON: %w", err)
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	return settingsDoc{root: root}, true, nil
}

// stopGroups returns the current "hooks"."Stop" array, decoded. Any other
// event under "hooks" (PreToolUse, PostToolUse, ...) is left as raw JSON in
// doc.hooks and is never touched.
func (d *settingsDoc) stopGroups() ([]hookGroup, error) {
	if d.hooks == nil {
		d.hooks = map[string]json.RawMessage{}
		if raw, ok := d.root["hooks"]; ok && len(raw) > 0 {
			if err := json.Unmarshal(raw, &d.hooks); err != nil {
				return nil, fmt.Errorf(`"hooks" is not an object: %w`, err)
			}
		}
	}

	raw, ok := d.hooks[stopEventName]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var groups []hookGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf(`"hooks"."Stop" is not an array of hook groups: %w`, err)
	}
	return groups, nil
}

// setStopGroups stages groups as the new "hooks"."Stop" value. Call
// marshal() afterward to fold it (and every untouched key) back into JSON.
func (d *settingsDoc) setStopGroups(groups []hookGroup) {
	if d.hooks == nil {
		d.hooks = map[string]json.RawMessage{}
	}
	// marshal error is impossible for a []hookGroup of plain strings/ints.
	raw, _ := json.Marshal(groups)
	d.hooks[stopEventName] = raw
}

// marshal folds d.hooks back into d.root (if it was ever touched) and
// serializes the whole document with stable, readable formatting. Every top
// -level key besides "hooks", and every hooks event besides "Stop", is
// whatever raw JSON it was read as — this function never re-derives it.
func (d *settingsDoc) marshal() ([]byte, error) {
	if d.hooks != nil {
		raw, err := json.Marshal(d.hooks)
		if err != nil {
			return nil, err
		}
		d.root["hooks"] = raw
	}
	// encoding/json marshals map[string]json.RawMessage keys in sorted
	// order. That reorders top-level keys relative to whatever order the
	// user's file happened to have them in, but every key and value
	// survives — see the report for why this trade-off was accepted.
	out, err := json.MarshalIndent(d.root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
