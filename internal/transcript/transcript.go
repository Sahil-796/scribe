// Package transcript reads Claude Code session transcripts incrementally.
//
// A transcript is a JSONL file at the path the Stop hook payload gives
// (scribe.HookPayload.TranscriptPath). This package was built against real
// transcripts found under ~/.claude/projects/<mangled-repo-path>/*.jsonl,
// not against the (undocumented, unstable) format's assumed shape. Notable
// findings from that inspection, in case the format drifts again:
//
//   - Every line is a JSON object with a top-level "type". Only "user" and
//     "assistant" lines ever carry conversation content. Real transcripts
//     also contain "queue-operation", "attachment", "last-prompt",
//     "custom-title", "system", "mode", "frame-link", "ai-title",
//     "pr-link", "permission-mode", "file-history-snapshot",
//     "file-history-delta" and "agent-name" lines — all metadata, none of
//     it a conversation turn. They are skipped silently; see knownMetaTypes.
//   - Tool results are recorded as type "user" (trap #1). They are
//     distinguishable from a real human turn by their content: a
//     tool_result message's content list holds only a "tool_result" block,
//     never a "text" block. This package extracts only "text" blocks, so
//     tool-result turns are dropped for free — no special-casing needed.
//   - The "isSidechain" field is real, but a full survey of 317 real
//     transcripts (docs/findings/09-sidechain.md) found it "true" on zero
//     lines in any top-level session file — subagent turns are instead
//     written to separate "<session>/subagents/*.jsonl" files, which Read
//     never opens, since transcriptPath only ever names the main file.
//     That file separation is what actually keeps subagent chatter out of
//     the docs. The "isSidechain" check below is kept anyway as cheap
//     insurance in case a future format inlines sidechain turns again.
//   - message.content is either a plain string (the common case for real
//     user prompts) or a list of typed blocks: "text", "thinking",
//     "tool_use", "tool_result", "image". Only "text" blocks (and plain
//     string content) become Entry text; everything else is tool noise.
//   - A single logical assistant reply is frequently split across several
//     JSONL lines that share one message.id (one line for "thinking", one
//     per "tool_use", one for the closing "text"). In every real
//     transcript inspected, no single message.id ever produced more than
//     one "text" block, so this package treats each JSONL line as its own
//     candidate Entry rather than joining lines by message.id. If a future
//     CLI version starts splitting a single reply's prose across multiple
//     text-bearing lines, this would surface as multiple consecutive
//     assistant Entries instead of one merged Entry — a readable, non-data
//     losing degradation, not a data-loss failure.
//   - Some assistant lines carry "model": "<synthetic>" — these are
//     CLI-injected placeholders ("No response requested.", session/rate
//     limit notices, auth errors), never something the model said. They
//     are dropped alongside the two documented traps.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// knownMetaTypes are top-level "type" values seen in real transcripts that
// never carry conversation content. They are skipped without comment. Any
// type not in this set and not "user"/"assistant" is unrecognised; see the
// policy note on unknownTypeSeen in Read.
var knownMetaTypes = map[string]bool{
	"queue-operation":       true,
	"attachment":            true,
	"last-prompt":           true,
	"custom-title":          true,
	"system":                true,
	"mode":                  true,
	"frame-link":            true,
	"ai-title":              true,
	"pr-link":               true,
	"permission-mode":       true,
	"file-history-snapshot": true,
	"file-history-delta":    true,
	"agent-name":            true,
	// "summary" is named in the design doc's risk log but was not observed
	// in any real transcript inspected while building this package. Listed
	// defensively so a summary line is treated as known-benign metadata
	// rather than tripping the unrecognised-shape warning.
	"summary": true,
}

// rawLine is the subset of a transcript line's fields this package reads.
// Unknown fields are ignored deliberately (types.go's stance on the
// undocumented format applies here too).
type rawLine struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	Timestamp   string          `json:"timestamp"`
	Message     json.RawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

