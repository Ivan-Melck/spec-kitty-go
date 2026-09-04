package execution

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

const (
	ExecutionEventSchemaV2  = uint32(1)
	ExecutionReducerV2      = uint32(1)
	executionStateSchemaV2  = uint16(1)
	executionResultSchemaV2 = uint32(1)
)

type ExecutionCommandKindV2 string

const (
	ExecutionAdmitV2     ExecutionCommandKindV2 = "admit"
	ExecutionHeartbeatV2 ExecutionCommandKindV2 = "heartbeat"
	ExecutionExpireV2    ExecutionCommandKindV2 = "expire"
	ExecutionReplaceV2   ExecutionCommandKindV2 = "replace"
	ExecutionOutcomeV2   ExecutionCommandKindV2 = "outcome"
)

type ExecutionSlotStatusV2 string

const (
	ExecutionActiveV2    ExecutionSlotStatusV2 = "active"
	ExecutionExpiredV2   ExecutionSlotStatusV2 = "expired"
	ExecutionCompletedV2 ExecutionSlotStatusV2 = "completed"
	ExecutionFailedV2    ExecutionSlotStatusV2 = "failed"
	ExecutionCanceledV2  ExecutionSlotStatusV2 = "canceled"
)

type ExecutionGovernanceV2 struct {
	ContextID           ids.GovernanceContextID   `json:"context_id"`
	AggregateAlgorithm  string                    `json:"aggregate_algorithm"`
	AggregateDigest     string                    `json:"aggregate_digest"`
	DoctrineRevision    string                    `json:"doctrine_revision"`
	ProfileRevision     string                    `json:"profile_revision"`
	MissionTypeRevision string                    `json:"mission_type_revision"`
	PolicyRevision      string                    `json:"policy_revision"`
	SourceCommitments   [4]string                 `json:"source_commitments"`
	Context             governancecontext.Context `json:"context"`
}

func ExecutionGovernanceFromContextV2(value governancecontext.Context) (ExecutionGovernanceV2, error) {
	if err := value.Validate(); err != nil {
		return ExecutionGovernanceV2{}, executionV2Error(KindInvalidCommand, "governance_context", err)
	}
	result := ExecutionGovernanceV2{ContextID: value.ID(), AggregateAlgorithm: value.Aggregate().Algorithm(), AggregateDigest: value.Aggregate().Digest(), Context: value}
	seen := map[governancecontext.EntryKind]bool{}
	for _, entry := range value.Entries() {
		if seen[entry.Kind()] {
			return ExecutionGovernanceV2{}, executionV2Error(KindInvalidCommand, "governance_entries", nil)
		}
		seen[entry.Kind()] = true
		switch entry.Kind() {
		case governancecontext.KindDoctrine:
			result.DoctrineRevision = entry.Revision()
		case governancecontext.KindProfile:
			result.ProfileRevision = entry.Revision()
		case governancecontext.KindMissionType:
			result.MissionTypeRevision = entry.Revision()
		case governancecontext.KindConfig:
			result.PolicyRevision = entry.Revision()
		default:
			return ExecutionGovernanceV2{}, executionV2Error(KindInvalidCommand, "governance_entries", nil)
		}
		for index := range result.SourceCommitments {
			if result.SourceCommitments[index] == "" {
				result.SourceCommitments[index] = string(entry.Kind()) + ":" + entry.Ref() + ":" + entry.Revision() + ":" + entry.Commitment().Digest()
				break
			}
		}
	}
	sort.Strings(result.SourceCommitments[:])
	if !seen[governancecontext.KindDoctrine] || !seen[governancecontext.KindProfile] || !seen[governancecontext.KindMissionType] || !seen[governancecontext.KindConfig] {
		return ExecutionGovernanceV2{}, executionV2Error(KindInvalidCommand, "governance_entries", nil)
	}
	if err := result.validate(); err != nil {
		return ExecutionGovernanceV2{}, err
	}
	return result, nil
}

func (g ExecutionGovernanceV2) validate() error {
	if err := g.Context.Validate(); err != nil || g.Context.ID() != g.ContextID || g.Context.Aggregate().Algorithm() != g.AggregateAlgorithm || g.Context.Aggregate().Digest() != g.AggregateDigest {
		return executionV2Error(KindInvalidCommand, "governance_context", err)
	}
	if _, err := ids.ParseGovernanceContextID(g.ContextID.String()); err != nil {
		return executionV2Error(KindInvalidCommand, "governance_commitment", err)
	}
	if _, err := governancecontext.NewCommitment(g.AggregateAlgorithm, g.AggregateDigest); err != nil {
		return executionV2Error(KindInvalidCommand, "governance_commitment", err)
	}
	for _, value := range []string{g.DoctrineRevision, g.ProfileRevision, g.MissionTypeRevision, g.PolicyRevision} {
		if _, err := journalv2.NewAggregateID(value); err != nil {
			return executionV2Error(KindInvalidCommand, "governance_revision", err)
		}
	}
	if !sort.StringsAreSorted(g.SourceCommitments[:]) {
		return executionV2Error(KindInvalidCommand, "governance_sources", nil)
	}
	for i, value := range g.SourceCommitments {
		if len(value) == 0 || len(value) > 512 || i > 0 && value == g.SourceCommitments[i-1] {
			return executionV2Error(KindInvalidCommand, "governance_sources", nil)
		}
	}
	restored, err := ExecutionGovernanceFromContextEntriesV2(g.Context)
	if err != nil || restored.DoctrineRevision != g.DoctrineRevision || restored.ProfileRevision != g.ProfileRevision || restored.MissionTypeRevision != g.MissionTypeRevision || restored.PolicyRevision != g.PolicyRevision || restored.SourceCommitments != g.SourceCommitments {
		return executionV2Error(KindInvalidCommand, "governance_projection", err)
	}
	return nil
}

