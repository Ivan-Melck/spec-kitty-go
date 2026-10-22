package doctrine_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestParityCharterStatusManifestProvenanceErrors covers the pydantic
// ValidationError texts of SynthesisManifest / ProvenanceEntry (DOC-04) in
// charter status and charter bundle validate.
func TestParityCharterStatusManifestProvenanceErrors(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	manifests := []string{
		"schema_version: \"9\"\ncreated_at: 5\nartifacts:\n- kind: bogus\n  slug: 1\nextra: x\n",
		"artifacts: notalist\nbuilt_in_only: maybe\n",
		"- a\n- b\n",
		"schema_version: \"3\"\ncreated_at: \"2026-01-01\"\nrun_id: r\nadapter_id: a\nadapter_version: \"1\"\nsynthesizer_version: \"\"\nmanifest_hash: abc\nartifacts: []\n",
	}
	provs := []string{
		"schema_version: \"2\"\nartifact_urn: 5\nextra: 1\n",
		"artifact_urn: directive:x\nartifact_kind: directive\nartifact_slug: x\nartifact_content_hash: h\ninputs_hash: i\nadapter_id: a\nadapter_version: \"1\"\nsynthesizer_version: \"1\"\nsource_section: null\nsource_urns: []\nsource_input_ids: []\ngenerated_at: g\nproduced_at: p\ncorpus_snapshot_id: c\nsynthesis_run_id: r\nunknown: 1\n",
	}
	for i, m := range manifests {
		p.Both(func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/charter/synthesis-manifest.yaml", m)
			s.WriteFile(".kittify/charter/provenance/x.yaml", provs[i%len(provs)])
		})
		compareCharter(t, p, "charter", "status", "--json")
		compareCharter(t, p, "charter", "bundle", "validate", "--json")
	}
}
