// Package docs reads and writes the four markdown docs scribe keeps under
// <repoRoot>/docs/scribe/ (scribe.DocsDir).
//
// The four docs split into two kinds (see docs/PLAN.md, "The four docs"):
//
//   - PROJECT.md and DECISIONS.md are "current state" docs: each run rewrites
//     them in place. Use WriteState.
//   - CHANGELOG.md and JOURNAL.md are "history" docs: kept forever,
//     append-only. Use AppendHistory.
//
// The two are deliberately different methods, not one Write(doc, content)
// call, so a caller cannot accidentally pass a full rewrite of a history doc
// and truncate real history — AppendHistory only ever grows the file, and
// WriteState refuses to touch a history doc at all.
//
// Per locked decision 10, git is the only undo and nothing auto-commits, so
// every write in this package is atomic (temp file + rename) — a crash or
// kill mid-write must never leave a doc half-written or empty.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// defaultHeader is the content a doc is seeded with the first time it's read
// and no file exists yet.
var defaultHeader = map[scribe.Doc]string{
	scribe.DocProject:   "# Project\n\n_Nothing recorded yet._\n",
	scribe.DocDecisions: "# Decisions\n\n_No decisions recorded yet._\n",
	scribe.DocChangelog: "# Changelog\n",
	scribe.DocJournal:   "# Journal\n",
}

// stateDocs and historyDocs partition scribe.AllDocs into the two kinds
// described above.
var stateDocs = map[scribe.Doc]bool{
	scribe.DocProject:   true,
	scribe.DocDecisions: true,
}

var historyDocs = map[scribe.Doc]bool{
	scribe.DocChangelog: true,
	scribe.DocJournal:   true,
}

// Store is a handle on one repo's docs/scribe/ directory.
type Store struct {
	dir string // <repoRoot>/docs/scribe
}

// Open returns a Store for repoRoot, creating docs/scribe/ if it doesn't
// exist yet. It does not create the four files — those are created lazily
// on first Read (or first Write/Append), each with a sensible header.
func Open(repoRoot string) (*Store, error) {
	dir := filepath.Join(repoRoot, scribe.DocsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("docs: create %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// IsStateDoc reports whether doc is one of the current-state docs
// (PROJECT.md, DECISIONS.md) — rewritten in place via WriteState.
func IsStateDoc(doc scribe.Doc) bool { return stateDocs[doc] }

// IsHistoryDoc reports whether doc is one of the append-only history docs
// (CHANGELOG.md, JOURNAL.md) — grown via AppendHistory.
func IsHistoryDoc(doc scribe.Doc) bool { return historyDocs[doc] }

// Path returns the absolute path of doc within the store.
func (s *Store) Path(doc scribe.Doc) string {
	return filepath.Join(s.dir, string(doc))
}

// Read returns the current content of doc, creating it with a default
// header first if it doesn't exist yet.
func (s *Store) Read(doc scribe.Doc) (string, error) {
	path := s.Path(doc)
	b, err := os.ReadFile(path)
	if err == nil {
		return string(b), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("docs: read %s: %w", path, err)
	}

	header := defaultHeader[doc]
	if err := atomicWrite(path, []byte(header)); err != nil {
		return "", fmt.Errorf("docs: seed %s: %w", path, err)
	}
	return header, nil
}

// ReadAll returns the current content of all four docs, in scribe.AllDocs
// order, seeding any that don't exist yet. This is the shape the worker
// hands to the writer each run.
func (s *Store) ReadAll() (map[scribe.Doc]string, error) {
	out := make(map[scribe.Doc]string, len(scribe.AllDocs))
	for _, d := range scribe.AllDocs {
		content, err := s.Read(d)
		if err != nil {
			return nil, err
		}
		out[d] = content
	}
	return out, nil
}

// WriteState rewrites doc in place with content. Only valid for PROJECT.md
// and DECISIONS.md — the current-state docs, decision "current state only,
// no history" in docs/PLAN.md. Called on a history doc, it returns an error
// rather than truncating real history.
func (s *Store) WriteState(doc scribe.Doc, content string) error {
	if !stateDocs[doc] {
		return fmt.Errorf("docs: WriteState called on %s, which is an append-only history doc — use AppendHistory", doc)
	}
	if err := atomicWrite(s.Path(doc), []byte(content)); err != nil {
		return fmt.Errorf("docs: write %s: %w", doc, err)
	}
	return nil
}

// AppendHistory adds entry to the end of doc, seeding the doc first if it
// doesn't exist. Only valid for CHANGELOG.md and JOURNAL.md. The write is
// still atomic — the whole file (old content + entry) is written to a temp
// file and renamed into place — so a crash mid-append can only leave the
// old content intact or the new content in full, never a torn file.
func (s *Store) AppendHistory(doc scribe.Doc, entry string) error {
	if !historyDocs[doc] {
		return fmt.Errorf("docs: AppendHistory called on %s, which is a current-state doc — use WriteState", doc)
	}
	cur, err := s.Read(doc)
	if err != nil {
		return err
	}

	sep := ""
	if cur != "" && !strings.HasSuffix(cur, "\n") {
		sep = "\n"
	}
	entry = strings.TrimRight(entry, "\n") + "\n"

	if err := atomicWrite(s.Path(doc), []byte(cur+sep+entry)); err != nil {
		return fmt.Errorf("docs: append %s: %w", doc, err)
	}
	return nil
}

// atomicWrite writes data to path via temp file + rename, so a reader (or a
// crash) never observes a partially written file. The temp file is created
// in the same directory as path so the rename is on one filesystem and is
// atomic per POSIX semantics.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup: if we return before the rename succeeds, don't
	// leave the temp file behind.
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