func ExecutionGovernanceFromContextEntriesV2(value governancecontext.Context) (ExecutionGovernanceV2, error) {
	result := ExecutionGovernanceV2{ContextID: value.ID(), AggregateAlgorithm: value.Aggregate().Algorithm(), AggregateDigest: value.Aggregate().Digest(), Context: value}
	seen := map[governancecontext.EntryKind]bool{}
	for _, entry := range value.Entries() {
		if seen[entry.Kind()] {
			return ExecutionGovernanceV2{}, executionV2Error(KindInvalidCommand, "governance_entries", nil)
		}
		seen[entry.Kind()] = true
		switch entry.Kind() {
		case governancecontext.KindDoctrine:
			result.DoctrineRevision = entry.Revision()
		case governancecontext.KindProfile:
			result.ProfileRevision = entry.Revision()
		case governancecontext.KindMissionType:
			result.MissionTypeRevision = entry.Revision()
		case governancecontext.KindConfig:
			result.PolicyRevision = entry.Revision()
		default:
			return ExecutionGovernanceV2{}, executionV2Error(KindInvalidCommand, "governance_entries", nil)
		}
		for i := range result.SourceCommitments {
			if result.SourceCommitments[i] == "" {
				result.SourceCommitments[i] = string(entry.Kind()) + ":" + entry.Ref() + ":" + entry.Revision() + ":" + entry.Commitment().Digest()
				break
			}
		}
	}
	sort.Strings(result.SourceCommitments[:])
	if len(seen) != 4 {
		return ExecutionGovernanceV2{}, executionV2Error(KindInvalidCommand, "governance_entries", nil)
	}
	return result, nil
}

func (g ExecutionGovernanceV2) journalRevisions() []string {
	values := []string{"config." + g.PolicyRevision, "doctrine." + g.DoctrineRevision, "mission_type." + g.MissionTypeRevision, "profile." + g.ProfileRevision}
	sort.Strings(values)
	return values
}

type ExecutionSlotStateV2 struct {
	SchemaVersion  uint16                   `json:"schema_version"`
	ProjectID      ids.ProjectID            `json:"project_id"`
	MissionID      ids.MissionID            `json:"mission_id"`
	WorkRevisionID ids.WorkRevisionID       `json:"work_revision_id"`
	WorkID         ids.WorkID               `json:"work_id"`
	LogicalVersion uint64                   `json:"logical_version"`
	RunID          ids.RunID                `json:"run_id"`
	ActorID        ids.ActorID              `json:"actor_id"`
	ProcessID      ids.ProcessID            `json:"process_id"`
	LeaseID        ids.LeaseID              `json:"lease_id"`
	Generation     uint64                   `json:"generation"`
	Fence          string                   `json:"fence"`
	HeartbeatAt    string                   `json:"heartbeat_at"`
	Deadline       string                   `json:"deadline"`
	RuntimeEpochID journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	Status         ExecutionSlotStatusV2    `json:"status"`
	Outcome        string                   `json:"outcome,omitempty"`
	Governance     ExecutionGovernanceV2    `json:"governance"`
}

func (s ExecutionSlotStateV2) Validate() error {
	if s.SchemaVersion != executionStateSchemaV2 || s.LogicalVersion == 0 || s.Generation == 0 || len(s.Fence) < 16 || len(s.Fence) > journalv2.MaxIdentityBytes {
		return executionV2Error(KindInvalidCommand, "execution_state", nil)
	}
	for _, parse := range []func() error{
		func() error { _, err := ids.ParseProjectID(s.ProjectID.String()); return err },
		func() error { _, err := ids.ParseMissionID(s.MissionID.String()); return err },
		func() error { _, err := ids.ParseWorkRevisionID(s.WorkRevisionID.String()); return err },
		func() error { _, err := ids.ParseWorkID(s.WorkID.String()); return err },
		func() error { _, err := ids.ParseRunID(s.RunID.String()); return err },
		func() error { _, err := ids.ParseActorID(s.ActorID.String()); return err },
		func() error { _, err := ids.ParseProcessID(s.ProcessID.String()); return err },
		func() error { _, err := ids.ParseLeaseID(s.LeaseID.String()); return err },
		func() error { _, err := journalv2.NewRuntimeEpochID(string(s.RuntimeEpochID)); return err },
	} {
		if err := parse(); err != nil {
			return executionV2Error(KindInvalidCommand, "execution_identity", err)
		}
	}
	heartbeat, err := time.Parse(time.RFC3339Nano, s.HeartbeatAt)
	if err != nil || heartbeat.Location() != time.UTC {
		return executionV2Error(KindInvalidCommand, "heartbeat_at", err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, s.Deadline)
	if err != nil || deadline.Location() != time.UTC || !deadline.After(heartbeat) {
		return executionV2Error(KindInvalidCommand, "deadline", err)
	}
	switch s.Status {
	case ExecutionActiveV2, ExecutionExpiredV2:
		if s.Outcome != "" {
			return executionV2Error(KindInvalidCommand, "outcome", nil)
		}
	case ExecutionCompletedV2, ExecutionFailedV2, ExecutionCanceledV2:
		if s.Outcome == "" || len(s.Outcome) > 128 {
			return executionV2Error(KindInvalidCommand, "outcome", nil)
		}
	default:
		return executionV2Error(KindInvalidCommand, "execution_status", nil)
	}
	return s.Governance.validate()
}

func (s ExecutionSlotStateV2) CanonicalBytes() []byte {
	encoded, err := encodeExecutionStateV2(s)
	if err != nil {
		return nil
	}
	return encoded
}

func encodeExecutionStateV2(s ExecutionSlotStateV2) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeExecutionSlotStateV2(encoded []byte) (ExecutionSlotStateV2, error) {
	var state ExecutionSlotStateV2
	if err := strictExecutionJSONV2(encoded, &state); err != nil || state.Validate() != nil {
		return ExecutionSlotStateV2{}, executionV2Error(KindInvalidCommand, "execution_state", err)
	}
	reencoded, _ := encodeExecutionStateV2(state)
	if !bytes.Equal(reencoded, encoded) {
		return ExecutionSlotStateV2{}, executionV2Error(KindInvalidCommand, "execution_state", nil)
	}
	return state, nil
}

func ExecutionSlotCoordinateV2(projectID ids.ProjectID, missionID ids.MissionID, revisionID ids.WorkRevisionID, workID ids.WorkID) (journalv2.AggregateCoordinate, error) {
	preimage := revisionID.String() + "\x00" + workID.String()
	sum := sha256.Sum256([]byte(preimage))
	return journalv2.NewAggregateCoordinate("execution_slot", journalv2.AggregateID("slot-"+hex.EncodeToString(sum[:])), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(projectID), MissionID: journalv2.MissionID(missionID), WorkRevisionID: journalv2.WorkRevisionID(revisionID), WorkID: journalv2.WorkID(workID)})
}

