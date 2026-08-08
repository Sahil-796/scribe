package main

import "fmt"

// notImplemented builds the error every stub command in this file returns.
// It's not a fake success and not a silent no-op — it says plainly which
// phase the command belongs to, per docs/PLAN.md's Phases section.
func notImplemented(cmdName, phase string) error {
	return fmt.Errorf("%s: not implemented until phase %s — see docs/PLAN.md", cmdName, phase)
}
