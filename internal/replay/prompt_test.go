package replay

import (
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func testEntries() []scribe.Entry {
	return []scribe.Entry{
		{Role: "user", Text: "please fix the flaky test", Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
		{Role: "assistant", Text: "done, it was a race on the cache", Timestamp: time.Time{}},
	}
}

func TestBuildChangelogPrompt(t *testing.T) {
	p := buildChangelogPrompt(testEntries())

	for _, want := range []string{
		"CHANGELOG.md",
		"past tense",
		"Discussion is not\nshipment",
		"fix the flaky test",
		"race on the cache",
		"2026-01-01T12:00:00Z",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("changelog prompt missing %q, got:\n%s", want, p)
		}
	}

	// Must not carry journal-only guidance — each call is scoped to its
	// own doc, not a combined ask.
	if strings.Contains(p, "JOURNAL.md") {
		t.Errorf("changelog prompt should not mention JOURNAL.md, got:\n%s", p)
	}
}

func TestBuildJournalPrompt(t *testing.T) {
	p := buildJournalPrompt(testEntries())

	for _, want := range []string{
		"JOURNAL.md",
		"confidently wrong",
		"fix the flaky test",
		"race on the cache",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("journal prompt missing %q, got:\n%s", want, p)
		}
	}

	if strings.Contains(p, "\"CHANGELOG.md\"") {
		t.Errorf("journal prompt should not mention the CHANGELOG.md key, got:\n%s", p)
	}
}

func TestPromptsShareChunkContext(t *testing.T) {
	entries := testEntries()
	cp := buildChangelogPrompt(entries)
	jp := buildJournalPrompt(entries)

	// Both prompts must warn against treating discussion as shipment and
	// against restating/contradicting other chunks — that framing must not
	// drift between the two prompt builders.
	for _, want := range []string{"Discussion is not", "other chunks are handled in separate\ncalls"} {
		if !strings.Contains(cp, want) {
			t.Errorf("changelog prompt missing shared chunk context %q", want)
		}
		if !strings.Contains(jp, want) {
			t.Errorf("journal prompt missing shared chunk context %q", want)
		}
	}
}

func TestRenderChunkFormatsEntries(t *testing.T) {
	entries := testEntries()
	out := renderChunk(entries)

	if !strings.Contains(out, "[2026-01-01T12:00:00Z user]: please fix the flaky test") {
		t.Errorf("expected timestamped entry rendered, got:\n%s", out)
	}
	if !strings.Contains(out, "[assistant]: done, it was a race on the cache") {
		t.Errorf("expected zero-timestamp entry rendered without a timestamp, got:\n%s", out)
	}
}