type ExecutionSlotObservationV2 struct {
	State             *ExecutionSlotStateV2
	RecordRevision    uint64
	LastPublicationID journalv2.PublicationID
}

type ExecutionAuthorityCoordinatesV2 struct {
	RunID          ids.RunID                `json:"run_id"`
	ActorID        ids.ActorID              `json:"actor_id"`
	ProcessID      ids.ProcessID            `json:"process_id"`
	LeaseID        ids.LeaseID              `json:"lease_id"`
	Generation     uint64                   `json:"generation"`
	Fence          string                   `json:"fence"`
	HeartbeatAt    string                   `json:"heartbeat_at"`
	Deadline       string                   `json:"deadline"`
	RuntimeEpochID journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
}

type ExecutionSlotEventV2 struct {
	SchemaVersion uint16                           `json:"schema_version"`
	Kind          string                           `json:"kind"`
	ObservedAt    string                           `json:"observed_at"`
	Predecessor   *ExecutionAuthorityCoordinatesV2 `json:"predecessor,omitempty"`
	State         ExecutionSlotStateV2             `json:"state"`
}

func executionCoordinatesV2(state ExecutionSlotStateV2) ExecutionAuthorityCoordinatesV2 {
	return ExecutionAuthorityCoordinatesV2{state.RunID, state.ActorID, state.ProcessID, state.LeaseID, state.Generation, state.Fence, state.HeartbeatAt, state.Deadline, state.RuntimeEpochID}
}

func encodeExecutionEventV2(value ExecutionSlotEventV2) ([]byte, error) {
	observedAt, observedErr := time.Parse(time.RFC3339Nano, value.ObservedAt)
	if value.SchemaVersion != 1 || value.State.Validate() != nil || observedErr != nil || observedAt.Location() != time.UTC {
		return nil, executionV2Error(KindInvalidCommand, "execution_event", nil)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func decodeExecutionEventV2(encoded []byte) (ExecutionSlotEventV2, error) {
	var value ExecutionSlotEventV2
	if err := strictExecutionJSONV2(encoded, &value); err != nil {
		return ExecutionSlotEventV2{}, executionV2Error(KindInvalidCommand, "execution_event", err)
	}
	reencoded, err := encodeExecutionEventV2(value)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		return ExecutionSlotEventV2{}, executionV2Error(KindInvalidCommand, "execution_event", err)
	}
	return value, nil
}

// DecodeExecutionSlotEventV2 exposes the capability-owned semantic event to
// narrow persistence adapters without exposing its wire model.
func DecodeExecutionSlotEventV2(encoded []byte) (ExecutionSlotEventV2, error) {
	return decodeExecutionEventV2(encoded)
}

type ExecutionOperationResolutionKindV2 string

const (
	ExecutionOperationAbsentV2   ExecutionOperationResolutionKindV2 = "absent"
	ExecutionOperationExactV2    ExecutionOperationResolutionKindV2 = "exact"
	ExecutionOperationConflictV2 ExecutionOperationResolutionKindV2 = "conflict"
)

type ExecutionOperationResolutionV2 struct {
	Kind           ExecutionOperationResolutionKindV2
	State          journalv2.OperationStateV2
	RecordRevision uint64
}

type ExecutionAuthorityPortV2 interface {
	ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (ExecutionOperationResolutionV2, error)
	ObserveExecutionEligibility(context.Context, ids.MissionID, journalv2.EligibilityShardID) (workgraph.ObservedEligibilityShardV2, error)
	ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (ExecutionSlotObservationV2, error)
	PublishExecution(context.Context, ExecutionAuthorityPlanV2) (ExecutionPublicationAckV2, error)
}

type ExecutionPublicationAckV2 struct {
	publicationID    journalv2.PublicationID
	planDigest       journalv2.Digest
	mutationCount    int
	providerRevision uint64
}

func NewExecutionPublicationAckV2(plan ExecutionAuthorityPlanV2, ack journalv2.AcknowledgedPublicationV2) (ExecutionPublicationAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || ack.PublicationID() != sealed.PublicationID() || ack.PlanDigest() != sealed.Digest() || ack.MutationCount() != sealed.MutationCount() || ack.ProviderRevision() == 0 || ack.DurableThroughRevision() < ack.ProviderRevision() {
		return ExecutionPublicationAckV2{}, executionV2Error(KindAuthorityConflict, "execution_ack", err)
	}
	return ExecutionPublicationAckV2{ack.PublicationID(), ack.PlanDigest(), ack.MutationCount(), ack.ProviderRevision()}, nil
}
func (a ExecutionPublicationAckV2) matches(plan ExecutionAuthorityPlanV2) bool {
	sealed, err := plan.Publication()
	return err == nil && a.providerRevision > 0 && a.publicationID == sealed.PublicationID() && a.planDigest == sealed.Digest() && a.mutationCount == sealed.MutationCount()
}

type ExecutionEpochSourceV2 interface {
	CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error)
}

// ExecutionFenceSourceV2 is an authority-owned entropy seam. A caller may
// present a fence for an existing lease, but it cannot choose the fence for a
// new grant or replacement.
type ExecutionFenceSourceV2 interface {
	NewExecutionFence(context.Context) (string, error)
}

type cryptographicExecutionFenceSourceV2 struct{}

