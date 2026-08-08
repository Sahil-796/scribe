package writer

import (
	"bytes"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// defaultTimeout is the hard ceiling applied when a Config doesn't set one.
// Chosen generously for phase 01 (no config system yet, decision: hardcode
// sensible defaults) — long enough for a real writer run against a full
// transcript, short enough that a wedged process can't hang the machine.
const defaultTimeout = 3 * time.Minute

// runUnattended runs bin with args to completion or until timeout, and
// returns its stdout.
//
// Three guarantees the plan requires of every connector (docs/PLAN.md,
// "Interface > The writer" and phase 00's "writer command completes
// unattended with no terminal attached"):
//
//  1. No TTY, ever. cmd.Stdin is left nil, which os/exec documents as
//     connecting the child's stdin to the null device — the same effect as
//     an explicit /dev/null redirect, without depending on that file
//     existing at a fixed path. A writer that tries to prompt for input
//     reads EOF instead of blocking forever.
//  2. A hard timeout. If the process (or anything it spawns) isn't done by
//     timeout, we stop waiting and kill it.
//  3. Process-group kill. The child is started in its own process group
//     (Setpgid), and on timeout we signal the whole group, not just the
//     one pid. A writer that shells out to something else — the actual
//     failure mode we're defending against — dies with it instead of
//     surviving as an orphan.
func runUnattended(bin string, args []string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	cmd := exec.Command(bin, args...)
	// cmd.Stdin left nil: os/exec connects it to the null device. See (1) above.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("writer: start %s: %w", bin, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("writer: %s exited: %w (stderr: %s)", bin, err, trimTail(stderr.String()))
		}
		return stdout.String(), nil

	case <-timer.C:
		if cmd.Process != nil {
			// Negative pid targets the whole process group created by
			// Setpgid above.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done // reap so the goroutine above doesn't leak
		return "", fmt.Errorf("writer: %s timed out after %s and was killed", bin, timeout)
	}
}

// trimTail keeps error messages readable when a misbehaving writer dumps a
// huge stderr.
func trimTail(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max:]
}
