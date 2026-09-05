package mission

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// CapabilityAuthorityObservationV2 is an opaque observation produced by one
// capability reader at one exact GapDB capture. Empty dynamic membership is
// evidence too: it must carry an explicit absence witness.
type CapabilityAuthorityObservationV2 struct {
	capture         journalv2.StoreRevisionV2
	projectID       journalv2.ProjectID
	missionID       journalv2.MissionID
	action          ActionID
	scopes          []AuthorityScope
	facts           []PredicateFact
	absenceIdentity string
	absenceDigest   string
	canonical       []byte
}

// SealCapabilityAuthorityObservationV2 is the narrow producer seam reserved
// for capability-owned readers. It binds all membership and predicate facts
// to the same project, mission, database incarnation, and exact revision.
func SealCapabilityAuthorityObservationV2(capture journalv2.StoreRevisionV2, projectID journalv2.ProjectID, missionID journalv2.MissionID, action ActionID, scopes []AuthorityScope, facts []PredicateFact, absenceIdentity, absenceDigest string) (CapabilityAuthorityObservationV2, error) {
	if _, err := journalv2.NewStoreRevisionV2(capture.DatabaseID(), capture.CurrentRevision(), capture.DurableThroughRevision()); err != nil {
		return CapabilityAuthorityObservationV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_capture"}
	}
	if _, err := journalv2.NewProjectID(string(projectID)); err != nil {
		return CapabilityAuthorityObservationV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_project"}
	}
	if _, err := journalv2.NewMissionID(string(missionID)); err != nil || action.Validate() != nil {
		return CapabilityAuthorityObservationV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_mission"}
	}
	values := slices.Clone(scopes)
	sort.Slice(values, func(i, j int) bool { return scopeKey(values[i]) < scopeKey(values[j]) })
	for index, scope := range values {
		if scope.Validate() != nil || index > 0 && scopeKey(values[index-1]) == scopeKey(scope) {
			return CapabilityAuthorityObservationV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_scopes"}
		}
	}
	if len(values) == 0 && (!boundedReadinessText(absenceIdentity, 128) || !lowerHexDigest(absenceDigest)) || len(values) != 0 && (absenceIdentity != "" || absenceDigest != "") {
		return CapabilityAuthorityObservationV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_absence"}
	}
	factValues := slices.Clone(facts)
	for _, fact := range factValues {
		if fact.Action != action || fact.Authority.Revision != capture.CurrentRevision() {
			return CapabilityAuthorityObservationV2{}, &ReadinessError{Kind: ReadinessInvalidFact, Field: "capability_fact_capture"}
		}
	}
	sort.Slice(factValues, func(i, j int) bool {
		left := factKey(ActionRef{ID: factValues[i].Action, Scope: factValues[i].Scope}, factValues[i].Kind)
		right := factKey(ActionRef{ID: factValues[j].Action, Scope: factValues[j].Scope}, factValues[j].Kind)
		return left < right
	})
	wire := struct {
		DatabaseID      journalv2.DatabaseID `json:"database_id"`
		Revision        uint64               `json:"revision"`
		ProjectID       journalv2.ProjectID  `json:"project_id"`
		MissionID       journalv2.MissionID  `json:"mission_id"`
		Action          ActionID             `json:"action"`
		Scopes          []AuthorityScope     `json:"scopes"`
		Facts           []PredicateFact      `json:"facts"`
		AbsenceIdentity string               `json:"absence_evidence_identity,omitempty"`
		AbsenceDigest   string               `json:"absence_evidence_sha256,omitempty"`
	}{capture.DatabaseID(), capture.CurrentRevision(), projectID, missionID, action, values, factValues, absenceIdentity, absenceDigest}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return CapabilityAuthorityObservationV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_observation"}
	}
	return CapabilityAuthorityObservationV2{capture: capture, projectID: projectID, missionID: missionID, action: action, scopes: values, facts: factValues, absenceIdentity: absenceIdentity, absenceDigest: absenceDigest, canonical: canonical}, nil
}

