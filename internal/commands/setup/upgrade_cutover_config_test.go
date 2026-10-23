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
	writeLegacyOrgConfig(s)
	// The converted pack is fetched, so the post-migration activation
	// (require_declared_org_roots) passes and the run commits.
	s.WriteFile("packs/acme/README.md", "acme\n")
}

// writeLegacyOrgConfig writes the commented legacy org selection without
// fetching the declared pack.
func writeLegacyOrgConfig(s *testkit.Sandbox) {
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

// TestUpgradeCutoverUnfetchedOrgPackMatchesOracle: the converted org pack
// is declared but not fetched, so the activation/repair preparation fails
// closed: the upgrade reports repair_preparation_failed, leaves its changes
// uncommitted and keeps the old version stamp (a second run re-reports).
func TestUpgradeCutoverUnfetchedOrgPackMatchesOracle(t *testing.T) {
	t.Parallel()
	p := testkit.NewPair(t)
	runCorpus(t, corpus{name: "cutover-unfetched-pack", ai: "claude", version: "4.0.0rc5", mutate: writeLegacyOrgConfig, pair: p})
	// The human report of the re-run names the same failure.
	g, o := p.Run("upgrade", "--yes")
	if g.Code != o.Code {
		t.Errorf("upgrade --yes: exit go=%d oracle=%d", g.Code, o.Code)
	}
	if gs, os_ := maskUpgrade(p.Go.Normalize(g.Stdout)), maskUpgrade(p.Oracle.Normalize(o.Stdout)); gs != os_ {
		t.Errorf("upgrade --yes: stdout differs\n%s", testkit.Diff(os_, gs))
	}
	if g, o := lastCommit(p.Go), lastCommit(p.Oracle); g != o {
		t.Errorf("commit after re-run go=%q oracle=%q", g, o)
	}
}
