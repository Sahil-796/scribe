package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// failureLogFile is the bounded, per-repo record of scribe hook failures,
// kept under scribe.StateDir so `scribe doctor` has something to read back
// (docs/findings/OPEN-ITEMS.md, item 11: a Stop hook exiting non-zero
// doesn't visibly break the session, and nothing else durable recorded that
// it happened where a human would look). Claude Code itself does log a
// failing hook's exit code and stderr, but only inside that one session's
// own transcript JSONL under ~/.claude/projects/ — not aggregated across
// sessions, not bounded, and not a place `scribe doctor` has any business
// reading. This file is scribe's own, purpose-built record.
const failureLogFile = "hook-failures.log"

// maxFailureLines bounds the log so a chronically failing hook (a broken
// writer command, a full disk, whatever) can't grow an unbounded file in
// every repo scribe touches. It's plain line-count trimming, not size-based
// rotation — the entries are short fixed-shape JSON, so a line cap gives a
// predictable, small upper bound on disk with no extra bookkeeping.
const maxFailureLines = 50

// FailureEntry is one line in the bounded hook-failures log: enough to say
// which session's hook run failed, when, and why — not the full payload,
// which would make the log both bigger and a second copy of data the
// transcript already has.
type FailureEntry struct {
	Time      time.Time `json:"time"`
	SessionID string    `json:"session_id,omitempty"`
	Reason    string    `json:"reason"`
}

// failureLogPath returns the log's path for repoRoot. Exported functions in
// this file take repoRoot rather than a path so callers never have to know
// the file's name or that it lives under scribe.StateDir.
func failureLogPath(repoRoot string) string {
	return filepath.Join(repoRoot, scribe.StateDir, failureLogFile)
}

// logFailure appends entry to repoRoot's bounded hook-failures log,
// trimming the oldest lines if needed to stay at or under maxFailureLines.
//
// Every error here is swallowed by design: a hook whose failure-logger can
// itself fail (permission error, full disk, read-only filesystem) must
// still exit with the code its real failure earned — a logger that can
// make a bad situation worse by crashing the hook, or by changing its exit
// code, defeats the one thing the hook is required to do (see hook.go's
// package doc: never blocks, never panics). Losing a log line to a bad
// filesystem is an acceptable trade for that guarantee; losing the hook's
// correct exit code is not.
//
// It is called synchronously, before Run returns, specifically so the line
// is flushed to disk before cmd/scribe/hook.go calls os.Exit with the
// non-zero code — a write queued for "later" would never happen at all.
func logFailure(repoRoot string, entry FailureEntry) {
	defer func() {
		// Belt-and-suspenders on top of the error-swallowing below: nothing
		// in this function is expected to panic, but if the standard
		// library ever surprises us, the hook's exit code must not change.
		_ = recover()
	}()

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}

	path := failureLogPath(repoRoot)
	existing, _ := os.ReadFile(path) // missing or unreadable: treat as empty, best effort
	lines := append(splitNonEmptyLines(existing), string(line))
	if len(lines) > maxFailureLines {
		lines = lines[len(lines)-maxFailureLines:]
	}
	data := []byte(strings.Join(lines, "\n") + "\n")

	// Write-to-temp-then-rename rather than a direct write: a direct write
	// that's interrupted (process killed mid-write, disk fills) can leave a
	// truncated, half-written file; rename is atomic on the same
	// filesystem, so readers (scribe doctor) only ever see the log before
	// or after this update, never mid-update. This doesn't fully solve two
	// hook invocations racing each other in the same repo (a real
	// possibility — Stop can fire concurrently for different sessions), but
	// the log is a best-effort diagnostic, not a source of truth, so an
	// occasional lost line under concurrent failures is an acceptable
	// trade for keeping this cheap and dependency-free.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// splitNonEmptyLines splits b on newlines and drops blank lines, so a
// trailing newline (which every write here produces) doesn't turn into a
// spurious empty entry on the next read-modify-write.
func splitNonEmptyLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	raw := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// RecentFailures reads back repoRoot's bounded hook-failures log, oldest
// first. A missing log (the common case: no hook has ever failed here)
// is not an error — it returns a nil slice. Lines that don't parse as
// FailureEntry JSON are skipped rather than failing the whole read: the
// log is written best-effort (see logFailure) and a truncated or
// concurrently-written line should degrade to "one entry missing," not
// "scribe doctor can't show anything."
func RecentFailures(repoRoot string) ([]FailureEntry, error) {
	data, err := os.ReadFile(failureLogPath(repoRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading hook failure log: %w", err)
	}

	lines := splitNonEmptyLines(data)
	out := make([]FailureEntry, 0, len(lines))
	for _, l := range lines {
		var e FailureEntry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
