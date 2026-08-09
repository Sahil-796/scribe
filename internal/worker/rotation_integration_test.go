package worker

// Item 30 (docs/findings/OPEN-ITEMS.md): rotation has never fired outside
// internal/docs's own unit tests, all of which call AppendHistory directly.
// The tests in this file drive rotation through the worker's real run
// loop — Run -> runOnce -> collectEdits -> applyEdits -> DocStore.AppendHistory
// — against a *real* docs.Store writing real files to a temp dir, not the
// fakeDocStore worker_test.go uses everywhere else. Queue, transcript, and
// writer stay fakes (there's no live opencode call to be had here and none
// is wanted — see the repo's scribe-test-with-opencode-only note), but the
// doc store is the genuine article, so a rotation triggered here is a
// rotation triggered by the same code path a real run would take.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// runOneWorkerCycle drives exactly one Run() over a real docs.Store: it
// appends one new transcript entry for session s1, wires up a fresh
// fakeQueue/fakeWriter for that single cycle (mirroring one real Stop-hook
// wakeup), and returns whatever error Run produced.
//
// The writer always answers CHANGELOG with changelogOut and JOURNAL with
// noChangeSentinel. Nothing in the transcript text below trips
// keywordPrefilter (see prompt.go's gateKeywords), so every cycle costs
// exactly those two calls — no gate classification call, no PROJECT/DECISIONS
// noise to keep the conservation bookkeeping simple.
func runOneWorkerCycle(t *testing.T, store *docs.Store, tr *fakeTranscript, cycle int, changelogOut string) {
	t.Helper()

	tr.mu.Lock()
	tr.entries["/repo/t1"] = append(tr.entries["/repo/t1"], scribe.Entry{
		Role: "user",
		Text: "logged a small thing during cycle",
	})
	tr.mu.Unlock()

	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	w := &fakeWriter{outputs: []string{changelogOut, noChangeSentinel}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}
	if err := Run(deps); err != nil {
		t.Fatalf("cycle %d: Run: %v", cycle, err)
	}
}

// collectConservedEntries reads every real-entry block (skipping archive
// pointers) belonging to doc across every archive file plus the live doc,
// oldest first: archive files sorted by name (which sorts chronologically
// for the "DOC-YYYY-MM.md" naming scheme — see archiveFileName in
// internal/docs/rotate.go), then whatever's left in the live doc. This is
// the byte-conservation invariant item 30 actually cares about: not "did an
// archive file appear" but "is every entry that was ever appended still
// findable exactly once across (archives + live), in order".
func collectConservedEntries(t *testing.T, store *docs.Store, doc scribe.Doc) []string {
	t.Helper()

	archiveDir := filepath.Join(filepath.Dir(store.Path(doc)), "archive")
	var out []string

	prefix := strings.TrimSuffix(string(doc), ".md") + "-"
	if ents, err := os.ReadDir(archiveDir); err == nil {
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), prefix) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names) // "CHANGELOG-2026-07.md" < "CHANGELOG-2026-08.md"
		for _, name := range names {
			b, err := os.ReadFile(filepath.Join(archiveDir, name))
			if err != nil {
				t.Fatalf("read archive %s: %v", name, err)
			}
			_, blocks := docs.SplitBlocks(string(b))
			for _, blk := range blocks {
				if !docs.IsArchivePointer(blk) {
					out = append(out, blk)
				}
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("ReadDir archive: %v", err)
	}

	live, err := store.Read(doc)
	if err != nil {
		t.Fatalf("read live %s: %v", doc, err)
	}
	_, blocks := docs.SplitBlocks(live)
	for _, blk := range blocks {
		if !docs.IsArchivePointer(blk) {
			out = append(out, blk)
		}
	}
	return out
}

func assertEntrySlicesEqual(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("entry count mismatch: appended %d, recovered %d\nappended: %v\nrecovered: %v", len(want), len(got), want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("entry %d mismatch:\n want: %q\n got:  %q\nfull appended: %v\nfull recovered: %v", i, want[i], got[i], want, got)
		}
	}
}

