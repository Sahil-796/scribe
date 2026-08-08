package worker

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// ---- fakes ----

type fakeQueue struct {
	mu          sync.Mutex
	locked      bool
	pending     bool
	drainQueue  [][]scribe.Trigger // successive Drain() calls return these, in order
	drainCalls  int
	lockCalls   int
	unlockCalls int
}

func (q *fakeQueue) TryLock() (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.lockCalls++
	if q.locked {
		return false, nil
	}
	q.locked = true
	return true, nil
}

func (q *fakeQueue) Unlock() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.unlockCalls++
	q.locked = false
	return nil
}

func (q *fakeQueue) Drain() ([]scribe.Trigger, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.drainCalls >= len(q.drainQueue) {
		q.drainCalls++
		return nil, nil
	}
	out := q.drainQueue[q.drainCalls]
	q.drainCalls++
	return out, nil
}

func (q *fakeQueue) SetPending() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = true
	return nil
}

func (q *fakeQueue) TakePending() (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	was := q.pending
	q.pending = false
	return was, nil
}

type fakeDocStore struct {
	mu       sync.Mutex
	state    map[scribe.Doc]string
	history  map[scribe.Doc][]string
	writeErr error
}

func newFakeDocStore() *fakeDocStore {
	return &fakeDocStore{
		state:   map[scribe.Doc]string{},
		history: map[scribe.Doc][]string{},
	}
}

func (f *fakeDocStore) ReadAll() (map[scribe.Doc]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[scribe.Doc]string, len(scribe.AllDocs))
	for _, d := range scribe.AllDocs {
		if docsIsState(d) {
			out[d] = f.state[d]
		} else {
			out[d] = fmt.Sprintf("%d entries", len(f.history[d]))
		}
	}
	return out, nil
}

func (f *fakeDocStore) WriteState(doc scribe.Doc, content string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[doc] = content
	return nil
}

func (f *fakeDocStore) AppendHistory(doc scribe.Doc, entry string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history[doc] = append(f.history[doc], entry)
	return nil
}

func docsIsState(d scribe.Doc) bool {
	return d == scribe.DocProject || d == scribe.DocDecisions
}

type fakeWriter struct {
	mu      sync.Mutex
	outputs []string // successive Run() calls return these, in order
	errs    []error
	calls   int
	prompts []string
}

func (w *fakeWriter) Name() string { return "fake" }

func (w *fakeWriter) Run(prompt string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.calls
	w.calls++
	w.prompts = append(w.prompts, prompt)
	var out string
	var err error
	if i < len(w.outputs) {
		out = w.outputs[i]
	}
	if i < len(w.errs) {
		err = w.errs[i]
	}
	return out, err
}

// fakeTranscript backs ReadTranscript/LoadOffset/SaveOffset with an
// in-memory model: sessionID -> full entry list, and tracks saved offsets.
type fakeTranscript struct {
	mu        sync.Mutex
	entries   map[string][]scribe.Entry // sessionID -> all entries "in the transcript"
	offsets   map[string]int64          // sessionID -> saved offset (in "entry count" units for simplicity)
	saveErr   error
	readErr   error
	saveCalls []scribe.Offset
}

func newFakeTranscript() *fakeTranscript {
	return &fakeTranscript{
		entries: map[string][]scribe.Entry{},
		offsets: map[string]int64{},
	}
}

func (f *fakeTranscript) LoadOffset(repoRoot, sessionID string) (scribe.Offset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return scribe.Offset{SessionID: sessionID, Bytes: f.offsets[sessionID]}, nil
}

// Read treats "from" as an index into the session's entry slice (standing in
// for a byte offset — the fake doesn't need real bytes to test ordering).
func (f *fakeTranscript) Read(transcriptPath string, from int64) ([]scribe.Entry, int64, error) {
	if f.readErr != nil {
		return nil, 0, f.readErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.entries[transcriptPath]
	if from >= int64(len(all)) {
		return nil, from, nil
	}
	newEntries := all[from:]
	return newEntries, int64(len(all)), nil
}

func (f *fakeTranscript) SaveOffset(repoRoot string, o scribe.Offset) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offsets[o.SessionID] = o.Bytes
	f.saveCalls = append(f.saveCalls, o)
	return nil
}

// ---- tests ----

func TestRunProcessesTriggerAndAdvancesOffsetOnSuccess(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "hello"}}

	store := newFakeDocStore()
	w := &fakeWriter{outputs: []string{`{"CHANGELOG.md": "did a thing"}`}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(store.history[scribe.DocChangelog]) != 1 || store.history[scribe.DocChangelog][0] != "did a thing" {
		t.Fatalf("expected changelog entry, got %v", store.history[scribe.DocChangelog])
	}
	if got := tr.offsets["s1"]; got != 1 {
		t.Fatalf("expected offset advanced to 1, got %d", got)
	}
	if q.lockCalls != 1 || q.unlockCalls != 1 {
		t.Fatalf("expected exactly one lock/unlock cycle, got lock=%d unlock=%d", q.lockCalls, q.unlockCalls)
	}
}

// The core ordering guarantee: if the writer or the doc write fails, the
// offset must NOT advance, or a crash mid-run would silently lose that
// reply forever (no other source for those transcript bytes).
func TestOffsetDoesNotAdvanceWhenWriterFails(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "hello"}}

	store := newFakeDocStore()
	w := &fakeWriter{errs: []error{errors.New("writer boom")}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err == nil {
		t.Fatal("expected Run to return the writer error")
	}

	if got := tr.offsets["s1"]; got != 0 {
		t.Fatalf("offset must not advance on writer failure, got %d", got)
	}
	if len(tr.saveCalls) != 0 {
		t.Fatalf("SaveOffset must not be called on writer failure, got %d calls", len(tr.saveCalls))
	}
}

