package v2

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
)

type EventDraftV2 struct {
	EventID                 EventID
	EventType               string
	Family                  string
	EventSchemaVersion      uint32
	ReducerVersion          uint32
	Coordinate              AggregateCoordinate
	AggregateLogicalVersion LogicalVersion
	CommittedRuntimeEpochID RuntimeEpochID
	ActorID                 ActorID
	ProcessID               ProcessID
	WorkRevisionID          WorkRevisionID
	PolicyRevision          string
	GovernanceRevisions     []string
	Observations            []byte
	Payload                 []byte
}

type HeadTransitionV2 struct {
	Coordinate              AggregateCoordinate `json:"coordinate"`
	ExpectedLogicalVersion  LogicalVersion      `json:"expected_logical_version"`
	NextLogicalVersion      LogicalVersion      `json:"next_logical_version"`
	EventID                 EventID             `json:"event_id"`
	ExpectedRecordRevision  uint64              `json:"expected_record_revision,omitempty"`
	CommittedRuntimeEpochID RuntimeEpochID      `json:"committed_runtime_epoch_id"`
	CanonicalStateDigest    Digest              `json:"canonical_state_digest"`
	AuthoritySummary        []byte              `json:"authority_summary"`
	Tombstone               bool                `json:"tombstone"`
}

type EffectIntentReference struct {
	EffectID  EffectID `json:"effect_id"`
	Digest    Digest   `json:"digest"`
	Canonical []byte   `json:"canonical"`
}

type EligibilityGuardTransition struct {
	MissionID              MissionID          `json:"mission_id"`
	ShardID                EligibilityShardID `json:"shard_id"`
	Canonical              []byte             `json:"canonical"`
	ExpectedRecordRevision uint64             `json:"expected_record_revision,omitempty"`
}

type CheckpointRecord struct {
	CheckpointID               CheckpointID     `json:"checkpoint_id"`
	Canonical                  []byte           `json:"canonical"`
	SlotID                     CheckpointSlotID `json:"slot_id"`
	SlotCanonical              []byte           `json:"slot_canonical"`
	ExpectedSlotRecordRevision uint64           `json:"expected_slot_record_revision,omitempty"`
}

type ConditionKind string

const (
	ConditionAbsent         ConditionKind = "absent"
	ConditionRecordRevision ConditionKind = "record_revision"
)

type ConditionTargetKind string

const (
	ConditionTargetAggregateHead    ConditionTargetKind = "aggregate_head"
	ConditionTargetEligibilityGuard ConditionTargetKind = "eligibility_guard"
	ConditionTargetCheckpointSlot   ConditionTargetKind = "checkpoint_slot"
)

type ConditionTarget struct {
	Kind             ConditionTargetKind `json:"kind"`
	Coordinate       AggregateCoordinate `json:"coordinate,omitempty"`
	MissionID        MissionID           `json:"mission_id,omitempty"`
	ShardID          EligibilityShardID  `json:"shard_id,omitempty"`
	CheckpointSlotID CheckpointSlotID    `json:"checkpoint_slot_id,omitempty"`
}

type ProviderCondition struct {
	Target           ConditionTarget `json:"target"`
	Kind             ConditionKind   `json:"kind"`
	ObservedRevision uint64          `json:"observed_revision,omitempty"`
}

func ConditionForHeadTransitionV2(head HeadTransitionV2) (ProviderCondition, error) {
	if _, err := NewAggregateCoordinate(head.Coordinate.Kind, head.Coordinate.ID, head.Coordinate.Scope); err != nil {
		return ProviderCondition{}, invalid(ErrorInvalidTransition, "condition_target")
	}
	condition := ProviderCondition{Target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: head.Coordinate}}
	switch {
	case head.ExpectedLogicalVersion == 0 && head.ExpectedRecordRevision == 0:
		condition.Kind = ConditionAbsent
	case head.ExpectedLogicalVersion > 0 && head.ExpectedRecordRevision > 0:
		condition.Kind = ConditionRecordRevision
		condition.ObservedRevision = head.ExpectedRecordRevision
	default:
		return ProviderCondition{}, invalid(ErrorInvalidTransition, "condition_revision")
	}
	return condition, nil
}

func ConditionForEligibilityGuardTransitionV2(guard EligibilityGuardTransition) (ProviderCondition, error) {
	if validateIdentity("mission_id", string(guard.MissionID)) != nil || validateIdentity("eligibility_shard_id", string(guard.ShardID)) != nil {
		return ProviderCondition{}, invalid(ErrorInvalidTransition, "condition_target")
	}
	return conditionFromObservedRevision(ConditionTarget{Kind: ConditionTargetEligibilityGuard, MissionID: guard.MissionID, ShardID: guard.ShardID}, guard.ExpectedRecordRevision), nil
}

func ConditionForCheckpointRecordV2(checkpoint CheckpointRecord) (ProviderCondition, error) {
	if validateIdentity("checkpoint_slot_id", string(checkpoint.SlotID)) != nil {
		return ProviderCondition{}, invalid(ErrorInvalidTransition, "condition_target")
	}
	return conditionFromObservedRevision(ConditionTarget{Kind: ConditionTargetCheckpointSlot, CheckpointSlotID: checkpoint.SlotID}, checkpoint.ExpectedSlotRecordRevision), nil
}

func conditionFromObservedRevision(target ConditionTarget, revision uint64) ProviderCondition {
	if revision == 0 {
		return ProviderCondition{Target: target, Kind: ConditionAbsent}
	}
	return ProviderCondition{Target: target, Kind: ConditionRecordRevision, ObservedRevision: revision}
}

type SealedRecordRole string

const (
	RecordOperationBinding SealedRecordRole = "operation_binding"
	RecordJournalEvent     SealedRecordRole = "journal_event"
	RecordOperationHead    SealedRecordRole = "operation_head"
	RecordAggregateHead    SealedRecordRole = "aggregate_head"
	RecordEffectIntent     SealedRecordRole = "effect_intent"
	RecordEligibilityGuard SealedRecordRole = "eligibility_guard"
	RecordCheckpointRecord SealedRecordRole = "checkpoint_record"
	RecordCheckpointSlot   SealedRecordRole = "checkpoint_slot"
)

type SealedRecordValueClass string

const (
	RecordValueOrdinary   SealedRecordValueClass = "ordinary"
	RecordValueCheckpoint SealedRecordValueClass = "checkpoint"
)

// SealedRecordTargetV2 is a closed semantic address. It deliberately exposes
// no raw persistence key; adapters must map the role-specific identity through
// their own admitted key codec.
type SealedRecordTargetV2 struct {
	operationID      OperationID
	effectID         EffectID
	coordinate       AggregateCoordinate
	logicalVersion   LogicalVersion
	missionID        MissionID
	shardID          EligibilityShardID
	checkpointID     CheckpointID
	checkpointSlotID CheckpointSlotID
}

func (t SealedRecordTargetV2) OperationID() OperationID           { return t.operationID }
func (t SealedRecordTargetV2) EffectID() EffectID                 { return t.effectID }
func (t SealedRecordTargetV2) Coordinate() AggregateCoordinate    { return t.coordinate }
func (t SealedRecordTargetV2) LogicalVersion() LogicalVersion     { return t.logicalVersion }
func (t SealedRecordTargetV2) MissionID() MissionID               { return t.missionID }
func (t SealedRecordTargetV2) ShardID() EligibilityShardID        { return t.shardID }
func (t SealedRecordTargetV2) CheckpointID() CheckpointID         { return t.checkpointID }
func (t SealedRecordTargetV2) CheckpointSlotID() CheckpointSlotID { return t.checkpointSlotID }

type SealedRecordConditionV2 struct {
	kind             ConditionKind
	observedRevision uint64
}

func (c SealedRecordConditionV2) Kind() ConditionKind      { return c.kind }
func (c SealedRecordConditionV2) ObservedRevision() uint64 { return c.observedRevision }

// SealedRecordV2 can only be produced by SealCommitPlanV2. Its fields are
// immutable by construction and form the complete membership consumed by a
// persistence adapter and response-ambiguity reconciliation.
type SealedRecordV2 struct {
	role       SealedRecordRole
	target     SealedRecordTargetV2
	valueClass SealedRecordValueClass
	condition  SealedRecordConditionV2
	canonical  []byte
}

