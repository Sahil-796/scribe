package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/hook"
	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/queue"
	"github.com/Sahil-796/scribe/internal/scribe"
	"github.com/Sahil-796/scribe/internal/writer"
)

// newDoctorWriter constructs the writer connector the "writer answers a
// trivial prompt" check drives. A package-level var, not a direct
// writer.New reference — the same seam init.go's newWriter and run.go's
// newWriterForRun use, named distinctly from both so each command's tests
// can redirect its own seam independently. No test of doctor.go may ever
// let this reach a real opencode/codex/claude process: doctor_test.go
// replaces it with a fake scribe.Writer.
var newDoctorWriter = writer.New

// doctorWriterTimeout bounds the "writer answers a trivial prompt" check.
// Shorter than writer.Config's normal multi-minute ceiling (internal/writer's
// defaultTimeout), because this check exists to catch a wrong model name or
// an agent hanging for want of an auto-approve flag, not to sit through a
// real run.
//
// It was 8s, reasoned from "doctor should be fast enough to run without
// thinking about it," and that number was simply wrong. Measured against a
// healthy opencode 1.18.15 on the default model, a trivial round trip —
// "reply with the single word OK" — takes 11.3s, most of it process
// startup and model latency that no configuration problem would change. So
// doctor failed working installs and told them to go check their config.
// A diagnostic that cries wolf is worse than no diagnostic: the first thing
// it teaches you is to ignore it.
//
// 45s is set from that measurement with room for a slower machine or model,
// and still catches the case it was built for — an agent waiting forever on
// an approval prompt never answers at all.
var doctorWriterTimeout = 45 * time.Second

// doctorTrivialPrompt is deliberately inert: it asks for a fixed word back
// and nothing else, so the check only ever exercises "can this agent run
// unattended and produce output," not the actual writing quality that
// docs/PLAN.md's phase 00 already settled separately.
const doctorTrivialPrompt = `Reply with exactly one word: ok`

// doctorStatus is a single check's outcome. There is no separate "warning"
// tier — docs/phases/04-config-and-safety.md is explicit that doctor's
// output must be actionable, not hedged, so every check is either
// something the user can trust or something they have a fix for.
type doctorStatus int

const (
	doctorPass doctorStatus = iota
	doctorFail
	doctorSkip // precondition not met (e.g. repo never initialised) — not itself a failure
)

// doctorCheck is one named, printable line of `scribe doctor` output.
type doctorCheck struct {
	name   string
	status doctorStatus
	detail string // shown for fail (the fix) and skip (why); empty for pass
}

