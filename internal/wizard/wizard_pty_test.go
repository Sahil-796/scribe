package wizard

import (
	"testing"
	"time"
)

// These tests drive Ask's actual huh form through a pty (see pty_test.go).
// They exist because everything else in this package only exercises the
// not-interactive short-circuit — nothing else proves the form renders,
// navigates, or reports the operator's real choices rather than silently
// falling back to defaults.
//
// Every transition that's expected to move focus to a new field is
// synchronized with WaitForFocused, not a plain WaitFor — see that
// method's comment for why a plain substring wait races huh's async
// focus-change Cmd and silently misattributes keystrokes.

const ptyTimeout = 5 * time.Second

// TestAsk_PTY_HappyPath_ReturnsActualChoices is the single most important
// gap this file closes: every other test (and the non-interactive ones)
// only prove Ask returns *some* Answers. This proves it returns the
// operator's actual selections — a non-default agent and a non-default
// graded model — not the seeded defaults that a bug (e.g. never wiring the
// form's Value pointers, or reading the wrong variable at the end) could
// return just as easily.
func TestAsk_PTY_HappyPath_ReturnsActualChoices(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{
			RepoRoot:     "/repo",
			Agents:       []string{"opencode", "claude", "custom"},
			DefaultAgent: "opencode",
			DefaultModel: DefaultModel, // GradedModels[0]
		})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter) // past the intro note

	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyDown)  // opencode -> claude
	s.Send(keyEnter) // commit "claude", advance to Model

	s.WaitForFocused("Model", ptyTimeout)
	s.Send(keyDown)  // longcat -> mimo (GradedModels[1], the second-ranked, non-default one)
	s.Send(keyEnter) // commit, submit group 2 (Model is the last field)

	// modelChoice != ModelOther, so the "Model name" group stays hidden and
	// we land straight on "Commit the docs to git?" — accept the default
	// (No / keep them out). No Layout question in between: item 31 removed
	// it (see wizard.go's Layout doc comment).
	s.WaitForFocused("Commit the docs to git?", ptyTimeout)
	s.Send(keyEnter)

	// Final confirm — accept the default (Yes / proceed).
	s.WaitForFocused("Run the seed and replay passes now?", ptyTimeout)
	s.Send(keyEnter)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil", got.err)
	}

	want := Answers{
		Agent:     "claude",
		Model:     "opencode/mimo-v2.5-free",
		DocsDir:   "docs/scribe",
		DocsInGit: false,
		Layout:    LayoutPerSession,
		Proceed:   true,
	}
	if got.answers != want {
		t.Fatalf("Ask() = %+v, want %+v (the whole point of this test is that these are the OPERATOR'S choices, not the seeded defaults)", got.answers, want)
	}
}

// TestAsk_PTY_ModelSelect_GradedModel drives the model select to a specific
// graded entry (not index 0, so a test that never moved the cursor at all
// couldn't accidentally pass) and confirms Ask reports exactly that model,
// with the conditional "Model name" free-text group staying hidden.
func TestAsk_PTY_ModelSelect_GradedModel(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{RepoRoot: "/repo"})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyEnter) // keep default agent, move to Model

	s.WaitForFocused("Model", ptyTimeout)
	s.Send(keyDown) // -> mimo
	s.Send(keyDown) // -> deepseek (GradedModels[2], the third)
	s.Send(keyEnter)

	// The hidden group must actually stay hidden: assert we're on "Commit
	// the docs to git?", never having seen "Model name" in between.
	s.WaitForFocused("Commit the docs to git?", ptyTimeout)
	s.AssertNotContaining("Model name")
	s.Send(keyEnter)
	s.WaitForFocused("Run the seed and replay passes now?", ptyTimeout)
	s.Send(keyEnter)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil", got.err)
	}
	want := GradedModels[2].ID
	if got.answers.Model != want {
		t.Fatalf("Model = %q, want %q", got.answers.Model, want)
	}
}

