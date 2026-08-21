package layout

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// SessionMeta is the identifying header for one session's history file: when it
// ran, which session it was, and a one-line summary of what it was about. It is
// the input both to the file's name (SessionFilePath) and to its frontmatter
// (RenderSessionFile), so the two always agree about the session they describe.
type SessionMeta struct {
	Date      time.Time // the session's date; only the calendar day is used in names
	SessionID string    // Claude Code session id; its sanitized prefix makes the name unique
	Summary   string    // one-line headline, slugged into the name and shown in frontmatter
}

// subdirForDoc maps a history doc to the directory its per-session files live
// in. journal/ and changelog/ sit beside the rendered JOURNAL.md / CHANGELOG.md
// under docs/scribe/, so everything for one history doc is grouped in one place.
var subdirForDoc = map[scribe.Doc]string{
	scribe.DocJournal:   "journal",
	scribe.DocChangelog: "changelog",
}

// sidPrefixLen is how many characters of the sanitized session id go into a
// file name. 8 is plenty to make collisions astronomically unlikely across the
// people and sessions of one repo while keeping the name short. This suffix —
// NOT the date or slug, which many sessions can share — is what guarantees two
// sessions never resolve to the same path, which is the whole point of the
// per-session layout, so it is never dropped even when the slug is empty.
const sidPrefixLen = 8

// dateLayout is the calendar-day stamp used in file names: YYYY-MM-DD, no clock
// time, so a file sorts and reads by day. It matches internal/index's layout.
const dateLayout = "2006-01-02"

// SessionFilePath returns the path, RELATIVE to docs/scribe/, of the file that
// holds one session's entries for a history doc. Examples:
//
//	journal/2026-08-21-auth-refactor-a1b2c3d4.md
//	changelog/2026-08-21-a1b2c3d4.md   (when the summary yields no usable slug)
//
// The scheme is <subdir>/<YYYY-MM-DD>[-<slug>]-<short8sid>.md. The session-id
// suffix is mandatory and is what makes the path unique per session, so two
// teammates writing on the same day about the same thing still get distinct
// files and never a merge conflict — the reason per-session mode exists. The
// slug is an optional readability hint; when Slug returns "" the name simply
// omits it (and its separating hyphen) rather than failing.
//
// It returns an error for the two state docs (PROJECT.md, DECISIONS.md): those
// are rewritten in place and never split per session, so asking for a
// per-session path for them is a caller bug worth surfacing, not a path worth
// inventing. It also errors when the session id has no usable characters, since
// without the suffix the uniqueness guarantee is gone.
func SessionFilePath(doc scribe.Doc, m SessionMeta) (string, error) {
	subdir, ok := subdirForDoc[doc]
	if !ok {
		return "", fmt.Errorf("layout: %s is not a per-session history doc (only %s and %s split per session)", doc, scribe.DocChangelog, scribe.DocJournal)
	}

	sid := sanitizeSessionID(m.SessionID)
	if sid == "" {
		return "", fmt.Errorf("layout: session id %q has no usable characters for a file name", m.SessionID)
	}
	if len(sid) > sidPrefixLen {
		sid = sid[:sidPrefixLen]
	}

	var name strings.Builder
	name.WriteString(subdir)
	name.WriteByte('/')
	name.WriteString(m.Date.Format(dateLayout))
	if slug := Slug(m.Summary); slug != "" {
		name.WriteByte('-')
		name.WriteString(slug)
	}
	name.WriteByte('-')
	name.WriteString(sid)
	name.WriteString(".md")
	return name.String(), nil
}

// sanitizeSessionID reduces a session id to the [a-z0-9] characters that are
// safe and stable in a file name, lowercasing letters and dropping everything
// else (hyphens in a UUID, say). It is deterministic and keeps character order,
// so the first sidPrefixLen characters of the result are a stable fingerprint
// of the id.
func sanitizeSessionID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unknownAuthor is the neutral value rendered when a session file has no author.
// The layout never omits the author line — a present-but-neutral value keeps the
// frontmatter shape identical across files, so tools and readers can rely on it.
const unknownAuthor = "unknown"

// RenderSessionFile returns the full markdown body of one session file: a small
// YAML frontmatter block (date, session, author, summary) followed by the entry
// content. The frontmatter makes each file self-describing — a reader who opens
// journal/2026-08-21-...md directly, without the rollup, still sees whose
// session it was and when — and gives the rollup a machine-readable header if it
// ever needs to re-derive one.
//
// It is pure and deterministic: the same inputs always produce byte-identical
// output, ending in exactly one trailing newline so the committed file has no
// ragged tail and diffs cleanly. author may be empty; it renders as a neutral
// value (unknownAuthor) rather than a blank field. Summary is emitted as-is on a
// single line; the frontmatter is intentionally simple key: value text, not a
// full YAML serializer, because the only values it carries are a date, an id, a
// name and a one-line summary.
func RenderSessionFile(m SessionMeta, author, entry string) string {
	author = strings.TrimSpace(author)
	if author == "" {
		author = unknownAuthor
	}
	summary := oneLine(m.Summary)

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("date: " + m.Date.Format(dateLayout) + "\n")
	b.WriteString("session: " + m.SessionID + "\n")
	b.WriteString("author: " + author + "\n")
	b.WriteString("summary: " + summary + "\n")
	b.WriteString("---\n")

	// The entry is the session's own content. Trim only the trailing newlines
	// it may carry so we can guarantee exactly one at the end; leading and
	// interior whitespace is the entry's own business and left untouched. An
	// entry that is empty (or only newlines) adds no body at all, so the file
	// ends on the frontmatter's single trailing newline rather than a run of
	// blank lines.
	body := strings.TrimRight(entry, "\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// oneLine flattens a value to a single line by turning CR and LF into spaces
// and trimming the ends. Frontmatter is one key per line, so a newline in the
// summary would break the block; this keeps the summary field on its own line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}
