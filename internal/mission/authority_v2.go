package mission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const (
	MissionAuthoritySchemaVersionV2 = uint32(1)
	MaxMissionAuthorityMembersV2    = 10_000
	MaxMissionAuthorityTextV2       = 256
)

type MissionAuthorityErrorKindV2 string

const (
	MissionAuthorityInvalidRequestV2      MissionAuthorityErrorKindV2 = "INVALID_REQUEST"
	MissionAuthorityIdempotencyConflictV2 MissionAuthorityErrorKindV2 = "IDEMPOTENCY_CONFLICT"
	MissionAuthorityStaleEpochV2          MissionAuthorityErrorKindV2 = "STALE_EPOCH"
	MissionAuthorityNotFoundV2            MissionAuthorityErrorKindV2 = "NOT_FOUND"
	MissionAuthorityVersionConflictV2     MissionAuthorityErrorKindV2 = "VERSION_CONFLICT"
	MissionAuthorityInvalidTransitionV2   MissionAuthorityErrorKindV2 = "INVALID_TRANSITION"
	MissionAuthorityActiveBlockerV2       MissionAuthorityErrorKindV2 = "ACTIVE_BLOCKER"
	MissionAuthorityOpenDecisionV2        MissionAuthorityErrorKindV2 = "OPEN_DECISION"
	MissionAuthorityTerminalV2            MissionAuthorityErrorKindV2 = "TERMINAL_MISSION"
	MissionAuthorityAmbiguousV2           MissionAuthorityErrorKindV2 = "AMBIGUOUS"
	MissionAuthorityCorruptV2             MissionAuthorityErrorKindV2 = "AUTHORITY_CORRUPT"
)

type MissionAuthorityErrorV2 struct {
	Kind  MissionAuthorityErrorKindV2
	Field string
	Cause error
}

func (e *MissionAuthorityErrorV2) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Field
}
func (e *MissionAuthorityErrorV2) Unwrap() error { return e.Cause }

func IsMissionAuthorityKindV2(err error, kind MissionAuthorityErrorKindV2) bool {
	var target *MissionAuthorityErrorV2
	return errors.As(err, &target) && target.Kind == kind
}

func missionAuthorityErrorV2(kind MissionAuthorityErrorKindV2, field string, cause error) error {
	if len(field) > 64 {
		field = "authority"
	}
	return &MissionAuthorityErrorV2{Kind: kind, Field: field, Cause: cause}
}

type AuthorityMissionStatusV2 string

const (
	AuthorityMissionActiveV2    AuthorityMissionStatusV2 = "active"
	AuthorityMissionPausedV2    AuthorityMissionStatusV2 = "paused"
	AuthorityMissionFailedV2    AuthorityMissionStatusV2 = "failed"
	AuthorityMissionCanceledV2  AuthorityMissionStatusV2 = "canceled"
	AuthorityMissionCompletedV2 AuthorityMissionStatusV2 = "completed"
)

func (s AuthorityMissionStatusV2) terminal() bool {
	return s == AuthorityMissionFailedV2 || s == AuthorityMissionCanceledV2 || s == AuthorityMissionCompletedV2
}

func (s AuthorityMissionStatusV2) validate() error {
	switch s {
	case AuthorityMissionActiveV2, AuthorityMissionPausedV2, AuthorityMissionFailedV2, AuthorityMissionCanceledV2, AuthorityMissionCompletedV2:
		return nil
	default:
		return missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_status", nil)
	}
}

type DecompositionPhaseV2 string

const (
	DecompositionNoneV2                   DecompositionPhaseV2 = "none"
	DecompositionRequestedV2              DecompositionPhaseV2 = "requested"
	DecompositionProgressV2               DecompositionPhaseV2 = "progress"
	DecompositionReconciliationRequiredV2 DecompositionPhaseV2 = "reconciliation_required"
	DecompositionCompletedV2              DecompositionPhaseV2 = "completed"
)

func (p DecompositionPhaseV2) validate() error {
	switch p {
	case DecompositionNoneV2, DecompositionRequestedV2, DecompositionProgressV2, DecompositionReconciliationRequiredV2, DecompositionCompletedV2:
		return nil
	default:
		return missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_phase", nil)
	}
}

type MissionAuthorityStateInputV2 struct {
	ProjectID             journalv2.ProjectID
	MissionID             journalv2.MissionID
	DefinitionRevision    string
	DefinitionDigest      journalv2.Digest
	Version               journalv2.LogicalVersion
	Status                AuthorityMissionStatusV2
	Step                  string
	CurrentWorkRevisionID journalv2.WorkRevisionID
	ActionCount           uint64
	ActiveBlockerIDs      []string
	OpenDecisionIDs       []string
	Decomposition         DecompositionPhaseV2
	EffectIDs             []journalv2.EffectID
	LastOperationID       journalv2.OperationID
	ObservedAt            string
}

type MissionAuthorityStateV2 struct {
	input     MissionAuthorityStateInputV2
	blockers  []string
	decisions []string
	effects   []journalv2.EffectID
	canonical []byte
	digest    journalv2.Digest
}

type missionAuthorityStateWireV2 struct {
	SchemaVersion         uint32                   `json:"schema_version"`
	ProjectID             journalv2.ProjectID      `json:"project_id"`
	MissionID             journalv2.MissionID      `json:"mission_id"`
	DefinitionRevision    string                   `json:"definition_revision"`
	DefinitionSHA256      string                   `json:"definition_sha256"`
	Version               uint64                   `json:"version"`
	Status                AuthorityMissionStatusV2 `json:"status"`
	Step                  string                   `json:"step"`
	CurrentWorkRevisionID journalv2.WorkRevisionID `json:"current_work_revision_id,omitempty"`
	ActionCount           uint64                   `json:"action_count"`
	ActiveBlockerIDs      []string                 `json:"active_blocker_ids"`
	OpenDecisionIDs       []string                 `json:"open_decision_ids"`
	Decomposition         DecompositionPhaseV2     `json:"decomposition"`
	EffectIDs             []journalv2.EffectID     `json:"effect_ids"`
	LastOperationID       journalv2.OperationID    `json:"last_operation_id"`
	ObservedAt            string                   `json:"observed_at"`
}

