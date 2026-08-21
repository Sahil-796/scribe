package main

import (
	"fmt"
	"strings"
)

// This file implements the line-based diff `scribe diff` renders. Per the
// phase 04 spec, this repo's entire dependency list is cobra + huh and it
// stays that way, so a unified diff — the one third-party-shaped piece of
// this command — is a small classic LCS instead of a new dependency. The
// docs it runs over are capped at DefaultHistoryCap/DefaultStateCap (32KB /
// 16KB, internal/docs/docs.go), a few hundred lines at most, so the O(n*m)
// table below is trivial in practice; there's no need for anything
// smarter.

// diffLine is one line of a rendered unified diff: unchanged (' '),
// removed ('-'), or added ('+') — the same three markers `diff -u` uses.
type diffLine struct {
	kind byte
	text string
}

// splitDiffLines splits s into lines the way a text editor would show them:
// no trailing empty element for a final "\n", so two strings differing only
// in a trailing newline diff as identical rather than as one extra blank
// line changing.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	return lines
}

// diffLCS runs the textbook longest-common-subsequence table over a and b,
// then walks it back into a line-by-line edit script. This is the same
// algorithm `diff` used before the smarter Myers variant became standard —
// worse asymptotically, simple enough to read and trust for docs this
// small, which is exactly the trade the phase 04 spec calls for.
func diffLCS(a, b []string) []diffLine {
	n, m := len(a), len(b)

	// dp[i][j] = length of the LCS of a[i:] and b[j:].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var out []diffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffLine{'-', a[i]})
			i++
		default:
			out = append(out, diffLine{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffLine{'-', a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffLine{'+', b[j]})
	}
	return out
}

// renderUnifiedDiff renders before→after as a unified diff for name (a doc
// filename, e.g. "CHANGELOG.md"). It always emits exactly one hunk covering
// the whole file rather than windowing down to a handful of context lines
// around each change — these docs are small (see the file comment above),
// so there's no readability cost, and it sidesteps having to get hunk
// splitting/merging right for a command whose whole job is "be trustworthy
// when you don't trust the automated version."
func renderUnifiedDiff(name, before, after string) string {
	a := splitDiffLines(before)
	b := splitDiffLines(after)
	edits := diffLCS(a, b)

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s (before)\n", name)
	fmt.Fprintf(&out, "+++ %s (after)\n", name)
	fmt.Fprintf(&out, "@@ -1,%d +1,%d @@\n", len(a), len(b))
	for _, e := range edits {
		fmt.Fprintf(&out, "%c%s\n", e.kind, e.text)
	}
	return out.String()
}
