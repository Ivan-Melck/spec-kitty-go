package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const (
	effectStateSchemaV2  = uint16(1)
	effectResultSchemaV2 = uint32(1)
)

type EffectActionV2 string

const (
	EffectRecordIntentV2      EffectActionV2 = "record_intent"
	EffectStartAttemptV2      EffectActionV2 = "start_attempt"
	EffectRecordObservationV2 EffectActionV2 = "record_observation"
	EffectRequireReconcileV2  EffectActionV2 = "require_reconciliation"
	EffectAllowRetryV2        EffectActionV2 = "allow_retry"
	EffectAllowCompensationV2 EffectActionV2 = "allow_compensation"
	EffectResolveV2           EffectActionV2 = "resolve"
	EffectRefuseV2            EffectActionV2 = "refuse"
	EffectAdoptRecoveryV2     EffectActionV2 = "adopt_recovery"
)

type EffectStageV2 string
type EffectTerminalOutcomeV2 string

const (
	EffectIntentRecordedV2         EffectStageV2 = "intent_recorded"
	EffectAttemptStartedV2         EffectStageV2 = "attempt_started"
	EffectObservationRecordedV2    EffectStageV2 = "observation_recorded"
	EffectAmbiguousV2              EffectStageV2 = "ambiguous"
	EffectReconciliationRequiredV2 EffectStageV2 = "reconciliation_required"
	EffectRetryAllowedV2           EffectStageV2 = "retry_allowed"
	EffectCompensationAllowedV2    EffectStageV2 = "compensation_allowed"
	EffectResolvedV2               EffectStageV2 = "resolved"
	EffectRefusedV2                EffectStageV2 = "refused"
)

const (
	EffectOutcomeAppliedV2         EffectTerminalOutcomeV2 = "applied"
	EffectOutcomeCompensatedV2     EffectTerminalOutcomeV2 = "compensated"
	EffectOutcomeNoChangeV2        EffectTerminalOutcomeV2 = "no_change"
	EffectOutcomeRefusedV2         EffectTerminalOutcomeV2 = "refused"
	EffectOutcomeAttributionLostV2 EffectTerminalOutcomeV2 = "attribution_lost"
	EffectOutcomeUnsupportedV2     EffectTerminalOutcomeV2 = "unsupported"
)

type EffectIntentV2 struct {
	EffectID                 journalv2.EffectID       `json:"effect_id"`
	OperationID              journalv2.OperationID    `json:"operation_id"`
	Target                   tracker.ExternalWorkRef  `json:"target"`
	IntendedValue            string                   `json:"intended_value"`
	ExpectedProviderRevision tracker.Revision         `json:"expected_provider_revision"`
	ExpectedValue            string                   `json:"expected_value"`
	ExpectedOperationMarker  string                   `json:"expected_operation_marker"`
	RequiredGuarantee        tracker.AssignmentLevel  `json:"required_guarantee"`
	CanonicalRequestDigest   string                   `json:"canonical_request_digest"`
	ActorID                  ids.ActorID              `json:"actor_id"`
	ProcessID                ids.ProcessID            `json:"process_id"`
	Governance               ExecutionGovernanceV2    `json:"governance"`
	OriginRuntimeEpochID     journalv2.RuntimeEpochID `json:"origin_runtime_epoch_id"`
	OriginObservedCreatedAt  string                   `json:"origin_observed_created_at"`
}

type EffectProviderObservationV2 struct {
	Target           tracker.ExternalWorkRef `json:"target"`
	ProviderRevision tracker.Revision        `json:"provider_revision"`
	CanonicalDigest  string                  `json:"canonical_digest,omitempty"`
	Value            string                  `json:"value,omitempty"`
	OperationMarker  string                  `json:"operation_marker,omitempty"`
	Outcome          string                  `json:"outcome"`
}

type ExternalEffectStateV2 struct {
	SchemaVersion        uint16                          `json:"schema_version"`
	LogicalVersion       uint64                          `json:"logical_version"`
	Stage                EffectStageV2                   `json:"stage"`
	Intent               EffectIntentV2                  `json:"intent"`
	ExecutionSlot        journalv2.AggregateCoordinate   `json:"execution_slot"`
	Execution            ExecutionAuthorityCoordinatesV2 `json:"execution"`
	CurrentGovernance    ExecutionGovernanceV2           `json:"current_governance"`
	RuntimeEpochID       journalv2.RuntimeEpochID        `json:"runtime_epoch_id"`
	Observation          *EffectProviderObservationV2    `json:"observation,omitempty"`
	AppliedObservation   *EffectProviderObservationV2    `json:"applied_observation,omitempty"`
	PermissionMarker     string                          `json:"permission_marker,omitempty"`
	Outcome              EffectTerminalOutcomeV2         `json:"outcome,omitempty"`
	LastOperationEventID journalv2.EventID               `json:"last_operation_event_id"`
	LastEffectEventID    journalv2.EventID               `json:"last_effect_event_id"`
}

func (s ExternalEffectStateV2) Validate() error {
	if s.SchemaVersion != effectStateSchemaV2 || s.LogicalVersion == 0 || s.Intent.validate() != nil || s.ExecutionSlot.Kind != "execution_slot" || s.ExecutionSlot.Scope.Validate() != nil || s.Execution.Generation == 0 || s.Execution.Fence == "" || s.Execution.RuntimeEpochID != s.RuntimeEpochID || s.CurrentGovernance.validate() != nil || s.LastOperationEventID == "" || s.LastEffectEventID == "" || s.LastOperationEventID == s.LastEffectEventID {
		return executionV2Error(KindInvalidCommand, "effect_state", nil)
	}
	if s.LogicalVersion == 1 && (s.Intent.ActorID != s.Execution.ActorID || s.Intent.ProcessID != s.Execution.ProcessID || s.Intent.OriginRuntimeEpochID != s.RuntimeEpochID || !equalExecutionGovernanceV2(s.Intent.Governance, s.CurrentGovernance)) {
		return executionV2Error(KindInvalidCommand, "effect_origin", nil)
	}
	switch s.Stage {
	case EffectIntentRecordedV2, EffectAttemptStartedV2:
		if s.Observation != nil || s.AppliedObservation != nil || s.PermissionMarker != "" || s.Outcome != "" {
			return executionV2Error(KindInvalidCommand, "effect_stage", nil)
		}
	case EffectObservationRecordedV2, EffectAmbiguousV2, EffectReconciliationRequiredV2:
		if s.Observation == nil || s.Observation.validateFor(s.Intent.Target) != nil || s.PermissionMarker != "" || s.Outcome != "" {
			return executionV2Error(KindInvalidCommand, "effect_observation", nil)
		}
	case EffectRetryAllowedV2, EffectCompensationAllowedV2:
		if s.Observation == nil || s.Observation.validateFor(s.Intent.Target) != nil || s.PermissionMarker == "" || s.Outcome != "" {
			return executionV2Error(KindInvalidCommand, "effect_permission", nil)
		}
	case EffectResolvedV2, EffectRefusedV2:
		if !validEffectTerminalOutcomeV2(s.Stage, s.Outcome) {
			return executionV2Error(KindInvalidCommand, "effect_outcome", nil)
		}
	default:
		return executionV2Error(KindInvalidCommand, "effect_stage", nil)
	}
	if s.PermissionMarker != "" && s.PermissionMarker != string(s.Intent.OperationID) {
		return executionV2Error(KindInvalidCommand, "effect_permission", nil)
	}
	if s.AppliedObservation != nil {
		if s.AppliedObservation.validateFor(s.Intent.Target) != nil || s.AppliedObservation.Outcome != "confirmed" || s.AppliedObservation.Value != s.Intent.IntendedValue || s.AppliedObservation.OperationMarker != string(s.Intent.OperationID) {
			return executionV2Error(KindInvalidCommand, "effect_applied_observation", nil)
		}
	}
	return nil
}

func validEffectTerminalOutcomeV2(stage EffectStageV2, outcome EffectTerminalOutcomeV2) bool {
	if stage == EffectResolvedV2 {
		return outcome == EffectOutcomeAppliedV2 || outcome == EffectOutcomeCompensatedV2 || outcome == EffectOutcomeNoChangeV2
	}
	return outcome == EffectOutcomeRefusedV2 || outcome == EffectOutcomeAttributionLostV2 || outcome == EffectOutcomeUnsupportedV2
}

func (i EffectIntentV2) validate() error {
	if _, err := journalv2.NewEffectID(string(i.EffectID)); err != nil {
		return err
	}
	if _, err := journalv2.NewOperationID(string(i.OperationID)); err != nil {
		return err
	}
	if _, err := journalv2.NewRuntimeEpochID(string(i.OriginRuntimeEpochID)); err != nil {
		return err
	}
	if createdAt, err := time.Parse(time.RFC3339Nano, i.OriginObservedCreatedAt); err != nil || createdAt.IsZero() {
		return executionV2Error(KindInvalidCommand, "origin_observed_created_at", err)
	}
	if i.Target.Validate() != nil || len(i.IntendedValue) > journalv2.MaxIdentityBytes || i.ExpectedProviderRevision == 0 || len(i.ExpectedValue) > journalv2.MaxIdentityBytes || len(i.ExpectedOperationMarker) > journalv2.MaxIdentityBytes || (i.RequiredGuarantee != tracker.AssignmentAtomicClaim && i.RequiredGuarantee != tracker.AssignmentUnguarded) {
		return executionV2Error(KindInvalidCommand, "effect_intent", nil)
	}
	if len(i.CanonicalRequestDigest) != 64 {
		return executionV2Error(KindInvalidCommand, "request_digest", nil)
	}
	if _, err := hex.DecodeString(i.CanonicalRequestDigest); err != nil {
		return executionV2Error(KindInvalidCommand, "request_digest", err)
	}
	if expected, err := EffectRequestDigestV2(i); err != nil || expected != i.CanonicalRequestDigest {
		return executionV2Error(KindInvalidCommand, "request_digest", err)
	}
	if _, err := ids.ParseActorID(i.ActorID.String()); err != nil {
		return err
	}
	if _, err := ids.ParseProcessID(i.ProcessID.String()); err != nil {
		return err
	}
	return i.Governance.validate()
}

