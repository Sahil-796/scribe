package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/replay"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/seed"
	"github.com/Sahil-796/scribe/internal/wizard"
	"github.com/Sahil-796/scribe/internal/writer"
)

// newWriter constructs the writer connector init's seed and replay passes
// call. A package-level var, not a direct writer.New reference, so tests
// can substitute a fake scribe.Writer without spawning a real agent
// subprocess — per this unit's safety rules, no test may invoke a real
// writer agent.
var newWriter = writer.New

// initPreviewSubdir is where `scribe init` stages what it would write,
// under scribe.StateDir (.scribe/, already gitignored local state). It is
// deliberately NOT docs/scribe: per docs/PLAN.md phase 02, "dry run by
// default, writing somewhere readable before it touches the repo" means a
// plain `scribe init` must be side-effect free against the real docs.
const initPreviewSubdir = "init-preview"

// newInitCmd builds `scribe init`: seed a first PROJECT.md/DECISIONS.md
// from the repo, replay past transcripts into CHANGELOG.md/JOURNAL.md, and
// — only once the operator has seen and approved the result — write it for
// real, install the Stop hook, and turn scribe on.
//
// The command runs in two stages that are deliberately independent of each
// other:
//
//  1. Always: compute a preview. This is the expensive part — it calls the
//     writer agent, possibly many times over chunked transcript history —
//     so it is the part that needs to be resumable, and it is, via
//     replay.Options.StatePath pointed at a preview-scoped checkpoint file
//     (see previewStatePath). Nothing here touches docs/scribe or
//     .claude/settings.json.
//  2. Only with --apply: copy the already-computed preview content into
//     the real docs, install the hook, and write the config. This step
//     does no further writer calls, so what an interactive operator
//     approved in the review screen is byte-for-byte what lands in the
//     repo — there's no second, possibly-nondeterministic writer pass
//     between "reviewed" and "written."
//
// The wizard (internal/wizard) runs whenever a terminal is attached and
// --yes wasn't passed, regardless of --apply — picking an agent and model
// is useful even for a dry run. Review (the "write these to the repo?"
// screen) only ever appears when --apply was passed, since it's pointless
// to ask "write?" when the command isn't going to.
func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Turn scribe on for this repo",
		Long: `scribe init turns scribe on for this repo: seeds docs/scribe/PROJECT.md and
DECISIONS.md from the repo itself, replays this repo's past Claude Code
sessions into CHANGELOG.md and JOURNAL.md, installs the Stop hook, and
writes .scribe/config.json.

It's the only command most people ever need to run.

Dry run by default: everything it would write is staged under
.scribe/init-preview/ and printed, but nothing touches docs/scribe and no
hook is installed. Pass --apply to make it real.

With a terminal attached (and without --yes), a wizard asks for the writer
agent and model up front, and — if --apply was passed — shows a review
screen with the exact content before writing anything. Without a terminal
(CI, scripts), it falls back to --agent/--model and their defaults; --apply
then requires --yes, since there is no way to show a review screen to
nobody.

The docs path is fixed at docs/scribe (decision 9) and is not configurable.`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}

	cmd.Flags().String("agent", "", "writer agent connector (default: "+wizard.DefaultAgent+")")
	cmd.Flags().String("model", "", "model passed to the writer agent (default: "+wizard.DefaultModel+")")
	cmd.Flags().Bool("docs-in-git", false, "commit the docs instead of adding them to .gitignore")
	cmd.Flags().Bool("yes", false, "skip the interactive wizard and review screen; use flags/defaults")
	cmd.Flags().Bool("apply", false, "write for real: docs, Stop hook, config. Default is a dry run")

	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	agentFlag, _ := cmd.Flags().GetString("agent")
	modelFlag, _ := cmd.Flags().GetString("model")
	docsInGitFlag, _ := cmd.Flags().GetBool("docs-in-git")
	yes, _ := cmd.Flags().GetBool("yes")
	apply, _ := cmd.Flags().GetBool("apply")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("scribe init: %w", err)
	}

	repoRoot, ok := findGitRoot(cwd)
	if !ok {
		return fmt.Errorf("scribe init: %s is not inside a git repository — scribe keeps its docs, hook and config per-repo, so init needs one to attach to", cwd)
	}

	// Already initialised: say so and stop, rather than silently redoing
	// the seed/replay passes and rewriting a config someone may have
	// hand-edited (a different agent, a different model).
	if cfg, cfgErr := install.ReadConfig(repoRoot); cfgErr == nil {
		fmt.Fprintf(out, "scribe is already on for %s\n  agent: %s\n  model: %s\n  docs:  %s\n\n", repoRoot, cfg.Agent, cfg.Model, cfg.DocsDir)
		fmt.Fprintln(out, `Re-running "scribe init" won't redo the seed or replay passes, or touch`)
		fmt.Fprintln(out, `the installed hook and config — those already ran once, and the Stop hook`)
		fmt.Fprintln(out, `keeps the four docs current after every reply from here on. To change the`)
		fmt.Fprintln(out, `agent or model, edit .scribe/config.json directly, or remove it and re-run`)
		fmt.Fprintln(out, `init to go through setup again.`)
		return nil
	} else if !errors.Is(cfgErr, install.ErrNotInitialised) {
		return fmt.Errorf("scribe init: checking existing config: %w", cfgErr)
	}

	// Collect agent/model/docs-dir, either from the wizard or from flags.
	useWizard := !yes && wizard.IsInteractive()
	var answers wizard.Answers
	if useWizard {
		wOpts := wizard.Options{RepoRoot: repoRoot}
		if agentFlag != "" {
			wOpts.DefaultAgent = agentFlag
		}
		if modelFlag != "" {
			wOpts.DefaultModel = modelFlag
		}
		answers, err = wizard.Ask(wOpts)
		if err != nil {
			return fmt.Errorf("scribe init: %w", err)
		}
		if !answers.Proceed {
			fmt.Fprintln(out, "scribe init: cancelled, nothing done.")
			return nil
		}
	} else {
		// No terminal: fall back to flags and the documented defaults. The
		// onboarding question the wizard asks (docs in git) has no answer
		// here, so it takes the conservative default — docs stay out of
		// git — and --docs-in-git lets a script say otherwise explicitly.
		// Layout has no flag at all (OPEN-ITEMS item 31): phase 06, the only
		// thing that would read it, doesn't exist, so it's always the
		// recorded default rather than something a script can (wrongly)
		// believe it's choosing.
		answers = wizard.Answers{
			Agent:     firstNonEmpty(agentFlag, wizard.DefaultAgent),
			Model:     firstNonEmpty(modelFlag, wizard.DefaultModel),
			DocsDir:   scribe.DocsDir,
			DocsInGit: docsInGitFlag,
			Layout:    wizard.LayoutPerSession,
			Proceed:   true,
		}
	}

	// A non-interactive --apply with no --yes has no review screen to show
	// and no operator to ask "are you sure" — that combination can only be
	// an accident (a script that forgot --yes), not an intentional choice,
	// so refuse rather than silently deciding for them.
	if apply && !yes && !wizard.IsInteractive() {
		return fmt.Errorf("scribe init: --apply with no terminal attached requires --yes (there is no review screen to show)")
	}

	w, err := newWriter(writer.Config{Agent: answers.Agent, Model: answers.Model})
	if err != nil {
		return fmt.Errorf("scribe init: %w", err)
	}

	fmt.Fprintf(out, "scribe init: %s\n\n", repoRoot)

	preview, err := computePreview(repoRoot, w, out, errOut)
	if err != nil {
		return err
	}

	previewDir := filepath.Join(repoRoot, scribe.StateDir, initPreviewSubdir)
	fmt.Fprintf(out, "\nPreview staged at %s\n", previewDir)

	if !apply {
		fmt.Fprintln(out, "\nDry run — nothing was written to docs/scribe, and no hook was installed.")
		fmt.Fprintln(out, "Re-run with --apply once you're happy with the preview.")
		fmt.Fprintln(out, "Working in other repos too? \"scribe nudge --install\" adds a one-time reminder if you go a while without running init there.")
		return nil
	}

	if useWizard {
		approved, err := wizard.Review(preview)
		if err != nil {
			return fmt.Errorf("scribe init: %w", err)
		}
		if !approved {
			fmt.Fprintf(out, "\nDiscarded — nothing written. Preview is still at %s.\n", previewDir)
			return nil
		}
	}

	if err := applyPreview(repoRoot, preview, answers, out); err != nil {
		return err
	}
	return nil
}

