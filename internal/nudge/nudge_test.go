package nudge

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahil-796/scribe/internal/install"
)

// withGlobalConfigDir redirects install.GlobalConfigDir (and everything
// nudge builds on it) at a fresh t.TempDir(), so no test in this file ever
// touches the developer's real ~/.config/scribe/nudge-state.json.
func withGlobalConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// gitRepo makes dir look like a git repo root (a bare .git entry is enough
// for findRepoRoot's os.Stat check) and returns dir.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return dir
}

func TestRun_NotAGitRepo_Silent(t *testing.T) {
	withGlobalConfigDir(t)
	dir := t.TempDir() // no .git

	var out bytes.Buffer
	for i := 0; i < Threshold+2; i++ {
		Run(dir, time.Now(), &out)
	}
	if out.Len() != 0 {
		t.Fatalf("Run outside a git repo: got output %q, want none", out.String())
	}
}

func TestRun_ScribeAlreadyOn_Silent(t *testing.T) {
	withGlobalConfigDir(t)
	repo := gitRepo(t)
	if err := install.WriteConfig(repo, install.Config{Agent: "opencode", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	var out bytes.Buffer
	for i := 0; i < Threshold+2; i++ {
		Run(repo, time.Now(), &out)
	}
	if out.Len() != 0 {
		t.Fatalf("Run in an already-initialised repo: got output %q, want none", out.String())
	}
}

func TestRun_FiresExactlyOnceAtThreshold(t *testing.T) {
	withGlobalConfigDir(t)
	repo := gitRepo(t)

	var fires int
	for i := 0; i < Threshold+5; i++ {
		var out bytes.Buffer
		Run(repo, time.Now(), &out)
		if out.Len() > 0 {
			fires++
			if !strings.Contains(out.String(), "scribe init") {
				t.Errorf("nudge output missing the call to action: %q", out.String())
			}
		}
	}
	if fires != 1 {
		t.Fatalf("Run fired %d times across %d sessions, want exactly 1", fires, Threshold+5)
	}
}

func TestRun_BelowThreshold_Silent(t *testing.T) {
	withGlobalConfigDir(t)
	repo := gitRepo(t)

	var out bytes.Buffer
	for i := 0; i < Threshold-1; i++ {
		Run(repo, time.Now(), &out)
	}
	if out.Len() != 0 {
		t.Fatalf("Run below threshold: got output %q, want none", out.String())
	}
}

func TestRun_NeverNagsTwiceEvenAfterMoreSessions(t *testing.T) {
	withGlobalConfigDir(t)
	repo := gitRepo(t)

	for i := 0; i < Threshold; i++ {
		Run(repo, time.Now(), &bytes.Buffer{})
	}
	// The Threshold-th call above already fired once (silently discarded
	// here); every call after that must stay silent no matter how many
	// more sessions happen.
	var out bytes.Buffer
	for i := 0; i < 20; i++ {
		Run(repo, time.Now(), &out)
	}
	if out.Len() != 0 {
		t.Fatalf("Run kept nagging after the first fire: got %q", out.String())
	}
}

func TestRun_DistinctReposCountedIndependently(t *testing.T) {
	withGlobalConfigDir(t)
	a := gitRepo(t)
	b := gitRepo(t)

	for i := 0; i < Threshold-1; i++ {
		Run(a, time.Now(), &bytes.Buffer{})
	}

	var out bytes.Buffer
	Run(b, time.Now(), &out)
	if out.Len() != 0 {
		t.Fatalf("repo b fired after only 1 session, want silence (repos must not share a counter): %q", out.String())
	}
}

func TestRun_InitialisingMidwayForgetsTheCounter(t *testing.T) {
	withGlobalConfigDir(t)
	repo := gitRepo(t)

	for i := 0; i < Threshold-1; i++ {
		Run(repo, time.Now(), &bytes.Buffer{})
	}

	if err := install.WriteConfig(repo, install.Config{Agent: "opencode", Enabled: true}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	Run(repo, time.Now(), &bytes.Buffer{}) // observes "already on", should forget

	path, err := statePath()
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	st, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if _, ok := st.Repos[repo]; ok {
		t.Errorf("repo entry still present in state after scribe init, want it forgotten")
	}
}

func TestPrune_BoundsStateFileSize(t *testing.T) {
	st := state{Repos: map[string]repoState{}}
	base := time.Now()
	for i := 0; i < MaxTrackedRepos+10; i++ {
		st.Repos[filepath.Join("/repo", string(rune('a'+i%26)), time.Duration(i).String())] = repoState{
			Count:    1,
			LastSeen: base.Add(time.Duration(i) * time.Minute),
		}
	}

	prune(&st)

	if len(st.Repos) != MaxTrackedRepos {
		t.Fatalf("len(st.Repos) = %d after prune, want %d", len(st.Repos), MaxTrackedRepos)
	}
}

func TestPrune_KeepsMostRecentlySeen(t *testing.T) {
	st := state{Repos: map[string]repoState{}}
	base := time.Now()
	const total = MaxTrackedRepos + 3
	for i := 0; i < total; i++ {
		st.Repos[filepath.Join("/repo", padded(i))] = repoState{
			Count:    1,
			LastSeen: base.Add(time.Duration(i) * time.Minute), // higher i = more recent
		}
	}

	prune(&st)

	// The 3 oldest (i = 0, 1, 2) should have been evicted.
	for i := 0; i < 3; i++ {
		if _, ok := st.Repos[filepath.Join("/repo", padded(i))]; ok {
			t.Errorf("oldest entry i=%d survived prune, want it evicted", i)
		}
	}
	// The most recent should have survived.
	if _, ok := st.Repos[filepath.Join("/repo", padded(total-1))]; !ok {
		t.Errorf("most recently seen entry was evicted, want it kept")
	}
}

func padded(i int) string {
	return time.Unix(int64(i), 0).Format("20060102150405")
}

func TestMessage_MatchesPlanWording(t *testing.T) {
	got := Message()
	if !strings.Contains(got, "scribe init") || !strings.Contains(got, "6 sessions") {
		t.Errorf("Message() = %q, doesn't match docs/PLAN.md's wording", got)
	}
}
