package wizard

import (
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// These cover Review's interactive branches through a pty — the
// not-interactive short-circuit is already covered in review_test.go, but
// nothing previously drove the actual confirm form.

func TestReview_PTY_Approve(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		approved bool
		err      error
	}
	resCh := make(chan result, 1)
	go func() {
		ok, err := Review(map[scribe.Doc]string{scribe.DocProject: "hello world"})
		resCh <- result{ok, err}
	}()

	s.WaitFor("Review", ptyTimeout)
	s.WaitFor("hello world", ptyTimeout) // the preview content actually rendered
	s.Send(keyEnter)                     // past the preview note

	s.WaitFor("Write these to the repo", ptyTimeout)
	s.Send("y") // Accept binding: explicit "approve", not just the default
	s.Send(keyEnter)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Review() error = %v, want nil", got.err)
	}
	if !got.approved {
		t.Fatal("Review() approved = false, want true")
	}
}

func TestReview_PTY_Discard(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		approved bool
		err      error
	}
	resCh := make(chan result, 1)
	go func() {
		ok, err := Review(map[scribe.Doc]string{scribe.DocProject: "hello world"})
		resCh <- result{ok, err}
	}()

	s.WaitFor("Review", ptyTimeout)
	s.Send(keyEnter)
	s.WaitFor("Write these to the repo", ptyTimeout)
	// Discard is the field's default (approve starts false) — submit
	// without touching it, proving the default path resolves to false.
	s.Send(keyEnter)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Review() error = %v, want nil", got.err)
	}
	if got.approved {
		t.Fatal("Review() approved = true, want false (default/Discard)")
	}
}

// TestReview_PTY_CtrlC_Aborts covers the huh.ErrUserAborted branch: backing
// out of the review must read as "no, don't write anything" — false — and
// must not be surfaced as an error.
func TestReview_PTY_CtrlC_Aborts(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		approved bool
		err      error
	}
	resCh := make(chan result, 1)
	go func() {
		ok, err := Review(map[scribe.Doc]string{scribe.DocProject: "hello world"})
		resCh <- result{ok, err}
	}()

	s.WaitFor("Review", ptyTimeout)
	s.Send(keyCtrlC)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Review() error = %v, want nil (huh.ErrUserAborted must be swallowed)", got.err)
	}
	if got.approved {
		t.Fatal("Review() approved = true after Ctrl+C, want false")
	}
}

// TestReview_PTY_Esc_Aborts covers OPEN-ITEMS item 32's Esc binding
// (wizard.go's withAbortKeys) on the Review form specifically — Ask's pty
// tests cover the setup form, but Review builds its own form and needs its
// own proof that the same keymap change actually reaches it.
func TestReview_PTY_Esc_Aborts(t *testing.T) {
	s := newPTYSession(t)

	type result struct {
		approved bool
		err      error
	}
	resCh := make(chan result, 1)
	go func() {
		ok, err := Review(map[scribe.Doc]string{scribe.DocProject: "hello world"})
		resCh <- result{ok, err}
	}()

	s.WaitFor("Review", ptyTimeout)
	s.Send(keyEsc)

	got := waitOnResult(t, resCh, ptyTimeout, func() string { return s.snapshot() })
	if got.err != nil {
		t.Fatalf("Review() error = %v, want nil (huh.ErrUserAborted must be swallowed)", got.err)
	}
	if got.approved {
		t.Fatal("Review() approved = true after Esc, want false")
	}
}
