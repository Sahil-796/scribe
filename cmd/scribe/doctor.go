package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/hook"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Is the hook installed, does the writer command work, is the model reachable (not implemented until phase 04)",
		Long: `scribe doctor checks whether the Stop hook is installed for this repo,
whether the configured writer command runs successfully, and whether its
model is reachable.

The full set of checks lands in phase 04. Ahead of that, doctor already
shows recent Stop hook failures for this repo (docs/findings/OPEN-ITEMS.md,
item 11) — a failing hook exits non-zero with nothing else visible to the
user, so this is the first thing worth having something to inspect.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			printRecentHookFailures(cmd.OutOrStdout())
			return notImplemented("scribe doctor", "04")
		},
	}
}

// printRecentHookFailures writes any entries from this repo's bounded
// hook-failures log (see internal/hook.RecentFailures) to out. It is
// deliberately silent — no error, no "nothing to show" message — when
// there's no repo, no log, or a log that fails to read: doctor's real
// checks (hook installed? writer works? model reachable?) are still the
// stub they always were until phase 04, so this one extra bit of
// diagnostic output shouldn't itself start failing the command or add
// noise to the common case of "scribe was never initialised here."
func printRecentHookFailures(out io.Writer) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	root, ok := hook.FindRepoRoot(cwd)
	if !ok {
		return
	}
	entries, err := hook.RecentFailures(root)
	if err != nil || len(entries) == 0 {
		return
	}

	fmt.Fprintf(out, "Recent Stop hook failures for %s (oldest first):\n", root)
	for _, e := range entries {
		session := e.SessionID
		if session == "" {
			session = "-"
		}
		fmt.Fprintf(out, "  %s  session=%s  %s\n", e.Time.Format("2006-01-02T15:04:05Z07:00"), session, e.Reason)
	}
	fmt.Fprintln(out)
}
