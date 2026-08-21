package replay

import (
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/redact"
	"github.com/Sahil-796/scribe/internal/scribe"
)

func testRedactor() *redact.Redactor {
	return redact.New(
		[]string{"api_key", "token", "password", "secret"},
		[]string{"**/.env*", "**/secrets/**"},
	)
}

func testEntries() []scribe.Entry {
	return []scribe.Entry{
		{Role: "user", Text: "please fix the flaky test", Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
		{Role: "assistant", Text: "done, it was a race on the cache", Timestamp: time.Time{}},
	}
}

func TestBuildChangelogPrompt(t *testing.T) {
	p := buildChangelogPrompt(testEntries(), testRedactor())

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
	p := buildJournalPrompt(testEntries(), testRedactor())

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
	cp := buildChangelogPrompt(entries, testRedactor())
	jp := buildJournalPrompt(entries, testRedactor())

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

// TestRedactionChokePointCoversEveryPromptBuilder is this package's half of
// the phase 04 requirement (docs/phases/04-config-and-safety.md): a test
// that fails if a prompt builder in this package learns a second way to
// reach transcript content, bypassing internal/redact. Both builders this
// package owns are enumerated here by name.
func TestRedactionChokePointCoversEveryPromptBuilder(t *testing.T) {
	const secret = "sk-supersecretvalue1234567890abcdef"
	entries := []scribe.Entry{{Role: "user", Text: "api_key=" + secret}}
	r := testRedactor()

	rendered := map[string]string{
		"buildChangelogPrompt": buildChangelogPrompt(entries, r),
		"buildJournalPrompt":   buildJournalPrompt(entries, r),
	}

	for name, prompt := range rendered {
		if strings.Contains(prompt, secret) {
			t.Errorf("%s leaked the planted secret into the rendered prompt:\n%s", name, prompt)
		}
		if !strings.Contains(prompt, "[redacted:") {
			t.Errorf("%s shows no redaction placeholder — expected the planted secret to be caught, got:\n%s", name, prompt)
		}
	}
}

func TestBuildChangelogPromptPanicsOnNilRedactor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected buildChangelogPrompt with a nil Redactor to panic")
		}
	}()
	buildChangelogPrompt(testEntries(), nil)
}

func TestBuildJournalPromptPanicsOnNilRedactor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected buildJournalPrompt with a nil Redactor to panic")
		}
	}()
	buildJournalPrompt(testEntries(), nil)
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
