package workgraph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// AuthorityPortV2 is the workgraph-owned durable publication contract.
type AuthorityPortV2 interface {
	Publish(context.Context, AuthorityPlanV2) (journalv2.AcknowledgedPublicationV2, error)
}

const (
	WorkgraphEventSchemaV2  = uint32(1)
	WorkgraphReducerV2      = uint32(1)
	workgraphStateSchemaV2  = uint16(1)
	workgraphResultSchemaV2 = uint32(1)
)

type workgraphEventKindV2 string

const (
	workgraphFinalizedV2  workgraphEventKindV2 = "finalized"
	workgraphReconciledV2 workgraphEventKindV2 = "reconciled"
)

// WorkgraphStateV2 is the immutable reducer result at one workgraph head. The
// historical event stream retains earlier generations; this state names the
// current generation and the exact reconciliation that selected it.
type WorkgraphStateV2 struct {
	SchemaVersion   uint16          `json:"schema_version"`
	ProjectID       ids.ProjectID   `json:"project_id"`
	MissionID       ids.MissionID   `json:"mission_id"`
	LogicalVersion  uint64          `json:"logical_version"`
	CurrentRevision json.RawMessage `json:"current_revision"`
	Reconciliation  json.RawMessage `json:"reconciliation,omitempty"`
}

type workgraphEventV2 struct {
	SchemaVersion  uint16               `json:"schema_version"`
	Kind           workgraphEventKindV2 `json:"kind"`
	Revision       json.RawMessage      `json:"revision"`
	Reconciliation json.RawMessage      `json:"reconciliation,omitempty"`
}

type reconciliationDecisionWireV2 struct {
	OldWorkID   ids.WorkID   `json:"old_work_id"`
	Disposition Disposition  `json:"disposition"`
	NewWorkIDs  []ids.WorkID `json:"new_work_ids"`
	ForkEdges   []ForkEdge   `json:"fork_edges"`
	Rationale   string       `json:"rationale"`
	Status      WorkStatus   `json:"work_status"`
}

type reconciliationWireV2 struct {
	SchemaVersion     uint16                         `json:"schema_version"`
	ID                string                         `json:"reconciliation_id"`
	DriftReportID     string                         `json:"drift_report_id"`
	OldRevisionID     ids.WorkRevisionID             `json:"old_work_revision_id"`
	NewRevisionID     ids.WorkRevisionID             `json:"new_work_revision_id"`
	OldGraphDigest    string                         `json:"old_graph_digest"`
	NewGraphDigest    string                         `json:"new_graph_digest"`
	OldCriteriaDigest string                         `json:"old_criteria_digest"`
	NewCriteriaDigest string                         `json:"new_criteria_digest"`
	DriftObservedAt   string                         `json:"drift_observed_at"`
	Decisions         []reconciliationDecisionWireV2 `json:"decisions"`
	ActorID           ids.ActorID                    `json:"actor_id"`
	Rationale         string                         `json:"rationale"`
	OccurredAt        string                         `json:"occurred_at"`
	RequestDigest     string                         `json:"request_digest"`
}

