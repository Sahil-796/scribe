// Package main is a throwaway Go version of the Stop-hook probe, built only
// to measure how fast a static Go binary can read the hook payload on stdin
// and append it to a log file, for comparison against the bash probe.sh.
// Not part of the scribe binary — this is phase-00 evidence only.
package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	start := time.Now()

	logPath := os.Getenv("SCRIBE_PROBE_LOG")
	if logPath == "" {
		logPath = "/tmp/scribe-hook-probe.log"
	}

	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(0) // never block or fail the user's session
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(0)
	}
	defer f.Close()

	elapsed := time.Since(start)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	fmt.Fprintf(f, "=== %s (go internal elapsed: %s) ===\n%s\n\n", ts, elapsed, payload)

	os.Exit(0)
}
