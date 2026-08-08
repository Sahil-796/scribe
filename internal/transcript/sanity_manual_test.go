package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestSanityRealTranscript is a manual, non-committed sanity check against
// a real transcript. Skipped unless SCRIBE_SANITY_DIR is set, so it never
// runs in CI or on another dev's machine.
func TestSanityRealTranscript(t *testing.T) {
	dir := os.Getenv("SCRIBE_SANITY_DIR")
	if dir == "" {
		t.Skip("set SCRIBE_SANITY_DIR to a directory of real .jsonl transcripts to run")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, path := range matches {
		entries, offset, err := Read(path, 0)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		info, _ := os.Stat(path)
		chars := 0
		for _, e := range entries {
			chars += len(e.Text)
		}
		ratio := 0.0
		if chars > 0 {
			ratio = float64(info.Size()) / float64(chars)
		}
		fmt.Printf("%s: raw=%d entries=%d offset=%d text_chars=%d ratio=%.1fx\n",
			filepath.Base(path), info.Size(), len(entries), offset, chars, ratio)
	}
}
