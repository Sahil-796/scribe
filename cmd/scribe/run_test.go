package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/writer"
)

// fakeRunWriter stands in for a real agent connector, mirroring
// init_test.go's fakeWriter. Never spawns a process, makes no network call
// — per this unit's safety rules, no test invokes a real writer agent.
type fakeRunWriter struct {
	out   string
	calls *int
}

func (f fakeRunWriter) Name() string { return "fake" }

func (f fakeRunWriter) Run(prompt string) (string, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.out, nil
}

// withFakeRunWriter points the package-level newWriterForRun seam at a fake
// connector for the duration of the test and restores it afterward.
func withFakeRunWriter(t *testing.T, out string) *int {
	t.Helper()
	calls := 0
	orig := newWriterForRun
	newWriterForRun = func(cfg writer.Config) (scribe.Writer, error) {
		return fakeRunWriter{out: out, calls: &calls}, nil
	}
	t.Cleanup(func() { newWriterForRun = orig })
	return &calls
}

// runRunCmd executes newRunCmd() standalone (not attached to root) in dir,
// capturing stdout/stderr. Mirrors runInitCmd in init_test.go.
func runRunCmd(t *testing.T, dir string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(dir)

	cmd := newRunCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestRun_WritesSessionIndex proves the phase 05 path end to end at the
// command seam: a run whose summary call returns a valid CATEGORY/SUMMARY
// reply leaves docs/scribe/INDEX.md carrying that session's line. The fake
// returns the same reply for every writer call, which is fine — the doc
// calls treat it as content, and only the summary call is asserted here.
func TestRun_WritesSessionIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("creating fake .git: %v", err)
	}
	if err := install.WriteConfig(dir, install.Config{
		Agent:   "opencode",
		Model:   "opencode/longcat-2.0-free",
		DocsDir: scribe.DocsDir,
		Enabled: true,
	}); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	transcriptLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"did the thing"}]},"timestamp":"2024-01-01T00:00:00Z","isSidechain":false}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcriptLine), 0o644); err != nil {
		t.Fatalf("writing fake transcript: %v", err)
	}
	if err := queue.Enqueue(dir, scribe.Trigger{
		SessionID:      "sess-index",
		TranscriptPath: transcriptPath,
		RepoRoot:       dir,
	}); err != nil {
		t.Fatalf("enqueueing trigger: %v", err)
	}

	withFakeRunWriter(t, "CATEGORY: feature\nSUMMARY: shipped the widget")

	if _, stderr, err := runRunCmd(t, dir); err != nil {
		t.Fatalf("scribe run failed: %v\nstderr: %s", err, stderr)
	}

	indexPath := filepath.Join(dir, "docs", "scribe", "INDEX.md")
	b, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("expected INDEX.md to exist after run: %v", err)
	}
	if !bytes.Contains(b, []byte("shipped the widget")) {
		t.Fatalf("INDEX.md missing the session summary, got:\n%s", b)
	}
}

func TestRun_OutsideGitRepo_Errors(t *testing.T) {
	dir := t.TempDir() // deliberately no .git

	_, _, err := runRunCmd(t, dir)
	if err == nil {
		t.Fatal("expected an error running scribe run outside a git repo, got nil")
	}
}

func TestRun_NotInitialised_Errors(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("creating fake .git: %v", err)
	}

	_, _, err := runRunCmd(t, dir)
	if err == nil {
		t.Fatal("expected an error running scribe run before scribe init, got nil")
	}
}

func TestRun_HappyPath_DrainsQueueAndAppliesWriterEdits(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("creating fake .git: %v", err)
	}

	if err := install.WriteConfig(dir, install.Config{
		Agent:   "opencode",
		Model:   "opencode/longcat-2.0-free",
		DocsDir: scribe.DocsDir,
		Enabled: true,
	}); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	// Seed a transcript with one real assistant turn so the writer has
	// something to react to, and a queue trigger pointing at it — the shape
	// "scribe hook" would have produced.
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	transcriptLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"did the thing"}]},"timestamp":"2024-01-01T00:00:00Z","isSidechain":false}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcriptLine), 0o644); err != nil {
		t.Fatalf("writing fake transcript: %v", err)
	}

	sessionID := "test-session"
	trigger := scribe.Trigger{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
		RepoRoot:       dir,
	}
	if err := queue.Enqueue(dir, trigger); err != nil {
		t.Fatalf("enqueueing trigger: %v", err)
	}

	// Phase 03 replaced the single combined JSON call with one plain-text
	// call per doc, so the fake's output is now an entry, not an envelope.
	// This transcript is pure engineering with no product-level talk, so the
	// PROJECT/DECISIONS gate declines before spending a call on either —
	// leaving the two history docs (one call each) plus the phase 05
	// per-session summary call, three in total. The fake returns the same
	// string for every call; the summary call's copy has no "SUMMARY:" line,
	// so parseSummaryOutput declines it and the worker logs and moves on —
	// the doc-writing the test asserts on is unaffected.
	fakeOut := "- Updated by the fake writer."
	calls := withFakeRunWriter(t, fakeOut)

	stdout, _, err := runRunCmd(t, dir)
	if err != nil {
		t.Fatalf("scribe run failed: %v\nstdout: %s", err, stdout)
	}
	if *calls != 3 {
		t.Fatalf("writer was called %d time(s), want 3 (CHANGELOG + JOURNAL + session summary)", *calls)
	}

	for _, doc := range []scribe.Doc{scribe.DocChangelog, scribe.DocJournal} {
		path := filepath.Join(dir, "docs", "scribe", string(doc))
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected %s to exist after run: %v", path, err)
		}
		if !bytes.Contains(b, []byte("Updated by the fake writer")) {
			t.Fatalf("%s wasn't updated by the writer's output, got: %s", doc, b)
		}
	}

	// The gate must actually have kept PROJECT.md out of it. The file still
	// exists — the store creates all four lazily with a header on first read
	// — so the check is that the writer's output never landed in it.
	projectPath := filepath.Join(dir, "docs", "scribe", string(scribe.DocProject))
	b, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatalf("reading %s: %v", projectPath, err)
	}
	if bytes.Contains(b, []byte("Updated by the fake writer")) {
		t.Fatalf("%s was written for a transcript with no product-level content", projectPath)
	}
}
