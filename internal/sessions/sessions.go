// Package sessions is the store of per-session records scribe keeps for one
// repo. The session index (internal/index) is a view over the same list of
// "what happened in each session". This package owns that list; index reads
// it and never touches the JSON directly.
//
// One record per Claude Code session id. A session spans many worker runs
// (a session is a long conversation; the Stop hook fires after every reply),
// so the record is written once on first sighting and updated in place on
// each later run — see Store.Upsert for exactly which fields are refreshed
// versus fixed.
//
// Persistence lives at <repoRoot>/.scribe/sessions.json (scribe.StateDir is
// ".scribe/"), alongside the queue, lock and offsets — local, gitignored,
// machine state, not a doc anyone reads or commits. On-disk shape is a JSON
// object with a single top-level "sessions" array (see fileFormat); the
// wrapper object rather than a bare array leaves room to add file-level
// metadata later without a format break. Writes are atomic (temp file +
// rename, mirroring internal/docs' atomicWrite) so a crash mid-write can
// never leave a torn or empty sessions.json — the next run reads either the
// old content in full or the new content in full.
package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// Category is the coarse bucket a session's work falls into. It exists so the
// index can label sessions without re-deriving intent from the summary text.
// The set is deliberately tiny and closed — three
// buckets a human skims, not a taxonomy — and anything that isn't clearly a
// feature or a bug is CategoryGeneral.
type Category string

const (
	CategoryFeature Category = "feature"
	CategoryBug     Category = "bug"
	CategoryGeneral Category = "general"
)

// Record is one session's line in the store. It is intentionally small: the
// index wants a headline, a category and enough timestamps to order by, not
// a transcript.
//
// Summary is contracted to be a single line with no embedded newlines — the
// index renders one record per row, so a stray newline would break the
// layout. This package stores Summary
// exactly as given and does NOT police that contract (callers strip
// newlines before calling Upsert); the field doc is the contract, the caller
// is the enforcement. Storing as-is keeps this package from silently
// mangling a caller's data on a rule it can't fully know the intent of.
type Record struct {
	SessionID string    `json:"session_id"`
	Started   time.Time `json:"started"`
	Updated   time.Time `json:"updated"`
	Summary   string    `json:"summary"` // one line, no embedded newlines (contract, not enforced here)
	Category  Category  `json:"category"`
}

// fileFormat is the on-disk shape of sessions.json: a wrapper object with a
// single "sessions" array. The wrapper (rather than persisting a bare
// []Record) means the file is a JSON object, so a future field — a schema
// version, say — can be added without every reader having to tell "old bare
// array" from "new object" apart.
type fileFormat struct {
	Sessions []Record `json:"sessions"`
}

// sessionsFileName is the store's file within scribe.StateDir.
const sessionsFileName = "sessions.json"

// Store is a handle on one repo's .scribe/sessions.json. It holds no state
// beyond the paths; every read and write goes to disk, so two Stores for the
// same repo (or the same Store across runs) never disagree about an
// in-memory cache — the file is the single source of truth.
type Store struct {
	repoRoot string // needed only for messages and to re-derive dir; kept for symmetry with internal/docs' Store
	dir      string // <repoRoot>/.scribe
	path     string // <repoRoot>/.scribe/sessions.json
}

// Open returns a Store for repoRoot. It creates .scribe/ if absent (cheap,
// and every write would need it anyway) but does NOT create sessions.json —
// the file is written lazily on the first Upsert, so a repo that has only
// ever been read still has no sessions.json and All() reports it as empty
// rather than seeding an empty file.
func Open(repoRoot string) (*Store, error) {
	dir := filepath.Join(repoRoot, scribe.StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sessions: create %s: %w", dir, err)
	}
	return &Store{
		repoRoot: repoRoot,
		dir:      dir,
		path:     filepath.Join(dir, sessionsFileName),
	}, nil
}