// computePreview runs the seed pass and the replay pass and stages
// everything they produce under .scribe/init-preview/, returning it as a
// map keyed the same way wizard.Review and the real docs.Store are keyed.
// It never writes to docs/scribe.
//
// Both passes fail loudly rather than reporting a quiet success with
// nothing written — phase 00 found `opencode run` can exit 0 having done
// nothing, and a first `scribe init` producing an empty PROJECT.md with no
// error is exactly the failure mode that has to be caught here, not
// discovered later by a confused user.
func computePreview(repoRoot string, w scribe.Writer, out, errOut io.Writer) (map[scribe.Doc]string, error) {
	fmt.Fprintln(out, "Seeding PROJECT.md and DECISIONS.md from the repo...")
	seeded, err := seed.Run(repoRoot, w)
	if err != nil {
		// seed.Run's own Parse already refuses an empty/unparseable writer
		// response, so this is already a loud failure, not a silent one.
		return nil, fmt.Errorf("scribe init: seed pass: %w", err)
	}
	for _, doc := range []scribe.Doc{scribe.DocProject, scribe.DocDecisions} {
		content, ok := seeded[doc]
		if !ok {
			continue
		}
		if err := writePreviewState(repoRoot, doc, content); err != nil {
			return nil, fmt.Errorf("scribe init: staging preview %s: %w", doc, err)
		}
	}

	sessions, err := replay.FindSessions(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("scribe init: finding past sessions: %w", err)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(out, "No prior Claude Code sessions found for this repo — nothing to replay yet.")
	} else {
		fmt.Fprintf(out, "Replaying %d prior session(s) into CHANGELOG.md and JOURNAL.md...\n", len(sessions))
	}

	var (
		stepFn          func(done int, label string)
		doneFn          func()
		chunksAttempted int
		entriesEmitted  int
	)
	replayErr := replay.Run(replay.Options{
		RepoRoot:  repoRoot,
		Writer:    w,
		StatePath: previewStatePath(repoRoot),
		Progress: func(status replay.ChunkStatus, done, total int, label string) {
			if total == 0 {
				return
			}
			if stepFn == nil {
				stepFn, doneFn = wizard.Progress(total)
			}
			// A skipped chunk was completed by an earlier run, so it says
			// nothing about whether this run produced anything.
			if status != replay.ChunkSkipped {
				chunksAttempted++
			}
			stepFn(done, label)
			if done >= total {
				doneFn()
			}
		},
		Emit: func(doc scribe.Doc, entry string) error {
			if err := appendPreviewHistory(repoRoot, doc, entry); err != nil {
				return err
			}
			entriesEmitted++
			return nil
		},
	})
	if replayErr != nil {
		// Partial failure — some chunks were replayed fine and are already
		// staged in the preview; surface the rest honestly rather than
		// swallowing it, but don't abort the command over it.
		fmt.Fprintf(errOut, "scribe init: warning: %v\n", replayErr)
	}
	if len(sessions) > 0 && chunksAttempted > 0 && entriesEmitted == 0 && replayErr == nil {
		return nil, fmt.Errorf("scribe init: replay processed %d chunk(s) across %d session(s) but the writer produced zero entries — that's the \"agent exited 0 having done nothing\" failure mode from phase 00, not a quiet success; check --agent/--model and try again", chunksAttempted, len(sessions))
	}

	preview := make(map[scribe.Doc]string, len(scribe.AllDocs))
	for _, doc := range scribe.AllDocs {
		preview[doc] = readPreview(repoRoot, doc)
	}
	return preview, nil
}