func encodeMixedReconciliationV2(value MixedReconciliation) ([]byte, error) {
	if value.ID() == "" || value.RequestDigest() == "" || len(value.Decisions()) == 0 {
		return nil, newError(KindInvalidReconciliation, "reconciliation authority is incomplete", nil)
	}
	decisions := make([]reconciliationDecisionWireV2, 0, len(value.Decisions()))
	for _, decision := range value.Decisions() {
		status, ok := value.AssignedStatus(decision.OldWorkID())
		if !ok {
			return nil, newError(KindInvalidReconciliation, "reconciliation status is incomplete", nil)
		}
		decisions = append(decisions, reconciliationDecisionWireV2{decision.OldWorkID(), decision.Disposition(), decision.NewWorkIDs(), decision.ForkEdges(), decision.Rationale(), status})
	}
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].OldWorkID < decisions[j].OldWorkID })
	wire := reconciliationWireV2{SchemaVersion: 2, ID: value.ID(), DriftReportID: value.DriftReportID(), OldRevisionID: value.OldRevisionID(), NewRevisionID: value.NewRevisionID(), OldGraphDigest: value.OldGraphDigest(), NewGraphDigest: value.NewGraphDigest(),
		OldCriteriaDigest: value.OldCriteriaDigest(), NewCriteriaDigest: value.NewCriteriaDigest(), DriftObservedAt: value.DriftObservedAt().Format("2006-01-02T15:04:05.999999999Z07:00"), Decisions: decisions, ActorID: value.ActorID(), Rationale: value.Rationale(), OccurredAt: value.OccurredAt().Format("2006-01-02T15:04:05.999999999Z07:00"), RequestDigest: value.RequestDigest()}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func decodeMixedReconciliationV2(encoded []byte, oldRevision, newRevision RevisionV2) (MixedReconciliation, error) {
	var wire reconciliationWireV2
	if err := decodeStrictWorkgraphJSON(encoded, &wire); err != nil || wire.SchemaVersion != 2 {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "reconciliation authority is malformed", nil)
	}
	driftObservedAt, err := time.Parse(time.RFC3339Nano, wire.DriftObservedAt)
	if err != nil || driftObservedAt.Location() != time.UTC {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "drift occurrence evidence is invalid", nil)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, wire.OccurredAt)
	if err != nil || occurredAt.Location() != time.UTC {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "reconciliation occurrence evidence is invalid", nil)
	}
	drift, err := DetectDrift(oldRevision, newRevision, driftObservedAt)
	if err != nil || drift.ID() != wire.DriftReportID {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "drift evidence does not reconstruct", nil)
	}
	statuses := make([]WorkStatusAssignment, 0, len(wire.Decisions))
	decisions := make([]WorkDispositionDecisionParams, 0, len(wire.Decisions))
	for _, decision := range wire.Decisions {
		statuses = append(statuses, WorkStatusAssignment{WorkID: decision.OldWorkID, Status: decision.Status})
		decisions = append(decisions, WorkDispositionDecisionParams{OldWorkID: decision.OldWorkID, Disposition: decision.Disposition, NewWorkIDs: append([]ids.WorkID(nil), decision.NewWorkIDs...), ForkEdges: append([]ForkEdge(nil), decision.ForkEdges...), Rationale: decision.Rationale})
	}
	reconstructed, err := ReconcileMixed(MixedReconcileRequest{ID: wire.ID, Old: oldRevision, New: newRevision, Drift: drift, Statuses: statuses, Decisions: decisions, ActorID: wire.ActorID, Rationale: wire.Rationale, OccurredAt: occurredAt})
	if err != nil || reconstructed.RequestDigest() != wire.RequestDigest || reconstructed.OldRevisionID() != wire.OldRevisionID || reconstructed.NewRevisionID() != wire.NewRevisionID || reconstructed.OldGraphDigest() != wire.OldGraphDigest || reconstructed.NewGraphDigest() != wire.NewGraphDigest || reconstructed.OldCriteriaDigest() != wire.OldCriteriaDigest || reconstructed.NewCriteriaDigest() != wire.NewCriteriaDigest {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "reconciliation authority does not reconstruct exactly", nil)
	}
	reencoded, err := encodeMixedReconciliationV2(reconstructed)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return MixedReconciliation{}, newError(KindInvalidReconciliation, "reconciliation authority is not canonical", nil)
	}
	return reconstructed, nil
}

func decodeStrictWorkgraphJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing workgraph JSON")
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return fmt.Errorf("non-canonical workgraph JSON")
	}
	return nil
}

