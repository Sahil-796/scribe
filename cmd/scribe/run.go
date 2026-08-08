package main

import "github.com/spf13/cobra"

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Force a writer run now, don't wait for a reply (not implemented until phase 01's worker lands)",
		Long: `scribe run forces the worker to run immediately for this repo, instead of
waiting for the next Stop hook trigger: new transcript bytes in, the four
docs read, the writer called, docs edited.

The worker itself (queue drain, per-repo lock, coalescing) is being built
alongside this command as part of phase 01 — "scribe hook" is the only
piece of phase 01 wired up in this change.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scribe run", "01 (worker)")
		},
	}
}
