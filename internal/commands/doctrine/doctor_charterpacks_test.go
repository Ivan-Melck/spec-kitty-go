package doctrine_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const orgPackConfig = `charter_packs:
  org:
    packs:
    - name: acme
      local_path: orgpack
`

// writeOrgPack writes an org pack under orgpack/ with a fragment, an org
// override of a built-in directive, a new tactic and a broken profile.
func writeOrgPack(s *testkit.Sandbox, fragment string) {
	s.WriteFile("orgpack/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\npack_level: org\npack_version: 1.2.3\n")
	s.WriteFile("orgpack/drg/fragment.yaml", fragment)
	s.WriteFile("orgpack/tactics/acme-tactic.tactic.yaml", "id: acme-tactic\nschema_version: \"1.0\"\nname: Acme Tactic\nsteps:\n  - title: Do it\n")
	s.WriteFile("orgpack/directives/010-x.directive.yaml", "id: DIRECTIVE_010\nschema_version: \"1.0\"\ntitle: Overridden\nintent: Org override\nenforcement: required\n")
	s.WriteFile("orgpack/agent_profiles/broken.agent.yaml", "name: Broken\n")
	s.WriteFile("orgpack/org-charter.yaml", "schema_version: \"2\"\norg_name: Acme\ninterview_defaults:\n  testing: pytest\nrequired_directives: [DIRECTIVE_010, acme-rule]\nrequired_tactics: [acme-tactic]\ngovernance_policies:\n  - field: x\n    value: y\n")
}

const acmeFragment = `nodes:
  - id: acme-rule
    kind: directives
    title: Acme Rule
edges:
  - source: acme-rule
    target: tactic:acme-tactic
    relation: requires
  - source: acme-rule
    target: directive:DIRECTIVE_999
    relation: suggests
`

func appendConfig(s *testkit.Sandbox, extra string) {
	s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+extra)
}

func TestParityDoctorCharterPacks(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	cp := []string{"doctor", "charter-packs"}
	cpj := []string{"doctor", "charter-packs", "--json"}
	compareDoctorHuman(t, p, cp...)
	compareMasked(t, p, false, cpj...)
	// Retired governance key + repo-root doctrine/ fallback.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("doctrine/x/keep", "")
		s.WriteFile(".kittify/charter/charter.yaml", "governance:\n  doctrine:\n    selected_directives: [DIRECTIVE_010]\n")
	})
	compareDoctorHuman(t, p, cp...)
	compareMasked(t, p, false, cpj...)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/charter.yaml", "governance:\n  charter:\n    selected_directives: [DIRECTIVE_010, nope]\n    selected_tactics: [acceptance-test-first]\n")
		writeOrgPack(s, acmeFragment)
		appendConfig(s, orgPackConfig+"    - name: gone\n      local_path: nowhere\n")
	})
	compareDoctorHuman(t, p, cp...)
	compareMasked(t, p, false, cpj...)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", strings.Replace(s.ReadFile(".kittify/config.yaml"), "    - name: gone\n      local_path: nowhere\n", "", 1))
	})
	compareDoctorHuman(t, p, cp...)
	compareMasked(t, p, false, cpj...)
	// Consumer sanction + unknown revocation target.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter-packs/replaceable-builtins.yaml", "replaceable_builtins:\n  - urn: directive:DIRECTIVE_010\n    reason: org knows best\nrevoked_pack_sanctions:\n  - pack: ghost\n")
	})
	compareDoctorHuman(t, p, cp...)
	compareMasked(t, p, false, cpj...)
}

func TestParityDoctorCharterPacksFragments(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		writeOrgPack(s, acmeFragment)
		appendConfig(s, orgPackConfig)
	})
	for _, frag := range []string{
		"nodes:\n  - id: x\n    kind: widgets\n",
		"nodes:\n  - id: x\n    kind: directives\n    extra: 1\nedges: []\nbogus: true\n",
		"nodes:\n  - id: x\n    kind: directives\nedges:\n  - source: x\n    target: nonexistent-thing\n    relation: requires\n  - source: x\n    target: DIRECTIVE_010\n    relation: requires\n",
		"nodes:\n  - id: DIRECTIVE_010\n    kind: tactics\n",
		"nodes:\n  - id: acceptance-test-first\n    kind: directives\n",
	} {
		p.Both(func(s *testkit.Sandbox) { s.WriteFile("orgpack/drg/fragment.yaml", frag) })
		compareDoctorHuman(t, p, "doctor", "charter-packs")
		compareMasked(t, p, false, "doctor", "charter-packs", "--json")
	}
	// Legacy template hint and a project-level pack.yaml (PACK_LEVEL_MISMATCH).
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/drg/fragment.yaml", acmeFragment)
		s.WriteFile("orgpack/templates/setup/replaceable-builtins.yaml", "replaceable_builtins:\n  - urn: directive:DIRECTIVE_010\n    reason: legacy reason\n")
	})
	compareDoctorHuman(t, p, "doctor", "charter-packs")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\npack_level: project\n")
	})
	compareMasked(t, p, false, "doctor", "charter-packs", "--json")
}

func TestDoctorCharterPacksGoOnly(t *testing.T) {
	s := newProject(t)
	r := s.Run("doctor", "charter-packs", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"org_configured": false`) || !strings.Contains(r.Stdout, `"term_count": 128`) {
		t.Fatalf("no packs: %d %s", r.Code, r.Stdout)
	}
	writeOrgPack(s, acmeFragment)
	appendConfig(s, orgPackConfig)
	r = s.Run("doctor", "charter-packs", "--json")
	for _, want := range []string{`"org_configured": true`, `"unsanctioned_overrides"`, `"node_count": 3`, `"item_id": "DIRECTIVE_010"`, `Dangling target`} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("missing %s in\n%s", want, r.Stdout)
		}
	}
	if r.Code != 1 {
		t.Fatalf("expected unhealthy exit 1, got %d", r.Code)
	}
}
