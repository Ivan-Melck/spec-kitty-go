package testkit

import "os"

// ColorEnv is the environment override SPEC_KITTY_TEST_COLOR selects for
// every sandbox command, so the differential suites can be rerun with
// colour on: "standard", "256" or "truecolor" force a terminal with that
// rich colour system; unset keeps NO_COLOR=1 / TERM=dumb.
func ColorEnv() []string {
	switch os.Getenv("SPEC_KITTY_TEST_COLOR") {
	case "standard":
		return []string{"NO_COLOR=", "FORCE_COLOR=1", "TERM=xterm", "COLORTERM="}
	case "256":
		return []string{"NO_COLOR=", "FORCE_COLOR=1", "TERM=xterm-256color", "COLORTERM="}
	case "truecolor":
		return []string{"NO_COLOR=", "FORCE_COLOR=1", "TERM=xterm", "COLORTERM=truecolor"}
	}
	return nil
}
