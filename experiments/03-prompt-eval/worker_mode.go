package main

// Worker mode: run a corpus item through the REAL worker rather than
// through this harness's own single-prompt reconstruction.
//
// Why this exists. The harness was built when a run was one writer call
// with one prompt, so `-variant` points at a .md file holding that prompt
// and buildEvalPrompt reassembles it. Phase 03 replaced that with four
// focused per-doc prompts plus a gate, and none of that shape lives in a
// variant file — it lives in internal/worker. Diffing variants therefore
// cannot answer "is phase 03's writing better", which is the entire point
// of OPEN-ITEMS item 28: a variant file is a museum piece of the old design.
//
// So this mode skips the reconstruction and drives worker.Run itself, with
// a real docs.Store on a temp repo, a real queue, and a real writer. What
// it measures is what actually ships — the gate's decisions, the per-doc
// calls, the correction path, and the fail-open guard, all in the real
// order. Nothing here simulates the worker; if this disagrees with
// production, production is what's wrong.
//
// It also gives item 8 the evidence it has been missing, because a run
// whose writer auto-rejects everything is exactly what `-no-auto` produces.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahil-796/scribe/internal/docs"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/transcript"
	"github.com/Sahil-796/scribe/internal/worker"
)

// workerRunResult is what one corpus item produced when run through the
// real worker.
type workerRunResult struct {
	Item        string            `json:"item"`
	Shape       string            `json:"shape"`
	Entries     int               `json:"entries"`
	DurationMS  int64             `json:"duration_ms"`
	WriterCalls int               `json:"writer_calls"`
	Docs        map[string]string `json:"docs"`
	DocBytes    map[string]int    `json:"doc_bytes"`
	Log         string            `json:"log,omitempty"`
	Err         string            `json:"error,omitempty"`
}

// countingWriter wraps the real writer so the harness can report how many
// model calls a run actually cost. The gate is supposed to keep a pure
// engineering run at two calls instead of four, and that claim has never
// been checked against a live run — only against fakes.
type countingWriter struct {
	inner scribe.Writer
	calls int
}

func (c *countingWriter) Name() string { return c.inner.Name() }

func (c *countingWriter) Run(prompt string) (string, error) {
	c.calls++
	return c.inner.Run(prompt)
}

// runItemThroughWorker builds a throwaway repo, plants the corpus slice as
// that repo's transcript, and runs the real worker over it once.
//
// The repo is a temp dir with a .git marker rather than a real clone: the
// worker only needs a repo root to hang .scribe/ and docs/scribe/ off, and
// a real clone would add nothing but IO.
func runItemThroughWorker(item corpusItem, w scribe.Writer, weight worker.CodeWeight) workerRunResult {
	res := workerRunResult{Item: item.Name, Shape: item.Shape, Docs: map[string]string{}, DocBytes: map[string]int{}}

	entries, err := loadEntries(item)
	if err != nil {
		res.Err = fmt.Sprintf("load entries: %v", err)
		return res
	}
	res.Entries = len(entries)

	repo, err := os.MkdirTemp("", "scribe-eval-"+item.Name+"-")
	if err != nil {
		res.Err = fmt.Sprintf("temp repo: %v", err)
		return res
	}
	defer os.RemoveAll(repo)
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		res.Err = fmt.Sprintf("fake .git: %v", err)
		return res
	}

	// Plant the corpus slice as this repo's transcript, byte for byte, so
	// internal/transcript parses exactly what a live Stop hook would hand it.
	raw, err := rawSlice(item)
	if err != nil {
		res.Err = fmt.Sprintf("read slice: %v", err)
		return res
	}
	transcriptPath := filepath.Join(repo, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, raw, 0o644); err != nil {
		res.Err = fmt.Sprintf("write transcript: %v", err)
		return res
	}

	q, err := queue.Open(repo)
	if err != nil {
		res.Err = fmt.Sprintf("open queue: %v", err)
		return res
	}
	if err := queue.Enqueue(repo, scribe.Trigger{
		SessionID:      item.Name,
		TranscriptPath: transcriptPath,
		RepoRoot:       repo,
	}); err != nil {
		res.Err = fmt.Sprintf("enqueue: %v", err)
		return res
	}

	store, err := docs.Open(repo)
	if err != nil {
		res.Err = fmt.Sprintf("open docs: %v", err)
		return res
	}

	counter := &countingWriter{inner: w}
	var log logBuffer
	deps := worker.Deps{
		Queue:          q,
		Docs:           store,
		Writer:         counter,
		ReadTranscript: transcript.Read,
		LoadOffset:     transcript.LoadOffset,
		SaveOffset:     transcript.SaveOffset,
		CodeWeight:     weight,
		Log:            &log,
	}

	start := time.Now()
	runErr := worker.Run(deps)
	res.DurationMS = time.Since(start).Milliseconds()
	res.WriterCalls = counter.calls
	res.Log = log.String()
	if runErr != nil {
		res.Err = runErr.Error()
	}

	// Capture whatever the run actually produced, including the empty
	// docs — "the gate correctly declined to touch PROJECT.md" is a result,
	// not an absence.
	for _, d := range scribe.AllDocs {
		b, readErr := os.ReadFile(filepath.Join(repo, scribe.DocsDir, string(d)))
		if readErr != nil {
			continue
		}
		res.Docs[string(d)] = string(b)
		res.DocBytes[string(d)] = len(b)
	}
	return res
}

// logBuffer is a tiny io.Writer so the run's operational log (partial
// failures, the item 29 correction warning) is captured as evidence rather
// than discarded.
type logBuffer struct{ b []byte }

func (l *logBuffer) Write(p []byte) (int, error) { l.b = append(l.b, p...); return len(p), nil }
func (l *logBuffer) String() string              { return string(l.b) }

// rawSlice returns the exact [FromByte, ToByte) bytes of the item's source
// session, which is what loadEntries parses — kept separate so worker mode
// can write those same bytes to disk for the real transcript reader.
func rawSlice(item corpusItem) ([]byte, error) {
	path, err := sessionPath(item.Session)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(item.FromByte, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, item.ToByte-item.FromByte))
}

// writeWorkerResults saves the per-item docs and a summary manifest.
func writeWorkerResults(runDir string, results []workerRunResult) error {
	for _, r := range results {
		for name, content := range r.Docs {
			out := filepath.Join(runDir, r.Item+"."+name)
			if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
				return err
			}
		}
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "worker-manifest.json"), b, 0o644)
}
