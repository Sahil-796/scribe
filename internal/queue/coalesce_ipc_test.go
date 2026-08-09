package queue

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file proves the coalescing half of decision 3 ("one run at a time
// per repo, with coalescing") under genuine OS-process concurrency, not
// goroutines. lock_ipc_test.go already proves the lock itself crosses
// process boundaries; these tests do the same for Enqueue/Drain and
// SetPending/TakePending — the two mechanisms that make "triggers landing
// during a run set a pending flag; the current run picks them up when it
// finishes" true for real, separately-invoked "scribe hook" processes
// racing a "scribe run" process, rather than for goroutines that happen to
// share one process's flock table.

// newHelperCmd builds an unstarted *exec.Cmd for the reused test binary,
// configured via env vars to run one action (see TestMain in
// lock_ipc_test.go) against repo, optionally naming a session.
func newHelperCmd(action, repo, session string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	env := append(os.Environ(),
		helperEnvVar+"=1",
		helperActionEnvVar+"="+action,
		helperRepoEnvVar+"="+repo,
	)
	if session != "" {
		env = append(env, helperSessionEnvVar+"="+session)
	}
	cmd.Env = env
	return cmd
}

// runHelperCollectOutput starts cmd, waits for it, and returns its
// trimmed combined stdout — used by tests that only care about the
// helper's final line, not about streaming it live (unlike
// startLockHolder, which must synchronize on "LOCKED" before the parent
// proceeds).
func runHelperCollectOutput(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Non-zero exit is itself informative for some helpers (e.g.
		// LOCK_ERROR); let callers inspect output text rather than
		// failing here unconditionally.
		return strings.TrimSpace(string(out)) + "\n<exit error: " + err.Error() + ">"
	}
	return strings.TrimSpace(string(out))
}

// TestCrossProcessLockRace_ManyProcessesExactlyOneWins starts N real OS
// processes as close to simultaneously as exec.Cmd.Start allows, all
// racing to acquire and then *hold* the lock (via "trylock-hold", not the
// fire-and-forget "trylock-once" this test originally used — see the long
// comment on runTryLockHoldHelper in lock_ipc_test.go for why the
// distinction matters). Exactly one must observe success and every other
// racer must lose against that *live* holder — this is the mutual
// exclusion guarantee the run lock exists to provide, and it can only be
// falsified by something that shares no in-process state with the
// others, which rules out goroutines entirely.
//
// An earlier version of this test had winners exit immediately after
// TryLock succeeded, without holding the lock. That produced multiple
// "WON" reports — not a mutual-exclusion bug, but the stale-lock
// crash-recovery path (isStale's dead-pid check) correctly reclaiming a
// lock whose "holder" had already exited, once per straggler that arrived
// after the previous winner was already gone. Each reclaim still only
// ever happened after the prior holder was confirmed dead, so no two
// processes ever held the lock at the same instant — but the test wasn't
// actually exercising contention against a live run, which is what item
// 22 asks to prove. See TestCrossProcessStaleLockRecoveryOnKill for that
// crash-recovery path pinned on its own, and
// TestCrossProcessLockRace_LiveHolderNeverDisplaced below for the
// many-racers-vs-one-live-holder case this test used to conflate with it.
func TestCrossProcessLockRace_ManyProcessesExactlyOneWins(t *testing.T) {
	repo := t.TempDir()

	const n = 12
	cmds := make([]*exec.Cmd, n)
	bufs := make([]bytes.Buffer, n)
	for i := range cmds {
		cmds[i] = newHelperCmd("trylock-hold", repo, "")
		// Long enough to comfortably outlast every racer's exec/start
		// overhead on a loaded CI machine, short enough not to slow the
		// suite down noticeably.
		cmds[i].Env = append(cmds[i].Env, holdDurationEnvVar+"=300ms")
		cmds[i].Stdout = &bufs[i]
		cmds[i].Stderr = &bufs[i]
	}

	// Start every process first, then wait for all — starting them in a
	// tight loop with no synchronization between Start calls is what
	// makes this a genuine race across process boundaries rather than a
	// serialized sequence of attempts. Output must be wired up via
	// Stdout/Stderr before Start (not via CombinedOutput afterward, which
	// would try to Start an already-started cmd).
	for i, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
	}

	wins := 0
	losses := 0
	for i, cmd := range cmds {
		err := cmd.Wait()
		if err != nil {
			t.Fatalf("helper %d exited with error: %v\noutput: %s", i, err, bufs[i].String())
		}
		text := strings.TrimSpace(bufs[i].String())
		switch {
		case strings.Contains(text, "WON"):
			wins++
			if !strings.Contains(text, "RELEASED") {
				t.Fatalf("helper %d won but did not report a clean release: %q", i, text)
			}
		case strings.Contains(text, "LOST"):
			losses++
		default:
			t.Fatalf("helper %d produced unexpected output: %q", i, text)
		}
	}

	if wins != 1 {
		t.Fatalf("got %d winners racing for the lock across %d real, lock-holding processes, want exactly 1 (losses=%d) — mutual exclusion violated", wins, n, losses)
	}
	if losses != n-1 {
		t.Fatalf("got %d losers, want %d", losses, n-1)
	}
}

