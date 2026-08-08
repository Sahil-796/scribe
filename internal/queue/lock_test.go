package queue

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

func TestTryLockUnlock(t *testing.T) {
	repo := t.TempDir()
	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ok, err := q.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !ok {
		t.Fatalf("expected TryLock to succeed on a fresh repo")
	}

	q2, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ok2, err := q2.TryLock()
	if err != nil {
		t.Fatalf("second TryLock: %v", err)
	}
	if ok2 {
		t.Fatalf("second TryLock succeeded while first holder still holds the lock")
	}

	if err := q.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	ok3, err := q2.TryLock()
	if err != nil {
		t.Fatalf("TryLock after unlock: %v", err)
	}
	if !ok3 {
		t.Fatalf("expected TryLock to succeed after the holder unlocked")
	}
	q2.Unlock()
}

func TestUnlockWithoutLockIsError(t *testing.T) {
	repo := t.TempDir()
	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := q.Unlock(); err == nil {
		t.Fatalf("expected error calling Unlock without a held lock")
	}
}

// TestTryLockConcurrentGoroutines races many goroutines for the same lock
// and checks exactly one holds it at a time, verified via a shared counter.
func TestTryLockConcurrentGoroutines(t *testing.T) {
	repo := t.TempDir()

	const n = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	holders := 0
	maxConcurrentHolders := 0
	acquired := 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, err := Open(repo)
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			ok, err := q.TryLock()
			if err != nil {
				t.Errorf("TryLock: %v", err)
				return
			}
			if !ok {
				return
			}
			mu.Lock()
			holders++
			acquired++
			if holders > maxConcurrentHolders {
				maxConcurrentHolders = holders
			}
			mu.Unlock()

			time.Sleep(2 * time.Millisecond)

			mu.Lock()
			holders--
			mu.Unlock()

			if err := q.Unlock(); err != nil {
				t.Errorf("Unlock: %v", err)
			}
		}()
	}
	wg.Wait()

	if maxConcurrentHolders > 1 {
		t.Fatalf("observed %d concurrent lock holders, want at most 1", maxConcurrentHolders)
	}
	if acquired == 0 {
		t.Fatalf("no goroutine ever acquired the lock")
	}
}

// TestStaleLockRecovery_DeadPid writes a lock file naming a pid that is
// guaranteed not to exist (we launch and wait out a short-lived process),
// with a fresh timestamp so only the pid-liveness check can explain
// recovery, not the age bound.
func TestStaleLockRecoveryDeadPid(t *testing.T) {
	repo := t.TempDir()
	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	deadPID := spawnAndWaitDeadPID(t)

	writeLockFile(t, q.lockPath, lockInfo{
		PID:       deadPID,
		Hostname:  hostname(),
		StartedAt: time.Now().UTC(), // fresh — age bound would NOT trigger
	})

	ok, err := q.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !ok {
		t.Fatalf("expected stale lock (dead pid) to be reclaimed")
	}
	q.Unlock()
}

// TestStaleLockRecovery_AgeBound writes a lock file naming our own (very
// much alive) pid, but with a StartedAt far enough in the past to exceed a
// shrunk StaleLockAge. This is the reboot/pid-reuse backstop.
func TestStaleLockRecoveryAgeBound(t *testing.T) {
	repo := t.TempDir()
	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	old := StaleLockAge
	StaleLockAge = 50 * time.Millisecond
	t.Cleanup(func() { StaleLockAge = old })

	writeLockFile(t, q.lockPath, lockInfo{
		PID:       os.Getpid(),
		Hostname:  hostname(),
		StartedAt: time.Now().UTC().Add(-time.Second), // older than 50ms bound
	})

	ok, err := q.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !ok {
		t.Fatalf("expected aged lock to be reclaimed by the age bound")
	}
	q.Unlock()
}

// TestLiveLockNotReclaimed is the negative case: a lock naming our own
// live pid and a fresh timestamp must NOT be reclaimed.
func TestLiveLockNotReclaimed(t *testing.T) {
	repo := t.TempDir()
	q, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	writeLockFile(t, q.lockPath, lockInfo{
		PID:       os.Getpid(),
		Hostname:  hostname(),
		StartedAt: time.Now().UTC(),
	})

	ok, err := q.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if ok {
		t.Fatalf("TryLock reclaimed a lock held by a live process")
	}
	// Clean up manually since q never actually acquired it.
	os.Remove(q.lockPath)
}

func writeLockFile(t *testing.T, path string, info lockInfo) {
	t.Helper()
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal lockInfo: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
}