// TestRotationFiresRepeatedlyThroughWorkerRunAndConservesAllBytes runs many
// worker cycles against a real docs.Store with a cap small enough that
// rotation fires more than once, and checks the one property that actually
// matters (item 30's own framing): the concatenation of every archive file
// plus whatever's left in the live doc equals, byte for byte and in order,
// everything ever appended. Existing internal/docs unit tests check this by
// calling AppendHistory directly; this one checks it through Run.
func TestRotationFiresRepeatedlyThroughWorkerRunAndConservesAllBytes(t *testing.T) {
	root := t.TempDir()
	store, err := docs.Open(root)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	// Small enough that a second entry always tips the doc over cap — forces
	// rotation attempts on essentially every cycle after the first, not just
	// once.
	store.SetCaps(0, 150)

	tr := newFakeTranscript()
	const cycles = 20

	var appended []string
	for i := 0; i < cycles; i++ {
		entry := entryText(i)
		runOneWorkerCycle(t, store, tr, i, entry)
		appended = append(appended, entry)
	}

	// Rotation must actually have fired — otherwise this test would be
	// exercising nothing item 30 asks about.
	archiveDir := filepath.Join(root, scribe.DocsDir, "archive")
	if _, err := os.Stat(archiveDir); err != nil {
		t.Fatalf("expected rotation to have created an archive dir after %d cycles at a 150-byte cap: %v", cycles, err)
	}

	got := collectConservedEntries(t, store, scribe.DocChangelog)
	assertEntrySlicesEqual(t, appended, got)
}

// TestRotationAcrossMonthBoundaryThroughWorkerRunConservesAllBytes is the
// same drive-it-through-Run setup, but crosses a calendar month mid-run via
// the store's injectable Now (docs.Store.Now — see internal/docs/docs.go).
// archiveFileName groups by "DOC-YYYY-MM.md", so this must produce two
// archive files, not one, and conservation must still hold across both.
func TestRotationAcrossMonthBoundaryThroughWorkerRunConservesAllBytes(t *testing.T) {
	root := t.TempDir()
	store, err := docs.Open(root)
	if err != nil {
		t.Fatalf("docs.Open: %v", err)
	}
	store.SetCaps(0, 150)

	july := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	august := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	store.Now = func() time.Time { return july }

	tr := newFakeTranscript()
	var appended []string

	const beforeBoundary = 6
	for i := 0; i < beforeBoundary; i++ {
		entry := entryText(i)
		runOneWorkerCycle(t, store, tr, i, entry)
		appended = append(appended, entry)
	}

	store.Now = func() time.Time { return august }

	const afterBoundary = 6
	for i := beforeBoundary; i < beforeBoundary+afterBoundary; i++ {
		entry := entryText(i)
		runOneWorkerCycle(t, store, tr, i, entry)
		appended = append(appended, entry)
	}

	archiveDir := filepath.Join(root, scribe.DocsDir, "archive")
	ents, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("ReadDir archive: %v", err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Fatalf("expected two archive files (July + August), got %v", names)
	}

	got := collectConservedEntries(t, store, scribe.DocChangelog)
	assertEntrySlicesEqual(t, appended, got)
}

// entryText builds a distinct, fixed-shape CHANGELOG entry for cycle i —
// short enough that a 150-byte cap comfortably forces rotation within a
// handful of cycles, and distinct enough that assertEntrySlicesEqual can
// tell any two apart.
func entryText(i int) string {
	return "changelog-entry-" + padInt(i) + ": did a small thing"
}

func padInt(i int) string {
	s := "000"
	digits := []byte(s)
	for p := len(digits) - 1; i > 0 && p >= 0; p-- {
		digits[p] = byte('0' + i%10)
		i /= 10
	}
	return string(digits)
}
