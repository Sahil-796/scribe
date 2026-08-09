package wizard

import (
	"strings"
	"testing"
)

// TestProgress_Interactive_RedrawsInPlace covers the branch progress_test.go
// couldn't: with a real terminal attached, Progress must redraw a single
// line in place (\r + clear-to-end-of-line) rather than emitting the
// plain one-line-per-step output used when nothing is interpreting escape
// codes. A pty is used only to make IsInteractive() report true — Progress
// itself writes to progressWriter, not to the pty, so output is still
// captured in an ordinary buffer.
func TestProgress_Interactive_RedrawsInPlace(t *testing.T) {
	newPTYSession(t) // stdin/stdout -> tty, so IsInteractive() == true
	buf := withProgressBuffer(t)

	step, done := Progress(3)
	step(1, "PROJECT.md")
	step(2, "DECISIONS.md")
	done()

	got := buf.String()

	// Every step after the first must return to column 0 and clear the
	// rest of the line — that's what makes a shorter later label not leave
	// stray trailing characters from a longer earlier one.
	if !strings.Contains(got, "\r\033[K[1/3] PROJECT.md") {
		t.Errorf("interactive progress output missing carriage-return redraw for step 1:\n%q", got)
	}
	if !strings.Contains(got, "\r\033[K[2/3] DECISIONS.md") {
		t.Errorf("interactive progress output missing carriage-return redraw for step 2:\n%q", got)
	}
	// done() must end the line so whatever's printed after Progress starts
	// on a fresh line instead of overwriting the last redraw.
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("interactive progress output does not end with a newline after done(): %q", got)
	}
	// Unlike the non-interactive path, this one is expected to use control
	// characters — assert they're actually present, the mirror image of
	// progress_test.go's assertion that the plain path never emits them.
	if !strings.ContainsAny(got, "\r\033") {
		t.Errorf("interactive progress output has no control characters at all: %q", got)
	}
}
