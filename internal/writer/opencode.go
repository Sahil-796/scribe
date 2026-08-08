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
// Verified against `opencode run --help` (1.18.15) and
// docs/findings/00-writer.md's empirical probing, both confirmed again on
// this machine (docs/findings/07-live-run.md, bug 1): there is no --print
// flag at all, and the real auto-approve flag is --auto, not
// --auto-approve. Without --auto, opencode run doesn't hang or error — it
// silently auto-rejects every edit the model attempts and still exits 0
// with normal-looking output, which is worse than a hang because nothing
// looks wrong until you notice the docs never changed (see the fail-open
// guard in internal/worker). --format is deliberately left unset: the
// default ("default") format's stdout is exactly the model's final text
// with no TUI/ANSI noise when there's no TTY attached, which is what
// internal/worker.parseEdits expects — --format json instead emits one
// JSON event per line (step_start/text/step_finish/...), which parseEdits
// (a single json.Unmarshal over all of stdout) cannot parse as-is. All
// opencode-specific argv shape is deliberately contained in this one
// function so a future correction is a one-function edit — nothing in
// exec.go, writer.go or any caller needs to change.
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
	// opencode's real non-interactive auto-approve flag (docs/findings/00-writer.md,
	// docs/findings/07-live-run.md bug 1). Required — without it every tool
	// call gets auto-rejected and the run still exits 0.
	args = append(args, "--auto")
	args = append(args, prompt)
	return args
}
