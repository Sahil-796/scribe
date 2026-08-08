package seed

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// fakeWriter is a stand-in scribe.Writer for tests: no real agent
// invocation, no network, no dependency on any binary being on this
// machine's PATH.
type fakeWriter struct {
	out        string
	err        error
	lastPrompt string
	calls      int
}

func (w *fakeWriter) Name() string { return "fake" }

func (w *fakeWriter) Run(prompt string) (string, error) {
	w.calls++
	w.lastPrompt = prompt
	return w.out, w.err
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

		f, err := Scan(root)
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

		f, err := Scan(root)
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

		f, err := Scan(root)
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

		f, err := Scan(root)
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

		f, err := Scan(root)
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
		_, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"))
		if err == nil {
			t.Fatal("expected error for missing repo root, got nil")
		}
	})
}

func TestPrompt(t *testing.T) {
	f := Facts{
		RepoRoot: "/repo",
		Tree:     "main.go\n",
		Files: map[string]string{
			"README.md": "hello",
			"go.mod":    "module foo",
		},
	}
	p := Prompt(f)

	for _, want := range []string{"/repo", "main.go", "README.md", "hello", "go.mod", "module foo", "PROJECT.md", "DECISIONS.md"} {
		if !strings.Contains(p, want) {
			t.Errorf("expected prompt to contain %q, got:\n%s", want, p)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    map[scribe.Doc]string
		wantErr bool
	}{
		{
			name: "plain json",
			in:   `{"PROJECT.md": "# Project\ncontent", "DECISIONS.md": "# Decisions\nnone yet"}`,
			want: map[scribe.Doc]string{
				scribe.DocProject:   "# Project\ncontent",
				scribe.DocDecisions: "# Decisions\nnone yet",
			},
		},
		{
			name: "fenced json",
			in:   "```json\n{\"PROJECT.md\": \"content\", \"DECISIONS.md\": \"more\"}\n```",
			want: map[scribe.Doc]string{
				scribe.DocProject:   "content",
				scribe.DocDecisions: "more",
			},
		},
		{
			name:    "empty string is an error",
			in:      "",
			wantErr: true,
		},
		{
			name:    "whitespace only is an error",
			in:      "   \n  ",
			wantErr: true,
		},
		{
			name:    "empty json object is an error",
			in:      "{}",
			wantErr: true,
		},
		{
			name:    "unknown key is an error",
			in:      `{"CHANGELOG.md": "nope"}`,
			wantErr: true,
		},
		{
			name:    "empty value is an error",
			in:      `{"PROJECT.md": "", "DECISIONS.md": "fine"}`,
			wantErr: true,
		},
		{
			name:    "not json is an error",
			in:      "sorry, I can't help with that",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d docs, want %d: %v", len(got), len(tt.want), got)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("doc %s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Run("happy path scans prompts writes nothing", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "README.md"), "# Thing")

		w := &fakeWriter{out: `{"PROJECT.md": "seeded project", "DECISIONS.md": "seeded decisions"}`}
		got, err := Run(root, w)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if w.calls != 1 {
			t.Errorf("expected writer called once, got %d", w.calls)
		}
		if !strings.Contains(w.lastPrompt, "# Thing") {
			t.Errorf("expected prompt to include README content, got:\n%s", w.lastPrompt)
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

	t.Run("propagates writer error", func(t *testing.T) {
		root := t.TempDir()
		w := &fakeWriter{err: errors.New("boom")}
		_, err := Run(root, w)
		if err == nil {
			t.Fatal("expected error from writer failure")
		}
	})

	t.Run("propagates parse error on garbage output", func(t *testing.T) {
		root := t.TempDir()
		w := &fakeWriter{out: "not json at all"}
		_, err := Run(root, w)
		if err == nil {
			t.Fatal("expected error from unparseable writer output")
		}
	})

	t.Run("propagates scan error on bad repo root", func(t *testing.T) {
		w := &fakeWriter{out: `{"PROJECT.md": "x", "DECISIONS.md": "y"}`}
		_, err := Run(filepath.Join(t.TempDir(), "missing"), w)
		if err == nil {
			t.Fatal("expected error from missing repo root")
		}
		if w.calls != 0 {
			t.Errorf("expected writer not called when scan fails, got %d calls", w.calls)
		}
	})
}
