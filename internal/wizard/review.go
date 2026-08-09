package wizard

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// Review presents the seeded/replayed content for approval before anything
// is written to the repo (docs/PLAN.md, phase 02: "Dry run by default,
// writing somewhere readable before it touches the repo" — this is that
// somewhere-readable step, made interactive). Returns whether the user
// approved.
func Review(preview map[scribe.Doc]string) (bool, error) {
	if !IsInteractive() {
		return false, ErrNotInteractive
	}

	approve := false
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Review").
				Description(formatPreview(preview)),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Write these to the repo?").
				Affirmative("Write").
				Negative("Discard").
				Value(&approve),
		),
	)

	form = withFormIO(form)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			// Backing out of the review is a "no", not an error — the
			// caller must not write anything.
			return false, nil
		}
		return false, fmt.Errorf("wizard: review form: %w", err)
	}
	return approve, nil
}

// formatPreview renders the preview map as the body of the review screen.
// It walks scribe.AllDocs rather than ranging over the map, because map
// iteration order is randomized in Go and the four docs need to show up in
// the same order every run — otherwise the review screen would reshuffle
// itself between runs for no reason, which is exactly the kind of thing a
// "review before it touches the repo" step should never do.
func formatPreview(preview map[scribe.Doc]string) string {
	var b strings.Builder
	for _, doc := range scribe.AllDocs {
		content, ok := preview[doc]
		fmt.Fprintf(&b, "── %s ──\n", doc)
		if !ok || strings.TrimSpace(content) == "" {
			b.WriteString("(no changes)\n\n")
			continue
		}
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
