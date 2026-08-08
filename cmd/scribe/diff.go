package main

import "github.com/spf13/cobra"

func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "What did the last writer run change (not implemented until phase 04)",
		Long: `scribe diff shows what the last writer run changed in docs/scribe/.

Lands in phase 04 alongside "scribe status" and "scribe doctor".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scribe diff", "04")
		},
	}
}
