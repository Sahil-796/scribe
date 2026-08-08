// Package wizard implements the interactive setup form for `scribe init`
// (docs/PLAN.md, phase 02, and the huh decision around line 195: "huh earns
// its place in exactly one command"). Every other scribe command stays
// plain stdout — status, diff and doctor are output, not interaction. This
// is the one place a full-screen form is worth the dependency, because it's
// the whole first impression a new repo gets of the tool: pick an agent,
// pick a model, confirm the docs path, watch the replay progress, and see
// what would be written before it touches the repo.
//
// This package only collects input and renders output. It never writes to
// the repo, never installs anything, and never calls a writer agent —
// cmd/init (out of scope here) takes the Answers and decides what to do
// with them.
//
// scribe also runs unattended, from Stop hooks and CI, with no terminal
// attached. Every exported function here has to detect that and fail fast
// rather than hang waiting on keystrokes nobody can type — see
// IsInteractive, ErrNotInteractive, and the no-TTY fallback in Progress.
package wizard

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// ErrNotInteractive is returned by Ask and Review when no terminal is
// attached to both stdin and stdout. Callers match on this with errors.Is
// to fall back to flags (or a hard failure with a clear message) instead of
// hanging on a form nobody can see — the case that matters is `scribe init`
// invoked from CI or another script.
var ErrNotInteractive = errors.New("wizard: no interactive terminal attached")

// DefaultAgent and DefaultModel are scribe's out-of-the-box choices, seeded
// into the form so accepting every default is a valid path through it.
//
// docs/PLAN.md uses opencode's deepseek-v4-flash-free as an illustrative
// example config; it is not the recommendation. Phase 00's model bakeoff
// (docs/PHASE_00_FINDINGS.md) actually graded the free models against each
// other on a real transcript and found longcat-2.0-free the best of the
// three tested — that is the model that belongs in this default, not the
// example from the plan.
//
// DefaultModel carries the "opencode/" provider prefix because opencode's
// -m/--model flag requires the "provider/model" form (`opencode run --help`:
// "model to use in the format of provider/model") — a bare model name gets
// an opaque UnknownError from opencode with no hint it's a naming problem
// (docs/findings/07-live-run.md, bug 2). Verified against `opencode models`
// on this machine, which lists this model as exactly "opencode/longcat-2.0-free".
const (
	DefaultAgent = "opencode"
	DefaultModel = "opencode/longcat-2.0-free"
)

// Answers is what the user chose.
type Answers struct {
	Agent   string
	Model   string
	DocsDir string
	Proceed bool
}

// Options seeds the form with defaults.
type Options struct {
	RepoRoot     string
	Agents       []string // selectable connectors, e.g. {"opencode", "custom"}
	DefaultAgent string
	DefaultModel string
	DocsDir      string
}

// stdin and stdout are package vars, not direct os.Stdin/os.Stdout
// references, so tests can point IsInteractive at a pipe (guaranteed not a
// terminal) instead of whatever fds the test binary happens to inherit.
var (
	stdin  = os.Stdin
	stdout = os.Stdout
)

// IsInteractive reports whether a form can be shown at all. Both stdin and
// stdout must be a real terminal, not a pipe, a redirected file, or a
// hook's inherited fds — a form needs to read keystrokes from one and
// repaint the other on every keypress.
func IsInteractive() bool {
	return isatty.IsTerminal(stdin.Fd()) && isatty.IsTerminal(stdout.Fd())
}

// withDefaults fills in the zero-valued fields of o. Split out from Ask so
// the defaulting logic is testable without driving an actual form.
func withDefaults(o Options) Options {
	if o.DefaultAgent == "" {
		o.DefaultAgent = DefaultAgent
	}
	if o.DefaultModel == "" {
		o.DefaultModel = DefaultModel
	}
	if o.DocsDir == "" {
		o.DocsDir = scribe.DocsDir
	}
	if len(o.Agents) == 0 {
		o.Agents = []string{DefaultAgent, "custom"}
	}
	// The select can only show a value that's in its option list. If the
	// caller passed a DefaultAgent that isn't among Agents, put it at the
	// front rather than silently seeding the form with an invalid value.
	if !containsString(o.Agents, o.DefaultAgent) {
		o.Agents = append([]string{o.DefaultAgent}, o.Agents...)
	}
	return o
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Ask runs the setup form. It returns ErrNotInteractive when there is no
// TTY (export something like ErrNotInteractive) so `scribe init` can fall
// back to flags in a non-interactive context such as CI.
func Ask(o Options) (Answers, error) {
	if !IsInteractive() {
		return Answers{}, ErrNotInteractive
	}
	o = withDefaults(o)

	agent := o.DefaultAgent
	model := o.DefaultModel
	docsDir := o.DocsDir
	proceed := true

	agentOptions := make([]huh.Option[string], 0, len(o.Agents))
	for _, a := range o.Agents {
		agentOptions = append(agentOptions, huh.NewOption(a, a))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("scribe init").
				Description(fmt.Sprintf("Setting up %s for %s.", docsDir, o.RepoRoot)),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Agent").
				Description("Which connector runs the writer agent.").
				Options(agentOptions...).
				Value(&agent),
			huh.NewInput().
				Title("Model").
				Description("Passed through to the connector, e.g. \""+DefaultModel+"\".").
				Value(&model).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Title("Docs directory").
				Description("Where the four docs live, relative to the repo root.").
				Value(&docsDir).
				Validate(huh.ValidateNotEmpty()),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Run the seed and replay passes now?").
				Affirmative("Yes").
				Negative("Not yet").
				Value(&proceed),
		),
	)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			// Ctrl+C / Esc: not an error, just "the user backed out".
			// Report whatever was chosen so far but with Proceed false.
			return Answers{Agent: agent, Model: model, DocsDir: docsDir, Proceed: false}, nil
		}
		return Answers{}, fmt.Errorf("wizard: setup form: %w", err)
	}

	return Answers{Agent: agent, Model: model, DocsDir: docsDir, Proceed: proceed}, nil
}
