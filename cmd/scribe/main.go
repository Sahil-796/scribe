// Command scribe keeps four markdown docs per repo current from Claude Code
// transcripts. See docs/PLAN.md for the design.
//
// Only `scribe hook` is fully functional in phase 01 (see docs/PLAN.md,
// "Phases > 01 — The loop"). Every other command is registered with correct
// help text but stubbed out until its phase lands.
package main

import (
	"fmt"
	"os"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "scribe: %v\n", err)
		os.Exit(1)
	}
}