func (i EffectIntentV2) CanonicalBytesV2() []byte {
	if i.validate() != nil {
		return nil
	}
	encoded, err := json.Marshal(i)
	if err != nil {
		return nil
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil {
		return nil
	}
	return canonical
}

func EffectRequestDigestV2(intent EffectIntentV2) (string, error) {
	encoded, err := json.Marshal(struct {
		Target           tracker.ExternalWorkRef `json:"target"`
		IntendedValue    string                  `json:"intended_value"`
		ExpectedRevision tracker.Revision        `json:"expected_revision"`
		ExpectedValue    string                  `json:"expected_value"`
		ExpectedMarker   string                  `json:"expected_marker"`
		Guarantee        tracker.AssignmentLevel `json:"guarantee"`
	}{intent.Target, intent.IntendedValue, intent.ExpectedProviderRevision, intent.ExpectedValue, intent.ExpectedOperationMarker, intent.RequiredGuarantee})
	if err != nil {
		return "", err
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func (o EffectProviderObservationV2) validateFor(target tracker.ExternalWorkRef) error {
	if !sameEffectMappingV2(o.Target, target) || o.Target.Validate() != nil {
		return executionV2Error(KindInvalidCommand, "provider_observation", nil)
	}
	switch o.Outcome {
	case "confirmed", "stale", "conflicting":
		if o.ProviderRevision == 0 || o.Target.LastObservation != o.ProviderRevision || len(o.CanonicalDigest) != 64 || effectObservationDigestV2(o) != o.CanonicalDigest {
			return executionV2Error(KindInvalidCommand, "provider_observation", nil)
		}
	case "absent":
		if o.ProviderRevision == 0 || o.Target.LastObservation != o.ProviderRevision || o.Value != "" || o.OperationMarker != "" || len(o.CanonicalDigest) != 64 || effectObservationDigestV2(o) != o.CanonicalDigest {
			return executionV2Error(KindInvalidCommand, "provider_observation", nil)
		}
	case "unavailable", "malformed", "unknown":
		if o.ProviderRevision != 0 || o.CanonicalDigest != "" || o.Value != "" || o.OperationMarker != "" {
			return executionV2Error(KindInvalidCommand, "provider_observation", nil)
		}
	default:
		return executionV2Error(KindInvalidCommand, "provider_observation", nil)
	}
	return nil
}

func sameEffectMappingV2(a, b tracker.ExternalWorkRef) bool {
	return a.ProjectID == b.ProjectID && a.WorkID == b.WorkID && a.Provider == b.Provider && a.ExternalID == b.ExternalID && a.MetadataSchema == b.MetadataSchema && a.CreatedByOperation == b.CreatedByOperation
}

func effectObservationDigestV2(o EffectProviderObservationV2) string {
	encoded, _ := json.Marshal(struct {
		Revision tracker.Revision `json:"revision"`
		Value    string           `json:"value"`
		Marker   string           `json:"marker"`
	}{o.ProviderRevision, o.Value, o.OperationMarker})
	canonical, _ := journalv2.CanonicalObject(encoded)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func encodeExternalEffectStateV2(state ExternalEffectStateV2) ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func (s ExternalEffectStateV2) CanonicalResultBytesV2() ([]byte, error) {
	return encodeExternalEffectStateV2(s)
}

func DecodeExternalEffectStateV2(encoded []byte) (ExternalEffectStateV2, error) {
	var state ExternalEffectStateV2
	if err := strictExecutionJSONV2(encoded, &state); err != nil || state.Validate() != nil {
		return ExternalEffectStateV2{}, executionV2Error(KindInvalidCommand, "effect_state", err)
	}
	reencoded, _ := encodeExternalEffectStateV2(state)
	if !bytes.Equal(reencoded, encoded) {
		return ExternalEffectStateV2{}, executionV2Error(KindInvalidCommand, "effect_state", nil)
	}
	return state, nil
}

type EffectAuthorityRequestV2 struct {
	Action           EffectActionV2
	Intent           EffectIntentV2
	RuntimeEpochID   journalv2.RuntimeEpochID
	ExecutionSlot    ExecutionSlotStateV2
	ObservedAt       time.Time
	Observation      *EffectProviderObservationV2
	DispatchEvidence *EffectDispatchEvidenceV2
	PermissionMarker string
	Outcome          EffectTerminalOutcomeV2
	OperationEventID journalv2.EventID
	EffectEventID    journalv2.EventID
}

type EffectOperationResolutionV2 struct {
	Kind           ExecutionOperationResolutionKindV2
	State          journalv2.OperationStateV2
	RecordRevision uint64
}

type EffectStateObservationV2 struct {
	State             *ExternalEffectStateV2
	RecordRevision    uint64
	LastPublicationID journalv2.PublicationID
}

type EffectAuthorityPortV2 interface {
	ResolveEffectOperation(context.Context, journalv2.OperationBindingV2) (EffectOperationResolutionV2, error)
	ObserveEffectState(context.Context, journalv2.AggregateCoordinate) (EffectStateObservationV2, error)
	ObserveEffectExecution(context.Context, journalv2.AggregateCoordinate) (ExecutionSlotObservationV2, error)
	PublishEffect(context.Context, EffectAuthorityPlanV2) (EffectPublicationAckV2, error)
}

type EffectPublicationAckV2 struct {
	publicationID    journalv2.PublicationID
	planDigest       journalv2.Digest
	mutationCount    int
	providerRevision uint64
}

func NewEffectPublicationAckV2(plan EffectAuthorityPlanV2, ack journalv2.AcknowledgedPublicationV2) (EffectPublicationAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || ack.PublicationID() != sealed.PublicationID() || ack.PlanDigest() != sealed.Digest() || ack.MutationCount() != sealed.MutationCount() || ack.ProviderRevision() == 0 || ack.DurableThroughRevision() < ack.ProviderRevision() {
		return EffectPublicationAckV2{}, executionV2Error(KindAuthorityConflict, "effect_ack", err)
	}
	return EffectPublicationAckV2{ack.PublicationID(), ack.PlanDigest(), ack.MutationCount(), ack.ProviderRevision()}, nil
}
func (a EffectPublicationAckV2) matches(plan EffectAuthorityPlanV2) bool {
	sealed, err := plan.Publication()
	return err == nil && a.providerRevision > 0 && a.publicationID == sealed.PublicationID() && a.planDigest == sealed.Digest() && a.mutationCount == sealed.MutationCount()
}

type EffectDispatchPermissionV2 struct {
	effectID         journalv2.EffectID
	action           EffectActionV2
	marker           string
	expected         EffectProviderObservationV2
	publicationID    journalv2.PublicationID
	planDigest       journalv2.Digest
	providerRevision uint64
	recovered        bool
	guarantee        tracker.AssignmentLevel
	intendedValue    string
	restoreValue     string
}

func (p EffectDispatchPermissionV2) EffectID() journalv2.EffectID { return p.effectID }
func (p EffectDispatchPermissionV2) Action() EffectActionV2       { return p.action }

type EffectAuthorityResultV2 struct {
	State         ExternalEffectStateV2
	ExecutionSlot ExecutionSlotStateV2
	Permission    *EffectDispatchPermissionV2
	Replayed      bool
}

// EffectDispatchCommandV2 supplies the journal identities for both the
// authority-granting publication and the mandatory post-dispatch observation
// publication. The service, not its caller, carries the opaque permission and
// dispatch evidence between those two publications.
type EffectDispatchCommandV2 struct {
	Authorization               EffectAuthorityRequestV2
	ObservationOperationEventID journalv2.EventID
	ObservationEffectEventID    journalv2.EventID
	ObservationAt               time.Time
}

type EffectConditionalMutationV2 struct {
	EffectID          journalv2.EffectID
	Target            tracker.ExternalWorkRef
	ExpectedRevision  tracker.Revision
	ExpectedValue     string
	ExpectedMarker    string
	OperationMarker   string
	IntendedValue     string
	RestoreValue      string
	RequiredGuarantee tracker.AssignmentLevel
}

type EffectDispatchEvidenceV2 struct {
	observation      EffectProviderObservationV2
	publicationID    journalv2.PublicationID
	planDigest       journalv2.Digest
	providerRevision uint64
	recovered        bool
}

func (e EffectDispatchEvidenceV2) Observation() EffectProviderObservationV2 { return e.observation }

type EffectMutationOutcomeV2 string

const (
	EffectMutationDefinitiveV2    EffectMutationOutcomeV2 = "definitive"
	EffectMutationIndeterminateV2 EffectMutationOutcomeV2 = "indeterminate"
)

type EffectMutationResultV2 struct {
	Kind        EffectMutationOutcomeV2
	Observation EffectProviderObservationV2
}

type EffectDispatcherPortV2 interface {
	ObserveEffect(context.Context, tracker.ExternalWorkRef) (EffectProviderObservationV2, error)
	ApplyEffect(context.Context, EffectConditionalMutationV2) (EffectMutationResultV2, error)
	CompensateEffect(context.Context, EffectConditionalMutationV2) (EffectMutationResultV2, error)
}

// AssignmentEffectDispatcherV2 is the provider-neutral bridge from the effect
// saga to the tracker assignment contract. It never supplies authority facts;
// it only returns typed observations for the journaled reconciliation flow.
type AssignmentEffectDispatcherV2 struct {
	projection tracker.AssignmentProjection
}

func NewAssignmentEffectDispatcherV2(projection tracker.AssignmentProjection) (*AssignmentEffectDispatcherV2, error) {
	if projection == nil {
		return nil, executionV2Error(KindInvalidCommand, "assignment_projection", nil)
	}
	return &AssignmentEffectDispatcherV2{projection: projection}, nil
}

func (d *AssignmentEffectDispatcherV2) ObserveEffect(ctx context.Context, target tracker.ExternalWorkRef) (EffectProviderObservationV2, error) {
	if d == nil || d.projection == nil {
		return EffectProviderObservationV2{Target: target, Outcome: "unknown"}, executionV2Error(KindInvalidCommand, "assignment_projection", nil)
	}
	observation, err := d.projection.ObserveAssignment(ctx, target)
	if err != nil {
		return EffectProviderObservationV2{Target: target, Outcome: "unknown"}, err
	}
	return effectObservationFromAssignmentV2(target, observation)
}

func (d *AssignmentEffectDispatcherV2) ApplyEffect(ctx context.Context, mutation EffectConditionalMutationV2) (EffectMutationResultV2, error) {
	if d == nil || d.projection == nil {
		return EffectMutationResultV2{Kind: EffectMutationIndeterminateV2}, executionV2Error(KindInvalidCommand, "assignment_projection", nil)
	}
	result, err := d.projection.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: mutation.Target, OperationID: ids.OperationID(mutation.OperationMarker), Assignee: mutation.IntendedValue, ExpectedObservation: mutation.ExpectedRevision, RequiredGuarantee: mutation.RequiredGuarantee})
	return classifyAssignmentMutationV2(mutation.Target, mutation.RequiredGuarantee, result, err)
}

func (d *AssignmentEffectDispatcherV2) CompensateEffect(ctx context.Context, mutation EffectConditionalMutationV2) (EffectMutationResultV2, error) {
	if d == nil || d.projection == nil {
		return EffectMutationResultV2{Kind: EffectMutationIndeterminateV2}, executionV2Error(KindInvalidCommand, "assignment_projection", nil)
	}
	result, err := d.projection.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{ExternalRef: mutation.Target, OperationID: ids.OperationID(mutation.OperationMarker), ExpectedObservation: mutation.ExpectedRevision, ExpectedAssignee: mutation.ExpectedValue, ExpectedMarker: mutation.ExpectedMarker, RestoreAssignee: mutation.RestoreValue})
	return classifyAssignmentMutationV2(mutation.Target, mutation.RequiredGuarantee, result, err)
}

func classifyAssignmentMutationV2(target tracker.ExternalWorkRef, required tracker.AssignmentLevel, mutation tracker.AssignmentMutation, mutationErr error) (EffectMutationResultV2, error) {
	observation, observationErr := effectObservationFromAssignmentV2(target, mutation.Observation)
	if observationErr != nil {
		return EffectMutationResultV2{Kind: EffectMutationIndeterminateV2}, errors.Join(mutationErr, observationErr)
	}
	if mutationErr == nil && !assignmentGuaranteeSatisfiesV2(mutation.Guarantee, required) {
		return EffectMutationResultV2{Kind: EffectMutationIndeterminateV2, Observation: observation}, executionV2Error(KindDecisionRequired, "assignment_guarantee", nil)
	}
	kind := EffectMutationDefinitiveV2
	if mutationErr != nil && (tracker.IsKind(mutationErr, tracker.ErrorAmbiguous) || tracker.IsKind(mutationErr, tracker.ErrorTimeout) || tracker.IsKind(mutationErr, tracker.ErrorUnavailable) || tracker.IsKind(mutationErr, tracker.ErrorMalformedProtocol)) {
		kind = EffectMutationIndeterminateV2
	}
	return EffectMutationResultV2{Kind: kind, Observation: observation}, mutationErr
}

func assignmentGuaranteeSatisfiesV2(actual, required tracker.AssignmentLevel) bool {
	return actual == tracker.AssignmentAtomicClaim || actual == tracker.AssignmentUnguarded && required == tracker.AssignmentUnguarded
}

func effectObservationFromAssignmentV2(target tracker.ExternalWorkRef, observed tracker.AssignmentObservation) (EffectProviderObservationV2, error) {
	revision := observed.Observation.Revision
	if !sameEffectMappingV2(target, observed.ExternalRef) || observed.ExternalRef.LastObservation != revision || observed.Observation.ExternalID != target.ExternalID || revision == 0 || len(observed.Observation.CanonicalFields()) == 0 || observed.Assignee != observed.Observation.Assignee || observed.OperationMarker != observed.Observation.OperationMarker {
		return EffectProviderObservationV2{}, executionV2Error(KindInvalidCommand, "assignment_observation", nil)
	}
	observed.ExternalRef.LastObservation = revision
	outcome := "confirmed"
	if observed.Assignee == "" && observed.OperationMarker == "" {
		outcome = "absent"
	}
	result := EffectProviderObservationV2{Target: observed.ExternalRef, ProviderRevision: revision, Value: observed.Assignee, OperationMarker: observed.OperationMarker, Outcome: outcome}
	result.CanonicalDigest = effectObservationDigestV2(result)
	return result, nil
}

// DispatchAuthorizedEffectV2 is the only provider-mutation entry point. Its
// unforgeable permission is minted only after the corresponding journal
// publication was acknowledged.
func DispatchAuthorizedEffectV2(ctx context.Context, permission EffectDispatchPermissionV2, provider EffectDispatcherPortV2) (EffectDispatchEvidenceV2, error) {
	target := permission.expected.Target
	if provider == nil || (!permission.recovered && permission.planDigest.IsZero()) || permission.providerRevision == 0 || permission.effectID == "" || permission.marker == "" || permission.expected.validateFor(target) != nil {
		return EffectDispatchEvidenceV2{}, executionV2Error(KindInvalidCommand, "effect_permission", nil)
	}
	if _, err := journalv2.DecodePublicationID(string(permission.publicationID)); err != nil {
		return EffectDispatchEvidenceV2{}, executionV2Error(KindInvalidCommand, "effect_permission", nil)
	}
	fresh, observeErr := provider.ObserveEffect(ctx, target)
	if observeErr != nil || fresh.validateFor(target) != nil {
		return EffectDispatchEvidenceV2{observation: EffectProviderObservationV2{Target: target, Outcome: "unknown"}, publicationID: permission.publicationID, planDigest: permission.planDigest, providerRevision: permission.providerRevision, recovered: permission.recovered}, executionV2Error(KindDecisionRequired, "effect_fresh_observation", observeErr)
	}
	if !effectAttributionExactV2(permission.expected, fresh) {
		return EffectDispatchEvidenceV2{observation: fresh, publicationID: permission.publicationID, planDigest: permission.planDigest, providerRevision: permission.providerRevision, recovered: permission.recovered}, executionV2Error(KindDecisionRequired, "effect_fresh_observation", nil)
	}
	conditional := EffectConditionalMutationV2{EffectID: permission.effectID, Target: target, ExpectedRevision: fresh.ProviderRevision, ExpectedValue: fresh.Value, ExpectedMarker: fresh.OperationMarker, OperationMarker: permission.marker, IntendedValue: permission.intendedValue, RestoreValue: permission.restoreValue, RequiredGuarantee: permission.guarantee}
	var result EffectMutationResultV2
	var err error
	if permission.action == EffectAllowCompensationV2 {
		if permission.guarantee != tracker.AssignmentAtomicClaim {
			return EffectDispatchEvidenceV2{observation: fresh, publicationID: permission.publicationID, planDigest: permission.planDigest, providerRevision: permission.providerRevision, recovered: permission.recovered}, executionV2Error(KindSafeCompensationUnsupported, "effect_guarantee", nil)
		}
		result, err = provider.CompensateEffect(ctx, conditional)
	} else if permission.action == EffectStartAttemptV2 || permission.action == EffectAllowRetryV2 {
		result, err = provider.ApplyEffect(ctx, conditional)
	} else {
		return EffectDispatchEvidenceV2{}, executionV2Error(KindInvalidCommand, "effect_permission", nil)
	}
	unknown := EffectProviderObservationV2{Target: conditional.Target, Outcome: "unknown"}
	if result.Kind == EffectMutationIndeterminateV2 {
		return EffectDispatchEvidenceV2{observation: unknown, publicationID: permission.publicationID, planDigest: permission.planDigest, providerRevision: permission.providerRevision, recovered: permission.recovered}, executionV2Error(KindDecisionRequired, "effect_mutation_indeterminate", err)
	}
	if result.Kind != EffectMutationDefinitiveV2 || result.Observation.validateFor(conditional.Target) != nil {
		return EffectDispatchEvidenceV2{observation: unknown, publicationID: permission.publicationID, planDigest: permission.planDigest, providerRevision: permission.providerRevision, recovered: permission.recovered}, executionV2Error(KindDecisionRequired, "effect_mutation", err)
	}
	if err != nil || !effectMutationResultMatchesV2(permission.action, permission, result.Observation) {
		return EffectDispatchEvidenceV2{observation: result.Observation, publicationID: permission.publicationID, planDigest: permission.planDigest, providerRevision: permission.providerRevision, recovered: permission.recovered}, executionV2Error(KindDecisionRequired, "effect_mutation", err)
	}
	return EffectDispatchEvidenceV2{observation: result.Observation, publicationID: permission.publicationID, planDigest: permission.planDigest, providerRevision: permission.providerRevision, recovered: permission.recovered}, nil
}

type EffectAuthorityServiceV2 struct {
	port     EffectAuthorityPortV2
	epochs   ExecutionEpochSourceV2
	registry journalv2.EventRegistryV2
}

func NewEffectAuthorityServiceV2(port EffectAuthorityPortV2, epochs ExecutionEpochSourceV2, registry journalv2.EventRegistryV2) (*EffectAuthorityServiceV2, error) {
	if port == nil || epochs == nil {
		return nil, executionV2Error(KindInvalidCommand, "effect_service", nil)
	}
	if _, err := registry.Resolve("external_effect", 1, 1); err != nil {
		return nil, err
	}
	return &EffectAuthorityServiceV2{port, epochs, registry}, nil
}

func effectAuthorityRequestErrorV2(kind ErrorKind, subject string, request EffectAuthorityRequestV2, cause error) error {
	return authorityRequestErrorV2(kind, subject, ExecutionAuthorityRequestV2{
		OperationID:    request.Intent.OperationID,
		WorkRevisionID: request.ExecutionSlot.WorkRevisionID,
		WorkID:         request.ExecutionSlot.WorkID,
		RunID:          request.ExecutionSlot.RunID,
		LeaseID:        request.ExecutionSlot.LeaseID,
		ActorID:        request.ExecutionSlot.ActorID,
		ProcessID:      request.ExecutionSlot.ProcessID,
		RuntimeEpochID: request.RuntimeEpochID,
	}, cause)
}

func (s *EffectAuthorityServiceV2) Execute(ctx context.Context, request EffectAuthorityRequestV2) (EffectAuthorityResultV2, error) {
	binding, coordinate, err := prepareEffectBindingV2(request)
	if err != nil {
		return EffectAuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveEffectOperation(ctx, binding)
	if err != nil {
		return EffectAuthorityResultV2{}, effectAuthorityRequestErrorV2(KindControlStoreBusy, "operation_resolution", request, err)
	}
	if resolution.Kind == ExecutionOperationConflictV2 {
		return EffectAuthorityResultV2{}, executionV2Error(KindIdempotencyConflict, "operation_id", nil)
	}
	if resolution.Kind == ExecutionOperationExactV2 && (resolution.State.Status() == journalv2.StatusResolved || resolution.State.Status() == journalv2.StatusRefused) {
		if resolution.State.ResultKind() != "external_effect.result" || resolution.State.ResultSchemaVersion() != effectResultSchemaV2 {
			return EffectAuthorityResultV2{}, executionV2Error(KindAuthorityConflict, "operation_result", nil)
		}
		state, decodeErr := DecodeExternalEffectStateV2(resolution.State.Result())
		if decodeErr != nil {
			return EffectAuthorityResultV2{}, decodeErr
		}
		return EffectAuthorityResultV2{State: state, Replayed: true}, nil
	}
	if current, epochErr := s.epochs.CurrentRuntimeEpoch(ctx); epochErr != nil || current != request.RuntimeEpochID {
		return EffectAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "runtime_epoch", epochErr)
	}
	executionCoordinate, _ := ExecutionSlotCoordinateV2(request.ExecutionSlot.ProjectID, request.ExecutionSlot.MissionID, request.ExecutionSlot.WorkRevisionID, request.ExecutionSlot.WorkID)
	executionObserved, err := s.port.ObserveEffectExecution(ctx, executionCoordinate)
	if err != nil || executionObserved.State == nil {
		return EffectAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "execution_slot", err)
	}
	observed, err := s.port.ObserveEffectState(ctx, coordinate)
	if err != nil {
		return EffectAuthorityResultV2{}, executionV2Error(KindControlStoreBusy, "effect_state", err)
	}
	if resolution.Kind == ExecutionOperationAbsentV2 && observed.State != nil {
		return EffectAuthorityResultV2{}, executionV2Error(KindAuthorityConflict, "effect_state", nil)
	}
	if resolution.Kind == ExecutionOperationExactV2 && observed.State == nil {
		return EffectAuthorityResultV2{}, executionV2Error(KindAuthorityConflict, "effect_state", nil)
	}
	exactExecution := bytes.Equal(executionObserved.State.CanonicalBytes(), request.ExecutionSlot.CanonicalBytes())
	advancedExecution := bytes.Equal(executionObserved.State.CanonicalBytes(), guardExecutionForEffectV2(request.ExecutionSlot).CanonicalBytes())
	if executionObserved.State.Status != ExecutionActiveV2 || !request.ObservedAt.Before(mustExecutionTimeV2(executionObserved.State.Deadline)) {
		return EffectAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "execution_slot", nil)
	}
	if !exactExecution {
		if !advancedExecution || resolution.Kind != ExecutionOperationExactV2 || observed.State == nil || !effectRecoveryPublicationProofV2(resolution, observed, executionObserved) || !effectActionAlreadyPublishedV2(request, *observed.State, resolution.State) {
			return EffectAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "execution_slot", nil)
		}
		permission := recoveredEffectPermissionV2(request.Action, *observed.State, resolution.State.LastPublicationID(), observed.RecordRevision)
		return EffectAuthorityResultV2{State: *observed.State, ExecutionSlot: *executionObserved.State, Permission: permission, Replayed: true}, nil
	}
	next, operationStatus, permission, err := decideEffectTransitionV2(request, observed.State, resolution.State)
	if err != nil {
		return EffectAuthorityResultV2{}, err
	}
	plan, err := buildEffectAuthorityPlanV2(binding, resolution, coordinate, observed, executionObserved, next, operationStatus, request, s.registry)
	if err != nil {
		return EffectAuthorityResultV2{}, err
	}
	ack, publishErr := s.port.PublishEffect(ctx, plan)
	if publishErr != nil || !ack.matches(plan) {
		resolved, resolveErr := s.port.ResolveEffectOperation(ctx, binding)
		if resolveErr == nil && resolved.Kind == ExecutionOperationExactV2 && (resolved.State.Status() == journalv2.StatusResolved || resolved.State.Status() == journalv2.StatusRefused) {
			state, decodeErr := DecodeExternalEffectStateV2(resolved.State.Result())
			if decodeErr == nil {
				return EffectAuthorityResultV2{State: state, Replayed: true}, nil
			}
		}
		if resolveErr == nil && resolved.Kind == ExecutionOperationExactV2 {
			recoveredState, stateErr := s.port.ObserveEffectState(ctx, coordinate)
			recoveredExecution, executionErr := s.port.ObserveEffectExecution(ctx, executionCoordinate)
			if stateErr == nil && executionErr == nil && recoveredState.State != nil && recoveredExecution.State != nil && bytes.Equal(recoveredExecution.State.CanonicalBytes(), guardExecutionForEffectV2(request.ExecutionSlot).CanonicalBytes()) && effectRecoveryPublicationProofV2(resolved, recoveredState, recoveredExecution) && effectActionAlreadyPublishedV2(request, *recoveredState.State, resolved.State) {
				recoveredPermission := recoveredEffectPermissionV2(request.Action, *recoveredState.State, resolved.State.LastPublicationID(), recoveredState.RecordRevision)
				return EffectAuthorityResultV2{State: *recoveredState.State, ExecutionSlot: *recoveredExecution.State, Permission: recoveredPermission, Replayed: true}, nil
			}
		}
		return EffectAuthorityResultV2{}, executionV2Error(KindControlStoreBusy, "effect_publication", publishErr)
	}
	if permission != nil {
		permission.publicationID = ack.publicationID
		permission.planDigest = ack.planDigest
		permission.providerRevision = ack.providerRevision
	}
	return EffectAuthorityResultV2{State: next, ExecutionSlot: guardExecutionForEffectV2(request.ExecutionSlot), Permission: permission}, nil
}

