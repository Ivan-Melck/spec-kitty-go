package doctrine_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestParityAgentProfileSchemaMessages covers the jsonschema (Draft 7)
// messages of skipped agent profiles (DOC-08) in pack validate and doctor
// charter-packs.
func TestParityAgentProfileSchemaMessages(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	compareCharter(t, p, "charter", "org", "init", "out1")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("out1/agent_profiles/a.agent.yaml", "name: Broken\n")
		s.WriteFile("out1/agent_profiles/b.agent.yaml", "profile-id: 5\nname: ''\npurpose: p\nspecialization:\n  primary-focus: 3\n  zzz: 1\nrole: implementer\nbogus: 1\naaa: 2\n")
		s.WriteFile("out1/agent_profiles/c.agent.yaml", "profile-id: c\nname: n\npurpose: p\nspecialization: nope\nroles: [x, 3]\ncollaboration:\n  handoff-to: x\ndirective-references:\n  - code: D\n    name: 1\n")
		s.WriteFile("out1/agent_profiles/d.agent.yaml", "profile-id: d\nname: n\npurpose: p\nspecialization:\n  primary-focus: f\nroles: []\nrouting-priority: 500\nmax-concurrent-tasks: 0\n")
	})
	comparePackTool(t, p, "charter", "pack", "validate", "out1", "--json")
	p.Both(func(s *testkit.Sandbox) {
		appendConfig(s, "charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: out1\n")
	})
	compareMasked(t, p, false, "doctor", "charter-packs", "--json")
}
