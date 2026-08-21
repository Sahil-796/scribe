package redact

import "strings"

import "testing"

// applySpans is where a leak would hide if two patterns ever produced
// overlapping-but-not-nested matches. The regexes above may or may not be
// able to generate that pair today — the point of testing applySpans
// directly, with hand-built spans, is that the answer stops mattering.
func TestApplySpans_StraddlingSpanDoesNotLeakItsTail(t *testing.T) {
	// "AAAA SECRETTAIL" — one span covers "AAAA SEC", a second starts
	// inside it and runs to the end. The second must not be dropped: its
	// tail, "RETTAIL", is content the first placeholder never covered.
	text := "AAAA SECRETTAIL"
	spans := []span{
		{start: 0, end: 8, label: "first"},
		{start: 5, end: 15, label: "second"},
	}

	got := applySpans(text, spans)

	if strings.Contains(got, "RETTAIL") {
		t.Fatalf("applySpans leaked the straddling span's tail: %q", got)
	}
	if strings.Contains(got, "SECRET") {
		t.Errorf("applySpans leaked redacted content: %q", got)
	}
}

func TestApplySpans_NestedSpanIsDropped(t *testing.T) {
	text := "prefix SECRETVALUE suffix"
	spans := []span{
		{start: 7, end: 18, label: "outer"},
		{start: 10, end: 14, label: "inner"},
	}

	got := applySpans(text, spans)

	if want := "prefix [redacted:outer] suffix"; got != want {
		t.Errorf("applySpans = %q, want %q", got, want)
	}
}

// The end-to-end property the package promises, stated once against the
// real Redact path: whatever the pattern set does, a known secret must not
// survive anywhere in the output.
func TestRedact_KnownSecretsNeverSurvive(t *testing.T) {
	r := New([]string{"api_key", "token", "password", "secret"}, nil)

	secrets := []string{
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz012345",
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"AKIAIOSFODNN7EXAMPLE",
		"hunter2correcthorse",
	}
	texts := []string{
		`the call failed with {"api_key": "` + secrets[0] + `"} in the body`,
		"export GITHUB_TOKEN=" + secrets[1] + " && gh auth status",
		"AWS_ACCESS_KEY_ID=" + secrets[2],
		"ran it with --password " + secrets[3] + " and it worked",
		"password=" + secrets[3] + " " + secrets[0],
	}

	for _, text := range texts {
		got := r.Redact(text)
		for _, s := range secrets {
			if strings.Contains(got, s) {
				t.Errorf("Redact(%q) leaked %q:\n  got %q", text, s, got)
			}
		}
	}
}

// The counterweight: prose that merely names a key, with no value attached,
// is the journal's actual content and must survive intact. A redactor that
// eats this is one nobody will leave switched on.
func TestRedact_ProseSurvives(t *testing.T) {
	r := New([]string{"api_key", "token", "password", "secret"}, nil)

	prose := []string{
		"the api_key was wrong so I regenerated it",
		"turns out the token had expired, which is why every request 401'd",
		"we decided the password reset flow needs rate limiting",
		"the secret is that there is no secret",
	}
	for _, p := range prose {
		if got := r.Redact(p); got != p {
			t.Errorf("Redact ate prose:\n  in  %q\n  out %q", p, got)
		}
	}
}
