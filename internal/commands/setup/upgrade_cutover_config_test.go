package setup_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// writeCommentedLegacyOrgConfig appends a commented legacy org selection to
// config.yaml: a convertible organisation_packs list, a non-convertible
// entry, a legacy governance.doctrine key and quoted/commented neighbours.
// The 4.0.0rc6 charter-pack cutover rewrites it through ruamel's round
// trip, keeping comments and quote styles.
func writeCommentedLegacyOrgConfig(s *testkit.Sandbox) {
	cfg := s.ReadFile(".kittify/config.yaml")
	s.WriteFile(".kittify/config.yaml", "# Project settings (keep this header)\n"+cfg+
		"# Organisation packs, still in the flat legacy form\n"+
		"organisation_packs:\n"+
		"  - name: acme   # the house pack\n"+
		"    path: ./packs/acme\n"+
		"  - name: remote\n"+
		"    url: 'https://example.invalid/pack.git'\n"+
		"governance:\n"+
		"  doctrine: \"strict\"  # legacy key\n"+
		"notes: 'single quoted'   # trailing comment\n")
	// The converted pack is fetched, so the post-migration activation
	// (require_declared_org_roots) passes and the run commits.
	s.WriteFile("packs/acme/README.md", "acme\n")
}

func TestUpgradeCutoverCommentedConfigMatchesOracle(t *testing.T) {
	for _, v := range []string{"4.0.0rc1", "4.0.0rc5"} {
		v := v
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			runCorpus(t, corpus{name: "cutover-config-" + v, ai: "claude", version: v, mutate: writeCommentedLegacyOrgConfig})
		})
	}
}
