package queue

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// This file drives real, separate OS processes against the same repo to
// prove the run lock holds across process boundaries (a Go mutex cannot do
// this) and that a killed holder's lock is recovered rather than wedging
// the repo forever.
//
// It reuses the test binary itself as the child process, gated by an env
// var, which is the standard Go "helper process" pattern (as used by
// os/exec's own tests) — no extra binary to build.

const helperEnvVar = "SCRIBE_QUEUE_TEST_HELPER"
const helperRepoEnvVar = "SCRIBE_QUEUE_TEST_REPO"

// helperActionEnvVar picks which helper behaviour runLockHolderHelper's
// process dispatches to. Unset (or "lock") preserves the original
// behaviour so existing callers of startLockHolder need no change; the
// other actions were added to prove coalescing and no-lost-trigger
// behaviour under genuine OS-process concurrency (OPEN-ITEMS item 22),
// which — like the lock itself — cannot be proven with goroutines, since
// goroutines share one process's file descriptor table and never exercise
// the cross-process flock/O_EXCL paths this package depends on.
const helperActionEnvVar = "SCRIBE_QUEUE_TEST_ACTION"
const helperSessionEnvVar = "SCRIBE_QUEUE_TEST_SESSION"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnvVar) == "1" {
		switch os.Getenv(helperActionEnvVar) {
		case "enqueue":
			runEnqueueHelper()
		case "pending":
			runSetPendingHelper()
		case "trylock-hold":
			runTryLockHoldHelper()
		default:
			runLockHolderHelper()
		}
		return
	}
	os.Exit(m.Run())
}

