package main

import "github.com/spf13/cobra"

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Turn scribe on for this repo (not implemented until phase 02)",
		Long: `scribe init turns scribe on for this repo: installs the Stop hook, seeds
docs/scribe/PROJECT.md and DECISIONS.md from the repo, and replays this
repo's past Claude Code sessions into CHANGELOG.md and JOURNAL.md.

It's the only command most people ever need to run.

Lands in phase 02, built on a huh wizard: pick your writer agent, pick a
model, confirm the docs path, watch the replay progress, review what it
wrote before it touches the repo.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scribe init", "02")
		},
	}
}
