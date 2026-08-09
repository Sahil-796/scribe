package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// TestRotationCrashBetweenArchiveAndLiveWriteOnlyDuplicatesNeverLoses pins
// down the ordering claim in rotate.go's rotateHistory doc comment: the
// archive file is written first, as a side effect of rotateHistory, and the
// live doc is only overwritten afterward by AppendHistory's own atomicWrite
// call. A crash landing between those two writes can leave the archived
// entries duplicated (present in the archive AND still in the live doc,
// because the live doc was never rewritten to drop them) but must never
// leave them missing from both.
//
// This test simulates exactly that window by calling rotateHistory directly
// (which performs the real archive write) and then deliberately NOT
// performing the live-doc write that AppendHistory would normally do next —
// standing in for a process kill in between. It then simulates the retry a
// real crash would provoke (the worker never advances the offset until
// AppendHistory returns successfully, so the next run re-appends the same
// entry) and checks the eventual on-disk state still has every entry
// somewhere, with duplication being the only acceptable defect.
func TestRotationCrashBetweenArchiveAndLiveWriteOnlyDuplicatesNeverLoses(t *testing.T) {
	s := mustOpen(t)

	title, _ := SplitBlocks(mustRead(t, s, scribe.DocChangelog))
	// Cap fits exactly one entry, so the second append rotates the first.
	s.SetCaps(0, sizeAfter(title, "AAAA"))

	// Get the doc to a state where the next append is about to rotate:
	// exactly one entry present, at cap.
	if err := s.AppendHistory(scribe.DocChangelog, "AAAA"); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	liveBefore, err := os.ReadFile(s.Path(scribe.DocChangelog))
	if err != nil {
		t.Fatalf("read live before simulated crash: %v", err)
	}

	// Reproduce AppendHistory's own logic up to (and including) the archive
	// write, then stop — simulating a crash after rotateHistory's archive
	// write but before AppendHistory's atomicWrite of the live doc.
	curTitle, curBlocks := SplitBlocks(string(liveBefore))
	curBlocks = append(curBlocks, "BBBB")
	if _, err := s.rotateHistory(scribe.DocChangelog, curTitle, curBlocks); err != nil {
		t.Fatalf("rotateHistory (simulated crash point): %v", err)
	}
	// Deliberately do NOT write the live doc here — that's the crash.

	// Post-"crash" state: the archive must already contain the rotated
	// entry (AAAA), and the live doc must be untouched (still exactly what
	// it was before the crashed append), never torn or half-written.
	archivePath := s.archivePath(scribe.DocChangelog, s.now())
	archived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("archive must exist after the simulated crash point: %v", err)
	}
	if !strings.Contains(string(archived), "AAAA") {
		t.Fatalf("archive missing the entry rotateHistory should have archived: %q", archived)
	}
	liveAfterCrash, err := os.ReadFile(s.Path(scribe.DocChangelog))
	if err != nil {
		t.Fatalf("read live after simulated crash: %v", err)
	}
	if string(liveAfterCrash) != string(liveBefore) {
		t.Fatalf("live doc was mutated despite the simulated crash before its write:\nbefore: %q\nafter:  %q", liveBefore, liveAfterCrash)
	}
	if !strings.Contains(string(liveAfterCrash), "AAAA") {
		t.Fatalf("AAAA must still be recoverable from the live doc post-crash (archive+live both have it — duplication, not loss)")
	}

	// Recovery: the worker never advances its offset until AppendHistory
	// returns successfully (internal/worker's runOnce), so a real crash here
	// means the next run retries the same append. Do that for real now.
	if err := s.AppendHistory(scribe.DocChangelog, "BBBB"); err != nil {
		t.Fatalf("retry append after simulated crash: %v", err)
	}

	// Final state: BBBB survives in the live doc, AAAA survives somewhere
	// (archive, possibly duplicated across two rotation attempts) — but
	// never dropped from both.
	finalLive, err := os.ReadFile(s.Path(scribe.DocChangelog))
	if err != nil {
		t.Fatalf("read final live: %v", err)
	}
	finalArchived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read final archive: %v", err)
	}
	if !strings.Contains(string(finalLive), "BBBB") {
		t.Fatalf("BBBB missing from live doc after recovery retry: %q", finalLive)
	}
	if !strings.Contains(string(finalArchived), "AAAA") {
		t.Fatalf("AAAA missing from archive after recovery — the crash window lost an entry: %q", finalArchived)
	}
}
