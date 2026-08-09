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

// "hello" contains none of gateKeywords, so the gate's keyword prefilter
// skips the classification call and PROJECT/DECISIONS entirely — a run
// like this costs exactly two writer calls: CHANGELOG, then JOURNAL.
func TestRunProcessesTriggerAndAdvancesOffsetOnSuccess(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "hello"}}

	store := newFakeDocStore()
	w := &fakeWriter{outputs: []string{"did a thing", noChangeSentinel}}

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
	if w.calls != 2 {
		t.Fatalf("expected exactly 2 writer calls (CHANGELOG, JOURNAL — no gate keyword, so PROJECT/DECISIONS skipped), got %d", w.calls)
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
	w := &fakeWriter{outputs: []string{"did a thing", noChangeSentinel}}

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
	// Each runOnce makes two calls with no gate keyword present (CHANGELOG,
	// then JOURNAL), so two runOnce iterations cost four calls total.
	w := &fakeWriter{outputs: []string{
		"covered first", noChangeSentinel,
		"covered second", noChangeSentinel,
	}}

	// Simulate a trigger landing mid-run: runOnce always calls the writer
	// after draining, so on the writer's very first call we both flip the
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
	w := &fakeWriter{outputs: []string{"one entry", noChangeSentinel}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Coalescing means one read of the new bytes, not one writer call — this
	// run still makes one call per doc in play (CHANGELOG, JOURNAL; no gate
	// keyword here). What coalescing buys is a single offset save below,
	// not a single writer call.
	if w.calls != 2 {
		t.Fatalf("expected exactly 2 writer calls (CHANGELOG, JOURNAL) for one coalesced batch, got %d", w.calls)
	}
	if len(tr.saveCalls) != 1 {
		t.Fatalf("expected exactly one offset save for the one session, got %d", len(tr.saveCalls))
	}
}

// TestRunSucceedsWhenEveryDocDeclines covers the case phase 03 makes
// common: every per-doc call legitimately answers noChangeSentinel. This
// must NOT be treated as a failure — unlike phase 01's single combined
// prompt, where "the writer changed nothing" was itself suspicious,
// per-doc calls are allowed to decline, and a quiet run declining
// everything is the expected outcome, not evidence of a broken writer. The
// offset still advances so this transcript slice isn't reprocessed next
// run.
func TestRunSucceedsWhenEveryDocDeclines(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "hello"}}

	store := newFakeDocStore()
	w := &fakeWriter{outputs: []string{noChangeSentinel, noChangeSentinel}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: expected success when every doc declines, got %v", err)
	}
	if got := tr.offsets["s1"]; got != 1 {
		t.Fatalf("expected offset to still advance on a legitimately quiet run, got %d", got)
	}
	for _, d := range scribe.AllDocs {
		if len(store.history[d]) != 0 {
			t.Fatalf("expected no history entries for %s, got %v", d, store.history[d])
		}
	}
}

// TestRunFailsWhenWriterEchoesUnchangedContent pins down open item 8
// (docs/findings/OPEN-ITEMS.md, docs/findings/00-writer.md): `opencode run`
// without `--auto` auto-rejects every edit the model attempts and still
// exits 0 with normal-looking output. A state-doc call that claims a
// change (returns something other than noChangeSentinel) but whose content
// is byte-identical to what's already on disk is exactly that failure mode
// wearing a disguise, and must be treated as an error, not silently
// accepted.
//
// This needs a gated-in PROJECT/DECISIONS call to exercise, since
// CHANGELOG/JOURNAL are history docs — AppendHistory always grows the
// file, so an "identical" entry still changes the doc on disk. Only a
// state doc's WriteState can produce a byte-for-byte no-op, so the entry
// text below deliberately trips the gate keyword prefilter.
func TestRunFailsWhenWriterEchoesUnchangedContent(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "we decided to go with plan B"}}

	store := newFakeDocStore()
	store.state[scribe.DocProject] = "existing content"
	w := &fakeWriter{outputs: []string{
		noChangeSentinel,   // CHANGELOG
		noChangeSentinel,   // JOURNAL
		"YES",              // gate classification ("decided" tripped the prefilter)
		"existing content", // PROJECT — echoed back unchanged
		noChangeSentinel,   // DECISIONS
	}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err == nil {
		t.Fatal("expected Run to fail when a state doc call echoes unchanged content")
	}
	if got := tr.offsets["s1"]; got != 0 {
		t.Fatalf("offset must not advance when nothing actually changed, got %d", got)
	}
	if len(tr.saveCalls) != 0 {
		t.Fatalf("SaveOffset must not be called when nothing actually changed, got %d calls", len(tr.saveCalls))
	}
}

