package redact

import (
	"strings"
	"testing"
)

func defaultRedactor() *Redactor {
	return New(
		[]string{"api_key", "token", "password", "secret"},
		[]string{"**/.env*", "**/secrets/**"},
	)
}

// TestRedactKeyShapes pins down the transcript-prose shapes
// docs/phases/04-config-and-safety.md names explicitly: api_key must be
// caught in every one of these structural forms, regardless of casing or
// separator style.
func TestRedactKeyShapes(t *testing.T) {
	r := defaultRedactor()

	cases := []struct {
		name  string
		input string
	}{
		{"bare equals", `api_key=sk-abc123456789`},
		{"json quoted camelCase", `"apiKey": "sk-abc123456789"`},
		{"yaml colon upper snake", `API_KEY: sk-abc123456789`},
		{"export line", `export API_KEY=sk-abc123456789`},
		{"cli flag space", `--api-key sk-abc123456789`},
		{"cli flag equals", `--api-key=sk-abc123456789`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Redact(c.input)
			if strings.Contains(got, "sk-abc123456789") {
				t.Errorf("value leaked through: input %q -> %q", c.input, got)
			}
			if !strings.Contains(got, "[redacted:") {
				t.Errorf("expected a redaction placeholder: input %q -> %q", c.input, got)
			}
		})
	}
}

// TestRedactPreservesProse is the other half of the contract: a key name
// appearing in ordinary sentence prose, with no value structurally
// attached to it, must survive completely untouched. This is what lets
// JOURNAL.md talk about "the api_key" as a topic without every mention
// vanishing.
func TestRedactPreservesProse(t *testing.T) {
	r := defaultRedactor()

	cases := []string{
		"the api_key was wrong so I regenerated it",
		"we should never log the token in plaintext",
		"password reset flow now sends an email instead of a temp password",
		"the secret sauce here is the caching layer, nothing to do with credentials",
		"I renamed the ApiKey field on the struct to Credential",
	}

	for _, in := range cases {
		got := r.Redact(in)
		if got != in {
			t.Errorf("prose was altered:\n  in:  %q\n  out: %q", in, got)
		}
	}
}

// TestRedactHighSignalShapes checks the shapes that must be redacted
// regardless of any configured key — a Redactor built with an empty key
// list must still catch these, because they're secrets by construction.
func TestRedactHighSignalShapes(t *testing.T) {
	r := New(nil, nil)

	cases := []struct {
		name  string
		input string
		leak  string // substring that must not survive
	}{
		{"openai key", "here is the key sk-abcdefghijklmnopqrstuvwx", "sk-abcdefghijklmnopqrstuvwx"},
		{"anthropic key", "sk-ant-api03-abcdefghijklmnopqrstuvwx", "sk-ant-api03-abcdefghijklmnopqrstuvwx"},
		{"github classic pat", "ghp_abcdefghijklmnopqrstuvwxyzABCDEFGH", "ghp_abcdefghijklmnopqrstuvwxyzABCDEFGH"},
		{"github fine-grained pat", "github_pat_abcdefghijklmnopqrstuvwxyz1234567890", "github_pat_abcdefghijklmnopqrstuvwxyz1234567890"},
		{"aws access key", "AKIA1234567890ABCDEF", "AKIA1234567890ABCDEF"},
		{"bearer token", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abc.def", "eyJhbGciOiJIUzI1NiJ9.abc.def"},
		{"env style secretish name", "STRIPE_SECRET_KEY=sk_live_abcdefghijklmnop", "sk_live_abcdefghijklmnop"},
		{
			"pem block",
			"-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK...\n-----END RSA PRIVATE KEY-----",
			"MIIBOgIBAAJBAK",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Redact(c.input)
			if strings.Contains(got, c.leak) {
				t.Errorf("secret leaked through with no configured key: input %q -> %q", c.input, got)
			}
		})
	}
}

