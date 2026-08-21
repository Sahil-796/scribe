// Package attribution resolves "who is running scribe right now" so that
// history entries written into docs/scribe/ can be credited to a person, not
// just to the tool. Phase 06 makes the docs multi-person; without a byline, a
// shared journal reads as if one anonymous author wrote everything.
//
// Resolution is deliberately best-effort: attribution is metadata riding
// along with a doc entry, not something a run should ever fail over. Resolve
// never returns an error and never panics — a repo with no git config, no
// relevant env vars and an unreadable os/user lookup still gets a usable
// (if empty) Author back, and callers fall back to "unknown" rather than
// abort.
package attribution

import (
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// Author identifies whoever a doc entry should be credited to. Either field
// may be empty — a git config with a name but no email is common, and
// Resolve fills each field independently rather than treating the identity
// as all-or-nothing.
type Author struct {
	Name  string
	Email string
}

// unknownName is the final fallback when no source yields a name at all. It
// is a name, not an error sentinel: Display and Byline treat it as "nothing
// worth printing" via IsZero, but a zero-value Author{} (empty Name) also
// counts as zero, so callers never have to know about this constant.
const unknownName = "unknown"

// gitConfig looks up one git config key ("user.name" or "user.email") in the
// repo at repoRoot, returning (value, true) when git reports the key as set
// and non-empty, or ("", false) for a missing key, a non-zero exit, or a
// value that is empty after trimming.
//
// This is a package-level var (rather than a plain function) so tests can
// substitute a fake and assert resolution precedence without depending on
// the real machine's git config — the real implementation shells out to
// `git -C repoRoot config <key>`, which would make tests non-deterministic
// across machines and CI environments.
var gitConfig = func(repoRoot, key string) (string, bool) {
	cmd := exec.Command("git", "-C", repoRoot, "config", key)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", false
	}
	return v, true
}

// envLookup reads an environment variable. It exists as a seam (like
// gitConfig) so tests can pin the env sources without mutating real process
// environment.
var envLookup = os.Getenv

// osUserLookup returns the current OS user, mirroring os/user.Current's
// signature. A seam for tests, same reasoning as gitConfig and envLookup.
var osUserLookup = user.Current

// Resolve determines the current author for repoRoot, trying each source in
// order and taking the first non-empty result — independently for Name and
// Email, since a source can supply one without the other:
//
//  1. git config in the repo (user.name / user.email) — the most specific
//     source: it's what `git commit` in this exact repo would use.
//  2. GIT_AUTHOR_NAME / GIT_AUTHOR_EMAIL, falling back to
//     GIT_COMMITTER_NAME / GIT_COMMITTER_EMAIL — the env vars git itself
//     honors, useful when config isn't set (CI, containers, scripted runs).
//  3. the OS user for Name only (Name field if set, else Username) — no
//     email, since an OS account carries no email at all.
//  4. "unknown" with no email, so callers always get something printable.
//
// Resolve never errors and never panics: every lookup source is best-effort,
// and a repo/environment that supplies nothing still yields a usable Author.
func Resolve(repoRoot string) Author {
	return Author{
		Name:  resolveName(repoRoot),
		Email: resolveEmail(repoRoot),
	}
}

func resolveName(repoRoot string) string {
	if v, ok := gitConfig(repoRoot, "user.name"); ok {
		return v
	}
	if v := envLookup("GIT_AUTHOR_NAME"); v != "" {
		return v
	}
	if v := envLookup("GIT_COMMITTER_NAME"); v != "" {
		return v
	}
	if u, err := osUserLookup(); err == nil && u != nil {
		if u.Name != "" {
			return u.Name
		}
		if u.Username != "" {
			return u.Username
		}
	}
	return unknownName
}

func resolveEmail(repoRoot string) string {
	if v, ok := gitConfig(repoRoot, "user.email"); ok {
		return v
	}
	if v := envLookup("GIT_AUTHOR_EMAIL"); v != "" {
		return v
	}
	if v := envLookup("GIT_COMMITTER_EMAIL"); v != "" {
		return v
	}
	return ""
}

// IsZero reports whether a carries no real identity — an empty or
// placeholder Name and no Email — meaning there is nothing worth
// attributing. Both StampShared and Byline consult this to decide whether
// printing an author line would add information or just noise.
func (a Author) IsZero() bool {
	return (a.Name == "" || a.Name == unknownName) && a.Email == ""
}

// Display renders a for a human reader: "Name <email>" when both are known,
// just "Name" when there's no email, and "unknown" when a is zero. This is
// the one formatting rule other packages should use whenever an Author needs
// to appear in text — keeping it here means the "Name <email>" convention
// only has to be decided once.
func (a Author) Display() string {
	if a.IsZero() {
		return unknownName
	}
	if a.Email == "" {
		return a.Name
	}
	name := a.Name
	if name == "" {
		name = unknownName
	}
	return name + " <" + a.Email + ">"
}

// Byline returns a compact inline marker suitable to trail a markdown
// history entry, e.g. "— Jane Doe <jane@example.com>". It is "" when a is
// zero: an entry attributed to nobody-in-particular should stay unmarked
// rather than print a hollow "— unknown" on every line.
func (a Author) Byline() string {
	if a.IsZero() {
		return ""
	}
	return "— " + a.Display()
}

// StampShared appends a's byline to entry as its own trailing line, for docs
// under the "shared" team layout where one file accumulates entries from
// multiple authors and each needs its own credit.
//
// StampShared is idempotent: if entry's last non-blank line is already
// exactly this byline, entry is returned unchanged (aside from trailing
// whitespace normalization) rather than stamped a second time. This matters
// because the worker may reprocess overlapping transcript slices and call
// this on the same entry more than once — without idempotency, a re-run
// would pile up duplicate "— Jane Doe" lines on one entry.
//
// When a is zero, entry is returned with only trailing whitespace trimmed —
// there is no byline to add, and StampShared should still normalize rather
// than leave the caller to do it.
func StampShared(entry string, a Author) string {
	byline := a.Byline()
	trimmed := strings.TrimRight(entry, "\n\t \r")

	if byline == "" {
		return trimmed
	}

	if lastLine(trimmed) == byline {
		return trimmed
	}

	return trimmed + "\n" + byline
}

// lastLine returns the final line of s (s having already had trailing
// whitespace stripped), used by StampShared to check for an existing byline
// without caring how many blank lines or paragraphs precede it.
func lastLine(s string) string {
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
