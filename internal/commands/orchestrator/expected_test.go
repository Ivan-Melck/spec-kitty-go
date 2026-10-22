package orchestrator

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const orgManifest = `schema_version: "1.0"
mission_type: "software-dev"
required_always:
  - artifact_key: "input.org.policy"
    artifact_class: "policy"
    path_pattern: "org-policy.md"
required_by_step:
  specify:
    - artifact_key: "input.spec.main"
      artifact_class: "input"
      path_pattern: "spec.md"
      blocking: true
`

// TestDiffDesignContextOrgExpectedArtifacts covers design-context's
// required_artifacts resolution through the org pack tier (the last
// configured pack carrying missions/<type>/expected-artifacts.yaml replaces
// the built-in manifest, validated and dumped in model order) and the
// loader's uncaught failures for a malformed / schema-invalid org manifest.
func TestDiffDesignContextOrgExpectedArtifacts(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/missions/software-dev/expected-artifacts.yaml", orgManifest)
		s.Exec("", "bash", "-c", "printf 'charter_packs:\\n  org:\\n    packs:\\n    - name: acme\\n      local_path: orgpack\\n' >> .kittify/config.yaml && git add -A && git commit -qm org")
	})
	step(t, p, api, "design-context", "--mission-type", "software-dev")
	step(t, p, api, "design-context", "--mission-type", "research")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/missions/software-dev/expected-artifacts.yaml", orgManifest+"bogus: 1\n")
	})
	step(t, p, api, "design-context", "--mission-type", "software-dev")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/missions/software-dev/expected-artifacts.yaml", "- not a mapping\n")
	})
	step(t, p, api, "design-context", "--mission-type", "software-dev")
}
