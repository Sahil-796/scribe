package seed

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sahil-796/scribe/internal/redact"
)

// ProjectPrompt and DecisionsPrompt each render the seed prompt for one
// doc: the facts Scan recovered, followed by instructions to produce that
// doc, and only that doc, from scratch.
//
// Seed used to ask a single writer call for both PROJECT.md and
// DECISIONS.md at once. They're different jobs — one describes current
// state, the other recovers discrete, deliberate decisions from the same
// evidence — and a combined ask tends to produce a DECISIONS.md that's
// really just PROJECT.md's "why" restated as bullet points, or padded with
// things that read like decisions but aren't. Splitting costs one extra
// writer call; init runs once per repo, so that's an acceptable price (see
// this change's commit message).
// r is phase 04's choke point (docs/phases/04-config-and-safety.md),
// applied the same way internal/worker's buildDocPrompt applies it: the
// fully assembled prompt is redacted in one pass right before it's
// returned. It matters here as much as anywhere else in the pipeline —
// Facts.Files can include a README or manifest that happens to have a
// real credential pasted into it, and scan.go's Ignore-glob filtering
// (which drops whole files before they're ever read) only catches files
// whose *path* looks secret-ish, not a stray key sitting in an otherwise
// ordinary file. r is required: redact.Redactor.Redact panics on a nil
// receiver rather than silently letting repo content through.
func ProjectPrompt(f Facts, r *redact.Redactor) string {
	var b strings.Builder
	b.WriteString(projectInstructions)
	renderFacts(&b, f)
	return r.Redact(b.String())
}

func DecisionsPrompt(f Facts, r *redact.Redactor) string {
	var b strings.Builder
	b.WriteString(decisionsInstructions)
	renderFacts(&b, f)
	return r.Redact(b.String())
}

// renderFacts writes the repo root, directory tree, and every scanned file
// to b, in the same layout both prompts share.
func renderFacts(b *strings.Builder, f Facts) {
	fmt.Fprintf(b, "\n--- repository root ---\n%s\n", f.RepoRoot)

	fmt.Fprintf(b, "\n--- directory tree ---\n%s\n", f.Tree)

	names := make([]string, 0, len(f.Files))
	for name := range f.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(b, "\n--- file: %s ---\n%s\n", name, f.Files[name])
	}
}

// projectInstructions defines the writer's output contract for PROJECT.md.
// Unlike the JSON-object contract shared by internal/worker and replay
// (multiple possible keys per call), a single-doc prompt has nothing to
// disambiguate — the response is just the file's Markdown content, no
// wrapping needed.
const projectInstructions = `You are writing the first draft of PROJECT.md for a repository scribe has
never seen before. You are given the directory tree, the README, package
manifests, and any docs already checked in — nothing else. There is no
transcript history yet; this is day one.

PROJECT.md answers three questions and nothing else: what this project is,
who it's for, and where it stands right now. No history (that's
CHANGELOG.md), no story of how it got here (that's JOURNAL.md), no
decisions and their rationale (that's DECISIONS.md) — just the current
state, as best you can support it from the evidence given.

Prefer saying less over inventing more. A thin README describing a small
CLI tool should produce a short, honest PROJECT.md — a few sentences is a
completely acceptable outcome, and is much better than a confident
paragraph of invented purpose. Do not manufacture a mission statement, a
target audience, or a maturity level the evidence does not support. If you
cannot tell who a project is for, say that plainly rather than guessing.

Respond with ONLY the complete Markdown content of PROJECT.md: no JSON, no
code fence, no commentary before or after it.
`

// decisionsInstructions defines the writer's output contract for
// DECISIONS.md.
const decisionsInstructions = `You are writing the first draft of DECISIONS.md for a repository scribe
has never seen before. You are given the same facts as for PROJECT.md:
directory tree, README, package manifests, and any docs already checked
in.

DECISIONS.md holds one block per deliberate decision you can actually
recover from the evidence — a chosen framework with a stated reason, an
explicit architecture, a tradeoff someone wrote down. Each block is marked
active, dropped, or superseded; a dropped decision carries the reason it
was dropped.

Most repos at this early stage record no real decisions in their README or
manifests. A go.mod naming a module is not "a decision to use Go" worth a
block — it's just the language the code happens to be in. Do not pad the
file with decisions you inferred from the tech stack rather than found
stated somewhere. If nothing in the given facts reads as a documented,
deliberate decision, respond with a DECISIONS.md that plainly says none are
recorded yet — that is a normal, complete answer here, not a failure to
find something.

Respond with ONLY the complete Markdown content of DECISIONS.md: no JSON,
no code fence, no commentary before or after it.
`
