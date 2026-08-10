package replay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// ---- fakes ----

// fakeWriter returns queued responses in order, or an error if configured
// for a given call index. It never touches a network or a real agent.
type fakeWriter struct {
	responses []string // one per call, cycles/holds on the last if calls exceed len
	errs      map[int]error
	calls     int
	prompts   []string
}

func (w *fakeWriter) Name() string { return "fake" }

func (w *fakeWriter) Run(prompt string) (string, error) {
	i := w.calls
	w.calls++
	w.prompts = append(w.prompts, prompt)
	if err, ok := w.errs[i]; ok {
		return "", err
	}
	if i < len(w.responses) {
		return w.responses[i], nil
	}
	if len(w.responses) == 0 {
		return "{}", nil
	}
	return w.responses[len(w.responses)-1], nil
}

var _ scribe.Writer = (*fakeWriter)(nil)

type emitted struct {
	doc   scribe.Doc
	entry string
}

// ---- synthetic transcript fixtures ----

// jsonlLine builds one synthetic transcript line matching the real shape
// internal/transcript.Read expects (see internal/transcript/transcript.go's
// package doc). ts is RFC3339 or "" for no timestamp.
func jsonlLine(role, text, ts string) string {
	tsField := ""
	if ts != "" {
		tsField = fmt.Sprintf(`,"timestamp":%q`, ts)
	}
	return fmt.Sprintf(`{"type":%q,"message":{"role":%q,"content":%q}%s}`, role, role, text, tsField)
}