// TestAsk_PTY_ModelSelect_CustomModel picks "something else…" and types a
// free-text model, proving the conditional group both reveals itself and
// feeds resolveModel the typed value rather than the sentinel or the
// default.
func TestAsk_PTY_ModelSelect_CustomModel(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{RepoRoot: "/repo"})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyEnter)

	s.WaitForFocused("Model", ptyTimeout)
	s.Send(keyDown) // mimo
	s.Send(keyDown) // deepseek
	s.Send(keyDown) // something else…
	s.Send(keyEnter)

	// The conditional group must now be visible with an empty field.
	s.WaitForFocused("Model name", ptyTimeout)
	s.Send("opencode/my-custom-model")
	s.Send(keyEnter)

	s.WaitForFocused("Commit the docs to git?", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Run the seed and replay passes now?", ptyTimeout)
	s.Send(keyEnter)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil", got.err)
	}
	want := "opencode/my-custom-model"
	if got.answers.Model != want {
		t.Fatalf("Model = %q, want %q", got.answers.Model, want)
	}
}

// TestAsk_PTY_NonGradedDefaultModel_PrePopulatesCustomField covers
// wizard.go's isGradedModel branch: a --model value that came in via
// Options.DefaultModel but isn't one of the three graded models must land
// the select on "something else…" AND pre-fill the free-text field with
// that value — not silently reset to DefaultModel/longcat, which is exactly
// the kind of thing that would quietly overwrite an operator's --model flag.
func TestAsk_PTY_NonGradedDefaultModel_PrePopulatesCustomField(t *testing.T) {
	s := newPTYSession(t)

	const preset = "opencode/some-unlisted-model"

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{RepoRoot: "/repo", DefaultModel: preset})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyEnter)

	// The select should already be sitting on "something else…" with no
	// Down presses at all — that's the pre-population under test.
	s.WaitForFocused("Model", ptyTimeout)
	s.Send(keyEnter)

	// The free-text field must show the preset value already typed in,
	// not a blank field.
	s.WaitForFocused("Model name", ptyTimeout)
	s.WaitFor(preset, ptyTimeout)
	s.Send(keyEnter) // accept the pre-filled value as-is

	s.WaitForFocused("Commit the docs to git?", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Run the seed and replay passes now?", ptyTimeout)
	s.Send(keyEnter)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil", got.err)
	}
	if got.answers.Model != preset {
		t.Fatalf("Model = %q, want %q (the pre-populated value, untouched)", got.answers.Model, preset)
	}
}

// TestAsk_PTY_CustomModelValidation_RejectsEmpty proves the custom-model
// field's huh.ValidateNotEmpty() actually blocks the form, rather than just
// being present in the source and never actually reached at runtime.
func TestAsk_PTY_CustomModelValidation_RejectsEmpty(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{RepoRoot: "/repo"})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Model", ptyTimeout)
	s.Send(keyDown)
	s.Send(keyDown)
	s.Send(keyDown) // something else…
	s.Send(keyEnter)

	s.WaitForFocused("Model name", ptyTimeout)
	// Submit with nothing typed.
	s.Send(keyEnter)
	s.WaitFor("input cannot be empty", ptyTimeout)

	// The form must not have advanced — still on the Model name screen, and
	// Ask must not have returned yet.
	select {
	case r := <-resCh:
		t.Fatalf("Ask() returned after an empty submit (answers=%+v err=%v); the empty-value validator should have blocked it", r.answers, r.err)
	case <-time.After(300 * time.Millisecond):
		// expected: still blocked
	}
	s.WaitForFocused("Model name", ptyTimeout)

	// Now supply a value and confirm the form actually unblocks and
	// completes — proves this was a real validation gate, not a hang.
	s.Send("opencode/now-valid")
	s.Send(keyEnter)
	s.WaitForFocused("Commit the docs to git?", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Run the seed and replay passes now?", ptyTimeout)
	s.Send(keyEnter)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil", got.err)
	}
	if got.answers.Model != "opencode/now-valid" {
		t.Fatalf("Model = %q, want %q", got.answers.Model, "opencode/now-valid")
	}
}