func (c doctorCheck) String() string {
	switch c.status {
	case doctorPass:
		if c.detail != "" {
			// A passing check may still have something worth seeing — the
			// writer round trip reports how long it took, which is the
			// early warning for a setup that's healthy but drifting
			// towards the timeout.
			return fmt.Sprintf("[ OK ] %s — %s", c.name, c.detail)
		}
		return fmt.Sprintf("[ OK ] %s", c.name)
	case doctorSkip:
		return fmt.Sprintf("[SKIP] %s — %s", c.name, c.detail)
	default:
		return fmt.Sprintf("[FAIL] %s — %s", c.name, c.detail)
	}
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run a list of named checks against this repo's scribe setup, each with a pass/fail and, on failure, the fix",
		Long: `scribe doctor runs a fixed list of named checks — is this a git repo, does
.scribe/config.json parse and name a registered writer agent, is the Stop
hook installed and pointing at a binary that still exists, are the four
docs present, does the configured writer resolve on $PATH and answer a
trivial prompt within a few seconds, is the queue/lock wedged, is scribe
on rather than paused — and reports each as a pass or a fail with the
specific fix, never just "something is wrong."

It also prints this repo's recent Stop hook failures (docs/findings/OPEN-ITEMS.md,
item 11), the same log "scribe status" summarizes the newest entry of.

Exits non-zero if any check fails, zero if every check passes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			printRecentHookFailures(cmd.OutOrStdout())
			return runDoctor(cmd.OutOrStdout())
		},
	}
}

// runDoctor is doctor's one entry point, kept as a plain function (not
// inlined in RunE) so tests can call it directly against a chosen cwd
// without going through cobra.
func runDoctor(out io.Writer) error {
	checks := doctorChecks()
	failed := 0
	for _, c := range checks {
		fmt.Fprintln(out, c.String())
		if c.status == doctorFail {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("scribe doctor: %d check(s) failed", failed)
	}
	return nil
}

// doctorChecks runs every check in order and returns the full list,
// regardless of how many fail — doctor's value is in showing the whole
// picture at once, not stopping at the first problem.
func doctorChecks() []doctorCheck {
	var checks []doctorCheck

	cwd, err := os.Getwd()
	if err != nil {
		return append(checks, doctorCheck{"inside a git repository", doctorFail, fmt.Sprintf("could not determine the current directory: %v", err)})
	}

	repoRoot, inGitRepo := findGitRoot(cwd)
	if !inGitRepo {
		return append(checks, doctorCheck{"inside a git repository", doctorFail, "scribe attaches to a git repo; cd into one and try again"})
	}
	checks = append(checks, doctorCheck{"inside a git repository", doctorPass, ""})

	cfg, cfgErr := install.LayeredConfig(repoRoot)
	initialised := cfgErr == nil
	checks = append(checks, doctorConfigCheck(cfgErr, cfg.Agent))

	if !initialised {
		reason := "repo not initialised — none of these apply until `scribe init` has run"
		checks = append(checks,
			doctorCheck{"Stop hook installed and points at a binary that exists", doctorSkip, reason},
			doctorCheck{"docs/scribe/ has all four docs", doctorSkip, reason},
			doctorCheck{"writer binary resolves on $PATH", doctorSkip, reason},
			doctorCheck{"writer answers a trivial prompt", doctorSkip, reason},
			// Unlike the others, the queue/lock check doesn't depend on
			// initialisation — a stray .scribe/lock can exist even in a
			// repo that later got un-initialised by hand, or one where
			// init crashed partway — so it still runs for real here
			// rather than being skipped alongside everything else.
			doctorQueueCheck(repoRoot),
			doctorCheck{"scribe is on, not paused", doctorSkip, reason},
		)
		return checks
	}

	checks = append(checks, doctorHookCheck(repoRoot))
	checks = append(checks, doctorDocsCheck(repoRoot, cfg))

	w, writerErr := newDoctorWriter(writer.Config{Agent: cfg.Agent, Model: cfg.Model, Command: cfg.Command, Args: cfg.Args, Timeout: doctorWriterTimeout})
	checks = append(checks, doctorPathCheck(cfg, writerErr))
	checks = append(checks, doctorTrivialPromptCheck(w, writerErr))

	checks = append(checks, doctorQueueCheck(repoRoot))
	checks = append(checks, doctorPauseCheck(cfg))

	return checks
}

// doctorConfigCheck is ".scribe/config.json exists, parses, and its agent
// is a registered writer connector." install.LayeredConfig folds in
// ~/.config/scribe/config.json (phase 04's global defaults) before
// defaulting, so this reports on the config that would actually be used,
// not just what's on disk in this one repo.
//
// The registry check builds a writer with only cfg.Agent set (no model, no
// timeout) for every agent except "custom", which legitimately fails to
// build from that bare Config — its real command lives in
// writer.Config.Command/Args, which install.Config has nowhere to store —
// so a construction error there would be about the missing command, not
// about "custom" being unregistered. "custom" is registered by
// construction (it's this check's one hardcoded exception); every other
// name either builds cleanly or reports writer.New's own "unknown agent"
// error, which is what this check surfaces as the fix.
func doctorConfigCheck(cfgErr error, agent string) doctorCheck {
	name := ".scribe/config.json exists, parses, and names a registered writer agent"
	if cfgErr != nil {
		if errors.Is(cfgErr, install.ErrNotInitialised) {
			return doctorCheck{name, doctorFail, "no .scribe/config.json here — run `scribe init`"}
		}
		return doctorCheck{name, doctorFail, fmt.Sprintf("%v — fix or remove .scribe/config.json and re-run `scribe init`", cfgErr)}
	}
	if agent != "custom" {
		if _, err := newDoctorWriter(writer.Config{Agent: agent}); err != nil {
			return doctorCheck{name, doctorFail, fmt.Sprintf("%v — set \"agent\" in .scribe/config.json to a connector scribe ships", err)}
		}
	}
	return doctorCheck{name, doctorPass, ""}
}

// doctorHookCheck is "the Stop hook is installed in .claude/settings.json
// and points at a binary path that still exists." A scribe binary that was
// moved or rebuilt somewhere else after `scribe init` ran is a silent,
// total failure — the hook still "runs," Claude Code sees a nonzero exit
// or nothing at all, and nothing else in the system surfaces it — which is
// exactly why this check exists rather than stopping at
// install.StopHookInstalled's plain true/false.
func doctorHookCheck(repoRoot string) doctorCheck {
	name := "Stop hook installed and points at a binary that exists"
	installed, err := install.StopHookInstalled(repoRoot)
	if err != nil {
		return doctorCheck{name, doctorFail, fmt.Sprintf("could not read .claude/settings.json: %v", err)}
	}
	if !installed {
		return doctorCheck{name, doctorFail, "no scribe Stop hook in .claude/settings.json — run `scribe init --apply`"}
	}

	binPath, ok := installedHookBinPath(repoRoot)
	if !ok {
		// Installed, but this doctor build couldn't parse out which binary
		// it points at — not itself proof of a problem, so this doesn't
		// fail the check; the hook-installed test above already covers
		// "is there a scribe hook at all."
		return doctorCheck{name, doctorPass, ""}
	}
	if info, statErr := os.Stat(binPath); statErr != nil || info.IsDir() {
		return doctorCheck{name, doctorFail, fmt.Sprintf("the installed hook points at %q, which no longer exists — re-run `scribe init --apply` (or `scribe init` again with --apply) to reinstall it at the current binary path", binPath)}
	}
	return doctorCheck{name, doctorPass, ""}
}

// installedHookBinPath best-effort extracts the scribe binary path from
// .claude/settings.json's installed Stop hook command. The on-disk shape
// (hooks.Stop[*].hooks[*].command, a string of the form `"<bin>" hook`) is
// install.go's own documented format — see subcommandCommand there — read
// directly here rather than through a new install.go export, the same
// read-only-against-the-documented-shape approach cmd/scribe/status.go
// already uses for the queue lock and pending files.
func installedHookBinPath(repoRoot string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".claude", "settings.json"))
	if err != nil {
		return "", false
	}
	var doc struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", false
	}
	for _, g := range doc.Hooks.Stop {
		for _, h := range g.Hooks {
			if bin, ok := scribeHookBinFromCommand(h.Command); ok {
				return bin, true
			}
		}
	}
	return "", false
}

// scribeHookBinFromCommand parses `"<bin>" hook` (install.go's
// subcommandCommand format) back into <bin>. Loose about quoting for the
// same reason install.go's own detection is loose: a hand-edited entry
// might not be perfectly re-quoted.
func scribeHookBinFromCommand(cmd string) (string, bool) {
	trimmed := strings.TrimSpace(cmd)
	if !strings.HasSuffix(trimmed, "hook") {
		return "", false
	}
	bin := strings.TrimSpace(strings.TrimSuffix(trimmed, "hook"))
	bin = strings.Trim(bin, `"'`)
	if bin == "" || !strings.Contains(filepath.Base(bin), "scribe") {
		return "", false
	}
	return bin, true
}

