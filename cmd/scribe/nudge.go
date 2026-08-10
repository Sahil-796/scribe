package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/install"
	"github.com/Sahil-796/scribe/internal/nudge"
)

// nudgeEventName and nudgeSubcommand identify the hook internal/install's
// generalised InstallEventHook/RemoveEventHook/EventHookInstalled write and
// look for: a SessionStart hook invoking "<scribe binary> nudge".
const (
	nudgeEventName  = "SessionStart"
	nudgeSubcommand = "nudge"
)

// globalClaudeSettingsPathFunc resolves the user's global Claude Code
// settings file. A package-level var, not a bare call to os.UserHomeDir
// inline, so nudge_test.go can redirect it without ever touching the
// developer's real ~/.claude/settings.json — the phase 04 spec's hardest
// safety rule for this unit.
var globalClaudeSettingsPathFunc = defaultGlobalClaudeSettingsPath

func defaultGlobalClaudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return home + "/.claude/settings.json", nil
}

// newNudgeCmd is registered by the orchestrator (see this unit's report —
// cmd/scribe/root.go is owned by nobody in phase 04 and is wired by hand
// once every unit lands). It builds `scribe nudge`: hidden, because the
// no-flags form is an internal entrypoint invoked by a SessionStart hook,
// never typed by a human — the same shape as `scribe hook` (see hook.go).
//
// docs/phases/04-config-and-safety.md is explicit that this is opt-in via
// its own command, not smuggled into `scribe init`, and adds no new wizard
// question: phase 03 closed OPEN-ITEMS item 31 by *removing* a question,
// and a nudge is not worth reopening that.
func newNudgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "nudge",
		Short:  "Internal: what the global SessionStart hook calls; --install/--remove manage that hook",
		Hidden: true,
		Long: `scribe nudge, with no flags, is what a SessionStart hook installed in the
user's global Claude Code settings (~/.claude/settings.json) calls at the
start of every session, in every repo. It counts one more session against
the current repo and, only once that count reaches 6 *and* scribe was
never turned on there, prints one line:

    scribe isn't on here — 6 sessions so far. ` + "`scribe init`" + ` to catch up.

It never nags twice for the same repo, and stays silent everywhere scribe
is already on (docs/PLAN.md, "Pausing"). Like "scribe hook", it must never
block a Claude Code session, so every failure along the way — no git repo,
a state file that can't be read or written — is silent and this command
always exits 0.

--install writes that SessionStart hook, and --remove takes it back out.
Both act on the user's own global settings file, not any one repo's, so
neither runs without being asked explicitly: there is no prompt for this
in "scribe init", and no wizard question — you have to type "scribe nudge
--install" yourself.`,
		Args: cobra.NoArgs,
		RunE: runNudge,
	}
	cmd.Flags().Bool("install", false, "install the SessionStart hook in ~/.claude/settings.json")
	cmd.Flags().Bool("remove", false, "remove the SessionStart hook from ~/.claude/settings.json")
	return cmd
}

func runNudge(cmd *cobra.Command, _ []string) error {
	installFlag, _ := cmd.Flags().GetBool("install")
	removeFlag, _ := cmd.Flags().GetBool("remove")

	if installFlag && removeFlag {
		return errors.New("scribe nudge: --install and --remove are mutually exclusive")
	}
	if installFlag {
		return runNudgeInstall(cmd.OutOrStdout())
	}
	if removeFlag {
		return runNudgeRemove(cmd.OutOrStdout())
	}

	// The hidden hook path: never block, never fail. cwd is what Claude
	// Code's SessionStart hook runs with — the session's working
	// directory — so, unlike the interactive --install/--remove paths
	// below, this never reads stdin (a human running "scribe nudge" bare
	// in a terminal must not hang waiting for a payload that never
	// arrives; Claude Code, which does send one, gives us the same
	// answer via cwd anyway).
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	nudge.Run(cwd, time.Now(), cmd.OutOrStdout())
	return nil
}

func runNudgeInstall(out io.Writer) error {
	settingsPath, err := globalClaudeSettingsPathFunc()
	if err != nil {
		return fmt.Errorf("scribe nudge --install: %w", err)
	}
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("scribe nudge --install: locating scribe binary: %w", err)
	}

	res, err := install.InstallEventHook(settingsPath, nudgeEventName, nudgeSubcommand, binPath)
	if err != nil {
		return fmt.Errorf("scribe nudge --install: %w", err)
	}

	if res.AlreadyPresent {
		fmt.Fprintf(out, "Nudge hook already installed in %s\n", settingsPath)
		return nil
	}
	fmt.Fprintf(out, "Installed the nudge SessionStart hook in %s\n", settingsPath)
	if res.Backup != "" {
		fmt.Fprintf(out, "Backed up the previous file to %s\n", res.Backup)
	}
	fmt.Fprintln(out, "From your next Claude Code session on, any repo you work in for 6+ sessions without running \"scribe init\" gets a one-line reminder.")
	return nil
}

func runNudgeRemove(out io.Writer) error {
	settingsPath, err := globalClaudeSettingsPathFunc()
	if err != nil {
		return fmt.Errorf("scribe nudge --remove: %w", err)
	}

	res, err := install.RemoveEventHook(settingsPath, nudgeEventName, nudgeSubcommand)
	if err != nil {
		return fmt.Errorf("scribe nudge --remove: %w", err)
	}

	if !res.Removed {
		fmt.Fprintf(out, "Nudge hook wasn't installed in %s — nothing to remove.\n", settingsPath)
		return nil
	}
	fmt.Fprintf(out, "Removed the nudge SessionStart hook from %s\n", settingsPath)
	if res.Backup != "" {
		fmt.Fprintf(out, "Backed up the previous file to %s\n", res.Backup)
	}
	return nil
}
