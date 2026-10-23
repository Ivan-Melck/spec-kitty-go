package setup_test

import (
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"testing"
)

func TestActivationLedgerMigrationMatchesOracle(t *testing.T) {
	for _, scenario := range []string{"config", "pointer", "divergent", "legacy-deny", "ledger-equal", "ledger-divergent"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				cfg := "agents:\n  available: []\nactivated_tactics: [common-docs-scaffold]\nmission_type_activations: []\n"
				switch scenario {
				case "pointer", "divergent":
					cfg = "charter: .kittify/charter/charter.yaml\nactivated_tactics: [common-docs-scaffold]\n"
					value := "common-docs-scaffold"
					if scenario == "divergent" {
						value = "other"
					}
					s.WriteFile(".kittify/charter/charter.yaml", "# authored\nactivated_tactics: ["+value+"]\nother: keep\n")
				case "legacy-deny":
					cfg += "deactivated_tactics: []\n"
				case "ledger-equal", "ledger-divergent":
					value := "common-docs-scaffold"
					if scenario == "ledger-divergent" {
						value = "other"
					}
					s.WriteFile(".kittify/charter/activation.yaml", "mission_type_activations: []\nactivated_tactics: ["+value+"]\n")
				}
				s.WriteFile(".kittify/config.yaml", cfg)
			})
			p.Compare(true, "migrate", "activation-ledger", "--dry-run", "--json")
			p.CompareTree(".kittify", true)
			p.Compare(true, "migrate", "activation-ledger")
			p.CompareTree(".kittify", true)
			p.Compare(true, "migrate", "activation-ledger", "--json")
			p.CompareTree(".kittify", true)
		})
	}
}