// contentBlock is one element of a message's content list.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Read parses the transcript at transcriptPath starting at byte offset
// from, returning the real user/assistant turns found and the byte offset
// a subsequent call should resume from.
//
// Partial-line safety: if the file is being appended to concurrently, the
// last line read may be incomplete (no trailing newline yet). Read never
// includes such a line in entries and never advances newOffset past its
// start — the next call will see it complete (or still incomplete, in
// which case it is safely re-read again).
//
// Truncation safety: if from is greater than the file's current size, the
// file was truncated or rotated out from under us. Rather than seek past
// EOF (which would silently yield nothing and desync the offset forever),
// Read resets and reads from byte 0.
//
// Error policy: a single malformed or unrecognised line does not abort the
// run — Read logs it (via the standard "log" package, so it reaches
// stderr and is visible to whoever runs the hook/worker) and continues
// with the next line, per the design doc's "fail loudly rather than
// writing nonsense quietly, but one weird line must not abort the whole
// run" instruction. Read only returns a non-nil error for conditions that
// make the whole read meaningless: the file cannot be opened/stat'd, or an
// I/O error occurs mid-read.
func Read(transcriptPath string, from int64) (entries []scribe.Entry, newOffset int64, err error) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil, from, fmt.Errorf("transcript: open %s: %w", transcriptPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, from, fmt.Errorf("transcript: stat %s: %w", transcriptPath, err)
	}

	start := from
	if start < 0 || start > info.Size() {
		// Truncated, rotated, or a stale/garbage offset. Reset rather than
		// propagate garbage: decision from the Risks section.
		start = 0
	}

	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, from, fmt.Errorf("transcript: seek %s: %w", transcriptPath, err)
		}
	}

	entries = []scribe.Entry{}
	offset := start
	r := bufio.NewReaderSize(f, 64*1024)

	for {
		line, readErr := r.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return entries, offset, fmt.Errorf("transcript: read %s: %w", transcriptPath, readErr)
		}

		if readErr == io.EOF {
			// No trailing newline: either truly nothing left (len(line)==0)
			// or a partial line still being written. Either way it is not
			// safe to consume — stop before it, leave it for next time.
			break
		}

		// Complete line, safe to consume regardless of what's inside it.
		lineLen := int64(len(line))
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			offset += lineLen
			continue
		}

		entry, ok, parseErr := parseLine(trimmed)
		if parseErr != nil {
			log.Printf("transcript: %s: skipping unparsable line at offset %d: %v", transcriptPath, offset, parseErr)
			offset += lineLen
			continue
		}
		if ok {
			entries = append(entries, entry)
		}
		offset += lineLen
	}

	return entries, offset, nil
}

// parseLine interprets one complete, non-blank transcript line. ok reports
// whether it yielded a real conversation Entry. err is non-nil only for
// shapes that are genuinely unparsable or unrecognised — callers treat
// that as "skip and log", not "abort".
func parseLine(line []byte) (entry scribe.Entry, ok bool, err error) {
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return scribe.Entry{}, false, fmt.Errorf("invalid JSON: %w", err)
	}

	if raw.Type == "" {
		return scribe.Entry{}, false, fmt.Errorf("line has no \"type\" field")
	}

	if knownMetaTypes[raw.Type] {
		return scribe.Entry{}, false, nil
	}

	if raw.Type != "user" && raw.Type != "assistant" {
		// Genuinely unrecognised: not a known meta type, not a
		// conversation turn. Per policy, surface it rather than silently
		// drop it, but do not abort the run over it.
		return scribe.Entry{}, false, fmt.Errorf("unrecognised line type %q", raw.Type)
	}

	// Never observed true in a real top-level transcript (see package doc
	// and docs/findings/09-sidechain.md) — subagent turns live in separate
	// files this package never reads. Kept as cheap insurance regardless.
	if raw.IsSidechain {
		return scribe.Entry{}, false, nil
	}

	if len(raw.Message) == 0 {
		return scribe.Entry{}, false, fmt.Errorf("%s line has no \"message\" field", raw.Type)
	}

	var msg rawMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return scribe.Entry{}, false, fmt.Errorf("invalid \"message\" field: %w", err)
	}

	// CLI-injected placeholders, not real model output.
	if raw.Type == "assistant" && msg.Model == "<synthetic>" {
		return scribe.Entry{}, false, nil
	}

	text, err := extractText(msg.Content)
	if err != nil {
		return scribe.Entry{}, false, fmt.Errorf("invalid \"content\" field: %w", err)
	}
	if text == "" {
		// Trap #1 (tool_result-as-"user") lands here: a tool_result-only
		// content list yields no text blocks. Also covers assistant lines
		// that are pure tool_use/thinking with no reply prose.
		return scribe.Entry{}, false, nil
	}

	ts := time.Time{}
	if raw.Timestamp != "" {
		parsed, perr := time.Parse(time.RFC3339, raw.Timestamp)
		if perr != nil {
			return scribe.Entry{}, false, fmt.Errorf("invalid \"timestamp\" field %q: %w", raw.Timestamp, perr)
		}
		ts = parsed
	}

	return scribe.Entry{
		Role:      raw.Type,
		Text:      text,
		Timestamp: ts,
	}, true, nil
}

