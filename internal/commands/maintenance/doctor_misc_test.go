package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffDoctorRunIndexProvenanceCutover(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "run-index"},
		step{"doctor", "run-index", "--json"},
		step{"doctor", "provenance"},
		step{"doctor", "provenance", "--json"},
		step{"doctor", "cutover"},
		step{"doctor", "cutover", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/runtime/feature-runs.json", `{"m1": {"run_dir": "/abs/elsewhere/run1", "run_id": "r1"}, "m2": {"run_dir": ".kittify/runtime/runs/r2"}, "m3": {"run_dir": "C:\\runs\\r3"}}`)
	})
	p.Both(runtimeCorpus)
	compareSteps(t, p, "", "",
		step{"doctor", "run-index"},
		step{"doctor", "run-index", "--json"},
		step{"doctor", "cutover"},
		step{"doctor", "cutover", "--json"},
	)
}
