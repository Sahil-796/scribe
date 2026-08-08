package main

import "github.com/spf13/cobra"

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Is the hook installed, does the writer command work, is the model reachable (not implemented until phase 04)",
		Long: `scribe doctor checks whether the Stop hook is installed for this repo,
whether the configured writer command runs successfully, and whether its
model is reachable.

Lands in phase 04.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scribe doctor", "04")
		},
	}
}