// ExecuteDispatchV2 is the trusted effect coordinator. It durably publishes
// authority before calling the provider, then durably records every provider
// result (including response loss) before returning it to the caller.
func (s *EffectAuthorityServiceV2) ExecuteDispatchV2(ctx context.Context, command EffectDispatchCommandV2, provider EffectDispatcherPortV2) (EffectAuthorityResultV2, error) {
	if command.ObservationOperationEventID == "" || command.ObservationEffectEventID == "" || command.ObservationOperationEventID == command.ObservationEffectEventID || command.ObservationAt.IsZero() {
		return EffectAuthorityResultV2{}, executionV2Error(KindInvalidCommand, "effect_dispatch_command", nil)
	}
	authorized, err := s.Execute(ctx, command.Authorization)
	if err != nil {
		return EffectAuthorityResultV2{}, err
	}
	if authorized.Permission == nil {
		return EffectAuthorityResultV2{}, executionV2Error(KindDecisionRequired, "effect_dispatch_permission", nil)
	}
	evidence, dispatchErr := DispatchAuthorizedEffectV2(ctx, *authorized.Permission, provider)
	if evidence.publicationID == "" || evidence.providerRevision == 0 {
		return EffectAuthorityResultV2{}, effectAuthorityRequestErrorV2(KindDecisionRequired, "effect_dispatch", command.Authorization, dispatchErr)
	}
	observation := evidence.Observation()
	record := command.Authorization
	record.Action = EffectRecordObservationV2
	record.ExecutionSlot = authorized.ExecutionSlot
	record.ObservedAt = command.ObservationAt
	record.OperationEventID = command.ObservationOperationEventID
	record.EffectEventID = command.ObservationEffectEventID
	record.Observation = &observation
	record.DispatchEvidence = &evidence
	record.PermissionMarker = ""
	record.Outcome = ""
	recorded, recordErr := s.Execute(ctx, record)
	if recordErr != nil {
		return EffectAuthorityResultV2{}, effectAuthorityRequestErrorV2(KindDecisionRequired, "effect_outcome_publication", command.Authorization, recordErr)
	}
	if dispatchErr != nil {
		return recorded, effectAuthorityRequestErrorV2(KindDecisionRequired, "effect_dispatch", command.Authorization, dispatchErr)
	}
	return recorded, nil
}