func (cryptographicExecutionFenceSourceV2) NewExecutionFence(context.Context) (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

type ExecutionAuthorityRequestV2 struct {
	Kind             ExecutionCommandKindV2
	OperationID      journalv2.OperationID
	ProjectID        ids.ProjectID
	MissionID        ids.MissionID
	WorkRevisionID   ids.WorkRevisionID
	WorkID           ids.WorkID
	RunID            ids.RunID
	ActorID          ids.ActorID
	ProcessID        ids.ProcessID
	LeaseID          ids.LeaseID
	Generation       uint64
	Fence            string
	ObservedAt       time.Time
	Deadline         time.Time
	RuntimeEpochID   journalv2.RuntimeEpochID
	Outcome          string
	Governance       governancecontext.Context
	OperationEventID journalv2.EventID
	SlotEventID      journalv2.EventID
}

// AuthorityRequestDiagnosticV2 is the bounded, provider-neutral diagnostic
// envelope for execution-authority failures. It carries only immutable request
// coordinates and a closed cause class; provider messages are deliberately not
// retained or serialized.
type AuthorityRequestDiagnosticV2 struct {
	SchemaVersion  uint16                   `json:"schema_version"`
	Kind           ErrorKind                `json:"kind"`
	Subject        string                   `json:"subject"`
	OperationID    journalv2.OperationID    `json:"operation_id"`
	RuntimeEpochID journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	WorkRevisionID ids.WorkRevisionID       `json:"work_revision_id"`
	WorkID         ids.WorkID               `json:"work_id"`
	RunID          ids.RunID                `json:"run_id"`
	LeaseID        ids.LeaseID              `json:"lease_id"`
	ActorID        ids.ActorID              `json:"actor_id"`
	ProcessID      ids.ProcessID            `json:"process_id"`
	CauseClass     string                   `json:"cause_class"`
}

// AuthorityRequestErrorV2 keeps raw causes usable by errors.Is/errors.As while
// ensuring Error and JSON rendering can never expose provider-controlled text.
type AuthorityRequestErrorV2 struct {
	Diagnostic AuthorityRequestDiagnosticV2 `json:"diagnostic"`
	typed      error
	cause      error
}

func (e *AuthorityRequestErrorV2) Error() string {
	return string(e.Diagnostic.Kind) + ": authority request failed"
}

func (e *AuthorityRequestErrorV2) Unwrap() []error {
	if e == nil {
		return nil
	}
	return []error{e.typed, e.cause}
}

func authorityCauseClassV2(cause error) string {
	switch {
	case cause == nil:
		return "none"
	case errors.Is(cause, context.Canceled):
		return "cancelled"
	case errors.Is(cause, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "provider_failure"
	}
}

func authorityRequestErrorV2(kind ErrorKind, subject string, request ExecutionAuthorityRequestV2, cause error) error {
	typed := executionV2Error(kind, subject, nil)
	return &AuthorityRequestErrorV2{
		Diagnostic: AuthorityRequestDiagnosticV2{
			SchemaVersion:  1,
			Kind:           kind,
			Subject:        subject,
			OperationID:    request.OperationID,
			RuntimeEpochID: request.RuntimeEpochID,
			WorkRevisionID: request.WorkRevisionID,
			WorkID:         request.WorkID,
			RunID:          request.RunID,
			LeaseID:        request.LeaseID,
			ActorID:        request.ActorID,
			ProcessID:      request.ProcessID,
			CauseClass:     authorityCauseClassV2(cause),
		},
		typed: typed,
		cause: cause,
	}
}

type ExecutionAuthorityResultV2 struct {
	State    ExecutionSlotStateV2
	Replayed bool
}

type ExecutionAuthorityServiceV2 struct {
	port     ExecutionAuthorityPortV2
	epochs   ExecutionEpochSourceV2
	fences   ExecutionFenceSourceV2
	registry journalv2.EventRegistryV2
}

func NewExecutionAuthorityServiceV2(port ExecutionAuthorityPortV2, epochs ExecutionEpochSourceV2, registry journalv2.EventRegistryV2) (*ExecutionAuthorityServiceV2, error) {
	if port == nil || epochs == nil {
		return nil, executionV2Error(KindInvalidCommand, "execution_service", nil)
	}
	if _, err := registry.Resolve("execution_slot", 1, 1); err != nil {
		return nil, executionV2Error(KindInvalidCommand, "event_registry", err)
	}
	return &ExecutionAuthorityServiceV2{port: port, epochs: epochs, fences: cryptographicExecutionFenceSourceV2{}, registry: registry}, nil
}

func NewExecutionAuthorityServiceWithFenceSourceV2(port ExecutionAuthorityPortV2, epochs ExecutionEpochSourceV2, fences ExecutionFenceSourceV2, registry journalv2.EventRegistryV2) (*ExecutionAuthorityServiceV2, error) {
	service, err := NewExecutionAuthorityServiceV2(port, epochs, registry)
	if err != nil || fences == nil {
		return nil, executionV2Error(KindInvalidCommand, "fence_source", err)
	}
	service.fences = fences
	return service, nil
}

func (s *ExecutionAuthorityServiceV2) Execute(ctx context.Context, request ExecutionAuthorityRequestV2) (ExecutionAuthorityResultV2, error) {
	prepared, err := prepareExecutionRequestV2(request)
	if err != nil {
		return ExecutionAuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveExecutionOperation(ctx, prepared.binding)
	if err != nil {
		return ExecutionAuthorityResultV2{}, authorityRequestErrorV2(KindControlStoreBusy, "operation_resolution", request, err)
	}
	switch resolution.Kind {
	case ExecutionOperationConflictV2:
		return ExecutionAuthorityResultV2{}, executionV2Error(KindIdempotencyConflict, "operation_id", nil)
	case ExecutionOperationExactV2:
		if resolution.State.Status() == journalv2.StatusResolved || resolution.State.Status() == journalv2.StatusRefused {
			if resolution.State.ResultKind() != "execution_slot.result" || resolution.State.ResultSchemaVersion() != executionResultSchemaV2 {
				return ExecutionAuthorityResultV2{}, executionV2Error(KindAuthorityConflict, "operation_result", nil)
			}
			state, decodeErr := DecodeExecutionSlotStateV2(resolution.State.Result())
			if decodeErr != nil {
				return ExecutionAuthorityResultV2{}, executionV2Error(KindAuthorityConflict, "operation_result", decodeErr)
			}
			return ExecutionAuthorityResultV2{State: state, Replayed: true}, nil
		}
		return ExecutionAuthorityResultV2{}, executionV2Error(KindDecisionRequired, "operation_progress", nil)
	case ExecutionOperationAbsentV2:
	default:
		return ExecutionAuthorityResultV2{}, executionV2Error(KindAuthorityConflict, "operation_resolution", nil)
	}
	currentEpoch, err := s.epochs.CurrentRuntimeEpoch(ctx)
	if err != nil || currentEpoch != request.RuntimeEpochID {
		return ExecutionAuthorityResultV2{}, authorityRequestErrorV2(KindStaleAuthority, "runtime_epoch", request, err)
	}
	observation, err := s.port.ObserveExecutionSlot(ctx, prepared.coordinate)
	if err != nil {
		return ExecutionAuthorityResultV2{}, authorityRequestErrorV2(KindControlStoreBusy, "execution_slot", request, err)
	}
	if request.Kind == ExecutionAdmitV2 || request.Kind == ExecutionReplaceV2 {
		shardID, shardErr := workgraph.NewEligibilityPolicy().ShardFor(request.MissionID, request.WorkID)
		if shardErr != nil {
			return ExecutionAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "eligibility", shardErr)
		}
		eligibility, observeErr := s.port.ObserveExecutionEligibility(ctx, request.MissionID, shardID)
		if observeErr != nil {
			return ExecutionAuthorityResultV2{}, authorityRequestErrorV2(KindStaleAuthority, "eligibility", request, observeErr)
		}
		token, tokenErr := eligibility.Token(request.WorkID, request.WorkRevisionID, workgraph.EligibilityStart)
		if tokenErr != nil {
			return ExecutionAuthorityResultV2{}, executionV2Error(KindStaleAuthority, "eligibility", tokenErr)
		}
		prepared.eligibility = token.Transition()
		fence, fenceErr := s.fences.NewExecutionFence(ctx)
		if fenceErr != nil {
			return ExecutionAuthorityResultV2{}, authorityRequestErrorV2(KindControlStoreBusy, "fence_source", request, fenceErr)
		}
		request.Fence = fence
	}
	next, err := decideExecutionTransitionV2(request, prepared.governance, observation.State)
	if err != nil {
		return ExecutionAuthorityResultV2{}, err
	}
	plan, err := buildExecutionAuthorityPlanV2(prepared, next, observation, s.registry)
	if err != nil {
		return ExecutionAuthorityResultV2{}, err
	}
	ack, publishErr := s.port.PublishExecution(ctx, plan)
	if publishErr != nil || !ack.matches(plan) {
		resolved, resolveErr := s.port.ResolveExecutionOperation(ctx, prepared.binding)
		if resolveErr == nil && resolved.Kind == ExecutionOperationExactV2 && (resolved.State.Status() == journalv2.StatusResolved || resolved.State.Status() == journalv2.StatusRefused) {
			state, decodeErr := DecodeExecutionSlotStateV2(resolved.State.Result())
			if decodeErr == nil {
				return ExecutionAuthorityResultV2{State: state, Replayed: true}, nil
			}
		}
		moved, movedErr := s.port.ObserveExecutionSlot(ctx, prepared.coordinate)
		if movedErr == nil && (moved.RecordRevision != observation.RecordRevision || !executionObservationEqualV2(moved.State, observation.State)) {
			return ExecutionAuthorityResultV2{}, authorityRequestErrorV2(KindStaleAuthority, "execution_slot", request, publishErr)
		}
		return ExecutionAuthorityResultV2{}, authorityRequestErrorV2(KindControlStoreBusy, "execution_publication", request, publishErr)
	}
	return ExecutionAuthorityResultV2{State: next}, nil
}

type preparedExecutionRequestV2 struct {
	binding     journalv2.OperationBindingV2
	coordinate  journalv2.AggregateCoordinate
	governance  ExecutionGovernanceV2
	eligibility journalv2.EligibilityGuardTransition
	eventIDs    [2]journalv2.EventID
	kind        ExecutionCommandKindV2
	observedAt  string
}

func prepareExecutionRequestV2(request ExecutionAuthorityRequestV2) (preparedExecutionRequestV2, error) {
	if request.ObservedAt.IsZero() || request.RuntimeEpochID == "" || request.OperationEventID == "" || request.SlotEventID == "" || request.OperationEventID == request.SlotEventID {
		return preparedExecutionRequestV2{}, executionV2Error(KindInvalidCommand, "execution_request", nil)
	}
	governance, err := ExecutionGovernanceFromContextV2(request.Governance)
	if err != nil {
		return preparedExecutionRequestV2{}, err
	}
	coordinate, err := ExecutionSlotCoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID)
	if err != nil {
		return preparedExecutionRequestV2{}, executionV2Error(KindInvalidCommand, "execution_slot", err)
	}
	command, _ := json.Marshal(struct {
		Kind           ExecutionCommandKindV2 `json:"kind"`
		ProjectID      ids.ProjectID          `json:"project_id"`
		MissionID      ids.MissionID          `json:"mission_id"`
		WorkRevisionID ids.WorkRevisionID     `json:"work_revision_id"`
		WorkID         ids.WorkID             `json:"work_id"`
		RunID          ids.RunID              `json:"run_id"`
		ActorID        ids.ActorID            `json:"actor_id"`
		ProcessID      ids.ProcessID          `json:"process_id"`
		LeaseID        ids.LeaseID            `json:"lease_id"`
		Generation     uint64                 `json:"generation"`
		PresentedFence string                 `json:"presented_fence,omitempty"`
		Deadline       string                 `json:"deadline"`
		Outcome        string                 `json:"outcome,omitempty"`
		Governance     ExecutionGovernanceV2  `json:"governance"`
	}{request.Kind, request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, request.RunID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, presentedExecutionFenceV2(request), request.Deadline.UTC().Format(time.RFC3339Nano), request.Outcome, governance})
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "execution_slot." + string(request.Kind), CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: command})
	if err != nil {
		return preparedExecutionRequestV2{}, err
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: request.OperationID, CommandKind: "execution_slot." + string(request.Kind), CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: coordinate.Scope, ActorID: journalv2.ActorID(request.ActorID), ProcessID: journalv2.ProcessID(request.ProcessID), DoctrineRevision: governance.DoctrineRevision, ProfileRevision: governance.ProfileRevision, PolicyRevision: governance.PolicyRevision, OriginatingRuntimeEpochID: request.RuntimeEpochID, ObservedCreatedAt: request.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
	if err != nil {
		return preparedExecutionRequestV2{}, executionV2Error(KindInvalidCommand, "operation_binding", err)
	}
	return preparedExecutionRequestV2{binding: binding, coordinate: coordinate, governance: governance, eventIDs: [2]journalv2.EventID{request.OperationEventID, request.SlotEventID}, kind: request.Kind, observedAt: request.ObservedAt.UTC().Format(time.RFC3339Nano)}, nil
}

