package softwaredev

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// EvaluateReadiness seals and evaluates one complete software-development
// authority observation. This is the mission-type composition seam used by
// runtime hosts; the mission kernel remains provider-neutral and pure.
func EvaluateReadiness(observation mission.ReadinessAuthorityObservationV2, scopes mission.CapabilityScopeCensus, evidence mission.ReadinessEvidence) (mission.ReadinessSnapshot, mission.ActionCensus, error) {
	snapshot, err := mission.SealReadinessSnapshot(observation, CapabilityDefinition(), scopes, evidence)
	if err != nil {
		return mission.ReadinessSnapshot{}, mission.ActionCensus{}, err
	}
	census, err := mission.EvaluateReadiness(snapshot)
	if err != nil {
		return mission.ReadinessSnapshot{}, mission.ActionCensus{}, err
	}
	return snapshot, census, nil
}

// EvaluatePrecreationReadiness exposes mission.create through the same
// complete capability census without inventing a mission lifecycle row.
func EvaluatePrecreationReadiness(missionID string, storeRevision uint64, scopes mission.CapabilityScopeCensus, evidence mission.ReadinessEvidence) (mission.ReadinessSnapshot, mission.ActionCensus, error) {
	snapshot, err := mission.SealPrecreationReadinessSnapshot(missionID, storeRevision, CapabilityDefinition(), scopes, evidence)
	if err != nil {
		return mission.ReadinessSnapshot{}, mission.ActionCensus{}, err
	}
	census, err := mission.EvaluateReadiness(snapshot)
	if err != nil {
		return mission.ReadinessSnapshot{}, mission.ActionCensus{}, err
	}
	return snapshot, census, nil
}

// BindAuthorizedActions turns the exact authorized census into the minimal
// immutable governance envelopes accepted by execution. Inapplicable and
// unavailable actions never cross this boundary.
func BindAuthorizedActions(census mission.ActionCensus, source governancecontext.Context, actorID ids.ActorID) ([]governancecontext.CapabilityAction, error) {
	if len(census.CanonicalBytes()) == 0 || source.Validate() != nil {
		return nil, fmt.Errorf("software-development readiness: invalid authority input")
	}
	if _, err := ids.ParseActorID(actorID.String()); err != nil {
		return nil, fmt.Errorf("software-development readiness: invalid actor authority: %w", err)
	}
	selected := make([]governancecontext.Entry, 0, 3)
	selectedKinds := make(map[governancecontext.EntryKind]bool, 3)
	for _, entry := range source.Entries() {
		switch entry.Kind() {
		case governancecontext.KindDoctrine, governancecontext.KindProfile, governancecontext.KindMissionType:
			if selectedKinds[entry.Kind()] {
				return nil, fmt.Errorf("software-development readiness: governance context has duplicate %s entries", entry.Kind())
			}
			selectedKinds[entry.Kind()] = true
			selected = append(selected, entry)
		}
	}
	if len(selected) != 3 {
		return nil, fmt.Errorf("software-development readiness: governance context requires exactly one doctrine, profile, and mission type")
	}
	missionID, err := ids.ParseMissionID(census.MissionID())
	if err != nil {
		return nil, fmt.Errorf("software-development readiness: invalid mission authority: %w", err)
	}
	authorized := census.Authorized()
	bindings := make([]governancecontext.CapabilityAction, 0, len(authorized))
	for _, action := range authorized {
		binding, bindErr := governancecontext.NewCapabilityAction(governancecontext.CapabilityActionParams{
			Source: source, MissionID: missionID, ActionID: string(action.ID), ScopeKind: string(action.Scope.Kind),
			ScopeIdentity: action.Scope.Identity, ScopeDigest: census.SnapshotDigest(), ActorID: actorID, SelectedEntries: selected,
		})
		if bindErr != nil {
			return nil, fmt.Errorf("software-development readiness: bind %s: %w", action.ID, bindErr)
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}
