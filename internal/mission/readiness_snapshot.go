package mission

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// CapabilityScopeSet records the complete current instance census for one
// action. An empty Scopes member is meaningful: the capability is admitted by
// the mission method but currently has no instance to schedule.
type CapabilityScopeSet struct {
	Action ActionID         `json:"action"`
	Scopes []AuthorityScope `json:"scopes"`
}

// CapabilityScopeCensus is an immutable, authority-attributed inventory. It
// contains one entry for every admitted ActionID, including actions with no
// current instances.
type CapabilityScopeCensus struct {
	authority        ReadinessAuthorityCoordinate
	evidenceIdentity string
	evidenceDigest   string
	sets             []CapabilityScopeSet
	canonical        []byte
}

func NewCapabilityScopeCensus(authority ReadinessAuthorityCoordinate, evidenceIdentity, evidenceDigest string, sets []CapabilityScopeSet) (CapabilityScopeCensus, error) {
	if authority.Validate() != nil || !authority.factSource() || !boundedReadinessText(evidenceIdentity, 128) || !lowerHexDigest(evidenceDigest) || len(sets) != len(allActionIDs) {
		return CapabilityScopeCensus{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_census"}
	}
	values := make([]CapabilityScopeSet, len(sets))
	copy(values, sets)
	sort.Slice(values, func(i, j int) bool { return values[i].Action < values[j].Action })
	expected := slices.Clone(allActionIDs)
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })
	for index := range values {
		entry := &values[index]
		if entry.Action.Validate() != nil || index >= len(expected) || entry.Action != expected[index] {
			return CapabilityScopeCensus{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_census"}
		}
		entry.Scopes = slices.Clone(entry.Scopes)
		sort.Slice(entry.Scopes, func(i, j int) bool { return scopeKey(entry.Scopes[i]) < scopeKey(entry.Scopes[j]) })
		for scopeIndex, scope := range entry.Scopes {
			if scope.Validate() != nil || scopeIndex > 0 && scopeKey(entry.Scopes[scopeIndex-1]) == scopeKey(scope) {
				return CapabilityScopeCensus{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_census"}
			}
		}
	}
	wire := struct {
		Authority        ReadinessAuthorityCoordinate `json:"authority"`
		EvidenceIdentity string                       `json:"evidence_identity"`
		EvidenceDigest   string                       `json:"evidence_sha256"`
		Sets             []CapabilityScopeSet         `json:"sets"`
	}{authority, evidenceIdentity, evidenceDigest, values}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return CapabilityScopeCensus{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_census"}
	}
	return CapabilityScopeCensus{authority: authority, evidenceIdentity: evidenceIdentity, evidenceDigest: evidenceDigest, sets: values, canonical: canonical}, nil
}

func (c CapabilityScopeCensus) IsZero() bool { return len(c.canonical) == 0 }

func (c CapabilityScopeCensus) Sets() []CapabilityScopeSet {
	result := make([]CapabilityScopeSet, len(c.sets))
	for index, value := range c.sets {
		result[index] = CapabilityScopeSet{Action: value.Action, Scopes: slices.Clone(value.Scopes)}
	}
	return result
}

// ReadinessEvidence is the normalized, provider-neutral authority membership
// observed at one captured store revision. Its members are copied and sealed
// before evaluation.
type ReadinessEvidence struct {
	Facts     []PredicateFact
	Decisions []ReadinessDecision
	Blockers  []ReadinessBlocker
	Conflicts []ReadinessConflict
}

// ReadinessSnapshot is the only input accepted by the pure evaluator. Its
// fields are private, its digest is derived from exact canonical membership,
// and it can only be produced by the complete sealing checks below.
type ReadinessSnapshot struct {
	missionID      string
	missionExists  bool
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

func (s ReadinessSnapshot) IsZero() bool                { return len(s.canonical) == 0 }
func (s ReadinessSnapshot) StoreRevision() uint64       { return s.storeRevision }
func (s ReadinessSnapshot) SnapshotDigest() string      { return s.snapshotDigest }
func (s ReadinessSnapshot) CanonicalBytes() []byte      { return slices.Clone(s.canonical) }
func (s ReadinessSnapshot) MissionExists() bool         { return s.missionExists }
func (s ReadinessSnapshot) Lifecycle() MissionLifecycle { return s.lifecycle }

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
func SealReadinessSnapshot(observation ReadinessAuthorityObservationV2, definition CapabilityDefinition, scopes CapabilityScopeCensus, evidence ReadinessEvidence) (ReadinessSnapshot, error) {
	if observation.State.IsZero() || observation.StoreRevision == 0 || definition.IsZero() || scopes.IsZero() || scopes.authority.Revision > observation.StoreRevision {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "snapshot_authority"}
	}
	return sealReadinessSnapshot(string(observation.State.MissionID()), true, observation.StoreRevision, observation.State.Lifecycle(), observation.State.CanonicalBytes(), definition, scopes, evidence)
}

// SealPrecreationReadinessSnapshot gives mission.create a real project-level
// census. No mission row or invented lifecycle is required; all other actions
// are unavailable because the mission does not yet exist.
func SealPrecreationReadinessSnapshot(missionID string, storeRevision uint64, definition CapabilityDefinition, scopes CapabilityScopeCensus, evidence ReadinessEvidence) (ReadinessSnapshot, error) {
	if !boundedReadinessText(missionID, 128) || storeRevision == 0 || definition.IsZero() || scopes.IsZero() || scopes.authority.Revision > storeRevision {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "precreation_authority"}
	}
	return sealReadinessSnapshot(missionID, false, storeRevision, "", nil, definition, scopes, evidence)
}