func presentedExecutionFenceV2(request ExecutionAuthorityRequestV2) string {
	if request.Kind == ExecutionAdmitV2 || request.Kind == ExecutionReplaceV2 {
		return ""
	}
	return request.Fence
}

func decideExecutionTransitionV2(r ExecutionAuthorityRequestV2, governance ExecutionGovernanceV2, prior *ExecutionSlotStateV2) (ExecutionSlotStateV2, error) {
	next := ExecutionSlotStateV2{SchemaVersion: executionStateSchemaV2, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, RunID: r.RunID, ActorID: r.ActorID, ProcessID: r.ProcessID, LeaseID: r.LeaseID, Generation: r.Generation, Fence: r.Fence, HeartbeatAt: r.ObservedAt.UTC().Format(time.RFC3339Nano), Deadline: r.Deadline.UTC().Format(time.RFC3339Nano), RuntimeEpochID: r.RuntimeEpochID, Status: ExecutionActiveV2, Governance: governance}
	if prior == nil {
		if r.Kind != ExecutionAdmitV2 || r.Generation != 1 {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "execution_slot", nil)
		}
		next.LogicalVersion = 1
		if err := next.Validate(); err != nil {
			return ExecutionSlotStateV2{}, err
		}
		return next, nil
	}
	if err := prior.Validate(); err != nil {
		return ExecutionSlotStateV2{}, executionV2Error(KindAuthorityConflict, "execution_state", err)
	}
	if prior.ProjectID != r.ProjectID || prior.MissionID != r.MissionID || prior.WorkRevisionID != r.WorkRevisionID || prior.WorkID != r.WorkID {
		return ExecutionSlotStateV2{}, executionV2Error(KindAuthorityConflict, "execution_slot", nil)
	}
	next.LogicalVersion = prior.LogicalVersion + 1
	sameAuthority := prior.RunID == r.RunID && prior.ActorID == r.ActorID && prior.ProcessID == r.ProcessID && prior.LeaseID == r.LeaseID && prior.Generation == r.Generation && prior.Fence == r.Fence && prior.RuntimeEpochID == r.RuntimeEpochID && equalExecutionGovernanceV2(prior.Governance, governance)
	switch r.Kind {
	case ExecutionHeartbeatV2:
		if !sameAuthority || prior.Status != ExecutionActiveV2 || !r.ObservedAt.After(mustExecutionTimeV2(prior.HeartbeatAt)) || !r.ObservedAt.Before(mustExecutionTimeV2(prior.Deadline)) || !r.Deadline.After(mustExecutionTimeV2(prior.Deadline)) || !r.Deadline.After(r.ObservedAt) {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "lease", nil)
		}
	case ExecutionExpireV2:
		if !sameAuthority || prior.Status != ExecutionActiveV2 || r.ObservedAt.Before(mustExecutionTimeV2(prior.Deadline)) {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "lease", nil)
		}
		next.Deadline, next.HeartbeatAt, next.Status = prior.Deadline, prior.HeartbeatAt, ExecutionExpiredV2
	case ExecutionReplaceV2:
		if prior.Status != ExecutionExpiredV2 || r.Generation != prior.Generation+1 || r.LeaseID == prior.LeaseID || r.RunID == prior.RunID || r.Fence == prior.Fence || r.ObservedAt.Before(mustExecutionTimeV2(prior.Deadline)) {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "replacement", nil)
		}
	case ExecutionOutcomeV2:
		if !sameAuthority || prior.Status != ExecutionActiveV2 || !r.ObservedAt.Before(mustExecutionTimeV2(prior.Deadline)) {
			return ExecutionSlotStateV2{}, executionV2Error(KindStaleAuthority, "lease", nil)
		}
		switch r.Outcome {
		case "completed":
			next.Status = ExecutionCompletedV2
		case "failed":
			next.Status = ExecutionFailedV2
		case "canceled":
			next.Status = ExecutionCanceledV2
		default:
			return ExecutionSlotStateV2{}, executionV2Error(KindInvalidCommand, "outcome", nil)
		}
		next.Outcome = r.Outcome
		next.HeartbeatAt, next.Deadline = prior.HeartbeatAt, prior.Deadline
	default:
		return ExecutionSlotStateV2{}, executionV2Error(KindInvalidCommand, "command_kind", nil)
	}
	if err := next.Validate(); err != nil {
		return ExecutionSlotStateV2{}, err
	}
	return next, nil
}

type ExecutionAuthorityPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p ExecutionAuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, executionV2Error(KindInvalidCommand, "execution_plan", nil)
	}
	return p.sealed, nil
}

func buildExecutionAuthorityPlanV2(prepared preparedExecutionRequestV2, state ExecutionSlotStateV2, observed ExecutionSlotObservationV2, registry journalv2.EventRegistryV2) (ExecutionAuthorityPlanV2, error) {
	stateBytes, err := encodeExecutionStateV2(state)
	if err != nil {
		return ExecutionAuthorityPlanV2{}, err
	}
	eventIDs := []journalv2.EventID{prepared.eventIDs[0], prepared.eventIDs[1]}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: prepared.binding.OperationID(), LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "execution_slot.result", ResultSchemaVersion: executionResultSchemaV2, CanonicalResult: stateBytes, EventIDs: eventIDs})
	if err != nil {
		return ExecutionAuthorityPlanV2{}, err
	}
	opPayload, err := journalv2.EncodeOperationStateEventPayloadV2(operation)
	if err != nil {
		return ExecutionAuthorityPlanV2{}, err
	}
	eventKind := map[ExecutionCommandKindV2]string{
		ExecutionAdmitV2: "run_admitted_lease_granted", ExecutionHeartbeatV2: "lease_renewed",
		ExecutionExpireV2: "lease_expired", ExecutionReplaceV2: "run_replaced_lease_granted",
		ExecutionOutcomeV2: "outcome_recorded",
	}[prepared.kind]
	eventPayload := ExecutionSlotEventV2{SchemaVersion: 1, Kind: eventKind, ObservedAt: prepared.observedAt, State: state}
	if observed.State != nil {
		predecessor := executionCoordinatesV2(*observed.State)
		eventPayload.Predecessor = &predecessor
	}
	eventBytes, err := encodeExecutionEventV2(eventPayload)
	if err != nil {
		return ExecutionAuthorityPlanV2{}, err
	}
	stateDigest, _ := journalv2.ReducerStateDigestV2(stateBytes)
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(prepared.binding.OperationID()), journalv2.Scope{Kind: journalv2.ScopeHost})
	priorVersion, priorRevision := journalv2.LogicalVersion(0), uint64(0)
	if observed.State != nil {
		priorVersion, priorRevision = journalv2.LogicalVersion(observed.State.LogicalVersion), observed.RecordRevision
	}
	governance := state.Governance.journalRevisions()
	eventType := "execution_slot." + eventKind
	events := []journalv2.EventDraftV2{
		{EventID: prepared.eventIDs[0], EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: state.RuntimeEpochID, ActorID: journalv2.ActorID(state.ActorID), ProcessID: journalv2.ProcessID(state.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(state.WorkRevisionID), PolicyRevision: state.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload},
		{EventID: prepared.eventIDs[1], EventType: eventType, Family: "execution_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: prepared.coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(state.LogicalVersion), CommittedRuntimeEpochID: state.RuntimeEpochID, ActorID: journalv2.ActorID(state.ActorID), ProcessID: journalv2.ProcessID(state.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(state.WorkRevisionID), PolicyRevision: state.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: eventBytes},
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: prepared.eventIDs[0], CommittedRuntimeEpochID: state.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: []byte(`{"status":"resolved"}`)},
		{Coordinate: prepared.coordinate, ExpectedLogicalVersion: priorVersion, NextLogicalVersion: journalv2.LogicalVersion(state.LogicalVersion), EventID: prepared.eventIDs[1], ExpectedRecordRevision: priorRevision, CommittedRuntimeEpochID: state.RuntimeEpochID, CanonicalStateDigest: stateDigest, AuthoritySummary: stateBytes},
	}
	conditions := make([]journalv2.ProviderCondition, 0, 3)
	for _, head := range heads {
		c, e := journalv2.ConditionForHeadTransitionV2(head)
		if e != nil {
			return ExecutionAuthorityPlanV2{}, e
		}
		conditions = append(conditions, c)
	}
	var eligibility []journalv2.EligibilityGuardTransition
	if prepared.kind == ExecutionAdmitV2 || prepared.kind == ExecutionReplaceV2 {
		eligibility = []journalv2.EligibilityGuardTransition{prepared.eligibility}
		c, conditionErr := journalv2.ConditionForEligibilityGuardTransitionV2(prepared.eligibility)
		if conditionErr != nil {
			return ExecutionAuthorityPlanV2{}, conditionErr
		}
		conditions = append(conditions, c)
	}
	sealed, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &prepared.binding, OperationState: operation, Events: events, Heads: heads, EligibilityGuards: eligibility, Conditions: conditions}, registry)
	if err != nil {
		return ExecutionAuthorityPlanV2{}, err
	}
	return ExecutionAuthorityPlanV2{sealed}, nil
}

func ReduceExecutionSlotEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	if event.Family() != "execution_slot" || event.EventSchemaVersion() != 1 || event.ReducerVersion() != 1 || event.Coordinate().Kind != "execution_slot" {
		return nil, executionV2Error(KindInvalidCommand, "event_tuple", nil)
	}
	payload, err := decodeExecutionEventV2(event.Payload())
	next := payload.State
	observedAt, _ := time.Parse(time.RFC3339Nano, payload.ObservedAt)
	if err != nil || next.LogicalVersion != uint64(event.LogicalVersion()) {
		return nil, executionV2Error(KindAuthorityConflict, "event_payload", err)
	}
	coordinate, err := ExecutionSlotCoordinateV2(next.ProjectID, next.MissionID, next.WorkRevisionID, next.WorkID)
	if err != nil || coordinate != event.Coordinate() {
		return nil, executionV2Error(KindAuthorityConflict, "event_membership", err)
	}
	if event.CommittedRuntimeEpochID() != next.RuntimeEpochID || event.ActorID() != journalv2.ActorID(next.ActorID) || event.ProcessID() != journalv2.ProcessID(next.ProcessID) || event.WorkRevisionID() != journalv2.WorkRevisionID(next.WorkRevisionID) || event.PolicyRevision() != next.Governance.PolicyRevision || !stringSlicesEqualV2(event.GovernanceRevisions(), next.Governance.journalRevisions()) {
		return nil, executionV2Error(KindAuthorityConflict, "event_authority", nil)
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.LogicalVersion != 1 || next.Status != ExecutionActiveV2 || payload.Predecessor != nil || payload.Kind != "run_admitted_lease_granted" || event.EventType() != "execution_slot.run_admitted_lease_granted" {
			return nil, executionV2Error(KindAuthorityConflict, "initial_transition", nil)
		}
		return next.CanonicalBytes(), nil
	}
	previous, err := DecodeExecutionSlotStateV2(prior)
	if err != nil || next.LogicalVersion != previous.LogicalVersion+1 {
		return nil, executionV2Error(KindAuthorityConflict, "logical_version", err)
	}
	// Replay validates the same transition law without any clock or adapter call.
	if next.ProjectID != previous.ProjectID || next.MissionID != previous.MissionID || next.WorkRevisionID != previous.WorkRevisionID || next.WorkID != previous.WorkID {
		return nil, executionV2Error(KindAuthorityConflict, "execution_slot", nil)
	}
	if payload.Predecessor == nil || *payload.Predecessor != executionCoordinatesV2(previous) {
		return nil, executionV2Error(KindAuthorityConflict, "predecessor_authority", nil)
	}
	switch next.Status {
	case ExecutionActiveV2:
		if previous.Status == ExecutionActiveV2 {
			renewed := payload.Kind == "lease_renewed" && event.EventType() == "execution_slot.lease_renewed" && mustExecutionTimeV2(next.Deadline).After(mustExecutionTimeV2(previous.Deadline)) && observedAt.Equal(mustExecutionTimeV2(next.HeartbeatAt)) && observedAt.After(mustExecutionTimeV2(previous.HeartbeatAt)) && observedAt.Before(mustExecutionTimeV2(previous.Deadline))
			effectAuthorized := payload.Kind == "effect_authorized" && event.EventType() == "execution_slot.effect_authorized" && next.HeartbeatAt == previous.HeartbeatAt && next.Deadline == previous.Deadline && observedAt.Before(mustExecutionTimeV2(previous.Deadline))
			if !sameExecutionAuthorityV2(previous, next) || (!renewed && !effectAuthorized) {
				return nil, executionV2Error(KindStaleAuthority, "lease", nil)
			}
		} else if previous.Status == ExecutionExpiredV2 {
			if payload.Kind != "run_replaced_lease_granted" || event.EventType() != "execution_slot.run_replaced_lease_granted" || next.Generation != previous.Generation+1 || next.LeaseID == previous.LeaseID || next.RunID == previous.RunID || next.Fence == previous.Fence || observedAt.Before(mustExecutionTimeV2(previous.Deadline)) || !observedAt.Equal(mustExecutionTimeV2(next.HeartbeatAt)) {
				return nil, executionV2Error(KindStaleAuthority, "replacement", nil)
			}
		} else {
			return nil, executionV2Error(KindAuthorityConflict, "terminal", nil)
		}
	case ExecutionExpiredV2:
		if payload.Kind != "lease_expired" || event.EventType() != "execution_slot.lease_expired" || previous.Status != ExecutionActiveV2 || !sameExecutionAuthorityV2(previous, next) || next.HeartbeatAt != previous.HeartbeatAt || next.Deadline != previous.Deadline || observedAt.Before(mustExecutionTimeV2(previous.Deadline)) {
			return nil, executionV2Error(KindStaleAuthority, "expiry", nil)
		}
	case ExecutionCompletedV2, ExecutionFailedV2, ExecutionCanceledV2:
		if payload.Kind != "outcome_recorded" || event.EventType() != "execution_slot.outcome_recorded" || previous.Status != ExecutionActiveV2 || !sameExecutionAuthorityV2(previous, next) || next.HeartbeatAt != previous.HeartbeatAt || next.Deadline != previous.Deadline || !observedAt.Before(mustExecutionTimeV2(previous.Deadline)) || string(next.Status) != next.Outcome {
			return nil, executionV2Error(KindStaleAuthority, "outcome", nil)
		}
	default:
		return nil, executionV2Error(KindInvalidCommand, "status", nil)
	}
	return next.CanonicalBytes(), nil
}

