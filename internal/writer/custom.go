package writer

import (
	"errors"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// customWriter is the escape hatch: a raw command from config, for any
// writer agent scribe doesn't ship a dedicated connector for yet. Per
// docs/PLAN.md: "unsupported meaning unsupported: if it breaks, that's
// yours" — no flag-guessing or output-format handling here, just run what
// the user configured, unattended, with the same timeout and process-group
// kill guarantees every connector gets.
type customWriter struct {
	argv    []string // argv[0] is the binary; argv[1:] are fixed leading args
	timeout time.Duration
}

func newCustomWriter(cfg Config) (scribe.Writer, error) {
	var argv []string
	switch {
	case len(cfg.Args) > 0:
		argv = cfg.Args
	case cfg.Command != "":
		// Naive whitespace split. Fine for simple commands; anything with
		// quoting or spaces in an argument should use Config.Args instead.
		argv = strings.Fields(cfg.Command)
	default:
		return nil, errors.New("writer: custom agent requires Config.Args or Config.Command")
	}
	if len(argv) == 0 {
		return nil, errors.New("writer: custom agent command is empty")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &customWriter{argv: argv, timeout: timeout}, nil
}

func (w *customWriter) Name() string { return "custom" }

func (w *customWriter) Run(prompt string) (string, error) {
	args := make([]string, 0, len(w.argv)-1+1)
	args = append(args, w.argv[1:]...)
	args = append(args, prompt)
	return runUnattended(w.argv[0], args, w.timeout)
}
