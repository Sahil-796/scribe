package attribution

import (
	"errors"
	"os/user"
	"testing"
)

// withGitConfig substitutes gitConfig for the duration of a test and
// restores the original afterward, so tests never depend on (or mutate) the
// real machine's git config.
func withGitConfig(t *testing.T, fn func(repoRoot, key string) (string, bool)) {
	t.Helper()
	orig := gitConfig
	gitConfig = fn
	t.Cleanup(func() { gitConfig = orig })
}

func withEnv(t *testing.T, values map[string]string) {
	t.Helper()
	orig := envLookup
	envLookup = func(key string) string { return values[key] }
	t.Cleanup(func() { envLookup = orig })
}

func withOSUser(t *testing.T, u *user.User, err error) {
	t.Helper()
	orig := osUserLookup
	osUserLookup = func() (*user.User, error) { return u, err }
	t.Cleanup(func() { osUserLookup = orig })
}

func noGitConfig(_, _ string) (string, bool) { return "", false }

func TestResolve_Precedence(t *testing.T) {
	tests := []struct {
		name      string
		gitConfig func(repoRoot, key string) (string, bool)
		env       map[string]string
		osUser    *user.User
		osUserErr error
		want      Author
	}{
		{
			name: "git config wins over everything",
			gitConfig: func(_, key string) (string, bool) {
				switch key {
				case "user.name":
					return "Git Name", true
				case "user.email":
					return "git@example.com", true
				}
				return "", false
			},
			env: map[string]string{
				"GIT_AUTHOR_NAME":  "Env Name",
				"GIT_AUTHOR_EMAIL": "env@example.com",
			},
			osUser: &user.User{Name: "OS Name", Username: "osuser"},
			want:   Author{Name: "Git Name", Email: "git@example.com"},
		},
		{
			name:      "git missing falls back to GIT_AUTHOR env",
			gitConfig: noGitConfig,
			env: map[string]string{
				"GIT_AUTHOR_NAME":  "Env Name",
				"GIT_AUTHOR_EMAIL": "env@example.com",
			},
			osUser: &user.User{Name: "OS Name", Username: "osuser"},
			want:   Author{Name: "Env Name", Email: "env@example.com"},
		},
		{
			name:      "GIT_AUTHOR missing falls back to GIT_COMMITTER env",
			gitConfig: noGitConfig,
			env: map[string]string{
				"GIT_COMMITTER_NAME":  "Committer Name",
				"GIT_COMMITTER_EMAIL": "committer@example.com",
			},
			osUser: &user.User{Name: "OS Name", Username: "osuser"},
			want:   Author{Name: "Committer Name", Email: "committer@example.com"},
		},
		{
			name:      "env missing falls back to os/user Name",
			gitConfig: noGitConfig,
			env:       map[string]string{},
			osUser:    &user.User{Name: "OS Name", Username: "osuser"},
			want:      Author{Name: "OS Name", Email: ""},
		},
		{
			name:      "os/user Name empty falls back to Username",
			gitConfig: noGitConfig,
			env:       map[string]string{},
			osUser:    &user.User{Name: "", Username: "osuser"},
			want:      Author{Name: "osuser", Email: ""},
		},
		{
			name:      "everything missing yields unknown",
			gitConfig: noGitConfig,
			env:       map[string]string{},
			osUserErr: errors.New("no current user"),
			want:      Author{Name: "unknown", Email: ""},
		},
		{
			name: "name-without-email: git sets name only",
			gitConfig: func(_, key string) (string, bool) {
				if key == "user.name" {
					return "Just Name", true
				}
				return "", false
			},
			env:    map[string]string{},
			osUser: &user.User{Name: "OS Name"},
			want:   Author{Name: "Just Name", Email: ""},
		},
		{
			name:      "email resolved independently from git when name isn't set in git",
			gitConfig: noGitConfig,
			env: map[string]string{
				"GIT_AUTHOR_EMAIL": "onlyemail@example.com",
			},
			osUser: &user.User{Name: "OS Name"},
			want:   Author{Name: "OS Name", Email: "onlyemail@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withGitConfig(t, tt.gitConfig)
			withEnv(t, tt.env)
			withOSUser(t, tt.osUser, tt.osUserErr)

			got := Resolve("/some/repo")
			if got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAuthor_IsZero(t *testing.T) {
	tests := []struct {
		name string
		a    Author
		want bool
	}{
		{"zero value", Author{}, true},
		{"unknown name no email", Author{Name: "unknown"}, true},
		{"name only", Author{Name: "Jane"}, false},
		{"email only", Author{Email: "jane@example.com"}, false},
		{"both set", Author{Name: "Jane", Email: "jane@example.com"}, false},
		{"unknown name with email is not zero", Author{Name: "unknown", Email: "jane@example.com"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthor_Display(t *testing.T) {
	tests := []struct {
		name string
		a    Author
		want string
	}{
		{"zero value", Author{}, "unknown"},
		{"name and email", Author{Name: "Jane Doe", Email: "jane@example.com"}, "Jane Doe <jane@example.com>"},
		{"name only", Author{Name: "Jane Doe"}, "Jane Doe"},
		{"email only", Author{Email: "jane@example.com"}, "unknown <jane@example.com>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Display(); got != tt.want {
				t.Errorf("Display() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthor_Byline(t *testing.T) {
	tests := []struct {
		name string
		a    Author
		want string
	}{
		{"zero value returns empty", Author{}, ""},
		{"name and email", Author{Name: "Jane Doe", Email: "jane@example.com"}, "— Jane Doe <jane@example.com>"},
		{"name only", Author{Name: "Jane Doe"}, "— Jane Doe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Byline(); got != tt.want {
				t.Errorf("Byline() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStampShared(t *testing.T) {
	jane := Author{Name: "Jane Doe", Email: "jane@example.com"}

	t.Run("fresh stamp appends byline", func(t *testing.T) {
		entry := "### 2026-08-21\n- did the thing"
		got := StampShared(entry, jane)
		want := "### 2026-08-21\n- did the thing\n— Jane Doe <jane@example.com>"
		if got != want {
			t.Errorf("StampShared() = %q, want %q", got, want)
		}
	})

	t.Run("re-stamp is a no-op", func(t *testing.T) {
		entry := "### 2026-08-21\n- did the thing"
		once := StampShared(entry, jane)
		twice := StampShared(once, jane)
		if once != twice {
			t.Errorf("StampShared not idempotent: once=%q twice=%q", once, twice)
		}
	})

	t.Run("re-stamp with trailing whitespace is still a no-op", func(t *testing.T) {
		entry := "### 2026-08-21\n- did the thing"
		once := StampShared(entry, jane)
		withTrailingWS := once + "\n\n  \n"
		twice := StampShared(withTrailingWS, jane)
		if once != twice {
			t.Errorf("StampShared not idempotent across trailing whitespace: once=%q twice=%q", once, twice)
		}
	})

	t.Run("empty author adds nothing, only trims trailing whitespace", func(t *testing.T) {
		entry := "### 2026-08-21\n- did the thing\n\n  "
		got := StampShared(entry, Author{})
		want := "### 2026-08-21\n- did the thing"
		if got != want {
			t.Errorf("StampShared() = %q, want %q", got, want)
		}
	})

	t.Run("empty author is idempotent too", func(t *testing.T) {
		entry := "### 2026-08-21\n- did the thing"
		once := StampShared(entry, Author{})
		twice := StampShared(once, Author{})
		if once != twice {
			t.Errorf("StampShared not idempotent for zero author: once=%q twice=%q", once, twice)
		}
	})
}
