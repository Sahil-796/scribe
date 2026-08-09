package docs

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestWriteStateRejectsContentOverCap(t *testing.T) {
	s := mustOpen(t)
	s.SetCaps(10, 0)

	err := s.WriteState(scribe.DocProject, strings.Repeat("x", 11))
	if err == nil {
		t.Fatal("expected error writing over-cap content to a state doc")
	}
	if !errors.Is(err, ErrStateDocTooLarge) {
		t.Fatalf("expected errors.Is ErrStateDocTooLarge, got %v", err)
	}

	// Nothing should have been written — the doc must not exist yet (it
	// was never successfully seeded/written).
	if _, statErr := os.Stat(s.Path(scribe.DocProject)); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file written on rejected WriteState, stat err = %v", statErr)
	}
}

func TestSetCapsZeroRestoresDefaults(t *testing.T) {
	s := mustOpen(t)
	s.SetCaps(1, 1)
	if got := s.capFor(scribe.DocProject); got != 1 {
		t.Fatalf("expected override cap 1, got %d", got)
	}
	s.SetCaps(0, 0)
	if got := s.capFor(scribe.DocProject); got != DefaultStateCap {
		t.Fatalf("expected default state cap restored, got %d", got)
	}
	if got := s.capFor(scribe.DocChangelog); got != DefaultHistoryCap {
		t.Fatalf("expected default history cap restored, got %d", got)
	}
}