// applyPreview commits an already-computed preview to the real repo: the
// four docs, the Stop hook, and the config that turns scribe on. It makes
// no writer calls of its own — everything it writes is exactly what
// computePreview staged (and, in an interactive run, what the operator just
// approved in wizard.Review).
func applyPreview(repoRoot string, preview map[scribe.Doc]string, answers wizard.Answers, out io.Writer) error {
	store, err := docs.Open(repoRoot)
	if err != nil {
		return fmt.Errorf("scribe init: %w", err)
	}
	// Track what actually lands. A repo with no prior sessions has nothing to
	// replay, so CHANGELOG and JOURNAL legitimately don't get created — and
	// reporting all four regardless (as this did) sends the operator looking
	// for files that aren't there.
	var written []string
	for _, doc := range []scribe.Doc{scribe.DocProject, scribe.DocDecisions} {
		if err := store.WriteState(doc, preview[doc]); err != nil {
			return fmt.Errorf("scribe init: writing %s: %w", doc, err)
		}
		written = append(written, string(doc))
	}
	for _, doc := range []scribe.Doc{scribe.DocChangelog, scribe.DocJournal} {
		content := preview[doc]
		if strings.TrimSpace(content) == "" {
			continue
		}
		if err := store.AppendHistory(doc, content); err != nil {
			return fmt.Errorf("scribe init: writing %s: %w", doc, err)
		}
		written = append(written, string(doc))
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("scribe init: locating scribe binary: %w", err)
	}
	hookResult, err := install.InstallStopHook(repoRoot, binPath)
	if err != nil {
		return fmt.Errorf("scribe init: installing Stop hook: %w", err)
	}

	if err := install.WriteConfig(repoRoot, install.Config{
		Agent:     answers.Agent,
		Model:     answers.Model,
		DocsDir:   answers.DocsDir,
		Enabled:   true,
		DocsInGit: answers.DocsInGit,
		Layout:    string(answers.Layout),
	}); err != nil {
		return fmt.Errorf("scribe init: writing config: %w", err)
	}

	ignored, err := ensureGitignore(repoRoot, answers.DocsInGit)
	if err != nil {
		return fmt.Errorf("scribe init: %w", err)
	}

	fmt.Fprintf(out, "\nWrote %s/{%s}\n", scribe.DocsDir, strings.Join(written, ","))
	if len(written) < len(scribe.AllDocs) {
		fmt.Fprintln(out, "CHANGELOG.md and JOURNAL.md start empty — this repo has no past sessions to replay.")
	}
	if hookResult.AlreadyPresent {
		fmt.Fprintln(out, "Stop hook was already installed.")
	} else {
		fmt.Fprintf(out, "Installed Stop hook in %s\n", hookResult.SettingsPath)
	}
	if len(ignored) > 0 {
		fmt.Fprintf(out, "Added %s to .gitignore\n", strings.Join(ignored, " and "))
	}
	if answers.DocsInGit {
		fmt.Fprintf(out, "%s/ is committed to git — the docs are part of the repo.\n", scribe.DocsDir)
	}
	fmt.Fprintln(out, "\nscribe is now on for this repo — the four docs stay current after every reply.")
	fmt.Fprintln(out, "Working in other repos too? \"scribe nudge --install\" adds a one-time reminder if you go a while without running init there.")
	return nil
}

