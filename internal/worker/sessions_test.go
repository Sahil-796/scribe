package worker

import (
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/sessions"
)

func TestParseSummaryOutput(t *testing.T) {
	tests := []struct {
		name        string
		out         string
		wantSummary string
		wantCat     sessions.Category
		wantErr     bool
	}{
		{
			name:        "well formed feature",
			out:         "CATEGORY: feature\nSUMMARY: added the session index renderer",
			wantSummary: "added the session index renderer",
			wantCat:     sessions.CategoryFeature,
		},
		{
			name:        "well formed bug",
			out:         "CATEGORY: bug\nSUMMARY: fixed the ISO-year boundary key",
			wantSummary: "fixed the ISO-year boundary key",
			wantCat:     sessions.CategoryBug,
		},
		{
			name:        "lowercase labels and general",
			out:         "category: general\nsummary: tidied imports and docs",
			wantSummary: "tidied imports and docs",
			wantCat:     sessions.CategoryGeneral,
		},
		{
			name:        "reversed order",
			out:         "SUMMARY: reordered lines\nCATEGORY: general",
			wantSummary: "reordered lines",
			wantCat:     sessions.CategoryGeneral,
		},
		{
			name:        "unknown category defaults to general",
			out:         "CATEGORY: chore\nSUMMARY: bumped a dependency",
			wantSummary: "bumped a dependency",
			wantCat:     sessions.CategoryGeneral,
		},
		{
			name:        "missing category defaults to general",
			out:         "SUMMARY: just a summary line",
			wantSummary: "just a summary line",
			wantCat:     sessions.CategoryGeneral,
		},
		{
			name:        "wrapped in a code fence",
			out:         "```\nCATEGORY: feature\nSUMMARY: shipped a thing\n```",
			wantSummary: "shipped a thing",
			wantCat:     sessions.CategoryFeature,
		},
		{
			name:        "summary with stray newline is flattened",
			out:         "CATEGORY: bug\nSUMMARY: fixed a\ntwo-line summary",
			wantSummary: "fixed a two-line summary",
			wantCat:     sessions.CategoryBug,
		},
		{
			name:    "no summary line is an error",
			out:     "CATEGORY: feature",
			wantErr: true,
		},
		{
			name:    "empty output is an error",
			out:     "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, cat, err := parseSummaryOutput(tt.out)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got summary=%q cat=%q", summary, cat)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if summary != tt.wantSummary {
				t.Errorf("summary = %q, want %q", summary, tt.wantSummary)
			}
			if cat != tt.wantCat {
				t.Errorf("category = %q, want %q", cat, tt.wantCat)
			}
		})
	}
}

// fakeRecorder captures Upsert calls so the wiring can be asserted without
// touching disk. Satisfies SessionRecorder.
type fakeRecorder struct {
	records []sessions.Record
	err     error
}

func (f *fakeRecorder) Upsert(r sessions.Record) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, r)
	return nil
}

// TestFinishRunRecordsEachSession checks the phase 05 wiring at the worker's
// seam: a run with two sessions upserts one record per session, carrying the
// summary and category the writer returned, and preserves the per-session
// session id.
func TestFinishRunRecordsEachSession(t *testing.T) {
	rec := &fakeRecorder{}
	w := &fakeWriter{outputs: []string{
		"CATEGORY: feature\nSUMMARY: did the work",
		"CATEGORY: bug\nSUMMARY: fixed the thing",
	}}
	// offsets is nil below, so saveOffsets never calls SaveOffset — no need
	// to wire one.
	deps := Deps{
		Writer:   w,
		Sessions: rec,
		Redactor: testRedactor(),
	}

	order := []string{"sess-a", "sess-b"}
	entriesBySession := map[string][]scribe.Entry{
		"sess-a": {{Role: "user", Text: "hello"}},
		"sess-b": {{Role: "user", Text: "world"}},
	}

	if err := finishRun(deps, nil, order, entriesBySession); err != nil {
		t.Fatalf("finishRun: %v", err)
	}
	if len(rec.records) != 2 {
		t.Fatalf("want 2 records upserted, got %d", len(rec.records))
	}
	if rec.records[0].SessionID != "sess-a" || rec.records[1].SessionID != "sess-b" {
		t.Errorf("session ids not preserved in order: %q, %q", rec.records[0].SessionID, rec.records[1].SessionID)
	}
	if rec.records[0].Summary != "did the work" || rec.records[0].Category != sessions.CategoryFeature {
		t.Errorf("record 0 = %+v, want feature/did the work", rec.records[0])
	}
	if rec.records[1].Category != sessions.CategoryBug {
		t.Errorf("record 1 category = %q, want bug", rec.records[1].Category)
	}
	if w.calls != 2 {
		t.Errorf("want one summary writer call per session (2), got %d", w.calls)
	}
}

// TestFinishRunNilSessionsIsNoOp confirms a worker wired without a Sessions
// store simply skips recording — no panic, no writer call.
func TestFinishRunNilSessionsIsNoOp(t *testing.T) {
	w := &fakeWriter{}
	deps := Deps{Writer: w, Redactor: testRedactor()}
	err := finishRun(deps, nil, []string{"sess-a"}, map[string][]scribe.Entry{
		"sess-a": {{Role: "user", Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("finishRun with nil Sessions: %v", err)
	}
	if w.calls != 0 {
		t.Errorf("no summary call expected when Sessions is nil, got %d", w.calls)
	}
}
