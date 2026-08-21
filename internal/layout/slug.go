package layout

import "strings"

// slugMaxRunes caps a slug's length. Session file names carry a date and an
// 8-char session suffix on top of the slug, and the slug is only a human hint
// at what the session was about — the suffix, not the slug, is what makes the
// name unique. ~50 runes keeps names readable and well clear of filesystem path
// limits while still fitting a short headline. The cap is applied at a word
// boundary (see Slug) so it never ends on a half-word.
const slugMaxRunes = 50

// Slug turns a free-text summary or heading into a filesystem-safe kebab-case
// slug: lowercase ASCII words joined by single hyphens, e.g. "Auth refactor:
// drop cookies!" -> "auth-refactor-drop-cookies".
//
// It is deliberately lossy and deterministic. Every rune that is not an ASCII
// letter or digit — spaces, punctuation, and every non-ASCII rune (accented
// letters, CJK, emoji) — is treated as a separator, so the result is always
// pure [a-z0-9-]. Non-ASCII input is dropped rather than transliterated because
// transliteration is neither deterministic across locales nor obviously correct
// for every script, and the slug is only a readability hint on a name that is
// already made unique by its session-id suffix. Runs of separators collapse to
// a single hyphen and leading/trailing hyphens are trimmed, so no name ever has
// "--" or a dangling "-".
//
// The result is truncated to slugMaxRunes at a word boundary: if the cut would
// land mid-word the slug is backed up to the last whole word, so a long summary
// yields a clean prefix, never a chopped token. Input that contains no usable
// ASCII letters or digits (empty, all-punctuation, all-emoji) yields "" — the
// caller is expected to fall back to a slug-free name (see SessionFilePath),
// this function does not invent one.
func Slug(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevHyphen = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a') // lowercase ASCII
			prevHyphen = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			// Any separator or non-ASCII rune. Emit at most one hyphen for a
			// run, and never a leading one (b.Len()==0 means nothing kept yet).
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	return truncateAtWord(slug, slugMaxRunes)
}

// truncateAtWord shortens a hyphen-joined slug to at most max runes without
// cutting a word in half. If the slug already fits it is returned unchanged.
// Otherwise it is cut at max runes and then backed up to the last hyphen, so
// the result ends on a whole word; the trailing hyphen is trimmed. A first word
// longer than max on its own has no earlier boundary to fall back to, so it is
// returned hard-cut at max rather than emptied — some slug beats none.
func truncateAtWord(slug string, max int) string {
	runes := []rune(slug)
	if len(runes) <= max {
		return slug
	}
	cut := string(runes[:max])
	if i := strings.LastIndexByte(cut, '-'); i > 0 {
		return cut[:i]
	}
	return cut
}
