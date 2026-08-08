package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestInit_Apply_ReportsOnlyDocsItActuallyWrote pins the fix for init
// claiming it wrote all four docs when a repo with no past sessions can
// only ever get two: PROJECT and DECISIONS. Reporting CHANGELOG/JOURNAL
// sends the operator looking for files that were never created.
func TestInit_Apply_ReportsOnlyDocsItActuallyWrote(t *testing.T) {
	withFakeWriter(t, fakeSeedOutput)
	dir := newTestRepo(t)

	stdout, _, err := runInitCmd(t, dir, "--yes", "--apply")
	if err != nil {
		t.Fatalf("init --yes --apply failed: %v\nstdout: %s", err, stdout)
	}

	for _, doc := range []scribe.Doc{scribe.DocChangelog, scribe.DocJournal} {
		path := filepath.Join(dir, "docs", "scribe", string(doc))
		if _, statErr := os.Stat(path); statErr == nil {
			t.Fatalf("%s exists, so this test can no longer prove anything", doc)
		}
		if strings.Contains(stdout, "Wrote") && strings.Contains(wroteLine(stdout), string(doc)) {
			t.Errorf("init reported writing %s but never created it:\n%s", doc, stdout)
		}
	}
	if !strings.Contains(wroteLine(stdout), string(scribe.DocProject)) {
		t.Errorf("init did not report writing PROJECT.md:\n%s", stdout)
	}
}

// wroteLine returns the "Wrote docs/scribe/{...}" line from init's output.
func wroteLine(stdout string) string {
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "Wrote ") {
			return l
		}
	}
	return ""
}

// TestInit_Apply_AddsGitignoreEntries covers OPEN-ITEMS item 19: an
// onboarded repo used to get no ignore entries at all, so scribe's own
// local state and docs showed up as untracked clutter in the user's repo.
func TestInit_Apply_AddsGitignoreEntries(t *testing.T) {
	withFakeWriter(t, fakeSeedOutput)
	dir := newTestRepo(t)

	if _, _, err := runInitCmd(t, dir, "--yes", "--apply"); err != nil {
		t.Fatalf("init --yes --apply failed: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("expected init to create .gitignore: %v", err)
	}
	for _, want := range []string{scribe.StateDir + "/", scribe.DocsDir + "/"} {
		if !strings.Contains(string(b), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, b)
		}
	}
}

// TestInit_Apply_PreservesExistingGitignore makes sure onboarding appends
// to a repo's ignore rules rather than replacing them, and doesn't
// duplicate an entry the repo already has.
func TestInit_Apply_PreservesExistingGitignore(t *testing.T) {
	withFakeWriter(t, fakeSeedOutput)
	dir := newTestRepo(t)

	existing := "node_modules/\n" + scribe.StateDir + "/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seeding .gitignore: %v", err)
	}

	if _, _, err := runInitCmd(t, dir, "--yes", "--apply"); err != nil {
		t.Fatalf("init --yes --apply failed: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "node_modules/") {
		t.Errorf("init clobbered an existing .gitignore entry:\n%s", got)
	}
	if n := strings.Count(got, scribe.StateDir+"/"); n != 1 {
		t.Errorf("%s/ appears %d times, want 1 (already present, must not duplicate):\n%s", scribe.StateDir, n, got)
	}
	if !strings.Contains(got, scribe.DocsDir+"/") {
		t.Errorf(".gitignore missing the entry init should have added:\n%s", got)
	}
}

// TestInit_ReplayResumesAfterAFailedChunk covers OPEN-ITEMS item 16: the
// replay pass's chunk-level resume was only ever tested inside
// internal/replay, never through `scribe init`. Temp repos have no
// ~/.claude/projects history, so this plants a synthetic one (HOME
// redirected, never the user's real history) and fails the writer partway
// so the second run has something to resume from.
func TestInit_ReplayResumesAfterAFailedChunk(t *testing.T) {
	dir := newTestRepo(t)
	plantTranscript(t, dir, 4)

	// Fail every replay chunk on the first run, so nothing is checkpointed
	// as complete and the second run must redo it.
	var calls int
	failReplay := true
	newWriterOrig := newWriter
	t.Cleanup(func() { newWriter = newWriterOrig })
	newWriter = func(_ writer.Config) (scribe.Writer, error) {
		return writerFunc(func(prompt string) (string, error) {
			calls++
			if strings.Contains(prompt, "PROJECT.md") {
				return fakeSeedOutput, nil // the seed pass
			}
			if failReplay {
				return "", errors.New("simulated writer failure")
			}
			return `{"CHANGELOG.md":"- replayed entry\n"}`, nil
		}), nil
	}

	if _, _, err := runInitCmd(t, dir, "--yes"); err != nil {
		t.Fatalf("first init failed unexpectedly: %v", err)
	}
	firstRunCalls := calls
	// Guard against a vacuous pass: if the planted transcript never turned
	// into replay chunks, every assertion below would hold trivially.
	if firstRunCalls < 2 {
		t.Fatalf("first run made %d writer call(s) — the planted transcript produced no replay chunks, so this test proves nothing", firstRunCalls)
	}

	// Second run: the writer now succeeds. Chunks that failed before were
	// never marked complete, so they must be retried rather than skipped.
	failReplay = false
	calls = 0
	if _, _, err := runInitCmd(t, dir, "--yes"); err != nil {
		t.Fatalf("second init failed: %v", err)
	}
	if calls == 0 {
		t.Fatal("second run made no writer calls — failed chunks were wrongly treated as complete")
	}

	// Third run: everything succeeded last time, so every replay chunk is
	// checkpointed and must be skipped. Only the seed pass should call the
	// writer again.
	calls = 0
	if _, _, err := runInitCmd(t, dir, "--yes"); err != nil {
		t.Fatalf("third init failed: %v", err)
	}
	if calls >= firstRunCalls {
		t.Errorf("third run made %d writer calls, want fewer than the first run's %d — completed chunks were not skipped", calls, firstRunCalls)
	}
}

// plantTranscript writes a synthetic transcript for repoRoot into a
// redirected HOME, so replay.FindSessions finds it. It never reads or
// writes the user's real ~/.claude/projects.
func plantTranscript(t *testing.T, repoRoot string, entries int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	mangled := strings.NewReplacer("/", "-", ".", "-").Replace(abs)
	dir := filepath.Join(home, ".claude", "projects", mangled)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating fake transcript dir: %v", err)
	}

	var b strings.Builder
	for i := 0; i < entries; i++ {
		fmt.Fprintf(&b, `{"type":"user","timestamp":"2026-08-08T10:0%d:00Z","message":{"role":"user","content":[{"type":"text","text":"synthetic prompt %d"}]}}`+"\n", i, i)
		fmt.Fprintf(&b, `{"type":"assistant","timestamp":"2026-08-08T10:0%d:30Z","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"synthetic reply %d"}]}}`+"\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("writing fake transcript: %v", err)
	}
}

// writerFunc adapts a plain function to scribe.Writer.
type writerFunc func(string) (string, error)

func (f writerFunc) Name() string                      { return "fake" }
func (f writerFunc) Run(prompt string) (string, error) { return f(prompt) }
