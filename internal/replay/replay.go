// Package replay implements phase 02's replay pass: turning a repo's
// existing Claude Code transcripts into CHANGELOG.md and JOURNAL.md history,
// so scribe's docs have real content on day one instead of starting empty.
//
// It builds entirely on internal/transcript for parsing (transcript.Read
// already drops tool noise and sidechains — this package never re-parses a
// transcript line itself) and reuses the writer-output contract shape
// established by internal/worker/{prompt,parse}.go: a single JSON object,
// keys naming which docs changed, code-fence-tolerant. Replay's version of
// that contract is narrower — only the two history docs are ever valid
// output — because a replay pass never touches PROJECT.md/DECISIONS.md;
// see prompt.go.
//
// A session's transcript can be arbitrarily large, so Run never hands one
// writer call a whole session: it splits each session into chunks of at
// most Options.MaxEntriesPerChunk entries (chunkEntries below) and makes one
// writer call per chunk. Progress is checkpointed to disk after every
// successful chunk (see state.go), so a run interrupted partway — killed,
// crashed, or the writer timing out — can be re-invoked and picks up where
// it left off instead of re-billing already-completed chunks.
package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/transcript"
)

// Session is one past transcript belonging to this repo.
type Session struct {
	ID      string
	Path    string
	ModTime time.Time
	Size    int64
}

// defaultMaxEntriesPerChunk bounds how many transcript entries go into a
// single writer prompt when Options.MaxEntriesPerChunk is unset. Chosen to
// be small enough that even a chunk of dense, long entries stays a
// reasonable prompt size, per docs/PLAN.md's "some histories are large, so
// it must be chunked" requirement.
const defaultMaxEntriesPerChunk = 40

// FindSessions locates this repo's transcripts under
// ~/.claude/projects/<mangled-repo-path>/*.jsonl, oldest first (by mtime,
// then by ID to break ties deterministically). Read-only: it only stats and
// lists, never writes into the Claude Code projects directory.
//
// A repo with no transcript directory yet (never used interactively from
// this path, or the path's never been seen before) is not an error — it
// returns an empty slice, the natural "nothing to replay" case for a brand
// new repo.
func FindSessions(repoRoot string) ([]Session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("replay: find home directory: %w", err)
	}

	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("replay: resolve repo root %q: %w", repoRoot, err)
	}

	dir := filepath.Join(home, ".claude", "projects", mangle(abs))

	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Session{}, nil
		}
		return nil, fmt.Errorf("replay: list transcripts in %s: %w", dir, err)
	}

	sessions := make([]Session, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			// Real transcript dirs also contain per-session subdirectories
			// (observed empirically) that are not transcripts themselves.
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("replay: stat %s: %w", name, err)
		}
		sessions = append(sessions, Session{
			ID:      strings.TrimSuffix(name, ".jsonl"),
			Path:    filepath.Join(dir, name),
			ModTime: info.ModTime(),
			Size:    info.Size(),
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		if !sessions[i].ModTime.Equal(sessions[j].ModTime) {
			return sessions[i].ModTime.Before(sessions[j].ModTime)
		}
		return sessions[i].ID < sessions[j].ID // stable tiebreak
	})

	return sessions, nil
}

// mangle reproduces the scheme Claude Code uses to derive a transcript
// directory name from an absolute repo path: every "/" and "." becomes "-".
// Verified empirically against real entries under ~/.claude/projects (see
// package doc); not documented anywhere, so if the CLI changes this scheme
// FindSessions will simply stop finding sessions rather than misbehave.
func mangle(absRepoRoot string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(absRepoRoot)
}

// ChunkStatus says what happened to one chunk, so a caller can tell
// "we did work" from "we skipped work already done" without parsing the
// human-readable progress label (OPEN-ITEMS item 18: cmd/scribe/init.go
// used to match on a " (already done)" suffix, which coupled a control
// decision to display text).
type ChunkStatus int

const (
	// ChunkWritten: the writer ran, its output parsed, and every entry was
	// emitted. This chunk contributed content.
	ChunkWritten ChunkStatus = iota
	// ChunkSkipped: resume state says this chunk was already completed by
	// an earlier run, so no writer call was made.
	ChunkSkipped
	// ChunkFailed: the writer call, the parse, or an emit failed. The run
	// continues, and Run's returned error names the chunk.
	ChunkFailed
)

// Options configures a replay run.
type Options struct {
	RepoRoot string
	Writer   scribe.Writer
	Sessions []Session // nil means FindSessions(RepoRoot)

	MaxEntriesPerChunk int    // zero means a sane default
	StatePath          string // resume state; zero means <repoRoot>/.scribe/replay.json

	Progress func(status ChunkStatus, done, total int, label string)

	// Emit receives one produced history entry. The caller decides where it
	// lands — real docs or a dry-run preview. Replay itself writes no docs.
	Emit func(doc scribe.Doc, entry string) error
}