// TestAsk_PTY_CtrlC_Aborts covers the huh.ErrUserAborted branch: Ctrl+C
// must make Ask return Proceed:false with whatever was already chosen, and
// must NOT surface an error — a caller that treats a non-nil error as a
// crash would otherwise turn an operator backing out into a scary failure
// message.
func TestAsk_PTY_CtrlC_Aborts(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{
			RepoRoot:     "/repo",
			Agents:       []string{"opencode", "claude", "custom"},
			DefaultAgent: "opencode",
		})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyDown) // pick "claude" before backing out, so we can prove it survives the abort

	// Ctrl+C is handled at the form level before any field-specific
	// dispatch (huh's Quit binding), so it doesn't need a focus-transition
	// wait the way advancing to a new field does — it aborts whatever's
	// currently focused immediately. Sending it right after the Down (which
	// huh's Select applies live, without needing a submit) is enough to
	// prove the in-progress choice survives the abort.
	s.Send(keyCtrlC)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil (huh.ErrUserAborted must be swallowed, not surfaced)", got.err)
	}
	if got.answers.Proceed {
		t.Fatalf("Ask().Proceed = true after Ctrl+C, want false")
	}
	if got.answers.Agent != "claude" {
		t.Fatalf("Ask().Agent = %q after Ctrl+C, want %q (whatever was chosen before backing out)", got.answers.Agent, "claude")
	}
}

// TestAsk_PTY_Esc_Aborts covers OPEN-ITEMS item 32: Esc now backs out of
// the form exactly like Ctrl+C, via withAbortKeys' extra Quit binding
// (wizard.go). This used to be a documented no-op (huh v1.0.0's own
// default keymap binds Quit to "ctrl+c" alone) — this test replaces that
// old assertion now that the package adds the binding itself.
func TestAsk_PTY_Esc_Aborts(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{
			RepoRoot:     "/repo",
			Agents:       []string{"opencode", "claude", "custom"},
			DefaultAgent: "opencode",
		})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyDown) // pick "claude" before backing out, so we can prove it survives the abort
	s.Send(keyEsc)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil (huh.ErrUserAborted must be swallowed, not surfaced)", got.err)
	}
	if got.answers.Proceed {
		t.Fatalf("Ask().Proceed = true after Esc, want false")
	}
	if got.answers.Agent != "claude" {
		t.Fatalf("Ask().Agent = %q after Esc, want %q (whatever was chosen before backing out)", got.answers.Agent, "claude")
	}
}

// TestAsk_PTY_CtrlC_StillAborts guards against withAbortKeys' extra Esc
// binding accidentally displacing the original ctrl+c one — key.Binding
// takes a list of keys, so this is really a "both keys, not just the new
// one" check.
func TestAsk_PTY_CtrlC_StillAborts(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{RepoRoot: "/repo"})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)
	s.Send(keyCtrlC)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil", got.err)
	}
	if got.answers.Proceed {
		t.Fatalf("Ask().Proceed = true after Ctrl+C, want false")
	}
}

// TestAsk_PTY_Esc_DuringFilter_AbortsInsteadOfClearingFilter documents the
// trade-off withAbortKeys' doc comment calls out: huh's Select field binds
// a bare Esc to leaving filter mode (SetFilter/ClearFilter in huh's
// keymap.go) while filtering, but Form.Update checks the form-level Quit
// binding before the keypress ever reaches the focused field (huh's
// form.go). Once Esc is also a Quit key, pressing it mid-filter aborts the
// whole form instead of just clearing the filter — a real regression in
// that one interaction, accepted as the cost of having a working abort key
// at all. This test exists so that trade-off stays visible and provable,
// not just asserted in a comment.
func TestAsk_PTY_Esc_DuringFilter_AbortsInsteadOfClearingFilter(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		answers Answers
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		a, err := Ask(Options{
			RepoRoot:     "/repo",
			Agents:       []string{"opencode", "claude", "custom"},
			DefaultAgent: "opencode",
		})
		resCh <- result{a, err}
	}()

	s.WaitFor("scribe init", ptyTimeout)
	s.Send(keyEnter)
	s.WaitForFocused("Agent", ptyTimeout)

	s.Send("/") // enter filter mode on the Agent select
	s.WaitFor("/", ptyTimeout)
	s.Send("claude") // type a filter
	s.Send(keyEsc)   // would normally leave filter mode and keep the results

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Ask() error = %v, want nil (huh.ErrUserAborted must be swallowed, not surfaced)", got.err)
	}
	if got.answers.Proceed {
		t.Fatalf("Ask().Proceed = true after Esc mid-filter, want false — this test documents that Esc aborts the form rather than just clearing the filter")
	}
}
