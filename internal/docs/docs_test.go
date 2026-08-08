package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestOpenCreatesDocsDir(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, scribe.DocsDir)); err != nil {
		t.Fatalf("docs dir not created: %v", err)
	}
	if s.Path(scribe.DocProject) != filepath.Join(root, scribe.DocsDir, "PROJECT.md") {
		t.Fatalf("unexpected path: %s", s.Path(scribe.DocProject))
	}
}

func TestReadSeedsMissingDocWithHeader(t *testing.T) {
	s := mustOpen(t)

	content, err := s.Read(scribe.DocJournal)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.HasPrefix(content, "# Journal") {
		t.Fatalf("expected default header, got %q", content)
	}

	// File must now exist on disk with exactly that content.
	b, err := os.ReadFile(s.Path(scribe.DocJournal))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != content {
		t.Fatalf("disk content mismatch: %q vs %q", b, content)
	}
}

func TestReadAllReturnsFourDocs(t *testing.T) {
	s := mustOpen(t)
	all, err := s.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(all) != len(scribe.AllDocs) {
		t.Fatalf("expected %d docs, got %d", len(scribe.AllDocs), len(all))
	}
	for _, d := range scribe.AllDocs {
		if _, ok := all[d]; !ok {
			t.Fatalf("missing doc %s in ReadAll result", d)
		}
	}
}

func TestWriteStateRewritesInPlace(t *testing.T) {
	s := mustOpen(t)

	if err := s.WriteState(scribe.DocProject, "# Project\n\nv1\n"); err != nil {
		t.Fatalf("WriteState v1: %v", err)
	}
	if err := s.WriteState(scribe.DocProject, "# Project\n\nv2\n"); err != nil {
		t.Fatalf("WriteState v2: %v", err)
	}

	got, err := s.Read(scribe.DocProject)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "# Project\n\nv2\n" {
		t.Fatalf("expected only v2 content (full rewrite), got %q", got)
	}
	if strings.Contains(got, "v1") {
		t.Fatalf("old content leaked through rewrite: %q", got)
	}
}

func TestWriteStateRejectsHistoryDoc(t *testing.T) {
	s := mustOpen(t)
	err := s.WriteState(scribe.DocChangelog, "nope")
	if err == nil {
		t.Fatal("expected error writing state to a history doc, got nil")
	}
}

func TestAppendHistoryRejectsStateDoc(t *testing.T) {
	s := mustOpen(t)
	err := s.AppendHistory(scribe.DocDecisions, "nope")
	if err == nil {
		t.Fatal("expected error appending to a state doc, got nil")
	}
}

func TestAppendHistoryNeverTruncates(t *testing.T) {
	s := mustOpen(t)

	if err := s.AppendHistory(scribe.DocChangelog, "2026-08-08: first entry"); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := s.AppendHistory(scribe.DocChangelog, "2026-08-08: second entry"); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := s.AppendHistory(scribe.DocChangelog, "2026-08-08: third entry"); err != nil {
		t.Fatalf("append 3: %v", err)
	}

	got, err := s.Read(scribe.DocChangelog)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, want := range []string{"first entry", "second entry", "third entry"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to survive in history, got:\n%s", want, got)
		}
	}
	// Order preserved.
	if strings.Index(got, "first entry") > strings.Index(got, "second entry") ||
		strings.Index(got, "second entry") > strings.Index(got, "third entry") {
		t.Fatalf("entries out of order:\n%s", got)
	}
}

func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	s := mustOpen(t)
	if err := s.WriteState(scribe.DocProject, "content"); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}
