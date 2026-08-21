// Package layout is the pure core of scribe's phase-06 "teammates" work: the
// per-repo choice of how the two append-only history docs (CHANGELOG.md and
// JOURNAL.md) are physically laid out on disk when more than one person shares
// a repo.
//
// The problem it solves. scribe commits its docs to git. In the original
// "shared" layout every teammate appends to the same CHANGELOG.md / JOURNAL.md,
// so two people who both worked today both edit the tail of the same file and
// git reports a merge conflict on nearly every pull — a conflict in
// machine-written markdown that a human then has to resolve by hand for no real
// reason. The "per-session" layout fixes this by construction: each session's
// history entries go to their OWN file under journal/ or changelog/, named with
// a suffix unique to that session, so two people never write the same path and
// there is nothing to conflict on. A separate, mechanically rendered rollup
// (Rollup) stitches those files back into the single CHANGELOG.md / JOURNAL.md
// view a reader still wants.
//
// Why only the history docs. The layout choice applies ONLY to CHANGELOG.md and
// JOURNAL.md. The two state docs (PROJECT.md, DECISIONS.md) describe current
// reality and are rewritten in place — they are shared by nature, one canonical
// copy the team keeps current, so "one file per session" makes no sense for
// them. SessionFilePath rejects them loudly rather than inventing a meaning.
//
// Purity. Everything here is a pure function of its inputs: paths and rendered
// markdown are computed with no filesystem access, no clock and no map-order or
// caller-order leaking into the result. Callers (the worker, docs.Store) do the
// reads and writes; this package only decides names and bytes. That keeps the
// rules trivially testable and byte-deterministic, the same property phase 05's
// render packages rely on so their output can be committed and diffed.
package layout

import "fmt"

// Mode is the per-repo layout choice, stored as Config.Layout. The two values
// mirror internal/install's LayoutPerSession / LayoutShared string constants
// deliberately (this package stays free of a dependency on that one) so a
// value read from config.json round-trips through ParseMode unchanged.
type Mode string

const (
	// PerSession gives each session its own history file plus a generated
	// rollup — conflict-free by construction, and the default (see ParseMode).
	PerSession Mode = "per-session"
	// Shared is the original single-file-per-doc behaviour: everyone appends
	// to one CHANGELOG.md / JOURNAL.md and lives with the merge conflicts.
	Shared Mode = "shared"
)

// ParseMode turns a raw config string into a Mode. The empty string maps to
// the default PerSession — matching how Config treats an unset layout field, so
// a config written before phase 06 (or with the field omitted) behaves as the
// conflict-free layout rather than silently falling back to the shared one that
// causes the conflicts this phase exists to remove. Any other unrecognised
// value is a loud error: a typo in config.json must not be papered over into a
// layout the user did not choose.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "":
		return PerSession, nil
	case string(PerSession):
		return PerSession, nil
	case string(Shared):
		return Shared, nil
	default:
		return "", fmt.Errorf("layout: unknown mode %q (want %q or %q)", s, PerSession, Shared)
	}
}
