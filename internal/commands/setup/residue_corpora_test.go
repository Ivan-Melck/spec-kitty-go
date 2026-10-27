package setup_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// writeResidueFolder commits a kitty-specs/ child that is not a Mission:
// only tasks.md and task files, one of them still in a legacy lane folder.
func writeResidueFolder(s *testkit.Sandbox) {
	s.WriteFile("kitty-specs/old-notes/tasks.md", "# Tasks\n\n- [ ] WP01 something\n")
	s.WriteFile("kitty-specs/old-notes/tasks/WP01-x.md", "---\nwork_package_id: WP01\ntitle: x\nlane: planned\n---\n# WP01\n")
	s.WriteFile("kitty-specs/old-notes/tasks/planned/WP02-y.md", "---\nwork_package_id: WP02\ntitle: y\n---\n# WP02\n")
}

// dropLedger rewinds the project to the pre-ledger era: no migration ledger
// and no recorded history, so the 0.9.x/2.0.6/3.1.1 steps are pending.
func dropLedger(s *testkit.Sandbox) {
	os.Remove(filepath.Join(s.Dir, ".kittify", "migration-ledger.jsonl"))
	os.Remove(filepath.Join(s.Dir, ".kittify", "migrations", "clone-local.jsonl"))
	p := filepath.Join(s.Dir, ".kittify", "metadata.yaml")
	os.Chmod(p, 0o644)
	md := regexp.MustCompile(`(?s)\nmigrations:.*`).ReplaceAllString(s.ReadFile(".kittify/metadata.yaml"), "\n")
	s.WriteFile(".kittify/metadata.yaml", md)
}

// The residue-safe successors (#6669) repair Missions and leave a residue
// folder untouched; the superseded walkers are recorded no-ops.
func TestUpgradeLeavesResidueFolderAloneMatchesOracle(t *testing.T) {
	t.Parallel()
	p := testkit.NewPair(t)
	runCorpus(t, corpus{name: "residue-pre-ledger", ai: "codex", version: "0.8.0", pair: p, mutate: func(s *testkit.Sandbox) {
		dropLedger(s)
		writeResidueFolder(s)
	}})
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		if s.Exists("kitty-specs/old-notes/meta.json") || !s.Exists("kitty-specs/old-notes/tasks/planned/WP02-y.md") {
			t.Errorf("%s: the residue folder was changed", s.Dir)
		}
	}
}

// A project Python main already upgraded records the superseded steps as
// skipped with superseded_by; Go must not re-run them on the next upgrade.
func TestUpgradeAfterPythonUpgradeMatchesOracle(t *testing.T) {
	t.Parallel()
	runCorpus(t, corpus{name: "residue-python-upgraded", ai: "codex", version: "0.8.0", mutate: func(s *testkit.Sandbox) {
		dropLedger(s)
		writeResidueFolder(s)
		s.WriteFile(".kittify/charter/interview/answers.yaml", "selected_tactics:\n- acceptance-test-first\n- zombies-tdd\n")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "legacy")
		if r := s.RunOracle("upgrade", "--yes"); r.Code != 0 {
			t.Fatalf("oracle upgrade: %s%s", r.Stdout, r.Stderr)
		}
		if ledger := s.ReadFile(".kittify/migration-ledger.jsonl"); !strings.Contains(ledger, `"superseded_by": "Superseded by 4.0.0rc8_promote_interview_selections"`) {
			t.Fatalf("oracle upgrade recorded no superseded promotion:\n%s", ledger)
		}
	}})
}

// 4.0.0rc8_promote_interview_selections (#6629) appends interview stems to a
// present list (minus the effective deny) and never writes an absent key.
func TestUpgradePromoteInterviewSelectionsMatchesOracle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, activation string
		keepShipped      bool
	}{
		{"promote-present", "activated_tactics:\n- acceptance-test-first\n", false},
		{"promote-denied", "activated_tactics:\n- acceptance-test-first\ndeactivated_tactics:\n- zombies-tdd\n", false},
		{"promote-absent", "", false},
		{"promote-already-ran", "activated_tactics:\n- acceptance-test-first\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCorpus(t, corpus{name: tc.name, ai: "codex", version: "4.0.0rc8", mutate: func(s *testkit.Sandbox) {
				s.WriteFile(".kittify/charter/interview/answers.yaml", "selected_tactics:\n- acceptance-test-first\n- zombies-tdd\n- tdd-red-green-refactor\n- bogus-thing\n"+
					"selected_directives:\n- DIRECTIVE_001\n")
				s.WriteFile(".kittify/charter/activation.yaml", s.ReadFile(".kittify/charter/activation.yaml")+tc.activation)
				// An existing provenance folder: the oracle's surface-repair
				// precondition trips when the upgrade creates it (upstream defect).
				s.WriteFile(".kittify/encoding-provenance/global.jsonl", "")
				var kept []string
				for _, line := range strings.SplitAfter(s.ReadFile(".kittify/migration-ledger.jsonl"), "\n") {
					if strings.Contains(line, `"4.0.0rc8_promote_interview_selections"`) ||
						!tc.keepShipped && strings.Contains(line, `"unify_charter_activation_promote_answers"`) {
						continue
					}
					kept = append(kept, line)
				}
				s.WriteFile(".kittify/migration-ledger.jsonl", strings.Join(kept, ""))
				s.Git("add", "-f", ".kittify/encoding-provenance/global.jsonl")
			}})
		})
	}
}