// ReduceWorkgraphEventV2 is the only workgraph-family reducer registered for
// schema/reducer tuple 1/1. It consumes recorded bytes only.
func ReduceWorkgraphEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	coordinate := event.Coordinate()
	if event.Family() != "workgraph" || event.EventSchemaVersion() != WorkgraphEventSchemaV2 || event.ReducerVersion() != WorkgraphReducerV2 || coordinate.Kind != "workgraph" {
		return nil, newError(KindInvalidRevision, "unknown workgraph event tuple", nil)
	}
	var payload workgraphEventV2
	if err := decodeStrictWorkgraphJSON(event.Payload(), &payload); err != nil || payload.SchemaVersion != workgraphStateSchemaV2 {
		return nil, newError(KindInvalidRevision, "workgraph event payload is malformed", nil)
	}
	revision, err := DecodeRevisionV2(payload.Revision)
	if err != nil {
		return nil, err
	}
	if revision.SchemaVersion() != RevisionSchemaV3 || revision.ContentDigest() == "" {
		return nil, newError(KindInvalidRevision, "workgraph authority event requires WorkRevision schema v3", nil)
	}
	if coordinate.Scope.Kind != journalv2.ScopeMission || coordinate.ID != journalv2.AggregateID(revision.MissionID()) || coordinate.Scope.ProjectID != journalv2.ProjectID(revision.ProjectID()) || coordinate.Scope.MissionID != journalv2.MissionID(revision.MissionID()) {
		return nil, newError(KindInvalidRevision, "workgraph event scope is mismatched", nil)
	}
	wantVersion := uint64(event.LogicalVersion())
	state := WorkgraphStateV2{SchemaVersion: workgraphStateSchemaV2, ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), LogicalVersion: wantVersion, CurrentRevision: append(json.RawMessage(nil), payload.Revision...)}
	switch payload.Kind {
	case workgraphFinalizedV2:
		if len(payload.Reconciliation) != 0 {
			return nil, newError(KindInvalidRevision, "finalization contains reconciliation", nil)
		}
		if !workgraphReducerInitialV2(prior) || wantVersion != 1 || revision.Generation() != 1 || revision.PredecessorID() != "" {
			return nil, newError(KindInvalidRevision, "first finalized generation is invalid", nil)
		}
	case workgraphReconciledV2:
		if workgraphReducerInitialV2(prior) || len(payload.Reconciliation) == 0 {
			return nil, newError(KindInvalidReconciliation, "reconciliation requires prior authority", nil)
		}
		previous, err := DecodeWorkgraphStateV2(prior)
		if err != nil || previous.LogicalVersion+1 != wantVersion {
			return nil, newError(KindInvalidReconciliation, "reconciliation logical version is stale", nil)
		}
		old, err := DecodeRevisionV2(previous.CurrentRevision)
		if err != nil {
			return nil, err
		}
		reconciliation, err := decodeMixedReconciliationV2(payload.Reconciliation, old, revision)
		if err != nil || reconciliation.OldRevisionID() != old.ID() || reconciliation.NewRevisionID() != revision.ID() {
			return nil, newError(KindInvalidReconciliation, "reconciliation does not bind the exact revision pair", nil)
		}
		state.Reconciliation = append(json.RawMessage(nil), payload.Reconciliation...)
	default:
		return nil, newError(KindInvalidRevision, "workgraph event kind is unsupported", nil)
	}
	return encodeWorkgraphStateV2(state)
}

func workgraphReducerInitialV2(prior []byte) bool {
	return len(prior) == 0 || bytes.Equal(prior, []byte(`{}`))
}

// ReducerRegistrationsV2 binds checkpoint compatibility to the exact
// workgraph authority reducer used by playback and live acknowledgement.
func ReducerRegistrationsV2() []journalv2.ReducerRegistration {
	digestBytes := sha256.Sum256([]byte("spec-kitty/workgraph-authority-reducer/1.1/complete-reconciliation"))
	digest, _ := journalv2.ParseDigest("workgraph_reducer_implementation", fmt.Sprintf("%x", digestBytes))
	return []journalv2.ReducerRegistration{{Family: "workgraph", EventSchemaVersion: WorkgraphEventSchemaV2, ReducerVersion: WorkgraphReducerV2, Owner: "internal/workgraph", ImplementationDigest: digest, Reduce: ReduceWorkgraphEventV2}}
}

