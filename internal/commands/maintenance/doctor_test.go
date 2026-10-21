package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var doctorLeaves = []string{"shim-registry", "contracts", "identity", "topology", "workspaces", "sparse-checkout", "run-index", "provenance", "cutover", "review-cycle-reconcile", "decisions", "mission-state", "coordination"}

const shimRegistry = `shims:
- legacy_path: specify_cli.old_mod
  canonical_import: specify_cli.new_mod
  introduced_in_release: 1.0.0
  removal_target_release: 2.0.0
  tracker_issue: '#12'
  grandfathered: false
- legacy_path: specify_cli.gone
  canonical_import: [specify_cli.a, specify_cli.b]
  introduced_in_release: 1.0.0
  removal_target_release: 9.0.0
  tracker_issue: https://example.com/1
  grandfathered: false
- legacy_path: specify_cli.later
  canonical_import: specify_cli.c
  introduced_in_release: 1.0.0
  removal_target_release: 9.0.0
  tracker_issue: '#3'
  grandfathered: false
- legacy_path: specify_cli.old2
  canonical_import: specify_cli.d
  introduced_in_release: 1.0.0
  removal_target_release: 1.5.0
  tracker_issue: '#4'
  grandfathered: true
`

const contractRegistry = `contracts:
- id: legacy-fallback
  kind: fallback_name
  anchor:
    symbol: specify_cli.old.fn
  status: active
  owner: core
  replaced_by: specify_cli.new.fn
  retirement:
    introduced_in: 3.0.0
    removal_target: 4.0.0
    tracker_issue: '#99'
  consumers:
    scan_roots: [src]
    exemptions: []
  verification:
    enforcement: advisory
- id: retired-word
  kind: retired_literal
  anchor:
    literals:
    - fragments: [cere, mony]
  status: retired
  owner: docs
  replaced_by: ritual
  retirement:
    introduced_in: 3.0.0
    removal_target: 3.1.0
    tracker_issue: '#100'
  consumers:
    scan_roots: [docs, src]
    exemptions: [CHANGELOG.md]
  verification:
    enforcement: enforcing
`

func TestDiffDoctorRegistries(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "shim-registry"},
		step{"doctor", "shim-registry", "--json"},
		step{"doctor", "contracts"},
		step{"doctor", "contracts", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("pyproject.toml", "[build-system]\nrequires = []\n\n[project]\nname = \"x\"\nversion = \"3.0.0\"\n")
		s.WriteFile("docs/migrations/shim-registry.yaml", shimRegistry)
		s.WriteFile("src/specify_cli/old_mod.py", "")
		s.WriteFile("src/specify_cli/later/__init__.py", "")
		s.WriteFile("docs/contracts/contract-registry.yaml", contractRegistry)
	})
	compareSteps(t, p, "", "",
		step{"doctor", "shim-registry"},
		step{"doctor", "shim-registry", "--json"},
		step{"doctor", "contracts"},
		step{"doctor", "contracts", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("docs/migrations/shim-registry.yaml", "shims:\n- legacy_path: bad path\n  extra: 1\n")
		s.WriteFile("docs/contracts/contract-registry.yaml", "contracts:\n- id: x\n  kind: nope\n  file: a.py:12\n")
	})
	compareSteps(t, p, "", "",
		step{"doctor", "shim-registry"},
		step{"doctor", "contracts", "--json"},
	)
}