// TestRedactEnvLineIgnoresBenignNames makes sure the "regardless of key"
// .env-style rule doesn't turn into "redact every assignment ever" — a
// name with nothing secret-ish about it must survive.
func TestRedactEnvLineIgnoresBenignNames(t *testing.T) {
	r := New(nil, nil)
	in := "PORT=8080\nLOG_LEVEL=debug\nNAME=scribe"
	got := r.Redact(in)
	if got != in {
		t.Errorf("benign env lines were altered:\n  in:  %q\n  out: %q", in, got)
	}
}

// TestRedactSplitAcrossLine is the fuzz-ish case named in the phase spec:
// a secret whose key/value pair is broken across an awkward line, still
// inside one call to Redact (Redact operates on whatever string it's
// given — a caller splitting a secret's key onto one line and its value
// onto the next, inside the same rendered entry, is exactly the
// adversarial case worth pinning down).
func TestRedactSplitAcrossLine(t *testing.T) {
	r := defaultRedactor()

	in := "config dump:\napi_key=\n  sk-thisisasecretvaluethatislong\ntoken: ghp_thisisasecretvaluethatislong1234"
	got := r.Redact(in)

	for _, leak := range []string{
		"sk-thisisasecretvaluethatislong",
		"ghp_thisisasecretvaluethatislong1234",
	} {
		if strings.Contains(got, leak) {
			t.Errorf("secret leaked through split-line input: %q leaked in %q", leak, got)
		}
	}
}

// TestRedactPlaceholderNamesCanonicalKey checks the placeholder uses the
// key name as it was configured, not whatever spelling appeared in the
// text, so the same logical key always produces the same stable
// placeholder no matter which structural form matched it.
func TestRedactPlaceholderNamesCanonicalKey(t *testing.T) {
	r := defaultRedactor()
	got := r.Redact(`"apiKey": "sk-abc123456789"`)
	if !strings.Contains(got, "[redacted:api_key]") {
		t.Errorf("expected canonical placeholder [redacted:api_key], got %q", got)
	}
}

// TestRedactNeverIncludesOriginalValue is a broad sweep: for every case
// above that's expected to redact something, the exact secret value must
// never appear anywhere in the output, not even partially reconstructable
// via a substring the placeholder text happens to share.
func TestRedactNeverIncludesOriginalValue(t *testing.T) {
	r := defaultRedactor()
	secret := "sk-supersecretvalue1234567890"
	in := "api_key=" + secret
	got := r.Redact(in)
	if strings.Contains(got, secret) {
		t.Fatalf("original secret value present in redacted output: %q", got)
	}
}

func TestRedactNilPanics(t *testing.T) {
	var r *Redactor
	defer func() {
		if recover() == nil {
			t.Fatal("expected Redact on a nil Redactor to panic, it returned normally")
		}
	}()
	r.Redact("api_key=sk-abc123456789")
}

func TestIgnoreFileNilPanics(t *testing.T) {
	var r *Redactor
	defer func() {
		if recover() == nil {
			t.Fatal("expected IgnoreFile on a nil Redactor to panic, it returned normally")
		}
	}()
	r.IgnoreFile(".env")
}

// TestIgnoreFileGlobs exercises the ** handling path/filepath.Match can't
// do on its own: zero-or-more-segment matches at both the start and the
// middle of a pattern.
func TestIgnoreFileGlobs(t *testing.T) {
	r := defaultRedactor()

	ignored := []string{
		".env",
		".env.local",
		"nested/dir/.env",
		"a/b/secrets/x.txt",
		"secrets/x.txt",
		"secrets",
		"a/secrets/b/c/d.txt",
	}
	for _, p := range ignored {
		if !r.IgnoreFile(p) {
			t.Errorf("expected %q to be ignored, it was not", p)
		}
	}

	kept := []string{
		"main.go",
		"README.md",
		"config/settings.json",
		"envfile.go", // must not match "**/.env*" via substring
		"vault.txt",
	}
	for _, p := range kept {
		if r.IgnoreFile(p) {
			t.Errorf("expected %q to be kept, it was ignored", p)
		}
	}
}

func TestIgnoreFileEmptyGlobsIgnoresNothing(t *testing.T) {
	r := New([]string{"api_key"}, nil)
	if r.IgnoreFile(".env") {
		t.Error("expected no ignore globs to mean nothing is ignored")
	}
}
