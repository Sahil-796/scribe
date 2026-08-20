package sessions

import (
	"fmt"
	"time"
)

// WeekKey returns the canonical ISO-week key for t, formatted "YYYY-Www"
// with a zero-padded two-digit week, e.g. "2026-W03". This is the single
// definition of "which week a session belongs to" — both the digest (which
// groups runs into weekly sections) and the index share it, so a session
// can never land in one week for one view and a different week for the
// other.
//
// It uses Go's t.ISOWeek(), and deliberately the ISO YEAR it returns, not
// t.Year(). ISO weeks belong to an ISO year that can differ from the
// calendar year in the last days of December or the first days of January:
// e.g. 2027-01-01 is a Friday that falls in ISO week 53 of ISO year 2026,
// so its key is "2026-W53", not "2027-W53". Pairing t.Year() with
// ISOWeek()'s week would produce a nonexistent "2027-W53" and split one real
// week across two keys. Using both values from ISOWeek() keeps every day of
// an ISO week under one key.
func WeekKey(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

// ByWeek groups records by WeekKey(r.Started), returning a map keyed by week
// key. Each group is sorted the same way All() sorts — Started ascending,
// then SessionID ascending — so a consumer can range over a group and render
// it directly. The returned map's own iteration order is undefined (as all
// Go maps are); callers that need weeks in order sort the keys themselves,
// which is why every value slice is pre-sorted but the map is not — there is
// no ordered-map to hand back.
func ByWeek(records []Record) map[string][]Record {
	groups := make(map[string][]Record)
	for _, r := range records {
		key := WeekKey(r.Started)
		groups[key] = append(groups[key], r)
	}
	for key := range groups {
		sortRecords(groups[key])
	}
	return groups
}
