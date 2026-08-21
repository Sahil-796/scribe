// Phase 06 ("teammates") makes the two history docs layout-aware. WriteHistory
// is the one entry point the worker calls for a CHANGELOG.md / JOURNAL.md
// entry; it branches on the Store's mode (see OpenWithLayout):
//
//   - Shared: stamp the author's byline onto the entry and append it to the
//     single doc, exactly as before — same rotation, same atomicity, same
//     `scribe diff` snapshot. This is the back-compat path Open records.
//   - PerSession: write the entry to its OWN file under changelog/ or journal/
//     (named uniquely per session by internal/layout, so two teammates never
//     write the same path and never conflict), then regenerate the top-level
//     CHANGELOG.md / JOURNAL.md as a pure rollup over every session file in
//     that subdir.
//
// The pure naming/rendering rules live in internal/layout; this file only does
// the filesystem work that package deliberately refuses to (reads, writes,
// directory listing) and a small frontmatter parser for the files it itself
// wrote via layout.RenderSessionFile — kept here so internal/layout stays pure.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/attribution"
	"github.com/Sahil-796/scribe/internal/layout"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// WriteHistory records one history entry for doc, crediting author, using the
// Store's configured layout. It is only valid for the append-only history docs
// (CHANGELOG.md, JOURNAL.md) — a state doc is a caller bug, surfaced rather
// than silently split.
//
// In Shared mode it is AppendHistory with a byline: the entry is stamped via
// attribution.StampShared (idempotent, so a reprocessed transcript slice never
// piles up duplicate credits) and then appended, preserving rotation, atomicity
// and the `scribe diff` snapshot exactly.
//
// In PerSession mode it writes the entry to its own session file and then
// regenerates the top-level doc as a rollup; both writes are atomic, and the
// rollup regeneration records the top-level change so `scribe diff` still sees
// it. meta identifies the session (its date and id name the file; its summary
// slugs the readable part of the name); an empty summary is fine — the session
// id suffix is what guarantees the path is unique.
func (s *Store) WriteHistory(doc scribe.Doc, meta layout.SessionMeta, author attribution.Author, entry string) error {
	if !historyDocs[doc] {
		return fmt.Errorf("docs: WriteHistory called on %s, which is a current-state doc — use WriteState", doc)
	}

	if s.mode == layout.PerSession {
		return s.writePerSession(doc, meta, author, entry)
	}
	// Shared is the default for any other value, including the zero value the
	// Open constructor leaves in place.
	return s.AppendHistory(doc, attribution.StampShared(entry, author))
}

// writePerSession writes one session file and regenerates doc's rollup. The
// session file is written first: if regeneration then fails the run aborts
// before offsets advance (see the worker's ordering rule) and the next run
// re-derives the same content, so a session file with no rollup line yet is a
// recoverable state, never lost work.
func (s *Store) writePerSession(doc scribe.Doc, meta layout.SessionMeta, author attribution.Author, entry string) error {
	relPath, err := layout.SessionFilePath(doc, meta)
	if err != nil {
		return fmt.Errorf("docs: per-session path for %s: %w", doc, err)
	}
	abs := filepath.Join(s.dir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("docs: create per-session dir for %s: %w", doc, err)
	}

	body := layout.RenderSessionFile(meta, author.Display(), entry)
	if err := atomicWrite(abs, []byte(body)); err != nil {
		return fmt.Errorf("docs: write session file %s: %w", relPath, err)
	}

	return s.regenerateRollup(doc, filepath.Dir(relPath))
}

// regenerateRollup rebuilds the top-level CHANGELOG.md / JOURNAL.md from every
// session file under subdir (relative to the docs dir), replacing it with
// layout.Rollup's pure, newest-first index. The before/after is recorded so
// `scribe diff` sees the rollup change the same way it sees a shared append —
// the session files themselves are per-session and conflict-free, but the
// rollup is the shared view a run actually changes.
func (s *Store) regenerateRollup(doc scribe.Doc, subdir string) error {
	files, err := s.listSessionFiles(doc, subdir)
	if err != nil {
		return err
	}

	before, err := s.Read(doc)
	if err != nil {
		return err
	}
	after := layout.Rollup(doc, files)
	if err := atomicWrite(s.Path(doc), []byte(after)); err != nil {
		return fmt.Errorf("docs: write rollup %s: %w", doc, err)
	}
	s.recordChange(doc, before, after)
	return nil
}

// listSessionFiles reads subdir and returns one layout.SessionFile per session
// file it holds, parsing back the frontmatter this package wrote via
// layout.RenderSessionFile. Order is irrelevant — layout.Rollup sorts its own
// input deterministically — so a directory listing's arbitrary order is fine.
// Non-markdown files, atomicWrite's dotfile temp files, and anything without
// readable frontmatter are skipped rather than allowed to break the rollup.
func (s *Store) listSessionFiles(doc scribe.Doc, subdir string) ([]layout.SessionFile, error) {
	dirAbs := filepath.Join(s.dir, subdir)
	ents, err := os.ReadDir(dirAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("docs: list session files in %s: %w", subdir, err)
	}

	var files []layout.SessionFile
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dirAbs, name))
		if err != nil {
			return nil, fmt.Errorf("docs: read session file %s: %w", name, err)
		}
		meta, authorLine := parseSessionFrontmatter(string(b))
		if meta.SessionID == "" {
			// No usable frontmatter — not a file we wrote. Skip rather than
			// render a broken rollup row for it.
			continue
		}
		files = append(files, layout.SessionFile{
			Path:   subdir + "/" + name,
			Meta:   meta,
			Author: authorLine,
		})
	}
	return files, nil
}

// parseSessionFrontmatter reads back the small key: value frontmatter block
// layout.RenderSessionFile writes (date / session / author / summary between a
// pair of "---" fences). It is intentionally minimal — not a YAML parser —
// because it only ever parses files this same package produced, whose shape is
// fixed. A malformed or absent block yields a zero SessionID, which the caller
// treats as "skip this file".
func parseSessionFrontmatter(content string) (meta layout.SessionMeta, author string) {
	inFrontmatter := false
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break // closing fence — nothing past here is frontmatter
		}
		if !inFrontmatter {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "date":
			if d, err := time.Parse("2006-01-02", val); err == nil {
				meta.Date = d
			}
		case "session":
			meta.SessionID = val
		case "author":
			author = val
		case "summary":
			meta.Summary = val
		}
	}
	return meta, author
}
