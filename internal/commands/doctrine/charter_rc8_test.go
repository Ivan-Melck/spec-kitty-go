package doctrine_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestParityRC8InterviewLanguages(t *testing.T) {
	for _, scenario := range []string{"detected", "stored", "unusable", "narrow", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				config := "agents:\n  available: [codex]\n"
				switch scenario {
				case "stored":
					config += "languages: [kotlin, java]\n"
				case "unusable":
					config += "languages: [not-a-language]\n"
				}
				s.WriteFile(".kittify/config.yaml", config)
				s.WriteFile("go.mod", "module demo\n")
				s.WriteFile("main.go", "package main\n")
				if scenario == "narrow" || scenario == "denied" {
					ledger := "activated_directives: [DIRECTIVE_003, DIRECTIVE_001]\nactivated_paradigms: [test-first, solid]\n"
					if scenario == "denied" {
						ledger += "deactivated_directives: [DIRECTIVE_003]\n"
					}
					s.WriteFile(".kittify/charter/activation.yaml", ledger)
				}
			})
			args := []string{"charter", "interview", "--defaults", "--selected-directives", "DIRECTIVE_003"}
			if scenario == "narrow" {
				args = append(args, "--narrow-activation")
			}
			p.Compare(false, append(args, "--json")...)
			p.Compare(false, args...)
			p.CompareTree(".kittify", true)
		})
	}
}

func TestParityRC8SynthesisSoundness(t *testing.T) {
	for _, content := range []string{"", "# Comment only\n", "{}\n", "[]\n"} {
		t.Run(content, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				s.WriteFile(".kittify/config.yaml", "charter: .kittify/charter/charter.yaml\n")
				s.WriteFile(".kittify/charter/charter.yaml", content)
				s.WriteFile(".kittify/charter-packs/PROVENANCE.md", "Preserve this content.\n")
			})
			for _, command := range []string{"synthesize", "resynthesize"} {
				p.Compare(false, "charter", command, "--json")
				p.Compare(false, "charter", command)
			}
			p.CompareTree(".kittify", true)
		})
	}
}

func TestParityRC8SynthesizedActivationMemory(t *testing.T) {
	p := newSynthPair(t)
	p.Both(func(s *testkit.Sandbox) {
		for name, content := range synthFiles {
			s.WriteFile(name, content)
		}
		s.WriteFile(".kittify/charter/activation.yaml", "activated_tactics: []\nactivated_styleguides: []\ndeactivated_tactics: [review-policy-tactic]\n")
	})
	args := []string{"charter", "synthesize", "--json", "--skip-code-evidence", "--skip-corpus"}
	compareSynth(t, p, true, args...)
	compareSynthFile(t, p, ".kittify/charter/synthesized-artifacts.yaml")
	compareSynthFile(t, p, ".kittify/charter/activation.yaml")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/activation.yaml", "activated_tactics: []\nactivated_styleguides: []\ndeactivated_tactics: [review-policy-tactic]\n")
	})
	compareSynth(t, p, true, args...)
	compareSynthFile(t, p, ".kittify/charter/synthesized-artifacts.yaml")
	compareSynthFile(t, p, ".kittify/charter/activation.yaml")
}
