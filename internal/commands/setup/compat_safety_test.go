package setup

import "testing"

// compat.safety_modes: doctor subcommands and orchestrator-api verbs.
func TestSafetyModes(t *testing.T) {
	for _, c := range []struct {
		argv []string
		safe bool
	}{
		{[]string{"doctor"}, true},
		{[]string{"doctor", "--fix"}, false},
		{[]string{"doctor", "skills"}, true},
		{[]string{"doctor", "skills", "--fix"}, false},
		{[]string{"doctor", "skills", "--json"}, true},
		{[]string{"doctor", "command-files", "--fix"}, true},
		{[]string{"doctor", "identity", "--json"}, true},
		{[]string{"doctor", "state-roots"}, true},
		{[]string{"doctor", "shim-registry"}, true},
		{[]string{"doctor", "mission-state", "--fix"}, false},
		{[]string{"doctor", "mission-state", "--audit"}, true},
		{[]string{"doctor", "sparse-checkout", "--fix"}, false},
		{[]string{"doctor", "sparse-checkout"}, true},
		{[]string{"doctor", "tool-surfaces"}, false},
		{[]string{"orchestrator-api", "--json"}, true},
		{[]string{"orchestrator-api", "start-review"}, false},
		{[]string{"migrate", "backfill-runtime-state", "--dry-run"}, true},
		{[]string{"migrate", "backfill-runtime-state"}, false},
		{[]string{"agent", "mission", "setup-plan"}, false},
	} {
		if got := invocationFromArgv(c.argv).Safe(); got != c.safe {
			t.Errorf("%v: safe=%v, want %v", c.argv, got, c.safe)
		}
	}
}
