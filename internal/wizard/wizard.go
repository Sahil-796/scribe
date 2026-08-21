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
	"io"
	"os"

	"github.com/charmbracelet/bubbles/key"
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

// GradedModels are the models phase 00's bakeoff actually ran against a real
// transcript and ranked, offered as the model choice rather than a single
// hardcoded default. Order is the bakeoff's ranking, best first, so the
// pre-selected option is the recommended one without the choice being made
// silently on the operator's behalf. See docs/findings/00-models.md.
var GradedModels = []struct{ ID, Note string }{
	{"opencode/longcat-2.0-free", "best of the three tested — reads like a human wrote it"},
	{"opencode/mimo-v2.5-free", "strong second — clean and correctly scoped"},
	{"opencode/deepseek-v4-flash-free", "right substance, visibly glitched prose"},
}

// ModelOther is the sentinel the model select uses for "none of these" — it
// reveals a free-text field rather than limiting anyone to the three models
// that happened to be graded once.
const ModelOther = "other"

// Layout is how a repo's two history docs are organised across the people
// working in it. The answer is genuinely per-repo — a solo repo and a shared
// one want different things — so phase 06 (which built the layout semantics in
// internal/layout and made docs.Store honour them) re-adds the question at
// onboarding, reversing OPEN-ITEMS item 31's temporary removal. LayoutPerSession
// is the default: it is conflict-free by construction, which is the whole
// reason the layout exists.
type Layout string

const (
	// LayoutPerSession gives each session its own file, aggregated by the
	// session index. No write conflicts by construction.
	LayoutPerSession Layout = "per-session"
	// LayoutShared keeps the four docs as single shared files that everyone
	// appends to. Simplest, but concurrent writes can conflict.
	LayoutShared Layout = "shared"
)