func prepareEffectBindingV2(request EffectAuthorityRequestV2) (journalv2.OperationBindingV2, journalv2.AggregateCoordinate, error) {
	if request.Intent.validate() != nil || request.Intent.OperationID == "" || request.OperationEventID == "" || request.EffectEventID == "" || request.OperationEventID == request.EffectEventID || request.RuntimeEpochID == "" || request.ObservedAt.IsZero() {
		return journalv2.OperationBindingV2{}, journalv2.AggregateCoordinate{}, executionV2Error(KindInvalidCommand, "effect_request", nil)
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(request.ExecutionSlot.ProjectID), MissionID: journalv2.MissionID(request.ExecutionSlot.MissionID), WorkRevisionID: journalv2.WorkRevisionID(request.ExecutionSlot.WorkRevisionID), WorkID: journalv2.WorkID(request.ExecutionSlot.WorkID)}
	coordinate, err := journalv2.NewAggregateCoordinate("external_effect", journalv2.AggregateID(request.Intent.EffectID), scope)
	if err != nil {
		return journalv2.OperationBindingV2{}, journalv2.AggregateCoordinate{}, err
	}
	intentBytes, _ := json.Marshal(request.Intent)
	digest, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "external_effect.saga", CommandSchemaVersion: 1, Scope: scope, Command: intentBytes})
	if err != nil {
		return journalv2.OperationBindingV2{}, journalv2.AggregateCoordinate{}, err
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: request.Intent.OperationID, CommandKind: "external_effect.saga", CommandSchemaVersion: 1, CanonicalIntentDigest: digest, Scope: scope, ActorID: journalv2.ActorID(request.Intent.ActorID), ProcessID: journalv2.ProcessID(request.Intent.ProcessID), DoctrineRevision: request.Intent.Governance.DoctrineRevision, ProfileRevision: request.Intent.Governance.ProfileRevision, PolicyRevision: request.Intent.Governance.PolicyRevision, OriginatingRuntimeEpochID: request.Intent.OriginRuntimeEpochID, ObservedCreatedAt: request.Intent.OriginObservedCreatedAt})
	return binding, coordinate, err
}

