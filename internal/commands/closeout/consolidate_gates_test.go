package closeout_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// setGatePolicy appends a policy section to .kittify/config.yaml and makes
// the two lanes parallel with overlapping parent directories (risk 0.12).
func setGatePolicy(t *testing.T, p *testkit.Pair, mode string) {
	p.Both(func(s *testkit.Sandbox) {
		cfg := s.ReadFile(".kittify/config.yaml")
		if i := strings.Index(cfg, "\npolicy:"); i >= 0 {
			cfg = cfg[:i+1]
		}
		cfg = strings.TrimRight(cfg, "\n") + "\npolicy:\n  merge_gates:\n    mode: " + mode + "\n  risk:\n    threshold: 0.1\n"
		s.WriteFile(".kittify/config.yaml", cfg)
		lj := filepath.Join("kitty-specs", slugOf(t, s), "lanes.json")
		m, err := jsonx.DecodeObject([]byte(s.ReadFile(lj)))
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range m.GetList("lanes") {
			lo := l.(*jsonx.Object)
			if lo.GetString("lane_id") == "lane-planning" {
				continue
			}
			lo.Set("parallel_group", 0)
			lo.Set("write_scope", []any{[]string{"src/pkg/a.py", "src/pkg/b.py"}[i%2]})
		}
		s.WriteFile(lj, jsonx.Dumps(m, jsonx.Options{Indent: 2, EnsureASCII: true})+"\n")
		s.Git("add", ".kittify/config.yaml", lj)
		s.Git("commit", "-q", "-m", "gate policy "+mode)
	})
}

// TestDiffConsolidateGates evaluates the merge gates for real: a blocking
// risk gate refuses the merge before any mutation; in warn mode the same
// finding is reported and the merge proceeds.
func TestDiffConsolidateGates(t *testing.T) {
	p := acceptedPair(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	setGatePolicy(t, p, "block")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
	setGatePolicy(t, p, "warn")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
}