func (r SealedRecordV2) Role() SealedRecordRole             { return r.role }
func (r SealedRecordV2) Target() SealedRecordTargetV2       { return r.target }
func (r SealedRecordV2) ValueClass() SealedRecordValueClass { return r.valueClass }
func (r SealedRecordV2) Condition() SealedRecordConditionV2 { return r.condition }
func (r SealedRecordV2) CanonicalValue() []byte             { return append([]byte(nil), r.canonical...) }

type CommitPlanInput struct {
	OperationBinding    *OperationBindingV2
	PriorOperationState *OperationStateV2
	OperationState      OperationStateV2
	Events              []EventDraftV2
	Heads               []HeadTransitionV2
	EffectIntents       []EffectIntentReference
	EligibilityGuards   []EligibilityGuardTransition
	Checkpoints         []CheckpointRecord
	Conditions          []ProviderCondition
}

type SealedCommitPlanV2 struct {
	publicationID   PublicationID
	digest          Digest
	recordSetDigest Digest
	binding         *OperationBindingV2
	operationState  OperationStateV2
	events          []EventEnvelopeV2
	heads           []HeadTransitionV2
	effects         []EffectIntentReference
	eligibility     []EligibilityGuardTransition
	checkpoints     []CheckpointRecord
	conditions      []ProviderCondition
	records         []SealedRecordV2
	canonical       []byte
}

func SealCommitPlanV2(input CommitPlanInput, registry EventRegistryV2) (SealedCommitPlanV2, error) {
	if err := preflightCommitPlan(input); err != nil {
		return SealedCommitPlanV2{}, err
	}
	if input.OperationState.logicalVersion == 0 || input.OperationState.lastPublicationID != "" {
		return SealedCommitPlanV2{}, invalid(ErrorInvalidTransition, "operation_transition")
	}
	if input.OperationState.logicalVersion == 1 && (input.OperationBinding == nil || input.PriorOperationState != nil) ||
		input.OperationState.logicalVersion > 1 && (input.OperationBinding != nil || input.PriorOperationState == nil) {
		return SealedCommitPlanV2{}, invalid(ErrorInvalidTransition, "operation_binding_create")
	}
	if input.OperationBinding != nil && input.OperationBinding.operationID != input.OperationState.operationID {
		return SealedCommitPlanV2{}, invalid(ErrorInvalidTransition, "operation_binding_membership")
	}
	for _, event := range input.Events {
		if _, err := registry.Resolve(event.Family, event.EventSchemaVersion, event.ReducerVersion); err != nil {
			return SealedCommitPlanV2{}, err
		}
		if err := validateEventAggregateAuthority(event.Family, event.Coordinate, input.OperationState.operationID, input.OperationState.logicalVersion, event.AggregateLogicalVersion); err != nil {
			return SealedCommitPlanV2{}, err
		}
	}
	drafts := cloneEventDrafts(input.Events)
	heads := cloneHeadTransitions(input.Heads)
	effects := cloneEffectIntents(input.EffectIntents)
	eligibility := cloneEligibilityGuards(input.EligibilityGuards)
	checkpoints := cloneCheckpoints(input.Checkpoints)
	conditions := append([]ProviderCondition(nil), input.Conditions...)
	for index := range drafts {
		payload, err := CanonicalObject(drafts[index].Payload)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
		drafts[index].Payload = payload
		if len(drafts[index].Observations) == 0 {
			drafts[index].Observations = []byte("{}")
		} else {
			drafts[index].Observations, err = CanonicalObject(drafts[index].Observations)
			if err != nil {
				return SealedCommitPlanV2{}, err
			}
		}
		sort.Strings(drafts[index].GovernanceRevisions)
	}
	operationPayload, err := EncodeOperationStateEventPayloadV2(input.OperationState)
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	for _, event := range drafts {
		if event.Family != "operation" {
			continue
		}
		if event.EventSchemaVersion != 2 || event.ReducerVersion != 2 || !bytes.Equal(event.Payload, operationPayload) {
			return SealedCommitPlanV2{}, invalid(ErrorInvalidTransition, "operation_event_payload")
		}
	}
	for index := range heads {
		canonical, err := requireCanonicalValue("authority_summary", heads[index].AuthoritySummary, MaxCanonicalPayloadBytes)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
		heads[index].AuthoritySummary = canonical
	}
	for index := range effects {
		canonical, err := requireCanonicalValue("effect_intent", effects[index].Canonical, MaxCanonicalPayloadBytes)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
		effects[index].Canonical = canonical
	}
	for index := range eligibility {
		canonical, err := requireCanonicalValue("eligibility_guard", eligibility[index].Canonical, MaxCanonicalPayloadBytes)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
		eligibility[index].Canonical = canonical
	}
	for index := range checkpoints {
		record, err := requireCanonicalValue("checkpoint_record", checkpoints[index].Canonical, MaxCheckpointValueBytes)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
		slot, err := requireCanonicalValue("checkpoint_slot", checkpoints[index].SlotCanonical, MaxCanonicalPayloadBytes)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
		checkpoints[index].Canonical, checkpoints[index].SlotCanonical = record, slot
	}
	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].Coordinate.canonical()+"/"+string(drafts[i].EventID) < drafts[j].Coordinate.canonical()+"/"+string(drafts[j].EventID)
	})
	sort.Slice(heads, func(i, j int) bool { return heads[i].Coordinate.canonical() < heads[j].Coordinate.canonical() })
	sort.Slice(effects, func(i, j int) bool { return effects[i].EffectID < effects[j].EffectID })
	sort.Slice(eligibility, func(i, j int) bool {
		return string(eligibility[i].MissionID)+"/"+string(eligibility[i].ShardID) < string(eligibility[j].MissionID)+"/"+string(eligibility[j].ShardID)
	})
	sort.Slice(checkpoints, func(i, j int) bool { return checkpoints[i].CheckpointID < checkpoints[j].CheckpointID })
	sort.Slice(conditions, func(i, j int) bool {
		return conditionTargetKey(conditions[i].Target) < conditionTargetKey(conditions[j].Target)
	})
	if err := validatePlanMembers(input.PriorOperationState, input.OperationState, drafts, heads, effects, eligibility, checkpoints, conditions); err != nil {
		return SealedCommitPlanV2{}, err
	}
	preimage, err := planPreimage(input.OperationBinding, input.PriorOperationState, input.OperationState, drafts, heads, effects, eligibility, checkpoints, conditions)
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	planDigest := digest("spec-kitty.commit-plan.v2", preimage)
	version, _ := input.OperationState.logicalVersion.Hex16()
	publicationDigest := digest(publicationDomain, []byte(string(input.OperationState.operationID)+"\x00"+version+"\x00"+planDigest.String()))
	publicationID := PublicationID(publicationDigest.String())

	state, err := NewOperationStateV2(OperationStateInput{input.OperationState.operationID, input.OperationState.logicalVersion, input.OperationState.status,
		input.OperationState.resultKind, input.OperationState.resultSchemaVersion, input.OperationState.canonicalResult,
		input.OperationState.eventIDs, input.OperationState.effectIDs, publicationID})
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	events := make([]EventEnvelopeV2, len(drafts))
	for index, draft := range drafts {
		events[index], err = NewEventEnvelopeV2(EventEnvelopeInput{
			EventID: draft.EventID, EventType: draft.EventType, Family: draft.Family, EventSchemaVersion: draft.EventSchemaVersion,
			ReducerVersion: draft.ReducerVersion, Coordinate: draft.Coordinate, AggregateLogicalVersion: draft.AggregateLogicalVersion,
			OperationID: state.operationID, OperationLogicalVersion: state.logicalVersion, PublicationID: publicationID,
			CommittedRuntimeEpochID: draft.CommittedRuntimeEpochID, EventIndex: uint16(index), EventCount: uint16(len(drafts)),
			ActorID: draft.ActorID, ProcessID: draft.ProcessID, WorkRevisionID: draft.WorkRevisionID, PolicyRevision: draft.PolicyRevision,
			GovernanceRevisions: draft.GovernanceRevisions, Observations: draft.Observations, Payload: draft.Payload,
		}, registry)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
	}
	if err := ValidatePublicationEventsV2(events); err != nil {
		return SealedCommitPlanV2{}, err
	}
	records, err := deriveSealedRecords(input.OperationBinding, state, events, heads, effects, eligibility, checkpoints, conditions)
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	if _, err := validateSealedRecordBudget(records); err != nil {
		return SealedCommitPlanV2{}, err
	}
	recordSetDigest, err := sealedRecordSetDigest(records)
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	plan := SealedCommitPlanV2{publicationID: publicationID, digest: planDigest, recordSetDigest: recordSetDigest,
		binding: cloneBinding(input.OperationBinding), operationState: state, events: events, heads: heads, effects: effects,
		eligibility: eligibility, checkpoints: checkpoints, conditions: conditions, records: records}
	plan.canonical, err = plan.finalBytes()
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	return plan, nil
}

