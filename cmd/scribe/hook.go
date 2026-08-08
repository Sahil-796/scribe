package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/queue"
)

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook",
		Short:  "Internal: what the Stop hook calls",
		Hidden: true, // never typed by a human
		Long: `scribe hook reads the Claude Code Stop hook's JSON payload on stdin,
enqueues a trigger for this repo, and exits.

This is the one command with a real performance budget: under 50ms, no
network, and it must never block the user's Claude session. It exits 0
quietly, doing nothing, in any repo scribe was never initialised in — off
is the default everywhere.

Never typed by a human; installed as the Stop hook command by "scribe init".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bypass cobra's error-printing path entirely: exit directly with
			// the code hook.Run computed. Every extra layer here is latency
			// this command isn't allowed to spend.
			code := hook.Run(cmd.InOrStdin(), cmd.ErrOrStderr(), queue.Enqueue)
			os.Exit(code)
			return nil // unreachable
		},
	}
}
