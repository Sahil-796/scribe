package seed

import (
	"fmt"
	"strings"
)

// ParseProject and ParseDecisions read one writer call's stdout into that
// doc's content. Since ProjectPrompt/DecisionsPrompt (prompt.go) each ask
// for exactly one doc's Markdown, there's no JSON envelope to decode here —
// unlike internal/worker and internal/replay, which multiplex several
// possible doc keys through one call and need a key to disambiguate, a
// single-doc response is just the content itself (code-fence-tolerant, in
// case the writer wraps it despite being told not to).
//
// An empty response is always an error, for both docs: the seed pass
// exists specifically to produce first-draft content, and a writer that
// returns nothing produced nothing. That's different from a short response
// — "no decisions recorded yet" is a valid, deliberately encouraged
// DECISIONS.md (see decisionsInstructions in prompt.go); it just can't be
// the empty string. Letting an empty response look like success would
// leave a repo's first doc silently missing with no signal anything went
// wrong (see docs/PLAN.md's "fail loudly rather than writing nonsense
// quietly").
func ParseProject(writerOut string) (string, error) {
	return parseDoc(writerOut, "PROJECT.md")
}

func ParseDecisions(writerOut string) (string, error) {
	return parseDoc(writerOut, "DECISIONS.md")
}

func parseDoc(writerOut, docName string) (string, error) {
	cleaned := stripCodeFence(strings.TrimSpace(writerOut))
	if cleaned == "" {
		return "", fmt.Errorf("seed: writer returned empty output for %s", docName)
	}
	return cleaned, nil
}

// stripCodeFence tolerates the common small deviation of a model wrapping
// its content in a ```/```json fence despite being told not to. Mirrors
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