// runEnqueueHelper enqueues exactly one trigger for the session named by
// helperSessionEnvVar into the repo named by helperRepoEnvVar, then exits.
// Used to prove Enqueue is safe against genuine concurrent OS processes,
// not just goroutines sharing one process's flock table.
func runEnqueueHelper() {
	repo := os.Getenv(helperRepoEnvVar)
	session := os.Getenv(helperSessionEnvVar)
	trig := scribe.Trigger{
		SessionID:      session,
		TranscriptPath: filepath.Join(repo, "transcript-"+session+".jsonl"),
		RepoRoot:       repo,
		EnqueuedAt:     time.Now().UTC(),
	}
	if err := Enqueue(repo, trig); err != nil {
		fmt.Println("ENQUEUE_ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("ENQUEUED")
}

// runSetPendingHelper sets the pending flag for the repo named by
// helperRepoEnvVar, then exits. Used to prove SetPending's "idempotent,
// safe from multiple concurrent processes" contract for real, not just
// for concurrent goroutines in one process.
func runSetPendingHelper() {
	repo := os.Getenv(helperRepoEnvVar)
	q, err := Open(repo)
	if err != nil {
		fmt.Println("OPEN_ERROR:", err)
		os.Exit(1)
	}
	if err := q.SetPending(); err != nil {
		fmt.Println("PENDING_ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("PENDING_SET")
}

// holdDurationEnvVar names the env var runTryLockHoldHelper reads to learn
// how long to hold the lock (as a time.Duration string) before releasing
// it. Configurable so tests can pick a hold long enough to outlast every
// racer's exec/start overhead without needlessly slowing the suite down.
const holdDurationEnvVar = "SCRIBE_QUEUE_TEST_HOLD"

// runTryLockHoldHelper makes a single TryLock attempt against the repo
// named by helperRepoEnvVar. A loser reports LOST and exits immediately.
// A winner reports WON, *actually holds the lock* for holdDurationEnvVar
// (default 300ms) — standing in for the real work a "scribe run" process
// does between acquiring the lock and releasing it — and then calls
// Unlock() cleanly before exiting.
//
// This is deliberately not the same shape as the old "acquire and exit
// immediately" helper it replaces. That version made every winner's pid
// go dead within microseconds of winning, so a second, third, or later
// racer's isStale() check (info.Hostname == hostname() &&
// !processAlive(info.PID)) correctly saw a dead holder and correctly
// reclaimed — repeatedly. That is the intended crash-recovery path
// (proven deliberately, with a single holder, by
// TestCrossProcessStaleLockRecoveryOnKill below) doing exactly its job,
// not a mutual-exclusion violation: each reclaim only ever happened after
// confirming the previous holder was already dead, so at no single
// instant did two processes both hold the lock. But it meant a test
// racing N simultaneous starters against each other never actually raced
// them against a *live* holder — it raced them against a cascade of
// instant crashes, which isn't the scenario item 22 asks to prove
// ("concurrency under real triggers", i.e. contention against a run
// that's actually in flight). Holding the lock here for a bounded
// interval well under StaleLockAge makes every loser's TryLock race a
// genuinely live holder, so "at most one winner" is actually testing
// mutual exclusion rather than incidentally testing crash recovery.
func runTryLockHoldHelper() {
	repo := os.Getenv(helperRepoEnvVar)
	hold := 300 * time.Millisecond
	if v := os.Getenv(holdDurationEnvVar); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			hold = d
		}
	}

	q, err := Open(repo)
	if err != nil {
		fmt.Println("OPEN_ERROR:", err)
		os.Exit(1)
	}
	ok, err := q.TryLock()
	if err != nil {
		fmt.Println("LOCK_ERROR:", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Println("LOST")
		return
	}
	fmt.Println("WON")
	time.Sleep(hold)
	if err := q.Unlock(); err != nil {
		fmt.Println("UNLOCK_ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("RELEASED")
}

// runLockHolderHelper acquires the run lock for the repo named by
// helperRepoEnvVar, prints "LOCKED" so the parent knows it succeeded, then
// blocks forever — until the parent kills it. This stands in for a worker
// process that crashes mid-run.
func runLockHolderHelper() {
	repo := os.Getenv(helperRepoEnvVar)
	q, err := Open(repo)
	if err != nil {
		fmt.Println("OPEN_ERROR:", err)
		os.Exit(1)
	}
	ok, err := q.TryLock()
	if err != nil {
		fmt.Println("LOCK_ERROR:", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Println("LOCK_BUSY")
		os.Exit(2)
	}
	fmt.Println("LOCKED")
	select {} // block until killed
}

// spawnAndWaitDeadPID launches a trivial subprocess, waits for it to exit,
// and returns its pid — guaranteed not to be alive, for stale-lock tests
// that need a definitely-dead pid without racing a real crash.
func spawnAndWaitDeadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start throwaway process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		// -test.run=^$ matches nothing and exits 0 normally; if the
		// test binary behaves differently that's fine too, we only
		// need the pid to be reliably dead afterward.
		_ = err
	}
	return pid
}

func startLockHolder(t *testing.T, repo string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnvVar+"=1", helperRepoEnvVar+"="+repo)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lock-holder helper: %v", err)
	}

	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		if sc.Scan() {
			line <- sc.Text()
		} else {
			line <- ""
		}
	}()

	select {
	case got := <-line:
		if got != "LOCKED" {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatalf("lock-holder helper did not report LOCKED, got %q", got)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("timed out waiting for lock-holder helper to acquire the lock")
	}
	return cmd
}

// TestCrossProcessLockContention proves the lock is observed across
// process boundaries: a real child process acquires it, and this process
// (a different pid) must see TryLock fail while the child holds it.
func TestCrossProcessLockContention(t *testing.T) {
	repo := t.TempDir()
	holder := startLockHolder(t, repo)
	defer func() {
		holder.Process.Kill()
		holder.Wait()
	}()

	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ok, err := q.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if ok {
		t.Fatalf("TryLock succeeded in this process while a separate process holds the lock")
	}
}

// TestCrossProcessStaleLockRecoveryOnKill proves the mandatory failure
// mode: a worker process that gets killed (crash, OOM, machine problem)
// must not wedge the repo. A separate process's TryLock must recover the
// lock once the holder is confirmed dead, without waiting out the full age
// bound.
func TestCrossProcessStaleLockRecoveryOnKill(t *testing.T) {
	repo := t.TempDir()
	holder := startLockHolder(t, repo)

	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ok, _ := q.TryLock(); ok {
		q.Unlock()
		t.Fatalf("expected the lock to be held by the child process")
	}

	// Simulate a crash: SIGKILL, no chance to clean up its own lock file.
	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("kill lock-holder: %v", err)
	}
	holder.Wait()

	// Give the OS a brief moment to finalize process teardown so the pid
	// liveness probe is unambiguous; this is not relying on StaleLockAge.
	deadline := time.Now().Add(2 * time.Second)
	var ok bool
	for time.Now().Before(deadline) {
		ok, err = q.TryLock()
		if err != nil {
			t.Fatalf("TryLock: %v", err)
		}
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("lock was not recovered after its holder process was killed")
	}
	q.Unlock()
}
