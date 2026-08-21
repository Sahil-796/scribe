package seed

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/redact"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// fakeWriter is a stand-in scribe.Writer for tests: no real agent
// invocation, no network, no dependency on any binary being on this
// machine's PATH.
//
// responses holds one output per call, in order (Run is now called twice
// per seed.Run — once for PROJECT.md, once for DECISIONS.md — so a single
// canned "out" string is no longer enough). errs, keyed by call index,
// forces a given call to fail instead. If out/err are set directly (the
// single-response shape used by most tests, where both calls should
// succeed with the same simple response), they're used verbatim; that
// keeps most existing test bodies unchanged.
type fakeWriter struct {
	out  string // used for every call if responses is empty
	err  error  // used for every call if errs is nil
	errs map[int]error

	responses  []string
	prompts    []string
	lastPrompt string
	calls      int
}

func (w *fakeWriter) Name() string { return "fake" }

func (w *fakeWriter) Run(prompt string) (string, error) {
	i := w.calls
	w.calls++
	w.lastPrompt = prompt
	w.prompts = append(w.prompts, prompt)

	if w.errs != nil {
		if err, ok := w.errs[i]; ok {
			return "", err
		}
	} else if w.err != nil {
		return "", w.err
	}

	if len(w.responses) > 0 {
		if i < len(w.responses) {
			return w.responses[i], nil
		}
		return w.responses[len(w.responses)-1], nil
	}
	return w.out, nil
}

func testRedactor() *redact.Redactor {
	return redact.New(
		[]string{"api_key", "token", "password", "secret"},
		[]string{"**/.env*", "**/secrets/**"},
	)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	t.Run("gathers readme manifest tree and prunes noise", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "README.md"), "# My Project\nDoes a thing.")
		writeFile(t, filepath.Join(root, "go.mod"), "module example.com/foo\n\ngo 1.22\n")
		writeFile(t, filepath.Join(root, "main.go"), "package main\n")
		writeFile(t, filepath.Join(root, "node_modules", "leftpad", "index.js"), "junk")
		writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main")

		f, err := Scan(root, testRedactor())
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}

		if !strings.Contains(f.Files["README.md"], "Does a thing") {
			t.Errorf("expected README.md content, got %q", f.Files["README.md"])
		}
		if !strings.Contains(f.Files["go.mod"], "module example.com/foo") {
			t.Errorf("expected go.mod content, got %q", f.Files["go.mod"])
		}
		if strings.Contains(f.Tree, "node_modules") {
			t.Errorf("expected node_modules pruned from tree, got:\n%s", f.Tree)
		}
		if strings.Contains(f.Tree, ".git") {
			t.Errorf("expected .git pruned from tree, got:\n%s", f.Tree)
		}
		if !strings.Contains(f.Tree, "main.go") {
			t.Errorf("expected main.go in tree, got:\n%s", f.Tree)
		}
	})

	t.Run("honors gitignore bare names", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".gitignore"), "coverage\nsecrets.txt\n")
		writeFile(t, filepath.Join(root, "coverage", "report.html"), "junk")
		writeFile(t, filepath.Join(root, "secrets.txt"), "shh")
		writeFile(t, filepath.Join(root, "keep.txt"), "keep me")

		f, err := Scan(root, testRedactor())
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if strings.Contains(f.Tree, "coverage") {
			t.Errorf("expected gitignored coverage/ pruned, got:\n%s", f.Tree)
		}
		if strings.Contains(f.Tree, "secrets.txt") {
			t.Errorf("expected gitignored secrets.txt pruned, got:\n%s", f.Tree)
		}
		if !strings.Contains(f.Tree, "keep.txt") {
			t.Errorf("expected keep.txt in tree, got:\n%s", f.Tree)
		}
	})

	t.Run("caps individual file size", func(t *testing.T) {
		root := t.TempDir()
		big := strings.Repeat("x", maxFileBytes*2)
		writeFile(t, filepath.Join(root, "README.md"), big)

		f, err := Scan(root, testRedactor())
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(f.Files["README.md"]) > maxFileBytes+64 {
			t.Errorf("expected README.md capped near %d bytes, got %d", maxFileBytes, len(f.Files["README.md"]))
		}
		if !strings.Contains(f.Files["README.md"], "truncated") {
			t.Errorf("expected truncation marker in capped file")
		}
	})

	t.Run("picks up existing scribe docs", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "docs", "scribe", "PROJECT.md"), "# Project\nExisting content.")

		f, err := Scan(root, testRedactor())
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !strings.Contains(f.Files["docs/scribe/PROJECT.md"], "Existing content") {
			t.Errorf("expected existing PROJECT.md picked up, got files: %v", f.Files)
		}
	})

	t.Run("does not follow symlinks out of repo root", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		writeFile(t, filepath.Join(outside, "secret.txt"), "outside content")
		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skipf("symlinks not supported: %v", err)
		}

		f, err := Scan(root, testRedactor())
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if strings.Contains(f.Tree, "secret.txt") {
			t.Errorf("expected symlinked-out content not to appear in tree, got:\n%s", f.Tree)
		}
		for name := range f.Files {
			if strings.Contains(name, "secret.txt") {
				t.Errorf("expected symlinked-out file not to be read, got %v", f.Files)
			}
		}
	})

	t.Run("errors on missing repo root", func(t *testing.T) {
		_, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"), testRedactor())
		if err == nil {
			t.Fatal("expected error for missing repo root, got nil")
		}
	})

	// Phase 04 requirement (docs/phases/04-config-and-safety.md): Privacy.Ignore
	// globs must drop a matching file before it's ever read, not merely keep
	// its content out of the final prompt. The assertion that matters here
	// is f.Files[...] being entirely absent, not just short/redacted — a
	// present-but-redacted entry would mean the bytes were read into memory
	// at all, which is the thing this rule exists to prevent.
	t.Run("drops ignored files before reading them", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "README.md"), "# Project\nordinary readme content")
		writeFile(t, filepath.Join(root, ".env"), "API_KEY=sk-shouldneverberead1234567890")
		writeFile(t, filepath.Join(root, "secrets", "creds.txt"), "shouldneverberead")
		writeFile(t, filepath.Join(root, "docs", "scribe", "PROJECT.md"), "existing docs content")

		r := redact.New(nil, []string{"**/.env*", "**/secrets/**"})
		f, err := Scan(root, r)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}

		if _, ok := f.Files[".env"]; ok {
			t.Errorf("expected .env dropped entirely, got it in Files: %v", f.Files)
		}
		if _, ok := f.Files["secrets/creds.txt"]; ok {
			t.Errorf("expected secrets/creds.txt dropped entirely, got it in Files: %v", f.Files)
		}
		if strings.Contains(f.Tree, ".env") {
			t.Errorf("expected .env absent from the tree too, got:\n%s", f.Tree)
		}
		if strings.Contains(f.Tree, "secrets") {
			t.Errorf("expected the secrets/ directory not even walked, got:\n%s", f.Tree)
		}
		if !strings.Contains(f.Files["README.md"], "ordinary readme content") {
			t.Errorf("expected README.md (not ignored) to still be read, got %v", f.Files)
		}
	})

	t.Run("nil secretIgnore is an error, not a silent skip", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "README.md"), "hi")
		if _, err := Scan(root, nil); err == nil {
			t.Fatal("expected Scan(root, nil) to error rather than silently read everything")
		}
	})
}