func encodeWorkgraphStateV2(state WorkgraphStateV2) ([]byte, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeWorkgraphStateV2(encoded []byte) (WorkgraphStateV2, error) {
	var state WorkgraphStateV2
	if err := decodeStrictWorkgraphJSON(encoded, &state); err != nil || state.SchemaVersion != workgraphStateSchemaV2 || state.LogicalVersion == 0 {
		return WorkgraphStateV2{}, newError(KindInvalidRevision, "workgraph state is malformed", nil)
	}
	revision, err := DecodeRevisionV2(state.CurrentRevision)
	if err != nil || revision.SchemaVersion() != RevisionSchemaV3 || revision.ContentDigest() == "" || revision.ProjectID() != state.ProjectID || revision.MissionID() != state.MissionID {
		return WorkgraphStateV2{}, newError(KindInvalidRevision, "workgraph state revision is mismatched", nil)
	}
	return state, nil
}

type AuthorityContextV2 struct {
	OperationID       journalv2.OperationID
	RuntimeEpochID    journalv2.RuntimeEpochID
	ActorID           journalv2.ActorID
	ProcessID         journalv2.ProcessID
	DoctrineRevision  string
	ProfileRevision   string
	PolicyRevision    string
	Governance        []string
	ObservedCreatedAt string
	OperationEventID  journalv2.EventID
	DomainEventID     journalv2.EventID
}

type FinalizationPlanInputV2 struct {
	Context                    AuthorityContextV2
	Revision                   RevisionV2
	PriorState                 []byte
	ExpectedHeadRecordRevision uint64
}

type ReconciliationPlanInputV2 struct {
	Context                    AuthorityContextV2
	PriorState                 []byte
	Successor                  RevisionV2
	Reconciliation             MixedReconciliation
	ExpectedHeadRecordRevision uint64
	ObservedGuards             map[journalv2.EligibilityShardID]EligibilityShardObservationV2
}

// AuthorityPlanV2 is minted only by workgraph command builders and is the
// capability presented to the GapDB adapter. Family-count lookalikes cannot be
// wrapped by callers.
type AuthorityPlanV2 struct {
	sealed journalv2.SealedCommitPlanV2
}

func (p AuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, newError(KindInvalidRevision, "workgraph authority plan is absent", nil)
	}
	return p.sealed, nil
}

func BuildFinalizationAuthorityV2(input FinalizationPlanInputV2) (AuthorityPlanV2, error) {
	sealed, err := BuildFinalizationPlanV2(input)
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	return AuthorityPlanV2{sealed: sealed}, nil
}

func BuildReconciliationAuthorityV2(input ReconciliationPlanInputV2) (AuthorityPlanV2, error) {
	sealed, err := BuildReconciliationPlanV2(input)
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	return AuthorityPlanV2{sealed: sealed}, nil
}

func BuildFinalizationPlanV2(input FinalizationPlanInputV2) (journalv2.SealedCommitPlanV2, error) {
	if input.Revision.SchemaVersion() != RevisionSchemaV3 || input.Revision.ContentDigest() == "" || len(input.PriorState) != 0 || input.ExpectedHeadRecordRevision != 0 || input.Revision.Generation() != 1 || input.Revision.PredecessorID() != "" {
		return journalv2.SealedCommitPlanV2{}, newError(KindInvalidRevision, "new authority requires complete content-addressed WorkRevision schema v3", nil)
	}
	revision, err := input.Revision.Encode()
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	payload, err := canonicalWorkgraphEventV2(workgraphEventV2{SchemaVersion: workgraphStateSchemaV2, Kind: workgraphFinalizedV2, Revision: revision})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	next, err := reduceDraftWorkgraphV2(input.PriorState, input, payload)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	shards, err := BuildInitialEligibilityGuardShards(input.Revision, string(input.Context.OperationID), allEligibilityOperationsV2())
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	guards, err := eligibilityTransitionsV2(shards, nil)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	return sealWorkgraphPlanV2(input.Context, input.Revision.ProjectID(), input.Revision.MissionID(), input.PriorState, next, payload, "workgraph.finalized", input.ExpectedHeadRecordRevision, guards)
}

