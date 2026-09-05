package softwaredev_test

import (
	"slices"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
)

func TestCapabilityDefinitionIsCompleteUnorderedAndStackNeutral(t *testing.T) {
	templates := softwaredev.CapabilityDefinition().Templates()
	if len(templates) != len(mission.AllActionIDs()) {
		t.Fatalf("capability census=%d want=%d", len(templates), len(mission.AllActionIDs()))
	}
	seen := map[mission.ActionID]bool{}
	var quarantine mission.CapabilityTemplate
	for _, template := range templates {
		if template.Action.Validate() != nil || seen[template.Action] {
			t.Fatalf("invalid or duplicate capability: %s", template.Action)
		}
		seen[template.Action] = true
		if template.Action == mission.ActionWorkspaceQuarantine {
			quarantine = template
		}
		if template.ScopeKind == "" {
			t.Fatalf("capability has no authority scope: %s", template.Action)
		}
		for _, predicate := range template.Predicates {
			if predicate.Authority != mission.InstructionInvariant || predicate.Kind.Validate() != nil || predicate.ResponsibleCapability.Validate() != nil {
				t.Fatalf("invalid safety predicate for %s: %+v", template.Action, predicate)
			}
		}
	}
	if len(quarantine.Predicates) != 2 || quarantine.Predicates[0].Kind != mission.PredicateLeaseExpired || quarantine.Predicates[1].Kind != mission.PredicateWorkspaceBound {
		t.Fatalf("workspace quarantine does not require an expired lease and bound workspace: %+v", quarantine.Predicates)
	}
	for _, action := range mission.AllActionIDs() {
		if !seen[action] {
			t.Fatalf("capability omitted from mission method: %s", action)
		}
	}

	first := softwaredev.CapabilityDefinition().Templates()
	second := softwaredev.CapabilityDefinition().Templates()
	first[0].Predicates = nil
	if slices.Equal(first[0].Predicates, second[0].Predicates) {
		t.Fatal("definition returned aliased predicate storage")
	}
}
