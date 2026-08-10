package worker

import (
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/redact"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// TestRedactionChokePointCoversEveryPromptBuilder is the test
// docs/phases/04-config-and-safety.md asks for explicitly: one that fails
// if a prompt builder in this package learns a second way to reach
// transcript or repo content, bypassing internal/redact.
//
// It plants one secret in a transcript entry and one in a PROJECT.md
// snapshot (the closest thing this package has to "repo-derived content" —
// buildDocPrompt embeds current doc content the same way it embeds
// entries), then runs every function in prompt.go that assembles a string
// handed to scribe.Writer.Run, and asserts the secret is absent from every
// one of them.
//
// The list below is deliberately exhaustive and named, not looped over
// reflectively — reflection would let a newly added builder go
// unenumerated just as easily as forgetting to redact it would. Anyone
// adding a fifth builder has to add it here by name for this test to keep
// meaning what it says.
func TestRedactionChokePointCoversEveryPromptBuilder(t *testing.T) {
	const secret = "sk-supersecretvalue1234567890abcdef"
	r := testRedactor()

	entries := []scribe.Entry{
		{Role: "user", Text: "here is the key: api_key=" + secret},
	}

	rendered := map[string]string{
		"buildDocPrompt(DocProject)":   buildDocPrompt(scribe.DocProject, "current project state", entries, CodeWeightCheck, r),
		"buildDocPrompt(DocDecisions)": buildDocPrompt(scribe.DocDecisions, "current decisions state", entries, CodeWeightCheck, r),
		"buildDocPrompt(DocChangelog)": buildDocPrompt(scribe.DocChangelog, "current changelog", entries, CodeWeightCheck, r),
		"buildDocPrompt(DocJournal)":   buildDocPrompt(scribe.DocJournal, "current journal", entries, CodeWeightCheck, r),
		"buildGatePrompt":              buildGatePrompt(entries, r),
		"projectRewriteNotice": projectRewriteNotice(
			"before: api_key="+secret,
			"after: api_key="+secret,
			r,
		),
	}

	for name, prompt := range rendered {
		if strings.Contains(prompt, secret) {
			t.Errorf("%s leaked the planted secret into the rendered prompt:\n%s", name, prompt)
		}
		if !strings.Contains(prompt, "[redacted:") {
			t.Errorf("%s shows no redaction placeholder at all — expected the planted secret to be caught, got:\n%s", name, prompt)
		}
	}
}

// TestBuildDocPromptPanicsOnNilRedactor and
// TestBuildGatePromptPanicsOnNilRedactor pin down the "required, not
// optional" contract at the one layer below Deps validation: even if a
// caller somehow got a nil Redactor past Run's own check (a future
// refactor, a different entry point), the prompt builders themselves
// refuse to silently pass content through. internal/redact.Redactor.Redact
// already panics on a nil receiver (see internal/redact/redact_test.go);
// this confirms that guarantee is actually reachable through this
// package's call sites, not just through calling redact directly.
func TestBuildDocPromptPanicsOnNilRedactor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected buildDocPrompt with a nil Redactor to panic")
		}
	}()
	buildDocPrompt(scribe.DocChangelog, "x", nil, CodeWeightCheck, nil)
}

func TestBuildGatePromptPanicsOnNilRedactor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected buildGatePrompt with a nil Redactor to panic")
		}
	}()
	buildGatePrompt(nil, nil)
}

// TestRunRejectsNilRedactor is Deps' own gate: a worker wired up without a
// Redactor must refuse to run at all, rather than reaching the prompt
// builders (which would panic) or, worse, some future code path that
// doesn't.
func TestRunRejectsNilRedactor(t *testing.T) {
	q := &fakeQueue{}
	tr := newFakeTranscript()
	store := newFakeDocStore()
	w := &fakeWriter{}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
		Redactor: nil,
	}

	err := Run(deps)
	if err == nil {
		t.Fatal("expected Run to reject a nil Redactor")
	}
	if !strings.Contains(err.Error(), "Redactor") {
		t.Fatalf("expected the error to name Redactor, got: %v", err)
	}
	if w.calls != 0 {
		t.Fatal("writer must never be called when Redactor is nil")
	}
}

// compile-time reminder that redact.Redactor is the type Deps.Redactor
// expects — if this ever fails to compile, Deps' field type changed out
// from under this test file.
var _ = (*redact.Redactor)(nil)
