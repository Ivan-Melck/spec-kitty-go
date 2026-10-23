package doctrine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// excText is the exception text below a rich traceback box (every stderr
// line outside the box), whitespace-collapsed.
func excText(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if pl := testkit.StripANSI(l); strings.HasPrefix(pl, "│") || strings.HasPrefix(pl, "╭") || strings.HasPrefix(pl, "╰") {
			continue
		}
		out = append(out, strings.Join(strings.Fields(l), " "))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// TestParityRegenerateGraphMalformedYAML covers the extractor's Python type
// semantics over malformed artifact YAML: unquoted YAML 1.2 ints/dates as
// directive ids (re.match TypeError), non-mapping reference entries
// (AttributeError), non-iterable lists (TypeError), unhashable reference
// types, pydantic DRGNode/DRGEdge validation (labels, when/reason, URNs),
// operating-procedure collection and sorting, and a non-str mission-type id
// reaching the step repository. Each crash is compared on its frames and its
// full exception text.
func TestParityRegenerateGraphMalformedYAML(t *testing.T) {
	t.Parallel()
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) { vendorBuiltIn(t, s, "pk") })
	check := []string{"charter", "pack", "regenerate-graph", "--pack-root", "pk", "--check"}
	cases := []struct{ file, body string }{
		{"directives/zz.directive.yaml", "id: 024\ntitle: X\n"},
		{"directives/zz.directive.yaml", "id: 2020-01-01\n"},
		{"directives/zz.directive.yaml", "id: DIRECTIVE_900\ntitle: 5\n"},
		{"directives/zz.directive.yaml", "title: X\n"},
		{"directives/zz.directive.yaml", "id: DIRECTIVE_900\ntactic_refs: 5\n"},
		{"directives/zz.directive.yaml", "id: DIRECTIVE_900\ntactic_refs: [{a: 1}]\n"},
		{"directives/zz.directive.yaml", "id: DIRECTIVE_900\nreferences: {type: tactic, id: x}\n"},
		{"directives/zz.directive.yaml", "id: DIRECTIVE_900\nreferences: [{type: [a], id: x}]\n"},
		{"directives/zz.directive.yaml", "id: DIRECTIVE_900\nreferences: [{type: tactic, id: x, when: 5, reason: [1]}]\n"},
		{"tactics/zz.tactic.yaml", "id: zz\nsteps: [{references: [5]}]\n"},
		{"paradigms/zz.paradigm.yaml", "id: zz\ndirective_refs: [5]\n"},
		{"procedures/zz.procedure.yaml", "id: zz\nreferences: [{type: {a: 1}, id: y}]\n"},
		{"agent_profiles/zz.agent.yaml", "profile-id: zz\ncollaboration: [x]\n"},
		{"agent_profiles/zz.agent.yaml", "profile-id: zz\ntactic-references: [{id: tdd-red-green-refactor, rationale: 5}]\n"},
		{"agent_profiles/zz.agent.yaml", "profile-id: zz\ncollaboration: {operating-procedures: [nope, 3]}\n"},
		{"styleguides/zz.styleguide.yaml", "id: zz\nreferences: 5\n"},
		{"missions/software-dev/actions/zz/index.yaml", "action: 5\n"},
		{"missions/mission_types/zz.yaml", "id: [x]\n"},
		{"missions/mission_types/zz.yaml", "id: 5\n"},
		{"missions/mission_types/zz.yaml", "id: zz\naction_sequence: ['a b']\n"},
		{"missions/built_in_step_contracts/zz.step-contract.yaml", "mission: zz\naction: 5\n"},
		{"missions/plan/governance-profile.yaml", "mission_type: 'a b'\nselected_tactics: [acceptance-test-first]\n"},
	}
	for _, c := range cases {
		var saved map[*testkit.Sandbox][]byte
		p.Both(func(s *testkit.Sandbox) {
			fp := filepath.Join(s.Dir, "pk", c.file)
			if b, err := os.ReadFile(fp); err == nil {
				if saved == nil {
					saved = map[*testkit.Sandbox][]byte{}
				}
				saved[s] = b
			}
			s.WriteFile(filepath.Join("pk", c.file), c.body)
		})
		g, o := compareRegenCrashTail(t, p, check...)
		compareTracebackFrames(t, c.file+": "+c.body, g, o)
		if ge, oe := excText(p.Go.Normalize(g.Stderr)), excText(p.Oracle.Normalize(o.Stderr)); ge != oe {
			t.Errorf("%s %q: exception text differs\n%s", c.file, c.body, testkit.Diff(oe, ge))
		}
		p.Both(func(s *testkit.Sandbox) {
			if b, ok := saved[s]; ok {
				s.WriteFile(filepath.Join("pk", c.file), string(b))
			} else {
				os.Remove(filepath.Join(s.Dir, "pk", c.file))
			}
		})
	}
	// Odd but valid values are formatted with str(): the written graph
	// matches.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("pk/tactics/zz.tactic.yaml", "id: 5\nname: Five\nreferences: [{type: tactic, id: 2020-01-02}, {type: paradigm, id: true}]\n")
		s.WriteFile("pk/directives/zz.directive.yaml", "id: DIRECTIVE_900\ntitle: Z\ntactic_refs: ab\n")
	})
	compareRegenCrashTail(t, p, "charter", "pack", "regenerate-graph", "--pack-root", "pk")
	p.CompareTree("pk", true)
}
