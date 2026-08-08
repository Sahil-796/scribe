// Package queue implements the concurrency core of scribe: the per-repo
// trigger queue, the cross-process run lock, and the pending-work flag that
// makes coalescing possible.
//
// State lives under <repoRoot>/.scribe (scribe.StateDir):
//
//	.scribe/
//	  queue.jsonl   append-only log of pending scribe.Trigger values
//	  queue.lock    short-lived flock guarding queue.jsonl (append + drain)
//	  lock          the per-repo run lock (O_EXCL lockfile with pid+time)
//	  pending       flag file: present means "more work arrived, run again"
//
// Intended protocol (enforced by callers, not by this package):
//
//	Enqueue is called from the hook path. It only ever appends to the
//	queue file — it never touches the run lock, so it can never block on
//	a run in progress, and it returns in well under the hook's 50ms
//	budget.
//
//	Whoever wants to run the writer calls TryLock. If it fails, another
//	run already owns the repo, so the caller calls SetPending and exits —
//	the run in progress will pick up the new trigger when it finishes.
//	If TryLock succeeds, the caller loops:
//
//	    triggers, _ := q.Drain()
//	    // ... process triggers ...
//	    more, _ := q.TakePending()
//	    if !more { break }
//	    // loop again — new triggers landed while we were working
//
//	Unlock is called once the loop ends.
//
// This is why both a queue and a pending flag exist: Drain empties the
// queue file at the start of a work unit, but a trigger can land after that
// Drain and before the run finishes. The pending flag is the signal that
// tells the run to loop instead of exiting with that trigger uncovered.
package queue

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

const (
	lockFileName       = "lock"
	queueFileName      = "queue.jsonl"
	queueLockFileName  = "queue.lock"
	pendingFileName    = "pending"
	defaultStaleAge    = 10 * time.Minute
	lockAcquireRetries = 2
)

// StaleLockAge is the age bound used, alongside pid liveness, to decide a
// run lock is abandoned rather than held by a live process. It is a var so
// tests can shrink it; production code should leave it at the default.
var StaleLockAge = defaultStaleAge

// Queue is a handle onto one repo's scribe state directory.
type Queue struct {
	repoRoot string
	dir      string

	lockPath      string
	queuePath     string
	queueLockPath string
	pendingPath   string

	locked bool
}

// Open prepares the state directory for repoRoot and returns a handle onto
// it. It does not acquire any lock.
func Open(repoRoot string) (*Queue, error) {
	dir := filepath.Join(repoRoot, scribe.StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("queue: open state dir: %w", err)
	}
	return &Queue{
		repoRoot:      repoRoot,
		dir:           dir,
		lockPath:      filepath.Join(dir, lockFileName),
		queuePath:     filepath.Join(dir, queueFileName),
		queueLockPath: filepath.Join(dir, queueLockFileName),
		pendingPath:   filepath.Join(dir, pendingFileName),
	}, nil
}

// Enqueue appends t to repoRoot's queue. It is the hook-path entry point:
// it never touches the run lock, so it can never block behind a run in
// progress, and it does a single bounded append under a microseconds-long
// flock rather than a read-modify-write of the whole file.
func Enqueue(repoRoot string, t scribe.Trigger) error {
	q, err := Open(repoRoot)
	if err != nil {
		return err
	}
	return q.append(t)
}

func (q *Queue) append(t scribe.Trigger) error {
	line, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("queue: marshal trigger: %w", err)
	}
	line = append(line, '\n')

	unlock, err := q.lockQueueFile()
	if err != nil {
		return err
	}
	defer unlock()

	f, err := os.OpenFile(q.queuePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("queue: open queue file: %w", err)
	}
	defer f.Close()

	// A single Write() of a small buffer is atomic against concurrent
	// appenders on POSIX (each write() under O_APPEND is serialized by
	// the kernel), and the queue.lock flock above additionally serializes
	// us against a concurrent Drain reading/clearing the file. Either
	// guard alone would be enough for correctness; both are cheap.
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("queue: append trigger: %w", err)
	}
	return nil
}

// Drain returns every trigger currently queued and clears the queue. It is
// safe to call concurrently with Enqueue (from other processes too): the
// two are serialized by a short-lived flock on queue.lock.
func (q *Queue) Drain() ([]scribe.Trigger, error) {
	unlock, err := q.lockQueueFile()
	if err != nil {
		return nil, err
	}
	defer unlock()

	data, err := os.ReadFile(q.queuePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("queue: read queue file: %w", err)
	}

	if err := os.Remove(q.queuePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("queue: clear queue file: %w", err)
	}

	return parseTriggers(data), nil
}

// parseTriggers decodes newline-delimited JSON triggers, skipping any line
// that fails to parse. That covers the one crash-safety edge case a single
// atomic Write() doesn't already rule out: a line truncated mid-write by a
// kill that lands between two separate append calls sharing a buffer larger
// than the kernel's atomic write guarantee. Good entries before and after a
// bad one are never lost.
func parseTriggers(data []byte) []scribe.Trigger {
	var triggers []scribe.Trigger
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var t scribe.Trigger
		if err := json.Unmarshal(line, &t); err != nil {
			continue
		}
		triggers = append(triggers, t)
	}
	return triggers
}

