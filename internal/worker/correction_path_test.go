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

// TestCorrectionPathOnlyProjectFiresDropsClaimWithNoRecordedReason
// characterises — does NOT fix — the half-fire case OPEN-ITEMS item 29
// warns about: the PROJECT call decides something is stale and rewrites it
// away, but the independent DECISIONS call doesn't reach the same
// conclusion and answers NO_CHANGE. There is nothing in worker.go that
// notices the two calls disagreed; each runDocWriter call is applied (or
// not) entirely on its own.
//
// Today's actual behaviour, pinned here: the run succeeds, PROJECT loses
// the claim, and DECISIONS is left completely untouched — no new block, no
// recorded reason, no error, no log line. Per docs/PLAN.md this is exactly
// the "worse than not trying" outcome item 29 describes: a claim vanishes
// from PROJECT.md and nothing anywhere says why. The fix, if one is wanted,
// is item 29's suggested one — passing all four docs' current content into
// every call — not something this test asserts should happen instead.
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

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v (a half-fired correction is not an error today — that's the point being characterised)", err)
	}

	if store.state[scribe.DocProject] != correctedProject {
		t.Fatalf("PROJECT should still have been corrected: got %q, want %q", store.state[scribe.DocProject], correctedProject)
	}
	if store.state[scribe.DocDecisions] != originalDecisions {
		t.Fatalf("DECISIONS unexpectedly changed — this test characterises the case where it stays silent, got %q, want unchanged %q",
			store.state[scribe.DocDecisions], originalDecisions)
	}
	// The offset still advances: nothing about a half-fired correction looks
	// like a failure to runOnce, because nothing checks the two calls
	// against each other. That's the exact silence item 29 is about.
	if got := tr.offsets["s1"]; got != int64(len(correctionEntries)) {
		t.Fatalf("expected offset to advance despite the half-fired correction (today's behaviour), got %d", got)
	}
}
