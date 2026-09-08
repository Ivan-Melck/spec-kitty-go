package m2

import (
	"fmt"
	"os"
	"path/filepath"
)

type familyRegistry struct {
	SchemaVersion   int           `json:"schema_version"`
	RegistryID      string        `json:"registry_id"`
	Families        []familyTuple `json:"families"`
	Ordering        []string      `json:"ordering"`
	PublicationRule string        `json:"publication_rule"`
}

type familyTuple struct {
	Family      string `json:"family"`
	EventSchema int    `json:"event_schema"`
	Reducer     int    `json:"reducer"`
	Owner       string `json:"owner"`
}

var admittedFamilies = []familyTuple{
	{"runtime_host", 1, 1, "internal/productionruntime"},
	{"operation", 2, 2, "internal/controljournal/application"},
	{"mission", 1, 1, "internal/mission"},
	{"artifact_catalog", 1, 1, "internal/artifact"},
	{"artifact_revision", 1, 1, "internal/artifact"},
	{"workgraph", 1, 1, "internal/workgraph"},
	{"execution_slot", 1, 1, "internal/execution"},
	{"workspace", 1, 1, "internal/workspace"},
	{"handoff_slot", 1, 1, "internal/handoff"},
	{"review_slot", 1, 1, "internal/review"},
	{"acceptance", 1, 1, "internal/integration"},
	{"integration_target", 1, 1, "internal/integration"},
	{"integration_attempt", 1, 1, "internal/integration"},
	{"external_effect", 1, 1, "internal/execution"},
	{"outbox", 1, 1, "internal/controlstore"},
}

func ValidateFamilyRegistry(repositoryRoot string) error {
	path := filepath.Join(repositoryRoot, "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/event-family-registry-v2.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var registry familyRegistry
	if err := DecodeStrict(data, &registry); err != nil {
		return fmt.Errorf("decode family registry: %w", err)
	}
	if registry.SchemaVersion != 2 || registry.RegistryID != "spec-kitty.authority-event-families.v2" || len(registry.Families) != len(admittedFamilies) || len(registry.Ordering) != 3 || registry.Ordering[0] != "gapdb_record_revision" || registry.Ordering[1] != "event_index" || registry.Ordering[2] != "event_id" || registry.PublicationRule != "one_transition_event_per_aggregate_per_publication" {
		return fmt.Errorf("family registry header/census differs")
	}
	seen := map[string]struct{}{}
	for index, want := range admittedFamilies {
		if registry.Families[index] != want {
			return fmt.Errorf("family registry tuple %d is reordered or changed", index)
		}
		if _, duplicate := seen[want.Family]; duplicate {
			return fmt.Errorf("family %q is duplicated", want.Family)
		}
		seen[want.Family] = struct{}{}
	}
	return nil
}
