package replay

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// edits is the parsed form of what the writer produced for one chunk: doc
// name -> entry text to append.
type edits map[scribe.Doc]string

// allowedReplayDocs are the only keys a replay writer call may return.
// Unlike internal/worker (which maintains all four docs), replay only ever
// seeds history — PROJECT.md and DECISIONS.md are current-state docs that a
// past-tense chunk-by-chunk replay has no sound way to rewrite piecemeal.
var allowedReplayDocs = map[scribe.Doc]bool{
	scribe.DocChangelog: true,
	scribe.DocJournal:   true,
}

// parseReplayEdits decodes the writer's raw stdout per the contract in
// replayPromptInstructions (prompt.go): a JSON object, one key per history
// doc that needs a new entry from this chunk. Follows
// internal/worker/parse.go's conventions — same code-fence tolerance, same
// "fail loudly on anything unexpected" policy — so the two writer-output
// contracts in this codebase stay consistent.
//
// An empty (or whitespace-only) response is treated as "this chunk had
// nothing worth recording," not a failure — that is the expected common
// case (per the prompt, most chunks are pure exploration or dead ends).
// Any non-empty response that isn't valid JSON, or that names a doc outside
// allowedReplayDocs, is "nothing parseable" and is a failure for the
// chunk — the caller (runChunk in replay.go) surfaces that as an error
// rather than silently treating it as success.
func parseReplayEdits(out string) (edits, error) {
	cleaned := stripCodeFence(strings.TrimSpace(out))
	if cleaned == "" {
		return edits{}, nil
	}

	var raw map[string]string
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, fmt.Errorf("writer output is not the expected JSON object: %w", err)
	}

	result := make(edits, len(raw))
	for k, v := range raw {
		d := scribe.Doc(k)
		if !allowedReplayDocs[d] {
			return nil, fmt.Errorf("writer returned unexpected doc key %q (want one of \"CHANGELOG.md\", \"JOURNAL.md\")", k)
		}
		result[d] = v
	}
	return result, nil
}

// stripCodeFence tolerates the common small deviation of a model wrapping
// its JSON in a ```/```json code fence despite being told not to. Mirrors
// internal/worker/parse.go's helper of the same name exactly.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return s
	}
	s = s[nl+1:]
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
