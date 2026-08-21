package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "scribe",
		Short: "Keep four markdown docs per repo current from Claude Code transcripts",
		Long: `scribe reads Claude Code transcripts and keeps docs/scribe/{PROJECT,DECISIONS,
CHANGELOG,JOURNAL}.md current — automatically, no manual input — using a
second agent so your own Claude session is never interrupted.

Off is the default everywhere. Run "scribe init" in a repo to turn it on.

See docs/PLAN.md for the full design.`,
		SilenceUsage:  true,
		SilenceErrors: true, // commands own their error output; main prints it once
	}

	root.AddCommand(
		newHookCmd(),
		newInitCmd(),
		newStatusCmd(),
		newRunCmd(),
		newDiffCmd(),
		newOnCmd(),
		newOffCmd(),
		newDoctorCmd(),
		newNudgeCmd(),
	)

	return root
}
