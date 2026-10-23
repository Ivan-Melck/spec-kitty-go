package doctrine_test

import (
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"testing"
)

func TestParityActivationLedger(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/config.yaml", "agents:\n  available: []\n") })
	for _, args := range [][]string{
		{"charter", "deactivate", "tactic", "common-docs-scaffold", "--no-compile"},
		{"charter", "deactivate", "tactic", "common-docs-scaffold", "--no-compile"},
		{"charter", "activate", "tactic", "common-docs-scaffold", "--no-compile"},
		{"charter", "activate", "directive", "DIRECTIVE_001", "--only", "--no-compile"},
		{"charter", "deactivate", "directive", "DIRECTIVE_001", "--no-compile"},
		{"charter", "activate", "directive", "DIRECTIVE_001", "--no-compile"},
	} {
		p.Compare(true, args...)
		p.CompareTree(".kittify", true)
	}
}

func TestParityActivationUnmigratedRefusal(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/config.yaml", "activated_tactics: []\n") })
	p.Compare(true, "charter", "deactivate", "tactic", "common-docs-scaffold", "--no-compile")
	p.CompareTree(".kittify", true)
}

func TestParityActivationLedgerHealth(t *testing.T) {
	for _, scenario := range []string{"fresh", "legacy", "denied", "stray", "required"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				cfg := "agents:\n  available: []\n"
				switch scenario {
				case "legacy":
					cfg += "activated_tactics: []\n"
				case "denied":
					s.WriteFile(".kittify/charter/activation.yaml", "activated_tactics: []\ndeactivated_tactics: [common-docs-scaffold, gone]\ndeactivated_future: [future]\n")
				case "stray":
					cfg += "activated_tactics: []\n"
					s.WriteFile(".kittify/charter/activation.yaml", "{}\n")
				case "required":
					cfg += "charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: org\n"
					s.WriteFile("org/org-charter.yaml", "required_tactics: [common-docs-scaffold]\n")
					s.WriteFile(".kittify/charter/activation.yaml", "activated_tactics: []\ndeactivated_tactics: [common-docs-scaffold]\n")
				}
				s.WriteFile(".kittify/config.yaml", cfg)
			})
			p.Compare(true, "doctor", "charter-packs", "--json")
			compareDoctorHuman(t, p, "doctor", "charter-packs")
			p.CompareTree(".kittify", true)
		})
	}
}

func TestParityActivationPresetExclusions(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep", true: "force"}[force], func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				s.WriteFile(".kittify/config.yaml", "charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: org\n")
				s.WriteFile("org/org-charter.yaml", "org_name: Acme\n")
				s.WriteFile("org/presets/sample.yaml", "name: sample\nactivations:\n  activated_tactics: [common-docs-scaffold]\n")
				s.WriteFile(".kittify/charter/activation.yaml", "# managed exclusions\ndeactivated_tactics: [common-docs-scaffold, stale]\n")
			})
			args := []string{"charter", "activate", "--pack", "acme", "--preset", "sample", "--json", "--no-compile"}
			if force {
				args = append(args, "--force")
			}
			p.Compare(true, args...)
			p.CompareTree(".kittify", true)
		})
	}
}

func TestParityActivationLedgerListAndCascade(t *testing.T) {
	for _, scenario := range []string{"list", "blocked", "include", "no-cascade"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				s.WriteFile(".kittify/config.yaml", "agents:\n  available: []\n")
				s.WriteFile(".kittify/charter/activation.yaml", "activated_agent_profiles: [python-pedro]\ndeactivated_directives: [001-architectural-integrity-standard]\n")
			})
			if scenario == "list" {
				p.Compare(true, "charter", "list", "--json")
				p.Compare(true, "charter", "list")
			} else {
				args := []string{"charter", "activate", "agent-profile", "python-pedro", "--no-compile"}
				if scenario != "no-cascade" {
					args = append(args, "--cascade", "all")
				}
				if scenario == "include" {
					args = append(args, "--include-deactivated")
				}
				compareDoctorHuman(t, p, args...)
			}
			p.CompareTree(".kittify", true)
		})
	}
}
