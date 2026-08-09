package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// sizeAfter computes what serializeHistoryDoc would produce for title plus
// blocks, in bytes — used to pick exact cap values for the boundary tests
// below instead of hand-counting characters.
func sizeAfter(title string, blocks ...string) int64 {
	return int64(len(serializeHistoryDoc(title, blocks)))
}

func TestAppendHistoryExactlyAtCapDoesNotRotate(t *testing.T) {
	s := mustOpen(t)

	title, _ := parseHistoryDoc(mustRead(t, s, scribe.DocChangelog))
	cap := sizeAfter(title, "AAAA", "BBBB")

	s.SetCaps(0, cap)
	if err := s.AppendHistory(scribe.DocChangelog, "AAAA"); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := s.AppendHistory(scribe.DocChangelog, "BBBB"); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	got := mustRead(t, s, scribe.DocChangelog)
	if int64(len(got)) != cap {
		t.Fatalf("expected doc to be exactly %d bytes, got %d: %q", cap, len(got), got)
	}
	if !strings.Contains(got, "AAAA") || !strings.Contains(got, "BBBB") {
		t.Fatalf("expected both entries to survive at exactly-at-cap, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "archive")); !os.IsNotExist(err) {
		t.Fatalf("expected no archive dir at exactly-at-cap, stat err = %v", err)
	}
}

func TestAppendHistoryOneByteOverCapRotatesOldest(t *testing.T) {
	s := mustOpen(t)

	title, _ := parseHistoryDoc(mustRead(t, s, scribe.DocChangelog))
	full := sizeAfter(title, "AAAA", "BBBB")

	// One byte less than what two entries need — the second append must
	// tip it over and rotate the oldest (first) entry out.
	s.SetCaps(0, full-1)
	if err := s.AppendHistory(scribe.DocChangelog, "AAAA"); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := s.AppendHistory(scribe.DocChangelog, "BBBB"); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	got := mustRead(t, s, scribe.DocChangelog)
	if strings.Contains(got, "AAAA") {
		t.Fatalf("expected oldest entry to be rotated out of the live doc, got %q", got)
	}
	if !strings.Contains(got, "BBBB") {
		t.Fatalf("expected newest entry to survive, got %q", got)
	}
	if !strings.Contains(got, "archived") {
		t.Fatalf("expected a pointer block referencing the archive, got %q", got)
	}

	archivePath := s.archivePath(scribe.DocChangelog, s.now())
	archived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !strings.Contains(string(archived), "AAAA") {
		t.Fatalf("expected rotated entry to be preserved in archive, got %q", archived)
	}
}

func TestAppendHistorySingleEntryLargerThanCapIsNotLost(t *testing.T) {
	s := mustOpen(t)

	huge := strings.Repeat("X", 1000)
	s.SetCaps(0, 10) // far smaller than the single entry alone

	if err := s.AppendHistory(scribe.DocJournal, huge); err != nil {
		t.Fatalf("append: %v", err)
	}

	got := mustRead(t, s, scribe.DocJournal)
	if !strings.Contains(got, huge) {
		t.Fatalf("expected the oversized single entry to survive in full, got %d bytes", len(got))
	}
	if int64(len(got)) <= s.capFor(scribe.DocJournal) {
		t.Fatalf("expected the doc to legitimately exceed its cap here (nothing else to rotate)")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "archive")); !os.IsNotExist(err) {
		t.Fatalf("expected no archive dir — there was nothing safe to rotate out")
	}
}

func TestAppendHistoryRotatesTwiceInARow(t *testing.T) {
	s := mustOpen(t)
	fixed := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return fixed }

	title, _ := parseHistoryDoc(mustRead(t, s, scribe.DocChangelog))
	// Cap fits exactly one 4-byte entry; every append after the first
	// forces a rotation of whatever's oldest at the time.
	s.SetCaps(0, sizeAfter(title, "AAAA"))

	entries := []string{"AAAA", "BBBB", "CCCC"}
	for _, e := range entries {
		if err := s.AppendHistory(scribe.DocChangelog, e); err != nil {
			t.Fatalf("append %q: %v", e, err)
		}
	}

	got := mustRead(t, s, scribe.DocChangelog)
	if strings.Contains(got, "AAAA") || strings.Contains(got, "BBBB") {
		t.Fatalf("expected both older entries rotated out of the live doc, got %q", got)
	}
	if !strings.Contains(got, "CCCC") {
		t.Fatalf("expected newest entry to survive, got %q", got)
	}

	archivePath := s.archivePath(scribe.DocChangelog, fixed)
	archived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	// Both rotations landed in the SAME month, so both AAAA and BBBB must
	// have accumulated in one archive file rather than the second
	// rotation clobbering the first.
	if !strings.Contains(string(archived), "AAAA") || !strings.Contains(string(archived), "BBBB") {
		t.Fatalf("expected both rotated entries accumulated in one archive file, got %q", archived)
	}
	if strings.Index(string(archived), "AAAA") > strings.Index(string(archived), "BBBB") {
		t.Fatalf("expected archived entries to stay in original (oldest-first) order, got %q", archived)
	}

	entries2, err := os.ReadDir(filepath.Join(s.dir, "archive"))
	if err != nil {
		t.Fatalf("ReadDir archive: %v", err)
	}
	if len(entries2) != 1 {
		t.Fatalf("expected exactly one archive file for two same-month rotations, got %d", len(entries2))
	}
}

