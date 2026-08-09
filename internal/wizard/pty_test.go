package wizard

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// ptySession drives the wizard's forms through a real pseudo-terminal. huh's
// forms are bubbletea programs: they insist on a terminal-backed
// io.Reader/io.Writer (raw mode, cursor control, redraw-in-place), so pipes
// or bytes.Buffers can't stand in for stdin/stdout the way they can for the
// package's non-interactive short-circuits. A pty is the one thing that
// looks enough like a real terminal to drive the form end to end.
//
// ptmx is the controlling end the test writes keystrokes to and reads
// rendered output from — the role a real terminal emulator plays. tty is
// the end the wizard package treats as stdin/stdout/form I/O, exactly as it
// would treat an operator's actual terminal.
type ptySession struct {
	t    *testing.T
	ptmx *os.File

	mu  sync.Mutex
	buf bytes.Buffer
}

// newPTYSession allocates a pty and points the wizard package's stdin,
// stdout, and form I/O overrides at the tty end for the duration of the
// test. Every var it touches is restored in t.Cleanup, and the pty itself
// is skipped (not failed) when the platform can't allocate one — CI sandboxes
// and some containers don't support pty allocation, and a skip there is
// correct, a failure is not.
func newPTYSession(t *testing.T) *ptySession {
	t.Helper()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty.Open: %v (no pty available on this platform/sandbox — skipping)", err)
	}
	// A generous, fixed size. huh sizes its layout off the window, and a
	// 0x0 or tiny window (which some pty defaults give you) can make
	// fields wrap or truncate in ways that break substring matches below.
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Skipf("pty.Setsize: %v", err)
	}

	origStdin, origStdout := stdin, stdout
	origFormInput, origFormOutput := formInput, formOutput
	stdin, stdout = tty, tty
	formInput, formOutput = tty, tty

	s := &ptySession{t: t, ptmx: ptmx}

	// Pump ptmx's output into a buffer the test can poll. Reading a pty
	// blocks until there's data (or it's closed), so this has to run on
	// its own goroutine rather than inline in WaitFor.
	done := make(chan struct{})
	go func() {
		defer close(done)
		readBuf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(readBuf)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(readBuf[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		stdin, stdout = origStdin, origStdout
		formInput, formOutput = origFormInput, origFormOutput
		tty.Close()
		ptmx.Close()
		<-done // wait for the reader goroutine to see EOF and exit
	})

	return s
}

// Send writes s to the pty as if it were typed at the terminal.
func (p *ptySession) Send(s string) {
	p.t.Helper()
	if _, err := p.ptmx.Write([]byte(s)); err != nil {
		p.t.Fatalf("write to pty: %v", err)
	}
}

// Terminal key sequences. Named rather than inlined at call sites so a test
// reads as "press Down twice" rather than a wall of escape codes.
const (
	keyEnter = "\r"
	keyDown  = "\x1b[B"
	keyUp    = "\x1b[A"
	keyCtrlC = "\x03"
	keyEsc   = "\x1b"
)

// WaitFor polls the accumulated output for substr, rather than sleeping a
// fixed duration — the form redraws asynchronously and a fixed sleep is
// either a flaky race (too short) or a slow suite (padded long enough to be
// safe). On timeout it fails with the full captured output so a failure is
// debuggable from CI logs instead of a bare "timed out".
func (p *ptySession) WaitFor(substr string, timeout time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		got := p.buf.String()
		p.mu.Unlock()
		if strings.Contains(got, substr) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatalf("timed out after %s waiting for %q in pty output; full output so far:\n%s", timeout, substr, p.snapshot())
}

// snapshot returns everything captured so far, for failure messages.
func (p *ptySession) snapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.String()
}

// WaitForFocused waits until fieldTitle is the *focused* field, not merely
// present on screen. huh renders a group's fields together — an unfocused
// select still shows all of its options — so a plain WaitFor(title) can
// return true before the field is actually focused, if that title also
// happens to be static content already on screen (e.g. every model in the
// Model select is visible whether or not Model has focus yet).
//
// That distinction matters because huh/bubbletea's focus transitions run
// through an async tea.Cmd (nextFieldMsg is processed on a later Update
// call, not synchronously with the keypress that triggered it). Firing the
// next keystroke as soon as the target text is merely visible — rather than
// once it's actually focused — races that Cmd: the keystroke can land on
// the field that's *still* focused instead of the one becoming focused,
// silently corrupting which field gets which input. This was caught by
// hand: a first draft of these tests kept mis-attributing keystrokes below
// this comment before switching to this marker.
//
// The marker itself is huh's own rendering convention: every line of the
// currently-focused field is prefixed with "┃ ", unfocused fields with two
// spaces. That prefix only appears for a field once it truly has focus, so
// waiting for "┃ <title>" is a correct synchronization point rather than a
// race disguised as one.
func (p *ptySession) WaitForFocused(fieldTitle string, timeout time.Duration) {
	p.t.Helper()
	p.WaitFor("┃ "+fieldTitle, timeout)
}

// AssertNotContaining fails the test if substr has appeared in the output
// captured up to this point. Used to assert a negative (e.g. "the abort
// message never showed up") without racing against output that just hasn't
// arrived yet — callers should WaitFor some later, definitive marker first.
func (p *ptySession) AssertNotContaining(substr string) {
	p.t.Helper()
	if strings.Contains(p.snapshot(), substr) {
		p.t.Fatalf("expected %q to be absent from pty output, but found it:\n%s", substr, p.snapshot())
	}
}

// waitOnResult blocks on ch for a form-driving goroutine's result, failing
// with a clear message (rather than hanging the test suite) if the form
// never returns — e.g. because a scripted keystroke sequence didn't match
// what the form actually expected at that point.
func waitOnResult[T any](t *testing.T, ch <-chan T, timeout time.Duration, onTimeout func() string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		msg := ""
		if onTimeout != nil {
			msg = onTimeout()
		}
		t.Fatalf("timed out after %s waiting for form to return\n%s", timeout, msg)
		var zero T
		return zero
	}
}