func BuildReconciliationPlanV2(input ReconciliationPlanInputV2) (journalv2.SealedCommitPlanV2, error) {
	prior, err := DecodeWorkgraphStateV2(input.PriorState)
	if err != nil || input.Successor.SchemaVersion() != RevisionSchemaV3 || prior.MissionID != input.Successor.MissionID() || input.Reconciliation.OldRevisionID().String() == "" || input.Reconciliation.NewRevisionID() != input.Successor.ID() {
		return journalv2.SealedCommitPlanV2{}, newError(KindInvalidReconciliation, "reconciliation input is outside the current graph", nil)
	}
	revision, err := input.Successor.Encode()
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	reconciliation, err := encodeMixedReconciliationV2(input.Reconciliation)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	payload, err := canonicalWorkgraphEventV2(workgraphEventV2{SchemaVersion: workgraphStateSchemaV2, Kind: workgraphReconciledV2, Revision: revision, Reconciliation: reconciliation})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	next, err := reduceDraftWorkgraphV2(input.PriorState, FinalizationPlanInputV2{Revision: input.Successor}, payload)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	changed, err := BuildEligibilityGuardShards(input.Successor, input.Reconciliation, allEligibilityOperationsV2())
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	changedIDs := make(map[journalv2.EligibilityShardID]struct{}, len(changed))
	for _, shard := range changed {
		changedIDs[shard.ShardID()] = struct{}{}
	}
	if len(input.ObservedGuards) != len(changedIDs) {
		return journalv2.SealedCommitPlanV2{}, newError(KindInvalidReconciliation, "eligibility observations must exactly cover every changed shard", nil)
	}
	for shardID, observed := range input.ObservedGuards {
		missionID, observedShardID := observed.identity()
		if _, required := changedIDs[shardID]; !required || observedShardID != shardID || missionID != input.Successor.MissionID() {
			return journalv2.SealedCommitPlanV2{}, newError(KindInvalidReconciliation, "observed eligibility shard is outside the reconciliation update", nil)
		}
	}
	guards := make([]journalv2.EligibilityGuardTransition, 0, len(changed))
	for _, shard := range changed {
		observed := input.ObservedGuards[shard.ShardID()]
		existing, expectedRevision := observed.prior()
		entries, err := mergeGuardEntriesV2(existing, shard.Entries(), shard.revocationSet())
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		if len(entries) > EligibilityMaxEntriesPerShardV2 {
			return journalv2.SealedCommitPlanV2{}, newError(KindInvalidReconciliation, "eligibility shard update exceeds its fixed budget", nil)
		}
		shard.entries = entries
		shard.revocations = nil
		canonical, err := shard.CanonicalBytes()
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		guards = append(guards, journalv2.EligibilityGuardTransition{MissionID: journalv2.MissionID(input.Successor.MissionID()), ShardID: shard.ShardID(), Canonical: canonical, ExpectedRecordRevision: expectedRevision})
	}
	return sealWorkgraphPlanV2(input.Context, input.Successor.ProjectID(), input.Successor.MissionID(), input.PriorState, next, payload, "workgraph.reconciled", input.ExpectedHeadRecordRevision, guards)
}

