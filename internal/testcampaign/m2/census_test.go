package m2

import (
	"fmt"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

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
	iterationContracts[definition.ID] = iterationContract{"test-boundary", stringSet("pass")}
	t.Cleanup(func() { delete(iterationContracts, definition.ID) })
	raw := []byte(event("run", "TestExact"))
	for index := range 100 {
		record, err := iterationreceipt.New(definition.ID, index, fmt.Sprintf("seed-%03d", index), "test-boundary", "pass", time.Duration(index+1), map[string]int{"index": index}, map[string]bool{"checked": true})
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, fixtureIterationEvent(definition.Package, definition.Selector, record)...)
	}
	raw = append(raw, event("pass", "TestExact")...)
	receipt, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0)
	if err != nil || receipt.CertifiedRepetitions != 100 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	definition.TrialsPerRun = 99
	if _, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0); err == nil {
		t.Fatal("99-trial substitution certified a frozen 100-trial campaign")
	}
	if _, err := VerifyGoTestReceipt(append([]byte(nil), raw[:len(raw)-len(event("pass", "TestExact"))]...), "run", source, Definition{ID: definition.ID, Package: definition.Package, Selector: definition.Selector, ExpectedRepetitions: 100, TrialsPerRun: 100}, 1, 0); err == nil {
		t.Fatal("missing terminal/iteration campaign certified")
	}
}

func TestInternalIterationReceiptContractCensusIsClosed(t *testing.T) {
	definitions := FrozenDefinitions()
	if err := validateIterationContractCensus(definitions, iterationContracts); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]iterationContract){
		"missing": func(contracts map[string]iterationContract) { delete(contracts, "fault/concurrent-claims") },
		"extra": func(contracts map[string]iterationContract) {
			contracts["fault/foreign"] = iterationContract{"foreign", stringSet("pass")}
		},
		"empty": func(contracts map[string]iterationContract) {
			contracts["fault/concurrent-claims"] = iterationContract{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			clone := make(map[string]iterationContract, len(iterationContracts))
			for id, contract := range iterationContracts {
				clone[id] = contract
			}
			mutate(clone)
			if validateIterationContractCensus(definitions, clone) == nil {
				t.Fatal("iteration contract census mutant survived")
			}
		})
	}
}
