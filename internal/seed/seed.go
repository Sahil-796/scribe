package seed

import (
	"fmt"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// Run is Scan + Prompt + w.Run + Parse. It returns content; it does NOT
// write any file — the caller decides whether this is a dry run.
//
// Per docs/PLAN.md phase 02 ("Dry run by default, writing somewhere
// readable before it touches the repo"), init's default behavior is to
// show the operator what it would write, not write it. Keeping that
// decision out of this package (rather than taking a "commit bool" or a
// docs.Store here) means Run can't accidentally skip the dry-run step —
// there is no code path in this package that touches the filesystem beyond
// the read-only Scan.
func Run(repoRoot string, w scribe.Writer) (map[scribe.Doc]string, error) {
	facts, err := Scan(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("seed: scan %s: %w", repoRoot, err)
	}

	prompt := Prompt(facts)

	out, err := w.Run(prompt)
	if err != nil {
		return nil, fmt.Errorf("seed: writer run: %w", err)
	}

	result, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("seed: parse writer output: %w", err)
	}

	return result, nil
}
