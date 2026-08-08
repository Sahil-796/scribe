// Package hook implements the fast path behind `scribe hook`: read the Stop
// hook's payload from stdin, enqueue a Trigger, exit. It is the one command
// in scribe with a real performance budget (under 50ms, no network, never
// blocks the user's Claude session — see docs/PLAN.md, "Phases > 01") so it
// deliberately does the least work possible and imports as little as
// possible.
//
// This package does not import internal/queue. The caller supplies an
// EnqueueFunc matching queue.Enqueue's exact signature, which keeps this
// package buildable and testable on its own.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// maxPayloadBytes bounds the stdin read so a misbehaving caller can't make
// the hook hang or balloon memory. The real payload is a few hundred bytes.
const maxPayloadBytes = 10 << 20 // 10MB

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitError = 1
)

// EnqueueFunc matches queue.Enqueue's signature exactly:
//
//	func Enqueue(repoRoot string, t scribe.Trigger) error
//
// Passing queue.Enqueue as this argument requires no adapter.
type EnqueueFunc func(repoRoot string, t scribe.Trigger) error

// Run executes the hook fast path: decode the payload from r, resolve the
// repo root from its cwd, and enqueue a trigger. It writes any failure to
// stderr and returns the process exit code — it never panics and never
// blocks past reading stdin and calling enqueue.
//
// Malformed input fails loudly (non-zero exit, message on stderr) rather
// than silently enqueueing nonsense — the transcript/hook payload format is
// undocumented and known to change between CLI versions (see docs/PLAN.md,
// "Risks: the transcript format is undocumented").
//
// A repo scribe was never initialised in (no .scribe dir in cwd or any
// ancestor) is not an error: off is the default everywhere, so Run exits 0
// quietly and enqueues nothing.
func Run(r io.Reader, stderr io.Writer, enqueue EnqueueFunc) int {
	data, err := io.ReadAll(io.LimitReader(r, maxPayloadBytes+1))
	if err != nil {
		fmt.Fprintf(stderr, "scribe hook: reading payload: %v\n", err)
		return ExitError
	}
	if len(data) == 0 {
		fmt.Fprintln(stderr, "scribe hook: empty payload on stdin")
		return ExitError
	}
	if len(data) > maxPayloadBytes {
		fmt.Fprintln(stderr, "scribe hook: payload too large")
		return ExitError
	}

	var payload scribe.HookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		fmt.Fprintf(stderr, "scribe hook: malformed payload: %v\n", err)
		return ExitError
	}

	if payload.SessionID == "" || payload.TranscriptPath == "" || payload.CWD == "" {
		fmt.Fprintln(stderr, "scribe hook: payload missing required field(s) (session_id, transcript_path, cwd)")
		return ExitError
	}

	root, ok := FindRepoRoot(payload.CWD)
	if !ok {
		// Never initialised here. Off is the default everywhere; do nothing.
		return ExitOK
	}

	if enqueue == nil {
		fmt.Fprintln(stderr, "scribe hook: internal error: no enqueue function configured")
		return ExitError
	}

	trigger := scribe.Trigger{
		SessionID:      payload.SessionID,
		TranscriptPath: payload.TranscriptPath,
		RepoRoot:       root,
		EnqueuedAt:     time.Now().UTC(),
	}

	if err := enqueue(root, trigger); err != nil {
		fmt.Fprintf(stderr, "scribe hook: enqueue failed: %v\n", err)
		return ExitError
	}

	return ExitOK
}

// FindRepoRoot walks up from cwd looking for a scribe.StateDir (".scribe")
// directory, the way git walks up looking for ".git". It returns the first
// ancestor (including cwd itself) that has one, or ok=false if none of them
// do — meaning scribe was never initialised for this repo.
func FindRepoRoot(cwd string) (root string, ok bool) {
	if cwd == "" {
		return "", false
	}
	dir := filepath.Clean(cwd)
	for {
		if info, err := os.Stat(filepath.Join(dir, scribe.StateDir)); err == nil && info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