// writeFixture writes a synthetic .jsonl transcript with n alternating
// user/assistant turns, entirely synthetic content authored by this test —
// never a real transcript.
func writeFixture(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		ts := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		b.WriteString(jsonlLine(role, fmt.Sprintf("synthetic turn %d", i), ts))
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// ---- mangle / FindSessions ----

func TestMangle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/sahil/work/pa", "-Users-sahil-work-pa"},
		{"/Users/sahil/work/pa-experiments-00-writer-probe", "-Users-sahil-work-pa-experiments-00-writer-probe"},
		{"/a/b.c/d", "-a-b-c-d"},
	}
	for _, c := range cases {
		if got := mangle(c.in); got != c.want {
			t.Errorf("mangle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFindSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoRoot := filepath.Join(home, "work", "someproject")
	dir := filepath.Join(home, ".claude", "projects", mangle(repoRoot))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two real-looking session files with distinct mtimes, plus noise that
	// must be ignored: a subdirectory and a non-.jsonl file.
	older := filepath.Join(dir, "session-old.jsonl")
	newer := filepath.Join(dir, "session-new.jsonl")
	writeFixture(t, older, 2)
	writeFixture(t, newer, 2)

	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "some-subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-transcript.txt"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := FindSessions(repoRoot)
	if err != nil {
		t.Fatalf("FindSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(sessions), sessions)
	}
	if sessions[0].ID != "session-old" || sessions[1].ID != "session-new" {
		t.Errorf("wrong order: got IDs %q, %q, want oldest first", sessions[0].ID, sessions[1].ID)
	}
	if sessions[0].Path != older {
		t.Errorf("Path = %q, want %q", sessions[0].Path, older)
	}
	if sessions[0].Size == 0 {
		t.Errorf("Size not populated")
	}
}

func TestFindSessions_NoDirYet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessions, err := FindSessions(filepath.Join(home, "work", "brandnew"))
	if err != nil {
		t.Fatalf("FindSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("got %d sessions, want 0 for a repo with no transcript dir", len(sessions))
	}
}

// ---- chunkEntries ----

func TestChunkEntries(t *testing.T) {
	mk := func(n int) []scribe.Entry {
		es := make([]scribe.Entry, n)
		for i := range es {
			es[i] = scribe.Entry{Role: "user", Text: fmt.Sprintf("%d", i)}
		}
		return es
	}

	tests := []struct {
		name   string
		n, max int
		want   []int // length of each chunk
	}{
		{"empty", 0, 5, nil},
		{"exact multiple", 6, 3, []int{3, 3}},
		{"remainder", 7, 3, []int{3, 3, 1}},
		{"fewer than max", 2, 5, []int{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkEntries(mk(tt.n), tt.max)
			var got []int
			for _, c := range chunks {
				got = append(got, len(c))
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chunk lengths = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---- parseReplayEdits ----

func TestParseReplayEdits(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    edits
		wantErr bool
	}{
		{"empty is nothing to record", "", edits{}, false},
		{"whitespace only", "   \n  ", edits{}, false},
		{"empty object", "{}", edits{}, false},
		{
			"both docs",
			`{"CHANGELOG.md":"- did a thing","JOURNAL.md":"story of the thing"}`,
			edits{scribe.DocChangelog: "- did a thing", scribe.DocJournal: "story of the thing"},
			false,
		},
		{
			"code fence wrapped",
			"```json\n{\"CHANGELOG.md\":\"- did a thing\"}\n```",
			edits{scribe.DocChangelog: "- did a thing"},
			false,
		},
		{"disallowed doc key", `{"PROJECT.md":"nope"}`, nil, true},
		{"garbage", "not json at all", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseReplayEdits(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none (result %+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// ---- Run ----

func TestRun_EmitsAndChunks(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "sess.jsonl")
	writeFixture(t, sessPath, 5) // 5 entries, chunk size 2 -> 3 chunks

	// Two writer calls per chunk now (changelog prompt, then journal
	// prompt) — see runChunk in replay.go. Order per chunk is
	// [changelog response, journal response].
	w := &fakeWriter{
		responses: []string{
			`{"CHANGELOG.md":"chunk0 changelog"}`, // chunk0 changelog
			`{}`,                                  // chunk0 journal: nothing
			`{}`,                                  // chunk1 changelog: nothing
			`{"JOURNAL.md":"chunk1 journal"}`,     // chunk1 journal
			`{}`,                                  // chunk2 changelog: nothing
			`{}`,                                  // chunk2 journal: nothing
		},
	}

	var got []emitted
	var progressLabels []string
	var progressStatuses []ChunkStatus

	err := Run(Options{Redactor: testRedactor(),
		RepoRoot:           tmp,
		Writer:             w,
		Sessions:           []Session{{ID: "sess", Path: sessPath}},
		MaxEntriesPerChunk: 2,
		StatePath:          filepath.Join(tmp, ".scribe", "replay.json"),
		Progress: func(status ChunkStatus, done, total int, label string) {
			progressStatuses = append(progressStatuses, status)
			progressLabels = append(progressLabels, fmt.Sprintf("%d/%d %s", done, total, label))
		},
		Emit: func(doc scribe.Doc, entry string) error {
			got = append(got, emitted{doc, entry})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []emitted{
		{scribe.DocChangelog, "chunk0 changelog"},
		{scribe.DocJournal, "chunk1 journal"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("emitted = %+v, want %+v", got, want)
	}
	if w.calls != 6 {
		t.Errorf("writer called %d times, want 6 (changelog + journal calls, one pair per chunk)", w.calls)
	}
	if len(progressLabels) != 3 {
		t.Errorf("progress called %d times, want 3: %v", len(progressLabels), progressLabels)
	}
	// The status is the typed signal callers act on (OPEN-ITEMS item 18);
	// a fresh run where every chunk was written must report exactly that,
	// with no caller ever needing to read the label.
	for i, st := range progressStatuses {
		if st != ChunkWritten {
			t.Errorf("chunk %d status = %v, want ChunkWritten", i, st)
		}
	}

	// Each writer prompt must only ever see its own chunk's entries, never
	// the whole session — that's the "bound what one call sees" guarantee.
	for _, p := range w.prompts {
		n := strings.Count(p, "synthetic turn")
		if n > 2 {
			t.Errorf("a single prompt saw %d entries, want at most 2 (chunk size): %s", n, p)
		}
	}
}

func TestRun_NilProgressAndEmit(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "sess.jsonl")
	writeFixture(t, sessPath, 2)

	w := &fakeWriter{responses: []string{`{"CHANGELOG.md":"x"}`}}

	err := Run(Options{Redactor: testRedactor(),
		RepoRoot: tmp,
		Writer:   w,
		Sessions: []Session{{ID: "sess", Path: sessPath}},
		// Progress and Emit both nil.
	})
	if err != nil {
		t.Fatalf("Run with nil Progress/Emit: %v", err)
	}
}

func TestRun_ResumesAfterFailure(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "sess.jsonl")
	writeFixture(t, sessPath, 4) // chunk size 2 -> 2 chunks

	statePath := filepath.Join(tmp, ".scribe", "replay.json")

	// First run: chunk 0 succeeds (both its writer calls), chunk 1's first
	// writer call (its changelog prompt, call index 2 — two calls per
	// chunk now) fails.
	w1 := &fakeWriter{
		responses: []string{`{"CHANGELOG.md":"chunk0"}`, `{}`},
		errs:      map[int]error{2: errors.New("writer boom")},
	}
	var got1 []emitted
	err := Run(Options{Redactor: testRedactor(),
		RepoRoot:           tmp,
		Writer:             w1,
		Sessions:           []Session{{ID: "sess", Path: sessPath}},
		MaxEntriesPerChunk: 2,
		StatePath:          statePath,
		Emit: func(doc scribe.Doc, entry string) error {
			got1 = append(got1, emitted{doc, entry})
			return nil
		},
	})
	if err == nil {
		t.Fatalf("expected Run to report the failed chunk, got nil error")
	}
	if !strings.Contains(err.Error(), "sess#1") {
		t.Errorf("error %q does not name the failed chunk", err.Error())
	}
	if len(got1) != 1 || got1[0].entry != "chunk0" {
		t.Errorf("first run emitted %+v, want just chunk0's entry", got1)
	}

	// Second run, same state path: chunk 0 must be skipped (no writer call,
	// no re-emit), chunk 1 must be retried and now succeed (both its calls:
	// changelog then journal).
	w2 := &fakeWriter{responses: []string{`{}`, `{"JOURNAL.md":"chunk1"}`}}
	var got2 []emitted
	err = Run(Options{Redactor: testRedactor(),
		RepoRoot:           tmp,
		Writer:             w2,
		Sessions:           []Session{{ID: "sess", Path: sessPath}},
		MaxEntriesPerChunk: 2,
		StatePath:          statePath,
		Emit: func(doc scribe.Doc, entry string) error {
			got2 = append(got2, emitted{doc, entry})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if w2.calls != 2 {
		t.Errorf("second run called writer %d times, want 2 (changelog + journal calls for the one retried chunk)", w2.calls)
	}
	if len(got2) != 1 || got2[0].entry != "chunk1" {
		t.Errorf("second run emitted %+v, want just chunk1's entry (chunk0 must not be re-emitted)", got2)
	}
}

func TestRun_CorruptStateDegradesToStartOver(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "sess.jsonl")
	writeFixture(t, sessPath, 2)

	statePath := filepath.Join(tmp, ".scribe", "replay.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &fakeWriter{responses: []string{`{"CHANGELOG.md":"x"}`}}
	err := Run(Options{Redactor: testRedactor(),
		RepoRoot:  tmp,
		Writer:    w,
		Sessions:  []Session{{ID: "sess", Path: sessPath}},
		StatePath: statePath,
	})
	if err != nil {
		t.Fatalf("Run with corrupt state should degrade to start-over, not error: %v", err)
	}
	if w.calls != 2 {
		t.Errorf("writer called %d times, want 2 (changelog + journal calls) — corrupt state should not crash or skip work", w.calls)
	}
}

func TestRun_ParseFailureRecordedNotSilentSuccess(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "sess.jsonl")
	writeFixture(t, sessPath, 2)

	w := &fakeWriter{responses: []string{"this is not json"}}
	var emitCalls int
	err := Run(Options{Redactor: testRedactor(),
		RepoRoot: tmp,
		Writer:   w,
		Sessions: []Session{{ID: "sess", Path: sessPath}},
		Emit: func(doc scribe.Doc, entry string) error {
			emitCalls++
			return nil
		},
	})
	if err == nil {
		t.Fatalf("expected an error for an unparseable writer response, got nil")
	}
	if emitCalls != 0 {
		t.Errorf("Emit called %d times for a chunk whose writer output never parsed", emitCalls)
	}
}

func TestRun_EmitFailureNotMarkedComplete(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "sess.jsonl")
	writeFixture(t, sessPath, 2)
	statePath := filepath.Join(tmp, ".scribe", "replay.json")

	w := &fakeWriter{responses: []string{`{"CHANGELOG.md":"x"}`}}
	err := Run(Options{Redactor: testRedactor(),
		RepoRoot:  tmp,
		Writer:    w,
		Sessions:  []Session{{ID: "sess", Path: sessPath}},
		StatePath: statePath,
		Emit: func(doc scribe.Doc, entry string) error {
			return errors.New("disk full")
		},
	})
	if err == nil {
		t.Fatalf("expected Run to report the Emit failure")
	}

	st := loadState(statePath)
	if len(st.Completed) != 0 {
		t.Errorf("chunk marked complete despite Emit failing: %+v", st.Completed)
	}
}

func TestRun_DefaultStatePath(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "sess.jsonl")
	writeFixture(t, sessPath, 2)

	w := &fakeWriter{responses: []string{`{}`}}
	err := Run(Options{Redactor: testRedactor(),
		RepoRoot: tmp,
		Writer:   w,
		Sessions: []Session{{ID: "sess", Path: sessPath}},
		// StatePath left empty -> should default under <repoRoot>/.scribe/
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := filepath.Join(tmp, scribe.StateDir, "replay.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected default state file at %s: %v", want, err)
	}
}

func TestRun_RequiresWriterAndRepoRoot(t *testing.T) {
	if err := Run(Options{Redactor: testRedactor(), RepoRoot: "x"}); err == nil {
		t.Error("expected error for missing Writer")
	}
	if err := Run(Options{Redactor: testRedactor(), Writer: &fakeWriter{}}); err == nil {
		t.Error("expected error for missing RepoRoot")
	}
}

// Sanity check that sessions passed in via Options.Sessions are used
// as-is, in the given order, without a FindSessions call touching the real
// filesystem (guards against accidentally reading the user's real history
// in a test that didn't ask for it).
func TestRun_UsesGivenSessionsOrder(t *testing.T) {
	tmp := t.TempDir()
	var paths []string
	for i := 0; i < 2; i++ {
		p := filepath.Join(tmp, fmt.Sprintf("s%d.jsonl", i))
		writeFixture(t, p, 1)
		paths = append(paths, p)
	}

	// Two calls per chunk now: [changelog, journal]. Put the distinguishing
	// content on each session's journal call so order still reflects
	// session order, not call order within a chunk.
	w := &fakeWriter{responses: []string{
		`{}`, `{"JOURNAL.md":"first"}`, // session s0's chunk
		`{}`, `{"JOURNAL.md":"second"}`, // session s1's chunk
	}}
	var order []string
	err := Run(Options{Redactor: testRedactor(),
		RepoRoot: tmp,
		Writer:   w,
		Sessions: []Session{
			{ID: "s0", Path: paths[0]},
			{ID: "s1", Path: paths[1]},
		},
		Emit: func(doc scribe.Doc, entry string) error {
			order = append(order, entry)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"first", "second"}) {
		t.Errorf("order = %v, want [first second]", order)
	}
}
