package seed

import (
	"fmt"
	"sort"
	"strings"
)

// Prompt renders the seed prompt for the writer: the facts Scan recovered,
// followed by instructions to produce PROJECT.md and DECISIONS.md from
// scratch. This mirrors internal/worker/prompt.go's shape (facts, then a
// fixed instructions block, single string) so the writer sees one
// consistent contract whether it's running the live loop or the seed pass.
func Prompt(f Facts) string {
	var b strings.Builder
	b.WriteString(seedInstructions)

	fmt.Fprintf(&b, "\n--- repository root ---\n%s\n", f.RepoRoot)

	fmt.Fprintf(&b, "\n--- directory tree ---\n%s\n", f.Tree)

	names := make([]string, 0, len(f.Files))
	for name := range f.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "\n--- file: %s ---\n%s\n", name, f.Files[name])
	}

	return b.String()
}

// seedInstructions defines the writer's output contract for the seed pass.
// It deliberately reuses the fenced-JSON-object shape from
// internal/worker/prompt.go's promptInstructions (same delimiters, same
// "no prose, no markdown fence" rule) so Parse (parse.go) can share the
// same fence-stripping and JSON-decoding logic as the live loop's
// parseEdits — one output contract for the whole system, not two to keep
// in sync.
const seedInstructions = `You are seeding documentation for a repository scribe has never seen
before. You are given the directory tree, the README, package manifests,
and any docs already checked in. There is no transcript history yet — this
is day one.

Produce first drafts of two documents:

PROJECT.md: what this project is, who it's for, its current state, based
on what you can infer from the facts given. Say what you can support from
the evidence; don't invent goals, roadmaps, or users the facts don't
support. If the evidence is thin, say so plainly rather than padding.

DECISIONS.md: any decisions you can actually recover from the evidence —
a chosen framework, a stated architecture, a documented tradeoff. If
nothing in the given facts reads as a deliberate decision, return a
DECISIONS.md that says so; do not fabricate decisions to fill the file.

Respond with ONLY a single JSON object and nothing else: no prose, no
markdown code fence. Keys are exactly "PROJECT.md" and "DECISIONS.md",
both required — even a thin project has a first-draft PROJECT.md and an
honest "nothing recorded yet" DECISIONS.md. Values are the complete file
content as strings.
`