// TestRunFailsWhenDocWriterReturnsEmptyOutput checks the other half of the
// noChangeSentinel contract: a blank response isn't a considered "nothing
// to say", it's parseDocOutput refusing to guess. Every per-doc prompt
// always asks for either real content or the literal sentinel, so nothing
// at all means something went wrong upstream, and that must surface as an
// error rather than being swallowed as equivalent to NO_CHANGE.
func TestRunFailsWhenDocWriterReturnsEmptyOutput(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	tr.entries["/repo/t1"] = []scribe.Entry{{Role: "user", Text: "hello"}}

	store := newFakeDocStore()
	w := &fakeWriter{outputs: []string{""}} // CHANGELOG call returns nothing at all

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err == nil {
		t.Fatal("expected Run to fail on an empty (non-sentinel) writer response")
	}
	if got := tr.offsets["s1"]; got != 0 {
		t.Fatalf("offset must not advance on a parse failure, got %d", got)
	}
}

// TestRunNoNewTranscriptEntriesIsNotAFailure is the counterpart to the
// no-change tests above: a trigger fires but the transcript has no new
// bytes since the last offset. "Nothing changed" here is legitimate (there
// was nothing to say), not the writer swallowing an edit, so the writer
// must not even be called and Run must not error.
func TestRunNoNewTranscriptEntriesIsNotAFailure(t *testing.T) {
	q := &fakeQueue{drainQueue: [][]scribe.Trigger{
		{{SessionID: "s1", TranscriptPath: "/repo/t1", RepoRoot: "/repo"}},
	}}
	tr := newFakeTranscript()
	// No entries seeded for "/repo/t1", so Read returns (nil, offset, nil):
	// the transcript hasn't grown since the last run.

	store := newFakeDocStore()
	w := &fakeWriter{outputs: []string{"should not be called"}}

	deps := Deps{
		Queue: q, Docs: store, Writer: w,
		ReadTranscript: tr.Read, LoadOffset: tr.LoadOffset, SaveOffset: tr.SaveOffset,
	}

	if err := Run(deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w.calls != 0 {
		t.Fatal("writer must not run when there are no new transcript entries")
	}
	if len(tr.saveCalls) != 0 {
		t.Fatalf("no offset save expected when there was nothing new, got %d calls", len(tr.saveCalls))
	}
}

func TestBuildDocPromptIncludesOwnContentAndEntries(t *testing.T) {
	entries := []scribe.Entry{
		{Role: "user", Text: "do the thing", Timestamp: time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)},
	}
	prompt := buildDocPrompt(scribe.DocChangelog, "log content", entries, CodeWeightCheck)
	for _, want := range []string{"log content", "do the thing", noChangeSentinel} {
		if !contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// TestBuildDocPromptDoesNotLeakOtherDocsGuidance is phase 03 item 1's core
// promise: a CHANGELOG job should not be handed DECISIONS guidance, or vice
// versa. Each per-doc prompt is built from only that doc's own current
// content and its own docPrompts entry, so the other three docs' guidance
// text (which is doc-specific enough to be a reliable fingerprint) must
// never show up.
func TestBuildDocPromptDoesNotLeakOtherDocsGuidance(t *testing.T) {
	entries := []scribe.Entry{{Role: "user", Text: "fixed the bug"}}
	prompt := buildDocPrompt(scribe.DocChangelog, "log content", entries, CodeWeightCheck)

	leaks := []string{
		"one block per decision",   // DECISIONS guidance
		"who it's for, where it",   // PROJECT guidance
		"AI got confidently wrong", // JOURNAL guidance
	}
	for _, l := range leaks {
		if contains(prompt, l) {
			t.Fatalf("CHANGELOG prompt leaked another doc's guidance (%q):\n%s", l, prompt)
		}
	}
}

// TestBuildDocPromptCorrectionPathGuidance checks phase 03 item 3 actually
// landed in the prompt text: PROJECT.md's guidance must tell the writer to
// pull stale claims, and DECISIONS.md's guidance must tell it to record why
// a decision was dropped, rather than silently deleting the block. The
// parse/apply path needs no extra support for this beyond what already
// exists — PROJECT.md and DECISIONS.md are full-replace state docs
// (docs.WriteState), so a corrected PROJECT.md that simply omits the
// scrapped claim, or a DECISIONS.md with a new "dropped: ..." block, is
// just an ordinary WriteState call, not a special case.
func TestBuildDocPromptCorrectionPathGuidance(t *testing.T) {
	entries := []scribe.Entry{{Role: "user", Text: "scrapping the plan"}}

	projectPrompt := buildDocPrompt(scribe.DocProject, "current", entries, CodeWeightCheck)
	if !contains(projectPrompt, "normal case") || !contains(projectPrompt, "remove that") {
		t.Fatalf("PROJECT prompt missing correction-path guidance:\n%s", projectPrompt)
	}

	decisionsPrompt := buildDocPrompt(scribe.DocDecisions, "current", entries, CodeWeightCheck)
	if !contains(decisionsPrompt, "dropped") || !contains(decisionsPrompt, "why") {
		t.Fatalf("DECISIONS prompt missing correction-path guidance:\n%s", decisionsPrompt)
	}
}

// TestBuildDocPromptJournalGuidanceTeachesWhatsWorthCapturing pins down
// phase 03 item 4: JOURNAL.md's guidance must distinguish what it wants
// (problems hit, AI mistakes, dead ends, the fix) from what it doesn't
// (a tool-call narration, a changelog restatement), with concrete examples
// of each — not just a one-line description a model can satisfy with
// filler.
func TestBuildDocPromptJournalGuidanceTeachesWhatsWorthCapturing(t *testing.T) {
	entries := []scribe.Entry{{Role: "user", Text: "fixed the bug"}}
	prompt := buildDocPrompt(scribe.DocJournal, "current", entries, CodeWeightCheck)

	for _, want := range []string{
		"confidently wrong", // what it wants
		"dead ends",
		"Good entry", // a concrete example of each
		"Bad entry",
		"quiet session", // permission to write nothing when there's nothing
	} {
		if !contains(prompt, want) {
			t.Fatalf("JOURNAL prompt missing %q:\n%s", want, prompt)
		}
	}
}

// TestBuildDocPromptCodeWeightChangesText checks phase 03 item 5's knob
// actually changes what the writer is told, per weight value, and that
// Deps.codeWeight() defaults an unset field to the safe CodeWeightCheck.
func TestBuildDocPromptCodeWeightChangesText(t *testing.T) {
	entries := []scribe.Entry{{Role: "user", Text: "shipped it"}}

	check := buildDocPrompt(scribe.DocChangelog, "x", entries, CodeWeightCheck)
	if !contains(check, "verify") {
		t.Fatalf("CodeWeightCheck prompt should say 'verify':\n%s", check)
	}
	if contains(check, "may use it to source") {
		t.Fatalf("CodeWeightCheck prompt should not offer to source content from code:\n%s", check)
	}

	full := buildDocPrompt(scribe.DocChangelog, "x", entries, CodeWeightFull)
	if !contains(full, "may use it to source") {
		t.Fatalf("CodeWeightFull prompt should say it may source content from code:\n%s", full)
	}

	off := buildDocPrompt(scribe.DocChangelog, "x", entries, CodeWeightOff)
	if !contains(off, "do not have code access") {
		t.Fatalf("CodeWeightOff prompt should say code access is off:\n%s", off)
	}

	var d Deps
	if got := d.codeWeight(); got != CodeWeightCheck {
		t.Fatalf("expected default CodeWeight to be %q, got %q", CodeWeightCheck, got)
	}
	d.CodeWeight = CodeWeightFull
	if got := d.codeWeight(); got != CodeWeightFull {
		t.Fatalf("expected an explicitly set CodeWeight to be honored, got %q", got)
	}
}

func TestKeywordPrefilter(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"we decided to go with postgres", true},
		{"dropped the redis idea", true},
		{"instead of a queue we'll use a channel", true},
		{"fixed the off-by-one in the offset loader", false},
		{"ran go test, all green", false},
	}
	for _, tt := range tests {
		got := keywordPrefilter([]scribe.Entry{{Role: "user", Text: tt.text}})
		if got != tt.want {
			t.Errorf("keywordPrefilter(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestParseGateOutput(t *testing.T) {
	tests := []struct {
		out  string
		want bool
	}{
		{"YES", true},
		{"yes", true},
		{"  Yes.  ", true},
		{"NO", false},
		{"no", false},
		{"unsure", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := parseGateOutput(tt.out); got != tt.want {
			t.Errorf("parseGateOutput(%q) = %v, want %v", tt.out, got, tt.want)
		}
	}
}

// TestGateProductLevelSkipsClassificationWithoutKeywordHit checks that the
// keyword prefilter actually saves a writer call, not just that it returns
// the right bool — a plain "no keyword -> false" test could pass even if
// gateProductLevel called the writer anyway and ignored the answer.
func TestGateProductLevelSkipsClassificationWithoutKeywordHit(t *testing.T) {
	w := &fakeWriter{}
	deps := Deps{Writer: w}
	entries := []scribe.Entry{{Role: "user", Text: "fixed a typo"}}

	gateIn, err := gateProductLevel(deps, entries)
	if err != nil {
		t.Fatalf("gateProductLevel: %v", err)
	}
	if gateIn {
		t.Fatal("expected gateIn=false with no keyword hit")
	}
	if w.calls != 0 {
		t.Fatalf("expected no classification call without a keyword hit, got %d calls", w.calls)
	}
}

// TestGateProductLevelAsksClassifierOnKeywordHit is the other half: a
// keyword hit alone isn't the verdict, it's permission to ask the cheap
// classifier, whose answer is what actually decides.
func TestGateProductLevelAsksClassifierOnKeywordHit(t *testing.T) {
	w := &fakeWriter{outputs: []string{"NO"}}
	deps := Deps{Writer: w}
	entries := []scribe.Entry{{Role: "user", Text: "we decided to drop the queue idea"}}

	gateIn, err := gateProductLevel(deps, entries)
	if err != nil {
		t.Fatalf("gateProductLevel: %v", err)
	}
	if gateIn {
		t.Fatal("expected gateIn=false when the classifier answers NO, even with a keyword hit")
	}
	if w.calls != 1 {
		t.Fatalf("expected exactly one classification call on a keyword hit, got %d", w.calls)
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