// TestCrossProcessLockRace_LiveHolderNeverDisplaced pins the other half
// of the same guarantee explicitly: while one real process holds the
// lock and is confirmed alive, many other real processes racing TryLock
// against it must *all* lose — none may be granted the lock out from
// under a live holder, regardless of how many contenders pile up.
func TestCrossProcessLockRace_LiveHolderNeverDisplaced(t *testing.T) {
	repo := t.TempDir()

	holder := startLockHolder(t, repo) // blocks forever until killed; a genuinely live holder
	defer func() {
		holder.Process.Kill()
		holder.Wait()
	}()

	const n = 15
	cmds := make([]*exec.Cmd, n)
	bufs := make([]bytes.Buffer, n)
	for i := range cmds {
		cmds[i] = newHelperCmd("trylock-hold", repo, "")
		cmds[i].Stdout = &bufs[i]
		cmds[i].Stderr = &bufs[i]
	}
	for i, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
	}

	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d exited with error: %v\noutput: %s", i, err, bufs[i].String())
		}
		text := strings.TrimSpace(bufs[i].String())
		if !strings.Contains(text, "LOST") {
			t.Fatalf("helper %d was granted the lock while a live holder held it: %q", i, text)
		}
	}
}

// TestCrossProcessLockRace_DeadHolderReclaimedByOneRacer pins the
// counterpart to the test above: stale-lock reclaim is intended
// behaviour, not a bug — a lock file naming a pid that is genuinely dead
// (crash, kill, OOM) must be reclaimable, and when many real processes
// race to reclaim it simultaneously, mutual exclusion must still hold:
// exactly one of them gets it.
func TestCrossProcessLockRace_DeadHolderReclaimedByOneRacer(t *testing.T) {
	repo := t.TempDir()

	holder := startLockHolder(t, repo)
	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("kill lock-holder: %v", err)
	}
	holder.Wait()
	// The holder is now confirmed dead (Wait returned), but its lock file
	// is still on disk naming its now-dead pid — exactly the state a real
	// crash leaves behind.

	const n = 12
	cmds := make([]*exec.Cmd, n)
	bufs := make([]bytes.Buffer, n)
	for i := range cmds {
		cmds[i] = newHelperCmd("trylock-hold", repo, "")
		cmds[i].Env = append(cmds[i].Env, holdDurationEnvVar+"=300ms")
		cmds[i].Stdout = &bufs[i]
		cmds[i].Stderr = &bufs[i]
	}
	for i, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
	}

	wins := 0
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d exited with error: %v\noutput: %s", i, err, bufs[i].String())
		}
		text := strings.TrimSpace(bufs[i].String())
		if strings.Contains(text, "WON") {
			wins++
		}
	}

	if wins != 1 {
		t.Fatalf("got %d racers reclaim a dead holder's lock, want exactly 1 — either reclaim is broken (0) or mutual exclusion is broken (>1)", wins)
	}
}