// ensureGitignore appends the ignore entries this repo needs and returns
// the ones it added.
//
// .scribe/ is always ignored: it's pure local state (queue, lock, offsets,
// logs) and has no business in anyone's history. docs/scribe/ depends on
// what the operator chose at onboarding — committing generated docs is a
// real choice with real trade-offs (reviewable in PRs and undoable via git,
// versus a working tree that goes dirty mid-session), and it's theirs to
// make, not scribe's (OPEN-ITEMS item 3).
//
// Without this, init leaves a repo full of untracked files it created
// itself, which reads as scribe making a mess of someone's working tree. It
// appends rather than rewrites, and matches exact lines so a repo that
// already ignores these is left alone.
func ensureGitignore(repoRoot string, docsInGit bool) ([]string, error) {
	gitignoreEntries := []string{scribe.StateDir + "/"}
	if !docsInGit {
		gitignoreEntries = append(gitignoreEntries, scribe.DocsDir+"/")
	}
	path := filepath.Join(repoRoot, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading .gitignore: %w", err)
	}

	have := make(map[string]bool)
	for _, line := range strings.Split(string(existing), "\n") {
		have[strings.TrimSpace(line)] = true
	}

	var missing []string
	for _, e := range gitignoreEntries {
		if !have[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}

	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# scribe\n")
	b.WriteString(strings.Join(missing, "\n"))
	b.WriteString("\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return nil, fmt.Errorf("writing .gitignore: %w", err)
	}
	return missing, nil
}

// findGitRoot walks up from cwd looking for a .git entry (a directory for
// a normal clone, a file for a worktree or submodule — os.Stat doesn't
// care which). scribe attaches its state to a git repo, not to an
// arbitrary directory, so init refuses politely outside one rather than
// happily writing .scribe/ and docs/scribe/ into whatever directory the
// user happened to be standing in.
func findGitRoot(cwd string) (string, bool) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// previewDirPath, previewStatePath and previewPath locate the dry-run
// staging area under .scribe/init-preview/. This deliberately reuses
// scribe.StateDir (already gitignored, already the home of local,
// non-committed state) rather than inventing a new top-level directory.
func previewDirPath(repoRoot string) string {
	return filepath.Join(repoRoot, scribe.StateDir, initPreviewSubdir)
}

func previewStatePath(repoRoot string) string {
	return filepath.Join(previewDirPath(repoRoot), "replay-state.json")
}

func previewPath(repoRoot string, doc scribe.Doc) string {
	return filepath.Join(previewDirPath(repoRoot), string(doc))
}

// writePreviewState overwrites the staged preview for a current-state doc
// (PROJECT.md, DECISIONS.md). The seed pass isn't chunked or resumable —
// one writer call either produces both docs or the whole pass errors — so
// unlike history entries there's nothing to accumulate across runs here.
func writePreviewState(repoRoot string, doc scribe.Doc, content string) error {
	if err := os.MkdirAll(previewDirPath(repoRoot), 0o755); err != nil {
		return err
	}
	return os.WriteFile(previewPath(repoRoot, doc), []byte(content), 0o644)
}

// appendPreviewHistory grows the staged preview for a history doc
// (CHANGELOG.md, JOURNAL.md) by one replay-produced entry. This mirrors
// internal/docs's AppendHistory shape (read what's there, append, write
// back) deliberately: replay.Run only calls Emit for chunks it actually
// processes this run, skipping ones a prior, interrupted run already
// completed (see replay.Options.StatePath). Reading the existing preview
// file before appending is what makes a resumed dry run end up with the
// full accumulated history rather than just whatever this run's chunks
// contributed.
func appendPreviewHistory(repoRoot string, doc scribe.Doc, entry string) error {
	if err := os.MkdirAll(previewDirPath(repoRoot), 0o755); err != nil {
		return err
	}
	path := previewPath(repoRoot, doc)
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	sep := ""
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		sep = "\n"
	}
	entry = strings.TrimRight(entry, "\n") + "\n"
	combined := append(append(existing, []byte(sep)...), []byte(entry)...)
	return os.WriteFile(path, combined, 0o644)
}

// readPreview returns the currently staged preview content for doc, or ""
// if nothing has been staged for it yet (a fresh repo with no history to
// replay, for instance — that's a legitimate "(no changes)" in
// wizard.Review, not an error).
func readPreview(repoRoot string, doc scribe.Doc) string {
	b, err := os.ReadFile(previewPath(repoRoot, doc))
	if err != nil {
		return ""
	}
	return string(b)
}

// firstNonEmpty returns the first non-empty string in vals, or "" if every
// one is empty. Used to layer flag overrides on top of package defaults
// without a chain of "if flag != \"\" { ... }" at every call site.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