func NewMissionAuthorityStateV2(input MissionAuthorityStateInputV2) (MissionAuthorityStateV2, error) {
	if input.Decomposition == "" {
		input.Decomposition = DecompositionNoneV2
	}
	if _, err := journalv2.NewProjectID(string(input.ProjectID)); err != nil {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "project_id", err)
	}
	if _, err := journalv2.NewMissionID(string(input.MissionID)); err != nil {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_id", err)
	}
	if !boundedAuthorityIdentityV2(input.DefinitionRevision) || input.DefinitionDigest.IsZero() || input.Version == 0 || input.Status.validate() != nil || !boundedAuthorityIdentityV2(input.Step) || input.Decomposition.validate() != nil {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_state", nil)
	}
	if input.CurrentWorkRevisionID != "" {
		if _, err := journalv2.NewWorkRevisionID(string(input.CurrentWorkRevisionID)); err != nil {
			return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_id", err)
		}
	}
	if input.LastOperationID != "" {
		if _, err := journalv2.NewOperationID(string(input.LastOperationID)); err != nil {
			return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "last_operation_id", err)
		}
	}
	if !canonicalAuthorityTimeV2(input.ObservedAt) {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "observed_at", nil)
	}
	blockers, err := canonicalAuthorityStringsV2(input.ActiveBlockerIDs)
	if err != nil {
		return MissionAuthorityStateV2{}, err
	}
	decisions, err := canonicalAuthorityStringsV2(input.OpenDecisionIDs)
	if err != nil {
		return MissionAuthorityStateV2{}, err
	}
	effects := append([]journalv2.EffectID(nil), input.EffectIDs...)
	if len(effects) > journalv2.MaxMembers || !sort.SliceIsSorted(effects, func(i, j int) bool { return effects[i] < effects[j] }) {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "effect_ids", nil)
	}
	for index, id := range effects {
		if _, err := journalv2.NewEffectID(string(id)); err != nil || index > 0 && id == effects[index-1] {
			return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "effect_ids", err)
		}
	}
	input.ActiveBlockerIDs, input.OpenDecisionIDs, input.EffectIDs = nil, nil, nil
	state := MissionAuthorityStateV2{input: input, blockers: blockers, decisions: decisions, effects: effects}
	canonical, err := canonicalMissionAuthorityStateV2(state)
	if err != nil {
		return MissionAuthorityStateV2{}, err
	}
	digest, err := journalv2.ReducerStateDigestV2(canonical)
	if err != nil {
		return MissionAuthorityStateV2{}, err
	}
	state.canonical, state.digest = canonical, digest
	return state, nil
}

func (s MissionAuthorityStateV2) IsZero() bool                       { return len(s.canonical) == 0 }
func (s MissionAuthorityStateV2) ProjectID() journalv2.ProjectID     { return s.input.ProjectID }
func (s MissionAuthorityStateV2) MissionID() journalv2.MissionID     { return s.input.MissionID }
func (s MissionAuthorityStateV2) Version() journalv2.LogicalVersion  { return s.input.Version }
func (s MissionAuthorityStateV2) Status() AuthorityMissionStatusV2   { return s.input.Status }
func (s MissionAuthorityStateV2) Step() string                       { return s.input.Step }
func (s MissionAuthorityStateV2) DefinitionRevision() string         { return s.input.DefinitionRevision }
func (s MissionAuthorityStateV2) DefinitionDigest() journalv2.Digest { return s.input.DefinitionDigest }
func (s MissionAuthorityStateV2) CurrentWorkRevisionID() journalv2.WorkRevisionID {
	return s.input.CurrentWorkRevisionID
}
func (s MissionAuthorityStateV2) ActionCount() uint64 { return s.input.ActionCount }
func (s MissionAuthorityStateV2) ActiveBlockerIDs() []string {
	return append([]string(nil), s.blockers...)
}
func (s MissionAuthorityStateV2) OpenDecisionIDs() []string {
	return append([]string(nil), s.decisions...)
}
func (s MissionAuthorityStateV2) Decomposition() DecompositionPhaseV2 { return s.input.Decomposition }
func (s MissionAuthorityStateV2) EffectIDs() []journalv2.EffectID {
	return append([]journalv2.EffectID(nil), s.effects...)
}
func (s MissionAuthorityStateV2) LastOperationID() journalv2.OperationID {
	return s.input.LastOperationID
}
func (s MissionAuthorityStateV2) ObservedAt() string       { return s.input.ObservedAt }
func (s MissionAuthorityStateV2) Digest() journalv2.Digest { return s.digest }
func (s MissionAuthorityStateV2) CanonicalBytes() []byte   { return append([]byte(nil), s.canonical...) }

func canonicalMissionAuthorityStateV2(s MissionAuthorityStateV2) ([]byte, error) {
	wire := missionAuthorityStateWireV2{
		SchemaVersion: MissionAuthoritySchemaVersionV2, ProjectID: s.input.ProjectID, MissionID: s.input.MissionID,
		DefinitionRevision: s.input.DefinitionRevision, DefinitionSHA256: s.input.DefinitionDigest.String(), Version: uint64(s.input.Version),
		Status: s.input.Status, Step: s.input.Step, CurrentWorkRevisionID: s.input.CurrentWorkRevisionID, ActionCount: s.input.ActionCount,
		ActiveBlockerIDs: append([]string(nil), s.blockers...), OpenDecisionIDs: append([]string(nil), s.decisions...),
		Decomposition: s.input.Decomposition, EffectIDs: append([]journalv2.EffectID(nil), s.effects...), LastOperationID: s.input.LastOperationID,
		ObservedAt: s.input.ObservedAt,
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_state", err)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_state", err)
	}
	return canonical, nil
}

