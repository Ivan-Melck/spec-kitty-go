package doctrine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// vendorBuiltIn copies the shipped built-in pack into rel (a vendored
// built-in-level pack for --pack-root).
func vendorBuiltIn(t testing.TB, s *testkit.Sandbox, rel string) {
	t.Helper()
	bi := dct.BuiltIn()
	for _, f := range bi.Walk(".", "", true) {
		b, err := bi.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s.WriteFile(filepath.Join(rel, filepath.FromSlash(f)), string(b))
	}
}

func appendFile(t testing.TB, s *testkit.Sandbox, rel, text string) {
	t.Helper()
	p := filepath.Join(s.Dir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte(text)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// compareRegenCrash compares exit codes and the final traceback line.
func compareRegenCrash(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%v: exit go=%d oracle=%d\n%s%s\n%s%s", args, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	last := func(s string) string {
		l := strings.Split(strings.TrimRight(s, "\n"), "\n")
		return strings.Join(strings.Fields(l[len(l)-1]), " ")
	}
	if gl, ol := last(p.Go.Normalize(g.Stderr)), last(p.Oracle.Normalize(o.Stderr)); gl != ol {
		t.Errorf("%v: final stderr line differs\n oracle: %s\n go:     %s", args, ol, gl)
	}
}

// TestParityRegenerateGraphExtractor covers the ported DRG extractor
// (DOC-07): fresh vendored copy, authored additions across every walker
// (directive/tactic/paradigm/procedure references, path-string references,
// agent-profile references, action scope + calibration, governance
// profiles, mission steps/templates), stale detection, the write path
// (fragments + pack-manifest.yaml), validation failures and fail-closed
// ValueErrors.
func TestParityRegenerateGraphExtractor(t *testing.T) {
	t.Parallel()
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) { vendorBuiltIn(t, s, "pk") })
	rg := []string{"charter", "pack", "regenerate-graph", "--pack-root", "pk"}
	check := append(append([]string{}, rg...), "--check")
	comparePackTool(t, p, check...)
	comparePackTool(t, p, append(check, "--json")...)

	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("pk/directives/099-new-rule.directive.yaml", "id: DIRECTIVE_099\ntitle: '  New Rule  '\ntactic_refs:\n  - brand-new-tactic\n"+
			"references:\n  - type: tactic\n    id: tdd-red-green-refactor\n    when: when testing\n    reason: because\n  - type: styleguide\n    id: kitty-glossary-writing\n  - type: bogus\n    id: x\n  - type: template\n    id: spec-template\n")
		s.WriteFile("pk/tactics/extra/brand-new.tactic.yaml", "id: brand-new-tactic\nname: Brand New\nreferences:\n  - type: directive\n    id: 24-locality\n"+
			"steps:\n  - title: s1\n    references:\n      - type: procedure\n        id: test-first-bug-fixing\n        reason: step reason\n")
		s.WriteFile("pk/styleguides/zz/new.styleguide.yaml", "id: zz-guide\ntitle: ZZ Guide\nreferences:\n  - src/charter/offering/tactics/built-in/sub/tdd-red-green-refactor.tactic.yaml\n"+
			"  - packs/built-in/directives/099-new-rule.directive.yaml\n  - https://example.com\n  - 42\n")
		s.WriteFile("pk/procedures/new-proc.procedure.yaml", "id: new-proc\nname: New Proc\nreferences:\n  - type: tactic\n    id: brand-new-tactic\n  - type: toolguide\n    id: git-hygiene\n")
		s.WriteFile("pk/paradigms/new.paradigm.yaml", "id: new-paradigm\nname: New\ndirective_refs: ['050']\nreferences:\n  - type: procedure\n    id: new-proc\n")
		s.WriteFile("pk/agent_profiles/new-agent.agent.yaml", "profile-id: new-agent\nname: New Agent\ndirective-references:\n  - code: '099'\n"+
			"tactic-references:\n  - id: brand-new-tactic\n    rationale: why\nstyleguide-references:\n  - id: zz-guide\ncollaboration:\n  operating-procedures: [new-proc, '']\n")
		s.WriteFile("pk/missions/software-dev/actions/extra/index.yaml", "action: extra\ntactics: [brand-new-tactic]\nprocedures: [new-proc]\n")
		appendFile(t, s, "pk/missions/software-dev/actions/implement/index.yaml", "  - new-proc\n  - zz-unknown-proc\nagent_profiles: [new-agent]\n")
	})
	comparePackTool(t, p, check...)
	comparePackTool(t, p, rg...)
	p.CompareTree("pk", true)
	comparePackTool(t, p, append(check, "--json")...)

	// A missing fragment and a stray monolith.
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "pk", "template.graph.yaml"))
		s.WriteFile("pk/graph.yaml", "schema_version: '1.0'\n")
	})
	comparePackTool(t, p, check...)
	comparePackTool(t, p, append(rg, "--json")...)
	p.CompareTree("pk", true)

	// requires cycle -> invalid.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("pk/procedures/cyc-a.procedure.yaml", "id: cyc-a\nname: A\nreferences:\n  - type: procedure\n    id: cyc-b\n")
		s.WriteFile("pk/procedures/cyc-b.procedure.yaml", "id: cyc-b\nname: B\nreferences:\n  - type: procedure\n    id: cyc-a\n")
	})
	comparePackTool(t, p, check...)
	comparePackTool(t, p, append(rg, "--json")...)
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "pk/procedures/cyc-a.procedure.yaml"))
		os.Remove(filepath.Join(s.Dir, "pk/procedures/cyc-b.procedure.yaml"))
	})

	// Fail-closed ValueErrors (uncaught upstream: traceback, exit 1).
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("pk/agent_profiles/bad-agent.agent.yaml", "profile-id: bad-agent\nname: Bad\ncollaboration:\n  operating-procedures: [no-such-proc, tdd-red-green-refactor]\n")
	})
	compareRegenCrash(t, p, check...)
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "pk/agent_profiles/bad-agent.agent.yaml"))
		s.WriteFile("pk/missions/plan/governance-profile.yaml", "mission_type: plan\nselected_tactics: [nope-tactic]\nselected_directives: ['077']\n")
	})
	compareRegenCrash(t, p, rg...)
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "pk/missions/plan/governance-profile.yaml"))
		s.WriteFile("pk/skills/x.skill.yaml", "name: no id\n")
	})
	compareRegenCrash(t, p, rg...)
	p.CompareTree("pk", true)
}
