package worker

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// edits is the parsed form of what the writer decided to change this run:
// doc name -> new content (full replacement for state docs, an entry to
// append for history docs — see promptInstructions in prompt.go).
type edits map[scribe.Doc]string

// parseEdits decodes the writer's raw stdout per the contract in
// promptInstructions: a JSON object, one key per changed doc. Tolerates the
// common small deviation of a model wrapping the JSON in a ```/```json code
// fence despite being told not to; anything else is a hard error rather than
// a guess, per the plan's "fail loudly rather than writing nonsense
// quietly" (docs/PLAN.md, Risks).
func parseEdits(out string) (edits, error) {
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
		if !docs.IsStateDoc(d) && !docs.IsHistoryDoc(d) {
			return nil, fmt.Errorf("writer returned unknown doc key %q (want one of %v)", k, scribe.AllDocs)
		}
		result[d] = v
	}
	return result, nil
}

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

// applyEdits writes each changed doc via the method its kind requires —
// WriteState for current-state docs, AppendHistory for history docs — so
// the append-only guarantee for CHANGELOG/JOURNAL holds no matter what the
// writer returned. Iterates in scribe.AllDocs order for deterministic
// behavior (matters for test assertions and for reading log output).
func applyEdits(store DocStore, e edits) error {
	for _, d := range scribe.AllDocs {
		content, ok := e[d]
		if !ok || content == "" {
			continue
		}
		if docs.IsStateDoc(d) {
			if err := store.WriteState(d, content); err != nil {
				return err
			}
			continue
		}
		if err := store.AppendHistory(d, content); err != nil {
			return err
		}
	}
	return nil
}
