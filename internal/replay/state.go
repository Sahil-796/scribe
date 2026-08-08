package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// replayState is the resume checkpoint persisted at Options.StatePath.
// Completed maps a chunk key (see chunkKey) to true once that chunk has
// gone through the writer, parsed cleanly, and had every one of its
// produced entries emitted successfully. Anything not in this map — never
// attempted, or attempted and failed — is retried on the next Run call.
type replayState struct {
	Completed map[string]bool `json:"completed"`
}

// loadState reads the resume state at path. A missing file is the normal
// first-run case: an empty, ready-to-use state, not an error. A present but
// corrupt file (truncated, hand-edited, written by something else) also
// degrades to an empty state rather than erroring — per the phase 02 spec,
// a corrupt resume file must never crash the replay pass. The cost of that
// choice is re-doing already-completed chunks, which is safe (idempotent
// re-derivation), unlike the offset-corruption case in
// internal/transcript, where resetting would risk duplicate doc content.
// Replay's Emit calls are not deduplicated by this package, so a caller
// that cares about avoiding duplicate history entries across a
// state-loss-triggered redo should make Emit idempotent or inspect the
// existing docs; that tradeoff is documented here rather than hidden.
func loadState(path string) replayState {
	data, err := os.ReadFile(path)
	if err != nil {
		return replayState{Completed: map[string]bool{}}
	}

	var st replayState
	if err := json.Unmarshal(data, &st); err != nil {
		return replayState{Completed: map[string]bool{}}
	}
	if st.Completed == nil {
		st.Completed = map[string]bool{}
	}
	return st
}

// saveState writes st to path atomically (temp file + rename), matching
// the write discipline used throughout scribe (see internal/docs,
// internal/transcript's SaveOffset) so a crash mid-write never leaves a
// torn or half-written resume file that a future loadState would choke on.
func saveState(path string, st replayState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
