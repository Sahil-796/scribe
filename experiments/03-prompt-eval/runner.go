// Command runner is the phase 03 prompt-eval harness: it plays a fixed
// corpus of real transcript slices (corpus/corpus.json) through a prompt
// variant (variants/*.md) against the real writer, and drops the raw output
// under runs/<run-id>/ so two variants can be diffed side by side.
//
// This is Go, not bash, for one reason: it needs internal/transcript.Read to
// turn a raw jsonl slice into scribe.Entry values exactly the way the real
// worker does (tool-noise stripping, tool_result-as-"user" filtering,
// <synthetic> assistant lines, etc — see internal/transcript's package doc).
// Reimplementing that in bash/jq would drift from the real parser the first
// time internal/transcript.go changes; importing it can't drift, and this
// package only ever reads that package, never writes to it. It lives under
// experiments/, outside internal/, so `go build ./...`/`go vet ./...` still
// see it (proving it compiles against the real types) but `go test ./...`
// has nothing to run here — no _test.go files in this directory.
//
// See README.md for usage. Kept intentionally small: list corpus items,
// build one prompt per corpus item from one variant, run it through
// internal/writer, save the result. No scoring automation — rubric.md is
// judged by a human (or a separate LLM-judge pass) reading runs/ side by
// side, not by this program.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/transcript"
	"github.com/Sahil-796/scribe/internal/writer"
)

// corpusItem mirrors one entry of corpus/corpus.json. Byte offsets are what
// actually get read; line numbers are carried along only so a human can
// find the exchange again in the source jsonl.
type corpusItem struct {
	Name        string `json:"name"`
	Session     string `json:"session"`
	FromLine    int    `json:"from_line"`
	ToLine      int    `json:"to_line"`
	FromByte    int64  `json:"from_byte"`
	ToByte      int64  `json:"to_byte"`
	Shape       string `json:"shape"`
	Description string `json:"description"`
}

type corpusFile struct {
	Items []corpusItem `json:"items"`
}

