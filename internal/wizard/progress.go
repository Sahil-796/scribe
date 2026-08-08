package wizard

import (
	"fmt"
	"io"
	"os"
)

// progressWriter is where Progress writes. A var, not a hardcoded
// os.Stderr, so tests can capture output in a buffer. Progress writes to
// stderr rather than stdout because stdout may be piped or redirected by
// whatever's driving `scribe init` (e.g. capturing Answers as JSON), and
// progress output would corrupt that stream.
var progressWriter io.Writer = os.Stderr

// Progress renders progress during the replay pass. Returns a function to
// call as work completes and a done func to tear the display down.
//
// With a terminal attached this redraws a single line in place, the usual
// TUI progress-bar feel. Without one — piped output, CI, a log file — it
// falls back to one plain line per step, because a display that depends on
// carriage-return tricks is unreadable (and often just noise) when nothing
// is interpreting the escape codes.
func Progress(total int) (step func(done int, label string), done func()) {
	if !IsInteractive() {
		return func(done int, label string) {
				fmt.Fprintf(progressWriter, "[%d/%d] %s\n", done, total, label)
			}, func() {
				// Nothing to tear down: every step already ended its own line.
			}
	}

	return func(done int, label string) {
			// \r returns to column 0, \033[K clears to end of line, so a
			// shorter label on a later step doesn't leave stray characters
			// from a longer previous one.
			fmt.Fprintf(progressWriter, "\r\033[K[%d/%d] %s", done, total, label)
		}, func() {
			fmt.Fprintln(progressWriter)
		}
}
