// Package scribe holds the types shared across the hook, queue, transcript
// reader and worker. It is the contract between those packages and deliberately
// contains no logic — nothing here may import another internal package.
package scribe

import "time"

// HookPayload is what Claude Code hands the Stop hook on stdin. Field names
// match the JSON the CLI emits. The format is undocumented and may change
// between CLI versions, so unknown fields are ignored and missing required
// fields are a loud error rather than a silent default.
type HookPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

// Trigger is one enqueued unit of work: a single reply ending in a single
// session. Many triggers may coalesce into one worker run.
type Trigger struct {
	SessionID      string    `json:"session_id"`
	TranscriptPath string    `json:"transcript_path"`
	RepoRoot       string    `json:"repo_root"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
}

// Offset records how far into a session's transcript the last run read, so a
// run only ever costs the new bytes. Keyed by session id.
type Offset struct {
	SessionID string    `json:"session_id"`
	Bytes     int64     `json:"bytes"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Entry is one meaningful turn recovered from a transcript: a user prompt or an
// assistant reply, with tool noise and subagent sidechains already dropped.
type Entry struct {
	Role      string    `json:"role"` // "user" or "assistant"
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// Doc names the four documents the writer maintains.
type Doc string

const (
	DocProject   Doc = "PROJECT.md"
	DocDecisions Doc = "DECISIONS.md"
	DocChangelog Doc = "CHANGELOG.md"
	DocJournal   Doc = "JOURNAL.md"
)

// AllDocs is the fixed set, in the order they are presented to the writer.
var AllDocs = []Doc{DocProject, DocDecisions, DocChangelog, DocJournal}

// DocsDir is the fixed folder, relative to the repo root. Decision 9: fixed
// name, nothing to detect, no collision with docs you already keep.
const DocsDir = "docs/scribe"

// StateDir is the per-repo local state folder, relative to the repo root. Holds
// the queue, the lock, the pending flag and the offsets. Gitignored.
const StateDir = ".scribe"

// Writer runs a secondary agent over a prompt and returns what it produced.
// Decision 6: the writer is config, not a hardcoded dependency.
type Writer interface {
	// Name identifies the connector, e.g. "opencode".
	Name() string
	// Run executes the agent against prompt and returns its stdout. It must
	// never block on a terminal — no TTY is attached.
	Run(prompt string) (string, error)
}
