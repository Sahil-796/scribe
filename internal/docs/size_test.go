package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

func TestSizeReportsBytesAndNearThreshold(t *testing.T) {
	s := mustOpen(t)
	s.SetCaps(0, 100)

	// Doc doesn't exist yet: reported at seed size, no side effect.
	status, err := s.Size(scribe.DocChangelog)
	if err != nil {
		t.Fatalf("Size (missing doc): %v", err)
	}
	if status.Cap != 100 {
		t.Fatalf("expected cap 100, got %d", status.Cap)
	}
	if _, statErr := os.Stat(s.Path(scribe.DocChangelog)); !os.IsNotExist(statErr) {
		t.Fatalf("Size must not create the file as a side effect")
	}

	if err := s.AppendHistory(scribe.DocChangelog, strings.Repeat("y", 91)); err != nil {
		t.Fatalf("append: %v", err)
	}
	status, err = s.Size(scribe.DocChangelog)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if !status.Near {
		t.Fatalf("expected Near=true once bytes are >=90%% of cap, got %+v", status)
	}
}
