package seed

import (
	"fmt"

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
func Run(repoRoot string, w scribe.Writer) (map[scribe.Doc]string, error) {
	facts, err := Scan(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("seed: scan %s: %w", repoRoot, err)
	}

	projectOut, err := w.Run(ProjectPrompt(facts))
	if err != nil {
		return nil, fmt.Errorf("seed: writer run (PROJECT.md): %w", err)
	}
	project, err := ParseProject(projectOut)
	if err != nil {
		return nil, fmt.Errorf("seed: parse writer output (PROJECT.md): %w", err)
	}

	decisionsOut, err := w.Run(DecisionsPrompt(facts))
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
