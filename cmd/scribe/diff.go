package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/scribe"
)

func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "What did the last writer run change in docs/scribe/",
		Long: `scribe diff shows what the last writer run changed in docs/scribe/.

It reads a snapshot the run itself recorded (internal/docs/lastrun.go), not
git — nothing commits docs/scribe/, so a plain "git diff" there shows
everything since your last human commit, not what the last scribe run did.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd.OutOrStdout())
		},
	}
}

// runDiff is diff.go's entry point, kept separate from RunE so tests can
// call it directly against a chosen repo root, the same way runStatus does.
func runDiff(out io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("scribe diff: %w", err)
	}
	root, ok := hook.FindRepoRoot(cwd)
	if !ok {
		fmt.Fprintln(out, "Not a git repository — scribe has nothing to report here.")
		return nil
	}

	store, err := docs.Open(root)
	if err != nil {
		return fmt.Errorf("scribe diff: %w", err)
	}

	snap, ok, err := store.LastRun()
	if err != nil {
		return fmt.Errorf("scribe diff: %w", err)
	}
	if !ok {
		fmt.Fprintln(out, "No run has been recorded yet — scribe hasn't written anything here.")
		return nil
	}

	changed := changedDocsInOrder(snap)
	if len(changed) == 0 {
		fmt.Fprintf(out, "The last run (%s) touched nothing.\n", snap.RanAt.Local().Format("2006-01-02 15:04:05 MST"))
		return nil
	}

	fmt.Fprintf(out, "Last run: %s\n\n", snap.RanAt.Local().Format("2006-01-02 15:04:05 MST"))
	for i, doc := range changed {
		if i > 0 {
			fmt.Fprintln(out)
		}
		change := snap.Docs[doc]
		if note := rotationNote(doc, change.Before, change.After); note != "" {
			fmt.Fprintln(out, note)
		}
		fmt.Fprint(out, renderUnifiedDiff(string(doc), change.Before, change.After))
	}
	return nil
}

// changedDocsInOrder returns the docs in snap whose content actually
// differs, in scribe.AllDocs order (rather than Go's randomised map
// iteration) so `scribe diff`'s output is stable run to run. A doc the run
// touched but left byte-for-byte identical (recorded by internal/docs with
// Before == After — see lastrun.go) is deliberately excluded here: that's
// still worth `scribe status` saying "touched N docs, changed nothing," but
// listing it here as an empty diff would just be noise.
func changedDocsInOrder(snap docs.RunSnapshot) []scribe.Doc {
	var out []scribe.Doc
	for _, doc := range scribe.AllDocs {
		if change, ok := snap.Docs[doc]; ok && change.Before != change.After {
			out = append(out, doc)
		}
	}
	return out
}

// rotationNote returns an explanatory line when before→after removed a
// chunk of a history doc's live content because rotateHistory (see
// internal/docs/rotate.go) moved it to an archive file, not because the
// writer deleted it. Without this, a rotation — which routinely removes far
// more text from the live doc than any single writer edit would — reads
// exactly like data loss in a raw line diff. Returns "" when nothing was
// rotated between before and after (the overwhelmingly common case).
func rotationNote(doc scribe.Doc, before, after string) string {
	if !docs.IsHistoryDoc(doc) {
		return ""
	}

	rotated := map[string]int{} // archive relPath -> additional entries rotated there
	_, beforeBlocks := docs.SplitBlocks(before)
	_, afterBlocks := docs.SplitBlocks(after)

	beforeCounts := archivePointerCounts(beforeBlocks)
	for _, b := range afterBlocks {
		n, relPath, ok := docs.ParseArchivePointer(b)
		if !ok {
			continue
		}
		if delta := n - beforeCounts[relPath]; delta > 0 {
			rotated[relPath] += delta
		}
	}
	if len(rotated) == 0 {
		return ""
	}

	var parts []string
	for relPath, n := range rotated {
		noun := "entry"
		if n != 1 {
			noun = "entries"
		}
		parts = append(parts, fmt.Sprintf("%d %s to %s", n, noun, relPath))
	}
	return fmt.Sprintf("Note: this run archived %s (moved, not deleted) — see the diff below for what stayed in the live doc.",
		strings.Join(parts, ", "))
}

// archivePointerCounts maps each archive pointer block's relPath to its
// recorded count, for the "how many were already archived there before
// this run" side of rotationNote's comparison.
func archivePointerCounts(blocks []string) map[string]int {
	out := map[string]int{}
	for _, b := range blocks {
		if n, relPath, ok := docs.ParseArchivePointer(b); ok {
			out[relPath] = n
		}
	}
	return out
}
