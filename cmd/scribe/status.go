package main

import "github.com/spf13/cobra"

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Is scribe on, when did it last run, is anything queued or failing (not implemented until phase 04)",
		Long: `scribe status reports, for this repo: whether scribe is on or off, when
the writer last ran, whether any triggers are queued or pending, and
whether the last run failed.

Lands in phase 04 alongside "scribe diff" and "scribe doctor".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scribe status", "04")
		},
	}
}
