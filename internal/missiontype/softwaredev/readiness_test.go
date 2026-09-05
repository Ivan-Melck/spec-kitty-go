package softwaredev_test

import (
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
)

func TestAuthorizedCensusBindsDirectlyToMinimalGovernanceActions(t *testing.T) {
	sets := make([]mission.CapabilityScopeSet, 0, len(mission.AllActionIDs()))
	for _, action := range mission.AllActionIDs() {
		set := mission.CapabilityScopeSet{Action: action}
		if action == mission.ActionMissionCreate {
			set.Scopes = []mission.AuthorityScope{{Kind: mission.ScopeMission, Identity: "mission-bind"}}
		}
		sets = append(sets, set)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	scopes, err := mission.NewCapabilityScopeCensus(
		mission.ReadinessAuthorityCoordinate{Family: "mission_authority", Identity: "mission-bind", Revision: 7},
		"scope-census-bind", digest, sets,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, census, err := softwaredev.EvaluatePrecreationReadiness("mission-bind", 7, scopes, mission.ReadinessEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	contextValue := governanceContextFixture(t)
	bindings, err := softwaredev.BindAuthorizedActions(census, contextValue, ids.ActorID("actor-bind"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].ActionID() != string(mission.ActionMissionCreate) || bindings[0].ScopeIdentity() != "mission-bind" || bindings[0].ScopeDigest() != census.SnapshotDigest() {
		t.Fatalf("authorized census did not bind exactly: %+v", bindings)
	}
	if len(bindings[0].Entries()) != 3 || len(bindings[0].Grants()) != 1 || bindings[0].Grants()[0] != governancecontext.GrantLifecycleMutate {
		t.Fatalf("governance binding over-collected or granted excess authority: %+v", bindings[0])
	}
}

func governanceContextFixture(t *testing.T) governancecontext.Context {
	t.Helper()
	makeEntry := func(kind governancecontext.EntryKind, ref string) governancecontext.Entry {
		commitment, err := governancecontext.CommitSHA256([]byte(ref))
		if err != nil {
			t.Fatal(err)
		}
		entry, err := governancecontext.NewEntry(kind, ref, "revision-1", commitment)
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	entries := []governancecontext.Entry{
		makeEntry(governancecontext.KindConfig, "config.default"),
		makeEntry(governancecontext.KindDoctrine, "doctrine.core"),
		makeEntry(governancecontext.KindProfile, "profile.implementer"),
		makeEntry(governancecontext.KindMissionType, "mission-type.software-development"),
	}
	value, err := governancecontext.NewContext(ids.GovernanceContextID("governance-bind"), governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
