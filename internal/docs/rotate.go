// Rotation and archiving for the two append-only docs, CHANGELOG.md and
// JOURNAL.md. See AppendHistory in docs.go for the entry point; everything
// here is the machinery behind "the oldest content must move out of the
// live file rather than being lost" (phase 03 bullet 2).
//
// History docs are stored on disk as a title line followed by an ordered
// list of blocks, each block separated by blockSep. A block is either a
// real entry (whatever AppendHistory was called with) or an archive
// pointer (see archivePointerText) left behind by an earlier rotation.
// Splitting on a literal separator instead of tracking entries out of band
// keeps the on-disk file the only source of truth — no side index that
// could drift from what's actually there — at the cost of assuming no
// entry ever contains the separator itself.
//
// That assumption is why the separator is an HTML comment and not a `---`
// horizontal rule. The content being split here is markdown prose written
// by a language model, and a rule surrounded by blank lines is an entirely
// ordinary thing for one to emit — a delimiter that common inside the
// payload isn't a delimiter, it's a latent corruption. An HTML comment
// carrying the tool's own name renders as nothing, survives markdown
// tooling intact, and is not something a writer producing a changelog
// entry will type by accident. The same reasoning applies to the archive
// pointer marker below: identifying a block by its visible prose ("> _")
// would misread any entry that happened to open with an italic blockquote.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// blockSep separates the title line from the first block, and each block
// from the next, in a history doc's on-disk text. It renders as nothing at
// all, so the file still reads as plain markdown rather than as scribe's
// internal format — see the package comment for why this is a comment and
// not a `---` rule.
const blockSep = "\n\n<!-- scribe:entry -->\n\n"

// archivePointerMarker leads a block that is a rotation pointer rather
// than a real entry — see archivePointerText and isArchivePointer. Like
// blockSep it is invisible when rendered, so the pointer's visible text
// stays plain prose while the machine-readable part can't be produced by
// accident.
//
// The marker also carries the pointer's data (how many entries, which
// archive file) as attributes, so a later rotation into the same archive
// can find its own pointer and update it in place rather than appending a
// second one. Parsing the visible prose instead would mean a rendering
// tweak silently breaking coalescing.
const archivePointerMarker = "<!-- scribe:archived"

// serializeHistoryDoc renders a history doc's title line plus its ordered
// blocks back into on-disk text. It is parseHistoryDoc's inverse.
func serializeHistoryDoc(title string, blocks []string) string {
	var b strings.Builder
	b.WriteString(title)
	for _, blk := range blocks {
		b.WriteString(blockSep)
		b.WriteString(blk)
	}
	b.WriteString("\n")
	return b.String()
}

// parseHistoryDoc splits a history doc's raw text into its title line and
// ordered blocks (archive pointers and entries, oldest first).
func parseHistoryDoc(content string) (title string, blocks []string) {
	parts := strings.Split(content, blockSep)
	title = strings.TrimRight(parts[0], "\n")
	for _, p := range parts[1:] {
		blocks = append(blocks, strings.TrimRight(p, "\n"))
	}
	return title, blocks
}

// isArchivePointer reports whether block is a rotation pointer left by an
// earlier rotateHistory call (see archivePointerText), not a real entry.
// Pointer blocks stay in the live doc forever — they're the trail back to
// whatever got rotated out — so rotation must never treat one as an entry
// it's free to remove.
func isArchivePointer(block string) bool {
	return strings.HasPrefix(block, archivePointerMarker)
}

// archivePointerText builds the pointer block left in the live doc after a
// rotation moves n entries out to relPath (the archive file's path,
// relative to docs/scribe/ — what a reader following the doc would use).
func archivePointerText(n int, relPath string) string {
	noun := "entry"
	if n != 1 {
		noun = "entries"
	}
	return fmt.Sprintf("%s count=%d path=%s -->\n> _%d earlier %s archived to [%s](%s) to stay under the size cap._",
		archivePointerMarker, n, relPath, n, noun, relPath, relPath)
}

// parseArchivePointer reads back what archivePointerText encoded. Returns
// ok=false for anything that isn't a pointer block.
func parseArchivePointer(block string) (n int, relPath string, ok bool) {
	if !isArchivePointer(block) {
		return 0, "", false
	}
	end := strings.Index(block, "-->")
	if end < 0 {
		return 0, "", false
	}
	for _, f := range strings.Fields(block[len(archivePointerMarker):end]) {
		switch {
		case strings.HasPrefix(f, "count="):
			v, err := strconv.Atoi(strings.TrimPrefix(f, "count="))
			if err != nil {
				return 0, "", false
			}
			n = v
		case strings.HasPrefix(f, "path="):
			relPath = strings.TrimPrefix(f, "path=")
		}
	}
	return n, relPath, relPath != ""
}

// mergePointer folds a rotation of n entries into relPath into the existing
// pointer blocks, updating the matching pointer's count in place if there
// already is one and appending a new pointer otherwise.
//
// Without this, every rotation appends a pointer that is never itself
// rotated, so the live doc permanently spends part of its size cap on
// bookkeeping — a doc that rotates often ends up over cap purely from
// pointers, which defeats the cap. Archives group by calendar month, so in
// steady state this keeps the count at one pointer per month.
func mergePointer(pointers []string, n int, relPath string) []string {
	for i, p := range pointers {
		if existing, path, ok := parseArchivePointer(p); ok && path == relPath {
			merged := append([]string{}, pointers...)
			merged[i] = archivePointerText(existing+n, relPath)
			return merged
		}
	}
	return append(append([]string{}, pointers...), archivePointerText(n, relPath))
}

