package digest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/sessions"
)

// digestsSubdir is the folder, under scribe.DocsDir, that holds the per-week
// digest files. Fixed name, one file per ISO week (<weekKey>.md).
const digestsSubdir = "digests"

// DigestsDir returns the absolute directory that holds a repo's digest files:
// <repoRoot>/docs/scribe/digests. Exposed so callers and tests can locate the
// files without re-deriving the layout.
func DigestsDir(repoRoot string) string {
	return filepath.Join(repoRoot, scribe.DocsDir, digestsSubdir)
}

// pathFor returns the absolute file path for a given week's digest.
func pathFor(repoRoot, weekKey string) string {
	return filepath.Join(DigestsDir(repoRoot), weekKey+".md")
}

// MaybeWrite writes any missing *past-week* digests derivable from records,
// relative to now, and returns the absolute paths of the files it wrote.
//
// A week is eligible only when all three hold:
//
//   - it is strictly before the ISO week containing now — the current,
//     still-in-progress week is never digested, because more sessions can
//     still land in it and a past week's digest is meant to be immutable;
//   - at least one record falls in it (empty weeks produce no file);
//   - its docs/scribe/digests/<week>.md does not already exist — a past week
//     is written once and never overwritten, so re-running is a no-op and a
//     hand-edited digest is safe.
//
// This is the whole "first run of a new week writes last week's digest"
// mechanism: there is no scheduler, so every run calls MaybeWrite and the
// first run that happens on or after a week boundary materializes the
// just-completed week — while the same pass also backfills any older week that
// never got a file (scribe was off, or `init` replayed a long history at
// once).
//
// Writes are atomic (temp file + rename) and the digests directory is created
// if absent. The returned paths are sorted by week key ascending
// (chronological, since the "YYYY-Www" keys sort lexically) for a
// deterministic result.
func MaybeWrite(repoRoot string, records []sessions.Record, now time.Time) ([]string, error) {
	currentWeek := sessions.WeekKey(now)
	groups := sessions.ByWeek(records)

	// Collect eligible weeks first, then sort, so the returned slice is
	// deterministic regardless of map iteration order.
	var weeks []string
	for week := range groups {
		if week >= currentWeek {
			continue // current or (defensively) future week: never digest it
		}
		weeks = append(weeks, week)
	}
	sort.Strings(weeks)

	if len(weeks) == 0 {
		return nil, nil
	}

	dir := DigestsDir(repoRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("digest: create %s: %w", dir, err)
	}

	var written []string
	for _, week := range weeks {
		path := pathFor(repoRoot, week)
		if _, err := os.Stat(path); err == nil {
			continue // already written; a past week is immutable
		} else if !os.IsNotExist(err) {
			return written, fmt.Errorf("digest: stat %s: %w", path, err)
		}

		body := Render(week, groups[week])
		if err := atomicWrite(path, []byte(body)); err != nil {
			return written, fmt.Errorf("digest: write %s: %w", path, err)
		}
		written = append(written, path)
	}

	return written, nil
}

// atomicWrite writes data to path via temp file + rename, so a reader (or a
// crash) never observes a partially written digest. The temp file is created
// in the same directory as path so the rename stays on one filesystem and is
// atomic per POSIX semantics. This mirrors internal/docs' atomicWrite
// deliberately as its own copy — phase 05 calls for the same idiom without a
// cross-package dependency on internal/docs.
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
