package writer

import (
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

const defaultOpencodeBin = "opencode"

// opencodeWriter shells out to the opencode CLI.
type opencodeWriter struct {
	bin     string
	model   string
	timeout time.Duration
}

func newOpencodeWriter(cfg Config) (scribe.Writer, error) {
	bin := cfg.Command
	if bin == "" {
		bin = defaultOpencodeBin
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &opencodeWriter{bin: bin, model: cfg.Model, timeout: timeout}, nil
}

func (w *opencodeWriter) Name() string { return "opencode" }

func (w *opencodeWriter) Run(prompt string) (string, error) {
	return runUnattended(w.bin, opencodeArgv(w.model, prompt), w.timeout)
}

// opencodeArgv builds the argv for one unattended opencode run.
//
// NOTE TO WHOEVER EDITS THIS NEXT (likely soon): unit 0B is, in parallel
// with this unit, empirically probing opencode's actual flags for
// non-interactive/headless mode and auto-approving tool use — phase 00's
// "confirm the writer command completes unattended with no terminal
// attached" unknown. The flags below are a best-effort placeholder, not a
// verified answer. All opencode-specific argv shape is deliberately
// contained in this one function so that once 0B's probing lands, fixing
// it up is a one-function edit — nothing in exec.go, writer.go or any
// caller needs to change.
//
// prompt is passed as the final positional argument rather than over
// stdin, since stdin is reserved for the /dev/null redirect that keeps
// this from ever blocking on a TTY (see exec.go). That means very large
// prompts risk the OS's argv length limit (ARG_MAX) — acceptable for
// phase 01's transcript sizes; if it becomes a problem, the fix is to
// write the prompt to a temp file and pass its path instead, still inside
// this one function.
func opencodeArgv(model, prompt string) []string {
	args := []string{"run"}
	if model != "" {
		args = append(args, "--model", model)
	}
	// Best guess at opencode's non-interactive/auto-approve flag — opencode's
	// TUI normally prompts for tool-use approval, which would hang forever
	// with no TTY to answer it. Confirm and correct once 0B reports back.
	args = append(args, "--print", "--auto-approve")
	args = append(args, prompt)
	return args
}