// lockQueueFile takes a short-lived exclusive flock guarding queue.jsonl.
// The returned func releases it. Held only for the duration of one append
// or one drain, so contention costs microseconds, not the hook's 50ms
// budget.
func (q *Queue) lockQueueFile() (func(), error) {
	f, err := os.OpenFile(q.queueLockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("queue: open queue lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("queue: flock queue lock: %w", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// SetPending marks that a trigger landed (or more work is otherwise ready)
// so the current or next run knows to pick it up. Idempotent and safe to
// call from multiple processes concurrently.
func (q *Queue) SetPending() error {
	// os.CreateTemp guarantees a unique path (it retries internally on a
	// name collision), unlike a hand-rolled pid+timestamp name: two
	// goroutines in the same process can land on the same nanosecond
	// clock reading, and a collision there would make the second
	// rename below fail with ENOENT once the first has already
	// consumed the shared tmp file.
	tmp, err := os.CreateTemp(q.dir, "pending.tmp-*")
	if err != nil {
		return fmt.Errorf("queue: create pending temp: %w", err)
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.WriteString(time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	closeErr := tmp.Close()
	if writeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("queue: write pending temp: %w", writeErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("queue: close pending temp: %w", closeErr)
	}
	if err := os.Rename(tmpPath, q.pendingPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("queue: rename pending: %w", err)
	}
	return nil
}

// TakePending reports whether the pending flag was set, clearing it
// atomically as part of the check. Only one caller ever observes true for
// a given flag-setting.
func (q *Queue) TakePending() (bool, error) {
	err := os.Remove(q.pendingPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("queue: take pending: %w", err)
	}
	return true, nil
}

// lockInfo is the content of the run lock file: enough to tell a dead
// holder from a live one.
type lockInfo struct {
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}

// TryLock attempts to acquire the per-repo run lock. It returns false, nil
// (not an error) when another run already holds it. The lock is an
// OS-level file: an O_EXCL lockfile recording the holder's pid, hostname
// and start time, which survives across process boundaries — unlike a Go
// mutex, a lock acquired by the hook process is correctly observed by a
// separately invoked worker process.
//
// A holder that was killed, crashed, or whose machine rebooted must not
// wedge the repo forever, so a lock file that already exists is checked
// for staleness before TryLock gives up: the recorded pid is checked for
// liveness (kill -0), and, as a backstop for a reused pid or a lock
// written on a different host, the lock's age is checked against
// StaleLockAge. Either signal alone is enough to reclaim it.
func (q *Queue) TryLock() (bool, error) {
	for attempt := 0; attempt < lockAcquireRetries; attempt++ {
		f, err := os.OpenFile(q.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			info := lockInfo{PID: os.Getpid(), Hostname: hostname(), StartedAt: time.Now().UTC()}
			encErr := json.NewEncoder(f).Encode(info)
			closeErr := f.Close()
			if encErr != nil || closeErr != nil {
				os.Remove(q.lockPath)
				if encErr != nil {
					return false, fmt.Errorf("queue: write lock info: %w", encErr)
				}
				return false, fmt.Errorf("queue: close lock file: %w", closeErr)
			}
			q.locked = true
			return true, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return false, fmt.Errorf("queue: create lock file: %w", err)
		}

		stale, serr := q.isStale()
		if serr != nil {
			// Unreadable lock content: be conservative and treat the
			// lock as held rather than risk two runs at once.
			return false, nil
		}
		if !stale {
			return false, nil
		}
		// Best-effort reclaim. If another process wins the race to
		// recreate the file first, our next OpenFile attempt above
		// (next loop iteration) fails with ErrExist again and, since
		// that process's lock is fresh, we correctly report false.
		os.Remove(q.lockPath)
	}
	return false, nil
}

// isStale reports whether the existing lock file names a dead holder or
// has exceeded StaleLockAge.
func (q *Queue) isStale() (bool, error) {
	b, err := os.ReadFile(q.lockPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Vanished between the failed create and this read —
			// treat as reclaimable, the next create attempt will
			// settle who actually gets it.
			return true, nil
		}
		return false, err
	}

	var info lockInfo
	if err := json.Unmarshal(b, &info); err != nil {
		// Corrupt/partial content. Fall back to file mtime for the
		// age check; we have no pid to check liveness against.
		fi, statErr := os.Stat(q.lockPath)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return true, nil
			}
			return false, statErr
		}
		return time.Since(fi.ModTime()) > StaleLockAge, nil
	}

	if info.Hostname == hostname() && !processAlive(info.PID) {
		return true, nil
	}
	if time.Since(info.StartedAt) > StaleLockAge {
		return true, nil
	}
	return false, nil
}

// processAlive reports whether pid names a live process on this host,
// using a signal-0 kill as a liveness probe: ESRCH means no such process,
// EPERM means it exists but we don't own it (still alive), nil means
// alive and ours.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// Unlock releases the run lock. It is an error to call Unlock without
// having successfully acquired it via TryLock.
func (q *Queue) Unlock() error {
	if !q.locked {
		return errors.New("queue: Unlock called without a held lock")
	}
	q.locked = false
	if err := os.Remove(q.lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("queue: remove lock file: %w", err)
	}
	return nil
}