// doctorDocsCheck is "docs/scribe/ exists and the four docs are present."
func doctorDocsCheck(repoRoot string, cfg install.Config) doctorCheck {
	name := "docs/scribe/ has all four docs"
	docsDir := cfg.DocsDir
	if docsDir == "" {
		docsDir = scribe.DocsDir
	}
	var missing []string
	for _, doc := range scribe.AllDocs {
		p := filepath.Join(repoRoot, docsDir, string(doc))
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, string(doc))
		}
	}
	if len(missing) > 0 {
		return doctorCheck{name, doctorFail, fmt.Sprintf("missing %s under %s — run `scribe init --apply` (or `scribe run` if init already ran and this repo just has no history yet)", strings.Join(missing, ", "), docsDir)}
	}
	return doctorCheck{name, doctorPass, ""}
}

// doctorPathCheck is "the writer binary resolves on $PATH." install.Config
// doesn't carry a command override (that's writer.Config.Command, filled
// in at construction time, not persisted) so this only knows how to find
// the binary for connectors with a fixed default name; "custom" has no
// fixed name to look for and is reported as skipped rather than guessed at
// — the trivial-prompt check below still exercises it for real.
func doctorPathCheck(cfg install.Config, writerErr error) doctorCheck {
	name := "writer binary resolves on $PATH"
	if writerErr != nil {
		return doctorCheck{name, doctorFail, fmt.Sprintf("could not construct the %q writer: %v", cfg.Agent, writerErr)}
	}

	binName, ok := defaultWriterBinName(cfg.Agent)
	if !ok {
		return doctorCheck{name, doctorSkip, fmt.Sprintf("agent %q has no fixed binary name to look up — covered by the trivial-prompt check instead", cfg.Agent)}
	}
	if _, err := exec.LookPath(binName); err != nil {
		return doctorCheck{name, doctorFail, fmt.Sprintf("%q is not on $PATH: %v — install it or point PATH at it", binName, err)}
	}
	return doctorCheck{name, doctorPass, ""}
}

