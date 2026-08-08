package queue

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
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

func TestMain(m *testing.M) {
	if os.Getenv(helperEnvVar) == "1" {
		runLockHolderHelper()
		return
	}
	os.Exit(m.Run())
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
