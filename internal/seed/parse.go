package seed

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// wantDocs are the only keys Parse accepts, per seedInstructions in
// prompt.go: the seed pass only ever produces PROJECT.md and DECISIONS.md
// — CHANGELOG.md and JOURNAL.md are history docs with nothing to seed from
// yet (that's the replay pass's job, not this one).
var wantDocs = map[scribe.Doc]bool{
	scribe.DocProject:   true,
	scribe.DocDecisions: true,
}

// Parse reads the writer's stdout into seeded content. Keys are limited to
// scribe.DocProject and scribe.DocDecisions.
//
// Unlike internal/worker's parseEdits — where an empty response legitimately
// means "nothing changed this run" — an empty or unparseable response here
// is always an error. The seed pass exists specifically to produce these
// two docs; a writer that returns nothing produced nothing, and letting
// that look like success would leave a repo's first PROJECT.md silently
// missing with no signal anything went wrong (see docs/PLAN.md's "fail
// loudly rather than writing nonsense quietly").
func Parse(writerOut string) (map[scribe.Doc]string, error) {
	cleaned := stripCodeFence(strings.TrimSpace(writerOut))
	if cleaned == "" {
		return nil, fmt.Errorf("seed: writer returned empty output")
	}

	var raw map[string]string
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, fmt.Errorf("seed: writer output is not the expected JSON object: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("seed: writer returned an empty JSON object")
	}

	result := make(map[scribe.Doc]string, len(raw))
	for k, v := range raw {
		d := scribe.Doc(k)
		if !wantDocs[d] {
			return nil, fmt.Errorf("seed: writer returned unexpected doc key %q (want PROJECT.md or DECISIONS.md)", k)
		}
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("seed: writer returned empty content for %q", k)
		}
		result[d] = v
	}
	return result, nil
}

// stripCodeFence tolerates the common small deviation of a model wrapping
// its JSON in a ```/```json fence despite being told not to. Mirrors
// internal/worker/parse.go's stripCodeFence exactly (kept as a local copy,
// not an import, since that one is unexported — see this package's doc
// comment on matching the worker's conventions rather than depending on
// its internals).
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