// CapabilityObservationSetV2 is the complete, exact-capture membership for
// every admitted capability action. Its contents are private and immutable.
type CapabilityObservationSetV2 struct {
	capture   journalv2.StoreRevisionV2
	projectID journalv2.ProjectID
	missionID journalv2.MissionID
	values    []CapabilityAuthorityObservationV2
	canonical []byte
}

func SealCapabilityObservationSetV2(capture journalv2.StoreRevisionV2, projectID journalv2.ProjectID, missionID journalv2.MissionID, observations []CapabilityAuthorityObservationV2) (CapabilityObservationSetV2, error) {
	if _, err := journalv2.NewStoreRevisionV2(capture.DatabaseID(), capture.CurrentRevision(), capture.DurableThroughRevision()); err != nil || len(observations) != len(allActionIDs) {
		return CapabilityObservationSetV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_observations"}
	}
	values := slices.Clone(observations)
	sort.Slice(values, func(i, j int) bool { return values[i].action < values[j].action })
	expected := slices.Clone(allActionIDs)
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })
	encoded := make([]json.RawMessage, len(values))
	for index, value := range values {
		if len(value.canonical) == 0 || value.action != expected[index] || !sameReadinessCapture(value.capture, capture) || value.projectID != projectID || value.missionID != missionID {
			return CapabilityObservationSetV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_observations"}
		}
		encoded[index] = value.canonical
	}
	canonical, err := json.Marshal(encoded)
	if err != nil {
		return CapabilityObservationSetV2{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "capability_observations"}
	}
	return CapabilityObservationSetV2{capture: capture, projectID: projectID, missionID: missionID, values: values, canonical: canonical}, nil
}

func (s CapabilityObservationSetV2) IsZero() bool { return len(s.canonical) == 0 }

// ReadinessSnapshot is the only input accepted by the pure evaluator. Its
// fields are private, its digest is derived from exact canonical membership,
// and it can only be produced by the complete sealing checks below.
type ReadinessSnapshot struct {
	missionID      string
	missionExists  bool
	databaseID     journalv2.DatabaseID
	storeRevision  uint64
	lifecycle      MissionLifecycle
	contracts      []CapabilityContract
	facts          []PredicateFact
	decisions      []ReadinessDecision
	blockers       []ReadinessBlocker
	conflicts      []ReadinessConflict
	snapshotDigest string
	canonical      []byte
}

func (s ReadinessSnapshot) IsZero() bool                     { return len(s.canonical) == 0 }
func (s ReadinessSnapshot) DatabaseID() journalv2.DatabaseID { return s.databaseID }
func (s ReadinessSnapshot) StoreRevision() uint64            { return s.storeRevision }
func (s ReadinessSnapshot) SnapshotDigest() string           { return s.snapshotDigest }
func (s ReadinessSnapshot) CanonicalBytes() []byte           { return slices.Clone(s.canonical) }
func (s ReadinessSnapshot) MissionExists() bool              { return s.missionExists }
func (s ReadinessSnapshot) Lifecycle() MissionLifecycle      { return s.lifecycle }

func (s ReadinessSnapshot) input() readinessInput {
	return readinessInput{
		MissionID: s.missionID, MissionExists: s.missionExists, Revision: s.storeRevision,
		SnapshotDigest: s.snapshotDigest, Lifecycle: s.lifecycle, Contracts: cloneContracts(s.contracts),
		Facts: slices.Clone(s.facts), Decisions: cloneDecisions(s.decisions), Blockers: cloneBlockers(s.blockers), Conflicts: cloneConflicts(s.conflicts),
	}
}

// SealReadinessSnapshot binds a complete mission-method definition and scope
// census to one exact GapDB observation. Facts from a later or uncommitted
// authority revision are rejected.
func SealReadinessSnapshot(observation ReadinessAuthorityObservationV2, definition CapabilityDefinition, observations CapabilityObservationSetV2) (ReadinessSnapshot, error) {
	capture := observation.capture
	if observation.state.IsZero() || !sameReadinessCapture(observations.capture, capture) || observation.recordRevision == 0 || observation.recordRevision > capture.CurrentRevision() || definition.IsZero() || observations.IsZero() || observations.projectID != observation.state.ProjectID() || observations.missionID != observation.state.MissionID() {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "snapshot_authority"}
	}
	return sealReadinessSnapshot(string(observation.state.MissionID()), true, capture, observation.state.Lifecycle(), observation.state.CanonicalBytes(), observation.state.ActiveFacts(), definition, observations)
}