func decideEffectTransitionV2(request EffectAuthorityRequestV2, prior *ExternalEffectStateV2, priorOperation journalv2.OperationStateV2) (ExternalEffectStateV2, journalv2.OperationStatus, *EffectDispatchPermissionV2, error) {
	next := ExternalEffectStateV2{SchemaVersion: 1, Intent: request.Intent, ExecutionSlot: mustExecutionSlotCoordinateV2(request.ExecutionSlot), Execution: executionCoordinatesV2(request.ExecutionSlot), CurrentGovernance: request.ExecutionSlot.Governance, RuntimeEpochID: request.RuntimeEpochID, LastOperationEventID: request.OperationEventID, LastEffectEventID: request.EffectEventID}
	var status journalv2.OperationStatus
	var permission *EffectDispatchPermissionV2
	if prior == nil {
		if request.Action != EffectRecordIntentV2 {
			return ExternalEffectStateV2{}, "", nil, executionV2Error(KindDecisionRequired, "effect_stage", nil)
		}
		next.LogicalVersion, next.Stage, status = 1, EffectIntentRecordedV2, journalv2.StatusAccepted
	} else {
		priorOperationStatus := priorOperation.Status()
		if prior.Validate() != nil || !effectIntentEqualV2(prior.Intent, request.Intent) {
			return ExternalEffectStateV2{}, "", nil, executionV2Error(KindStaleAuthority, "effect_authority", nil)
		}
		next = *prior
		next.LogicalVersion++
		next.LastOperationEventID = request.OperationEventID
		next.LastEffectEventID = request.EffectEventID
		if request.Action == EffectAdoptRecoveryV2 {
			if prior.Execution == executionCoordinatesV2(request.ExecutionSlot) {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindInvalidCommand, "recovery_adoption", nil)
			}
			next.ExecutionSlot = mustExecutionSlotCoordinateV2(request.ExecutionSlot)
			next.Execution = executionCoordinatesV2(request.ExecutionSlot)
			next.CurrentGovernance = request.ExecutionSlot.Governance
			next.RuntimeEpochID = request.RuntimeEpochID
			switch priorOperationStatus {
			case journalv2.StatusAccepted, journalv2.StatusInProgress:
				status = journalv2.StatusInProgress
			case journalv2.StatusAmbiguous:
				status = journalv2.StatusReconciliationRequired
				if prior.Stage == EffectObservationRecordedV2 || prior.Stage == EffectAmbiguousV2 {
					next.Stage = EffectReconciliationRequiredV2
				}
			case journalv2.StatusReconciliationRequired:
				status = journalv2.StatusAmbiguous
				if prior.Stage == EffectReconciliationRequiredV2 {
					next.Stage = EffectAmbiguousV2
				}
			default:
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindDecisionRequired, "recovery_adoption", nil)
			}
			if err := next.Validate(); err != nil {
				return ExternalEffectStateV2{}, "", nil, err
			}
			return next, status, nil, nil
		}
		if prior.RuntimeEpochID != request.RuntimeEpochID || prior.ExecutionSlot != mustExecutionSlotCoordinateV2(request.ExecutionSlot) || prior.Execution != executionCoordinatesV2(request.ExecutionSlot) || !equalExecutionGovernanceV2(prior.CurrentGovernance, request.ExecutionSlot.Governance) {
			return ExternalEffectStateV2{}, "", nil, executionV2Error(KindStaleAuthority, "effect_authority", nil)
		}
		next.Execution = executionCoordinatesV2(request.ExecutionSlot)
		switch request.Action {
		case EffectStartAttemptV2:
			if prior.Stage != EffectIntentRecordedV2 {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindDecisionRequired, "effect_stage", nil)
			}
			next.Stage, status = EffectAttemptStartedV2, journalv2.StatusInProgress
			target := request.Intent.Target
			target.LastObservation = request.Intent.ExpectedProviderRevision
			expected := EffectProviderObservationV2{Target: target, ProviderRevision: request.Intent.ExpectedProviderRevision, Value: request.Intent.ExpectedValue, OperationMarker: request.Intent.ExpectedOperationMarker, Outcome: effectOutcomeForValueMarkerV2(request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker)}
			expected.CanonicalDigest = effectObservationDigestV2(expected)
			permission = &EffectDispatchPermissionV2{effectID: request.Intent.EffectID, action: request.Action, marker: string(request.Intent.OperationID), expected: expected, guarantee: request.Intent.RequiredGuarantee, intendedValue: request.Intent.IntendedValue, restoreValue: request.Intent.ExpectedValue}
		case EffectRecordObservationV2:
			if (prior.Stage != EffectAttemptStartedV2 && prior.Stage != EffectRetryAllowedV2 && prior.Stage != EffectCompensationAllowedV2 && prior.Stage != EffectReconciliationRequiredV2) || request.Observation == nil || request.Observation.validateFor(request.Intent.Target) != nil {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindInvalidCommand, "effect_observation", nil)
			}
			if request.DispatchEvidence != nil && ((!request.DispatchEvidence.recovered && request.DispatchEvidence.planDigest.IsZero()) || request.DispatchEvidence.providerRevision == 0 || request.DispatchEvidence.publicationID != priorOperation.LastPublicationID() || !effectAttributionExactV2(request.DispatchEvidence.observation, *request.Observation)) {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindInvalidCommand, "dispatch_evidence", nil)
			}
			next.Observation = cloneEffectObservationV2(request.Observation)
			next.PermissionMarker = ""
			if request.DispatchEvidence != nil && request.Observation.Outcome == "confirmed" && request.Observation.Value == request.Intent.IntendedValue && request.Observation.OperationMarker == string(request.Intent.OperationID) {
				next.AppliedObservation = cloneEffectObservationV2(request.Observation)
			}
			if prior.Stage == EffectAttemptStartedV2 || prior.Stage == EffectReconciliationRequiredV2 || ((prior.Stage == EffectRetryAllowedV2 || prior.Stage == EffectCompensationAllowedV2) && priorOperationStatus == journalv2.StatusReconciliationRequired) {
				next.Stage, status = EffectObservationRecordedV2, journalv2.StatusAmbiguous
			} else {
				next.Stage, status = EffectReconciliationRequiredV2, journalv2.StatusReconciliationRequired
			}
		case EffectRequireReconcileV2:
			if prior.Stage != EffectObservationRecordedV2 && prior.Stage != EffectAmbiguousV2 {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindDecisionRequired, "effect_stage", nil)
			}
			next.Stage, status = EffectReconciliationRequiredV2, journalv2.StatusReconciliationRequired
		case EffectAllowRetryV2, EffectAllowCompensationV2:
			if prior.Stage != EffectReconciliationRequiredV2 || request.Observation == nil || request.Observation.validateFor(request.Intent.Target) != nil || !effectAttributionExactV2(*prior.Observation, *request.Observation) || !effectActionAttributableV2(request.Action, request.Intent, prior) {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindExternalAttributionLost, "effect_attribution", nil)
			}
			next.Observation = cloneEffectObservationV2(request.Observation)
			next.PermissionMarker = request.PermissionMarker
			if next.PermissionMarker != string(request.Intent.OperationID) {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindInvalidCommand, "permission_marker", nil)
			}
			if request.Action == EffectAllowRetryV2 {
				next.Stage = EffectRetryAllowedV2
			} else {
				next.Stage = EffectCompensationAllowedV2
			}
			status = journalv2.StatusAmbiguous
			permission = &EffectDispatchPermissionV2{effectID: request.Intent.EffectID, action: request.Action, marker: request.PermissionMarker, expected: *cloneEffectObservationV2(request.Observation), guarantee: request.Intent.RequiredGuarantee, intendedValue: request.Intent.IntendedValue, restoreValue: request.Intent.ExpectedValue}
		case EffectResolveV2, EffectRefuseV2:
			if prior.Stage != EffectReconciliationRequiredV2 {
				return ExternalEffectStateV2{}, "", nil, executionV2Error(KindDecisionRequired, "effect_stage", nil)
			}
			next.Outcome = request.Outcome
			if request.Action == EffectResolveV2 {
				next.Stage, status = EffectResolvedV2, journalv2.StatusResolved
			} else {
				next.Stage, status = EffectRefusedV2, journalv2.StatusRefused
			}
		default:
			return ExternalEffectStateV2{}, "", nil, executionV2Error(KindInvalidCommand, "effect_action", nil)
		}
	}
	if err := next.Validate(); err != nil {
		return ExternalEffectStateV2{}, "", nil, err
	}
	return next, status, permission, nil
}

