// Package worker implements the run loop from docs/PLAN.md's "The loop"
// diagram: take the per-repo lock, drain the queue, read new transcript
// bytes, read the four docs, call the writer, apply whatever edits came
// back, save offsets, release the lock, and re-run if a trigger landed
// while we were busy.
//
// Phase 01 ("one repo, one person, nothing configurable yet") hardcodes
// what would later be config — one prompt, no per-doc weighting, no
// redaction. That's deliberate; see docs/PLAN.md phases 01 vs 03/04.
//
// This package depends on two sibling packages by contract, not by import:
// internal/queue (unit 1B) and internal/transcript (unit 1C) were still
// being written when this package was built, so Deps below takes an
// interface for the queue (any *queue.Queue satisfies it structurally, no
// adapter needed) and plain function values for the three transcript
// functions (matching internal/transcript's exact signatures, so wiring is
// literally transcript.Read / transcript.LoadOffset / transcript.SaveOffset
// with no glue code). Both are trivially fakeable, which is how this
// package's tests exercise the ordering guarantees without either sibling
// package existing yet.
package worker

import (
	"fmt"
	"time"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// Queue is the subset of internal/queue's *Queue this package needs. Method
// set matches unit 1B's documented API exactly, so *queue.Queue satisfies
// this with no wrapper.
type Queue interface {
	TryLock() (bool, error)
	Unlock() error
	Drain() ([]scribe.Trigger, error)
	SetPending() error
	TakePending() (bool, error)
}

// DocStore is the subset of internal/docs's *Store this package needs.
// *docs.Store satisfies it; tests use a fake to check ordering without
// touching disk.
type DocStore interface {
	ReadAll() (map[scribe.Doc]string, error)
	WriteState(doc scribe.Doc, content string) error
	AppendHistory(doc scribe.Doc, entry string) error
}

// TranscriptReader matches internal/transcript.Read's signature.
type TranscriptReader func(transcriptPath string, from int64) (entries []scribe.Entry, newOffset int64, err error)

// OffsetLoader matches internal/transcript.LoadOffset's signature.
type OffsetLoader func(repoRoot, sessionID string) (scribe.Offset, error)

// OffsetSaver matches internal/transcript.SaveOffset's signature.
type OffsetSaver func(repoRoot string, o scribe.Offset) error

// Deps wires the worker to the rest of the system. Every field is required.
type Deps struct {
	Queue          Queue
	Docs           DocStore
	Writer         scribe.Writer
	ReadTranscript TranscriptReader
	LoadOffset     OffsetLoader
	SaveOffset     OffsetSaver

	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Run is the loop's entrypoint, called once per Stop-hook-triggered wakeup.
// It takes the lock, processes everything currently queued, and — because a
// trigger can land mid-run — keeps re-running for as long as the pending
// flag keeps getting set, so no reply goes uncovered (decision 3).
//
// If the lock is already held (another run is in flight), Run doesn't wait
// or error: it marks pending and returns. The in-flight run will see the
// flag when it finishes and cover this trigger itself. This is the "busy?
// -> mark pending, exit" branch in docs/PLAN.md's loop diagram.
func Run(deps Deps) error {
	got, err := deps.Queue.TryLock()
	if err != nil {
		return fmt.Errorf("worker: acquire lock: %w", err)
	}
	if !got {
		if err := deps.Queue.SetPending(); err != nil {
			return fmt.Errorf("worker: set pending while busy: %w", err)
		}
		return nil
	}
	defer deps.Queue.Unlock()

	for {
		if err := runOnce(deps); err != nil {
			return err
		}
		pending, err := deps.Queue.TakePending()
		if err != nil {
			return fmt.Errorf("worker: check pending: %w", err)
		}
		if !pending {
			return nil
		}
		// A trigger landed while we were running. Loop and cover it before
		// releasing the lock, rather than releasing and letting the next
		// hook invocation race to re-acquire it.
	}
}

// runOnce drains whatever is currently queued, reads the new transcript
// bytes, calls the writer once, and applies whatever it produced.
//
// Offset ordering: LoadOffset/ReadTranscript happen up front (read-only,
// safe to redo), but SaveOffset only happens at the very end, after
// applyEdits has fully succeeded. If the writer call or the doc write fails
// partway, we return the error *before* saving any offset. The next run
// then re-reads the same transcript bytes and tries again. That's the only
// safe order: if we saved the offset first (or before the write finished)
// and then crashed or errored, the bytes it covered would never be read
// again and that reply's content would be silently lost — there is no
// second source for it, the transcript is trimmed to "new bytes only" by
// design (decision 4). Re-deriving the same doc edits twice is at worst
// redundant work; losing a reply is unrecoverable.
func runOnce(deps Deps) error {
	triggers, err := deps.Queue.Drain()
	if err != nil {
		return fmt.Errorf("worker: drain queue: %w", err)
	}
	if len(triggers) == 0 {
		return nil
	}

	// Multiple triggers can coalesce for the same session (several replies
	// landed before we got the lock). Process each distinct session once,
	// using its transcript path from whichever trigger we saw for it —
	// same session, same path, no reason to re-read the same new bytes
	// more than once.
	bySession := make(map[string]scribe.Trigger)
	order := make([]string, 0, len(triggers))
	for _, t := range triggers {
		if _, seen := bySession[t.SessionID]; !seen {
			order = append(order, t.SessionID)
		}
		bySession[t.SessionID] = t
	}

	var allEntries []scribe.Entry
	type pendingOffset struct {
		repoRoot  string
		sessionID string
		bytes     int64
	}
	var offsetsToSave []pendingOffset

	for _, sessionID := range order {
		trig := bySession[sessionID]

		off, err := deps.LoadOffset(trig.RepoRoot, sessionID)
		if err != nil {
			return fmt.Errorf("worker: load offset for session %s: %w", sessionID, err)
		}

		entries, newOffset, err := deps.ReadTranscript(trig.TranscriptPath, off.Bytes)
		if err != nil {
			return fmt.Errorf("worker: read transcript for session %s: %w", sessionID, err)
		}
		if len(entries) == 0 && newOffset == off.Bytes {
			continue // genuinely nothing new
		}

		allEntries = append(allEntries, entries...)
		offsetsToSave = append(offsetsToSave, pendingOffset{
			repoRoot:  trig.RepoRoot,
			sessionID: sessionID,
			bytes:     newOffset,
		})
	}

	if len(allEntries) == 0 {
		// Nothing new landed (e.g. a trigger fired but the transcript hadn't
		// grown). Nothing to write, nothing to advance.
		return nil
	}

	current, err := deps.Docs.ReadAll()
	if err != nil {
		return fmt.Errorf("worker: read docs: %w", err)
	}

	prompt := buildPrompt(current, allEntries)

	out, err := deps.Writer.Run(prompt)
	if err != nil {
		return fmt.Errorf("worker: writer run: %w", err)
	}

	edits, err := parseEdits(out)
	if err != nil {
		return fmt.Errorf("worker: parse writer output: %w", err)
	}

	if err := applyEdits(deps.Docs, edits); err != nil {
		return fmt.Errorf("worker: apply edits: %w", err)
	}

	// Re-read after applying and compare against the "before" snapshot taken
	// above. This is the fail-open guard from docs/findings/OPEN-ITEMS.md
	// item 8 / docs/findings/00-writer.md: `opencode run` without `--auto`
	// auto-rejects every edit the model attempts and still exits 0 with
	// normal-looking JSON, so "the writer returned success" is not evidence
	// that anything was written. We got this far because allEntries was
	// non-empty (there was genuinely something to write about — the
	// legitimate "nothing new happened" case already returned above, before
	// the writer was ever called), so if the docs are still identical after
	// applying whatever the writer sent back, that's not a quiet no-op, it's
	// the writer silently swallowing an edit. Treat it as a failure and,
	// per the ordering rule above, do not save any offset — the next run
	// will retry the same transcript bytes instead of losing them.
	after, err := deps.Docs.ReadAll()
	if err != nil {
		return fmt.Errorf("worker: read docs after apply: %w", err)
	}
	if docsUnchanged(current, after) {
		return fmt.Errorf("worker: writer %q exited successfully but changed no docs (likely a silent auto-reject — see docs/findings/00-writer.md; forgetting the writer's auto-approve flag makes opencode run reject every edit and still exit 0)", deps.Writer.Name())
	}

	// Only now, after every doc write above has succeeded and actually
	// changed something, do offsets move.
	// See the ordering comment above the function.
	for _, po := range offsetsToSave {
		if err := deps.SaveOffset(po.repoRoot, scribe.Offset{
			SessionID: po.sessionID,
			Bytes:     po.bytes,
			UpdatedAt: deps.now(),
		}); err != nil {
			return fmt.Errorf("worker: save offset for session %s: %w", po.sessionID, err)
		}
	}

	return nil
}

// docsUnchanged reports whether before and after are identical for every
// doc. Used by the fail-open guard in runOnce (see the comment there) — a
// plain map comparison is enough because DocStore.ReadAll already gives us
// a full, comparable snapshot for both state docs (full content) and
// history docs (whatever ReadAll represents them as, e.g. a rendering that
// changes when an entry is actually appended).
func docsUnchanged(before, after map[scribe.Doc]string) bool {
	if len(before) != len(after) {
		return false
	}
	for d, v := range before {
		if after[d] != v {
			return false
		}
	}
	return true
}

// compile-time check that *docs.Store satisfies DocStore.
var _ DocStore = (*docs.Store)(nil)