// SealPrecreationReadinessSnapshot gives mission.create a real project-level
// census. No mission row or invented lifecycle is required; all other actions
// are unavailable because the mission does not yet exist.
func SealPrecreationReadinessSnapshot(projectID journalv2.ProjectID, missionID journalv2.MissionID, capture journalv2.StoreRevisionV2, definition CapabilityDefinition, observations CapabilityObservationSetV2) (ReadinessSnapshot, error) {
	if _, err := journalv2.NewProjectID(string(projectID)); err != nil || !boundedReadinessText(string(missionID), 128) || capture.CurrentRevision() == 0 || definition.IsZero() || observations.IsZero() || !sameReadinessCapture(observations.capture, capture) || observations.projectID != projectID || observations.missionID != missionID {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "precreation_authority"}
	}
	return sealReadinessSnapshot(string(missionID), false, capture, "", nil, nil, definition, observations)
}

func sealReadinessSnapshot(missionID string, exists bool, capture journalv2.StoreRevisionV2, lifecycle MissionLifecycle, missionState []byte, activeFacts []ScopedMissionFactV2, definition CapabilityDefinition, observations CapabilityObservationSetV2) (ReadinessSnapshot, error) {
	if exists && lifecycle.Validate() != nil {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "lifecycle"}
	}
	templates := definition.Templates()
	contracts := make([]CapabilityContract, 0, len(observations.values))
	factMembership := make([]PredicateFact, 0)
	for index, template := range templates {
		observed := observations.values[index]
		if observed.action != template.Action {
			return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_definition_membership"}
		}
		scopes := observed.scopes
		if template.ScopeKind == ScopeMission {
			if len(observed.scopes) != 0 || observed.absenceIdentity == "" {
				return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "mission_scope_must_be_derived"}
			}
			scopes = []AuthorityScope{{Kind: ScopeMission, Identity: missionID}}
		}
		for _, scope := range scopes {
			if scope.Kind != template.ScopeKind {
				return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_definition_membership"}
			}
			contract, err := template.Instantiate(scope)
			if err != nil {
				return ReadinessSnapshot{}, err
			}
			contracts = append(contracts, contract)
		}
		factMembership = append(factMembership, observed.facts...)
	}
	canonicalContractsValue, err := canonicalContracts(contracts)
	if err != nil || len(canonicalContractsValue) == 0 {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_instances"}
	}
	facts, err := canonicalFacts(factMembership, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	decisionsRaw, blockersRaw, conflictsRaw := readinessMissionFacts(activeFacts, capture.CurrentRevision())
	decisions, err := canonicalDecisions(decisionsRaw, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	blockers, err := canonicalBlockers(blockersRaw, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	conflicts, err := canonicalConflicts(conflictsRaw, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	for _, coordinate := range readinessEvidenceCoordinates(facts, decisions, blockers, conflicts) {
		if coordinate.Revision != capture.CurrentRevision() {
			return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidFact, Field: "non_capture_authority_revision"}
		}
	}
	wire := struct {
		SchemaVersion uint16               `json:"schema_version"`
		MissionID     string               `json:"mission_id"`
		MissionExists bool                 `json:"mission_exists"`
		DatabaseID    journalv2.DatabaseID `json:"database_id"`
		StoreRevision uint64               `json:"store_revision"`
		Lifecycle     MissionLifecycle     `json:"lifecycle,omitempty"`
		MissionState  json.RawMessage      `json:"mission_state,omitempty"`
		Definition    json.RawMessage      `json:"capability_definition"`
		Observations  json.RawMessage      `json:"capability_observations"`
		Contracts     []CapabilityContract `json:"contracts"`
		Facts         []PredicateFact      `json:"facts"`
		Decisions     []ReadinessDecision  `json:"decisions"`
		Blockers      []ReadinessBlocker   `json:"blockers"`
		Conflicts     []ReadinessConflict  `json:"conflicts"`
	}{ReadinessSchemaVersion, missionID, exists, capture.DatabaseID(), capture.CurrentRevision(), lifecycle, missionState, definition.canonical, observations.canonical, canonicalContractsValue, facts, decisions, blockers, conflicts}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "snapshot_encoding"}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(canonical))
	return ReadinessSnapshot{missionID: missionID, missionExists: exists, databaseID: capture.DatabaseID(), storeRevision: capture.CurrentRevision(), lifecycle: lifecycle, contracts: canonicalContractsValue, facts: facts, decisions: decisions, blockers: blockers, conflicts: conflicts, snapshotDigest: digest, canonical: canonical}, nil
}

