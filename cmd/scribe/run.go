package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/index"
	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/layout"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/redact"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/sessions"
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

	// LayeredConfig, not ReadConfig: the user's global defaults fill in any
	// field this repo's config didn't set (internal/install/global.go).
	cfg, err := install.LayeredConfig(repoRoot)
	if err != nil {
		if errors.Is(err, install.ErrNotInitialised) {
			return fmt.Errorf(`scribe run: scribe isn't on for %s yet — run "scribe init --apply" first`, repoRoot)
		}
		return fmt.Errorf("scribe run: reading config: %w", err)
	}
	if !cfg.Enabled {
		return fmt.Errorf(`scribe run: scribe is off for %s — run "scribe on" (or re-run "scribe init --apply") first`, repoRoot)
	}
	// The hook already declines to enqueue while paused, so in the normal
	// loop this never fires. It exists because `scribe run` is also the
	// manual entrypoint a human types, and a pause the manual path ignored
	// would be a pause with a hole in it — including for whatever the queue
	// still held from before the pause.
	if cfg.IsPaused(time.Now()) {
		return fmt.Errorf(`scribe run: scribe is paused for %s — run "scribe on" to resume`, repoRoot)
	}

	q, err := queue.Open(repoRoot)
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	// The repo's configured history layout (phase 06). ParseMode already maps
	// an unset field to the conflict-free per-session default; a genuinely
	// invalid string (a hand-edited typo in config.json) is logged and falls
	// back to per-session rather than aborting the run — unlike code.weight
	// above, an unreadable layout value shouldn't cost the user a run, and
	// per-session is the safe default the plan settled on.
	mode, err := layout.ParseMode(cfg.Layout)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "scribe run: %v; falling back to %q\n", err, layout.PerSession)
		mode = layout.PerSession
	}
	store, err := docs.OpenWithLayout(repoRoot, mode)
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	// Phase 05: the worker records one summary line per session here; after
	// the run we render INDEX.md from the full set.
	sessStore, err := sessions.Open(repoRoot)
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	w, err := newWriterForRun(writer.Config{Agent: cfg.Agent, Model: cfg.Model, Command: cfg.Command, Args: cfg.Args})
	if err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	// An unrecognised code.weight is a hard error, not a fallback to the
	// default: silently downgrading "ful" to "check" would leave the user
	// with a config that reads as one thing and behaves as another.
	codeWeight, err := worker.ParseCodeWeight(cfg.Code.Weight)
	if err != nil {
		return fmt.Errorf("scribe run: %s: %w", filepath.Join(repoRoot, scribe.StateDir, "config.json"), err)
	}

	deps := worker.Deps{
		Queue:          q,
		Docs:           store,
		Writer:         w,
		ReadTranscript: transcript.Read,
		LoadOffset:     transcript.LoadOffset,
		SaveOffset:     transcript.SaveOffset,
		CodeWeight:     codeWeight,
		Redactor:       redact.New(cfg.Privacy.Redact, cfg.Privacy.Ignore),
		Sessions:       sessStore,
		Log:            cmd.ErrOrStderr(),
	}

	if err := worker.Run(deps); err != nil {
		return fmt.Errorf("scribe run: %w", err)
	}

	// Render the phase 05 views from whatever the run recorded. These are
	// pure functions of the session records (no writer call), and a failure
	// here must not fail the run that already wrote its docs — the index is
	// a skimmable view of history, not history itself. Report what happened
	// and move on.
	renderViews(cmd.ErrOrStderr(), repoRoot, sessStore)

	fmt.Fprintf(out, "scribe run: done for %s\n", repoRoot)
	return nil
}

// renderViews rebuilds docs/scribe/INDEX.md from the recorded session set.
// Best-effort: every failure is reported to errOut and then swallowed,
// because the doc-writing run this view summarises has already succeeded by
// the time this is called.
func renderViews(errOut io.Writer, repoRoot string, sessStore *sessions.Store) {
	recs, err := sessStore.All()
	if err != nil {
		fmt.Fprintf(errOut, "scribe run: could not read session records for the index: %v\n", err)
		return
	}
	if err := index.Write(repoRoot, recs); err != nil {
		fmt.Fprintf(errOut, "scribe run: could not write the session index: %v\n", err)
	}
}
