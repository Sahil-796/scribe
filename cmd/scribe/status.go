package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/scribe"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Is scribe on, when did it last run, is anything queued or failing",
		Long: `scribe status reports, for this repo: whether scribe is on or off, when
the writer last ran, whether any triggers are queued or pending, and
whether the last run failed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.OutOrStdout())
		},
	}
}

// runStatus is status.go's one entry point, kept as a plain function (not
// inlined in RunE) so tests can call it directly against a chosen repo
// root without going through cobra or os.Getwd.
func runStatus(out io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("scribe status: %w", err)
	}
	root, ok := hook.FindRepoRoot(cwd)
	if !ok {
		// Not even a git repo scribe could ever be initialised in. Not an
		// error — see the package-level rule that status must always say
		// something useful rather than fail.
		fmt.Fprintln(out, "Not a git repository — scribe has nothing to report here.")
		return nil
	}

	printStatusState(out, root)
	printStatusLastRun(out, root)
	printStatusQueue(out, root)
	printStatusLock(out, root)
	printStatusHookFailure(out, root)
	return nil
}

// printStatusState reports the on/off/paused/never-initialised state from
// install.Config, per the phase 04 spec: Enabled and Pause are separate
// axes (Enabled means "this repo was onboarded," Pause is separate expiring
// state), and ErrNotInitialised is the default everywhere, not a failure.
func printStatusState(out io.Writer, root string) {
	cfg, err := install.ReadConfig(root)
	if errors.Is(err, install.ErrNotInitialised) {
		fmt.Fprintln(out, "scribe: never initialised in this repo. Run `scribe init` to turn it on.")
		return
	}
	if err != nil {
		fmt.Fprintf(out, "scribe: could not read config: %v\n", err)
		return
	}

	if !cfg.Enabled {
		fmt.Fprintln(out, "scribe: off (this repo was initialised, but scribe is not enabled).")
		return
	}

	now := time.Now()
	if cfg.IsPaused(now) {
		switch {
		case cfg.Pause.Stay:
			fmt.Fprintln(out, "scribe: paused indefinitely (`scribe off --stay`). `scribe on` to resume.")
		case cfg.Pause.Until != nil:
			fmt.Fprintf(out, "scribe: paused until %s. `scribe on` to resume now.\n", cfg.Pause.Until.Format("2006-01-02 15:04 MST"))
		default:
			fmt.Fprintln(out, "scribe: paused. `scribe on` to resume.")
		}
		return
	}

	fmt.Fprintln(out, "scribe: on.")
}

// printStatusLastRun reports when the writer last ran and whether that run
// changed anything, from the run snapshot internal/docs records (see
// internal/docs/lastrun.go). A repo with no recorded run — never
// initialised, or initialised but never triggered — gets a plain statement
// rather than an error.
func printStatusLastRun(out io.Writer, root string) {
	store, err := docs.Open(root)
	if err != nil {
		fmt.Fprintf(out, "last run: could not open docs store: %v\n", err)
		return
	}
	snap, ok, err := store.LastRun()
	if err != nil {
		fmt.Fprintf(out, "last run: could not read run snapshot: %v\n", err)
		return
	}
	if !ok {
		fmt.Fprintln(out, "last run: none recorded yet.")
		return
	}

	changed := 0
	for _, c := range snap.Docs {
		if c.Before != c.After {
			changed++
		}
	}

	fmt.Fprintf(out, "last run: %s", snap.RanAt.Local().Format("2006-01-02 15:04:05 MST"))
	switch {
	case len(snap.Docs) == 0:
		fmt.Fprintln(out, " — touched nothing.")
	case changed == 0:
		fmt.Fprintf(out, " — touched %s, changed nothing.\n", pluralDocs(len(snap.Docs)))
	default:
		fmt.Fprintf(out, " — changed %s. `scribe diff` for details.\n", pluralDocs(changed))
	}
}

func pluralDocs(n int) string {
	if n == 1 {
		return "1 doc"
	}
	return fmt.Sprintf("%d docs", n)
}

// statusQueuedTrigger is the subset of a queue.jsonl line this command
// needs. status reads the queue file directly rather than importing
// internal/queue's Drain (which would consume it) — status is read-only by
// design, and internal/queue's package doc already documents queue.jsonl as
// plain newline-delimited scribe.Trigger JSON, so this is reading the
// documented on-disk format, not reaching into the package's internals.
func printStatusQueue(out io.Writer, root string) {
	stateDir := filepath.Join(root, scribe.StateDir)

	triggers := readQueuedTriggers(filepath.Join(stateDir, "queue.jsonl"))
	pending := fileExists(filepath.Join(stateDir, "pending"))

	switch len(triggers) {
	case 0:
		fmt.Fprint(out, "queue: empty")
	case 1:
		fmt.Fprint(out, "queue: 1 trigger waiting")
	default:
		fmt.Fprintf(out, "queue: %d triggers waiting", len(triggers))
	}
	if pending {
		fmt.Fprint(out, "; pending flag set — a run is in progress and will loop to cover this")
	}
	fmt.Fprintln(out, ".")
}

// readQueuedTriggers best-effort parses queue.jsonl. A missing file (the
// common case — nothing has ever been enqueued, or the last run drained
// it) reads as no triggers, not an error; a line that fails to parse is
// skipped rather than failing the whole read, matching how
// internal/hook.RecentFailures treats its own best-effort log.
func readQueuedTriggers(path string) []scribe.Trigger {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []scribe.Trigger
	// json.Decoder (unlike json.Unmarshal) handles a stream of concatenated
	// JSON values, which is exactly what one-object-per-line looks like to
	// it — no need to split the file into lines first.
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var t scribe.Trigger
		if err := dec.Decode(&t); err != nil {
			// EOF is the normal end of stream; anything else is a single
			// malformed line, which stops decoding — what's already been
			// decoded is still a reasonable best-effort answer.
			break
		}
		out = append(out, t)
	}
	return out
}

// statusLockInfo mirrors internal/queue's unexported lockInfo JSON shape
// (pid, hostname, started_at) so status can report who holds the run lock
// and since when without internal/queue exporting anything new — the lock
// file's format is already documented in that package's doc comment as
// "an O_EXCL lockfile recording the holder's pid, hostname and start time."
type statusLockInfo struct {
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}

// printStatusLock reports whether the per-repo run lock is currently held.
// A wedged lock — a holder that crashed without cleaning up — is a real
// failure mode the phase 04 spec calls out by name, so this reports the
// lock's age plainly rather than trying to judge staleness itself; deciding
// whether a lock is stale enough to reclaim is internal/queue's job
// (TryLock), not this read-only command's.
func printStatusLock(out io.Writer, root string) {
	path := filepath.Join(root, scribe.StateDir, "lock")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(out, "lock: not held.")
		return
	}

	var info statusLockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		fmt.Fprintln(out, "lock: held (contents unreadable).")
		return
	}

	age := time.Since(info.StartedAt).Round(time.Second)
	fmt.Fprintf(out, "lock: held by pid %d on %s, since %s ago.\n", info.PID, info.Hostname, age)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// printStatusHookFailure reports the most recent Stop hook failure, if any,
// reusing internal/hook.RecentFailures the same way cmd/scribe/doctor.go
// does. Only the newest entry is shown here — status is meant to be read at
// a glance; the full bounded log is what doctor is for.
func printStatusHookFailure(out io.Writer, root string) {
	entries, err := hook.RecentFailures(root)
	if err != nil || len(entries) == 0 {
		fmt.Fprintln(out, "hook failures: none recorded.")
		return
	}
	last := entries[len(entries)-1]
	session := last.SessionID
	if session == "" {
		session = "-"
	}
	fmt.Fprintf(out, "hook failures: most recent at %s (session=%s): %s\n",
		last.Time.Local().Format("2006-01-02 15:04:05 MST"), session, last.Reason)
}
