package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/transcript"
	"github.com/Sahil-796/scribe/internal/worker"
	"github.com/Sahil-796/scribe/internal/writer"
)

// newWriterForRun constructs the writer connector `scribe run` drives. A
// package-level var, not a direct writer.New reference, so tests can
// substitute a fake scribe.Writer without spawning a real agent subprocess
// — same seam cmd/scribe/init.go uses (see newWriter there and
// init_test.go's withFakeWriter). Named distinctly from init.go's newWriter
// so each command's tests can redirect its own seam independently.
var newWriterForRun = writer.New

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Force the worker to run now for this repo, instead of waiting for a Stop hook",
		Long: `scribe run drains whatever is queued for this repo (docs/PLAN.md's "the
loop"): take the per-repo lock, read new transcript bytes for every session
with a pending trigger, call the writer once, apply whatever doc edits it
produced, and save offsets. It re-runs itself if another trigger lands while
it's working, so nothing queued gets left uncovered.

This is a manual/administrative entrypoint, not what normally drives the
loop — "scribe hook" (installed as the Stop hook by "scribe init") only
ever enqueues a trigger and returns, in under 50ms, because it must never
block a Claude Code session. Nothing today invokes "scribe run"
automatically after that; see docs/findings for the open question of what
should.

Requires the repo to already be initialised ("scribe init --apply").`,
		Args: cobra.NoArgs,
		RunE: runRun,
	}
}

func runRun(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	repoRoot, ok := findGitRoot(cwd)
	if !ok {
		return fmt.Errorf("scribe run: %s is not inside a git repository — scribe keeps its queue, docs and config per-repo, so run needs one to attach to", cwd)
	}

	cfg, err := install.ReadConfig(repoRoot)
	if err != nil {
		if errors.Is(err, install.ErrNotInitialised) {
			return fmt.Errorf(`scribe run: scribe isn't on for %s yet — run "scribe init --apply" first`, repoRoot)
		}
		return fmt.Errorf("scribe run: reading config: %w", err)
	}
	if !cfg.Enabled {
		return fmt.Errorf(`scribe run: scribe is off for %s — run "scribe on" (or re-run "scribe init --apply") first`, repoRoot)
	}

	q, err := queue.Open(repoRoot)
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	store, err := docs.Open(repoRoot)
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	w, err := newWriterForRun(writer.Config{Agent: cfg.Agent, Model: cfg.Model})
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	deps := worker.Deps{
		Queue:          q,
		Docs:           store,
		Writer:         w,
		ReadTranscript: transcript.Read,
		LoadOffset:     transcript.LoadOffset,
		SaveOffset:     transcript.SaveOffset,
	}

	if err := worker.Run(deps); err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	fmt.Fprintf(out, "scribe run: done for %s\n", repoRoot)
	return nil
}
