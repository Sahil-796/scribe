package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/scribe"
)

// noSpawnEnvVar suppresses the background "scribe run" spawn below when
// set to "1". Tests use it so enqueueing can be exercised without ever
// starting a child process; it's also a legitimate debugging escape hatch
// for a human who wants to drain the queue by hand instead.
const noSpawnEnvVar = "SCRIBE_NO_SPAWN"

// spawnBinEnvVar overrides the binary path spawnRun execs, in place of the
// real os.Executable() lookup. Test-only seam: it lets hook_spawn_test.go
// point the spawn at a stub executable (or at a path that's guaranteed not
// to exist, to exercise the "spawn failed" branch) without ever spawning a
// real "scribe run" / opencode call.
const spawnBinEnvVar = "SCRIBE_SPAWN_BIN"

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook",
		Short:  "Internal: what the Stop hook calls",
		Hidden: true, // never typed by a human
		Long: `scribe hook reads the Claude Code Stop hook's JSON payload on stdin,
enqueues a trigger for this repo, and exits.

This is the one command with a real performance budget: under 50ms, no
network, and it must never block the user's Claude session. It exits 0
quietly, doing nothing, in any repo scribe was never initialised in — off
is the default everywhere.

Once a trigger is safely enqueued, it also fires a detached "scribe run"
in the background so the loop actually drains without a human running
"scribe run" by hand — see docs/findings/07-live-run.md, bug 3. Spawning
is fire-and-forget: this command never waits on the child and a spawn
failure never changes this command's exit code, since the trigger is
already durably queued by that point regardless.

Never typed by a human; installed as the Stop hook command by "scribe init".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bypass cobra's error-printing path entirely: exit directly with
			// the code hook.Run computed. Every extra layer here is latency
			// this command isn't allowed to spend.
			code := hook.Run(cmd.InOrStdin(), cmd.ErrOrStderr(), enqueueAndSpawn)
			os.Exit(code)
			return nil // unreachable
		},
	}
}

// enqueueAndSpawn is the hook.EnqueueFunc this command actually wires up.
// It enqueues exactly as before (queue.Enqueue, unchanged), and only once
// that succeeds does it fire the background worker run. Spawn failures are
// swallowed here, not surfaced as an enqueue error: hook.Run's caller
// (RunE above) uses this function's return value only to decide the exit
// code, and the trigger is already safely on disk before spawnRun is ever
// called — a broken spawn must not turn a successful enqueue into an
// ExitError, or the hook would look like it lost the trigger when it
// didn't.
func enqueueAndSpawn(repoRoot string, t scribe.Trigger) error {
	if err := queue.Enqueue(repoRoot, t); err != nil {
		return err
	}
	if err := spawnRun(repoRoot); err != nil {
		// Recorded, not returned: the enqueue succeeded, so the hook must
		// still report success. But a spawn that never starts means the
		// docs quietly stop updating, which is exactly what the failure
		// log exists to make visible.
		hook.LogFailure(repoRoot, hook.FailureEntry{
			Time:      time.Now().UTC(),
			SessionID: t.SessionID,
			Reason:    fmt.Sprintf("enqueued, but spawning the background worker failed: %v", err),
		})
	}
	return nil
}

// spawnRun starts a detached, background "scribe run" for repoRoot and
// returns immediately without waiting on it. It returns whatever stopped it
// from starting the child so the caller can record it — but the caller must
// not turn that into a non-zero exit, per the hook's one hard rule: it must
// never change behavior the caller depends on. The trigger this run would
// have picked up is already enqueued regardless of whether spawning here
// succeeds; the next hook invocation (or a human running "scribe run") will
// still find and drain it.
func spawnRun(repoRoot string) error {
	if os.Getenv(noSpawnEnvVar) == "1" {
		return nil
	}

	bin := os.Getenv(spawnBinEnvVar)
	if bin == "" {
		// os.Executable(), not a bare "scribe" on $PATH: the hook is
		// invoked by Claude Code with an unknown environment, and $PATH
		// there is not guaranteed to contain the directory scribe was
		// installed into.
		resolved, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locating the scribe binary: %w", err)
		}
		bin = resolved
	}

	cmd := exec.Command(bin, "run")
	cmd.Dir = repoRoot
	// No inherited stdio: nil here means "connect to /dev/null" (see
	// os/exec's docs), not "inherit the parent's fds". Inheriting would
	// keep the hook's own stdout/stderr pipes open for as long as the
	// child runs (up to internal/writer's multi-minute timeout), which
	// is exactly the kind of pipe-holding the fire-and-forget contract
	// forbids — Claude Code (or whatever invoked the hook) could be left
	// waiting on a pipe it thinks closed when the hook exited.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Setsid detaches the child into its own session: it's no longer a
	// member of this process's process group, so it survives this
	// process exiting (which happens within milliseconds, right after
	// this call returns) as a normal orphan reparented by the OS, not as
	// something that gets signaled or torn down alongside the hook.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Start, not Run or CombinedOutput: this call must not block on the
	// child. We also deliberately never call Wait — waiting is itself a
	// form of blocking on the child, and skipping it is safe here
	// because this process (the hook) exits almost immediately after
	// this function returns, at which point the child (already
	// detached) is reparented by the OS rather than left as a zombie
	// under a long-lived parent.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s run: %w", bin, err)
	}
	return nil
}