func sealReadinessSnapshot(missionID string, exists bool, storeRevision uint64, lifecycle MissionLifecycle, missionState []byte, definition CapabilityDefinition, scopes CapabilityScopeCensus, evidence ReadinessEvidence) (ReadinessSnapshot, error) {
	if exists && lifecycle.Validate() != nil {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "lifecycle"}
	}
	templates := definition.Templates()
	sets := scopes.Sets()
	contracts := make([]CapabilityContract, 0, len(sets))
	for index, template := range templates {
		if sets[index].Action != template.Action {
			return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_definition_membership"}
		}
		for _, scope := range sets[index].Scopes {
			if scope.Kind != template.ScopeKind {
				return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_definition_membership"}
			}
			contract, err := template.Instantiate(scope)
			if err != nil {
				return ReadinessSnapshot{}, err
			}
			contracts = append(contracts, contract)
		}
	}
	canonicalContractsValue, err := canonicalContracts(contracts)
	if err != nil || len(canonicalContractsValue) == 0 {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "scope_instances"}
	}
	facts, err := canonicalFacts(evidence.Facts, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	decisions, err := canonicalDecisions(evidence.Decisions, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	blockers, err := canonicalBlockers(evidence.Blockers, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	conflicts, err := canonicalConflicts(evidence.Conflicts, canonicalContractsValue)
	if err != nil {
		return ReadinessSnapshot{}, err
	}
	for _, coordinate := range readinessEvidenceCoordinates(facts, decisions, blockers, conflicts) {
		if coordinate.Revision > storeRevision {
			return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidFact, Field: "future_authority_revision"}
		}
	}
	wire := struct {
		SchemaVersion uint16               `json:"schema_version"`
		MissionID     string               `json:"mission_id"`
		MissionExists bool                 `json:"mission_exists"`
		StoreRevision uint64               `json:"store_revision"`
		Lifecycle     MissionLifecycle     `json:"lifecycle,omitempty"`
		MissionState  json.RawMessage      `json:"mission_state,omitempty"`
		Definition    json.RawMessage      `json:"capability_definition"`
		ScopeCensus   json.RawMessage      `json:"scope_census"`
		Contracts     []CapabilityContract `json:"contracts"`
		Facts         []PredicateFact      `json:"facts"`
		Decisions     []ReadinessDecision  `json:"decisions"`
		Blockers      []ReadinessBlocker   `json:"blockers"`
		Conflicts     []ReadinessConflict  `json:"conflicts"`
	}{ReadinessSchemaVersion, missionID, exists, storeRevision, lifecycle, missionState, definition.canonical, scopes.canonical, canonicalContractsValue, facts, decisions, blockers, conflicts}
	canonical, err := json.Marshal(wire)
	if err != nil {
		return ReadinessSnapshot{}, &ReadinessError{Kind: ReadinessInvalidInput, Field: "snapshot_encoding"}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(canonical))
	return ReadinessSnapshot{missionID: missionID, missionExists: exists, storeRevision: storeRevision, lifecycle: lifecycle, contracts: canonicalContractsValue, facts: facts, decisions: decisions, blockers: blockers, conflicts: conflicts, snapshotDigest: digest, canonical: canonical}, nil
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