// Answers is what the user chose.
type Answers struct {
	Agent   string
	Model   string
	DocsDir string
	// DocsInGit is whether the docs are committed to the repo or kept out of
	// it via .gitignore. Asked rather than assumed (OPEN-ITEMS item 3).
	DocsInGit bool
	// Layout is the per-repo history layout chosen at onboarding (phase 06):
	// LayoutPerSession (default, conflict-free) or LayoutShared.
	Layout  Layout
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

// formInput and formOutput let tests wire a form's actual keystroke source
// and render target to something other than the real process's stdin/stdout
// — a pty, in practice, since bubbletea insists on a term.File to drive raw
// mode. nil (the default) leaves huh's own default in place, which is
// os.Stdin/os.Stdout. stdin/stdout above only gate IsInteractive's check;
// these two are what the form actually reads from and writes to, and in a
// pty-backed test both pairs need to point at the same tty.
var (
	formInput  io.Reader
	formOutput io.Writer
)

// withFormIO applies formInput/formOutput to f when a test has set them,
// leaving huh's defaults (os.Stdin/os.Stdout) alone otherwise.
func withFormIO(f *huh.Form) *huh.Form {
	if formInput != nil {
		f = f.WithInput(formInput)
	}
	if formOutput != nil {
		f = f.WithOutput(formOutput)
	}
	return f
}

// withAbortKeys binds Esc alongside huh's default ctrl+c-only Quit
// (OPEN-ITEMS item 32). huh v1.0.0's own default keymap only ever sets
// Quit to "ctrl+c" (keymap.go); an operator who wants out and presses Esc
// instead gets nothing and, absent this, would eventually kill the
// terminal — leaving a half-onboarded repo behind. Esc-to-cancel is a
// standard enough terminal convention that it should just work here too.
//
// This goes through huh's own WithKeyMap rather than intercepting key
// messages ourselves, so it stays a keymap change, not a parallel input
// path. huh.NewDefaultKeyMap() is called fresh each time (not shared as a
// package var) because Form.WithKeyMap propagates the same *KeyMap
// pointer down into every field, and two forms sharing one mutable keymap
// would let a change to one bleed into the other.
//
// The one place this trades something away: a Select field's own keymap
// binds a bare Esc to leaving filter mode (SetFilter/ClearFilter in
// keymap.go), but Form.Update checks its own Quit binding before the
// keypress ever reaches the focused field (form.go). So while a select is
// mid-filter (after pressing "/"), Esc now aborts the whole form instead
// of just clearing the filter — verified live through a pty
// (TestAsk_PTY_Esc_DuringFilter_AbortsInsteadOfClearingFilter). The
// operator isn't stuck: Enter still both applies the filter and commits
// the highlighted option, so filtering itself remains usable, but "Esc to
// step back to the unfiltered list" is gone. That's judged an acceptable
// trade for having a working abort key at all.
func withAbortKeys(f *huh.Form) *huh.Form {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return f.WithKeyMap(km)
}

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
	docsDir := o.DocsDir
	proceed := true
	docsInGit := false
	// layoutChoice is the history layout select's value (phase 06). It
	// defaults to the conflict-free per-session layout; the string form is
	// resolved back into a Layout at the end via the select's own values.
	layoutChoice := string(LayoutPerSession)

	// modelChoice is the select's value; customModel is the free-text field
	// behind ModelOther. They're resolved into one model at the end.
	modelChoice := o.DefaultModel
	customModel := ""

	agentOptions := make([]huh.Option[string], 0, len(o.Agents))
	for _, a := range o.Agents {
		agentOptions = append(agentOptions, huh.NewOption(a, a))
	}

	modelOptions := make([]huh.Option[string], 0, len(GradedModels)+1)
	for _, m := range GradedModels {
		modelOptions = append(modelOptions, huh.NewOption(m.ID+" — "+m.Note, m.ID))
	}
	modelOptions = append(modelOptions, huh.NewOption("something else…", ModelOther))
	if !isGradedModel(modelChoice) {
		// A model passed via --model that isn't one of the graded three
		// shouldn't silently reset the select to longcat.
		customModel = modelChoice
		modelChoice = ModelOther
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("scribe init").
				Description(fmt.Sprintf("Setting up %s in %s.\nThe four docs stay current after every reply.", docsDir, o.RepoRoot)),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Agent").
				Description("Which connector runs the writer agent.").
				Options(agentOptions...).
				Value(&agent),
			huh.NewSelect[string]().
				Title("Model").
				Description("Ranked by phase 00's bakeoff on a real transcript.").
				Options(modelOptions...).
				Value(&modelChoice),
			// No "docs directory" field: the path is fixed by decision 9
			// (scribe.DocsDir — fixed name, nothing to detect, no collision
			// with docs you already keep), and internal/docs hardcodes it
			// regardless of what's configured. Asking would imply a choice
			// that doesn't exist. The note above states the path instead.
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Model name").
				Description("In provider/model form, e.g. \"opencode/longcat-2.0-free\".").
				Value(&customModel).
				Validate(huh.ValidateNotEmpty()),
		).WithHideFunc(func() bool { return modelChoice != ModelOther }),
		huh.NewGroup(
			// Phase 06 re-adds the history layout question (reversing item 31's
			// temporary removal, now that internal/layout and docs.Store give
			// it meaning). Per-session is the pre-selected default because it's
			// conflict-free by construction; shared is the simpler single-file
			// layout for solo repos that don't mind the occasional merge
			// conflict on a pull.
			huh.NewSelect[string]().
				Title("History layout").
				Description("How CHANGELOG.md and JOURNAL.md are organised across teammates.").
				Options(
					huh.NewOption("per-session — one file per session, conflict-free", string(LayoutPerSession)),
					huh.NewOption("shared — single files, simpler, conflicts on pull", string(LayoutShared)),
				).
				Value(&layoutChoice),
			huh.NewConfirm().
				Title("Commit the docs to git?").
				Description("No keeps "+scribe.DocsDir+" out of the repo via .gitignore.").
				Affirmative("Commit them").
				Negative("Keep them out").
				Value(&docsInGit),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Run the seed and replay passes now?").
				Affirmative("Yes").
				Negative("Not yet").
				Value(&proceed),
		),
	)

	form = withAbortKeys(withFormIO(form))

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			// Ctrl+C or Esc (see withAbortKeys): not an error, just "the
			// user backed out". Report whatever was chosen so far but with
			// Proceed false.
			return Answers{Agent: agent, Model: resolveModel(modelChoice, customModel), DocsDir: docsDir, DocsInGit: docsInGit, Layout: Layout(layoutChoice), Proceed: false}, nil
		}
		return Answers{}, fmt.Errorf("wizard: setup form: %w", err)
	}

	return Answers{
		Agent:     agent,
		Model:     resolveModel(modelChoice, customModel),
		DocsDir:   docsDir,
		DocsInGit: docsInGit,
		Layout:    Layout(layoutChoice),
		Proceed:   proceed,
	}, nil
}

// isGradedModel reports whether id is one of the bakeoff's ranked models.
func isGradedModel(id string) bool {
	for _, m := range GradedModels {
		if m.ID == id {
			return true
		}
	}
	return false
}

// resolveModel collapses the select and the free-text field into the one
// model string the connector actually gets.
func resolveModel(choice, custom string) string {
	if choice == ModelOther {
		return custom
	}
	return choice
}