// manifestEntry records what actually happened for one (corpus item,
// variant) run, so runs/<run-id>/manifest.json is a trustworthy log of real
// invocations rather than something inferred after the fact from stray
// files.
type manifestEntry struct {
	Corpus     string        `json:"corpus"`
	Variant    string        `json:"variant"`
	Agent      string        `json:"agent"`
	Model      string        `json:"model"`
	EntryCount int           `json:"entry_count"`
	PromptFile string        `json:"prompt_file"`
	OutputFile string        `json:"output_file"`
	Duration   time.Duration `json:"duration_ns"`
	Error      string        `json:"error,omitempty"`
	StartedAt  time.Time     `json:"started_at"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("runner", flag.ExitOnError)
	var (
		listOnly    = fs.Bool("list", false, "list corpus items and exit")
		corpusDir   = fs.String("corpus-dir", defaultCorpusDir(), "directory holding corpus.json and referenced sessions cache")
		corpusPath  = fs.String("corpus-file", "", "override path to corpus.json (default: <corpus-dir>/corpus.json)")
		itemName    = fs.String("item", "all", `corpus item to run ("all" for every item)`)
		variantPath = fs.String("variant", "", "path to a prompt variant .md file (required unless -list)")
		runID       = fs.String("run-id", "", "name for this run's output directory under runs/ (default: variant-file basename + timestamp)")
		agent       = fs.String("agent", "opencode", `writer agent, passed to internal/writer.New ("opencode" or "custom")`)
		model       = fs.String("model", "opencode/longcat-2.0-free", "model flag passed to the writer agent")
		outDir      = fs.String("out-dir", defaultRunsDir(), "directory to write runs/<run-id>/ under")
		dryRun      = fs.Bool("dry-run", false, "build prompts and print what would run, but never invoke the writer")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *corpusPath == "" {
		*corpusPath = filepath.Join(*corpusDir, "corpus.json")
	}
	items, err := loadCorpus(*corpusPath)
	if err != nil {
		return err
	}

	if *listOnly {
		printCorpus(items)
		return nil
	}

	if *variantPath == "" {
		return fmt.Errorf("-variant is required (see -list for corpus items, variants/*.md for available prompts)")
	}
	variantBytes, err := os.ReadFile(*variantPath)
	if err != nil {
		return fmt.Errorf("read variant: %w", err)
	}
	instructions := string(variantBytes)

	selected, err := selectItems(items, *itemName)
	if err != nil {
		return err
	}

	id := *runID
	if id == "" {
		base := strings.TrimSuffix(filepath.Base(*variantPath), filepath.Ext(*variantPath))
		id = fmt.Sprintf("%s-%s", base, time.Now().UTC().Format("20060102T150405Z"))
	}
	runDir := filepath.Join(*outDir, id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("mkdir run dir: %w", err)
	}

	var w scribe.Writer
	if !*dryRun {
		w, err = writer.New(writer.Config{Agent: *agent, Model: *model})
		if err != nil {
			return fmt.Errorf("build writer: %w", err)
		}
	}

	var manifest []manifestEntry
	for _, item := range selected {
		entries, err := loadEntries(item)
		if err != nil {
			return fmt.Errorf("corpus item %s: %w", item.Name, err)
		}

		prompt := buildEvalPrompt(instructions, entries)
		promptFile := filepath.Join(runDir, item.Name+".prompt.txt")
		if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
			return fmt.Errorf("write prompt file: %w", err)
		}

		me := manifestEntry{
			Corpus:     item.Name,
			Variant:    filepath.Base(*variantPath),
			Agent:      *agent,
			Model:      *model,
			EntryCount: len(entries),
			PromptFile: promptFile,
			StartedAt:  time.Now().UTC(),
		}

		if *dryRun {
			fmt.Printf("[dry-run] %s: %d entries, prompt %d bytes -> %s\n", item.Name, len(entries), len(prompt), promptFile)
			manifest = append(manifest, me)
			continue
		}

		fmt.Printf("running %s (%d entries)... ", item.Name, len(entries))
		start := time.Now()
		out, runErr := w.Run(prompt)
		me.Duration = time.Since(start)

		outFile := filepath.Join(runDir, item.Name+".out.txt")
		if writeErr := os.WriteFile(outFile, []byte(out), 0o644); writeErr != nil {
			return fmt.Errorf("write output file: %w", writeErr)
		}
		me.OutputFile = outFile

		if runErr != nil {
			me.Error = runErr.Error()
			fmt.Printf("ERROR after %s: %v\n", me.Duration.Round(time.Millisecond), runErr)
		} else {
			fmt.Printf("ok in %s, %d bytes\n", me.Duration.Round(time.Millisecond), len(out))
		}
		manifest = append(manifest, me)
	}

	manifestFile := filepath.Join(runDir, "manifest.json")
	merged, err := mergeManifest(manifestFile, manifest)
	if err != nil {
		return fmt.Errorf("merge manifest: %w", err)
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(manifestFile, data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	fmt.Printf("run %q written to %s\n", id, runDir)
	return nil
}

// mergeManifest folds fresh entries into whatever manifest.json already
// exists at path, keyed by corpus item name — a fresh entry replaces a
// stale one for the same item, but items not touched by this invocation
// (e.g. re-running with -item to redo just one) are kept. This is what
// makes a run directory cheap to re-run one item against: without it, a
// second `-item X` call against the same -run-id would silently drop every
// other item's record even though their output files are still on disk
// (caught by this harness's own smoke test — see README).
func mergeManifest(path string, fresh []manifestEntry) ([]manifestEntry, error) {
	byName := map[string]manifestEntry{}

	if data, err := os.ReadFile(path); err == nil {
		var existing []manifestEntry
		if err := json.Unmarshal(data, &existing); err != nil {
			return nil, fmt.Errorf("parse existing manifest %s: %w", path, err)
		}
		for _, e := range existing {
			byName[e.Corpus] = e
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read existing manifest %s: %w", path, err)
	}

	for _, e := range fresh {
		byName[e.Corpus] = e
	}

	merged := make([]manifestEntry, 0, len(byName))
	for _, e := range byName {
		merged = append(merged, e)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Corpus < merged[j].Corpus })
	return merged, nil
}

func defaultCorpusDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "corpus"
	}
	return filepath.Join(dir, "corpus")
}

func defaultRunsDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "runs"
	}
	return filepath.Join(dir, "runs")
}

func loadCorpus(path string) ([]corpusItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read corpus file %s: %w", path, err)
	}
	var cf corpusFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse corpus file %s: %w", path, err)
	}
	sort.Slice(cf.Items, func(i, j int) bool { return cf.Items[i].Name < cf.Items[j].Name })
	return cf.Items, nil
}

func selectItems(items []corpusItem, name string) ([]corpusItem, error) {
	if name == "all" || name == "" {
		return items, nil
	}
	for _, it := range items {
		if it.Name == name {
			return []corpusItem{it}, nil
		}
	}
	return nil, fmt.Errorf("no corpus item named %q (use -list to see available items)", name)
}

func printCorpus(items []corpusItem) {
	for _, it := range items {
		fmt.Printf("%-12s %-26s lines %d-%d  (%s)\n  %s\n", it.Name, it.Shape, it.FromLine, it.ToLine, it.Session, it.Description)
	}
}

// sessionPath resolves a corpus item's session filename to the real
// transcript path on this machine: ~/.claude/projects/<mangled-repo-path>/.
// The mangled directory name is Claude Code's own scheme — every "/" in the
// absolute repo path becomes "-" — recomputed here from the current repo
// root rather than hardcoded, so the harness still works if this repo is
// ever cloned somewhere else (the corpus sessions themselves, of course,
// would not exist there — see README's privacy note).
func sessionPath(session string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	repoRoot, err := repoRootFromWD()
	if err != nil {
		return "", err
	}
	mangled := "-" + strings.ReplaceAll(strings.TrimPrefix(repoRoot, "/"), "/", "-")
	return filepath.Join(home, ".claude", "projects", mangled, session), nil
}

// repoRootFromWD walks up from the working directory looking for go.mod.
// This binary is always invoked from inside experiments/03-prompt-eval (see
// README), so this just needs to find the module root two levels up; it
// walks rather than hardcoding "../.." so it still works if invoked from a
// subdirectory.
func repoRootFromWD() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repo root (go.mod) above %s", dir)
		}
		dir = parent
	}
}

// loadEntries reads exactly [item.FromByte, item.ToByte) out of the real
// transcript file and hands it to internal/transcript.Read, so parsing goes
// through the exact same code the live worker uses. The slice is written to
// a temp file first: Read's contract is "a transcript file starting at
// offset from", and a byte range in the middle of a much longer file is not
// a valid transcript on its own — corpus.json's offsets are chosen to fall
// on line boundaries (see corpus/offsets.py), so the slice re-assembled
// here is itself a well-formed, if truncated, jsonl file.
func loadEntries(item corpusItem) ([]scribe.Entry, error) {
	path, err := sessionPath(item.Session)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Seek(item.FromByte, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek transcript %s: %w", path, err)
	}
	n := item.ToByte - item.FromByte
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, fmt.Errorf("read %d bytes from %s at %d: %w", n, path, item.FromByte, err)
	}

	tmp, err := os.CreateTemp("", "prompt-eval-slice-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("create temp slice file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write temp slice file: %w", err)
	}
	tmp.Close()

	entries, _, err := transcript.Read(tmp.Name(), 0)
	if err != nil {
		return nil, fmt.Errorf("parse transcript slice: %w", err)
	}
	return entries, nil
}

// buildEvalPrompt mirrors internal/worker.buildPrompt's shape (variant
// instructions, then current doc state, then new entries) so a variant
// written against this harness sees the same overall prompt structure the
// real worker builds — only promptInstructions itself varies between
// variant files. Current doc state is seeded empty for every corpus item:
// this harness evaluates one prompt variant's judgment on one transcript
// slice in isolation, not accumulated project state, which none of these
// ad hoc corpus slices have anyway (see README's "known simplification").
func buildEvalPrompt(instructions string, entries []scribe.Entry) string {
	var b strings.Builder
	b.WriteString(instructions)

	for _, d := range scribe.AllDocs {
		fmt.Fprintf(&b, "\n--- current %s ---\n%s\n", d, "(not yet created)")
	}

	b.WriteString("\n--- new transcript entries since the last run ---\n")
	for _, e := range entries {
		ts := e.Timestamp
		if ts.IsZero() {
			fmt.Fprintf(&b, "[%s]: %s\n", e.Role, e.Text)
		} else {
			fmt.Fprintf(&b, "[%s %s]: %s\n", ts.Format(time.RFC3339), e.Role, e.Text)
		}
	}

	return b.String()
}
