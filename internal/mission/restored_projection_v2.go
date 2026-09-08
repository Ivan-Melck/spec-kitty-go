package mission

import (
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/playback"
)

// MissionRestoredProjectionV2 is the mission-owned query view constructed
// from WP04's already-validated, I/O-free playback result. It does not replay
// events or retain provider handles.
type MissionRestoredProjectionV2 struct {
	missions  map[string]MissionAuthorityStateV2
	readiness map[string]ReadinessAuthorityStateV2
	facts     map[string]MissionFactStateV2
}

func NewMissionRestoredProjectionV2(result playback.ResultV2) (MissionRestoredProjectionV2, error) {
	if err := result.Validate(); err != nil {
		return MissionRestoredProjectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "playback_result", err)
	}
	projection := MissionRestoredProjectionV2{
		missions:  make(map[string]MissionAuthorityStateV2),
		readiness: make(map[string]ReadinessAuthorityStateV2),
		facts:     make(map[string]MissionFactStateV2),
	}
	for _, restored := range result.States() {
		coordinate := restored.Coordinate()
		if coordinate.Kind != "mission" || coordinate.Scope.Kind != journalv2.ScopeMission {
			continue
		}
		if coordinate.ID == journalv2.AggregateID(coordinate.Scope.MissionID) {
			readiness, readinessErr := DecodeReadinessAuthorityStateV2(restored.CanonicalState())
			if readinessErr == nil {
				if readiness.ProjectID() != coordinate.Scope.ProjectID || readiness.MissionID() != coordinate.Scope.MissionID || readiness.Version() != restored.LogicalVersion() || readiness.Digest() != restored.StateDigest() {
					return MissionRestoredProjectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "restored_readiness", nil)
				}
				projection.readiness[missionProjectionKeyV2(readiness.ProjectID(), readiness.MissionID())] = readiness
				continue
			}
			state, err := DecodeMissionAuthorityStateV2(restored.CanonicalState())
			if err != nil || state.ProjectID() != coordinate.Scope.ProjectID || state.MissionID() != coordinate.Scope.MissionID || state.Version() != restored.LogicalVersion() || state.Digest() != restored.StateDigest() {
				return MissionRestoredProjectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "restored_mission", err)
			}
			projection.missions[missionProjectionKeyV2(state.ProjectID(), state.MissionID())] = state
			continue
		}
		fact, err := decodeMissionFactStateV2(restored.CanonicalState())
		if err != nil || fact.input.ProjectID != coordinate.Scope.ProjectID || fact.input.MissionID != coordinate.Scope.MissionID || missionFactAggregateIDV2(fact) != coordinate.ID || fact.LogicalVersion() != restored.LogicalVersion() || fact.Digest() != restored.StateDigest() {
			return MissionRestoredProjectionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "restored_fact", err)
		}
		projection.facts[missionFactProjectionKeyV2(fact.input.ProjectID, fact.input.MissionID, fact.Kind(), fact.FactID())] = fact
	}
	return projection, nil
}

func (p MissionRestoredProjectionV2) Readiness(projectID journalv2.ProjectID, missionID journalv2.MissionID) (ReadinessAuthorityStateV2, bool) {
	state, found := p.readiness[missionProjectionKeyV2(projectID, missionID)]
	return state, found
}

func (p MissionRestoredProjectionV2) Mission(projectID journalv2.ProjectID, missionID journalv2.MissionID) (MissionAuthorityStateV2, bool) {
	state, found := p.missions[missionProjectionKeyV2(projectID, missionID)]
	return state, found
}

func (p MissionRestoredProjectionV2) Fact(projectID journalv2.ProjectID, missionID journalv2.MissionID, kind MissionFactKindV2, factID string) (MissionFactStateV2, bool) {
	state, found := p.facts[missionFactProjectionKeyV2(projectID, missionID, kind, factID)]
	return state, found
}

func missionProjectionKeyV2(projectID journalv2.ProjectID, missionID journalv2.MissionID) string {
	return string(projectID) + "\x00" + string(missionID)
}

func missionFactProjectionKeyV2(projectID journalv2.ProjectID, missionID journalv2.MissionID, kind MissionFactKindV2, factID string) string {
	return missionProjectionKeyV2(projectID, missionID) + "\x00" + string(kind) + "\x00" + factID
}