func TestProjectPrompt(t *testing.T) {
	f := Facts{
		RepoRoot: "/repo",
		Tree:     "main.go\n",
		Files: map[string]string{
			"README.md": "hello",
			"go.mod":    "module foo",
		},
	}
	p := ProjectPrompt(f, testRedactor())

	for _, want := range []string{"/repo", "main.go", "README.md", "hello", "go.mod", "module foo", "PROJECT.md", "Prefer saying less"} {
		if !strings.Contains(p, want) {
			t.Errorf("expected project prompt to contain %q, got:\n%s", want, p)
		}
	}
	// The project prompt should not carry decisions-specific guidance —
	// each call is scoped to its own doc.
	if strings.Contains(p, "active, dropped, or superseded") {
		t.Errorf("project prompt should not mention decisions-specific guidance, got:\n%s", p)
	}
}

func TestDecisionsPrompt(t *testing.T) {
	f := Facts{
		RepoRoot: "/repo",
		Tree:     "main.go\n",
		Files: map[string]string{
			"README.md": "hello",
			"go.mod":    "module foo",
		},
	}
	p := DecisionsPrompt(f, testRedactor())

	for _, want := range []string{"/repo", "main.go", "README.md", "hello", "go.mod", "module foo", "DECISIONS.md", "none are\nrecorded yet"} {
		if !strings.Contains(p, want) {
			t.Errorf("expected decisions prompt to contain %q, got:\n%s", want, p)
		}
	}
}

// TestRedactionChokePointCoversEveryPromptBuilder is this package's half of
// the phase 04 requirement (docs/phases/04-config-and-safety.md): a test
// that fails if a prompt builder in this package learns a second way to
// reach repo content, bypassing internal/redact. Both builders this
// package owns are enumerated here by name. The secret is planted in a
// scanned file's *content*, not its path, so this specifically exercises
// the redaction pass in prompt.go rather than the Ignore-glob file
// dropping already covered by TestScan's "drops ignored files" case.
func TestRedactionChokePointCoversEveryPromptBuilder(t *testing.T) {
	const secret = "sk-supersecretvalue1234567890abcdef"
	r := testRedactor()

	f := Facts{
		RepoRoot: "/repo",
		Tree:     "main.go\n",
		Files: map[string]string{
			"README.md": "setup: api_key=" + secret,
		},
	}

	rendered := map[string]string{
		"ProjectPrompt":   ProjectPrompt(f, r),
		"DecisionsPrompt": DecisionsPrompt(f, r),
	}

	for name, prompt := range rendered {
		if strings.Contains(prompt, secret) {
			t.Errorf("%s leaked the planted secret into the rendered prompt:\n%s", name, prompt)
		}
		if !strings.Contains(prompt, "[redacted:") {
			t.Errorf("%s shows no redaction placeholder — expected the planted secret to be caught, got:\n%s", name, prompt)
		}
	}
}

