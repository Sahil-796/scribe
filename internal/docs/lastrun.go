// Snapshot recording for `scribe diff` (docs/phases/04-config-and-safety.md,
// unit C). Git is scribe's undo (docs/PLAN.md, decision 10), but nothing
// commits docs/scribe/ — so a `git diff` there shows everything since the
// last human commit, not what the last worker run actually changed. This
// file gives `scribe diff` something narrower to read: a before/after
// snapshot of exactly the docs one run touched, recorded by this package at
// the same two call sites that already write the docs themselves
// (WriteState and AppendHistory, in docs.go).
//
// It is deliberately bounded to the last run only. This is a debugging aid,
// not a second history — CHANGELOG.md and JOURNAL.md already are the
// history, and letting this file grow across runs would mean re-inventing
// that inside .scribe/ with none of the size-cap or rotation machinery that
// keeps the real docs bounded.
package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// lastRunFileName is where the snapshot lives, under scribe.StateDir
// (.scribe/) alongside the queue and lock — local, gitignored, machine
// state, not a doc anyone reads directly.
const lastRunFileName = "lastrun.json"

// DocChange is one doc's content immediately before and after a run that
// touched it. Before is captured on the doc's first write within the run;
// After is overwritten on every write, so a doc rewritten more than once in
// one run nets out to "what it looked like before the run" vs "what it
// looked like when the run finished" rather than some intermediate state.
type DocChange struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// RunSnapshot is the on-disk shape of lastrun.json: when the run that
// produced it started, and what it changed. Docs is keyed only by the docs
// actually written during the run — a doc the writer left untouched never
// gets an entry. A write that leaves a doc byte-for-byte the same it
// started still gets an entry with Before == After: "the writer looked at
// this and made no change" is a fact `scribe diff` can report ("no run has
// happened yet" and "the last run changed nothing" must read differently,
// per the phase 04 spec), and collapsing that into "no entry" would make it
// indistinguishable from a doc the run never looked at.
type RunSnapshot struct {
	RanAt time.Time                `json:"ranAt"`
	Docs  map[scribe.Doc]DocChange `json:"docs"`
}

// lastRunPath returns lastrun.json's path for this store. It lives under
// .scribe/, a sibling of docs/scribe/ rather than inside it — it's local
// run state like the queue and lock, not part of the docs a repo might
// commit.
func (s *Store) lastRunPath() string {
	return filepath.Join(s.repoRoot, scribe.StateDir, lastRunFileName)
}

// ResetRun starts a new run's snapshot, discarding whatever the previous
// run recorded. It must be called once, before any WriteState/AppendHistory
// calls belonging to the new run — recordChange (called from those two
// methods) is a no-op whenever lastrun.json doesn't already exist, so a run
// that never calls ResetRun first is simply not tracked, and one that calls
// it partway through would lose the writes that already happened.
//
// The orchestrator's wiring note (docs/phases/04-config-and-safety.md):
// call Store.ResetRun once per worker run — when the run lock is acquired,
// before the drain/apply loop — not once per drained batch, since a run
// that loops on the pending flag (docs/PLAN.md's "pending set? run again")
// is still one run for this purpose.
func (s *Store) ResetRun() error {
	snap := RunSnapshot{RanAt: s.now(), Docs: map[scribe.Doc]DocChange{}}
	return s.writeRunSnapshot(snap)
}

// recordChange folds one doc's before/after into the run currently being
// tracked, if any. Errors reading or writing lastrun.json are swallowed by
// design: this is a debugging aid layered on top of the real write that
// WriteState/AppendHistory already performed and returned success for by
// the time this is called — a snapshot-recording failure must never turn a
// successful doc write into a reported error, and there is nothing a
// caller could usefully do differently in response to one anyway.
func (s *Store) recordChange(doc scribe.Doc, before, after string) {
	path := s.lastRunPath()
	data, err := os.ReadFile(path)
	if err != nil {
		// No active run being tracked (ResetRun was never called, or this
		// is a fresh repo with no run recorded yet) — nothing to fold this
		// change into.
		return
	}

	var snap RunSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	if snap.Docs == nil {
		snap.Docs = map[scribe.Doc]DocChange{}
	}

	change, seen := snap.Docs[doc]
	if !seen {
		change.Before = before
	}
	change.After = after
	snap.Docs[doc] = change

	_ = s.writeRunSnapshot(snap)
}

// writeRunSnapshot serialises snap and writes it atomically, matching the
// temp-file-plus-rename pattern the rest of this package uses (see
// atomicWrite in docs.go) so a crash mid-write can't leave `scribe diff`
// reading a torn file.
func (s *Store) writeRunSnapshot(snap RunSnapshot) error {
	dir := filepath.Dir(s.lastRunPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("docs: create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("docs: encode lastrun snapshot: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWrite(s.lastRunPath(), data); err != nil {
		return fmt.Errorf("docs: write lastrun snapshot: %w", err)
	}
	return nil
}

// LastRun reads back the most recently recorded run snapshot. ok is false
// when no run has ever called ResetRun for this repo — the "no run has
// happened yet" case `scribe diff` must tell apart from "the last run
// recorded, but touched nothing" (a RunSnapshot with an empty Docs map).
func (s *Store) LastRun() (snap RunSnapshot, ok bool, err error) {
	data, err := os.ReadFile(s.lastRunPath())
	if err != nil {
		if os.IsNotExist(err) {
			return RunSnapshot{}, false, nil
		}
		return RunSnapshot{}, false, fmt.Errorf("docs: read lastrun snapshot: %w", err)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return RunSnapshot{}, false, fmt.Errorf("docs: parse lastrun snapshot: %w", err)
	}
	return snap, true, nil
}