// extractText pulls the human-readable prose out of a message's content
// field, which is either a plain string or a list of typed blocks. Only
// "text" blocks contribute; "thinking", "tool_use", "tool_result" and
// "image" blocks are tool/model noise and are dropped. Multiple text
// blocks in one line (not observed in practice, but the format allows it)
// are joined with a blank line.
func extractText(content json.RawMessage) (string, error) {
	if len(content) == 0 {
		return "", nil
	}

	// Plain string content: the common case for a real human prompt.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s, nil
	}

	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", fmt.Errorf("content is neither a string nor a block list: %w", err)
	}

	var out bytes.Buffer
	for _, b := range blocks {
		if b.Type != "text" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(b.Text)
	}
	return out.String(), nil
}

// LoadOffset reads the persisted offset for sessionID in repoRoot's state
// directory. A session with no persisted offset yet is not an error — it
// returns a zero offset (Bytes: 0) so a first read starts from the
// beginning of the transcript.
func LoadOffset(repoRoot, sessionID string) (scribe.Offset, error) {
	path := offsetPath(repoRoot, sessionID)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return scribe.Offset{SessionID: sessionID, Bytes: 0}, nil
		}
		return scribe.Offset{}, fmt.Errorf("transcript: load offset for %s: %w", sessionID, err)
	}

	var o scribe.Offset
	if err := json.Unmarshal(data, &o); err != nil {
		// Unlike a stray transcript line, a corrupt offset file is our own
		// state, written atomically by SaveOffset (see below). Corruption
		// here means something outside this package touched it. Silently
		// resetting to 0 would re-read the whole transcript and, worse,
		// risk duplicate content in the docs — the one thing decision 4
		// promises never to do. So this is a loud error, not a reset.
		return scribe.Offset{}, fmt.Errorf("transcript: offset file %s is corrupt: %w", path, err)
	}
	return o, nil
}

// SaveOffset persists o under repoRoot's state directory, keyed by
// o.SessionID. The write is atomic (write to a temp file, then rename)
// so a crash or concurrent read never observes a half-written offset
// file.
func SaveOffset(repoRoot string, o scribe.Offset) error {
	if o.SessionID == "" {
		return fmt.Errorf("transcript: save offset: empty session id")
	}

	dir := filepath.Join(repoRoot, scribe.StateDir, "offsets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("transcript: save offset: %w", err)
	}

	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = time.Now().UTC()
	}

	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return fmt.Errorf("transcript: save offset: %w", err)
	}

	final := offsetPath(repoRoot, o.SessionID)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("transcript: save offset: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("transcript: save offset: %w", err)
	}
	return nil
}

func offsetPath(repoRoot, sessionID string) string {
	return filepath.Join(repoRoot, scribe.StateDir, "offsets", sessionID+".json")
}
