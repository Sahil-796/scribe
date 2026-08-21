// Package nudge implements the uninitialised-repo reminder: docs/PLAN.md's
// "cheap mitigation" for the one gap zero-input design leaves open — you
// have to remember to turn scribe on, and the sessions you most want
// documented are usually the early ones, before you've thought about scribe
// at all.
//
// It has to fire in repos scribe was never initialised in, which is
// precisely where scribe has installed nothing — no hook, no .scribe/, no
// docs/scribe/. That rules out a project-level Stop hook (there's nothing
// installed to run it) and rules out this package doing anything without
// being asked: it never touches the user's global Claude settings on its
// own. Installing the SessionStart hook that drives Run is an explicit,
// separate opt-in — see cmd/scribe/nudge.go's --install/--remove — and this
// package only ever does the counting and the one-line message once that
// hook is calling it.
//
// State lives in the user's config dir (install.GlobalConfigDir), not the
// repo: the whole point is that scribe has written nothing into the repos
// this tracks.
package nudge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Sahil-796/scribe/internal/install"
)

// Threshold is the session count at which the nudge fires. Matches
// docs/PLAN.md's example message verbatim ("6 sessions so far").
const Threshold = 6

// MaxTrackedRepos bounds the state file. Working across many uninitialised
// repos over months (every scratch clone, every tutorial repo, every repo
// you decided not to use scribe on) must not grow the counter file forever
// — when adding a new repo would exceed this, the least-recently-seen
// entries are evicted first, since those are the ones most likely to be
// abandoned rather than the one just visited.
const MaxTrackedRepos = 200

const stateFileName = "nudge-state.json"

// Message is the exact line Run prints when it fires, per docs/PLAN.md:
// "one line once" — printed here rather than built inline in Run so
// cmd/scribe/nudge_test.go can assert against it without duplicating the
// wording.
func Message() string {
	return fmt.Sprintf("scribe isn't on here — %d sessions so far. `scribe init` to catch up.", Threshold)
}

// state is the on-disk shape of the counter file: one entry per repo,
// keyed by its absolute path.
type state struct {
	Repos map[string]repoState `json:"repos"`
}

type repoState struct {
	Count    int       `json:"count"`
	Notified bool      `json:"notified"`
	LastSeen time.Time `json:"lastSeen"`
}

// Run is what `scribe nudge` (no flags) does — the hidden entrypoint the
// installed SessionStart hook invokes. It resolves cwd to a git repo,
// counts one more session against it unless scribe is already on there,
// and writes Message() to out exactly once per repo, the first time the
// count reaches Threshold.
//
// Every failure along the way — cwd isn't a git repo, the state file can't
// be read or written, whatever — is silent and non-fatal. This is a
// SessionStart hook: per docs/phases/04-config-and-safety.md it "must be
// fast, must never block a Claude session, and must exit 0 no matter what
// goes wrong," and a reminder about a reminder is not worth risking that
// contract for.
func Run(cwd string, now time.Time, out io.Writer) {
	repoRoot, ok := findRepoRoot(cwd)
	if !ok {
		return
	}

	if alreadyOn(repoRoot) {
		// Best-effort tidy: a repo that graduated to `scribe init` has no
		// further use for its counter entry, so drop it rather than let
		// dead weight sit in the state file until MaxTrackedRepos evicts
		// it anyway. Failure here is silent like everything else in Run.
		_ = forget(repoRoot)
		return
	}

	fire, err := recordSession(repoRoot, now)
	if err != nil || !fire {
		return
	}
	fmt.Fprintln(out, Message())
}

// alreadyOn reports whether scribe is already turned on for repoRoot.
// Ambiguous outcomes (a config file that exists but fails to parse, a read
// error that isn't "file doesn't exist") are treated as "on" rather than
// "off": the failure mode of wrongly staying silent is a missed reminder,
// the failure mode of wrongly nagging about a repo scribe has, in fact,
// touched is worse — the one thing Run must never do is nag twice or nag
// somewhere scribe is already running.
func alreadyOn(repoRoot string) bool {
	_, err := install.ReadConfig(repoRoot)
	if err == nil {
		return true
	}
	return !errors.Is(err, install.ErrNotInitialised)
}

// recordSession increments repoRoot's session count, bumps its LastSeen,
// prunes the state file back under MaxTrackedRepos if needed, and reports
// whether this call is the one that should fire the nudge: the count just
// reached (or already was at, for a state file written by an older
// binary with a smaller Threshold) Threshold, and no earlier call already
// fired it. fire is only ever true once per repo — Notified is set in the
// same write that returns true.
func recordSession(repoRoot string, now time.Time) (fire bool, err error) {
	path, err := statePath()
	if err != nil {
		return false, err
	}

	st, err := loadState(path)
	if err != nil {
		return false, err
	}
	if st.Repos == nil {
		st.Repos = map[string]repoState{}
	}

	rs := st.Repos[repoRoot]
	rs.Count++
	rs.LastSeen = now
	fire = rs.Count >= Threshold && !rs.Notified
	if fire {
		rs.Notified = true
	}
	st.Repos[repoRoot] = rs

	prune(&st)

	if err := saveState(path, st); err != nil {
		return false, err
	}
	return fire, nil
}

// forget drops repoRoot's counter entry entirely, if the state file exists
// at all. Not finding one, or not finding repoRoot in it, is not an error.
func forget(repoRoot string) error {
	path, err := statePath()
	if err != nil {
		return err
	}
	st, err := loadState(path)
	if err != nil {
		return err
	}
	if st.Repos == nil {
		return nil
	}
	if _, ok := st.Repos[repoRoot]; !ok {
		return nil
	}
	delete(st.Repos, repoRoot)
	return saveState(path, st)
}

// prune evicts the least-recently-seen entries once st holds more than
// MaxTrackedRepos, so the state file has a hard ceiling regardless of how
// many different repos ever get counted from this machine.
func prune(st *state) {
	if len(st.Repos) <= MaxTrackedRepos {
		return
	}
	type kv struct {
		key  string
		seen time.Time
	}
	entries := make([]kv, 0, len(st.Repos))
	for k, v := range st.Repos {
		entries = append(entries, kv{k, v.LastSeen})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].seen.Before(entries[j].seen) })
	drop := len(entries) - MaxTrackedRepos
	for i := 0; i < drop; i++ {
		delete(st.Repos, entries[i].key)
	}
}

func statePath() (string, error) {
	dir, err := install.GlobalConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateFileName), nil
}

// loadState reads path, treating a missing file as an empty, freshly-zeroed
// state rather than an error — the common case for a machine that has
// never nudged before.
func loadState(path string) (state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state{Repos: map[string]repoState{}}, nil
		}
		return state{}, err
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		// A corrupt counter file is low-stakes (unlike settings.json, it
		// carries no user authorship to lose) — starting over is safer
		// than failing Run outright, which per Run's own contract must
		// never surface an error to the hook that called it.
		return state{Repos: map[string]repoState{}}, nil
	}
	if st.Repos == nil {
		st.Repos = map[string]repoState{}
	}
	return st, nil
}

// saveState writes st to path. Plain write, not the temp-file-plus-rename
// dance install.go's settings writer uses: unlike a hand-maintained
// settings.json, losing or truncating this file mid-write costs a repeat
// nudge at worst, never a user's own data.
func saveState(path string, st state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// findRepoRoot walks up from cwd looking for a .git entry, the same way
// cmd/scribe's findGitRoot does for init/run/on/off — duplicated rather
// than imported because that helper lives in package main (cmd/scribe),
// which internal/nudge, an internal package, cannot import.
func findRepoRoot(cwd string) (string, bool) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
