package seed

import (
	"fmt"

	"github.com/Sahil-796/scribe/internal/redact"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// Run is Scan, then one writer call per doc (ProjectPrompt/ParseProject,
// DecisionsPrompt/ParseDecisions), and returns both. It does NOT write any
// file — the caller decides whether this is a dry run.
//
// Per docs/PLAN.md phase 02 ("Dry run by default, writing somewhere
// readable before it touches the repo"), init's default behavior is to
// show the operator what it would write, not write it. Keeping that
// decision out of this package (rather than taking a "commit bool" or a
// docs.Store here) means Run can't accidentally skip the dry-run step —
// there is no code path in this package that touches the filesystem beyond
// the read-only Scan.
//
// Two writer calls, not one: see ProjectPrompt/DecisionsPrompt's doc
// comment in prompt.go for why PROJECT.md and DECISIONS.md get separate
// prompts. If the first call fails, the second is never made — there's no
// point asking for DECISIONS.md if init is going to fail anyway, and it
// keeps a scan/writer failure here reported the same way seed.Run always
// has (name the doc, wrap the error, return nothing).
// The redactor is required, not optional. Seeding reads the repo itself —
// README, package files, existing docs — and a repo is exactly where a
// committed .env or a key pasted into a config file lives. A nil here would
// mean the one pass that reads the most files reads them unfiltered.
func Run(repoRoot string, w scribe.Writer, r *redact.Redactor) (map[scribe.Doc]string, error) {
	if r == nil {
		return nil, fmt.Errorf("seed: a redactor is required — seeding reads the repo and must never send it unredacted")
	}

	facts, err := Scan(repoRoot, r)
	if err != nil {
		return nil, fmt.Errorf("seed: scan %s: %w", repoRoot, err)
	}

	projectOut, err := w.Run(ProjectPrompt(facts, r))
	if err != nil {
		return nil, fmt.Errorf("seed: writer run (PROJECT.md): %w", err)
	}
	project, err := ParseProject(projectOut)
	if err != nil {
		return nil, fmt.Errorf("seed: parse writer output (PROJECT.md): %w", err)
	}

	decisionsOut, err := w.Run(DecisionsPrompt(facts, r))
	if err != nil {
		return nil, fmt.Errorf("seed: writer run (DECISIONS.md): %w", err)
	}
	decisions, err := ParseDecisions(decisionsOut)
	if err != nil {
		return nil, fmt.Errorf("seed: parse writer output (DECISIONS.md): %w", err)
	}

	return map[scribe.Doc]string{
		scribe.DocProject:   project,
		scribe.DocDecisions: decisions,
	}, nil
}
