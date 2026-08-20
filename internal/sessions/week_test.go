package sessions

import (
	"fmt"
	"reflect"
	"testing"
)

func TestWeekKey(t *testing.T) {
	tests := []struct {
		name string
		when string
		want string
	}{
		{"mid-year", "2026-08-20T12:00:00Z", "2026-W34"},
		{"single-digit week zero-padded", "2026-01-19T00:00:00Z", "2026-W04"},
		// ISO-year edge: 2027-01-01 is a Friday in ISO week 53 of ISO YEAR
		// 2026 — the key must use the ISO year, not the calendar year.
		{"jan 1 belongs to prior ISO year", "2027-01-01T00:00:00Z", "2026-W53"},
		// ISO-year edge the other direction: 2026-12-31 is a Thursday that
		// already belongs to ISO week 53 of ISO year 2026 (same calendar
		// year here, but verified against ISOWeek below regardless).
		{"dec 31", "2026-12-31T00:00:00Z", "2026-W53"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			when := ts(tc.when)
			got := WeekKey(when)
			if got != tc.want {
				t.Fatalf("WeekKey(%s) = %q, want %q", tc.when, got, tc.want)
			}
			// Independently verify the key matches Go's own ISOWeek, so the
			// expectations above can't drift from the real definition.
			isoYear, isoWeek := when.ISOWeek()
			wantFromISO := fmt.Sprintf("%04d-W%02d", isoYear, isoWeek)
			if got != wantFromISO {
				t.Fatalf("WeekKey(%s) = %q, disagrees with ISOWeek()-derived %q", tc.when, got, wantFromISO)
			}
			// And confirm the edge case really is a divergence between ISO
			// year and calendar year where the test says it is.
			if tc.name == "jan 1 belongs to prior ISO year" && isoYear == when.Year() {
				t.Fatalf("test premise broken: ISO year %d equals calendar year %d", isoYear, when.Year())
			}
		})
	}
}

func TestByWeek(t *testing.T) {
	recs := []Record{
		{SessionID: "b", Started: ts("2026-08-20T00:00:00Z"), Category: CategoryGeneral}, // W34
		{SessionID: "a", Started: ts("2026-08-20T00:00:00Z"), Category: CategoryFeature}, // W34, tied Started
		{SessionID: "c", Started: ts("2026-08-18T00:00:00Z"), Category: CategoryBug},     // W34 (Tuesday)
		{SessionID: "d", Started: ts("2026-08-10T00:00:00Z"), Category: CategoryGeneral}, // W33
	}
	groups := ByWeek(recs)

	if len(groups) != 2 {
		t.Fatalf("got %d week groups, want 2 (%v)", len(groups), keys(groups))
	}

	w34 := groups["2026-W34"]
	// Within a group: Started asc, then SessionID asc. c (18th) first, then
	// the two tied on the 20th ordered by SessionID: a before b.
	gotIDs := ids(w34)
	wantIDs := []string{"c", "a", "b"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("2026-W34 group order = %v, want %v", gotIDs, wantIDs)
	}

	w33 := groups["2026-W33"]
	if len(w33) != 1 || w33[0].SessionID != "d" {
		t.Fatalf("2026-W33 group = %v, want [d]", ids(w33))
	}
}

func TestByWeekEmpty(t *testing.T) {
	groups := ByWeek(nil)
	if len(groups) != 0 {
		t.Fatalf("ByWeek(nil) returned %d groups, want 0", len(groups))
	}
}

func keys(m map[string][]Record) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
