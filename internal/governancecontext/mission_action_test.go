package governancecontext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestMissionActionGovernanceBindsExactSealedContext(t *testing.T) {
	context := actionContext(t, "governance-original", "profile-r1")
	selected := context.Entries()
	binding, err := NewMissionAction(MissionActionParams{
		Source: context, MissionID: "mission-1", StepKey: "planning", ActionKey: "mission.plan",
		ActorID: "actor-1", SelectedEntries: selected,
	})
	if err != nil {
		t.Fatalf("construct binding: %v", err)
	}
	selected[0] = Entry{}
	if binding.ContextID() != context.ID() || binding.Aggregate() != context.Aggregate() || len(binding.Entries()) != 4 {
		t.Fatal("binding did not retain exact sealed context evidence defensively")
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored MissionAction
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !binding.Equal(restored) || restored.Validate() != nil {
		t.Fatal("mission action governance did not round trip")
	}
	var tampered map[string]any
	if err := json.Unmarshal(encoded, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["aggregate"].(map[string]any)["digest"] = strings.Repeat("0", 64)
	tamperedBytes, _ := json.Marshal(tampered)
	if json.Unmarshal(tamperedBytes, &restored) == nil {
		t.Fatal("valid-shaped aggregate tamper was accepted")
	}
	for _, forbidden := range []string{"secret", "transcript", "environment", "/home/"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("encoded governance leaked %q", forbidden)
		}
	}

	for i := 0; i < 100; i++ {
		_ = actionContext(t, ids.GovernanceContextID("governance-new-"+string(rune('a'+i%26))), "profile-r2")
		var historical MissionAction
		if err := json.Unmarshal(encoded, &historical); err != nil || !historical.Equal(binding) {
			t.Fatalf("historical reconstruction %d changed: %v", i, err)
		}
	}
}

func TestMissionActionGovernanceRefusesTamperingAndOvercollection(t *testing.T) {
	context := actionContext(t, "governance-action", "profile-r1")
	base := MissionActionParams{Source: context, MissionID: "mission-1", StepKey: "planning", ActionKey: "mission.plan", ActorID: "actor-1", SelectedEntries: context.Entries()}
	tests := map[string]func(*MissionActionParams){
		"missing profile":      func(p *MissionActionParams) { p.SelectedEntries = p.SelectedEntries[1:] },
		"missing mission type": func(p *MissionActionParams) { p.SelectedEntries = p.SelectedEntries[:3] },
		"duplicate selection":  func(p *MissionActionParams) { p.SelectedEntries = append(p.SelectedEntries, p.SelectedEntries[0]) },
		"blank action":         func(p *MissionActionParams) { p.ActionKey = " " },
		"unknown action":       func(p *MissionActionParams) { p.ActionKey = "mission.root" },
		"wrong mission":        func(p *MissionActionParams) { p.MissionID = "bad mission" },
		"tampered revision": func(p *MissionActionParams) {
			entry := p.SelectedEntries[0]
			p.SelectedEntries[0] = mustActionEntry(t, entry.Kind(), entry.Ref(), "changed", entry.Commitment())
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.SelectedEntries = append([]Entry(nil), base.SelectedEntries...)
			mutate(&candidate)
			if _, err := NewMissionAction(candidate); err == nil {
				t.Fatal("expected typed refusal")
			} else if _, ok := err.(*ActionValidationError); !ok {
				t.Fatalf("unexpected error type %T", err)
			}
		})
	}
}

func TestMissionActionPolicyIsPerActionAndMinimal(t *testing.T) {
	context := actionContext(t, "governance-policy", "profile-r1")
	entries := context.Entries()
	withoutConfig := make([]Entry, 0, 3)
	for _, entry := range entries {
		if entry.Kind() != KindConfig {
			withoutConfig = append(withoutConfig, entry)
		}
	}
	if _, err := NewMissionAction(MissionActionParams{Source: context, MissionID: "mission-1", StepKey: "planning", ActionKey: "mission.resolve-decision", ActorID: "actor-1", SelectedEntries: withoutConfig}); err != nil {
		t.Fatalf("decision policy should not collect config: %v", err)
	}
	if _, err := NewMissionAction(MissionActionParams{Source: context, MissionID: "mission-1", StepKey: "planning", ActionKey: "mission.resolve-decision", ActorID: "actor-1", SelectedEntries: entries}); err == nil {
		t.Fatal("decision policy accepted unrelated config")
	}
	if _, err := NewMissionAction(MissionActionParams{Source: context, MissionID: "mission-1", StepKey: "planning", ActionKey: "mission.plan", ActorID: "actor-1", SelectedEntries: withoutConfig}); err == nil {
		t.Fatal("configured lifecycle action accepted missing config")
	}
}

func actionContext(t *testing.T, id ids.GovernanceContextID, profileRevision string) Context {
	t.Helper()
	entries := []Entry{
		mustActionEntry(t, KindConfig, "config.default", "config-r1", actionCommitment(t, "config")),
		mustActionEntry(t, KindDoctrine, "doctrine.core", "doctrine-r1", actionCommitment(t, "doctrine")),
		mustActionEntry(t, KindProfile, "profile.implementer", profileRevision, actionCommitment(t, profileRevision)),
		mustActionEntry(t, KindMissionType, "mission-type.software-development", "v1", actionCommitment(t, "mission-type")),
	}
	value, err := NewContext(id, SchemaVersion, entries, time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	return value
}

func actionCommitment(t *testing.T, value string) Commitment {
	t.Helper()
	commitment, err := CommitSHA256([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return commitment
}

func mustActionEntry(t *testing.T, kind EntryKind, ref, revision string, commitment Commitment) Entry {
	t.Helper()
	entry, err := NewEntry(kind, ref, revision, commitment)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}
