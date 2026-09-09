package architecture

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

func TestHubnotActiveCutover(t *testing.T) {
	root := phase2RepositoryRoot(t)
	for _, path := range []string{"internal/tracker/beads", "internal/testcampaign/cmd/beads-qualification", "scripts/install-qualified-beads.sh", ".github/workflows/beads-qualification.yml", "scripts/install-qualified-hubnot.sh"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("retired executable surface remains: %s (%v)", path, err)
		}
	}
	portable, err := testcampaign.ProfileByName("provider-portable")
	if err != nil || len(portable.ExpectedTests) != 2 {
		t.Fatalf("portable census: %+v %v", portable, err)
	}
	adversarial, err := testcampaign.ProfileByName("provider-adversarial")
	if err != nil || adversarial.Package != "./internal/tracker/hubnot" || len(adversarial.ExpectedTests) != 32 {
		t.Fatalf("offline successor census: %+v %v", adversarial, err)
	}
	for _, tc := range []struct{ profile, test string }{{"provider-real", testcampaign.HubnotHostedTest}, {"provider-full-local", testcampaign.HubnotFullTest}, {testcampaign.MissionRealIdentityProfile, testcampaign.HubnotIdentityTest}} {
		p, err := testcampaign.ProfileByName(tc.profile)
		if err != nil || p.Package != testcampaign.HubnotQualificationPackage || !slices.Equal(p.ExpectedTests, []string{tc.test}) {
			t.Errorf("scope %s mismatch: %+v %v", tc.profile, p, err)
		}
	}
	for _, name := range []string{"provider_bounded", "provider_real"} {
		c, err := testcampaign.CampaignByName(name)
		if err != nil || !c.RequireUniversal || !c.RequireStaleProbe {
			t.Errorf("campaign weakened: %s %v", name, err)
		}
	}
	historical := m2LegacyExemptions(m2ModulePath)
	active := m2ActiveLegacyExemptions(m2ModulePath)
	if len(historical) != 16 || len(active) != 11 {
		t.Fatalf("historical/active exemption count = %d/%d", len(historical), len(active))
	}
	if err := validateM2LegacyExemptions(historical); err != nil {
		t.Fatal(err)
	}
	policy := m2ClosedPolicy(t, m2ModulePath)
	for _, row := range historical {
		retired := row.Source == m2ModulePath+"/internal/tracker/beads" && row.Kind == ImportTestOnly
		if slices.Contains(active, row) == retired {
			t.Fatalf("active exemption changed wrong row: %+v", row)
		}
		if !retired {
			continue
		}
		if m2IsLegacyExempt(row.Source, row.Target, row.Kind, active) {
			t.Fatalf("retired import still exempt: %+v", row)
		}
		graph := fixtureGraph([]string{row.Source, row.Target}, ImportEdge{SourcePackage: row.Source, TargetPackage: row.Target, Kind: row.Kind})
		graph.ModulePath = m2ModulePath
		found := false
		for _, violation := range m2EvaluateIncrementalRepository(policy, graph) {
			if violation.SourcePackage == row.Source && violation.TargetPackage == row.Target {
				found = true
			}
		}
		if !found {
			t.Fatalf("live guard filtered retired import: %+v", row)
		}
	}
	makefile := phase2Read(t, root, "Makefile")
	for _, target := range []string{"hubnot-contract:", "hubnot-qualification:", "hubnot-hosted-provider:", "hubnot-install:", "hubnot-build:"} {
		if !strings.Contains(makefile, target) {
			t.Errorf("missing %s", target)
		}
	}
	for _, obsolete := range []string{"beads-contract:", "beads-qualification:", "QUALIFIED_BEADS_DIGEST", "install-qualified-hubnot.sh"} {
		if strings.Contains(makefile, obsolete) {
			t.Errorf("active obsolete Make surface %s", obsolete)
		}
	}
}