// defaultWriterBinName mirrors internal/writer's per-connector default
// binary name for connectors that have a fixed one. Kept here rather than
// exported from internal/writer (not this unit's file to change) — it's a
// one-entry table that only needs to agree with writer.newOpencodeWriter's
// default, not reach into it.
func defaultWriterBinName(agent string) (string, bool) {
	switch agent {
	case "opencode":
		return "opencode", true
	default:
		return "", false
	}
}

// doctorTrivialPromptCheck is "the writer actually answers a trivial
// prompt within a short timeout" — the check that catches a wrong model
// name or an agent hanging for want of an auto-approve flag, per
// docs/phases/04-config-and-safety.md. w is nil when writerErr != nil (the
// config check above already failed in that case); this check then just
// reports the same construction error rather than a nil-pointer panic.
func doctorTrivialPromptCheck(w scribe.Writer, writerErr error) doctorCheck {
	name := "writer answers a trivial prompt"
	if writerErr != nil {
		return doctorCheck{name, doctorFail, fmt.Sprintf("could not construct the writer: %v", writerErr)}
	}
	// Timed, and the duration is reported on success. This is the one
	// check that makes a network round trip, so it dominates doctor's
	// runtime — and a healthy-but-slow writer is worth seeing before it
	// starts timing out for real.
	started := time.Now()
	if _, err := w.Run(doctorTrivialPrompt); err != nil {
		return doctorCheck{name, doctorFail, fmt.Sprintf("%v — check the agent/model in .scribe/config.json (or ~/.config/scribe/config.json) and that the agent doesn't need an auto-approve flag", err)}
	}
	return doctorCheck{name, doctorPass, fmt.Sprintf("answered in %s", time.Since(started).Round(100*time.Millisecond))}
}

// doctorLockInfo mirrors internal/queue's unexported lockInfo JSON shape
// (pid, hostname, started_at), the same duplication cmd/scribe/status.go's
// statusLockInfo already makes for the same reason: internal/queue's
// package doc documents the lock file's on-disk format explicitly ("an
// O_EXCL lockfile recording the holder's pid, hostname and start time"),
// so this reads the documented shape rather than reaching into the
// package's internals, and internal/queue itself is off-limits to edit in
// this phase (its own package doc says so).
type doctorLockInfo struct {
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}