// Run replays the sessions in chunks, calling Emit for each produced entry
// and Progress as it goes.
//
// Resumability: state is checkpointed to StatePath after every chunk that
// completes cleanly (writer call succeeded, output parsed, every Emit call
// succeeded). A chunk that fails at any of those steps is recorded in the
// returned error but is deliberately NOT marked complete, so a later Run
// call retries it rather than skipping it forever. This package's policy
// (see docs/PLAN.md phase 02, "chunked and resumable"): one bad chunk does
// not abort the whole run — later chunks and other sessions still get
// their shot, since replay is a best-effort backfill, not a
// correctness-critical live path — but a bad chunk is never silently
// treated as success. Run returns a non-nil error listing every chunk that
// failed once the whole pass is done, so callers can tell "fully replayed"
// from "replayed with gaps" and choose to re-run.
func Run(o Options) error {
	if o.Writer == nil {
		return fmt.Errorf("replay: Options.Writer is required")
	}
	if o.RepoRoot == "" {
		return fmt.Errorf("replay: Options.RepoRoot is required")
	}

	maxEntries := o.MaxEntriesPerChunk
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntriesPerChunk
	}

	statePath := o.StatePath
	if statePath == "" {
		statePath = filepath.Join(o.RepoRoot, scribe.StateDir, "replay.json")
	}

	sessions := o.Sessions
	if sessions == nil {
		found, err := FindSessions(o.RepoRoot)
		if err != nil {
			return fmt.Errorf("replay: find sessions: %w", err)
		}
		sessions = found
	}

	st := loadState(statePath)

	type plannedChunk struct {
		sessionID string
		index     int
		total     int // chunk count for this session, for the progress label
		entries   []scribe.Entry
	}
	var plan []plannedChunk

	for _, sess := range sessions {
		entries, _, err := transcript.Read(sess.Path, 0)
		if err != nil {
			return fmt.Errorf("replay: read session %s: %w", sess.ID, err)
		}
		chunks := chunkEntries(entries, maxEntries)
		for i, c := range chunks {
			plan = append(plan, plannedChunk{
				sessionID: sess.ID,
				index:     i,
				total:     len(chunks),
				entries:   c,
			})
		}
	}

	total := len(plan)
	done := 0
	var failures []string

	for _, pc := range plan {
		key := chunkKey(pc.sessionID, pc.index)
		label := fmt.Sprintf("session %s chunk %d/%d", pc.sessionID, pc.index+1, pc.total)

		if st.Completed[key] {
			done++
			reportProgress(o.Progress, ChunkSkipped, done, total, label+" (already done)")
			continue
		}

		if err := runChunk(o, pc.entries); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
			done++
			reportProgress(o.Progress, ChunkFailed, done, total, label+" (failed)")
			continue
		}

		st.Completed[key] = true
		if err := saveState(statePath, st); err != nil {
			return fmt.Errorf("replay: save resume state: %w", err)
		}

		done++
		reportProgress(o.Progress, ChunkWritten, done, total, label)
	}

	if len(failures) > 0 {
		return fmt.Errorf("replay: %d of %d chunk(s) failed:\n%s", len(failures), total, strings.Join(failures, "\n"))
	}
	return nil
}

// runChunk drives one chunk through the writer, parses its output, and
// emits every entry it produced. Nothing about this chunk's outcome is
// persisted to resume state here — the caller decides completion based on
// whether this returns an error.
func runChunk(o Options, entries []scribe.Entry) error {
	out, err := o.Writer.Run(buildReplayPrompt(entries))
	if err != nil {
		return fmt.Errorf("writer: %w", err)
	}

	edits, err := parseReplayEdits(out)
	if err != nil {
		return fmt.Errorf("parse writer output: %w", err)
	}

	if o.Emit == nil {
		return nil
	}

	for _, d := range []scribe.Doc{scribe.DocChangelog, scribe.DocJournal} {
		content, ok := edits[d]
		if !ok || content == "" {
			continue
		}
		if err := o.Emit(d, content); err != nil {
			return fmt.Errorf("emit %s: %w", d, err)
		}
	}
	return nil
}

func reportProgress(fn func(status ChunkStatus, done, total int, label string), status ChunkStatus, done, total int, label string) {
	if fn == nil {
		return
	}
	fn(status, done, total, label)
}

// chunkEntries splits entries into groups of at most max, preserving order.
// A session with no entries produces zero chunks.
func chunkEntries(entries []scribe.Entry, max int) [][]scribe.Entry {
	if len(entries) == 0 {
		return nil
	}
	var chunks [][]scribe.Entry
	for i := 0; i < len(entries); i += max {
		end := i + max
		if end > len(entries) {
			end = len(entries)
		}
		chunks = append(chunks, entries[i:end])
	}
	return chunks
}

func chunkKey(sessionID string, index int) string {
	return fmt.Sprintf("%s#%d", sessionID, index)
}