func mergeGuardEntriesV2(existing, changed []EligibilityGuardEntryV2, revocations []eligibilityRevocationV2) ([]EligibilityGuardEntryV2, error) {
	byKey := make(map[string]EligibilityGuardEntryV2, len(existing)+len(changed))
	for _, entry := range existing {
		key := eligibilityEntryKeyV2(entry)
		combined, err := combineEligibilityEntriesV2(byKey[key], entry)
		if err != nil {
			return nil, err
		}
		byKey[key] = combined
	}
	seenRevocations := make(map[eligibilityRevocationV2]struct{}, len(revocations))
	for _, revocation := range revocations {
		if _, seen := seenRevocations[revocation]; seen || revocation.WorkID == "" || revocation.WorkRevisionID == "" {
			return nil, newError(KindInvalidReconciliation, "eligibility revocation is incomplete or duplicated", nil)
		}
		seenRevocations[revocation] = struct{}{}
		key := revocation.WorkID.String()
		entry, exists := byKey[key]
		if !exists {
			continue
		}
		kept := entry.generations[:0]
		for _, generation := range entry.generations {
			if generation.WorkRevisionID != revocation.WorkRevisionID {
				kept = append(kept, generation)
			}
		}
		if len(kept) == 0 {
			delete(byKey, key)
			continue
		}
		entry.generations = append([]eligibilityGenerationV2(nil), kept...)
		byKey[key] = entry
	}
	for _, entry := range changed {
		key := eligibilityEntryKeyV2(entry)
		combined, err := overlayEligibilityEntryV2(byKey[key], entry)
		if err != nil {
			return nil, err
		}
		byKey[key] = combined
	}
	result := make([]EligibilityGuardEntryV2, 0, len(byKey))
	for _, entry := range byKey {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		return eligibilityEntryKeyV2(result[i]) < eligibilityEntryKeyV2(result[j])
	})
	return result, nil
}

func eligibilityTransitionsV2(shards []EligibilityGuardShardV2, observed map[journalv2.EligibilityShardID]ObservedEligibilityShardV2) ([]journalv2.EligibilityGuardTransition, error) {
	result := make([]journalv2.EligibilityGuardTransition, 0, len(shards))
	for _, shard := range shards {
		canonical, err := shard.CanonicalBytes()
		if err != nil {
			return nil, err
		}
		revision := uint64(0)
		if value, ok := observed[shard.ShardID()]; ok {
			revision = value.RecordRevision()
		}
		result = append(result, journalv2.EligibilityGuardTransition{MissionID: journalv2.MissionID(shard.MissionID()), ShardID: shard.ShardID(), Canonical: canonical, ExpectedRecordRevision: revision})
	}
	return result, nil
}

func canonicalWorkgraphEventV2(event workgraphEventV2) ([]byte, error) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func reduceDraftWorkgraphV2(prior []byte, input FinalizationPlanInputV2, payload []byte) ([]byte, error) {
	version := uint64(1)
	if len(prior) != 0 {
		state, err := DecodeWorkgraphStateV2(prior)
		if err != nil {
			return nil, err
		}
		version = state.LogicalVersion + 1
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("workgraph", journalv2.AggregateID(input.Revision.MissionID()), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(input.Revision.ProjectID()), MissionID: journalv2.MissionID(input.Revision.MissionID())})
	envelope, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: "event-workgraph-draft", EventType: "workgraph.draft", Family: "workgraph", EventSchemaVersion: WorkgraphEventSchemaV2, ReducerVersion: WorkgraphReducerV2, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(version), OperationID: "operation-workgraph-draft", OperationLogicalVersion: 1, PublicationID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CommittedRuntimeEpochID: "epoch-draft", EventIndex: 0, EventCount: 1, Payload: payload}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		return nil, err
	}
	return ReduceWorkgraphEventV2(prior, envelope)
}