// TestCoalescing_ConcurrentEnqueuesAndPendingFromRealProcesses spawns many
// real OS processes that each enqueue one trigger, plus several more that
// each set the pending flag, all while a separate real process holds the
// run lock (standing in for a run in progress) — mirroring "scribe hook"
// invocations racing a live "scribe run". It then proves the two halves
// of coalescing: every enqueued trigger survives (none lost, none
// duplicated), and the pending flag — regardless of how many processes
// raced to set it — is taken exactly once.
func TestCoalescing_ConcurrentEnqueuesAndPendingFromRealProcesses(t *testing.T) {
	repo := t.TempDir()

	holder := startLockHolder(t, repo)
	defer func() {
		holder.Process.Kill()
		holder.Wait()
	}()

	const nEnqueue = 20
	const nPending = 6

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string

	for i := 0; i < nEnqueue; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := fmt.Sprintf("coalesce-%d", i)
			cmd := newHelperCmd("enqueue", repo, session)
			out := runHelperCollectOutput(t, cmd)
			if !strings.Contains(out, "ENQUEUED") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("session %s: %s", session, out))
				mu.Unlock()
			}
		}(i)
	}
	for i := 0; i < nPending; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := newHelperCmd("pending", repo, "")
			out := runHelperCollectOutput(t, cmd)
			if !strings.Contains(out, "PENDING_SET") {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("pending helper %d: %s", i, out))
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("%d helper process(es) failed:\n%s", len(failures), strings.Join(failures, "\n"))
	}

	// The run lock being held this whole time must not have blocked any
	// of the above — Enqueue/SetPending never touch it (see queue.go's
	// package doc). Now release it and confirm what the "run in
	// progress" would see on its next loop iteration.
	holder.Process.Kill()
	holder.Wait()

	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	triggers, err := q.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	got := make([]string, 0, len(triggers))
	for _, tr := range triggers {
		got = append(got, tr.SessionID)
	}
	sort.Strings(got)

	want := make([]string, 0, nEnqueue)
	for i := 0; i < nEnqueue; i++ {
		want = append(want, fmt.Sprintf("coalesce-%d", i))
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("drained %d triggers from %d real concurrent enqueue processes, want %d\ngot:  %v\nwant: %v", len(got), nEnqueue, nEnqueue, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("drained session set mismatch at index %d: got %q want %q\nfull got:  %v\nfull want: %v", i, got[i], want[i], got, want)
		}
	}

	// Pending must have been set (nPending processes raced to set it) and
	// must be taken exactly once, no matter how many processes raced to
	// set it — that is the whole point of coalescing: N triggers landing
	// during a run collapse into one "run again" signal, not N.
	pending, err := q.TakePending()
	if err != nil {
		t.Fatalf("TakePending: %v", err)
	}
	if !pending {
		t.Fatalf("expected pending flag set after %d concurrent real-process SetPending calls", nPending)
	}
	pending2, err := q.TakePending()
	if err != nil {
		t.Fatalf("second TakePending: %v", err)
	}
	if pending2 {
		t.Fatalf("TakePending returned true a second time — pending was not consumed exactly once")
	}
}

// TestNoTriggerLostUnderRealProcessContention enqueues from many real OS
// processes while this process repeatedly drains concurrently (standing
// in for a run actively working through the queue), and asserts the union
// of everything drained across every Drain call, plus whatever a final
// sweep picks up, equals exactly the set of triggers the helper processes
// enqueued — no loss, no duplication, regardless of how Drain calls land
// relative to the concurrent Enqueue processes.
func TestNoTriggerLostUnderRealProcessContention(t *testing.T) {
	repo := t.TempDir()
	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const n = 40

	var enqueueWG sync.WaitGroup
	var enqueueFailures int32Counter

	for i := 0; i < n; i++ {
		enqueueWG.Add(1)
		go func(i int) {
			defer enqueueWG.Done()
			session := fmt.Sprintf("nolost-%d", i)
			cmd := newHelperCmd("enqueue", repo, session)
			out := runHelperCollectOutput(t, cmd)
			if !strings.Contains(out, "ENQUEUED") {
				enqueueFailures.inc()
			}
		}(i)
	}

	// Drain concurrently with the enqueue processes, from this process,
	// until every helper has exited, then do one final sweep.
	done := make(chan struct{})
	go func() {
		enqueueWG.Wait()
		close(done)
	}()

	var mu sync.Mutex
	seen := make(map[string]int)
	collect := func() {
		triggers, err := q.Drain()
		if err != nil {
			t.Errorf("Drain: %v", err)
			return
		}
		mu.Lock()
		for _, tr := range triggers {
			seen[tr.SessionID]++
		}
		mu.Unlock()
	}

drainLoop:
	for {
		select {
		case <-done:
			break drainLoop
		default:
			collect()
			time.Sleep(2 * time.Millisecond)
		}
	}
	// Final sweep: anything enqueued after the last in-loop Drain but
	// before the last helper process actually exited.
	collect()

	if enqueueFailures.get() != 0 {
		t.Fatalf("%d of %d real enqueue processes failed", enqueueFailures.get(), n)
	}

	if len(seen) != n {
		missing := make([]string, 0)
		for i := 0; i < n; i++ {
			s := fmt.Sprintf("nolost-%d", i)
			if seen[s] == 0 {
				missing = append(missing, s)
			}
		}
		t.Fatalf("got %d distinct sessions across all drains, want %d; missing: %v (seen=%v)", len(seen), n, missing, seen)
	}
	for k, c := range seen {
		if c != 1 {
			t.Fatalf("session %s was drained %d times, want exactly 1 (duplicated across concurrent Drain calls)", k, c)
		}
	}
}

// int32Counter is a tiny race-safe counter, used instead of importing
// sync/atomic just for one int in this file.
type int32Counter struct {
	mu sync.Mutex
	n  int
}

func (c *int32Counter) inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *int32Counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}