func sameReadinessCapture(left, right journalv2.StoreRevisionV2) bool {
	return left.DatabaseID() == right.DatabaseID() && left.CurrentRevision() == right.CurrentRevision() && left.DurableThroughRevision() == right.DurableThroughRevision()
}

func readinessMissionFacts(values []ScopedMissionFactV2, revision uint64) ([]ReadinessDecision, []ReadinessBlocker, []ReadinessConflict) {
	decisions := make([]ReadinessDecision, 0)
	blockers := make([]ReadinessBlocker, 0)
	conflicts := make([]ReadinessConflict, 0)
	for _, value := range values {
		coordinate := ReadinessAuthorityCoordinate{Identity: value.FactID, Revision: revision}
		switch value.Kind {
		case ScopedFactDecisionV2:
			coordinate.Family = "mission_decision"
			decisions = append(decisions, ReadinessDecision{ID: value.FactID, Affects: []ActionRef{value.Scope}, Authority: coordinate, EvidenceIdentity: value.EvidenceIdentity, EvidenceDigest: value.EvidenceDigest})
		case ScopedFactBlockerV2:
			coordinate.Family = "mission_blocker"
			blockers = append(blockers, ReadinessBlocker{ID: value.FactID, Affects: []ActionRef{value.Scope}, Authority: coordinate, EvidenceIdentity: value.EvidenceIdentity, EvidenceDigest: value.EvidenceDigest, ResponsibleCapability: ActionBlockerClear, Recovery: ReadinessRecoverBlocker})
		case ScopedFactConflictV2:
			coordinate.Family = "mission_conflict"
			conflicts = append(conflicts, ReadinessConflict{ID: value.FactID, Affects: []ActionRef{value.Scope}, Authority: coordinate, EvidenceIdentity: value.EvidenceIdentity, EvidenceDigest: value.EvidenceDigest})
		}
	}
	return decisions, blockers, conflicts
}

func readinessEvidenceCoordinates(facts []PredicateFact, decisions []ReadinessDecision, blockers []ReadinessBlocker, conflicts []ReadinessConflict) []ReadinessAuthorityCoordinate {
	result := make([]ReadinessAuthorityCoordinate, 0, len(facts)+len(decisions)+len(blockers)+len(conflicts))
	for _, value := range facts {
		result = append(result, value.Authority)
	}
	for _, value := range decisions {
		result = append(result, value.Authority)
	}
	for _, value := range blockers {
		result = append(result, value.Authority)
	}
	for _, value := range conflicts {
		result = append(result, value.Authority)
	}
	return result
}

func cloneContracts(values []CapabilityContract) []CapabilityContract {
	result := slices.Clone(values)
	for index := range result {
		result[index].Predicates = slices.Clone(result[index].Predicates)
		if result[index].Applicability != nil {
			copyValue := *result[index].Applicability
			result[index].Applicability = &copyValue
		}
	}
	return result
}

func scopeKey(scope AuthorityScope) string {
	return string(scope.Kind) + "\x00" + scope.Identity + "\x00" + scope.Commitment
}
