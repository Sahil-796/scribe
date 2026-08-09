package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// OPEN-ITEMS item 13: this machine's opencode has permissions pre-opened
// globally by an `oh-my-openagent` plugin config (~/.config/opencode/).
// Any test that spawns a *real* opencode process and lets the approval
// path run through this machine's defaults proves nothing about approval
// behaviour in general — it would pass the same way whether the approval
// logic worked or not, because nothing here is ever actually denied.
// Phase 00 (docs/findings/00-writer.md) worked around this by hand,
// dropping a project-local opencode.json with `"permission": {"bash":
// "ask", "edit": "ask"}` next to the probe before running it. That was a
// one-off file, not a reusable helper — this file makes it structural.
//
// forceOpencodeAskOverride writes that same project-local override into
// repoRoot, so any test built on top of it is forced through opencode's
// actual approval path regardless of what this (or any other) machine's
// global config already allows. Every caller of the real "opencode"
// binary in this unit's test suite must call this first if it wants its
// pass/fail to mean anything about approval behaviour specifically.
//
// As of this pass, grep confirms no test in this repository spawns a real
// opencode process at all — every test that exercises "scribe run" or
// "scribe init" (run_test.go, init_test.go, hook_spawn_test.go) redirects
// the writer to an in-process fake (withFakeRunWriter / withFakeWriter)
// or explicitly suppresses spawning (SCRIBE_NO_SPAWN in
// hook_spawn_test.go), and internal/writer's own tests
// (internal/writer/writer_test.go) shell out to a POSIX stub script, never
// the real opencode binary. So today, nothing in this codebase touches
// the approval path for real, and this machine's pre-opened permissions
// cannot currently make any test pass for the wrong reason. This helper
// exists so that stays true the day someone adds a real-opencode
// integration test: they get the override for free by calling it, instead
// of rediscovering — or forgetting — the phase 00 workaround.
func forceOpencodeAskOverride(t *testing.T, repoRoot string) {
	t.Helper()
	cfg := map[string]any{
		"permission": map[string]string{
			"bash": "ask",
			"edit": "ask",
		},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal opencode ask-override config: %v", err)
	}
	path := filepath.Join(repoRoot, "opencode.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestForceOpencodeAskOverride_WritesExpectedConfig is a test of the guard
// itself, not of opencode: it pins the exact file and content
// forceOpencodeAskOverride produces, matching docs/findings/00-writer.md's
// proven-working `permission: {"bash": "ask", "edit": "ask"}` shape
// exactly, so a future edit to this helper can't silently drift from the
// config that's actually known to force the approval path.
func TestForceOpencodeAskOverride_WritesExpectedConfig(t *testing.T) {
	dir := t.TempDir()
	forceOpencodeAskOverride(t, dir)

	b, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("reading opencode.json: %v", err)
	}

	var got struct {
		Permission struct {
			Bash string `json:"bash"`
			Edit string `json:"edit"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("parsing opencode.json: %v\ncontent: %s", err, b)
	}
	if got.Permission.Bash != "ask" {
		t.Fatalf("permission.bash = %q, want %q", got.Permission.Bash, "ask")
	}
	if got.Permission.Edit != "ask" {
		t.Fatalf("permission.edit = %q, want %q", got.Permission.Edit, "ask")
	}
}