func ExecutionReducerRegistrationsV2() []journalv2.ReducerRegistration {
	sum := sha256.Sum256([]byte("spec-kitty/execution-slot-reducer/1.1"))
	digest, _ := journalv2.ParseDigest("execution_reducer", hex.EncodeToString(sum[:]))
	return []journalv2.ReducerRegistration{{Family: "execution_slot", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/execution", ImplementationDigest: digest, Reduce: ReduceExecutionSlotEventV2}}
}

func strictExecutionJSONV2(encoded []byte, destination any) error {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return fmt.Errorf("execution payload over budget")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing execution JSON")
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return fmt.Errorf("noncanonical execution JSON")
	}
	return nil
}
func executionV2Error(kind ErrorKind, field string, cause error) error {
	d := NewDiagnostic(kind, field, RetryDecisionRequired, "", "", map[string]string{"field": field}, nil, nil, time.Unix(1, 0).UTC())
	d.Cause = cause
	return d
}
func equalExecutionGovernanceV2(a, b ExecutionGovernanceV2) bool {
	aBytes, aErr := json.Marshal(a)
	bBytes, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && bytes.Equal(aBytes, bBytes)
}
func executionObservationEqualV2(a, b *ExecutionSlotStateV2) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return bytes.Equal(a.CanonicalBytes(), b.CanonicalBytes())
}
func sameExecutionAuthorityV2(a, b ExecutionSlotStateV2) bool {
	return a.ProjectID == b.ProjectID && a.MissionID == b.MissionID && a.WorkRevisionID == b.WorkRevisionID && a.WorkID == b.WorkID && a.RunID == b.RunID && a.ActorID == b.ActorID && a.ProcessID == b.ProcessID && a.LeaseID == b.LeaseID && a.Generation == b.Generation && a.Fence == b.Fence && a.RuntimeEpochID == b.RuntimeEpochID && equalExecutionGovernanceV2(a.Governance, b.Governance)
}
func stringSlicesEqualV2(a, b []string) bool {
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
func mustExecutionTimeV2(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