func buildEffectAuthorityPlanV2(binding journalv2.OperationBindingV2, resolution EffectOperationResolutionV2, coordinate journalv2.AggregateCoordinate, observed EffectStateObservationV2, executionObserved ExecutionSlotObservationV2, next ExternalEffectStateV2, status journalv2.OperationStatus, request EffectAuthorityRequestV2, registry journalv2.EventRegistryV2) (EffectAuthorityPlanV2, error) {
	stateBytes, err := encodeExternalEffectStateV2(next)
	if err != nil {
		return EffectAuthorityPlanV2{}, err
	}
	var priorOperation *journalv2.OperationStateV2
	var priorEvents []journalv2.EventID
	var priorEffects []journalv2.EffectID
	var opVersion journalv2.LogicalVersion
	if resolution.Kind == ExecutionOperationExactV2 {
		copyState := resolution.State
		priorOperation = &copyState
		priorEvents = copyState.EventIDs()
		priorEffects = copyState.EffectIDs()
		opVersion = copyState.LogicalVersion()
	}
	executionEventID, err := journalv2.NewEventID(string(request.EffectEventID) + "-execution")
	if err != nil {
		return EffectAuthorityPlanV2{}, err
	}
	events := append(priorEvents, request.OperationEventID, request.EffectEventID, executionEventID)
	sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })
	effects := append(priorEffects, request.Intent.EffectID)
	sort.Slice(effects, func(i, j int) bool { return effects[i] < effects[j] })
	effects = uniqueEffectIDsV2(effects)
	input := journalv2.OperationStateInput{OperationID: request.Intent.OperationID, LogicalVersion: opVersion + 1, Status: status, EventIDs: events, EffectIDs: effects}
	if status == journalv2.StatusResolved || status == journalv2.StatusRefused {
		input.ResultKind = "external_effect.result"
		input.ResultSchemaVersion = effectResultSchemaV2
		input.CanonicalResult = stateBytes
	}
	operation, err := journalv2.NewOperationStateV2(input)
	if err != nil {
		return EffectAuthorityPlanV2{}, err
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(request.Intent.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	governance := request.ExecutionSlot.Governance.journalRevisions()
	guardedExecution := guardExecutionForEffectV2(request.ExecutionSlot)
	guardedExecutionBytes, err := encodeExecutionStateV2(guardedExecution)
	if err != nil {
		return EffectAuthorityPlanV2{}, err
	}
	predecessor := executionCoordinatesV2(request.ExecutionSlot)
	guardEventBytes, err := encodeExecutionEventV2(ExecutionSlotEventV2{SchemaVersion: 1, Kind: "effect_authorized", ObservedAt: request.ObservedAt.UTC().Format(time.RFC3339Nano), Predecessor: &predecessor, State: guardedExecution})
	if err != nil {
		return EffectAuthorityPlanV2{}, err
	}
	eventDrafts := []journalv2.EventDraftV2{
		{EventID: request.OperationEventID, EventType: "operation." + string(status), Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: opVersion + 1, CommittedRuntimeEpochID: request.RuntimeEpochID, ActorID: journalv2.ActorID(request.ExecutionSlot.ActorID), ProcessID: journalv2.ProcessID(request.ExecutionSlot.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(request.ExecutionSlot.WorkRevisionID), PolicyRevision: request.ExecutionSlot.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload},
		{EventID: request.EffectEventID, EventType: "external_effect." + string(request.Action), Family: "external_effect", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), CommittedRuntimeEpochID: request.RuntimeEpochID, ActorID: journalv2.ActorID(request.ExecutionSlot.ActorID), ProcessID: journalv2.ProcessID(request.ExecutionSlot.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(request.ExecutionSlot.WorkRevisionID), PolicyRevision: request.ExecutionSlot.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: stateBytes},
		{EventID: executionEventID, EventType: "execution_slot.effect_authorized", Family: "execution_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: next.ExecutionSlot, AggregateLogicalVersion: journalv2.LogicalVersion(guardedExecution.LogicalVersion), CommittedRuntimeEpochID: request.RuntimeEpochID, ActorID: journalv2.ActorID(request.ExecutionSlot.ActorID), ProcessID: journalv2.ProcessID(request.ExecutionSlot.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(request.ExecutionSlot.WorkRevisionID), PolicyRevision: request.ExecutionSlot.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: guardEventBytes},
	}
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	stateDigest, _ := journalv2.ReducerStateDigestV2(stateBytes)
	executionDigest, _ := journalv2.ReducerStateDigestV2(guardedExecutionBytes)
	heads := []journalv2.HeadTransitionV2{{Coordinate: opCoordinate, ExpectedLogicalVersion: opVersion, NextLogicalVersion: opVersion + 1, EventID: request.OperationEventID, ExpectedRecordRevision: resolution.RecordRevision, CommittedRuntimeEpochID: request.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opPayload}, {Coordinate: coordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion - 1), NextLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), EventID: request.EffectEventID, ExpectedRecordRevision: observed.RecordRevision, CommittedRuntimeEpochID: request.RuntimeEpochID, CanonicalStateDigest: stateDigest, AuthoritySummary: stateBytes}, {Coordinate: next.ExecutionSlot, ExpectedLogicalVersion: journalv2.LogicalVersion(request.ExecutionSlot.LogicalVersion), NextLogicalVersion: journalv2.LogicalVersion(guardedExecution.LogicalVersion), EventID: executionEventID, ExpectedRecordRevision: executionObserved.RecordRevision, CommittedRuntimeEpochID: request.RuntimeEpochID, CanonicalStateDigest: executionDigest, AuthoritySummary: guardedExecutionBytes}}
	conditions := make([]journalv2.ProviderCondition, 0, 3)
	for _, head := range heads {
		condition, e := journalv2.ConditionForHeadTransitionV2(head)
		if e != nil {
			return EffectAuthorityPlanV2{}, e
		}
		conditions = append(conditions, condition)
	}
	planInput := journalv2.CommitPlanInput{PriorOperationState: priorOperation, OperationState: operation, Events: eventDrafts, Heads: heads, Conditions: conditions}
	if resolution.Kind == ExecutionOperationAbsentV2 {
		planInput.OperationBinding = &binding
	}
	if next.LogicalVersion == 1 {
		intentBytes, _ := json.Marshal(request.Intent)
		canonical, _ := journalv2.CanonicalObject(intentBytes)
		sum := sha256.Sum256(canonical)
		digest, _ := journalv2.ParseDigest("effect_intent", hex.EncodeToString(sum[:]))
		planInput.EffectIntents = []journalv2.EffectIntentReference{{EffectID: request.Intent.EffectID, Digest: digest, Canonical: canonical}}
	}
	sealed, err := journalv2.SealCommitPlanV2(planInput, registry)
	if err != nil {
		return EffectAuthorityPlanV2{}, err
	}
	return EffectAuthorityPlanV2{sealed}, nil
}

type EffectAuthorityPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p EffectAuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, executionV2Error(KindInvalidCommand, "effect_plan", nil)
	}
	return p.sealed, nil
}

