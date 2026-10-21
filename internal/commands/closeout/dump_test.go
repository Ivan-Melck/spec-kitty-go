package closeout_test

import (
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDumpOracleScenario is a development aid (KITTY_DUMP=<scenario>): it
// runs a closeout scenario on the oracle side and logs files/git state.
func TestDumpOracleScenario(t *testing.T) {
	sc := os.Getenv("KITTY_DUMP")
	if sc == "" {
		t.Skip("set KITTY_DUMP")
	}
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	s := p.Oracle
	run := func(args ...string) {
		r := s.RunOracle(subst(args, slugOf(t, s))...)
		t.Logf("$ %s\n== exit %d ==\n%s%s", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
	}
	sh := func(args ...string) {
		r := s.Exec("", args[0], args[1:]...)
		t.Logf("$ %s\n%s%s", strings.Join(args, " "), r.Stdout, r.Stderr)
	}
	for _, step := range strings.Split(os.Getenv("KITTY_DUMP_STEPS"), ";") {
		f := strings.Fields(step)
		if len(f) == 0 {
			continue
		}
		if f[0] == "sh" {
			sh(f[1:]...)
		} else {
			run(f...)
		}
	}
	_ = testkit.Files
}