func sealedRecordSetDigest(records []SealedRecordV2) (Digest, error) {
	type recordDigestWire struct {
		Role       SealedRecordRole       `json:"role"`
		Target     string                 `json:"target"`
		ValueClass SealedRecordValueClass `json:"value_class"`
		Condition  ConditionKind          `json:"condition"`
		Revision   uint64                 `json:"revision,omitempty"`
		Value      []byte                 `json:"value"`
	}
	values := make([]recordDigestWire, len(records))
	for index, record := range records {
		values[index] = recordDigestWire{record.role, sealedTargetIdentity(record.role, record.target), record.valueClass,
			record.condition.kind, record.condition.observedRevision, append([]byte(nil), record.canonical...)}
	}
	sort.Slice(values, func(i, j int) bool {
		return string(values[i].Role)+"\x00"+values[i].Target < string(values[j].Role)+"\x00"+values[j].Target
	})
	canonical, err := marshalCanonical(values, MaxCommitPlanBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest("spec-kitty.sealed-record-set.v2", canonical), nil
}

func sealedTargetIdentity(role SealedRecordRole, target SealedRecordTargetV2) string {
	switch role {
	case RecordOperationBinding, RecordOperationHead:
		return string(target.operationID)
	case RecordJournalEvent:
		version, _ := target.logicalVersion.Hex16()
		return target.coordinate.canonical() + "/" + version
	case RecordAggregateHead:
		return target.coordinate.canonical()
	case RecordEffectIntent:
		return string(target.effectID)
	case RecordEligibilityGuard:
		return string(target.missionID) + "/" + string(target.shardID)
	case RecordCheckpointRecord:
		return string(target.checkpointID)
	case RecordCheckpointSlot:
		return string(target.checkpointSlotID)
	default:
		return ""
	}
}

func validatePlanMembers(prior *OperationStateV2, state OperationStateV2, events []EventDraftV2, heads []HeadTransitionV2, effects []EffectIntentReference, eligibility []EligibilityGuardTransition, checkpoints []CheckpointRecord, conditions []ProviderCondition) error {
	eventCoordinates := make(map[string]EventDraftV2, len(events))
	eventIDs := make(map[EventID]bool, len(events))
	operationEvents := 0
	for _, event := range events {
		if _, err := NewAggregateCoordinate(event.Coordinate.Kind, event.Coordinate.ID, event.Coordinate.Scope); err != nil || event.AggregateLogicalVersion == 0 || validateIdentity("event_id", string(event.EventID)) != nil {
			return invalid(ErrorInvalidTransition, "event")
		}
		coordinate := event.Coordinate.canonical()
		if eventCoordinates[coordinate].EventID != "" || eventIDs[event.EventID] {
			return invalid(ErrorInvalidTransition, "duplicate_event")
		}
		eventCoordinates[coordinate], eventIDs[event.EventID] = event, true
		if event.Coordinate.Kind == AggregateKind("operation") {
			operationEvents++
		}
		if err := validateEventAggregateAuthority(event.Family, event.Coordinate, state.operationID, state.logicalVersion, event.AggregateLogicalVersion); err != nil {
			return err
		}
	}
	if operationEvents != 1 {
		return invalid(ErrorInvalidTransition, "operation_event")
	}
	headCoordinates := make(map[string]bool, len(heads))
	for _, head := range heads {
		coordinate := head.Coordinate.canonical()
		event, ok := eventCoordinates[coordinate]
		if !ok || headCoordinates[coordinate] || head.EventID != event.EventID || head.NextLogicalVersion != event.AggregateLogicalVersion ||
			head.NextLogicalVersion == 0 || head.ExpectedLogicalVersion+1 != head.NextLogicalVersion || head.CanonicalStateDigest.IsZero() ||
			validateIdentity("committed_runtime_epoch_id", string(head.CommittedRuntimeEpochID)) != nil ||
			head.CommittedRuntimeEpochID != event.CommittedRuntimeEpochID || len(head.AuthoritySummary) == 0 {
			return invalid(ErrorInvalidTransition, "event_head_membership")
		}
		headCoordinates[coordinate] = true
	}
	currentEventIDs := make([]EventID, 0, len(eventIDs))
	for eventID := range eventIDs {
		currentEventIDs = append(currentEventIDs, eventID)
	}
	sort.Slice(currentEventIDs, func(i, j int) bool { return currentEventIDs[i] < currentEventIDs[j] })
	currentEffectIDs := make([]EffectID, len(effects))
	for index, effect := range effects {
		currentEffectIDs[index] = effect.EffectID
	}
	if err := exactPublicationMembership(prior, state, currentEventIDs, currentEffectIDs); err != nil {
		return err
	}
	if err := uniqueEffects(effects); err != nil {
		return err
	}
	if err := validatePersistenceMembers(eligibility, checkpoints); err != nil {
		return err
	}
	if err := validateExactConditions(heads, eligibility, checkpoints, conditions); err != nil {
		return err
	}
	return nil
}

func preflightCommitPlan(input CommitPlanInput) error {
	if len(input.Events) == 0 || len(input.Events) > MaxEventsPerPublication || len(input.Heads) != len(input.Events) ||
		len(input.Heads) > MaxHeadsPerPublication || len(input.EffectIntents) > MaxEffectsPerPublication || len(input.EligibilityGuards) > MaxMembers ||
		len(input.Checkpoints) > MaxCheckpointsPerPlan || len(input.Conditions) > MaxConditionsPerPlan {
		return invalid(ErrorBudgetExceeded, "commit_plan_members")
	}
	projectedRecords := len(input.Events) + len(input.Heads) + len(input.EffectIntents) + len(input.EligibilityGuards) + 2*len(input.Checkpoints)
	if input.OperationBinding != nil {
		projectedRecords++
	}
	if projectedRecords > MaxMembers {
		return invalid(ErrorBudgetExceeded, "sealed_record_projection")
	}
	add := func(field string, size, maximum int) error {
		if size > maximum {
			return invalid(ErrorBudgetExceeded, field)
		}
		return nil
	}
	for _, event := range input.Events {
		if err := preflightGovernanceRevisions(event.GovernanceRevisions); err != nil {
			return err
		}
		for _, revision := range event.GovernanceRevisions {
			if err := add("governance_revisions", len(revision), MaxIdentityBytes); err != nil {
				return err
			}
		}
		if err := add("event_payload", len(event.Payload), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
		if err := add("event_observations", len(event.Observations), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
	}
	for _, head := range input.Heads {
		if err := add("authority_summary", len(head.AuthoritySummary), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
	}
	for _, effect := range input.EffectIntents {
		if err := add("effect_intent", len(effect.Canonical), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
	}
	for _, guard := range input.EligibilityGuards {
		if err := add("eligibility_guard", len(guard.Canonical), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
	}
	for _, checkpoint := range input.Checkpoints {
		if err := add("checkpoint_payload", len(checkpoint.Canonical), MaxCheckpointValueBytes); err != nil {
			return err
		}
		if err := add("checkpoint_slot", len(checkpoint.SlotCanonical), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
	}
	if _, err := conservativeProductBatchSize(input); err != nil {
		return err
	}
	_, err := prospectiveEncodedPlanSize(input)
	return err
}

// WP02 deliberately does not own the spk/v2 key codec. Before the adapter is
// available, each projected target is therefore charged the full frozen 1 KiB
// key ceiling. Value sizes are conservative bounds for the final canonical
// records, so an admitted plan is guaranteed to fit the 6 MiB raw batch law.
// WP03 repeats the check with actual encoded keys and provider wire framing.
func conservativeProductBatchSize(input CommitPlanInput) (uint64, error) {
	budget := productRawBudget{}
	if input.OperationBinding != nil {
		if err := budget.addRecord(conservativeBindingSize(*input.OperationBinding), RecordValueOrdinary); err != nil {
			return 0, err
		}
	}
	stateSize := conservativeOperationStateSize(input.OperationState)
	for _, event := range input.Events {
		if err := budget.addRecord(conservativeEventEnvelopeSize(event, input.OperationState, len(input.Events)), RecordValueOrdinary); err != nil {
			return 0, err
		}
	}
	for _, head := range input.Heads {
		valueSize := conservativeAggregateHeadSize(head)
		if head.Coordinate.Kind == AggregateKind("operation") {
			valueSize = stateSize
		}
		if err := budget.addRecord(valueSize, RecordValueOrdinary); err != nil {
			return 0, err
		}
	}
	for _, effect := range input.EffectIntents {
		if err := budget.addRecord(canonicalJSONSizeBound(effect.Canonical), RecordValueOrdinary); err != nil {
			return 0, err
		}
	}
	for _, guard := range input.EligibilityGuards {
		if err := budget.addRecord(canonicalJSONSizeBound(guard.Canonical), RecordValueOrdinary); err != nil {
			return 0, err
		}
	}
	for _, checkpoint := range input.Checkpoints {
		if err := budget.addRecord(canonicalJSONSizeBound(checkpoint.Canonical), RecordValueCheckpoint); err != nil {
			return 0, err
		}
		if err := budget.addRecord(canonicalJSONSizeBound(checkpoint.SlotCanonical), RecordValueOrdinary); err != nil {
			return 0, err
		}
	}
	return budget.value, nil
}

type productRawBudget struct{ value uint64 }

func (b *productRawBudget) addRecord(value uint64, class SealedRecordValueClass) error {
	maximum, field := uint64(MaxCanonicalPayloadBytes), "ordinary_record_value"
	if class == RecordValueCheckpoint {
		maximum, field = uint64(MaxCheckpointValueBytes), "checkpoint_record_value"
	}
	if value > maximum {
		return invalid(ErrorBudgetExceeded, field)
	}
	recordBytes := uint64(MaxProductKeyBytes) + value
	if recordBytes > uint64(MaxProductRawBatchBytes)-b.value {
		return invalid(ErrorBudgetExceeded, "product_raw_batch_bytes")
	}
	b.value += recordBytes
	return nil
}

func conservativeBindingSize(binding OperationBindingV2) uint64 {
	fields := []uint64{
		jsonFieldSize("schema_version", uintJSONSize(SchemaVersion)),
		jsonFieldSize("operation_id", jsonStringSizeBound(string(binding.operationID))),
		jsonFieldSize("command_kind", jsonStringSizeBound(binding.commandKind)),
		jsonFieldSize("command_schema_version", uintJSONSize(binding.commandSchemaVersion)),
		jsonFieldSize("canonical_intent_sha256", jsonStringSizeBound(strings.Repeat("0", 64))),
		jsonFieldSize("scope", scopeJSONSizeBound(binding.scope)),
	}
	fields = appendOptionalStringField(fields, "actor_id", string(binding.actorID))
	fields = appendOptionalStringField(fields, "process_id", string(binding.processID))
	fields = appendOptionalStringField(fields, "doctrine_revision", binding.doctrineRevision)
	fields = appendOptionalStringField(fields, "profile_revision", binding.profileRevision)
	fields = appendOptionalStringField(fields, "policy_revision", binding.policyRevision)
	fields = append(fields,
		jsonFieldSize("originating_runtime_epoch_id", jsonStringSizeBound(string(binding.originatingRuntimeEpochID))),
		jsonFieldSize("observed_created_at", jsonStringSizeBound(binding.observedCreatedAt)),
	)
	return jsonObjectSize(fields)
}

func conservativeOperationStateSize(state OperationStateV2) uint64 {
	fields := []uint64{
		jsonFieldSize("schema_version", uintJSONSize(SchemaVersion)),
		jsonFieldSize("operation_id", jsonStringSizeBound(string(state.operationID))),
		jsonFieldSize("logical_version", uintJSONSize(uint64(state.logicalVersion))),
		jsonFieldSize("status", jsonStringSizeBound(string(state.status))),
	}
	fields = appendOptionalStringField(fields, "result_kind", state.resultKind)
	if state.resultSchemaVersion != 0 {
		fields = append(fields, jsonFieldSize("result_schema_version", uintJSONSize(state.resultSchemaVersion)))
	}
	if len(state.canonicalResult) != 0 {
		fields = append(fields, jsonFieldSize("canonical_result", base64JSONSizeBound(uint64(len(state.canonicalResult)))))
	}
	if !state.resultDigest.IsZero() {
		fields = append(fields, jsonFieldSize("result_sha256", jsonStringSizeBound(strings.Repeat("0", 64))))
	}
	fields = append(fields,
		jsonFieldSize("event_ids", stringArrayJSONSize(state.eventIDs)),
		jsonFieldSize("effect_ids", stringArrayJSONSize(state.effectIDs)),
		jsonFieldSize("last_publication_id", jsonStringSizeBound(strings.Repeat("0", 64))),
	)
	return jsonObjectSize(fields)
}

func conservativeEventEnvelopeSize(event EventDraftV2, state OperationStateV2, eventCount int) uint64 {
	observationSize := canonicalJSONSizeBound(event.Observations)
	if len(event.Observations) == 0 {
		observationSize = 2
	}
	fields := []uint64{
		jsonFieldSize("envelope_version", uintJSONSize(EnvelopeVersion)),
		jsonFieldSize("event_id", jsonStringSizeBound(string(event.EventID))),
		jsonFieldSize("event_type", jsonStringSizeBound(event.EventType)),
		jsonFieldSize("family", jsonStringSizeBound(event.Family)),
		jsonFieldSize("event_schema_version", uintJSONSize(event.EventSchemaVersion)),
		jsonFieldSize("reducer_version", uintJSONSize(event.ReducerVersion)),
		jsonFieldSize("coordinate", coordinateJSONSizeBound(event.Coordinate)),
		jsonFieldSize("aggregate_logical_version", uintJSONSize(uint64(event.AggregateLogicalVersion))),
		jsonFieldSize("operation_id", jsonStringSizeBound(string(state.operationID))),
		jsonFieldSize("operation_logical_version", uintJSONSize(uint64(state.logicalVersion))),
		jsonFieldSize("publication_id", jsonStringSizeBound(strings.Repeat("0", 64))),
		jsonFieldSize("committed_runtime_epoch_id", jsonStringSizeBound(string(event.CommittedRuntimeEpochID))),
		jsonFieldSize("event_index", uintJSONSize(uint64(eventCount-1))),
		jsonFieldSize("event_count", uintJSONSize(uint64(eventCount))),
	}
	fields = appendOptionalStringField(fields, "actor_id", string(event.ActorID))
	fields = appendOptionalStringField(fields, "process_id", string(event.ProcessID))
	fields = appendOptionalStringField(fields, "work_revision_id", string(event.WorkRevisionID))
	fields = appendOptionalStringField(fields, "policy_revision", event.PolicyRevision)
	if len(event.GovernanceRevisions) != 0 {
		fields = append(fields, jsonFieldSize("governance_revisions", stringArrayJSONSize(event.GovernanceRevisions)))
	}
	fields = append(fields,
		jsonFieldSize("observations", base64JSONSizeBound(observationSize)),
		jsonFieldSize("payload", base64JSONSizeBound(canonicalJSONSizeBound(event.Payload))),
		jsonFieldSize("canonical_event_sha256", jsonStringSizeBound(strings.Repeat("0", 64))),
	)
	return jsonObjectSize(fields)
}

func conservativeAggregateHeadSize(head HeadTransitionV2) uint64 {
	return jsonObjectSize([]uint64{
		jsonFieldSize("schema_version", uintJSONSize(SchemaVersion)),
		jsonFieldSize("coordinate", coordinateJSONSizeBound(head.Coordinate)),
		jsonFieldSize("logical_version", uintJSONSize(uint64(head.NextLogicalVersion))),
		jsonFieldSize("last_event_id", jsonStringSizeBound(string(head.EventID))),
		jsonFieldSize("last_publication_id", jsonStringSizeBound(strings.Repeat("0", 64))),
		jsonFieldSize("committed_runtime_epoch_id", jsonStringSizeBound(string(head.CommittedRuntimeEpochID))),
		jsonFieldSize("canonical_state_sha256", jsonStringSizeBound(strings.Repeat("0", 64))),
		jsonFieldSize("authority_summary", base64JSONSizeBound(canonicalJSONSizeBound(head.AuthoritySummary))),
		jsonFieldSize("tombstone", 5),
	})
}

func jsonFieldSize(name string, value uint64) uint64 {
	return jsonStringSizeBound(name) + 1 + value
}

func jsonObjectSize(fields []uint64) uint64 {
	size := uint64(2)
	for index, field := range fields {
		size += field
		if index > 0 {
			size++
		}
	}
	return size
}

func uintJSONSize[T ~uint16 | ~uint32 | ~uint64](value T) uint64 {
	return uint64(len(strconv.FormatUint(uint64(value), 10)))
}

func stringArrayJSONSize[T ~string](values []T) uint64 {
	size := uint64(2)
	for index, value := range values {
		size += jsonStringSizeBound(string(value))
		if index > 0 {
			size++
		}
	}
	return size
}

func appendOptionalStringField(fields []uint64, name, value string) []uint64 {
	if value == "" {
		return fields
	}
	return append(fields, jsonFieldSize(name, jsonStringSizeBound(value)))
}

func scopeJSONSizeBound(scope Scope) uint64 {
	fields := []uint64{jsonFieldSize("kind", jsonStringSizeBound(string(scope.Kind)))}
	fields = appendOptionalStringField(fields, "project_id", string(scope.ProjectID))
	fields = appendOptionalStringField(fields, "mission_id", string(scope.MissionID))
	fields = appendOptionalStringField(fields, "repository_id", string(scope.RepositoryID))
	fields = appendOptionalStringField(fields, "work_revision_id", string(scope.WorkRevisionID))
	fields = appendOptionalStringField(fields, "work_id", string(scope.WorkID))
	return jsonObjectSize(fields)
}

func coordinateJSONSizeBound(coordinate AggregateCoordinate) uint64 {
	return jsonObjectSize([]uint64{
		jsonFieldSize("kind", jsonStringSizeBound(string(coordinate.Kind))),
		jsonFieldSize("id", jsonStringSizeBound(string(coordinate.ID))),
		jsonFieldSize("scope", scopeJSONSizeBound(coordinate.Scope)),
	})
}

type checkedPlanSize struct{ value uint64 }

func (s *checkedPlanSize) add(value uint64) error {
	if value > uint64(MaxCommitPlanBytes)-s.value {
		return invalid(ErrorBudgetExceeded, "commit_plan_bytes")
	}
	s.value += value
	return nil
}

func (s *checkedPlanSize) addTimes(value uint64, multiplier uint64) error {
	if value > uint64(MaxCommitPlanBytes)/multiplier {
		return invalid(ErrorBudgetExceeded, "commit_plan_bytes")
	}
	return s.add(value * multiplier)
}

func (s *checkedPlanSize) addString(value string) error {
	if len(value) > MaxIdentityBytes {
		return invalid(ErrorBudgetExceeded, "commit_plan_identity")
	}
	return s.addTimes(jsonStringSizeBound(value), 2)
}

func (s *checkedPlanSize) addScope(scope Scope) error {
	for _, value := range []string{string(scope.Kind), string(scope.ProjectID), string(scope.MissionID), string(scope.RepositoryID), string(scope.WorkRevisionID), string(scope.WorkID)} {
		if err := s.addString(value); err != nil {
			return err
		}
	}
	return nil
}

// prospectiveEncodedPlanSize is a checked upper bound for both the digest
// preimage and final plan wire form. The factor of two covers base64 framing
// at the nested event/state layer and again at the final plan layer.
func prospectiveEncodedPlanSize(input CommitPlanInput) (uint64, error) {
	size := checkedPlanSize{}
	if err := size.add(16 * 1024); err != nil { // plan fields, punctuation, and fixed digests
		return 0, err
	}
	addState := func(state OperationStateV2) error {
		if err := size.add(4 * 1024); err != nil {
			return err
		}
		for _, value := range []string{string(state.operationID), string(state.status), state.resultKind, string(state.lastPublicationID)} {
			if err := size.addString(value); err != nil {
				return err
			}
		}
		for _, value := range state.eventIDs {
			if err := size.addString(string(value)); err != nil {
				return err
			}
		}
		for _, value := range state.effectIDs {
			if err := size.addString(string(value)); err != nil {
				return err
			}
		}
		return size.add(doubleBase64JSONSizeBound(uint64(len(state.canonicalResult))))
	}
	if input.OperationBinding != nil {
		if err := size.add(4 * 1024); err != nil {
			return 0, err
		}
		binding := input.OperationBinding
		for _, value := range []string{string(binding.operationID), binding.commandKind, string(binding.actorID), string(binding.processID), binding.doctrineRevision,
			binding.profileRevision, binding.policyRevision, string(binding.originatingRuntimeEpochID), binding.observedCreatedAt} {
			if err := size.addString(value); err != nil {
				return 0, err
			}
		}
		if err := size.addScope(binding.scope); err != nil {
			return 0, err
		}
	}
	if input.PriorOperationState != nil {
		if err := addState(*input.PriorOperationState); err != nil {
			return 0, err
		}
	}
	if err := addState(input.OperationState); err != nil {
		return 0, err
	}
	for _, event := range input.Events {
		if err := size.add(2 * 1024); err != nil {
			return 0, err
		}
		for _, value := range []string{string(event.EventID), event.EventType, event.Family, string(event.Coordinate.Kind), string(event.Coordinate.ID),
			string(event.CommittedRuntimeEpochID), string(event.ActorID), string(event.ProcessID), string(event.WorkRevisionID), event.PolicyRevision} {
			if err := size.addString(value); err != nil {
				return 0, err
			}
		}
		if err := size.addScope(event.Coordinate.Scope); err != nil {
			return 0, err
		}
		for _, revision := range event.GovernanceRevisions {
			if err := size.addString(revision); err != nil {
				return 0, err
			}
		}
		payloadSize := canonicalJSONSizeBound(event.Payload)
		observationSize := canonicalJSONSizeBound(event.Observations)
		if len(event.Observations) == 0 {
			observationSize = 2
		}
		if err := size.add(doubleBase64JSONSizeBound(payloadSize)); err != nil {
			return 0, err
		}
		if err := size.add(doubleBase64JSONSizeBound(observationSize)); err != nil {
			return 0, err
		}
	}
	for _, head := range input.Heads {
		if err := size.add(1024); err != nil {
			return 0, err
		}
		if err := size.addString(string(head.Coordinate.Kind)); err != nil {
			return 0, err
		}
		if err := size.addString(string(head.Coordinate.ID)); err != nil {
			return 0, err
		}
		if err := size.addScope(head.Coordinate.Scope); err != nil {
			return 0, err
		}
		if err := size.addString(string(head.EventID)); err != nil {
			return 0, err
		}
		if err := size.addString(string(head.CommittedRuntimeEpochID)); err != nil {
			return 0, err
		}
		if err := size.add(base64JSONSizeBound(canonicalJSONSizeBound(head.AuthoritySummary))); err != nil {
			return 0, err
		}
	}
	for _, effect := range input.EffectIntents {
		if err := size.add(512); err != nil {
			return 0, err
		}
		if err := size.addString(string(effect.EffectID)); err != nil {
			return 0, err
		}
		if err := size.add(base64JSONSizeBound(canonicalJSONSizeBound(effect.Canonical))); err != nil {
			return 0, err
		}
	}
	for _, guard := range input.EligibilityGuards {
		if err := size.add(512); err != nil {
			return 0, err
		}
		if err := size.addString(string(guard.MissionID)); err != nil {
			return 0, err
		}
		if err := size.addString(string(guard.ShardID)); err != nil {
			return 0, err
		}
		if err := size.add(base64JSONSizeBound(canonicalJSONSizeBound(guard.Canonical))); err != nil {
			return 0, err
		}
	}
	for _, checkpoint := range input.Checkpoints {
		if err := size.add(512); err != nil {
			return 0, err
		}
		if err := size.addString(string(checkpoint.CheckpointID)); err != nil {
			return 0, err
		}
		if err := size.addString(string(checkpoint.SlotID)); err != nil {
			return 0, err
		}
		if err := size.add(base64JSONSizeBound(canonicalJSONSizeBound(checkpoint.Canonical))); err != nil {
			return 0, err
		}
		if err := size.add(base64JSONSizeBound(canonicalJSONSizeBound(checkpoint.SlotCanonical))); err != nil {
			return 0, err
		}
	}
	for _, condition := range input.Conditions {
		if err := size.add(1024); err != nil {
			return 0, err
		}
		for _, value := range []string{string(condition.Target.Kind), string(condition.Target.Coordinate.Kind), string(condition.Target.Coordinate.ID),
			string(condition.Target.MissionID), string(condition.Target.ShardID), string(condition.Target.CheckpointSlotID), string(condition.Kind)} {
			if err := size.addString(value); err != nil {
				return 0, err
			}
		}
		if err := size.addScope(condition.Target.Coordinate.Scope); err != nil {
			return 0, err
		}
	}
	return size.value, nil
}

func base64JSONSizeBound(value uint64) uint64 {
	return 2 + 4*((value+2)/3)
}

func doubleBase64JSONSizeBound(value uint64) uint64 {
	return base64JSONSizeBound(base64JSONSizeBound(value))
}

func jsonStringSizeBound(value string) uint64 {
	size := uint64(2)
	for index := 0; index < len(value); index++ {
		char := value[index]
		switch {
		case char < 0x20 || char == '<' || char == '>' || char == '&':
			size += 6
		case char == '\\' || char == '"':
			size += 2
		case char < 0x80:
			size++
		default:
			// Invalid UTF-8 expands to a six-byte replacement escape; valid
			// UTF-8 cannot expand beyond six bytes per source byte.
			size += 6
		}
	}
	return size
}

func canonicalJSONSizeBound(value []byte) uint64 {
	size := uint64(0)
	inString := false
	for index := 0; index < len(value); index++ {
		char := value[index]
		if !inString {
			if char == ' ' || char == '\t' || char == '\r' || char == '\n' {
				continue
			}
			size++
			inString = char == '"'
			continue
		}
		switch {
		case char == '"':
			size++
			inString = false
		case char == '\\':
			size += 6
			if index+1 < len(value) {
				index++
				if value[index] == 'u' && index+4 < len(value) {
					index += 4
				}
			}
		case char < 0x20 || char == '<' || char == '>' || char == '&':
			size += 6
		case char < 0x80:
			size++
		default:
			size += 6
		}
	}
	return size
}

func uniqueEffects(effects []EffectIntentReference) error {
	seen := make(map[EffectID]bool, len(effects))
	for _, effect := range effects {
		if validateIdentity("effect_id", string(effect.EffectID)) != nil || effect.Digest.IsZero() || seen[effect.EffectID] {
			return invalid(ErrorInvalidTransition, "effect_membership")
		}
		seen[effect.EffectID] = true
	}
	return nil
}

func exactPublicationMembership(prior *OperationStateV2, state OperationStateV2, events []EventID, effects []EffectID) error {
	if prior == nil {
		if !equalIDs(state.eventIDs, events) || !equalIDs(state.effectIDs, effects) {
			return invalid(ErrorInvalidTransition, "first_publication_membership")
		}
		return nil
	}
	if prior.operationID != state.operationID || prior.logicalVersion+1 != state.logicalVersion || !containsAllIDs(prior.eventIDs, state.eventIDs) || !containsAllIDs(prior.effectIDs, state.effectIDs) {
		return invalid(ErrorInvalidTransition, "prior_operation_membership")
	}
	rebuilt, err := AdvanceOperationState(*prior, OperationTransition{prior.logicalVersion, state.logicalVersion, state.status, state.resultKind,
		state.resultSchemaVersion, state.canonicalResult, state.eventIDs, state.effectIDs, state.lastPublicationID})
	rebuiltBytes, _ := marshalCanonical(operationStatePreimage(rebuilt, false), MaxCanonicalPayloadBytes)
	stateBytes, _ := marshalCanonical(operationStatePreimage(state, false), MaxCanonicalPayloadBytes)
	if err != nil || !bytes.Equal(rebuiltBytes, stateBytes) {
		return invalid(ErrorInvalidTransition, "prior_operation_transition")
	}
	if !equalIDs(addedIDs(prior.eventIDs, state.eventIDs), events) || !equalIDs(addedIDs(prior.effectIDs, state.effectIDs), effects) {
		return invalid(ErrorInvalidTransition, "later_publication_membership")
	}
	return nil
}

func addedIDs[T ~string](prior, current []T) []T {
	result := make([]T, 0, len(current))
	priorIndex := 0
	for _, value := range current {
		for priorIndex < len(prior) && prior[priorIndex] < value {
			priorIndex++
		}
		if priorIndex == len(prior) || prior[priorIndex] != value {
			result = append(result, value)
		}
	}
	return result
}

func equalIDs[T ~string](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validatePersistenceMembers(guards []EligibilityGuardTransition, checkpoints []CheckpointRecord) error {
	seen := make(map[string]bool, len(guards)+len(checkpoints))
	for _, guard := range guards {
		key := string(guard.MissionID) + "/" + string(guard.ShardID)
		if validateIdentity("mission_id", string(guard.MissionID)) != nil || validateIdentity("eligibility_shard_id", string(guard.ShardID)) != nil ||
			len(guard.Canonical) == 0 || seen["guard:"+key] {
			return invalid(ErrorInvalidTransition, "eligibility_guards")
		}
		seen["guard:"+key] = true
	}
	for _, checkpoint := range checkpoints {
		if validateIdentity("checkpoint_id", string(checkpoint.CheckpointID)) != nil || validateIdentity("checkpoint_slot_id", string(checkpoint.SlotID)) != nil ||
			seen["checkpoint:"+string(checkpoint.CheckpointID)] || seen["slot:"+string(checkpoint.SlotID)] {
			return invalid(ErrorInvalidTransition, "checkpoints")
		}
		seen["checkpoint:"+string(checkpoint.CheckpointID)] = true
		seen["slot:"+string(checkpoint.SlotID)] = true
	}
	return nil
}

func conditionTargetKey(target ConditionTarget) string {
	switch target.Kind {
	case ConditionTargetAggregateHead:
		return string(target.Kind) + "/" + target.Coordinate.canonical()
	case ConditionTargetEligibilityGuard:
		return string(target.Kind) + "/" + string(target.MissionID) + "/" + string(target.ShardID)
	case ConditionTargetCheckpointSlot:
		return string(target.Kind) + "/" + string(target.CheckpointSlotID)
	default:
		return string(target.Kind)
	}
}

func validateExactConditions(heads []HeadTransitionV2, guards []EligibilityGuardTransition, checkpoints []CheckpointRecord, conditions []ProviderCondition) error {
	if len(conditions) != len(heads)+len(guards)+len(checkpoints) {
		return invalid(ErrorInvalidTransition, "condition_membership")
	}
	expected := make(map[string]ProviderCondition, len(heads)+len(guards)+len(checkpoints))
	for _, head := range heads {
		condition, err := ConditionForHeadTransitionV2(head)
		if err != nil {
			return err
		}
		key := conditionTargetKey(condition.Target)
		if _, exists := expected[key]; exists {
			return invalid(ErrorInvalidTransition, "condition_membership")
		}
		expected[key] = condition
	}
	for _, guard := range guards {
		condition, err := ConditionForEligibilityGuardTransitionV2(guard)
		if err != nil {
			return err
		}
		key := conditionTargetKey(condition.Target)
		if _, exists := expected[key]; exists {
			return invalid(ErrorInvalidTransition, "condition_membership")
		}
		expected[key] = condition
	}
	for _, checkpoint := range checkpoints {
		condition, err := ConditionForCheckpointRecordV2(checkpoint)
		if err != nil {
			return err
		}
		key := conditionTargetKey(condition.Target)
		if _, exists := expected[key]; exists {
			return invalid(ErrorInvalidTransition, "condition_membership")
		}
		expected[key] = condition
	}
	seen := make(map[string]bool, len(conditions))
	for _, condition := range conditions {
		if err := validateConditionTarget(condition.Target); err != nil {
			return invalid(ErrorInvalidTransition, "condition_target")
		}
		key := conditionTargetKey(condition.Target)
		want, exists := expected[key]
		if !exists || seen[key] || condition != want {
			return invalid(ErrorInvalidTransition, "condition_membership")
		}
		seen[key] = true
	}
	return nil
}

func validateConditionTarget(target ConditionTarget) error {
	emptyCoordinate := target.Coordinate == (AggregateCoordinate{})
	switch target.Kind {
	case ConditionTargetAggregateHead:
		if target.MissionID != "" || target.ShardID != "" || target.CheckpointSlotID != "" {
			return invalid(ErrorInvalidTransition, "condition_target")
		}
		_, err := NewAggregateCoordinate(target.Coordinate.Kind, target.Coordinate.ID, target.Coordinate.Scope)
		return err
	case ConditionTargetEligibilityGuard:
		if !emptyCoordinate || target.CheckpointSlotID != "" || validateIdentity("mission_id", string(target.MissionID)) != nil ||
			validateIdentity("eligibility_shard_id", string(target.ShardID)) != nil {
			return invalid(ErrorInvalidTransition, "condition_target")
		}
	case ConditionTargetCheckpointSlot:
		if !emptyCoordinate || target.MissionID != "" || target.ShardID != "" || validateIdentity("checkpoint_slot_id", string(target.CheckpointSlotID)) != nil {
			return invalid(ErrorInvalidTransition, "condition_target")
		}
	default:
		return invalid(ErrorInvalidTransition, "condition_target")
	}
	return nil
}

func cloneEventDrafts(values []EventDraftV2) []EventDraftV2 {
	result := append([]EventDraftV2(nil), values...)
	for index := range result {
		result[index].GovernanceRevisions = append([]string(nil), result[index].GovernanceRevisions...)
		result[index].Observations = append([]byte(nil), result[index].Observations...)
		result[index].Payload = append([]byte(nil), result[index].Payload...)
	}
	return result
}

func cloneHeadTransitions(values []HeadTransitionV2) []HeadTransitionV2 {
	result := append([]HeadTransitionV2(nil), values...)
	for index := range result {
		result[index].AuthoritySummary = append([]byte(nil), result[index].AuthoritySummary...)
	}
	return result
}

func cloneEffectIntents(values []EffectIntentReference) []EffectIntentReference {
	result := append([]EffectIntentReference(nil), values...)
	for index := range result {
		result[index].Canonical = append([]byte(nil), result[index].Canonical...)
	}
	return result
}

func cloneEligibilityGuards(values []EligibilityGuardTransition) []EligibilityGuardTransition {
	result := append([]EligibilityGuardTransition(nil), values...)
	for index := range result {
		result[index].Canonical = append([]byte(nil), result[index].Canonical...)
	}
	return result
}

func cloneCheckpoints(values []CheckpointRecord) []CheckpointRecord {
	result := append([]CheckpointRecord(nil), values...)
	for index := range result {
		result[index].Canonical = append([]byte(nil), result[index].Canonical...)
		result[index].SlotCanonical = append([]byte(nil), result[index].SlotCanonical...)
	}
	return result
}

func requireCanonicalValue(field string, value []byte, maximum int) ([]byte, error) {
	canonical, err := canonicalObjectBounded(value, maximum)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, value) {
		return nil, invalid(ErrorInvalidCanonicalData, field)
	}
	return append([]byte(nil), canonical...), nil
}

func deriveSealedRecords(binding *OperationBindingV2, state OperationStateV2, events []EventEnvelopeV2, heads []HeadTransitionV2,
	effects []EffectIntentReference, guards []EligibilityGuardTransition, checkpoints []CheckpointRecord, conditions []ProviderCondition,
) ([]SealedRecordV2, error) {
	conditionByTarget := make(map[string]ProviderCondition, len(conditions))
	for _, condition := range conditions {
		conditionByTarget[conditionTargetKey(condition.Target)] = condition
	}
	records := make([]SealedRecordV2, 0, boolInt(binding != nil)+len(events)+len(heads)+len(effects)+len(guards)+2*len(checkpoints))
	absent := SealedRecordConditionV2{kind: ConditionAbsent}
	appendRecord := func(role SealedRecordRole, target SealedRecordTargetV2, class SealedRecordValueClass, condition SealedRecordConditionV2, canonical []byte) {
		records = append(records, SealedRecordV2{role: role, target: target, valueClass: class, condition: condition, canonical: append([]byte(nil), canonical...)})
	}
	if binding != nil {
		canonical, err := EncodeOperationBindingV2(*binding)
		if err != nil {
			return nil, err
		}
		appendRecord(RecordOperationBinding, SealedRecordTargetV2{operationID: binding.operationID}, RecordValueOrdinary, absent, canonical)
	}
	for _, event := range events {
		canonical, err := EncodeEventEnvelopeV2(event)
		if err != nil {
			return nil, err
		}
		appendRecord(RecordJournalEvent, SealedRecordTargetV2{coordinate: event.input.Coordinate, logicalVersion: event.input.AggregateLogicalVersion}, RecordValueOrdinary, absent, canonical)
	}
	eventByCoordinate := make(map[string]EventEnvelopeV2, len(events))
	for _, event := range events {
		eventByCoordinate[event.input.Coordinate.canonical()] = event
	}
	stateBytes, err := EncodeOperationStateV2(state)
	if err != nil {
		return nil, err
	}
	for _, transition := range heads {
		providerCondition, ok := conditionByTarget[conditionTargetKey(ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: transition.Coordinate})]
		if !ok {
			return nil, invalid(ErrorInvalidTransition, "sealed_record_condition")
		}
		condition := SealedRecordConditionV2{kind: providerCondition.Kind, observedRevision: providerCondition.ObservedRevision}
		if transition.Coordinate.Kind == AggregateKind("operation") {
			appendRecord(RecordOperationHead, SealedRecordTargetV2{operationID: state.operationID}, RecordValueOrdinary, condition, stateBytes)
			continue
		}
		event := eventByCoordinate[transition.Coordinate.canonical()]
		head, err := NewAggregateHeadV2(AggregateHeadInput{
			Coordinate: transition.Coordinate, LogicalVersion: transition.NextLogicalVersion, LastEventID: transition.EventID,
			LastPublicationID: state.lastPublicationID, CommittedRuntimeEpochID: transition.CommittedRuntimeEpochID,
			CanonicalStateDigest: transition.CanonicalStateDigest, AuthoritySummary: transition.AuthoritySummary, Tombstone: transition.Tombstone,
		})
		if err != nil || event.input.EventID != transition.EventID || event.input.CommittedRuntimeEpochID != transition.CommittedRuntimeEpochID {
			return nil, invalid(ErrorInvalidTransition, "aggregate_head_projection")
		}
		canonical, err := EncodeAggregateHeadV2(head)
		if err != nil {
			return nil, err
		}
		appendRecord(RecordAggregateHead, SealedRecordTargetV2{coordinate: transition.Coordinate}, RecordValueOrdinary, condition, canonical)
	}
	for _, effect := range effects {
		appendRecord(RecordEffectIntent, SealedRecordTargetV2{effectID: effect.EffectID}, RecordValueOrdinary, absent, effect.Canonical)
	}
	for _, guard := range guards {
		providerCondition, ok := conditionByTarget[conditionTargetKey(ConditionTarget{Kind: ConditionTargetEligibilityGuard, MissionID: guard.MissionID, ShardID: guard.ShardID})]
		if !ok {
			return nil, invalid(ErrorInvalidTransition, "sealed_record_condition")
		}
		condition := SealedRecordConditionV2{kind: providerCondition.Kind, observedRevision: providerCondition.ObservedRevision}
		appendRecord(RecordEligibilityGuard, SealedRecordTargetV2{missionID: guard.MissionID, shardID: guard.ShardID}, RecordValueOrdinary, condition, guard.Canonical)
	}
	for _, checkpoint := range checkpoints {
		appendRecord(RecordCheckpointRecord, SealedRecordTargetV2{checkpointID: checkpoint.CheckpointID}, RecordValueCheckpoint, absent, checkpoint.Canonical)
		providerCondition, ok := conditionByTarget[conditionTargetKey(ConditionTarget{Kind: ConditionTargetCheckpointSlot, CheckpointSlotID: checkpoint.SlotID})]
		if !ok {
			return nil, invalid(ErrorInvalidTransition, "sealed_record_condition")
		}
		condition := SealedRecordConditionV2{kind: providerCondition.Kind, observedRevision: providerCondition.ObservedRevision}
		appendRecord(RecordCheckpointSlot, SealedRecordTargetV2{checkpointSlotID: checkpoint.SlotID}, RecordValueOrdinary, condition, checkpoint.SlotCanonical)
	}
	sort.Slice(records, func(i, j int) bool { return sealedRecordOrderKey(records[i]) < sealedRecordOrderKey(records[j]) })
	return records, nil
}

// validateSealedRecordBudget is the exact value-side closure check. Targets
// retain the conservative 1 KiB charge because canonical provider keys belong
// to WP03; values and membership are exact because projection is now sealed.
func validateSealedRecordBudget(records []SealedRecordV2) (uint64, error) {
	if len(records) > MaxMembers {
		return 0, invalid(ErrorBudgetExceeded, "sealed_record_projection")
	}
	budget := productRawBudget{}
	for _, record := range records {
		if len(record.canonical) == 0 {
			return 0, invalid(ErrorInvalidCanonicalData, "sealed_record_value")
		}
		if err := budget.addRecord(uint64(len(record.canonical)), record.valueClass); err != nil {
			return 0, err
		}
	}
	return budget.value, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func sealedRecordOrderKey(record SealedRecordV2) string {
	rank := sealedRecordRoleRank(record.role)
	target := record.target
	return rank + "/" + string(target.operationID) + "/" + target.coordinate.canonical() + "/" + strconv.FormatUint(uint64(target.logicalVersion), 10) + "/" +
		string(target.effectID) + "/" + string(target.missionID) + "/" + string(target.shardID) + "/" + string(target.checkpointID) + "/" + string(target.checkpointSlotID)
}

func sealedRecordRoleRank(role SealedRecordRole) string {
	switch role {
	case RecordOperationBinding:
		return "0"
	case RecordJournalEvent:
		return "1"
	case RecordOperationHead:
		return "2"
	case RecordAggregateHead:
		return "3"
	case RecordEffectIntent:
		return "4"
	case RecordEligibilityGuard:
		return "5"
	case RecordCheckpointRecord:
		return "6"
	case RecordCheckpointSlot:
		return "7"
	default:
		panic("unsealed record role")
	}
}

func cloneBinding(value *OperationBindingV2) *OperationBindingV2 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func planPreimage(binding *OperationBindingV2, prior *OperationStateV2, state OperationStateV2, events []EventDraftV2, heads []HeadTransitionV2, effects []EffectIntentReference, eligibility []EligibilityGuardTransition, checkpoints []CheckpointRecord, conditions []ProviderCondition) ([]byte, error) {
	var bindingWire *operationBindingWire
	if binding != nil {
		value := binding.wire()
		bindingWire = &value
	}
	var priorWire *operationStatePreimageWire
	if prior != nil {
		value := operationStatePreimage(*prior, true)
		priorWire = &value
	}
	return marshalCanonical(struct {
		Binding     *operationBindingWire        `json:"operation_binding_create,omitempty"`
		PriorState  *operationStatePreimageWire  `json:"prior_operation_state,omitempty"`
		State       operationStatePreimageWire   `json:"operation_state"`
		Events      []EventDraftV2               `json:"events"`
		Heads       []HeadTransitionV2           `json:"heads"`
		Effects     []EffectIntentReference      `json:"effect_intents"`
		Eligibility []EligibilityGuardTransition `json:"eligibility_guards"`
		Checkpoints []CheckpointRecord           `json:"checkpoints"`
		Conditions  []ProviderCondition          `json:"conditions"`
	}{bindingWire, priorWire, operationStatePreimage(state, false), events, heads, effects, eligibility, checkpoints, conditions}, MaxCommitPlanBytes)
}

type operationStatePreimageWire struct {
	OperationID         string          `json:"operation_id"`
	LogicalVersion      uint64          `json:"logical_version"`
	Status              OperationStatus `json:"status"`
	ResultKind          string          `json:"result_kind,omitempty"`
	ResultSchemaVersion uint32          `json:"result_schema_version,omitempty"`
	CanonicalResult     []byte          `json:"canonical_result,omitempty"`
	ResultSHA256        string          `json:"result_sha256,omitempty"`
	EventIDs            []EventID       `json:"event_ids"`
	EffectIDs           []EffectID      `json:"effect_ids"`
	LastPublicationID   string          `json:"last_publication_id,omitempty"`
}

func operationStatePreimage(state OperationStateV2, includePublication bool) operationStatePreimageWire {
	resultDigest := ""
	if !state.resultDigest.IsZero() {
		resultDigest = state.resultDigest.String()
	}
	publicationID := ""
	if includePublication {
		publicationID = string(state.lastPublicationID)
	}
	return operationStatePreimageWire{string(state.operationID), uint64(state.logicalVersion), state.status, state.resultKind, state.resultSchemaVersion,
		append([]byte(nil), state.canonicalResult...), resultDigest, append([]EventID(nil), state.eventIDs...), append([]EffectID(nil), state.effectIDs...), publicationID}
}

func (p SealedCommitPlanV2) finalBytes() ([]byte, error) {
	var binding *operationBindingWire
	if p.binding != nil {
		value := p.binding.wire()
		binding = &value
	}
	state, err := EncodeOperationStateV2(p.operationState)
	if err != nil {
		return nil, err
	}
	events := make([][]byte, len(p.events))
	for index, event := range p.events {
		encoded, err := EncodeEventEnvelopeV2(event)
		if err != nil {
			return nil, err
		}
		events[index] = encoded
	}
	return marshalCanonical(struct {
		PublicationID   string                       `json:"publication_id"`
		PlanSHA256      string                       `json:"canonical_plan_sha256"`
		RecordSetSHA256 string                       `json:"sealed_record_set_sha256"`
		Binding         *operationBindingWire        `json:"operation_binding_create,omitempty"`
		OperationState  []byte                       `json:"operation_state"`
		Events          [][]byte                     `json:"events"`
		Heads           []HeadTransitionV2           `json:"heads"`
		Effects         []EffectIntentReference      `json:"effect_intents"`
		Eligibility     []EligibilityGuardTransition `json:"eligibility_guards"`
		Checkpoints     []CheckpointRecord           `json:"checkpoints"`
		Conditions      []ProviderCondition          `json:"conditions"`
	}{string(p.publicationID), p.digest.String(), p.recordSetDigest.String(), binding, state, events, p.heads, p.effects, p.eligibility, p.checkpoints, p.conditions}, MaxCommitPlanBytes)
}

func (p SealedCommitPlanV2) PublicationID() PublicationID     { return p.publicationID }
func (p SealedCommitPlanV2) Digest() Digest                   { return p.digest }
func (p SealedCommitPlanV2) RecordSetDigest() Digest          { return p.recordSetDigest }
func (p SealedCommitPlanV2) MutationCount() int               { return len(p.records) }
func (p SealedCommitPlanV2) CanonicalBytes() []byte           { return append([]byte(nil), p.canonical...) }
func (p SealedCommitPlanV2) OperationState() OperationStateV2 { return p.operationState }
func (p SealedCommitPlanV2) Events() []EventEnvelopeV2 {
	return append([]EventEnvelopeV2(nil), p.events...)
}
func (p SealedCommitPlanV2) Heads() []HeadTransitionV2 {
	return cloneHeadTransitions(p.heads)
}
func (p SealedCommitPlanV2) Conditions() []ProviderCondition {
	return append([]ProviderCondition(nil), p.conditions...)
}
func (p SealedCommitPlanV2) BindingCreate() *OperationBindingV2 { return cloneBinding(p.binding) }
func (p SealedCommitPlanV2) EffectIntents() []EffectIntentReference {
	return cloneEffectIntents(p.effects)
}
func (p SealedCommitPlanV2) EligibilityGuards() []EligibilityGuardTransition {
	return cloneEligibilityGuards(p.eligibility)
}
func (p SealedCommitPlanV2) Checkpoints() []CheckpointRecord { return cloneCheckpoints(p.checkpoints) }
func (p SealedCommitPlanV2) Records() []SealedRecordV2 {
	result := append([]SealedRecordV2(nil), p.records...)
	for index := range result {
		result[index].canonical = append([]byte(nil), result[index].canonical...)
	}
	return result
}

func DecodePublicationID(value string) (PublicationID, error) {
	if len(value) != 64 {
		return "", invalid(ErrorInvalidDigest, "publication_id")
	}
	for _, char := range []byte(value) {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return "", invalid(ErrorInvalidDigest, "publication_id")
		}
	}
	return PublicationID(value), nil
}
