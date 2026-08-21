package layout

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple phrase", "Auth refactor", "auth-refactor"},
		{"punctuation becomes separators", "Auth refactor: drop cookies!", "auth-refactor-drop-cookies"},
		{"collapses repeated separators", "a   --  b", "a-b"},
		{"trims leading and trailing separators", "  --hello world--  ", "hello-world"},
		{"lowercases ascii", "HELLO World", "hello-world"},
		{"keeps digits", "phase 06 teammates", "phase-06-teammates"},
		{"empty input", "", ""},
		{"all punctuation", "!!! --- ???", ""},
		{"unicode letters dropped", "café résumé", "caf-r-sum"},
		{"emoji only", "🚀🔥", ""},
		{"leading number kept", "123 go", "123-go"},
		{"underscores are separators", "auth_refactor_done", "auth-refactor-done"},
		{"slashes are separators", "docs/scribe/plan", "docs-scribe-plan"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Slug(tc.in); got != tc.want {
				t.Fatalf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSlugOutputIsAlwaysSafe asserts the invariant every slug must hold: only
// [a-z0-9-], no leading/trailing hyphen, and no "--".
func TestSlugOutputIsAlwaysSafe(t *testing.T) {
	inputs := []string{
		"", "!!!", "Hello, World!", "café", "🚀 launch",
		"a-----b", "  spaced  out  ", strings.Repeat("word ", 40),
		"UPPER_lower/MiXeD 123", "---leading", "trailing---",
	}
	for _, in := range inputs {
		got := Slug(in)
		for _, r := range got {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				t.Errorf("Slug(%q) = %q contains illegal rune %q", in, got, r)
			}
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("Slug(%q) = %q has a leading/trailing hyphen", in, got)
		}
		if strings.Contains(got, "--") {
			t.Errorf("Slug(%q) = %q contains a double hyphen", in, got)
		}
	}
}

func TestSlugTruncatesAtWordBoundary(t *testing.T) {
	// Many short words: the slug must be cut to <= slugMaxRunes and must not
	// end mid-word (no trailing hyphen, and the last kept word is whole).
	in := "one two three four five six seven eight nine ten eleven twelve thirteen"
	got := Slug(in)
	if len([]rune(got)) > slugMaxRunes {
		t.Fatalf("Slug(%q) = %q length %d exceeds max %d", in, got, len([]rune(got)), slugMaxRunes)
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("Slug(%q) = %q ends on a hyphen (cut mid-word)", in, got)
	}
	// Every emitted word must be a whole word from the input.
	whole := map[string]bool{}
	for _, w := range strings.Fields(in) {
		whole[w] = true
	}
	for _, w := range strings.Split(got, "-") {
		if !whole[w] {
			t.Fatalf("Slug(%q) = %q emitted partial word %q", in, got, w)
		}
	}
}

func TestSlugSingleLongWordHardCut(t *testing.T) {
	// A first word longer than the cap has no earlier boundary; it is hard-cut
	// to exactly the cap rather than emptied — some slug beats none.
	in := strings.Repeat("a", slugMaxRunes+20)
	got := Slug(in)
	if len([]rune(got)) != slugMaxRunes {
		t.Fatalf("Slug(long single word) length = %d, want %d", len([]rune(got)), slugMaxRunes)
	}
}

func TestSlugDeterministic(t *testing.T) {
	in := "Auth refactor: drop cookies!"
	first := Slug(in)
	for i := 0; i < 100; i++ {
		if got := Slug(in); got != first {
			t.Fatalf("Slug not deterministic: %q vs %q", got, first)
		}
	}
}
