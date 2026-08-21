package layout

import "testing"

func TestParseMode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Mode
		wantErr bool
	}{
		{"empty defaults to per-session", "", PerSession, false},
		{"explicit per-session", "per-session", PerSession, false},
		{"explicit shared", "shared", Shared, false},
		{"unknown value errors", "monolith", "", true},
		{"case-sensitive: PerSession is not valid", "Per-Session", "", true},
		{"whitespace is not tolerated", " shared ", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseMode(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMode(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestModeConstants pins the wire values, which must match the strings written
// to config.json by internal/install so a config round-trips unchanged.
func TestModeConstants(t *testing.T) {
	if PerSession != "per-session" {
		t.Errorf("PerSession = %q, want %q", PerSession, "per-session")
	}
	if Shared != "shared" {
		t.Errorf("Shared = %q, want %q", Shared, "shared")
	}
}
