package worker

// Item 29 (docs/findings/OPEN-ITEMS.md): each per-doc writer call sees only
// its own doc's current content (buildDocPrompt in prompt.go), so the
// correction path — pull a stale claim from PROJECT.md, record why in
// DECISIONS.md — only produces both halves if the PROJECT call and the
// DECISIONS call independently reach the same conclusion from the same
// transcript slice. Nothing in this package can make that agreement more
// likely; a fake writer can only choose what each call answers with. What
// these tests do is pin down, precisely, what the worker does with the two
// possible outcomes: both calls agreeing (the case the design is built for),
// and only one firing (the case OPEN-ITEMS item 29 says would be worse than
// not trying, because it silently drops a claim with no recorded reason).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// correctionEntries is one transcript entry engineered to trip the gate
// prefilter (see gateKeywords in prompt.go — "decided" and "drop the" both
// match) so PROJECT and DECISIONS are actually attempted this run.
var correctionEntries = []scribe.Entry{
	{Role: "user", Text: "we decided to drop the old sync-on-save approach entirely"},
}

// TestCorrectionPathBothHalvesFireUpdatesProjectAndRecordsReason is the
// design's intended case: the PROJECT call and the DECISIONS call
// independently agree something got dropped. Per buildDocPrompt's guidance
// (TestBuildDocPromptCorrectionPathGuidance in worker_test.go pins the
// prompt text itself), PROJECT should come back with the stale claim
// removed and DECISIONS should come back with a new block recording why —
// and when both calls actually do that, the worker must apply both writes,
// not just one.
func TestCorrectionPathBothHalvesFireUpdatesProjectAndRecordsReason(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = correctionEntries

	store := newFakeDocStore()
	store.state[scribe.DocProject] = "# Project\n\nSyncs on every save.\n"
	store.state[scribe.DocDecisions] = "# Decisions\n\n- Sync on save: active.\n"

	correctedProject := "# Project\n\nNo longer syncs on save."
	correctedDecisions := "# Decisions\n\n- Sync on save: dropped. Caused too many partial-write races; replaced with explicit save."

	w := &fakeWriter{outputs: []string{
		noChangeSentinel, // CHANGELOG
		noChangeSentinel, // JOURNAL
		"YES",            // gate classification
		correctedProject,
		correctedDecisions,
	}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
		Redactor:       testRedactor(),
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if store.state[scribe.DocProject] != correctedProject {
		t.Fatalf("PROJECT not updated: got %q, want %q", store.state[scribe.DocProject], correctedProject)
	}
	if store.state[scribe.DocDecisions] != correctedDecisions {
		t.Fatalf("DECISIONS not updated: got %q, want %q", store.state[scribe.DocDecisions], correctedDecisions)
	}
}

// The half-fire case from OPEN-ITEMS item 29: the PROJECT call decides
// something is stale and rewrites it away, and the DECISIONS call still
// answers NO_CHANGE despite being told what PROJECT did.
//
// DECISIONS now receives projectRewriteNotice, so this is no longer the
// blind disagreement item 29 described — but a writer is still free to
// decline, and the run must not fail over it: the CHANGELOG/JOURNAL work is
// real and replaying the transcript bytes would help nobody. What must NOT
// happen is silence. This pins both halves of that: the run still succeeds
// and the offset still advances, and the drop is reported on Deps.Log so it
// is visible rather than invisible.
func TestCorrectionPathOnlyProjectFiresDropsClaimWithNoRecordedReason(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = correctionEntries

	store := newFakeDocStore()
	originalProject := "# Project\n\nSyncs on every save.\n"
	originalDecisions := "# Decisions\n\n- Sync on save: active.\n"
	store.state[scribe.DocProject] = originalProject
	store.state[scribe.DocDecisions] = originalDecisions

	correctedProject := "# Project\n\nNo longer syncs on save."

	w := &fakeWriter{outputs: []string{
		noChangeSentinel, // CHANGELOG
		noChangeSentinel, // JOURNAL
		"YES",            // gate classification
		correctedProject, // PROJECT call reaches the correction conclusion...
		noChangeSentinel, // ...DECISIONS call independently does not
	}}

	var log bytes.Buffer
	deps := Deps{
		Queue: q, Docs: store, Writer: w, Log: &log,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
		Redactor:       testRedactor(),
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v (a declined DECISIONS entry is not a failed run)", err)
	}

	if store.state[scribe.DocProject] != correctedProject {
		t.Fatalf("PROJECT should still have been corrected: got %q, want %q", store.state[scribe.DocProject], correctedProject)
	}
	if store.state[scribe.DocDecisions] != originalDecisions {
		t.Fatalf("DECISIONS unexpectedly changed — this test characterises the case where it stays silent, got %q, want unchanged %q",
			store.state[scribe.DocDecisions], originalDecisions)
	}
	// The offset still advances — a declined DECISIONS entry is not a
	// failed run, and replaying these bytes would not change the outcome.
	if got := tr.offsets["s1"]; got != int64(len(correctionEntries)) {
		t.Fatalf("expected offset to advance despite the half-fired correction, got %d", got)
	}
	// The part that actually matters: it must not be silent.
	if !strings.Contains(log.String(), "item 29") {
		t.Fatalf("a dropped claim with no recorded reason must be reported, got log: %q", log.String())
	}

	// And DECISIONS must have been told what PROJECT did — otherwise it was
	// declining blind, which is the condition item 29 was actually about.
	if len(w.prompts) < 5 || !strings.Contains(w.prompts[4], "PROJECT.md was rewritten earlier in this same run") {
		t.Fatal("the DECISIONS call was not given PROJECT.md's rewrite context")
	}
	if !strings.Contains(w.prompts[4], originalProject) {
		t.Fatal("the DECISIONS call was not shown PROJECT.md's previous content")
	}
}
