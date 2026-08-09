package docs

import (
	"fmt"
	"os"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// SizeStatus reports one doc's current size against its cap.
type SizeStatus struct {
	Doc   scribe.Doc
	Bytes int64
	Cap   int64
	// Near is true once Bytes is at least 90% of Cap — a caller's cue to
	// treat the doc as "worth checking before the next write", not a hard
	// threshold with any other effect inside this package.
	Near bool
}

// Size reports doc's current SizeStatus without reading its full content
// into memory — it stats the file on disk. A doc that doesn't exist yet is
// reported at the size it would be seeded at (len of its default header);
// asking does not create it, unlike Read.
//
// This is the read path phase 03 asks for: a caller — the worker, before
// building the writer's prompt for a doc — can check whether a doc is
// already near its cap and worth extra care, without paying for a full
// ReadAll first.
func (s *Store) Size(doc scribe.Doc) (SizeStatus, error) {
	path := s.Path(doc)
	info, err := os.Stat(path)
	var n int64
	switch {
	case err == nil:
		n = info.Size()
	case os.IsNotExist(err):
		n = int64(len(defaultHeader[doc]))
	default:
		return SizeStatus{}, fmt.Errorf("docs: stat %s: %w", path, err)
	}

	cap := s.capFor(doc)
	return SizeStatus{
		Doc:   doc,
		Bytes: n,
		Cap:   cap,
		Near:  cap > 0 && n*10 >= cap*9,
	}, nil
}