func TestOffsetDoesNotAdvanceWhenDocWriteFails(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "hello"}}

	store := newFakeDocStore()
	store.writeErr = errors.New("disk full")
	w := &fakeWriter{outputs: []string{`{"CHANGELOG.md": "did a thing"}`}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err == nil {
		t.Fatal("expected Run to return the doc-write error")
	}
	if got := tr.offsets["s1"]; got != 0 {
		t.Fatalf("offset must not advance on doc write failure, got %d", got)
	}
}

// Decision 3: triggers landing during a run set a pending flag; the run
// picks them up before releasing the lock rather than making the caller
// re-invoke.
func TestRunReRunsWhenPendingFlagIsSet(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}}, // first drain
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}}, // second drain, after re-run triggered
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{
		{Role: "user", Text: "first"},
	}

	store := newFakeDocStore()
	w := &fakeWriter{outputs: []string{
		`{"CHANGELOG.md": "covered first"}`,
		`{"CHANGELOG.md": "covered second"}`,
	}}

	// Simulate a trigger landing mid-run: runOnce always calls the writer
	// after draining, so on the writer's first call we both flip the
	// pending flag AND append a new transcript entry — that's what a Stop
	// hook firing mid-run actually looks like: a new reply appears, plus a
	// trigger gets queued for it. Without a new entry, the second runOnce
	// would correctly find nothing new and skip the writer, which would be
	// right but wouldn't be testing re-run coverage.
	wrapped := &pendingSettingWriter{inner: w, queue: q, tr: tr, transcriptPath: "/repo/t1"}

	deps := Deps{
		Queue: q, Docs: store, Writer: wrapped,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if q.drainCalls != 2 {
		t.Fatalf("expected 2 Drain() calls (initial + pending re-run), got %d", q.drainCalls)
	}
	if len(store.history[scribe.DocChangelog]) != 2 {
		t.Fatalf("expected both writer outputs applied across both runs, got %v", store.history[scribe.DocChangelog])
	}
	if q.lockCalls != 1 || q.unlockCalls != 1 {
		t.Fatalf("expected the lock held across both iterations (1 lock/unlock cycle), got lock=%d unlock=%d", q.lockCalls, q.unlockCalls)
	}
}

// pendingSettingWriter sets the queue's pending flag (and, to simulate a
// genuinely new reply, appends a transcript entry) the first time it's
// called, then delegates to inner.
type pendingSettingWriter struct {
	inner          *fakeWriter
	queue          *fakeQueue
	tr             *fakeTranscript
	transcriptPath string
	calls          int
}

func (p *pendingSettingWriter) Name() string { return "pending-setting" }

func (p *pendingSettingWriter) Run(prompt string) (string, error) {
	p.calls++
	if p.calls == 1 {
		_ = p.queue.SetPending()
		p.tr.mu.Lock()
		p.tr.entries[p.transcriptPath] = append(p.tr.entries[p.transcriptPath], scribe.Entry{Role: "user", Text: "second"})
		p.tr.mu.Unlock()
	}
	return p.inner.Run(prompt)
}

func TestRunSkipsAndMarksPendingWhenLockAlreadyHeld(t *testing.T) {
	q := &fakeQueue{locked: true} // simulate another run already in progress
	tr := newFakeTranscript()
	store := newFakeDocStore()
	w := &fakeWriter{}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !q.pending {
		t.Fatal("expected pending flag to be set when lock is already held")
	}
	if w.calls != 0 {
		t.Fatal("writer must not run when the lock could not be acquired")
	}
}

func TestRunWithEmptyQueueDoesNothing(t *testing.T) {
	q := &fakeQueue{} // Drain() returns nil, nil
	tr := newFakeTranscript()
	store := newFakeDocStore()
	w := &fakeWriter{}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w.calls != 0 {
		t.Fatal("writer must not run when there's nothing queued")
	}
	if q.unlockCalls != 1 {
		t.Fatal("lock must still be released even when there was nothing to do")
	}
}

func TestRunCoalescesMultipleTriggersForSameSessionIntoOneRead(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{
			{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"},
			{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}, // same session, coalesced
		},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "hello"}}
	store := newFakeDocStore()
	w := &fakeWriter{outputs: []string{`{"CHANGELOG.md": "one entry"}`}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w.calls != 1 {
		t.Fatalf("expected exactly one writer call for one coalesced batch, got %d", w.calls)
	}
	if len(tr.saveCalls) != 1 {
		t.Fatalf("expected exactly one offset save for the one session, got %d", len(tr.saveCalls))
	}
}

func TestBuildPromptIncludesDocsAndEntries(t *testing.T) {
	current := map[scribe.Doc]string{
		scribe.DocProject:   "proj content",
		scribe.DocDecisions: "dec content",
		scribe.DocChangelog: "log content",
		scribe.DocJournal:   "journal content",
	}
	entries := []scribe.Entry{
		{Role: "user", Text: "do the thing", Timestamp: time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)},
	}
	prompt := buildPrompt(current, entries)
	for _, want := range []string{"proj content", "dec content", "log content", "journal content", "do the thing"} {
		if !contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
