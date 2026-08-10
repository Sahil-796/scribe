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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// Size caps, in bytes, on the content the writer is handed for a doc. Phase
// 03's constraint (docs/PLAN.md, item "size caps on the edited docs so the
// model can hold one whole and edit it safely") is what makes locked
// decision 4 — flat per-run cost, only new transcript bytes plus the
// current docs — actually hold: that guarantee breaks the moment a doc is
// too big for the writer to hold in full.
//
// The numbers: markdown prose runs roughly 4 bytes per token (a common rule
// of thumb for English text — code-heavy content runs worse, but these
// docs are prose). The writer is a cheap model by design (decision 6) —
// commonly advertised at an 8K-32K token context window — but the slice of
// that window it can hold AND edit reliably, without losing track of
// earlier content or corrupting unrelated sections, is well short of the
// advertised ceiling. It also has to share the window with the system
// prompt, the new transcript slice being folded in, and its own output.
//
// DefaultHistoryCap budgets roughly 8,000 tokens (32 KB) of doc content.
// CHANGELOG.md and JOURNAL.md are the docs expected to actually grow over a
// project's life, so they get the larger of the two budgets — safe to do
// because AppendHistory backs it with rotation (see rotateHistory), so
// growth never actually threatens the cap for long.
//
// DefaultStateCap budgets roughly 4,000 tokens (16 KB) — half that. PROJECT
// and DECISIONS are "current state only, no history" (docs/PLAN.md), so
// they should stay terse by design; there is no rotation backstop for them
// (see WriteState), so exceeding the cap is treated as the writer being
// bloated, not as expected growth, and the tighter budget makes that show
// up sooner.
const (
	DefaultHistoryCap int64 = 32 * 1024
	DefaultStateCap   int64 = 16 * 1024
)

// ErrStateDocTooLarge is returned by WriteState when content is over the
// doc's size cap. PROJECT.md and DECISIONS.md are rewritten in place, not
// appended, so unlike the history docs there is nowhere to rotate the
// excess to: going over cap here means the writer produced a bloated
// rewrite, not that real history piled up. That is the caller's problem to
// react to (re-prompt the writer to trim, alert, whatever), not something
// this package can silently paper over — so it is surfaced as an error
// rather than written anyway or silently truncated.
var ErrStateDocTooLarge = errors.New("docs: state doc content exceeds size cap")

// Store is a handle on one repo's docs/scribe/ directory.
type Store struct {
	repoRoot string // needed for lastrun.json, which lives under .scribe/, a sibling of docs/scribe/ rather than inside it
	dir      string // <repoRoot>/docs/scribe

	// stateCap and historyCap override DefaultStateCap and DefaultHistoryCap
	// when non-zero. Set via SetCaps; zero (the zero value) means "use the
	// default".
	stateCap   int64
	historyCap int64

	// Now stamps archive filenames (archive/DOC-YYYY-MM.md) when
	// AppendHistory rotates. Defaults to time.Now; tests set it directly for
	// deterministic archive paths and to exercise "rotate twice in a row"
	// without sleeping across a month boundary.
	Now func() time.Time
}

// Open returns a Store for repoRoot, creating docs/scribe/ if it doesn't
// exist yet. It does not create the four files — those are created lazily
// on first Read (or first Write/Append), each with a sensible header.
func Open(repoRoot string) (*Store, error) {
	dir := filepath.Join(repoRoot, scribe.DocsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("docs: create %s: %w", dir, err)
	}
	return &Store{repoRoot: repoRoot, dir: dir}, nil
}

// SetCaps overrides this store's default size caps, in bytes. Pass 0 for
// either argument to leave that kind's default (DefaultStateCap /
// DefaultHistoryCap) in place — this is what lets a caller raise only one
// of the two, or reset a store back to defaults with SetCaps(0, 0).
func (s *Store) SetCaps(stateCap, historyCap int64) {
	s.stateCap = stateCap
	s.historyCap = historyCap
}

// capFor returns the effective size cap for doc, in bytes: the override set
// via SetCaps if positive, otherwise the default for doc's kind.
func (s *Store) capFor(doc scribe.Doc) int64 {
	if stateDocs[doc] {
		if s.stateCap > 0 {
			return s.stateCap
		}
		return DefaultStateCap
	}
	if s.historyCap > 0 {
		return s.historyCap
	}
	return DefaultHistoryCap
}

// now returns s.Now() if set, else time.Now(). Mirrors the Deps.now()
// pattern in internal/worker.
func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
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
	if cap := s.capFor(doc); int64(len(content)) > cap {
		return fmt.Errorf("docs: %s content is %d bytes, over its %d byte cap: %w", doc, len(content), cap, ErrStateDocTooLarge)
	}

	// Captured before the write so the snapshot (see lastrun.go) has
	// something to diff against. Read seeds the doc with its default
	// header if this is the first write ever, which is exactly the right
	// "before" for a doc going from nothing to something.
	before, err := s.Read(doc)
	if err != nil {
		return err
	}

	if err := atomicWrite(s.Path(doc), []byte(content)); err != nil {
		return fmt.Errorf("docs: write %s: %w", doc, err)
	}
	s.recordChange(doc, before, content)
	return nil
}

// AppendHistory adds entry to the end of doc as one block, seeding the doc
// first if it doesn't exist. Only valid for CHANGELOG.md and JOURNAL.md.
//
// If the doc would exceed its size cap after the append, the oldest entries
// are rotated out to an archive file first (see rotateHistory) — never the
// entry just being added, and never a partial entry. The live doc write is
// still atomic — the whole file is written to a temp file and renamed into
// place — so a crash mid-append can only leave the old content intact or
// the new content in full, never a torn file.
func (s *Store) AppendHistory(doc scribe.Doc, entry string) error {
	if !historyDocs[doc] {
		return fmt.Errorf("docs: AppendHistory called on %s, which is a current-state doc — use WriteState", doc)
	}
	cur, err := s.Read(doc)
	if err != nil {
		return err
	}

	title, blocks := parseHistoryDoc(cur)
	blocks = append(blocks, strings.TrimRight(entry, "\n"))

	blocks, err = s.rotateHistory(doc, title, blocks)
	if err != nil {
		return fmt.Errorf("docs: rotate %s: %w", doc, err)
	}

	after := serializeHistoryDoc(title, blocks)
	if err := atomicWrite(s.Path(doc), []byte(after)); err != nil {
		return fmt.Errorf("docs: append %s: %w", doc, err)
	}
	s.recordChange(doc, cur, after)
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