// SplitBlocks exposes parseHistoryDoc's title/blocks split to callers
// outside this package — namely integration tests in internal/worker that
// verify rotation through the real worker run loop and need to inspect both
// the live doc and archive files without duplicating the on-disk format
// here. Not needed by any production caller; AppendHistory and its
// machinery stay the only writer of this format.
func SplitBlocks(content string) (title string, blocks []string) {
	return parseHistoryDoc(content)
}

// IsArchivePointer reports whether block is a rotation pointer rather than
// a real entry — see isArchivePointer. Exported for the same reason as
// SplitBlocks: verification code outside this package needs to tell the
// two apart when checking that no entry bytes were lost across rotation.
func IsArchivePointer(block string) bool {
	return isArchivePointer(block)
}

// archiveTitle is the header line a new archive file is seeded with.
func archiveTitle(doc scribe.Doc) string {
	return strings.TrimRight(defaultHeader[doc], "\n") + " (archived)"
}

// archiveFileName names the archive file a rotation happening at t should
// write to. Archives are grouped by calendar month (CHANGELOG-2026-08.md)
// so repeated rotations within the same month append to one file instead
// of scattering many tiny ones; a rotation in a later month starts a new
// file, so archives stay bounded too instead of becoming one huge one.
func archiveFileName(doc scribe.Doc, t time.Time) string {
	base := strings.TrimSuffix(string(doc), ".md")
	return fmt.Sprintf("%s-%s.md", base, t.Format("2006-01"))
}

// archivePath returns the absolute path of the archive file for a rotation
// of doc happening at t.
func (s *Store) archivePath(doc scribe.Doc, t time.Time) string {
	return filepath.Join(s.dir, "archive", archiveFileName(doc, t))
}

// archiveRelPath returns that same path relative to docs/scribe/, which is
// what gets written into the live doc's pointer block — a reader with the
// live doc open can follow it directly.
func (s *Store) archiveRelPath(doc scribe.Doc, t time.Time) string {
	return filepath.Join("archive", archiveFileName(doc, t))
}

// rotateHistory moves the oldest entry blocks out of blocks and into an
// archive file, oldest first, until title+blocks serializes back under
// doc's cap, or until only the single most recent entry is left — a
// rotation never drops the entry that triggered it, and never splits an
// entry across the live doc and the archive. If even the lone remaining
// entry keeps the doc over cap (a single entry bigger than the whole cap),
// rotation stops there and the doc is left over cap rather than losing or
// truncating anything.
//
// It writes the archive file itself as a side effect — the only place
// archive files are touched — before returning the trimmed block list, so
// a crash between the archive write and AppendHistory's later live-doc
// write can only leave the rotated entries duplicated in both places,
// never missing from both.
func (s *Store) rotateHistory(doc scribe.Doc, title string, blocks []string) ([]string, error) {
	cap := s.capFor(doc)
	if cap <= 0 || int64(len(serializeHistoryDoc(title, blocks))) <= cap {
		return blocks, nil
	}

	// Pointer blocks from earlier rotations always lead and are never
	// themselves candidates for rotation.
	lead := 0
	for lead < len(blocks) && isArchivePointer(blocks[lead]) {
		lead++
	}
	pointerBlocks := append([]string{}, blocks[:lead]...)
	entries := append([]string{}, blocks[lead:]...)

	var archived []string
	for len(entries) > 1 {
		trial := append(append([]string{}, pointerBlocks...), entries...)
		if int64(len(serializeHistoryDoc(title, trial))) <= cap {
			break
		}
		archived = append(archived, entries[0])
		entries = entries[1:]
	}
	if len(archived) == 0 {
		// Nothing could be rotated out without dropping the newest entry —
		// typically a single entry larger than the whole cap. Leave the
		// doc over cap rather than lose or split anything.
		return blocks, nil
	}

	archivePath := s.archivePath(doc, s.now())
	if err := s.appendArchive(archivePath, doc, archived); err != nil {
		return nil, err
	}

	kept := mergePointer(pointerBlocks, len(archived), s.archiveRelPath(doc, s.now()))
	kept = append(kept, entries...)
	return kept, nil
}

// appendArchive adds blocks to the archive file at path — creating the
// file and its directory on the first rotation into a given month,
// appending to it on later rotations within the same month. Like
// AppendHistory, the write is atomic (temp file + rename): a crash mid-write
// leaves the archive either untouched or fully updated, never torn.
func (s *Store) appendArchive(path string, doc scribe.Doc, blocks []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("docs: create archive dir: %w", err)
	}

	var cur string
	if b, err := os.ReadFile(path); err == nil {
		cur = string(b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("docs: read archive %s: %w", path, err)
	}

	title, existing := parseHistoryDoc(cur)
	if title == "" {
		title = archiveTitle(doc)
	}
	existing = append(existing, blocks...)

	if err := atomicWrite(path, []byte(serializeHistoryDoc(title, existing))); err != nil {
		return fmt.Errorf("docs: write archive %s: %w", path, err)
	}
	return nil
}
