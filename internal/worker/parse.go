package worker

import (
	"fmt"
	"strings"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// edits is the parsed form of what the writer decided to change this run:
// doc name -> new content (full replacement for state docs, an entry to
// append for history docs — see docPrompts in prompt.go). Phase 03 moved
// from one combined JSON-object call to one plain-text call per doc
// (prompt.go), so this map is now assembled by collectEdits in worker.go
// from several parseDocOutput calls rather than by one JSON unmarshal.
type edits map[scribe.Doc]string

// parseDocOutput decodes one per-doc writer call's raw stdout per the
// contract in buildDocPrompt: either the literal noChangeSentinel, meaning
// this doc doesn't need to change, or the doc's new content verbatim.
// Tolerates the common small deviation of a model wrapping its answer in a
// ```/```json code fence despite being told not to.
//
// An empty response (after trimming and fence-stripping) is a hard error
// rather than being treated as "no change" — the prompt always asks for
// either content or the sentinel, so a blank reply means something went
// wrong upstream (docs/findings/00-writer.md's silent-auto-reject failure
// mode), not that the writer made a considered "nothing to say" call. Per
// the plan's "fail loudly rather than writing nonsense quietly" (docs/PLAN.md,
// Risks), that distinction matters: a real NO_CHANGE is common now that
// PROJECT/DECISIONS are gated and every doc call is allowed to decline, so
// treating blank the same as NO_CHANGE would swallow real failures.
func parseDocOutput(out string) (content string, changed bool, err error) {
	cleaned := stripCodeFence(strings.TrimSpace(out))
	if cleaned == "" {
		return "", false, fmt.Errorf("writer returned empty output (want doc content or %s)", noChangeSentinel)
	}
	if strings.EqualFold(cleaned, noChangeSentinel) {
		return "", false, nil
	}
	return cleaned, true, nil
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
