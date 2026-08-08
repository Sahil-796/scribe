package wizard

import (
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestFormatPreview_OrdersByAllDocs(t *testing.T) {
	// Deliberately insert in the reverse of scribe.AllDocs order — a map
	// iterates randomly, so if formatPreview ever regressed to `for k, v :=
	// range preview` this test would flake red often enough to catch it.
	preview := map[scribe.Doc]string{
		scribe.DocJournal:   "journal body",
		scribe.DocChangelog: "changelog body",
		scribe.DocDecisions: "decisions body",
		scribe.DocProject:   "project body",
	}

	got := formatPreview(preview)

	var positions []int
	for _, doc := range scribe.AllDocs {
		idx := strings.Index(got, string(doc))
		if idx < 0 {
			t.Fatalf("formatPreview output missing header for %s:\n%s", doc, got)
		}
		positions = append(positions, idx)
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] <= positions[i-1] {
			t.Fatalf("doc headers out of scribe.AllDocs order: positions=%v\n%s", positions, got)
		}
	}
}

func TestFormatPreview_EmptyAndMissingShowNoChanges(t *testing.T) {
	preview := map[scribe.Doc]string{
		scribe.DocProject: "",   // present but empty
		scribe.DocJournal: "  ", // whitespace-only
		// DocDecisions and DocChangelog absent entirely
	}

	got := formatPreview(preview)
	if n := strings.Count(got, "(no changes)"); n != 4 {
		t.Errorf("(no changes) count = %d, want 4 (all docs empty/missing):\n%s", n, got)
	}
}

func TestFormatPreview_ContentIsIncluded(t *testing.T) {
	preview := map[scribe.Doc]string{
		scribe.DocProject: "# Project\n\nSome real content.\n",
	}

	got := formatPreview(preview)
	if !strings.Contains(got, "Some real content.") {
		t.Errorf("formatPreview dropped doc content:\n%s", got)
	}
}

func TestFormatPreview_NoTrailingBlankLines(t *testing.T) {
	preview := map[scribe.Doc]string{
		scribe.DocProject: "content\n",
	}

	got := formatPreview(preview)
	if strings.HasSuffix(got, "\n\n") {
		t.Errorf("formatPreview output has trailing blank line: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("formatPreview output does not end with a single newline: %q", got)
	}
}
