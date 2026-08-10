// Package redact is the single choke point transcript and repo bytes must
// pass through before they reach a writer prompt (docs/PLAN.md's Risks
// section, docs/phases/04-config-and-safety.md: "redaction is the gate on
// this repo going public and on any hosted model seeing a transcript").
//
// A Redactor does two independent jobs, matching install.PrivacyConfig's
// two fields:
//
//   - Redact(text) strips values bound to a configured key name (api_key,
//     token, ...), wherever that key shows up structurally — JSON, YAML,
//     an env-style assignment, a CLI flag, an `export` line — while leaving
//     the surrounding prose untouched. It also strips a handful of
//     high-signal secret shapes (API key prefixes, GitHub tokens, AWS
//     access key ids, bearer tokens, PEM blocks, secret-looking .env
//     lines) regardless of whether any configured key matched, because
//     those shapes are secrets by construction and a user who forgot to
//     list the right key name should not pay for the omission with a
//     leaked credential.
//   - IgnoreFile(path) reports whether path matches one of the configured
//     ignore globs, so a caller (internal/seed/scan.go) can skip reading a
//     file's contents entirely rather than read-then-redact.
//
// Both are read-only and side-effect-free: nothing here writes to disk or
// talks to a network. The package has no dependency on internal/install —
// callers pass plain []string slices (PrivacyConfig.Redact,
// PrivacyConfig.Ignore) rather than a config type, so redact stays wireable
// from anywhere without an import cycle risk.
package redact

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Redactor holds the normalised configured key set and the raw ignore
// globs. The zero value is not usable — construct with New. A nil
// *Redactor is treated as a programmer error, not a silent pass-through:
// every exported method panics on a nil receiver rather than quietly
// returning its input unchanged, because the failure mode of "redaction
// silently did nothing" is invisible and permanent (see
// docs/phases/04-config-and-safety.md, "the failure mode of the optional
// version is silent and permanent").
type Redactor struct {
	// keys maps a normalised key (see normalizeKey) to the canonical,
	// as-configured name used in the placeholder — e.g. normalizeKey
	// produces "apikey" for both "api_key" and "apiKey", but the
	// placeholder always reads "[redacted:api_key]" if that's how the key
	// was spelled in config, regardless of which spelling appeared in the
	// text being redacted.
	keys map[string]string

	// ignoreGlobs are PrivacyConfig.Ignore verbatim, matched with ** support
	// by matchGlob below (path/filepath.Match alone does not handle **).
	ignoreGlobs []string
}

// New builds a Redactor from a repo's configured redact-key names and
// ignore globs (install.PrivacyConfig's two fields, passed as plain slices
// so this package never imports internal/install). Both may be empty —
// that's a legitimate, if unusual, configuration ("redact nothing by key
// name, ignore nothing by path") and is different from a nil *Redactor,
// which is a wiring bug.
func New(keys []string, ignoreGlobs []string) *Redactor {
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		nk := normalizeKey(k)
		if nk == "" {
			continue
		}
		if _, exists := m[nk]; !exists {
			m[nk] = k
		}
	}
	return &Redactor{
		keys:        m,
		ignoreGlobs: append([]string(nil), ignoreGlobs...),
	}
}

// normalizeKey collapses a key name to a form that's stable across the
// spellings the same logical key shows up as in real transcript prose:
// "api_key", "API_KEY", "apiKey" and "api-key" must all compare equal.
// Lower-cases and drops every non-alphanumeric rune, which folds
// underscores, hyphens and spaces away and makes case differences (and
// therefore camelCase word boundaries) irrelevant without needing a real
// camelCase splitter.
func normalizeKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchConfiguredKey reports whether key normalises to one of r's
// configured keys, returning the canonical placeholder name if so.
func (r *Redactor) matchConfiguredKey(key string) (string, bool) {
	canon, ok := r.keys[normalizeKey(key)]
	return canon, ok
}

// secretishSubstrings flags an unqualified NAME=value assignment line
// (.env style) as worth redacting even when NAME isn't literally one of
// the configured Redact keys — e.g. DB_PASSWORD or STRIPE_SECRET_KEY
// should not survive just because the config list says "password" and
// "secret" rather than those exact compounds. This is deliberately looser
// than exact key matching: false positives here cost a placeholder in
// place of a harmless value, false negatives cost a leaked credential, and
// docs/phases/04-config-and-safety.md's fail-safe direction picks the
// former every time.
var secretishSubstrings = []string{
	"key", "token", "password", "passwd", "pwd", "secret", "credential", "pat",
}

func looksSecretish(normalizedName string) bool {
	for _, s := range secretishSubstrings {
		if strings.Contains(normalizedName, s) {
			return true
		}
	}
	return false
}