// ValidatePublicationV2 is the capability-owned semantic membership check.
// Persistence adapters may enforce transport shape, but never interpret saga
// stages, operation status, or execution authority policy themselves.
func (p EffectAuthorityPlanV2) ValidatePublicationV2() error {
	plan, err := p.Publication()
	if err != nil || len(plan.Events()) != 3 || len(plan.Heads()) != 3 || len(plan.Conditions()) != 3 || len(plan.EligibilityGuards()) != 0 || len(plan.Checkpoints()) != 0 {
		return executionV2Error(KindInvalidCommand, "effect_plan_membership", err)
	}
	var operationEvent, effectEvent, executionEvent journalv2.EventEnvelopeV2
	for _, event := range plan.Events() {
		switch event.Family() {
		case "operation":
			if operationEvent.EventID() != "" {
				return executionV2Error(KindInvalidCommand, "effect_plan_membership", nil)
			}
			operationEvent = event
		case "external_effect":
			if effectEvent.EventID() != "" {
				return executionV2Error(KindInvalidCommand, "effect_plan_membership", nil)
			}
			effectEvent = event
		case "execution_slot":
			if executionEvent.EventID() != "" {
				return executionV2Error(KindInvalidCommand, "effect_plan_membership", nil)
			}
			executionEvent = event
		default:
			return executionV2Error(KindInvalidCommand, "effect_plan_membership", nil)
		}
	}
	state, err := DecodeExternalEffectStateV2(effectEvent.Payload())
	if err != nil || operationEvent.EventID() != state.LastOperationEventID || effectEvent.EventID() != state.LastEffectEventID || plan.OperationState().OperationID() != state.Intent.OperationID || operationEvent.EventType() != "operation."+string(plan.OperationState().Status()) || !effectActionStateValidV2(EffectActionV2(strings.TrimPrefix(effectEvent.EventType(), "external_effect.")), state.Stage, plan.OperationState().Status()) {
		return executionV2Error(KindInvalidCommand, "effect_plan_semantics", err)
	}
	executionPayload, err := decodeExecutionEventV2(executionEvent.Payload())
	if err != nil || executionPayload.Kind != "effect_authorized" || executionEvent.EventType() != "execution_slot.effect_authorized" || executionPayload.State.RunID != state.Execution.RunID || executionPayload.State.ActorID != state.Execution.ActorID || executionPayload.State.ProcessID != state.Execution.ProcessID || executionPayload.State.LeaseID != state.Execution.LeaseID || executionPayload.State.Generation != state.Execution.Generation || executionPayload.State.Fence != state.Execution.Fence || executionPayload.State.RuntimeEpochID != state.RuntimeEpochID {
		return executionV2Error(KindInvalidCommand, "effect_execution_membership", err)
	}
	for _, event := range plan.Events() {
		matched := false
		for _, head := range plan.Heads() {
			matched = matched || head.Coordinate == event.Coordinate() && head.EventID == event.EventID() && head.NextLogicalVersion == event.LogicalVersion()
		}
		if !matched {
			return executionV2Error(KindInvalidCommand, "effect_head_membership", nil)
		}
	}
	if binding := plan.BindingCreate(); binding != nil {
		intents := plan.EffectIntents()
		if state.LogicalVersion != 1 || binding.OperationID() != state.Intent.OperationID || len(intents) != 1 || intents[0].EffectID != state.Intent.EffectID || !bytes.Equal(intents[0].Canonical, state.Intent.CanonicalBytesV2()) {
			return executionV2Error(KindInvalidCommand, "effect_intent_membership", nil)
		}
	} else if state.LogicalVersion == 1 || len(plan.EffectIntents()) != 0 {
		return executionV2Error(KindInvalidCommand, "effect_intent_membership", nil)
	}
	return nil
}

func effectActionStateValidV2(action EffectActionV2, stage EffectStageV2, status journalv2.OperationStatus) bool {
	switch action {
	case EffectRecordIntentV2:
		return stage == EffectIntentRecordedV2 && status == journalv2.StatusAccepted
	case EffectStartAttemptV2:
		return stage == EffectAttemptStartedV2 && status == journalv2.StatusInProgress
	case EffectRecordObservationV2:
		return stage == EffectObservationRecordedV2 && status == journalv2.StatusAmbiguous || stage == EffectReconciliationRequiredV2 && status == journalv2.StatusReconciliationRequired
	case EffectRequireReconcileV2:
		return stage == EffectReconciliationRequiredV2 && status == journalv2.StatusReconciliationRequired
	case EffectAllowRetryV2:
		return stage == EffectRetryAllowedV2 && status == journalv2.StatusAmbiguous
	case EffectAllowCompensationV2:
		return stage == EffectCompensationAllowedV2 && status == journalv2.StatusAmbiguous
	case EffectResolveV2:
		return stage == EffectResolvedV2 && status == journalv2.StatusResolved
	case EffectRefuseV2:
		return stage == EffectRefusedV2 && status == journalv2.StatusRefused
	case EffectAdoptRecoveryV2:
		return status == journalv2.StatusInProgress || status == journalv2.StatusAmbiguous || status == journalv2.StatusReconciliationRequired
	default:
		return false
	}
}

func ReduceExternalEffectEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	if event.Family() != "external_effect" || event.EventSchemaVersion() != 1 || event.ReducerVersion() != 1 || event.Coordinate().Kind != "external_effect" {
		return nil, executionV2Error(KindInvalidCommand, "effect_event", nil)
	}
	next, err := DecodeExternalEffectStateV2(event.Payload())
	if err != nil || next.LogicalVersion != uint64(event.LogicalVersion()) || event.CommittedRuntimeEpochID() != next.RuntimeEpochID || event.ActorID() != journalv2.ActorID(next.Execution.ActorID) || event.ProcessID() != journalv2.ProcessID(next.Execution.ProcessID) {
		return nil, executionV2Error(KindAuthorityConflict, "effect_event_authority", err)
	}
	if event.Coordinate().ID != journalv2.AggregateID(next.Intent.EffectID) || event.Coordinate().Scope != next.ExecutionSlot.Scope || event.WorkRevisionID() != event.Coordinate().Scope.WorkRevisionID || event.PolicyRevision() != next.CurrentGovernance.PolicyRevision || !stringSlicesEqualV2(event.GovernanceRevisions(), next.CurrentGovernance.journalRevisions()) {
		return nil, executionV2Error(KindAuthorityConflict, "effect_event_authority", nil)
	}
	if event.OperationID() != next.Intent.OperationID {
		return nil, executionV2Error(KindAuthorityConflict, "effect_operation", nil)
	}
	if event.EventID() != next.LastEffectEventID {
		return nil, executionV2Error(KindAuthorityConflict, "effect_event_identity", nil)
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.LogicalVersion != 1 || next.Stage != EffectIntentRecordedV2 || event.EventType() != "external_effect.record_intent" {
			return nil, executionV2Error(KindAuthorityConflict, "effect_initial", nil)
		}
	} else {
		previous, e := DecodeExternalEffectStateV2(prior)
		adopted := event.EventType() == "external_effect.adopt_recovery"
		if e != nil || next.LogicalVersion != previous.LogicalVersion+1 || !effectIntentEqualV2(previous.Intent, next.Intent) || (!adopted && previous.RuntimeEpochID != next.RuntimeEpochID) {
			return nil, executionV2Error(KindAuthorityConflict, "effect_transition", e)
		}
		if (!adopted && !allowedEffectTransitionV2(previous.Stage, next.Stage)) || (adopted && !allowedEffectAdoptionV2(previous.Stage, next.Stage)) {
			return nil, executionV2Error(KindAuthorityConflict, "effect_transition", nil)
		}
		appliedAddedByDispatch := previous.AppliedObservation == nil && (previous.Stage == EffectAttemptStartedV2 || previous.Stage == EffectRetryAllowedV2) && (next.Stage == EffectObservationRecordedV2 || next.Stage == EffectReconciliationRequiredV2)
		if (!adopted && (previous.ExecutionSlot != next.ExecutionSlot || previous.Execution != next.Execution || !equalExecutionGovernanceV2(previous.CurrentGovernance, next.CurrentGovernance))) || (!effectObservationEqualV2(previous.AppliedObservation, next.AppliedObservation) && !appliedAddedByDispatch) {
			return nil, executionV2Error(KindAuthorityConflict, "effect_immutable_evidence", nil)
		}
		observationChanged := !effectObservationEqualV2(previous.Observation, next.Observation)
		if observationChanged && next.Stage != EffectObservationRecordedV2 && !(next.Stage == EffectReconciliationRequiredV2 && (previous.Stage == EffectRetryAllowedV2 || previous.Stage == EffectCompensationAllowedV2)) {
			return nil, executionV2Error(KindAuthorityConflict, "effect_observation_delta", nil)
		}
		if previous.PermissionMarker != next.PermissionMarker && next.Stage != EffectRetryAllowedV2 && next.Stage != EffectCompensationAllowedV2 && next.Stage != EffectObservationRecordedV2 && next.Stage != EffectReconciliationRequiredV2 {
			return nil, executionV2Error(KindAuthorityConflict, "effect_permission_delta", nil)
		}
		if previous.Outcome != next.Outcome && next.Stage != EffectResolvedV2 && next.Stage != EffectRefusedV2 {
			return nil, executionV2Error(KindAuthorityConflict, "effect_outcome_delta", nil)
		}
		if (next.Stage == EffectRetryAllowedV2 && !effectActionAttributableV2(EffectAllowRetryV2, next.Intent, &previous)) || (next.Stage == EffectCompensationAllowedV2 && !effectActionAttributableV2(EffectAllowCompensationV2, next.Intent, &previous)) {
			return nil, executionV2Error(KindExternalAttributionLost, "effect_attribution", nil)
		}
		if previous.AppliedObservation == nil && next.AppliedObservation != nil && (!appliedAddedByDispatch || next.Observation == nil || !effectAttributionExactV2(*next.AppliedObservation, *next.Observation)) {
			return nil, executionV2Error(KindAuthorityConflict, "effect_applied_observation", nil)
		}
		expectedAction := effectActionForTransitionV2(previous.Stage, next.Stage)
		if adopted {
			expectedAction = EffectAdoptRecoveryV2
		}
		if event.EventType() != "external_effect."+string(expectedAction) {
			return nil, executionV2Error(KindAuthorityConflict, "effect_event_type", nil)
		}
	}
	return encodeExternalEffectStateV2(next)
}