func DecodeMissionAuthorityStateV2(encoded []byte) (MissionAuthorityStateV2, error) {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", nil)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire missionAuthorityStateWireV2
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != MissionAuthoritySchemaVersionV2 {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	digest, err := journalv2.ParseDigest("definition_sha256", wire.DefinitionSHA256)
	if err != nil {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	state, err := NewMissionAuthorityStateV2(MissionAuthorityStateInputV2{
		ProjectID: wire.ProjectID, MissionID: wire.MissionID, DefinitionRevision: wire.DefinitionRevision, DefinitionDigest: digest,
		Version: journalv2.LogicalVersion(wire.Version), Status: wire.Status, Step: wire.Step, CurrentWorkRevisionID: wire.CurrentWorkRevisionID,
		ActionCount: wire.ActionCount, ActiveBlockerIDs: wire.ActiveBlockerIDs, OpenDecisionIDs: wire.OpenDecisionIDs,
		Decomposition: wire.Decomposition, EffectIDs: wire.EffectIDs, LastOperationID: wire.LastOperationID, ObservedAt: wire.ObservedAt,
	})
	if err != nil || !bytes.Equal(state.canonical, encoded) {
		return MissionAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	return state, nil
}

type MissionEventTypeV2 string

const (
	MissionEventCreatedV2                     MissionEventTypeV2 = "mission_created"
	MissionEventLifecycleAdvancedV2           MissionEventTypeV2 = "lifecycle_advanced"
	MissionEventPausedV2                      MissionEventTypeV2 = "mission_paused"
	MissionEventResumedV2                     MissionEventTypeV2 = "mission_resumed"
	MissionEventFailedV2                      MissionEventTypeV2 = "mission_failed"
	MissionEventCanceledV2                    MissionEventTypeV2 = "mission_canceled"
	MissionEventCompletedV2                   MissionEventTypeV2 = "mission_completed"
	MissionEventActionRecordedV2              MissionEventTypeV2 = "action_recorded"
	MissionEventBlockerRaisedV2               MissionEventTypeV2 = "blocker_raised"
	MissionEventBlockerClearedV2              MissionEventTypeV2 = "blocker_cleared"
	MissionEventDecisionOpenedV2              MissionEventTypeV2 = "decision_opened"
	MissionEventDecisionResolvedV2            MissionEventTypeV2 = "decision_resolved"
	MissionEventDecisionDeferredV2            MissionEventTypeV2 = "decision_deferred"
	MissionEventDecisionCanceledV2            MissionEventTypeV2 = "decision_canceled"
	MissionEventDecompositionRequestedV2      MissionEventTypeV2 = "decomposition_requested"
	MissionEventDecompositionProgressedV2     MissionEventTypeV2 = "decomposition_progressed"
	MissionEventDecompositionReconciliationV2 MissionEventTypeV2 = "decomposition_reconciliation_required"
	MissionEventDecompositionCompletedV2      MissionEventTypeV2 = "decomposition_completed"
)

func (e MissionEventTypeV2) validate() error {
	switch e {
	case MissionEventCreatedV2, MissionEventLifecycleAdvancedV2, MissionEventPausedV2, MissionEventResumedV2,
		MissionEventFailedV2, MissionEventCanceledV2, MissionEventCompletedV2, MissionEventActionRecordedV2,
		MissionEventBlockerRaisedV2, MissionEventBlockerClearedV2, MissionEventDecisionOpenedV2,
		MissionEventDecisionResolvedV2, MissionEventDecisionDeferredV2, MissionEventDecisionCanceledV2,
		MissionEventDecompositionRequestedV2, MissionEventDecompositionProgressedV2,
		MissionEventDecompositionReconciliationV2, MissionEventDecompositionCompletedV2:
		return nil
	default:
		return missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "event_type", nil)
	}
}

type MissionEventPayloadV2 struct {
	eventType MissionEventTypeV2
	prior     MissionAuthorityStateV2
	next      MissionAuthorityStateV2
}

type missionEventPayloadWireV2 struct {
	SchemaVersion uint32             `json:"schema_version"`
	EventType     MissionEventTypeV2 `json:"event_type"`
	Prior         json.RawMessage    `json:"prior,omitempty"`
	Next          json.RawMessage    `json:"next"`
}

func NewMissionEventPayloadV2(eventType MissionEventTypeV2, prior, next MissionAuthorityStateV2) (MissionEventPayloadV2, error) {
	if eventType.validate() != nil || next.IsZero() {
		return MissionEventPayloadV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_event", nil)
	}
	if err := validateMissionTransitionV2(eventType, prior, next); err != nil {
		return MissionEventPayloadV2{}, err
	}
	return MissionEventPayloadV2{eventType: eventType, prior: prior, next: next}, nil
}

func (p MissionEventPayloadV2) EventType() MissionEventTypeV2      { return p.eventType }
func (p MissionEventPayloadV2) Prior() MissionAuthorityStateV2     { return p.prior }
func (p MissionEventPayloadV2) Successor() MissionAuthorityStateV2 { return p.next }

func EncodeMissionEventPayloadV2(payload MissionEventPayloadV2) ([]byte, error) {
	validated, err := NewMissionEventPayloadV2(payload.eventType, payload.prior, payload.next)
	if err != nil {
		return nil, err
	}
	var prior json.RawMessage
	if !validated.prior.IsZero() {
		prior = validated.prior.CanonicalBytes()
	}
	encoded, err := json.Marshal(missionEventPayloadWireV2{MissionAuthoritySchemaVersionV2, validated.eventType, prior, validated.next.CanonicalBytes()})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeMissionEventPayloadV2(encoded []byte) (MissionEventPayloadV2, error) {
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return MissionEventPayloadV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_event", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire missionEventPayloadWireV2
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != MissionAuthoritySchemaVersionV2 {
		return MissionEventPayloadV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_event", err)
	}
	var prior MissionAuthorityStateV2
	if len(wire.Prior) != 0 {
		prior, err = DecodeMissionAuthorityStateV2(wire.Prior)
		if err != nil {
			return MissionEventPayloadV2{}, err
		}
	}
	next, err := DecodeMissionAuthorityStateV2(wire.Next)
	if err != nil {
		return MissionEventPayloadV2{}, err
	}
	payload, err := NewMissionEventPayloadV2(wire.EventType, prior, next)
	if err != nil {
		return MissionEventPayloadV2{}, err
	}
	reencoded, _ := EncodeMissionEventPayloadV2(payload)
	if !bytes.Equal(reencoded, encoded) {
		return MissionEventPayloadV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_event", nil)
	}
	return payload, nil
}

func ReduceMissionEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	if event.Family() != "mission" || event.EventSchemaVersion() != 1 || event.ReducerVersion() != 1 || event.Coordinate().Kind != "mission" {
		return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_tuple", nil)
	}
	if event.Coordinate().Scope.Kind == journalv2.ScopeMission && event.Coordinate().ID != journalv2.AggregateID(event.Coordinate().Scope.MissionID) {
		return reduceMissionFactEventV2(prior, event)
	}
	payload, err := DecodeMissionEventPayloadV2(event.Payload())
	if err != nil || string(payload.eventType) != event.EventType() || payload.next.Version() != event.LogicalVersion() || payload.next.MissionID() != journalv2.MissionID(event.Coordinate().ID) {
		return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_membership", err)
	}
	if payload.prior.IsZero() {
		if !bytes.Equal(prior, []byte(`{}`)) && len(prior) != 0 {
			return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
		}
	} else {
		decoded, decodeErr := DecodeMissionAuthorityStateV2(prior)
		if decodeErr != nil || decoded.Digest() != payload.prior.Digest() {
			return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", decodeErr)
		}
	}
	return payload.next.CanonicalBytes(), nil
}

func MissionReducerImplementationDigestV2() journalv2.Digest {
	digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"implementation":"mission-authority-v2"}`))
	return digest
}

type OperationResolutionKindV2 string

const (
	OperationAbsentV2   OperationResolutionKindV2 = "absent"
	OperationExactV2    OperationResolutionKindV2 = "exact"
	OperationConflictV2 OperationResolutionKindV2 = "conflict"
)

type OperationResolutionV2 struct {
	Kind                OperationResolutionKindV2
	Binding             journalv2.OperationBindingV2
	State               journalv2.OperationStateV2
	StateRecordRevision uint64
}

type MissionObservationV2 struct {
	State          MissionAuthorityStateV2
	RecordRevision uint64
	LastEventID    journalv2.EventID
}

type MissionFactObservationV2 struct {
	State          MissionFactStateV2
	RecordRevision uint64
	LastEventID    journalv2.EventID
	Found          bool
}

type MissionAuthorityPortV2 interface {
	ResolveOperation(context.Context, journalv2.OperationBindingV2) (OperationResolutionV2, error)
	ObserveMission(context.Context, journalv2.ProjectID, journalv2.MissionID) (MissionObservationV2, error)
	ObserveMissionFact(context.Context, journalv2.ProjectID, journalv2.MissionID, MissionFactKindV2, string) (MissionFactObservationV2, error)
	PublishMission(context.Context, journalv2.SealedCommitPlanV2) (MissionPublicationV2, error)
}

// MissionPublicationV2 is the capability-owned durable acknowledgement view.
// It contains only the coordinates needed to continue the same operation;
// provider values and errors never enter the mission package.
type MissionPublicationV2 struct {
	PublicationID          journalv2.PublicationID
	ProviderRevision       uint64
	DurableThroughRevision uint64
	MutationCount          int
}

func (p MissionPublicationV2) validate(plan journalv2.SealedCommitPlanV2) error {
	if p.PublicationID != plan.PublicationID() || p.ProviderRevision == 0 || p.DurableThroughRevision < p.ProviderRevision || p.MutationCount != plan.MutationCount() {
		return missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "publication_acknowledgement", nil)
	}
	return nil
}

type RuntimeEpochSourceV2 interface {
	CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error)
}

type MissionEffectIntentV2 struct {
	EffectID  journalv2.EffectID
	Canonical []byte
}

type MissionAuthorityCommandV2 struct {
	SchemaVersion             uint32
	OperationID               journalv2.OperationID
	ProjectID                 journalv2.ProjectID
	MissionID                 journalv2.MissionID
	ActorID                   journalv2.ActorID
	ProcessID                 journalv2.ProcessID
	OriginatingRuntimeEpochID journalv2.RuntimeEpochID
	DoctrineRevision          string
	ProfileRevision           string
	PolicyRevision            string
	ObservedAt                string
	EventType                 MissionEventTypeV2
	Expected                  MissionAuthorityStateV2
	Successor                 MissionAuthorityStateV2
	EffectIntents             []MissionEffectIntentV2
	ObservationEvidence       []byte
	RelatedFact               *MissionFactTransitionV2
}

type MissionAuthorityResultV2 struct {
	state         MissionAuthorityStateV2
	operationID   journalv2.OperationID
	publicationID journalv2.PublicationID
	canonical     []byte
	replayed      bool
}

func (r MissionAuthorityResultV2) State() MissionAuthorityStateV2         { return r.state }
func (r MissionAuthorityResultV2) OperationID() journalv2.OperationID     { return r.operationID }
func (r MissionAuthorityResultV2) PublicationID() journalv2.PublicationID { return r.publicationID }
func (r MissionAuthorityResultV2) CanonicalBytes() []byte                 { return append([]byte(nil), r.canonical...) }
func (r MissionAuthorityResultV2) Replayed() bool                         { return r.replayed }

type missionAuthorityResultWireV2 struct {
	SchemaVersion uint32                `json:"schema_version"`
	OperationID   journalv2.OperationID `json:"operation_id"`
	State         json.RawMessage       `json:"state"`
}

type MissionAuthorityServiceV2 struct {
	port     MissionAuthorityPortV2
	epoch    RuntimeEpochSourceV2
	registry journalv2.EventRegistryV2
}

func NewMissionAuthorityServiceV2(port MissionAuthorityPortV2, epoch RuntimeEpochSourceV2, registry journalv2.EventRegistryV2) (*MissionAuthorityServiceV2, error) {
	if port == nil || epoch == nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "service", nil)
	}
	if _, err := registry.Resolve("mission", 1, 1); err != nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "registry", err)
	}
	return &MissionAuthorityServiceV2{port: port, epoch: epoch, registry: registry}, nil
}

func (s *MissionAuthorityServiceV2) Execute(ctx context.Context, command MissionAuthorityCommandV2) (MissionAuthorityResultV2, error) {
	binding, intentDigest, err := validateMissionAuthorityCommandV2(command)
	if err != nil {
		return MissionAuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveOperation(ctx, binding)
	if err != nil {
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "operation_resolution", err)
	}
	switch resolution.Kind {
	case OperationExactV2:
		if resolution.State.Status() == journalv2.StatusResolved || resolution.State.Status() == journalv2.StatusRefused {
			result, decodeErr := decodeMissionAuthorityResultV2(resolution.State.Result(), resolution.State.LastPublicationID(), true)
			if decodeErr != nil || result.operationID != command.OperationID {
				return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "operation_result", decodeErr)
			}
			return result, nil
		}
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "operation_in_progress", nil)
	case OperationConflictV2:
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityIdempotencyConflictV2, "operation_id", nil)
	case OperationAbsentV2:
	default:
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "operation_resolution", nil)
	}
	currentEpoch, err := s.epoch.CurrentRuntimeEpoch(ctx)
	if err != nil || currentEpoch != command.OriginatingRuntimeEpochID {
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityStaleEpochV2, "runtime_epoch", err)
	}
	var observed MissionObservationV2
	if command.EventType != MissionEventCreatedV2 {
		observed, err = s.port.ObserveMission(ctx, command.ProjectID, command.MissionID)
		if err != nil {
			return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityNotFoundV2, "mission", err)
		}
		if observed.State.Digest() != command.Expected.Digest() {
			return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "expected_mission", nil)
		}
	} else if !command.Expected.IsZero() {
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "expected_mission", nil)
	}
	if command.RelatedFact != nil {
		fact := command.RelatedFact
		factObservation, observeErr := s.port.ObserveMissionFact(ctx, command.ProjectID, command.MissionID, fact.Successor.Kind(), fact.Successor.FactID())
		if observeErr != nil {
			return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "mission_fact", observeErr)
		}
		if fact.Prior.IsZero() {
			if factObservation.Found {
				return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "mission_fact", nil)
			}
		} else if !factObservation.Found || factObservation.RecordRevision != fact.ExpectedRecordRevision || factObservation.State.Digest() != fact.Prior.Digest() {
			return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "mission_fact", nil)
		}
	}
	if err := validateMissionTransitionV2(command.EventType, command.Expected, command.Successor); err != nil {
		return MissionAuthorityResultV2{}, err
	}
	plan, canonicalResult, err := buildMissionAuthorityPlanV2(command, binding, intentDigest, observed, s.registry)
	if err != nil {
		return MissionAuthorityResultV2{}, err
	}
	publication, err := s.port.PublishMission(ctx, plan)
	if err != nil {
		var typed *MissionAuthorityErrorV2
		if errors.As(err, &typed) {
			return MissionAuthorityResultV2{}, err
		}
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "publication", err)
	}
	if err := publication.validate(plan); err != nil {
		return MissionAuthorityResultV2{}, err
	}
	return MissionAuthorityResultV2{state: command.Successor, operationID: command.OperationID, publicationID: plan.PublicationID(), canonical: canonicalResult}, nil
}

func validateMissionAuthorityCommandV2(command MissionAuthorityCommandV2) (journalv2.OperationBindingV2, journalv2.Digest, error) {
	if command.SchemaVersion != MissionAuthoritySchemaVersionV2 || command.EventType.validate() != nil || command.Successor.IsZero() ||
		command.Successor.ProjectID() != command.ProjectID || command.Successor.MissionID() != command.MissionID || command.Successor.LastOperationID() != command.OperationID ||
		!canonicalAuthorityTimeV2(command.ObservedAt) || command.Successor.ObservedAt() != command.ObservedAt || len(command.EffectIntents) > journalv2.MaxEffectsPerPublication || len(command.ObservationEvidence) > journalv2.MaxCanonicalPayloadBytes {
		return journalv2.OperationBindingV2{}, journalv2.Digest{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "command", nil)
	}
	if len(command.ObservationEvidence) != 0 {
		canonical, err := journalv2.CanonicalObject(command.ObservationEvidence)
		if err != nil || !bytes.Equal(canonical, command.ObservationEvidence) {
			return journalv2.OperationBindingV2{}, journalv2.Digest{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "observation_evidence", err)
		}
	}
	if err := validateMissionFactForEventV2(command.EventType, command.RelatedFact); err != nil {
		return journalv2.OperationBindingV2{}, journalv2.Digest{}, err
	}
	if command.RelatedFact != nil && (command.RelatedFact.Successor.input.ProjectID != command.ProjectID || command.RelatedFact.Successor.input.MissionID != command.MissionID || command.RelatedFact.Successor.input.LastOperationID != command.OperationID) {
		return journalv2.OperationBindingV2{}, journalv2.Digest{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "related_fact", nil)
	}
	for _, value := range []string{string(command.OperationID), string(command.ProjectID), string(command.MissionID), string(command.ActorID), string(command.ProcessID), string(command.OriginatingRuntimeEpochID), command.DoctrineRevision, command.ProfileRevision, command.PolicyRevision} {
		if !boundedAuthorityIdentityV2(value) {
			return journalv2.OperationBindingV2{}, journalv2.Digest{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "identity", nil)
		}
	}
	stable, err := encodeMissionAuthorityIntentV2(command)
	if err != nil {
		return journalv2.OperationBindingV2{}, journalv2.Digest{}, err
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: command.ProjectID, MissionID: command.MissionID}
	digest, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "mission." + string(command.EventType), CommandSchemaVersion: command.SchemaVersion, Scope: scope, Command: stable})
	if err != nil {
		return journalv2.OperationBindingV2{}, journalv2.Digest{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "intent", err)
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{
		OperationID: command.OperationID, CommandKind: "mission." + string(command.EventType), CommandSchemaVersion: command.SchemaVersion,
		CanonicalIntentDigest: digest, Scope: scope, ActorID: command.ActorID, ProcessID: command.ProcessID,
		DoctrineRevision: command.DoctrineRevision, ProfileRevision: command.ProfileRevision, PolicyRevision: command.PolicyRevision,
		OriginatingRuntimeEpochID: command.OriginatingRuntimeEpochID, ObservedCreatedAt: command.ObservedAt,
	})
	if err != nil {
		return journalv2.OperationBindingV2{}, journalv2.Digest{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "binding", err)
	}
	return binding, digest, nil
}

func encodeMissionAuthorityIntentV2(command MissionAuthorityCommandV2) ([]byte, error) {
	effects := append([]MissionEffectIntentV2(nil), command.EffectIntents...)
	sort.Slice(effects, func(i, j int) bool { return effects[i].EffectID < effects[j].EffectID })
	type effectWire struct {
		EffectID  journalv2.EffectID `json:"effect_id"`
		Canonical json.RawMessage    `json:"canonical"`
	}
	effectValues := make([]effectWire, len(effects))
	for index, effect := range effects {
		if _, err := journalv2.NewEffectID(string(effect.EffectID)); err != nil || index > 0 && effect.EffectID == effects[index-1].EffectID {
			return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "effect_intents", err)
		}
		canonical, err := journalv2.CanonicalObject(effect.Canonical)
		if err != nil || !bytes.Equal(canonical, effect.Canonical) {
			return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "effect_intents", err)
		}
		effectValues[index] = effectWire{effect.EffectID, canonical}
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion uint32              `json:"schema_version"`
		ProjectID     journalv2.ProjectID `json:"project_id"`
		MissionID     journalv2.MissionID `json:"mission_id"`
		EventType     MissionEventTypeV2  `json:"event_type"`
		Expected      json.RawMessage     `json:"expected,omitempty"`
		Successor     json.RawMessage     `json:"successor"`
		Effects       []effectWire        `json:"effects"`
		RelatedFact   json.RawMessage     `json:"related_fact,omitempty"`
	}{command.SchemaVersion, command.ProjectID, command.MissionID, command.EventType, command.Expected.CanonicalBytes(), command.Successor.CanonicalBytes(), effectValues, canonicalMissionFactIntentV2(command.RelatedFact)})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func buildMissionAuthorityPlanV2(command MissionAuthorityCommandV2, binding journalv2.OperationBindingV2, _ journalv2.Digest, observed MissionObservationV2, registry journalv2.EventRegistryV2) (journalv2.SealedCommitPlanV2, []byte, error) {
	resultBytes, err := encodeMissionAuthorityResultV2(command.OperationID, command.Successor)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, nil, err
	}
	plan, err := buildMissionAuthorityStagePlanV2(command, &binding, nil, 0, observed, journalv2.StatusResolved, "mission.authority.result", resultBytes, command.EffectIntents, registry)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, nil, err
	}
	return plan, resultBytes, nil
}

func buildMissionAuthorityStagePlanV2(command MissionAuthorityCommandV2, binding *journalv2.OperationBindingV2, priorOperation *journalv2.OperationStateV2, operationRecordRevision uint64, observed MissionObservationV2, status journalv2.OperationStatus, resultKind string, result []byte, newEffects []MissionEffectIntentV2, registry journalv2.EventRegistryV2) (journalv2.SealedCommitPlanV2, error) {
	missionPayload, err := NewMissionEventPayloadV2(command.EventType, command.Expected, command.Successor)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, err
	}
	missionBytes, _ := EncodeMissionEventPayloadV2(missionPayload)
	operationVersion := journalv2.LogicalVersion(1)
	var eventIDs []journalv2.EventID
	var effectIDs []journalv2.EffectID
	if priorOperation != nil {
		operationVersion = priorOperation.LogicalVersion() + 1
		eventIDs = priorOperation.EventIDs()
		effectIDs = priorOperation.EffectIDs()
	}
	operationEventID := deterministicAuthorityIDV2[journalv2.EventID]("operation-event", command.OperationID, operationVersion)
	missionEventID := deterministicAuthorityIDV2[journalv2.EventID]("mission-event", command.OperationID, operationVersion)
	eventIDs = append(eventIDs, operationEventID, missionEventID)
	var factEventID journalv2.EventID
	if command.RelatedFact != nil {
		factEventID = deterministicAuthorityIDV2[journalv2.EventID]("mission-fact-event", command.OperationID, operationVersion)
		eventIDs = append(eventIDs, factEventID)
	}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	effectRefs := make([]journalv2.EffectIntentReference, len(newEffects))
	for index, intent := range newEffects {
		canonical, canonicalErr := journalv2.CanonicalObject(intent.Canonical)
		if canonicalErr != nil || !bytes.Equal(canonical, intent.Canonical) {
			return journalv2.SealedCommitPlanV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "effect_intent", canonicalErr)
		}
		digest, digestErr := journalv2.ReducerStateDigestV2(canonical)
		if digestErr != nil {
			return journalv2.SealedCommitPlanV2{}, digestErr
		}
		effectRefs[index] = journalv2.EffectIntentReference{EffectID: intent.EffectID, Digest: digest, Canonical: canonical}
		effectIDs = append(effectIDs, intent.EffectID)
	}
	sort.Slice(effectRefs, func(i, j int) bool { return effectRefs[i].EffectID < effectRefs[j].EffectID })
	sort.Slice(effectIDs, func(i, j int) bool { return effectIDs[i] < effectIDs[j] })
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{
		OperationID: command.OperationID, LogicalVersion: operationVersion, Status: status,
		ResultKind: resultKind, ResultSchemaVersion: boolUint32V2(resultKind != ""), CanonicalResult: result,
		EventIDs: eventIDs, EffectIDs: effectIDs,
	})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "operation_state", err)
	}
	operationPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(command.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	missionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(command.MissionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: command.ProjectID, MissionID: command.MissionID})
	opDigest, _ := journalv2.ReducerStateDigestV2(operationPayload)
	drafts := []journalv2.EventDraftV2{
		{EventID: operationEventID, EventType: "operation_transitioned", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: operationVersion, CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, ActorID: command.ActorID, ProcessID: command.ProcessID, PolicyRevision: command.PolicyRevision, GovernanceRevisions: []string{command.DoctrineRevision, command.ProfileRevision}, Payload: operationPayload},
		{EventID: missionEventID, EventType: string(command.EventType), Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: command.Successor.Version(), CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, ActorID: command.ActorID, ProcessID: command.ProcessID, WorkRevisionID: command.Successor.CurrentWorkRevisionID(), PolicyRevision: command.PolicyRevision, GovernanceRevisions: []string{command.DoctrineRevision, command.ProfileRevision}, Observations: command.ObservationEvidence, Payload: missionBytes},
	}
	var factHead *journalv2.HeadTransitionV2
	if command.RelatedFact != nil {
		factBytes, factErr := encodeMissionFactEventV2(*command.RelatedFact)
		if factErr != nil {
			return journalv2.SealedCommitPlanV2{}, factErr
		}
		factCoordinate, coordinateErr := journalv2.NewAggregateCoordinate("mission", missionFactAggregateIDV2(command.RelatedFact.Successor), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: command.ProjectID, MissionID: command.MissionID})
		if coordinateErr != nil {
			return journalv2.SealedCommitPlanV2{}, coordinateErr
		}
		drafts = append(drafts, journalv2.EventDraftV2{EventID: factEventID, EventType: string(command.EventType), Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: factCoordinate, AggregateLogicalVersion: command.RelatedFact.Successor.LogicalVersion(), CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, ActorID: command.ActorID, ProcessID: command.ProcessID, WorkRevisionID: command.Successor.CurrentWorkRevisionID(), PolicyRevision: command.PolicyRevision, GovernanceRevisions: []string{command.DoctrineRevision, command.ProfileRevision}, Payload: factBytes})
		factHead = &journalv2.HeadTransitionV2{Coordinate: factCoordinate, ExpectedLogicalVersion: command.RelatedFact.Prior.LogicalVersion(), NextLogicalVersion: command.RelatedFact.Successor.LogicalVersion(), EventID: factEventID, ExpectedRecordRevision: command.RelatedFact.ExpectedRecordRevision, CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, CanonicalStateDigest: command.RelatedFact.Successor.Digest(), AuthoritySummary: command.RelatedFact.Successor.CanonicalBytes(), Tombstone: command.RelatedFact.Successor.Status() != MissionFactActiveV2 && command.RelatedFact.Successor.Status() != MissionFactOpenV2}
	}
	for index := range drafts {
		sort.Strings(drafts[index].GovernanceRevisions)
	}
	missionExpectedVersion, missionRecordRevision := journalv2.LogicalVersion(0), uint64(0)
	if command.EventType != MissionEventCreatedV2 {
		missionExpectedVersion, missionRecordRevision = command.Expected.Version(), observed.RecordRevision
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: operationCoordinate, ExpectedLogicalVersion: operationVersion - 1, NextLogicalVersion: operationVersion, EventID: operationEventID, ExpectedRecordRevision: operationRecordRevision, CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: []byte(`{}`)},
		{Coordinate: missionCoordinate, ExpectedLogicalVersion: missionExpectedVersion, NextLogicalVersion: command.Successor.Version(), EventID: missionEventID, ExpectedRecordRevision: missionRecordRevision, CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, CanonicalStateDigest: command.Successor.Digest(), AuthoritySummary: command.Successor.CanonicalBytes(), Tombstone: command.Successor.Status().terminal()},
	}
	if factHead != nil {
		heads = append(heads, *factHead)
	}
	conditions := make([]journalv2.ProviderCondition, len(heads))
	for index := range heads {
		conditions[index], err = journalv2.ConditionForHeadTransitionV2(heads[index])
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, err
		}
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: binding, PriorOperationState: priorOperation, OperationState: operation, Events: drafts, Heads: heads, EffectIntents: effectRefs, Conditions: conditions}, registry)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "commit_plan", err)
	}
	return plan, nil
}

func boolUint32V2(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func encodeMissionAuthorityResultV2(operationID journalv2.OperationID, state MissionAuthorityStateV2) ([]byte, error) {
	encoded, err := json.Marshal(missionAuthorityResultWireV2{MissionAuthoritySchemaVersionV2, operationID, state.CanonicalBytes()})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func decodeMissionAuthorityResultV2(encoded []byte, publicationID journalv2.PublicationID, replayed bool) (MissionAuthorityResultV2, error) {
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return MissionAuthorityResultV2{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire missionAuthorityResultWireV2
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != MissionAuthoritySchemaVersionV2 {
		return MissionAuthorityResultV2{}, err
	}
	state, err := DecodeMissionAuthorityStateV2(wire.State)
	if err != nil {
		return MissionAuthorityResultV2{}, err
	}
	reencoded, _ := encodeMissionAuthorityResultV2(wire.OperationID, state)
	if !bytes.Equal(reencoded, encoded) {
		return MissionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "operation_result", nil)
	}
	return MissionAuthorityResultV2{state: state, operationID: wire.OperationID, publicationID: publicationID, canonical: append([]byte(nil), encoded...), replayed: replayed}, nil
}

func validateMissionTransitionV2(event MissionEventTypeV2, prior, next MissionAuthorityStateV2) error {
	if event == MissionEventCreatedV2 {
		if !prior.IsZero() || next.Version() != 1 || next.Status() != AuthorityMissionActiveV2 || len(next.ActiveBlockerIDs()) != 0 || len(next.OpenDecisionIDs()) != 0 || next.Decomposition() != DecompositionNoneV2 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "mission_created", nil)
		}
		return nil
	}
	if !prior.IsZero() && next.Version() != prior.Version()+1 {
		return missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "mission_version", nil)
	}
	if prior.IsZero() || next.ProjectID() != prior.ProjectID() || next.MissionID() != prior.MissionID() || next.DefinitionRevision() != prior.DefinitionRevision() || next.DefinitionDigest() != prior.DefinitionDigest() || prior.Status().terminal() {
		kind := MissionAuthorityInvalidTransitionV2
		if !prior.IsZero() && prior.Status().terminal() {
			kind = MissionAuthorityTerminalV2
		}
		return missionAuthorityErrorV2(kind, "mission_transition", nil)
	}
	if next.ObservedAt() < prior.ObservedAt() {
		return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "observed_at", nil)
	}
	if event == MissionEventLifecycleAdvancedV2 || event == MissionEventCompletedV2 {
		if len(prior.ActiveBlockerIDs()) != 0 {
			return missionAuthorityErrorV2(MissionAuthorityActiveBlockerV2, "active_blockers", nil)
		}
		if len(prior.OpenDecisionIDs()) != 0 {
			return missionAuthorityErrorV2(MissionAuthorityOpenDecisionV2, "open_decisions", nil)
		}
	}
	sameBase := func() bool {
		return next.CurrentWorkRevisionID() == prior.CurrentWorkRevisionID() && next.ActionCount() == prior.ActionCount() &&
			equalStringsV2(next.ActiveBlockerIDs(), prior.ActiveBlockerIDs()) && equalStringsV2(next.OpenDecisionIDs(), prior.OpenDecisionIDs()) &&
			next.Decomposition() == prior.Decomposition() && equalEffectsV2(next.EffectIDs(), prior.EffectIDs())
	}
	switch event {
	case MissionEventLifecycleAdvancedV2:
		if prior.Status() != AuthorityMissionActiveV2 || next.Status() != AuthorityMissionActiveV2 || next.Step() == prior.Step() || !sameBase() {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "lifecycle", nil)
		}
	case MissionEventPausedV2:
		if prior.Status() != AuthorityMissionActiveV2 || next.Status() != AuthorityMissionPausedV2 || next.Step() != prior.Step() || !sameBase() {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "pause", nil)
		}
	case MissionEventResumedV2:
		if prior.Status() != AuthorityMissionPausedV2 || next.Status() != AuthorityMissionActiveV2 || next.Step() != prior.Step() || !sameBase() {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "resume", nil)
		}
	case MissionEventFailedV2, MissionEventCanceledV2, MissionEventCompletedV2:
		want := map[MissionEventTypeV2]AuthorityMissionStatusV2{MissionEventFailedV2: AuthorityMissionFailedV2, MissionEventCanceledV2: AuthorityMissionCanceledV2, MissionEventCompletedV2: AuthorityMissionCompletedV2}[event]
		if next.Status() != want || next.Step() != prior.Step() || !sameBase() {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "terminal", nil)
		}
	case MissionEventActionRecordedV2:
		if next.Status() != prior.Status() || next.Step() != prior.Step() || next.ActionCount() != prior.ActionCount()+1 || !sameExceptActionV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "action", nil)
		}
	case MissionEventBlockerRaisedV2:
		if !oneStringAddedV2(prior.ActiveBlockerIDs(), next.ActiveBlockerIDs()) || !sameExceptBlockersV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "blocker", nil)
		}
	case MissionEventBlockerClearedV2:
		if !oneStringAddedV2(next.ActiveBlockerIDs(), prior.ActiveBlockerIDs()) || !sameExceptBlockersV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "blocker", nil)
		}
	case MissionEventDecisionOpenedV2:
		if !oneStringAddedV2(prior.OpenDecisionIDs(), next.OpenDecisionIDs()) || !sameExceptDecisionsV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decision", nil)
		}
	case MissionEventDecisionResolvedV2, MissionEventDecisionDeferredV2, MissionEventDecisionCanceledV2:
		if !oneStringAddedV2(next.OpenDecisionIDs(), prior.OpenDecisionIDs()) || !sameExceptDecisionsV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decision", nil)
		}
	case MissionEventDecompositionRequestedV2:
		if prior.Decomposition() != DecompositionNoneV2 || next.Decomposition() != DecompositionRequestedV2 || !sameExceptDecompositionV2(prior, next) || len(next.EffectIDs()) == 0 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decomposition", nil)
		}
	case MissionEventDecompositionProgressedV2:
		if (prior.Decomposition() != DecompositionRequestedV2 && prior.Decomposition() != DecompositionProgressV2) || next.Decomposition() != DecompositionProgressV2 || !sameExceptDecompositionV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decomposition", nil)
		}
	case MissionEventDecompositionReconciliationV2:
		if next.Decomposition() != DecompositionReconciliationRequiredV2 || !sameExceptDecompositionV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decomposition", nil)
		}
	case MissionEventDecompositionCompletedV2:
		if prior.Decomposition() != DecompositionProgressV2 || next.Decomposition() != DecompositionCompletedV2 || !sameExceptDecompositionV2(prior, next) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decomposition", nil)
		}
	default:
		return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "event_type", nil)
	}
	return nil
}

func sameExceptActionV2(a, b MissionAuthorityStateV2) bool {
	return a.Status() == b.Status() && a.Step() == b.Step() && a.CurrentWorkRevisionID() == b.CurrentWorkRevisionID() && equalStringsV2(a.ActiveBlockerIDs(), b.ActiveBlockerIDs()) && equalStringsV2(a.OpenDecisionIDs(), b.OpenDecisionIDs()) && a.Decomposition() == b.Decomposition() && equalEffectsV2(a.EffectIDs(), b.EffectIDs())
}
func sameExceptBlockersV2(a, b MissionAuthorityStateV2) bool {
	return a.Status() == b.Status() && a.Step() == b.Step() && a.CurrentWorkRevisionID() == b.CurrentWorkRevisionID() && a.ActionCount() == b.ActionCount() && equalStringsV2(a.OpenDecisionIDs(), b.OpenDecisionIDs()) && a.Decomposition() == b.Decomposition() && equalEffectsV2(a.EffectIDs(), b.EffectIDs())
}
func sameExceptDecisionsV2(a, b MissionAuthorityStateV2) bool {
	return a.Status() == b.Status() && a.Step() == b.Step() && a.CurrentWorkRevisionID() == b.CurrentWorkRevisionID() && a.ActionCount() == b.ActionCount() && equalStringsV2(a.ActiveBlockerIDs(), b.ActiveBlockerIDs()) && a.Decomposition() == b.Decomposition() && equalEffectsV2(a.EffectIDs(), b.EffectIDs())
}
func sameExceptDecompositionV2(a, b MissionAuthorityStateV2) bool {
	return a.Status() == b.Status() && a.Step() == b.Step() && a.CurrentWorkRevisionID() == b.CurrentWorkRevisionID() && a.ActionCount() == b.ActionCount() && equalStringsV2(a.ActiveBlockerIDs(), b.ActiveBlockerIDs()) && equalStringsV2(a.OpenDecisionIDs(), b.OpenDecisionIDs())
}

func equalStringsV2(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func equalEffectsV2(a, b []journalv2.EffectID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func oneStringAddedV2(before, after []string) bool {
	if len(after) != len(before)+1 {
		return false
	}
	i := 0
	for _, v := range after {
		if i < len(before) && before[i] == v {
			i++
			continue
		}
	}
	return i == len(before)
}

func canonicalAuthorityStringsV2(values []string) ([]string, error) {
	if len(values) > MaxMissionAuthorityMembersV2 {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "members", nil)
	}
	copyValues := append([]string(nil), values...)
	if !sort.StringsAreSorted(copyValues) {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "members", nil)
	}
	for i, v := range copyValues {
		if !boundedAuthorityIdentityV2(v) || i > 0 && v == copyValues[i-1] {
			return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "members", nil)
		}
	}
	return copyValues, nil
}

func boundedAuthorityIdentityV2(value string) bool {
	if value == "" || len(value) > journalv2.MaxIdentityBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e || r == '/' {
			return false
		}
	}
	return true
}

func canonicalAuthorityTimeV2(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !parsed.IsZero() && parsed.Location() == time.UTC && parsed.Format(time.RFC3339Nano) == value
}

func deterministicAuthorityIDV2[T ~string](domain string, operationID journalv2.OperationID, version journalv2.LogicalVersion) T {
	hash := sha256.Sum256([]byte("spec-kitty/mission/" + domain + "/v2\x00" + string(operationID) + "\x00" + fmt.Sprintf("%d", version)))
	return T(domain + "-" + hex.EncodeToString(hash[:]))
}
