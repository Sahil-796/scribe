package wizard

import (
	"errors"
	"os"
	"testing"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// withPipes points stdin/stdout at an os.Pipe for the duration of the test.
// A pipe is guaranteed not to be a terminal, so IsInteractive is guaranteed
// false without depending on however the test binary itself was launched
// (which may or may not have a real tty behind it).
func withPipes(t *testing.T) {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdin): %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdout): %v", err)
	}

	origIn, origOut := stdin, stdout
	stdin, stdout = inR, outW
	t.Cleanup(func() {
		stdin, stdout = origIn, origOut
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
	})
}

func TestIsInteractive_FalseOnPipes(t *testing.T) {
	withPipes(t)

	if IsInteractive() {
		t.Fatal("IsInteractive() = true with stdin/stdout pointed at pipes; pipes are never terminals")
	}
}

func TestAsk_NotInteractive(t *testing.T) {
	withPipes(t)

	_, err := Ask(Options{RepoRoot: "/tmp/repo"})
	if !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("Ask() error = %v, want ErrNotInteractive", err)
	}
}

func TestReview_NotInteractive(t *testing.T) {
	withPipes(t)

	approved, err := Review(map[scribe.Doc]string{scribe.DocProject: "hi"})
	if !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("Review() error = %v, want ErrNotInteractive", err)
	}
	if approved {
		t.Fatal("Review() approved = true on the not-interactive path, want false")
	}
}

func TestWithDefaults_FillsZeroValues(t *testing.T) {
	got := withDefaults(Options{})

	if got.DefaultAgent != DefaultAgent {
		t.Errorf("DefaultAgent = %q, want %q", got.DefaultAgent, DefaultAgent)
	}
	if got.DefaultModel != DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", got.DefaultModel, DefaultModel)
	}
	if got.DocsDir != scribe.DocsDir {
		t.Errorf("DocsDir = %q, want %q", got.DocsDir, scribe.DocsDir)
	}
	if !containsString(got.Agents, DefaultAgent) {
		t.Errorf("Agents = %v, want to contain default agent %q", got.Agents, DefaultAgent)
	}
}

func TestWithDefaults_PreservesExplicitValues(t *testing.T) {
	o := Options{
		RepoRoot:     "/repo",
		Agents:       []string{"claude", "custom"},
		DefaultAgent: "claude",
		DefaultModel: "some-model",
		DocsDir:      "docs/custom",
	}

	got := withDefaults(o)
	if got.DefaultAgent != "claude" || got.DefaultModel != "some-model" || got.DocsDir != "docs/custom" {
		t.Errorf("withDefaults mutated explicit values: %+v", got)
	}
	if len(got.Agents) != 2 {
		t.Errorf("Agents = %v, want unchanged 2-element list", got.Agents)
	}
}

func TestWithDefaults_AgentNotInListIsPrepended(t *testing.T) {
	o := Options{
		Agents:       []string{"opencode", "custom"},
		DefaultAgent: "codex", // not in Agents
	}

	got := withDefaults(o)
	if len(got.Agents) == 0 || got.Agents[0] != "codex" {
		t.Errorf("Agents = %v, want DefaultAgent %q prepended", got.Agents, "codex")
	}
	if !containsString(got.Agents, "opencode") {
		t.Errorf("Agents = %v, want original entries preserved", got.Agents)
	}
}

func TestContainsString(t *testing.T) {
	list := []string{"a", "b", "c"}
	if !containsString(list, "b") {
		t.Error("containsString(list, \"b\") = false, want true")
	}
	if containsString(list, "z") {
		t.Error("containsString(list, \"z\") = true, want false")
	}
	if containsString(nil, "a") {
		t.Error("containsString(nil, \"a\") = true, want false")
	}
}

// TestResolveModel covers the select-plus-free-text pair the model question
// uses: the graded models are offered as choices, and "something else"
// reveals a field so nobody is limited to the three that happened to be
// benchmarked once.
func TestResolveModel(t *testing.T) {
	tests := []struct {
		name           string
		choice, custom string
		want           string
	}{
		{"graded model wins", GradedModels[0].ID, "ignored", GradedModels[0].ID},
		{"other uses the typed value", ModelOther, "opencode/some-new-model", "opencode/some-new-model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveModel(tt.choice, tt.custom); got != tt.want {
				t.Errorf("resolveModel(%q, %q) = %q, want %q", tt.choice, tt.custom, got, tt.want)
			}
		})
	}
}

// TestGradedModelsRanking guards the ordering the form depends on: the
// pre-selected option is the first, so it must stay the bakeoff's winner.
// docs/findings/00-models.md is the source of truth.
func TestGradedModelsRanking(t *testing.T) {
	if len(GradedModels) < 2 {
		t.Fatal("GradedModels should list every model phase 00 actually graded")
	}
	if GradedModels[0].ID != DefaultModel {
		t.Errorf("first graded model %q should be DefaultModel %q — the form pre-selects it", GradedModels[0].ID, DefaultModel)
	}
	if !isGradedModel(DefaultModel) {
		t.Error("isGradedModel(DefaultModel) = false")
	}
	if isGradedModel("opencode/not-a-real-model") {
		t.Error("isGradedModel accepted an unknown model")
	}
}
