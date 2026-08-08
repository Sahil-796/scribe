package main

import "github.com/spf13/cobra"

func newOnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "on",
		Short: "Resume scribe for this repo now (not implemented until phase 04)",
		Long: `scribe on resumes scribe for this repo immediately, undoing a prior
"scribe off".

Off is the default everywhere until "scribe init" runs. Lands in phase 04.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scribe on", "04")
		},
	}
}

func newOffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "off",
		Short: "Pause scribe for this repo without uninstalling (not implemented until phase 04)",
		Long: `scribe off pauses scribe for this repo without uninstalling the hook.

By default the pause expires at the end of the day, because a permanent
pause you forget about fails the same way forgetting to turn scribe on
does. Pass --stay to pause indefinitely; "scribe on" resumes at any time.

Lands in phase 04.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scribe off", "04")
		},
	}
	cmd.Flags().Bool("stay", false, "pause indefinitely instead of expiring at end of day")
	return cmd
}