func TestAppendHistoryRotationAcrossMonthsUsesSeparateArchiveFiles(t *testing.T) {
	s := mustOpen(t)

	title, _ := parseHistoryDoc(mustRead(t, s, scribe.DocChangelog))
	s.SetCaps(0, sizeAfter(title, "AAAA"))

	s.Now = func() time.Time { return time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC) }
	if err := s.AppendHistory(scribe.DocChangelog, "AAAA"); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := s.AppendHistory(scribe.DocChangelog, "BBBB"); err != nil {
		t.Fatalf("append 2 (rotates in July): %v", err)
	}

	s.Now = func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }
	if err := s.AppendHistory(scribe.DocChangelog, "CCCC"); err != nil {
		t.Fatalf("append 3 (rotates in August): %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(s.dir, "archive"))
	if err != nil {
		t.Fatalf("ReadDir archive: %v", err)
	}
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected two archive files (one per month), got %v", names)
	}
}

func TestAppendHistoryNeverLosesBytesAcrossRotation(t *testing.T) {
	s := mustOpen(t)
	title, _ := parseHistoryDoc(mustRead(t, s, scribe.DocChangelog))
	s.SetCaps(0, sizeAfter(title, "AAAA"))

	all := []string{"AAAA", "BBBB", "CCCC", "DDDD"}
	for _, e := range all {
		if err := s.AppendHistory(scribe.DocChangelog, e); err != nil {
			t.Fatalf("append %q: %v", e, err)
		}
	}

	live := mustRead(t, s, scribe.DocChangelog)
	archivePath := s.archivePath(scribe.DocChangelog, s.now())
	archivedBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	archived := string(archivedBytes)

	for _, e := range all {
		if !strings.Contains(live, e) && !strings.Contains(archived, e) {
			t.Fatalf("entry %q missing from both live doc and archive — bytes were lost", e)
		}
	}
}

func mustRead(t *testing.T, s *Store, doc scribe.Doc) string {
	t.Helper()
	content, err := s.Read(doc)
	if err != nil {
		t.Fatalf("Read %s: %v", doc, err)
	}
	return content
}

// The blocks being split apart here are markdown prose written by a
// language model, so the separator has to be something such a model won't
// emit. A `---` horizontal rule with blank lines around it — the obvious
// choice, and what this originally used — is completely ordinary in
// generated markdown, which would make entries silently split in half.
func TestAppendHistoryEntryContainingHorizontalRuleStaysOneEntry(t *testing.T) {
	s := mustOpen(t)

	entry := "## What happened\n\nFirst part.\n\n---\n\nSecond part, same entry."
	if err := s.AppendHistory(scribe.DocChangelog, entry); err != nil {
		t.Fatalf("append: %v", err)
	}

	_, blocks := parseHistoryDoc(mustRead(t, s, scribe.DocChangelog))
	if len(blocks) != 1 {
		t.Fatalf("an entry containing a --- rule was split into %d blocks: %q", len(blocks), blocks)
	}
	if blocks[0] != entry {
		t.Fatalf("entry did not round-trip:\n got: %q\nwant: %q", blocks[0], entry)
	}
}

// Rotation identifies pointer blocks by an invisible marker, not by their
// visible prose. An entry that happens to open with an italic blockquote
// must not be mistaken for one and made unrotatable.
func TestEntryOpeningWithItalicBlockquoteIsNotAnArchivePointer(t *testing.T) {
	if isArchivePointer("> _a quote the model chose to open with_") {
		t.Fatal("a plain entry was misidentified as an archive pointer")
	}
	if !isArchivePointer(archivePointerText(3, "archive/CHANGELOG-2026-08.md")) {
		t.Fatal("a real archive pointer was not recognised")
	}
}