func EffectReducerRegistrationsV2() []journalv2.ReducerRegistration {
	sum := sha256.Sum256([]byte("spec-kitty/external-effect-reducer/1.0"))
	digest, _ := journalv2.ParseDigest("effect_reducer", hex.EncodeToString(sum[:]))
	return []journalv2.ReducerRegistration{{Family: "external_effect", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/execution", ImplementationDigest: digest, Reduce: ReduceExternalEffectEventV2}}
}

func allowedEffectTransitionV2(from, to EffectStageV2) bool {
	switch from {
	case EffectIntentRecordedV2:
		return to == EffectAttemptStartedV2
	case EffectAttemptStartedV2:
		return to == EffectObservationRecordedV2
	case EffectObservationRecordedV2, EffectAmbiguousV2:
		return to == EffectReconciliationRequiredV2
	case EffectReconciliationRequiredV2:
		return to == EffectObservationRecordedV2 || to == EffectRetryAllowedV2 || to == EffectCompensationAllowedV2 || to == EffectResolvedV2 || to == EffectRefusedV2
	case EffectRetryAllowedV2, EffectCompensationAllowedV2:
		return to == EffectObservationRecordedV2 || to == EffectReconciliationRequiredV2
	default:
		return false
	}
}

func allowedEffectAdoptionV2(from, to EffectStageV2) bool {
	return from == to || (from == EffectObservationRecordedV2 || from == EffectAmbiguousV2) && to == EffectReconciliationRequiredV2 || from == EffectReconciliationRequiredV2 && to == EffectAmbiguousV2
}

func effectActionForTransitionV2(from, to EffectStageV2) EffectActionV2 {
	switch {
	case from == to:
		return EffectAdoptRecoveryV2
	case from == EffectIntentRecordedV2 && to == EffectAttemptStartedV2:
		return EffectStartAttemptV2
	case from == EffectAttemptStartedV2 && to == EffectObservationRecordedV2:
		return EffectRecordObservationV2
	case from == EffectObservationRecordedV2 && to == EffectReconciliationRequiredV2:
		return EffectRequireReconcileV2
	case from == EffectReconciliationRequiredV2 && to == EffectObservationRecordedV2:
		return EffectRecordObservationV2
	case from == EffectReconciliationRequiredV2 && to == EffectRetryAllowedV2:
		return EffectAllowRetryV2
	case from == EffectReconciliationRequiredV2 && to == EffectCompensationAllowedV2:
		return EffectAllowCompensationV2
	case from == EffectReconciliationRequiredV2 && to == EffectResolvedV2:
		return EffectResolveV2
	case from == EffectReconciliationRequiredV2 && to == EffectRefusedV2:
		return EffectRefuseV2
	case (from == EffectRetryAllowedV2 || from == EffectCompensationAllowedV2) && (to == EffectObservationRecordedV2 || to == EffectReconciliationRequiredV2):
		return EffectRecordObservationV2
	default:
		return ""
	}
}
func effectAttributionExactV2(a, b EffectProviderObservationV2) bool {
	return sameEffectMappingV2(a.Target, b.Target) && a.ProviderRevision == b.ProviderRevision && a.Value == b.Value && a.OperationMarker == b.OperationMarker && a.Outcome == b.Outcome
}

func effectMutationResultMatchesV2(action EffectActionV2, permission EffectDispatchPermissionV2, observation EffectProviderObservationV2) bool {
	if action == EffectAllowCompensationV2 {
		return observation.Value == permission.restoreValue && observation.OperationMarker == "" && observation.Outcome == effectOutcomeForValueMarkerV2(permission.restoreValue, "")
	}
	return observation.Outcome == "confirmed" && observation.OperationMarker == permission.marker && observation.Value == permission.intendedValue
}

func effectOutcomeForValueMarkerV2(value, marker string) string {
	if value == "" && marker == "" {
		return "absent"
	}
	return "confirmed"
}

func effectActionAlreadyPublishedV2(request EffectAuthorityRequestV2, state ExternalEffectStateV2, operation journalv2.OperationStateV2) bool {
	if state.Validate() != nil || !effectIntentEqualV2(state.Intent, request.Intent) || operation.OperationID() != request.Intent.OperationID || operation.LastPublicationID() == "" {
		return false
	}
	wantStage, wantStatus := EffectStageV2(""), journalv2.OperationStatus("")
	switch request.Action {
	case EffectRecordIntentV2:
		wantStage, wantStatus = EffectIntentRecordedV2, journalv2.StatusAccepted
	case EffectStartAttemptV2:
		wantStage, wantStatus = EffectAttemptStartedV2, journalv2.StatusInProgress
	case EffectRecordObservationV2:
		if state.Stage == EffectObservationRecordedV2 && operation.Status() == journalv2.StatusAmbiguous || state.Stage == EffectReconciliationRequiredV2 && operation.Status() == journalv2.StatusReconciliationRequired {
			wantStage, wantStatus = state.Stage, operation.Status()
		} else {
			return false
		}
	case EffectRequireReconcileV2:
		wantStage, wantStatus = EffectReconciliationRequiredV2, journalv2.StatusReconciliationRequired
	case EffectAllowRetryV2:
		wantStage, wantStatus = EffectRetryAllowedV2, journalv2.StatusAmbiguous
	case EffectAllowCompensationV2:
		wantStage, wantStatus = EffectCompensationAllowedV2, journalv2.StatusAmbiguous
	case EffectResolveV2:
		wantStage, wantStatus = EffectResolvedV2, journalv2.StatusResolved
	case EffectRefuseV2:
		wantStage, wantStatus = EffectRefusedV2, journalv2.StatusRefused
	case EffectAdoptRecoveryV2:
		wantStage = state.Stage
	default:
		return false
	}
	if state.Stage != wantStage || (wantStatus != "" && operation.Status() != wantStatus) {
		return false
	}
	return state.LastOperationEventID == request.OperationEventID && state.LastEffectEventID == request.EffectEventID
}

func effectRecoveryPublicationProofV2(operation EffectOperationResolutionV2, effect EffectStateObservationV2, execution ExecutionSlotObservationV2) bool {
	publicationID := operation.State.LastPublicationID()
	return publicationID != "" && effect.LastPublicationID == publicationID && execution.LastPublicationID == publicationID && operation.RecordRevision > 0 && operation.RecordRevision == effect.RecordRevision && effect.RecordRevision == execution.RecordRevision
}

func recoveredEffectPermissionV2(action EffectActionV2, state ExternalEffectStateV2, publicationID journalv2.PublicationID, recordRevision uint64) *EffectDispatchPermissionV2 {
	permission := &EffectDispatchPermissionV2{effectID: state.Intent.EffectID, action: action, marker: state.PermissionMarker, publicationID: publicationID, providerRevision: recordRevision, recovered: true, guarantee: state.Intent.RequiredGuarantee, intendedValue: state.Intent.IntendedValue, restoreValue: state.Intent.ExpectedValue}
	switch action {
	case EffectStartAttemptV2:
		target := state.Intent.Target
		target.LastObservation = state.Intent.ExpectedProviderRevision
		permission.marker = string(state.Intent.OperationID)
		permission.expected = EffectProviderObservationV2{Target: target, ProviderRevision: state.Intent.ExpectedProviderRevision, Value: state.Intent.ExpectedValue, OperationMarker: state.Intent.ExpectedOperationMarker, Outcome: effectOutcomeForValueMarkerV2(state.Intent.ExpectedValue, state.Intent.ExpectedOperationMarker)}
		permission.expected.CanonicalDigest = effectObservationDigestV2(permission.expected)
	case EffectAllowRetryV2, EffectAllowCompensationV2:
		if state.Observation == nil {
			return nil
		}
		permission.expected = *cloneEffectObservationV2(state.Observation)
	default:
		return nil
	}
	if permission.marker == "" || publicationID == "" || recordRevision == 0 {
		return nil
	}
	return permission
}
func effectActionAttributableV2(action EffectActionV2, intent EffectIntentV2, state *ExternalEffectStateV2) bool {
	if state == nil || state.Observation == nil {
		return false
	}
	observed := *state.Observation
	switch action {
	case EffectAllowCompensationV2:
		return state.AppliedObservation != nil && effectAttributionExactV2(*state.AppliedObservation, observed) && observed.Outcome == "confirmed" && observed.Value == intent.IntendedValue && observed.OperationMarker == string(intent.OperationID)
	case EffectAllowRetryV2:
		return observed.Outcome == "absent" || (intent.RequiredGuarantee == tracker.AssignmentAtomicClaim && observed.OperationMarker == string(intent.OperationID))
	default:
		return false
	}
}
func cloneEffectObservationV2(v *EffectProviderObservationV2) *EffectProviderObservationV2 {
	if v == nil {
		return nil
	}
	copyValue := *v
	return &copyValue
}
func effectObservationEqualV2(a, b *EffectProviderObservationV2) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return effectAttributionExactV2(*a, *b) && a.CanonicalDigest == b.CanonicalDigest
}
func effectIntentEqualV2(a, b EffectIntentV2) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func uniqueEffectIDsV2(values []journalv2.EffectID) []journalv2.EffectID {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, v := range values[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func mustExecutionSlotCoordinateV2(state ExecutionSlotStateV2) journalv2.AggregateCoordinate {
	coordinate, _ := ExecutionSlotCoordinateV2(state.ProjectID, state.MissionID, state.WorkRevisionID, state.WorkID)
	return coordinate
}
func guardExecutionForEffectV2(state ExecutionSlotStateV2) ExecutionSlotStateV2 {
	state.LogicalVersion++
	return state
}