// Path returns the absolute path of this store's sessions.json. Useful to
// tests and to callers wiring up gitignore.
func (s *Store) Path() string { return s.path }

// load reads and decodes sessions.json. A missing file is not an error — it
// is the normal "nothing recorded yet" state — and yields an empty slice. A
// file that exists but doesn't decode IS a loud error: it means real
// recorded state is corrupt, and silently starting over would throw away
// every session already tracked. Callers get the error and can decide;
// this package never overwrites a file it couldn't read.
func (s *Store) load() ([]Record, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, fmt.Errorf("sessions: read %s: %w", s.path, err)
	}

	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("sessions: parse %s (refusing to discard existing state): %w", s.path, err)
	}
	if f.Sessions == nil {
		f.Sessions = []Record{}
	}
	return f.Sessions, nil
}

// save writes records to sessions.json atomically. The records are sorted
// into canonical order (see sortRecords) before writing so the file on disk
// is deterministic — the same set of sessions always produces byte-identical
// JSON regardless of Upsert order, which keeps diffs of the state file (and
// tests) stable.
func (s *Store) save(records []Record) error {
	sortRecords(records)

	data, err := json.MarshalIndent(fileFormat{Sessions: records}, "", "  ")
	if err != nil {
		return fmt.Errorf("sessions: encode %s: %w", s.path, err)
	}
	data = append(data, '\n')
	if err := atomicWrite(s.path, data); err != nil {
		return fmt.Errorf("sessions: write %s: %w", s.path, err)
	}
	return nil
}

// Upsert inserts the record for r.SessionID, or updates the existing one.
//
// On update it PRESERVES the stored Started — a session's start time is
// fixed on first sighting and a later run must not move it, even though that
// run's Record carries its own (later) Started — and refreshes Updated,
// Summary and Category from r. On insert it stores r exactly as given.
//
// The whole file is rewritten atomically (read, apply, write temp, rename),
// so a crash mid-write leaves the previous sessions.json intact. Reading a
// malformed existing file is a loud error (see load), not a silent reset.
func (s *Store) Upsert(r Record) error {
	records, err := s.load()
	if err != nil {
		return err
	}

	found := false
	for i := range records {
		if records[i].SessionID == r.SessionID {
			// Started is fixed on first sighting; keep the stored value and
			// refresh only the fields a later run can legitimately change.
			started := records[i].Started
			records[i] = r
			records[i].Started = started
			found = true
			break
		}
	}
	if !found {
		records = append(records, r)
	}

	return s.save(records)
}

// All returns every stored record sorted by Started ascending, ties broken
// by SessionID ascending (see sortRecords) for a stable, deterministic
// order the index can render without re-sorting. A missing file
// yields an empty, non-nil slice and a nil error — "no sessions yet" is a
// normal state, not a failure.
func (s *Store) All() ([]Record, error) {
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	sortRecords(records)
	return records, nil
}

// sortRecords orders records in place by Started ascending, breaking ties on
// SessionID ascending. This is the one canonical order used everywhere —
// the file on disk, All(), and each ByWeek group — so no map-iteration or
// insertion order can leak into rendered output. SessionID is a total
// tiebreaker (session ids are unique), so the result is fully deterministic
// even when many sessions share an identical Started.
func sortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		if !records[i].Started.Equal(records[j].Started) {
			return records[i].Started.Before(records[j].Started)
		}
		return records[i].SessionID < records[j].SessionID
	})
}

// atomicWrite writes data to path via temp file + rename, so a reader (or a
// crash) never observes a partially written file. The temp file is created
// in the same directory as path so the rename stays on one filesystem and is
// atomic per POSIX semantics. This mirrors internal/docs' atomicWrite
// deliberately as its own copy — the phase 05 brief calls for the same idiom
// without a cross-package dependency on internal/docs.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup: if we return before the rename succeeds, don't
	// leave the temp file behind.
	succeeded := false
	defer func() {
		if !succeeded {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	succeeded = true
	return nil
}