func TestProjectPromptPanicsOnNilRedactor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected ProjectPrompt with a nil Redactor to panic")
		}
	}()
	ProjectPrompt(Facts{RepoRoot: "/repo"}, nil)
}

func TestDecisionsPromptPanicsOnNilRedactor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected DecisionsPrompt with a nil Redactor to panic")
		}
	}()
	DecisionsPrompt(Facts{RepoRoot: "/repo"}, nil)
}

func TestParseProject(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain content", in: "# Project\ncontent", want: "# Project\ncontent"},
		{name: "fenced content", in: "```markdown\n# Project\ncontent\n```", want: "# Project\ncontent"},
		{name: "empty string is an error", in: "", wantErr: true},
		{name: "whitespace only is an error", in: "   \n  ", wantErr: true},
		{name: "short honest content is not an error", in: "A CLI tool. Not enough evidence to say more.", want: "A CLI tool. Not enough evidence to say more."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProject(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseDecisions(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain content", in: "# Decisions\nnone yet", want: "# Decisions\nnone yet"},
		{name: "fenced content", in: "```\n# Decisions\nnone yet\n```", want: "# Decisions\nnone yet"},
		{name: "empty string is an error", in: "", wantErr: true},
		{name: "near-empty honest content is not an error", in: "No decisions recorded yet.", want: "No decisions recorded yet."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDecisions(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Run("happy path scans, makes one call per doc, writes nothing", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "README.md"), "# Thing")

		w := &fakeWriter{responses: []string{"seeded project", "seeded decisions"}}
		got, err := Run(root, w, testRedactor())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if w.calls != 2 {
			t.Errorf("expected writer called twice (one per doc), got %d", w.calls)
		}
		for i, p := range w.prompts {
			if !strings.Contains(p, "# Thing") {
				t.Errorf("expected prompt %d to include README content, got:\n%s", i, p)
			}
		}
		if !strings.Contains(w.prompts[0], "PROJECT.md") {
			t.Errorf("expected first call's prompt to be the project prompt, got:\n%s", w.prompts[0])
		}
		if !strings.Contains(w.prompts[1], "DECISIONS.md") {
			t.Errorf("expected second call's prompt to be the decisions prompt, got:\n%s", w.prompts[1])
		}
		if got[scribe.DocProject] != "seeded project" {
			t.Errorf("PROJECT.md = %q", got[scribe.DocProject])
		}
		if got[scribe.DocDecisions] != "seeded decisions" {
			t.Errorf("DECISIONS.md = %q", got[scribe.DocDecisions])
		}

		// Run must not write any file — it only returns content.
		if _, err := os.Stat(filepath.Join(root, "docs", "scribe", "PROJECT.md")); !os.IsNotExist(err) {
			t.Errorf("expected Run not to write docs/scribe/PROJECT.md, stat err: %v", err)
		}
	})

	t.Run("propagates writer error on the project call", func(t *testing.T) {
		root := t.TempDir()
		w := &fakeWriter{err: errors.New("boom")}
		_, err := Run(root, w, testRedactor())
		if err == nil {
			t.Fatal("expected error from writer failure")
		}
		if w.calls != 1 {
			t.Errorf("expected the decisions call to be skipped after the project call failed, got %d calls", w.calls)
		}
	})

	t.Run("propagates writer error on the decisions call", func(t *testing.T) {
		root := t.TempDir()
		w := &fakeWriter{out: "seeded project", errs: map[int]error{1: errors.New("boom")}}
		_, err := Run(root, w, testRedactor())
		if err == nil {
			t.Fatal("expected error from writer failure on the second call")
		}
		if w.calls != 2 {
			t.Errorf("expected both calls attempted before failing on the second, got %d calls", w.calls)
		}
	})

	t.Run("propagates parse error on empty writer output", func(t *testing.T) {
		root := t.TempDir()
		w := &fakeWriter{out: ""}
		_, err := Run(root, w, testRedactor())
		if err == nil {
			t.Fatal("expected error from empty writer output")
		}
	})

	t.Run("propagates scan error on bad repo root", func(t *testing.T) {
		w := &fakeWriter{responses: []string{"x", "y"}}
		_, err := Run(filepath.Join(t.TempDir(), "missing"), w, testRedactor())
		if err == nil {
			t.Fatal("expected error from missing repo root")
		}
		if w.calls != 0 {
			t.Errorf("expected writer not called when scan fails, got %d calls", w.calls)
		}
	})
}
