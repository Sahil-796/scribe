package wizard

import (
	"bytes"
	"strings"
	"testing"
)

// withProgressBuffer redirects Progress's output to a buffer for the
// duration of the test and returns it.
func withProgressBuffer(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := progressWriter
	progressWriter = &buf
	t.Cleanup(func() { progressWriter = orig })
	return &buf
}

func TestProgress_NotInteractive_PlainLines(t *testing.T) {
	withPipes(t) // forces IsInteractive() == false
	buf := withProgressBuffer(t)

	step, done := Progress(3)
	step(1, "PROJECT.md")
	step(2, "DECISIONS.md")
	done()

	got := buf.String()
	want := "[1/3] PROJECT.md\n[2/3] DECISIONS.md\n"
	if got != want {
		t.Errorf("plain progress output = %q, want %q", got, want)
	}
	// The non-interactive path must never emit carriage returns or ANSI
	// escapes: a piped log or CI console doesn't interpret them, and they'd
	// just show up as literal garbage characters.
	if strings.ContainsAny(got, "\r\033") {
		t.Errorf("plain progress output contains control characters: %q", got)
	}
}

func TestProgress_NotInteractive_DoneIsNoop(t *testing.T) {
	withPipes(t)
	buf := withProgressBuffer(t)

	_, done := Progress(1)
	before := buf.String()
	done()
	if buf.String() != before {
		t.Errorf("done() wrote output on the non-interactive path: %q", buf.String())
	}
}
