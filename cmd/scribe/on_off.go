package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahil-796/scribe/internal/install"
)

func newOnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "on",
		Short: "Resume scribe for this repo now",
		Long: `scribe on resumes scribe for this repo immediately, undoing a prior
"scribe off".

Off is the default everywhere until "scribe init" runs. In a repo that was
never initialised, "scribe on" says so and points at "scribe init" instead
of writing a config — turning it "on" here would half-onboard a repo that
never asked to be onboarded, and init is the only command allowed to seed
docs and install the hook.`,
		Args: cobra.NoArgs,
		RunE: runOn,
	}
}

func newOffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "off",
		Short: "Pause scribe for this repo without uninstalling",
		Long: `scribe off pauses scribe for this repo without uninstalling the hook.

By default the pause expires at the end of the day, because a permanent
pause you forget about fails the same way forgetting to turn scribe on
does. Pass --stay to pause indefinitely; "scribe on" resumes at any time.

While paused, "scribe hook" exits quietly without enqueueing or spawning
a worker — a pause that still queued work would just be a delay, since the
queue would drain the moment the pause lapsed.`,
		Args: cobra.NoArgs,
		RunE: runOff,
	}
	cmd.Flags().Bool("stay", false, "pause indefinitely instead of expiring at end of day")
	return cmd
}

// pauseRepoRootFor resolves the git root for this unit's on/off commands.
// Both are per-repo, like init, and share init's rule: scribe attaches to a
// git repo, not an arbitrary directory. findGitRoot lives in run.go, owned
// by nobody in this phase, and is reused as-is rather than duplicated.
func pauseRepoRootFor(cmdName string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%s: %w", cmdName, err)
	}
	root, ok := findGitRoot(cwd)
	if !ok {
		return "", fmt.Errorf("%s: %s is not inside a git repository", cmdName, cwd)
	}
	return root, nil
}

func runOff(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	stay, _ := cmd.Flags().GetBool("stay")

	root, err := pauseRepoRootFor("scribe off")
	if err != nil {
		return err
	}

	cfg, err := install.ReadConfig(root)
	if err != nil {
		if errors.Is(err, install.ErrNotInitialised) {
			fmt.Fprintf(out, "scribe was never turned on for %s — nothing to pause. Run \"scribe init\" first.\n", root)
			return nil
		}
		return fmt.Errorf("scribe off: reading config: %w", err)
	}

	now := time.Now()
	pause := &install.Pause{Since: now, Stay: stay}
	if !stay {
		until := install.EndOfLocalDay(now)
		pause.Until = &until
	}
	cfg.Pause = pause

	if err := install.WriteConfig(root, cfg); err != nil {
		return fmt.Errorf("scribe off: %w", err)
	}

	if stay {
		fmt.Fprintf(out, "scribe is off for %s, indefinitely — \"scribe on\" resumes it\n", root)
	} else {
		fmt.Fprintf(out, "scribe is off for %s until %s — \"scribe on\" resumes sooner\n", root, pause.Until.Format("15:04 today"))
	}
	return nil
}

func runOn(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	root, err := pauseRepoRootFor("scribe on")
	if err != nil {
		return err
	}

	cfg, err := install.ReadConfig(root)
	if err != nil {
		if errors.Is(err, install.ErrNotInitialised) {
			fmt.Fprintf(out, "scribe was never turned on for %s. Run \"scribe init\" to set it up — \"scribe on\" only resumes an existing pause, it doesn't onboard a repo.\n", root)
			return nil
		}
		return fmt.Errorf("scribe on: reading config: %w", err)
	}

	if !cfg.IsPaused(time.Now()) {
		fmt.Fprintf(out, "scribe is already on for %s\n", root)
		return nil
	}

	cfg.Pause = nil
	if err := install.WriteConfig(root, cfg); err != nil {
		return fmt.Errorf("scribe on: %w", err)
	}

	fmt.Fprintf(out, "scribe is on for %s\n", root)
	return nil
}
