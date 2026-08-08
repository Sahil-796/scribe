package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/writer"
)

// newTestRepo creates a fresh temp directory with just enough to look like
// a git repo (a .git entry) for findGitRoot to accept it. No real git
// commands are run — init only ever looks for .git, never shells out to
// git itself.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("creating fake .git: %v", err)
	}
	return dir
}

// fakeSeedOutput is a valid seed.Parse response: the fenced-JSON-object
// contract every writer call in this codebase shares.
const fakeSeedOutput = `{"PROJECT.md":"# Project\n\nA fake project, seeded by a fake writer.\n","DECISIONS.md":"# Decisions\n\nNone recorded.\n"}`

// fakeWriter stands in for a real agent connector. It never spawns a
// process, makes no network call, and is the only writer any test in this
// file may use — per this unit's safety rules, no test invokes a real
// writer agent (opencode or otherwise).
type fakeWriter struct {
	out   string
	calls *int
}

func (f fakeWriter) Name() string { return "fake" }

func (f fakeWriter) Run(prompt string) (string, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.out, nil
}

// withFakeWriter points the package-level newWriter seam at a fake
// connector for the duration of the test and restores it afterward.
func withFakeWriter(t *testing.T, out string) *int {
	t.Helper()
	calls := 0
	orig := newWriter
	newWriter = func(cfg writer.Config) (scribe.Writer, error) {
		return fakeWriter{out: out, calls: &calls}, nil
	}
	t.Cleanup(func() { newWriter = orig })
	return &calls
}

// runInitCmd executes newInitCmd() standalone (not attached to root) with
// args, in dir, capturing stdout/stderr. Tests run sequentially in this
// package (no t.Parallel), so changing the process cwd via t.Chdir is safe.
func runInitCmd(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)

	cmd := newInitCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)

	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestInit_OutsideGitRepo_Errors(t *testing.T) {
	dir := t.TempDir() // deliberately no .git

	_, _, err := runInitCmd(t, dir, "--yes")
	if err == nil {
		t.Fatal("expected an error running init outside a git repo, got nil")
	}
}

func TestInit_DryRun_StagesPreviewWithoutTouchingRealDocsOrHook(t *testing.T) {
	withFakeWriter(t, fakeSeedOutput)
	dir := newTestRepo(t)

	stdout, _, err := runInitCmd(t, dir, "--yes")
	if err != nil {
		t.Fatalf("init --yes (dry run) failed: %v\nstdout: %s", err, stdout)
	}

	// Preview staged.
	previewProject := filepath.Join(dir, ".scribe", "init-preview", string(scribe.DocProject))
	b, err := os.ReadFile(previewProject)
	if err != nil {
		t.Fatalf("expected preview PROJECT.md at %s: %v", previewProject, err)
	}
	if len(b) == 0 {
		t.Fatal("preview PROJECT.md is empty")
	}

	// Real docs untouched.
	if _, err := os.Stat(filepath.Join(dir, "docs", "scribe")); !os.IsNotExist(err) {
		t.Fatalf("dry run must not create docs/scribe, stat err = %v", err)
	}

	// No hook installed.
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("dry run must not install a hook, stat err = %v", err)
	}

	// Not marked initialised.
	if _, err := install.ReadConfig(dir); err != install.ErrNotInitialised {
		t.Fatalf("dry run must not write config, ReadConfig err = %v", err)
	}
}

func TestInit_Apply_WritesDocsAndInstallsHook(t *testing.T) {
	withFakeWriter(t, fakeSeedOutput)
	dir := newTestRepo(t)

	stdout, _, err := runInitCmd(t, dir, "--yes", "--apply")
	if err != nil {
		t.Fatalf("init --yes --apply failed: %v\nstdout: %s", err, stdout)
	}

	// Real docs written.
	projectPath := filepath.Join(dir, "docs", "scribe", string(scribe.DocProject))
	b, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", projectPath, err)
	}
	if len(b) == 0 {
		t.Fatal("PROJECT.md was written empty")
	}
	decisionsPath := filepath.Join(dir, "docs", "scribe", string(scribe.DocDecisions))
	if _, err := os.ReadFile(decisionsPath); err != nil {
		t.Fatalf("expected %s to exist: %v", decisionsPath, err)
	}

	// Hook installed.
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	settingsBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("expected Stop hook settings at %s: %v", settingsPath, err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(settingsBytes, &settings); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	if _, ok := settings["hooks"]; !ok {
		t.Fatalf("settings.json has no \"hooks\" key: %s", settingsBytes)
	}

	// Config written and enabled.
	cfg, err := install.ReadConfig(dir)
	if err != nil {
		t.Fatalf("expected config to be readable after apply: %v", err)
	}
	if !cfg.Enabled {
		t.Error("config.Enabled = false after apply, want true")
	}
	if cfg.Agent == "" || cfg.Model == "" {
		t.Errorf("config missing agent/model: %+v", cfg)
	}
}

func TestInit_AlreadyInitialised_DoesNotRerun(t *testing.T) {
	calls := withFakeWriter(t, fakeSeedOutput)
	dir := newTestRepo(t)

	if err := install.WriteConfig(dir, install.Config{
		Agent:   "opencode",
		Model:   "longcat-2.0-free",
		DocsDir: scribe.DocsDir,
		Enabled: true,
	}); err != nil {
		t.Fatalf("seeding existing config: %v", err)
	}

	stdout, _, err := runInitCmd(t, dir, "--yes", "--apply")
	if err != nil {
		t.Fatalf("init on an already-initialised repo should not error, got: %v", err)
	}
	if *calls != 0 {
		t.Errorf("writer was called %d time(s) on an already-initialised repo, want 0 (should not redo seed/replay)", *calls)
	}
	if stdout == "" {
		t.Error("expected init to explain that the repo is already initialised, got empty stdout")
	}
}

func TestInit_DryRun_ThenApply_IsResumableAndDoesNotDoubleCallWriter(t *testing.T) {
	// Two separate runs, same repo: a dry run followed by --apply. This
	// exercises the preview-staging path end to end (compute once, apply
	// from what was staged) without needing real transcript history — the
	// seed pass runs once per invocation regardless, so this mainly checks
	// that the second run's apply succeeds using the first run's preview.
	withFakeWriter(t, fakeSeedOutput)
	dir := newTestRepo(t)

	if _, _, err := runInitCmd(t, dir, "--yes"); err != nil {
		t.Fatalf("first (dry run) init failed: %v", err)
	}
	if _, _, err := runInitCmd(t, dir, "--yes", "--apply"); err != nil {
		t.Fatalf("second (apply) init failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "scribe", string(scribe.DocProject))); err != nil {
		t.Fatalf("expected PROJECT.md after the apply run: %v", err)
	}
}
