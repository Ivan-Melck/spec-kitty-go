package doctrine_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestParityCharterInterviewOrgPrefill covers the org charter interview
// pre-fill and org-required promotion (apply_org_charter_to_interview).
func TestParityCharterInterviewOrgPrefill(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		writeOrgPack(s, acmeFragment)
		appendConfig(s, orgPackConfig)
	})
	compareCharter(t, p, "charter", "interview", "--defaults", "--json")
	p.CompareTree(".kittify/charter/interview", true)
	compareCharter(t, p, "charter", "interview", "--defaults")
	if g, o := p.Go.ReadFile(".kittify/config.yaml"), p.Oracle.ReadFile(".kittify/config.yaml"); p.Go.Normalize(g) != p.Oracle.Normalize(o) {
		t.Errorf("config.yaml differs\n%s", testkit.Diff(p.Oracle.Normalize(o), p.Go.Normalize(g)))
	}
	compareCharter(t, p, "charter", "generate", "--json")
}

const extendsConfig = `charter_packs:
  org:
    packs:
    - name: overlay
      local_path: packs/overlay
    - name: base
      local_path: packs/base
`

func writeExtendsPacks(s *testkit.Sandbox, base, overlay string) {
	for _, pk := range []string{"base", "overlay"} {
		s.WriteFile("packs/"+pk+"/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: "+pk+"\npack_level: org\n")
		s.WriteFile("packs/"+pk+"/drg/fragment.yaml", "nodes: []\nedges: []\n")
	}
	s.WriteFile("packs/base/org-charter.yaml", base)
	s.WriteFile("packs/overlay/org-charter.yaml", overlay)
}

// TestParityDoctorCharterPacksExtends covers org-charter extends: chains in
// the selection block (DOC-13): base-first order, missing base, cycle.
func TestParityDoctorCharterPacksExtends(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) { appendConfig(s, extendsConfig) })
	for _, c := range [][2]string{
		{"schema_version: 2\norg_name: Base\nrequired_directives: [DIRECTIVE_001]\nrequired_tactics: [acceptance-test-first]\n",
			"schema_version: 2\norg_name: Overlay\nextends: base\nrequired_directives: [DIRECTIVE_010]\n"},
		{"schema_version: 2\nrequired_directives: [DIRECTIVE_001]\n", "schema_version: 2\nextends: ghost\nrequired_directives: [DIRECTIVE_010]\n"},
		{"schema_version: 2\nextends: overlay\nrequired_directives: [DIRECTIVE_001]\n", "schema_version: 2\nextends: base\nrequired_directives: [DIRECTIVE_010]\n"},
		{"schema_version: 1\nrequired_directives: [DIRECTIVE_001]\n", "schema_version: 3\nextends: base\nrequired_directives: [DIRECTIVE_010]\n"},
	} {
		p.Both(func(s *testkit.Sandbox) { writeExtendsPacks(s, c[0], c[1]) })
		compareMasked(t, p, false, "doctor", "charter-packs", "--json")
		compareDoctorHuman(t, p, "doctor", "charter-packs")
	}
}