func sealWorkgraphPlanV2(context AuthorityContextV2, projectID ids.ProjectID, missionID ids.MissionID, prior, next, payload []byte, eventType string, expectedHeadRevision uint64, guards []journalv2.EligibilityGuardTransition) (journalv2.SealedCommitPlanV2, error) {
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(projectID), MissionID: journalv2.MissionID(missionID)}
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: eventType, CommandSchemaVersion: 1, Scope: scope, Command: payload})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: context.OperationID, CommandKind: eventType, CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope,
		ActorID: context.ActorID, ProcessID: context.ProcessID, DoctrineRevision: context.DoctrineRevision, ProfileRevision: context.ProfileRevision, PolicyRevision: context.PolicyRevision,
		OriginatingRuntimeEpochID: context.RuntimeEpochID, ObservedCreatedAt: context.ObservedCreatedAt})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	eventIDs := []journalv2.EventID{context.OperationEventID, context.DomainEventID}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	nextState, err := DecodeWorkgraphStateV2(next)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	nextRevision, err := DecodeRevisionV2(nextState.CurrentRevision)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	result, _ := journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"work_revision_id":%q}`, nextRevision.ID())))
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: context.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "workgraph.result", ResultSchemaVersion: workgraphResultSchemaV2, CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	operationPayload, err := journalv2.EncodeOperationStateEventPayloadV2(operation)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	logicalVersion := journalv2.LogicalVersion(1)
	if len(prior) != 0 {
		state, _ := DecodeWorkgraphStateV2(prior)
		logicalVersion = journalv2.LogicalVersion(state.LogicalVersion + 1)
	}
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(context.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	graphCoordinate, _ := journalv2.NewAggregateCoordinate("workgraph", journalv2.AggregateID(missionID), scope)
	events := []journalv2.EventDraftV2{
		{EventID: context.OperationEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: context.RuntimeEpochID, ActorID: context.ActorID, ProcessID: context.ProcessID, PolicyRevision: context.PolicyRevision, GovernanceRevisions: append([]string(nil), context.Governance...), Payload: operationPayload},
		{EventID: context.DomainEventID, EventType: eventType, Family: "workgraph", EventSchemaVersion: WorkgraphEventSchemaV2, ReducerVersion: WorkgraphReducerV2, Coordinate: graphCoordinate, AggregateLogicalVersion: logicalVersion, CommittedRuntimeEpochID: context.RuntimeEpochID, ActorID: context.ActorID, ProcessID: context.ProcessID, PolicyRevision: context.PolicyRevision, GovernanceRevisions: append([]string(nil), context.Governance...), Payload: payload},
	}
	opDigest, _ := journalv2.ReducerStateDigestV2(operationPayload)
	graphDigest, _ := journalv2.ReducerStateDigestV2(next)
	opSummary, _ := journalv2.CanonicalObject([]byte(`{"status":"resolved"}`))
	graphSummary := next
	expectedLogical := journalv2.LogicalVersion(0)
	if logicalVersion > 1 {
		expectedLogical = logicalVersion - 1
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: context.OperationEventID, CommittedRuntimeEpochID: context.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opSummary},
		{Coordinate: graphCoordinate, ExpectedLogicalVersion: expectedLogical, NextLogicalVersion: logicalVersion, EventID: context.DomainEventID, ExpectedRecordRevision: expectedHeadRevision, CommittedRuntimeEpochID: context.RuntimeEpochID, CanonicalStateDigest: graphDigest, AuthoritySummary: graphSummary},
	}
	conditions := make([]journalv2.ProviderCondition, 0, len(heads)+len(guards))
	for _, head := range heads {
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		conditions = append(conditions, condition)
	}
	for _, guard := range guards {
		condition, err := journalv2.ConditionForEligibilityGuardTransitionV2(guard)
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
		conditions = append(conditions, condition)
	}
	return journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: events, Heads: heads, EligibilityGuards: guards, Conditions: conditions}, journalv2.DefaultEventRegistryV2())
}

func ResolveWorkgraphRetryV2(candidate journalv2.OperationBindingV2, existingBinding []byte, bindingPresent bool, existingState *journalv2.OperationStateV2, currentEpoch journalv2.RuntimeEpochID) (journalv2.RetryResolution, error) {
	return journalv2.ResolveRetryV2(candidate, existingBinding, bindingPresent, existingState, currentEpoch)
}