// Structural patterns for "a value bound to a key", each restricted to a
// shape that only appears when a value is actually attached — never a bare
// mention of the key name in prose. That's the property that keeps a
// sentence like "the api_key was wrong so I regenerated it" untouched: it
// has no ':' or '=' or quote-colon-quote immediately after "api_key", so
// none of these match it at all.
var (
	// `"key": "value"` / `'key': 'value'` — JSON/YAML-with-quotes style.
	// Both key and value must be quoted; reBareKV below handles the
	// unquoted-value forms.
	reQuotedKV = regexp.MustCompile(`["']([A-Za-z_][A-Za-z0-9_ -]*)["']\s*:\s*["']([^"']*)["']`)

	// `key=value`, `key: value`, `KEY=value` (also matches inside
	// `export KEY=value`, since the scan isn't anchored to line start —
	// it simply finds "KEY=value" wherever it occurs), `--flag=value`.
	reBareKV = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_-]*)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;]+)`)

	// `--flag value` — a CLI flag and its value separated by whitespace
	// rather than '=' or ':', which reBareKV does not match (it requires
	// an explicit separator character).
	reFlagSpace = regexp.MustCompile(`--([A-Za-z][A-Za-z0-9_-]*)\s+("[^"]*"|'[^']*'|[^\s,;]+)`)

	// A bare `NAME=value` line, .env style. Filtered by looksSecretish
	// below rather than by a configured key match — this is the
	// "regardless of key" case docs/phases/04-config-and-safety.md asks
	// for.
	reEnvLine = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][A-Za-z0-9_]*)[ \t]*=[ \t]*(.+?)[ \t]*$`)
)

// High-signal shapes: secrets by construction, redacted regardless of any
// configured key name. Each of these strings is, on its own, close enough
// to unambiguous that requiring a key match first would just be a way to
// leak a credential because the user's Redact list didn't happen to name
// the right key.
var (
	// OpenAI-style (`sk-...`) and Anthropic-style (`sk-ant-...`) secret
	// keys; the latter is a strict prefix extension of the former so one
	// pattern covers both.
	reSK = regexp.MustCompile(`sk-(?:ant-)?[A-Za-z0-9_-]{10,}`)

	// GitHub personal access tokens, classic and fine-grained.
	reGH = regexp.MustCompile(`gh[po]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}`)

	// AWS access key ids.
	reAWS = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)

	// `Bearer <token>` — only the token itself is redacted, "Bearer " stays
	// so the placeholder reads sensibly in context.
	reBearer = regexp.MustCompile(`(?i)\bBearer[ \t]+([A-Za-z0-9\-_.=]+)`)

	// PEM blocks: `-----BEGIN ... KEY-----` through the matching END line,
	// whatever the key type. (?s) makes '.' match newlines so the body is
	// swallowed along with the header/footer.
	rePEM = regexp.MustCompile(`(?s)-----BEGIN [^-]+-----.*?-----END [^-]+-----`)
)

// span is one region of text to replace with a placeholder.
type span struct {
	start, end int
	label      string
}

// Redact returns text with every value bound to a configured key, and
// every high-signal secret shape, replaced by a visible `[redacted:label]`
// placeholder. Nothing is deleted outright — see the package doc — and
// prose that merely mentions a key name with no value structurally
// attached to it is returned untouched.
//
// This is the single function every prompt builder in internal/worker,
// internal/replay and internal/seed calls on the fully assembled prompt (or
// on each piece of content before it's embedded — see the call sites) —
// nothing downstream of Redact should ever see raw transcript or repo
// bytes.
func (r *Redactor) Redact(text string) string {
	if r == nil {
		panic("redact: Redact called on a nil Redactor — a nil redactor must fail the caller loudly at construction time, not silently pass content through (see docs/phases/04-config-and-safety.md)")
	}
	if text == "" {
		return text
	}

	var spans []span

	// Configured-key matches are collected first, deliberately, so that
	// when a key-based match and a shape-based match cover the exact same
	// span (a value that's both a configured key's value and, say, looks
	// like an OpenAI key) applySpans' stable sort keeps the more
	// informative, config-named placeholder rather than the generic one —
	// "[redacted:api_key]" beats "[redacted:secret-key]" when both would
	// otherwise be equally valid.
	for _, m := range reQuotedKV.FindAllStringSubmatchIndex(text, -1) {
		key := text[m[2]:m[3]]
		if canon, ok := r.matchConfiguredKey(key); ok {
			spans = append(spans, span{m[4], m[5], canon})
		}
	}
	for _, m := range reBareKV.FindAllStringSubmatchIndex(text, -1) {
		key := text[m[2]:m[3]]
		if canon, ok := r.matchConfiguredKey(key); ok {
			vs, ve := trimQuotedSpan(text, m[4], m[5])
			spans = append(spans, span{vs, ve, canon})
		}
	}
	for _, m := range reFlagSpace.FindAllStringSubmatchIndex(text, -1) {
		key := text[m[2]:m[3]]
		if canon, ok := r.matchConfiguredKey(key); ok {
			vs, ve := trimQuotedSpan(text, m[4], m[5])
			spans = append(spans, span{vs, ve, canon})
		}
	}
	for _, m := range reEnvLine.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[2]:m[3]]
		if looksSecretish(normalizeKey(name)) {
			spans = append(spans, span{m[4], m[5], strings.ToLower(name)})
		}
	}

	// PEM blocks: a private key body can easily contain characters that
	// would otherwise trip reBareKV or reEnvLine, and those partial
	// matches would leave most of the key material exposed around a
	// redacted fragment. applySpans prefers the widest span at a shared
	// start position, so a PEM block wins over anything smaller starting
	// at the same offset regardless of collection order.
	for _, m := range rePEM.FindAllStringIndex(text, -1) {
		spans = append(spans, span{m[0], m[1], "private-key"})
	}
	for _, m := range reAWS.FindAllStringIndex(text, -1) {
		spans = append(spans, span{m[0], m[1], "aws-access-key"})
	}
	for _, m := range reGH.FindAllStringIndex(text, -1) {
		spans = append(spans, span{m[0], m[1], "token"})
	}
	for _, m := range reSK.FindAllStringIndex(text, -1) {
		spans = append(spans, span{m[0], m[1], "secret-key"})
	}
	for _, m := range reBearer.FindAllStringSubmatchIndex(text, -1) {
		spans = append(spans, span{m[2], m[3], "token"})
	}

	return applySpans(text, spans)
}

// trimQuotedSpan narrows [start,end) by one rune on each side if it's
// wrapped in a matching quote pair, so a redacted quoted value keeps its
// quotes in the output (`"[redacted:api_key]"` rather than
// `"[redacted:api_key]"` including the quotes inside the brackets).
func trimQuotedSpan(text string, start, end int) (int, int) {
	if end-start >= 2 {
		first, last := text[start], text[end-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return start + 1, end - 1
		}
	}
	return start, end
}

// applySpans sorts spans by start position (widest first at a shared
// start, so e.g. a PEM block wins over a smaller match that happens to
// start at the same offset) and rewrites text, replacing each
// non-overlapping span with its placeholder. A span that overlaps one
// already applied is dropped rather than double-redacted or spliced
// incorrectly.
func applySpans(text string, spans []span) string {
	if len(spans) == 0 {
		return text
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return (spans[i].end - spans[i].start) > (spans[j].end - spans[j].start)
	})

	var b strings.Builder
	cursor := 0
	for _, s := range spans {
		if s.start < cursor {
			continue // overlaps a span already applied
		}
		b.WriteString(text[cursor:s.start])
		fmt.Fprintf(&b, "[redacted:%s]", s.label)
		cursor = s.end
	}
	b.WriteString(text[cursor:])
	return b.String()
}

// IgnoreFile reports whether relPath matches one of the configured ignore
// globs (install.PrivacyConfig.Ignore — e.g. "**/.env*", "**/secrets/**").
// Intended for callers deciding whether to read a file at all
// (internal/seed/scan.go): dropping the file before it's read is the point,
// not redacting its contents after the fact.
//
// relPath is matched segment by segment after normalising to forward
// slashes, with "**" matching zero or more whole path segments —
// path/filepath.Match alone doesn't support "**" (it treats "*" as
// matching everything including separators within a single pattern
// segment, but has no notion of a multi-segment wildcard), so this walks
// the split path itself. See matchGlob.
func (r *Redactor) IgnoreFile(relPath string) bool {
	if r == nil {
		panic("redact: IgnoreFile called on a nil Redactor — a nil redactor must fail the caller loudly at construction time, not silently pass content through (see docs/phases/04-config-and-safety.md)")
	}
	clean := filepath.ToSlash(relPath)
	for _, g := range r.ignoreGlobs {
		if matchGlob(g, clean) {
			return true
		}
	}
	return false
}

// matchGlob matches a single ignore-glob pattern against a forward-slash
// path, splitting both on '/' and matching segment by segment. "**" as a
// whole segment matches zero or more path segments; any other segment is
// matched against the corresponding path segment with path/filepath.Match
// (safe there because a single segment never contains '/').
func matchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pats, segs []string) bool {
	if len(pats) == 0 {
		return len(segs) == 0
	}
	p := pats[0]
	if p == "**" {
		// Zero-segment match: drop the "**" and keep going.
		if matchSegments(pats[1:], segs) {
			return true
		}
		// One-or-more-segment match: consume one path segment and try
		// "**" again against the rest.
		if len(segs) == 0 {
			return false
		}
		return matchSegments(pats, segs[1:])
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := filepath.Match(p, segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pats[1:], segs[1:])
}
