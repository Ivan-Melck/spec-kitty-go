package m2

import "testing"

func TestAuthorityFamilyOwnershipMatchesFinalizedTaskAuthority(t *testing.T) {
	want := map[string]string{
		"family/operation":           "WP02",
		"family/mission":             "WP05",
		"family/workgraph":           "WP05",
		"family/artifact_catalog":    "WP06",
		"family/artifact_revision":   "WP06",
		"family/execution_slot":      "WP07",
		"family/external_effect":     "WP07",
		"family/workspace":           "WP08",
		"family/handoff_slot":        "WP09",
		"family/review_slot":         "WP09",
		"family/acceptance":          "WP10",
		"family/integration_target":  "WP10",
		"family/integration_attempt": "WP10",
		"family/outbox":              "WP11",
		"family/runtime_host":        "WP13",
	}
	if len(authorityFamilies) != len(want) {
		t.Fatalf("authority family census=%d want=%d", len(authorityFamilies), len(want))
	}
	for _, definition := range authorityFamilies {
		if want[definition.ID] != definition.OwnerWP {
			t.Errorf("%s owner=%s want=%s", definition.ID, definition.OwnerWP, want[definition.ID])
		}
	}
}

func TestTrialsPerRunCannotManufactureObservedRepetitions(t *testing.T) {
	for _, definition := range FrozenDefinitions() {
		if definition.TrialsPerRun <= 0 || definition.ExpectedRepetitions%definition.TrialsPerRun != 0 {
			t.Errorf("%s has invalid frozen trial accounting", definition.ID)
		}
	}
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40)}
	definition := Definition{ID: "inner-campaign", Package: "./internal/example", Selector: "TestExact", ExpectedRepetitions: 100, TrialsPerRun: 100}
	raw := []byte(event("run", "TestExact") + event("pass", "TestExact"))
	receipt, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0)
	if err != nil || receipt.CertifiedRepetitions != 100 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	definition.TrialsPerRun = 99
	if _, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0); err == nil {
		t.Fatal("99-trial substitution certified a frozen 100-trial campaign")
	}
}
