package testcampaign

import (
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
	"reflect"
	"testing"
)

func TestHubnotManifestScopesAreClosedAndIndependent(t *testing.T) {
	manifest := HubnotManifest()
	if manifest.Provider != hubnot.QualifiedProviderIdentity() {
		t.Fatal("tuple drift")
	}
	full, err := HubnotSelection("full-local")
	if err != nil {
		t.Fatal(err)
	}
	hosted, err := HubnotSelection("hosted-provider")
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != len(hosted)+1 {
		t.Fatal("hosted must exclude exactly local recovery")
	}
	families := map[int]bool{}
	for _, row := range full {
		families[row.Family] = true
		if row.ID != "cutover.local-recovery" {
			found := false
			for _, h := range hosted {
				if reflect.DeepEqual(row, h) {
					found = true
				}
			}
			if !found {
				t.Fatalf("hosted omitted %s", row.ID)
			}
		}
	}
	if len(families) != 11 {
		t.Fatal("missing required family")
	}
	for _, scope := range []string{"", "automatic", "missing-originals"} {
		if _, err := HubnotSelection(scope); err == nil {
			t.Fatal("invented scope admitted")
		}
	}
	full[0].ID = "changed"
	full[0].Tests = append(full[0].Tests, "changed")
	if reflect.DeepEqual(full, HubnotManifest().Scenarios) {
		t.Fatal("manifest mutable")
	}
}