// doctorQueueCheck is "the queue/lock aren't wedged: a lock held by a dead
// pid, or a pending flag set with nothing draining." It is read-only —
// unlike internal/queue.TryLock, which would reclaim a stale lock as a
// side effect of merely checking it, this only ever reports what it finds.
// TryLock's own reclaim-on-stale behavior means a truly wedged lock heals
// itself the next time anything actually tries to run; doctor's job is
// telling the user "this instant" is wedged before that next attempt
// happens, not fixing it.
func doctorQueueCheck(repoRoot string) doctorCheck {
	name := "queue/lock not wedged"
	stateDir := filepath.Join(repoRoot, scribe.StateDir)

	lockPath := filepath.Join(stateDir, "lock")
	if data, err := os.ReadFile(lockPath); err == nil {
		var info doctorLockInfo
		if json.Unmarshal(data, &info) == nil {
			dead := !processAliveOnThisHost(info)
			stale := time.Since(info.StartedAt) > queue.StaleLockAge
			if dead || stale {
				reason := "stale"
				if dead {
					reason = fmt.Sprintf("held by pid %d, which is not running", info.PID)
				}
				return doctorCheck{name, doctorFail, fmt.Sprintf("lock is %s (since %s) — remove %s and re-run `scribe run`", reason, info.StartedAt.Format(time.RFC3339), lockPath)}
			}
		}
		// Lock exists, parses, and looks live: a run is genuinely in
		// progress, not wedged.
		return doctorCheck{name, doctorPass, ""}
	}

	pendingPath := filepath.Join(stateDir, "pending")
	if info, err := os.Stat(pendingPath); err == nil {
		// No lock held, but the pending flag is set: nothing is currently
		// draining it. A flag that was just set (the run that set it is
		// about to pick it up, or another process is about to grab the
		// lock) isn't wedged, only one that's been sitting for longer
		// than a normal run ever takes — StaleLockAge is reused as that
		// threshold since it already expresses "how long is too long for
		// this repo's queue machinery to sit untouched."
		if time.Since(info.ModTime()) > queue.StaleLockAge {
			return doctorCheck{name, doctorFail, fmt.Sprintf("pending flag has been set since %s with no lock held and nothing draining it — run `scribe run`", info.ModTime().Format(time.RFC3339))}
		}
	}
	return doctorCheck{name, doctorPass, ""}
}

// processAliveOnThisHost mirrors internal/queue's own processAlive/isStale
// liveness probe (signal-0 kill: ESRCH means no such process, anything
// else means alive or not ours to say). Only trusted when info.Hostname
// matches this machine — a lock written on a different host has a pid that
// means nothing here, so it's judged on age alone (see the stale check
// alongside this call).
func processAliveOnThisHost(info doctorLockInfo) bool {
	h, err := os.Hostname()
	if err != nil || h != info.Hostname || info.PID <= 0 {
		return true // can't disprove liveness; don't call it dead on weak evidence
	}
	err = syscall.Kill(info.PID, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}

// doctorPauseCheck is "scribe is on rather than paused." Enabled and
// paused are different axes (internal/install/config.go's Config doc
// comment): a config that exists but was hand-edited to Enabled=false
// hasn't been "paused" via `scribe off`, so it gets its own message rather
// than being folded into the IsPaused branch.
func doctorPauseCheck(cfg install.Config) doctorCheck {
	name := "scribe is on, not paused"
	if !cfg.Enabled {
		return doctorCheck{name, doctorFail, "config has \"enabled\": false — re-run `scribe init` or set it back to true"}
	}
	now := time.Now()
	if cfg.IsPaused(now) {
		switch {
		case cfg.Pause != nil && cfg.Pause.Stay:
			return doctorCheck{name, doctorFail, "paused indefinitely (`scribe off --stay`) — run `scribe on` to resume"}
		case cfg.Pause != nil && cfg.Pause.Until != nil:
			return doctorCheck{name, doctorFail, fmt.Sprintf("paused until %s — run `scribe on` to resume now", cfg.Pause.Until.Format(time.RFC3339))}
		default:
			return doctorCheck{name, doctorFail, "paused — run `scribe on` to resume"}
		}
	}
	return doctorCheck{name, doctorPass, ""}
}

// printRecentHookFailures writes any entries from this repo's bounded
// hook-failures log (see internal/hook.RecentFailures) to out. It is
// deliberately silent — no error, no "nothing to show" message — when
// there's no repo, no log, or a log that fails to read, since the checks
// below already say plainly whether this repo is initialised at all.
func printRecentHookFailures(out io.Writer) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	root, ok := hook.FindRepoRoot(cwd)
	if !ok {
		return
	}
	entries, err := hook.RecentFailures(root)
	if err != nil || len(entries) == 0 {
		return
	}

	fmt.Fprintf(out, "Recent Stop hook failures for %s (oldest first):\n", root)
	for _, e := range entries {
		session := e.SessionID
		if session == "" {
			session = "-"
		}
		fmt.Fprintf(out, "  %s  session=%s  %s\n", e.Time.Format("2006-01-02T15:04:05Z07:00"), session, e.Reason)
	}
	fmt.Fprintln(out)
}
