package integration

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
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type IntegrationAttemptStageV2 string

const (
	IntegrationAttemptRequestedV2       IntegrationAttemptStageV2 = "requested"
	IntegrationAttemptAuthorityActiveV2 IntegrationAttemptStageV2 = "authority_active"
	IntegrationAttemptResultObservedV2  IntegrationAttemptStageV2 = "result_observed"
	IntegrationAttemptPublishingV2      IntegrationAttemptStageV2 = "publishing"
	IntegrationAttemptCompletedV2       IntegrationAttemptStageV2 = "completed"
	IntegrationAttemptRepairRequiredV2  IntegrationAttemptStageV2 = "repair_required"
)

type IntegrationTargetStageV2 string

const (
	IntegrationTargetReservedV2 IntegrationTargetStageV2 = "reserved"
	IntegrationTargetActiveV2   IntegrationTargetStageV2 = "active"
	IntegrationTargetReleasedV2 IntegrationTargetStageV2 = "released"
)

type IntegrationTargetStateV2 struct {
	SchemaVersion  uint16                          `json:"schema_version"`
	LogicalVersion uint64                          `json:"logical_version"`
	Stage          IntegrationTargetStageV2        `json:"stage"`
	RepositoryID   ids.RepositoryID                `json:"repository_id"`
	TargetRef      string                          `json:"target_ref"`
	OperationID    journalv2.OperationID           `json:"operation_id"`
	AcceptanceID   string                          `json:"acceptance_id"`
	Candidate      string                          `json:"candidate"`
	ExpectedOld    string                          `json:"expected_old"`
	Generation     uint64                          `json:"generation"`
	FenceEffectID  journalv2.EffectID              `json:"fence_effect_id"`
	FenceTokenHash string                          `json:"fence_token_sha256,omitempty"`
	RuntimeEpochID journalv2.RuntimeEpochID        `json:"runtime_epoch_id"`
	ActorID        ids.ActorID                     `json:"actor_id"`
	ProcessID      ids.ProcessID                   `json:"process_id"`
	Governance     execution.ExecutionGovernanceV2 `json:"governance"`
	ObservedAt     string                          `json:"observed_at"`
}

func (s IntegrationTargetStateV2) Validate() error {
	if s.SchemaVersion != 1 || s.LogicalVersion == 0 || !validTargetRef(s.TargetRef) || !validCommit(s.Candidate) || !validCommit(s.ExpectedOld) || s.Generation == 0 || s.AcceptanceID == "" || s.FenceEffectID == "" || s.Governance.PolicyRevision == "" {
		return acceptanceErrorV2(KindInvalid, "integration_target")
	}
	for _, parse := range []func() error{
		func() error { _, err := ids.ParseRepositoryID(s.RepositoryID.String()); return err },
		func() error { _, err := journalv2.NewOperationID(string(s.OperationID)); return err },
		func() error { _, err := journalv2.NewRuntimeEpochID(string(s.RuntimeEpochID)); return err },
		func() error { _, err := ids.ParseActorID(s.ActorID.String()); return err },
		func() error { _, err := ids.ParseProcessID(s.ProcessID.String()); return err },
	} {
		if err := parse(); err != nil {
			return acceptanceErrorV2(KindInvalid, "integration_target_identity")
		}
	}
	governance, err := execution.ExecutionGovernanceFromContextV2(s.Governance.Context)
	if err != nil || !bytes.Equal(acceptanceCanonical(governance), acceptanceCanonical(s.Governance)) {
		return acceptanceErrorV2(KindInvalid, "integration_target_governance")
	}
	when, err := time.Parse(time.RFC3339Nano, s.ObservedAt)
	if err != nil || when.Location() != time.UTC {
		return acceptanceErrorV2(KindInvalid, "integration_target_time")
	}
	switch s.Stage {
	case IntegrationTargetReservedV2:
		if s.FenceTokenHash != "" {
			return acceptanceErrorV2(KindInvalid, "integration_target_reserved")
		}
	case IntegrationTargetActiveV2, IntegrationTargetReleasedV2:
		if !validAcceptanceDigest(s.FenceTokenHash) {
			return acceptanceErrorV2(KindInvalid, "integration_target_fence")
		}
	default:
		return acceptanceErrorV2(KindInvalid, "integration_target_stage")
	}
	return nil
}

func (s IntegrationTargetStateV2) CanonicalBytes() []byte {
	if s.Validate() != nil {
		return nil
	}
	return acceptanceCanonical(s)
}

type IntegrationAttemptStateV2 struct {
	SchemaVersion     uint16                          `json:"schema_version"`
	LogicalVersion    uint64                          `json:"logical_version"`
	Stage             IntegrationAttemptStageV2       `json:"stage"`
	AttemptID         string                          `json:"attempt_id"`
	OperationID       journalv2.OperationID           `json:"operation_id"`
	ProjectID         ids.ProjectID                   `json:"project_id"`
	MissionID         ids.MissionID                   `json:"mission_id"`
	WorkRevisionID    ids.WorkRevisionID              `json:"work_revision_id"`
	WorkID            ids.WorkID                      `json:"work_id"`
	AcceptanceID      string                          `json:"acceptance_id"`
	AcceptanceDigest  string                          `json:"acceptance_digest_sha256"`
	RepositoryID      ids.RepositoryID                `json:"repository_id"`
	TargetRef         string                          `json:"target_ref"`
	Candidate         string                          `json:"candidate"`
	ExpectedOld       string                          `json:"expected_old"`
	TargetGeneration  uint64                          `json:"target_generation"`
	FenceTokenHash    string                          `json:"fence_token_sha256,omitempty"`
	Result            string                          `json:"result,omitempty"`
	ObservedTarget    string                          `json:"observed_target,omitempty"`
	Workspace         WorkspaceFacts                  `json:"workspace,omitempty"`
	Conflict          *ConflictEvidence               `json:"conflict,omitempty"`
	RepairAction      string                          `json:"repair_action,omitempty"`
	FenceEffectID     journalv2.EffectID              `json:"fence_effect_id"`
	WorkspaceEffectID journalv2.EffectID              `json:"workspace_effect_id"`
	MergeEffectID     journalv2.EffectID              `json:"merge_effect_id"`
	PublishEffectID   journalv2.EffectID              `json:"publish_effect_id"`
	CleanupEffectID   journalv2.EffectID              `json:"cleanup_effect_id"`
	RuntimeEpochID    journalv2.RuntimeEpochID        `json:"runtime_epoch_id"`
	ActorID           ids.ActorID                     `json:"actor_id"`
	ProcessID         ids.ProcessID                   `json:"process_id"`
	Governance        execution.ExecutionGovernanceV2 `json:"governance"`
	ObservedAt        string                          `json:"observed_at"`
}

func (s IntegrationAttemptStateV2) Validate() error {
	if s.SchemaVersion != 1 || s.LogicalVersion == 0 || s.AttemptID == "" || s.AcceptanceID == "" || !validAcceptanceDigest(s.AcceptanceDigest) || !validTargetRef(s.TargetRef) || !validCommit(s.Candidate) || !validCommit(s.ExpectedOld) || s.TargetGeneration == 0 || s.FenceEffectID == "" || s.WorkspaceEffectID == "" || s.MergeEffectID == "" || s.PublishEffectID == "" || s.CleanupEffectID == "" {
		return acceptanceErrorV2(KindInvalid, "integration_attempt")
	}
	for _, parse := range []func() error{
		func() error { _, err := ids.ParseProjectID(s.ProjectID.String()); return err },
		func() error { _, err := ids.ParseMissionID(s.MissionID.String()); return err },
		func() error { _, err := ids.ParseWorkRevisionID(s.WorkRevisionID.String()); return err },
		func() error { _, err := ids.ParseWorkID(s.WorkID.String()); return err },
		func() error { _, err := ids.ParseRepositoryID(s.RepositoryID.String()); return err },
		func() error { _, err := journalv2.NewOperationID(string(s.OperationID)); return err },
		func() error { _, err := journalv2.NewRuntimeEpochID(string(s.RuntimeEpochID)); return err },
		func() error { _, err := ids.ParseActorID(s.ActorID.String()); return err },
		func() error { _, err := ids.ParseProcessID(s.ProcessID.String()); return err },
	} {
		if err := parse(); err != nil {
			return acceptanceErrorV2(KindInvalid, "integration_attempt_identity")
		}
	}
	governance, err := execution.ExecutionGovernanceFromContextV2(s.Governance.Context)
	if err != nil || !bytes.Equal(acceptanceCanonical(governance), acceptanceCanonical(s.Governance)) {
		return acceptanceErrorV2(KindInvalid, "integration_attempt_governance")
	}
	when, err := time.Parse(time.RFC3339Nano, s.ObservedAt)
	if err != nil || when.Location() != time.UTC {
		return acceptanceErrorV2(KindInvalid, "integration_attempt_time")
	}
	zeroWorkspace := s.Workspace == (WorkspaceFacts{})
	switch s.Stage {
	case IntegrationAttemptRequestedV2:
		if s.FenceTokenHash != "" || s.Result != "" || s.ObservedTarget != "" || !zeroWorkspace || s.Conflict != nil || s.RepairAction != "" {
			return acceptanceErrorV2(KindInvalid, "integration_attempt_requested")
		}
	case IntegrationAttemptAuthorityActiveV2:
		if !validAcceptanceDigest(s.FenceTokenHash) || s.Result != "" || s.ObservedTarget != "" || !zeroWorkspace || s.Conflict != nil || s.RepairAction != "" {
			return acceptanceErrorV2(KindInvalid, "integration_attempt_early")
		}
	case IntegrationAttemptResultObservedV2, IntegrationAttemptPublishingV2, IntegrationAttemptCompletedV2:
		if !validAcceptanceDigest(s.FenceTokenHash) || !validCommit(s.Result) || !validCommit(s.ObservedTarget) || zeroWorkspace || s.Conflict != nil || s.RepairAction != "" {
			return acceptanceErrorV2(KindInvalid, "integration_attempt_result")
		}
	case IntegrationAttemptRepairRequiredV2:
		if !validAcceptanceDigest(s.FenceTokenHash) || s.Conflict == nil || s.RepairAction == "" {
			return acceptanceErrorV2(KindInvalid, "integration_attempt_repair")
		}
	default:
		return acceptanceErrorV2(KindInvalid, "integration_attempt_stage")
	}
	return nil
}

func (s IntegrationAttemptStateV2) CanonicalBytes() []byte {
	if s.Validate() != nil {
		return nil
	}
	return acceptanceCanonical(s)
}

func DecodeIntegrationTargetStateV2(encoded []byte) (IntegrationTargetStateV2, error) {
	var state IntegrationTargetStateV2
	if strictAcceptanceJSON(encoded, &state) != nil || state.Validate() != nil || !bytes.Equal(encoded, state.CanonicalBytes()) {
		return state, acceptanceErrorV2(KindInvalid, "integration_target")
	}
	return state, nil
}
func DecodeIntegrationAttemptStateV2(encoded []byte) (IntegrationAttemptStateV2, error) {
	var state IntegrationAttemptStateV2
	if strictAcceptanceJSON(encoded, &state) != nil || state.Validate() != nil || !bytes.Equal(encoded, state.CanonicalBytes()) {
		return state, acceptanceErrorV2(KindInvalid, "integration_attempt")
	}
	if state.Conflict != nil {
		copyConflict := *state.Conflict
		copyConflict.ConflictFiles = append([]string(nil), state.Conflict.ConflictFiles...)
		state.Conflict = &copyConflict
	}
	return state, nil
}

type integrationStateEventV2 struct {
	SchemaVersion uint16          `json:"schema_version"`
	Kind          string          `json:"kind"`
	PriorDigest   string          `json:"prior_digest,omitempty"`
	State         json.RawMessage `json:"state"`
}

func ReduceIntegrationTargetEventV2(prior []byte, envelope journalv2.EventEnvelopeV2) ([]byte, error) {
	if envelope.Family() != "integration_target" || envelope.EventSchemaVersion() != 1 || envelope.ReducerVersion() != 1 {
		return nil, acceptanceErrorV2(KindInvalid, "integration_target_event")
	}
	var event integrationStateEventV2
	if strictAcceptanceJSON(envelope.Payload(), &event) != nil || event.SchemaVersion != 1 {
		return nil, acceptanceErrorV2(KindInvalid, "integration_target_event")
	}
	next, err := DecodeIntegrationTargetStateV2(event.State)
	if err != nil || event.Kind != string(next.Stage) || envelope.EventType() != "integration_target."+event.Kind {
		return nil, acceptanceErrorV2(KindInvalid, "integration_target_event")
	}
	coordinate, _ := IntegrationTargetCoordinateV2(next.RepositoryID, next.TargetRef)
	if envelope.Coordinate() != coordinate || envelope.LogicalVersion() != journalv2.LogicalVersion(next.LogicalVersion) || envelope.CommittedRuntimeEpochID() != next.RuntimeEpochID || envelope.ActorID() != journalv2.ActorID(next.ActorID) || envelope.ProcessID() != journalv2.ProcessID(next.ProcessID) {
		return nil, acceptanceErrorV2(KindStaleAuthority, "integration_target_event")
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.Stage != IntegrationTargetReservedV2 || next.LogicalVersion != 1 || event.PriorDigest != "" {
			return nil, acceptanceErrorV2(KindConflict, "integration_target_initial")
		}
		return next.CanonicalBytes(), nil
	}
	old, err := DecodeIntegrationTargetStateV2(prior)
	if err != nil || next.LogicalVersion != old.LogicalVersion+1 || !validIntegrationTargetSuccessorV2(old, next) {
		return nil, acceptanceErrorV2(KindConflict, "integration_target_transition")
	}
	digest, _ := journalv2.ReducerStateDigestV2(old.CanonicalBytes())
	if event.PriorDigest != digest.String() {
		return nil, acceptanceErrorV2(KindConflict, "integration_target_prior")
	}
	return next.CanonicalBytes(), nil
}

func ReduceIntegrationAttemptEventV2(prior []byte, envelope journalv2.EventEnvelopeV2) ([]byte, error) {
	if envelope.Family() != "integration_attempt" || envelope.EventSchemaVersion() != 1 || envelope.ReducerVersion() != 1 {
		return nil, acceptanceErrorV2(KindInvalid, "integration_attempt_event")
	}
	var event integrationStateEventV2
	if strictAcceptanceJSON(envelope.Payload(), &event) != nil || event.SchemaVersion != 1 {
		return nil, acceptanceErrorV2(KindInvalid, "integration_attempt_event")
	}
	next, err := DecodeIntegrationAttemptStateV2(event.State)
	if err != nil || event.Kind != string(next.Stage) || envelope.EventType() != "integration_attempt."+event.Kind {
		return nil, acceptanceErrorV2(KindInvalid, "integration_attempt_event")
	}
	coordinate, _ := IntegrationAttemptCoordinateV2(next.ProjectID, next.MissionID, next.WorkRevisionID, next.WorkID, next.AttemptID)
	if envelope.Coordinate() != coordinate || envelope.LogicalVersion() != journalv2.LogicalVersion(next.LogicalVersion) || envelope.CommittedRuntimeEpochID() != next.RuntimeEpochID || envelope.ActorID() != journalv2.ActorID(next.ActorID) || envelope.ProcessID() != journalv2.ProcessID(next.ProcessID) || envelope.WorkRevisionID() != journalv2.WorkRevisionID(next.WorkRevisionID) {
		return nil, acceptanceErrorV2(KindStaleAuthority, "integration_attempt_event")
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.Stage != IntegrationAttemptRequestedV2 || next.LogicalVersion != 1 || event.PriorDigest != "" {
			return nil, acceptanceErrorV2(KindConflict, "integration_attempt_initial")
		}
		return next.CanonicalBytes(), nil
	}
	old, err := DecodeIntegrationAttemptStateV2(prior)
	if err != nil || next.LogicalVersion != old.LogicalVersion+1 || !sameIntegrationAttemptIdentityV2(old, next) || !validAttemptTransitionV2(old.Stage, next.Stage) {
		return nil, acceptanceErrorV2(KindConflict, "integration_attempt_transition")
	}
	digest, _ := journalv2.ReducerStateDigestV2(old.CanonicalBytes())
	if event.PriorDigest != digest.String() {
		return nil, acceptanceErrorV2(KindConflict, "integration_attempt_prior")
	}
	return next.CanonicalBytes(), nil
}

func IntegrationReducerRegistrationsV2() []journalv2.ReducerRegistration {
	makeRegistration := func(family string, reduce journalv2.Reducer) journalv2.ReducerRegistration {
		sum := sha256.Sum256([]byte("spec-kitty/" + family + "-reducer/1/serialized-target"))
		digest, _ := journalv2.ParseDigest("integration_reducer", hex.EncodeToString(sum[:]))
		return journalv2.ReducerRegistration{Family: family, EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/integration", ImplementationDigest: digest, Reduce: reduce}
	}
	return []journalv2.ReducerRegistration{makeRegistration("integration_target", ReduceIntegrationTargetEventV2), makeRegistration("integration_attempt", ReduceIntegrationAttemptEventV2)}
}

func IntegrationTargetCoordinateV2(repositoryID ids.RepositoryID, targetRef string) (journalv2.AggregateCoordinate, error) {
	if _, err := ids.ParseRepositoryID(repositoryID.String()); err != nil || !validTargetRef(targetRef) {
		return journalv2.AggregateCoordinate{}, acceptanceErrorV2(KindInvalid, "integration_target_coordinate")
	}
	sum := sha256.Sum256([]byte(repositoryID.String() + "\x00" + targetRef))
	return journalv2.NewAggregateCoordinate("integration_target", journalv2.AggregateID("target-"+hex.EncodeToString(sum[:20])), journalv2.Scope{Kind: journalv2.ScopeHost})
}
func IntegrationAttemptCoordinateV2(projectID ids.ProjectID, missionID ids.MissionID, revisionID ids.WorkRevisionID, workID ids.WorkID, attemptID string) (journalv2.AggregateCoordinate, error) {
	return journalv2.NewAggregateCoordinate("integration_attempt", journalv2.AggregateID(attemptID), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(projectID), MissionID: journalv2.MissionID(missionID), WorkRevisionID: journalv2.WorkRevisionID(revisionID), WorkID: journalv2.WorkID(workID)})
}

type IntegrationAuthorityRequestV2 struct {
	OperationID    journalv2.OperationID
	AttemptID      string
	ProjectID      ids.ProjectID
	MissionID      ids.MissionID
	WorkRevisionID ids.WorkRevisionID
	WorkID         ids.WorkID
	AcceptanceID   string
	RepositoryID   ids.RepositoryID
	TargetRef      string
	Candidate      string
	RuntimeEpochID journalv2.RuntimeEpochID
	ActorID        ids.ActorID
	ProcessID      ids.ProcessID
	Governance     execution.ExecutionGovernanceV2
	ObservedAt     time.Time
}

type IntegrationAuthorityObservationV2 struct {
	Acceptance  AcceptanceObservationV2
	Eligibility workgraph.ObservedEligibilityShardV2
	Assertions  []journalv2.AuthorityAssertionV2
}
type IntegrationObservationV2 struct {
	Target          *IntegrationTargetStateV2
	TargetRevision  uint64
	Attempt         *IntegrationAttemptStateV2
	AttemptRevision uint64
}
type IntegrationOperationResolutionV2 struct {
	Kind           AcceptanceOperationResolutionKindV2
	State          journalv2.OperationStateV2
	RecordRevision uint64
}
type IntegrationPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p IntegrationPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return p.sealed, acceptanceErrorV2(KindInvalid, "integration_plan")
	}
	return p.sealed, nil
}

type IntegrationAckV2 = AcceptanceAckV2

type IntegrationAuthorityPortV2 interface {
	ResolveIntegrationOperation(context.Context, journalv2.OperationBindingV2) (IntegrationOperationResolutionV2, error)
	ObserveIntegration(context.Context, journalv2.AggregateCoordinate, journalv2.AggregateCoordinate) (IntegrationObservationV2, error)
	ObserveIntegrationAuthority(context.Context, IntegrationAuthorityRequestV2) (IntegrationAuthorityObservationV2, error)
	PublishIntegration(context.Context, IntegrationPlanV2) (IntegrationAckV2, error)
	ReconcileIntegration(context.Context, IntegrationPlanV2) (IntegrationAckV2, error)
}

type IntegrationGitPortV2 interface {
	TargetHead(context.Context, ids.RepositoryID, string) (string, error)
	InstallFence(context.Context, IntegrationFenceIntentV2) (IntegrationFenceObservationV2, error)
	ObserveFence(context.Context, IntegrationFenceIntentV2) (IntegrationFenceObservationV2, bool, error)
	EnsureWorkspace(context.Context, IntegrationWorkspaceIntentV2) (WorkspaceFacts, error)
	MergeCandidate(context.Context, IntegrationMergeIntentV2, WorkspaceFacts) (string, *ConflictEvidence, error)
	CompareAndSwapTarget(context.Context, IntegrationPublishIntentV2) error
	IsAncestor(context.Context, ids.RepositoryID, string, string) (bool, error)
}

type IntegrationFenceIntentV2 struct {
	EffectID                          journalv2.EffectID
	OperationID                       journalv2.OperationID
	RepositoryID                      ids.RepositoryID
	TargetRef, ExpectedOld, Candidate string
	Generation                        uint64
}
type IntegrationFenceObservationV2 struct {
	TokenDigest string
	Generation  uint64
}
type IntegrationWorkspaceIntentV2 struct {
	EffectID                          journalv2.EffectID
	OperationID                       journalv2.OperationID
	RepositoryID                      ids.RepositoryID
	TargetRef, ExpectedOld, Candidate string
	Generation                        uint64
	FenceTokenHash                    string
}
type IntegrationMergeIntentV2 = IntegrationWorkspaceIntentV2
type IntegrationPublishIntentV2 struct {
	EffectID                       journalv2.EffectID
	OperationID                    journalv2.OperationID
	RepositoryID                   ids.RepositoryID
	TargetRef, ExpectedOld, Result string
	Generation                     uint64
	FenceTokenHash                 string
}
type IntegrationCleanupIntentV2 struct {
	EffectID       journalv2.EffectID
	OperationID    journalv2.OperationID
	AttemptID      string
	RepositoryID   ids.RepositoryID
	TargetRef      string
	WorkspacePath  string
	Generation     uint64
	FenceTokenHash string
}

type IntegrationAuthorityServiceV2 struct {
	port     IntegrationAuthorityPortV2
	epochs   AcceptanceEpochSourceV2
	git      IntegrationGitPortV2
	registry journalv2.EventRegistryV2
}

func NewIntegrationAuthorityServiceV2(port IntegrationAuthorityPortV2, epochs AcceptanceEpochSourceV2, git IntegrationGitPortV2, registry journalv2.EventRegistryV2) (*IntegrationAuthorityServiceV2, error) {
	if port == nil || epochs == nil || git == nil {
		return nil, acceptanceErrorV2(KindInvalid, "integration_dependencies")
	}
	for _, family := range []string{"integration_target", "integration_attempt"} {
		if _, err := registry.Resolve(family, 1, 1); err != nil {
			return nil, err
		}
	}
	return &IntegrationAuthorityServiceV2{port, epochs, git, registry}, nil
}

func (s *IntegrationAuthorityServiceV2) Integrate(ctx context.Context, request IntegrationAuthorityRequestV2) (IntegrationAttemptStateV2, error) {
	binding, seed, err := prepareIntegrationAuthorityRequestV2(request)
	if err != nil {
		return IntegrationAttemptStateV2{}, err
	}
	resolution, err := s.port.ResolveIntegrationOperation(ctx, binding)
	if err != nil {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindStoreFailure, "integration_operation")
	}
	if resolution.Kind == AcceptanceOperationConflictV2 {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindIdempotencyConflict, "integration_operation")
	}
	if resolution.Kind == AcceptanceOperationExactV2 && resolution.State.Status() == journalv2.StatusResolved {
		state, decodeErr := DecodeIntegrationAttemptStateV2(resolution.State.Result())
		if decodeErr != nil || !sameIntegrationAttemptRequestV2(seed, state) {
			return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindConflict, "integration_result")
		}
		return state, nil
	}
	targetCoordinate, _ := IntegrationTargetCoordinateV2(request.RepositoryID, request.TargetRef)
	attemptCoordinate, _ := IntegrationAttemptCoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, request.AttemptID)
	var observed IntegrationObservationV2
	var operation *journalv2.OperationStateV2
	var operationRevision uint64
	if resolution.Kind == AcceptanceOperationExactV2 {
		observed, err = s.port.ObserveIntegration(ctx, targetCoordinate, attemptCoordinate)
		if err != nil || observed.Attempt == nil || !sameIntegrationAttemptRequestV2(seed, *observed.Attempt) {
			return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindConflict, "integration_resume")
		}
		op := resolution.State
		operation, operationRevision = &op, resolution.RecordRevision
	} else if resolution.Kind != AcceptanceOperationAbsentV2 {
		return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindConflict, "integration_operation")
	} else {
		observed, err = s.port.ObserveIntegration(ctx, targetCoordinate, attemptCoordinate)
		if err != nil {
			return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindStoreFailure, "integration_target_observation")
		}
		if observed.Attempt != nil {
			return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindConflict, "integration_attempt_alias")
		}
	}

	for {
		if observed.Attempt != nil && (observed.Attempt.Stage == IntegrationAttemptCompletedV2 || observed.Attempt.Stage == IntegrationAttemptRepairRequiredV2) {
			return *observed.Attempt, nil
		}
		proof, err := s.requireIntegrationAuthorityV2(ctx, request)
		if err != nil {
			return IntegrationAttemptStateV2{}, err
		}
		if observed.Attempt == nil {
			head, gitErr := s.git.TargetHead(ctx, request.RepositoryID, request.TargetRef)
			if gitErr != nil {
				return IntegrationAttemptStateV2{}, &Error{Kind: KindGitFailure, Message: "observe integration target", Cause: gitErr}
			}
			nextAttempt := seed
			nextAttempt.AcceptanceDigest = proof.acceptanceDigest
			nextAttempt.ExpectedOld = head
			nextAttempt.LogicalVersion = 1
			nextAttempt.Stage = IntegrationAttemptRequestedV2
			nextTarget := IntegrationTargetStateV2{SchemaVersion: 1, LogicalVersion: 1, Stage: IntegrationTargetReservedV2, RepositoryID: request.RepositoryID, TargetRef: request.TargetRef, OperationID: request.OperationID, AcceptanceID: request.AcceptanceID, Candidate: request.Candidate, ExpectedOld: head, Generation: 1, FenceEffectID: seed.FenceEffectID, RuntimeEpochID: request.RuntimeEpochID, ActorID: request.ActorID, ProcessID: request.ProcessID, Governance: request.Governance, ObservedAt: request.ObservedAt.UTC().Format(time.RFC3339Nano)}
			if observed.Target != nil {
				if observed.Target.Stage != IntegrationTargetReleasedV2 {
					return IntegrationAttemptStateV2{}, &Error{Kind: KindConflict, Message: "integration target already has active authority", Observed: map[string]string{"operation_id": string(observed.Target.OperationID)}}
				}
				nextTarget.LogicalVersion = observed.Target.LogicalVersion + 1
				nextTarget.Generation = observed.Target.Generation + 1
			}
			nextAttempt.TargetGeneration = nextTarget.Generation
			plan, planErr := buildIntegrationPlanV2(request, binding, operation, operationRevision, observed, &nextTarget, nextAttempt, []journalv2.EffectID{seed.FenceEffectID}, proof, s.registry)
			if planErr != nil {
				return IntegrationAttemptStateV2{}, planErr
			}
			ack, publishErr := s.publish(ctx, plan)
			if publishErr != nil {
				return IntegrationAttemptStateV2{}, publishErr
			}
			sealed, _ := plan.Publication()
			op := sealed.OperationState()
			operation, operationRevision = &op, ack.ProviderRevision
			observed = IntegrationObservationV2{Target: &nextTarget, TargetRevision: ack.ProviderRevision, Attempt: &nextAttempt, AttemptRevision: ack.ProviderRevision}
			continue
		}
		attempt := *observed.Attempt
		if observed.Target == nil {
			return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindStaleAuthority, "integration_target_missing")
		}
		target := *observed.Target
		if target.OperationID != request.OperationID || target.RepositoryID != request.RepositoryID || target.TargetRef != request.TargetRef || target.Generation != attempt.TargetGeneration || (attempt.Stage != IntegrationAttemptRequestedV2 && target.FenceTokenHash != attempt.FenceTokenHash) {
			return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindStaleAuthority, "integration_target_authority")
		}
		switch attempt.Stage {
		case IntegrationAttemptRequestedV2:
			intent := IntegrationFenceIntentV2{attempt.FenceEffectID, request.OperationID, request.RepositoryID, request.TargetRef, attempt.ExpectedOld, request.Candidate, target.Generation}
			fence, gitErr := s.git.InstallFence(ctx, intent)
			if gitErr != nil {
				var found bool
				fence, found, gitErr = s.git.ObserveFence(ctx, intent)
				if !found && gitErr == nil {
					gitErr = errors.New("integration fence was not observed after ambiguous installation")
				}
			}
			if gitErr != nil || fence.Generation != target.Generation || !validAcceptanceDigest(fence.TokenDigest) {
				return IntegrationAttemptStateV2{}, &Error{Kind: KindGitFailure, Message: "install integration fence", Cause: gitErr}
			}
			nextAttempt := attempt
			nextAttempt.LogicalVersion++
			nextAttempt.Stage = IntegrationAttemptAuthorityActiveV2
			nextAttempt.FenceTokenHash = fence.TokenDigest
			nextTarget := target
			nextTarget.LogicalVersion++
			nextTarget.Stage = IntegrationTargetActiveV2
			nextTarget.FenceTokenHash = fence.TokenDigest
			plan, planErr := buildIntegrationPlanV2(request, binding, operation, operationRevision, observed, &nextTarget, nextAttempt, []journalv2.EffectID{attempt.WorkspaceEffectID, attempt.MergeEffectID}, proof, s.registry)
			if planErr != nil {
				return IntegrationAttemptStateV2{}, planErr
			}
			ack, publishErr := s.publish(ctx, plan)
			if publishErr != nil {
				return IntegrationAttemptStateV2{}, publishErr
			}
			sealed, _ := plan.Publication()
			op := sealed.OperationState()
			operation, operationRevision = &op, ack.ProviderRevision
			observed = IntegrationObservationV2{Target: &nextTarget, TargetRevision: ack.ProviderRevision, Attempt: &nextAttempt, AttemptRevision: ack.ProviderRevision}
		case IntegrationAttemptAuthorityActiveV2:
			workspace, gitErr := s.git.EnsureWorkspace(ctx, IntegrationWorkspaceIntentV2{attempt.WorkspaceEffectID, request.OperationID, request.RepositoryID, request.TargetRef, attempt.ExpectedOld, request.Candidate, attempt.TargetGeneration, attempt.FenceTokenHash})
			if gitErr != nil {
				return IntegrationAttemptStateV2{}, &Error{Kind: KindGitFailure, Message: "ensure integration workspace", Cause: gitErr}
			}
			result, conflict, mergeErr := s.git.MergeCandidate(ctx, IntegrationMergeIntentV2{attempt.MergeEffectID, request.OperationID, request.RepositoryID, request.TargetRef, attempt.ExpectedOld, request.Candidate, attempt.TargetGeneration, attempt.FenceTokenHash}, workspace)
			next := attempt
			next.LogicalVersion++
			next.Workspace = workspace
			if mergeErr != nil || conflict != nil {
				if conflict == nil {
					return IntegrationAttemptStateV2{}, &Error{Kind: KindGitFailure, Message: "merge candidate", Cause: mergeErr}
				}
				next.Stage = IntegrationAttemptRepairRequiredV2
				next.Conflict = conflict
				next.RepairAction = "submit a new immutable candidate, review, and acceptance after explicit conflict repair"
			} else {
				if !validCommit(result) {
					return IntegrationAttemptStateV2{}, acceptanceErrorV2(KindGitFailure, "integration_result")
				}
				next.Stage = IntegrationAttemptResultObservedV2
				next.Result = result
				next.ObservedTarget = attempt.ExpectedOld
			}
			plan, planErr := buildIntegrationPlanV2(request, binding, operation, operationRevision, observed, nil, next, nil, proof, s.registry)
			if planErr != nil {
				return IntegrationAttemptStateV2{}, planErr
			}
			ack, publishErr := s.publish(ctx, plan)
			if publishErr != nil {
				return IntegrationAttemptStateV2{}, publishErr
			}
			sealed, _ := plan.Publication()
			op := sealed.OperationState()
			operation, operationRevision = &op, ack.ProviderRevision
			observed.Attempt, observed.AttemptRevision = &next, ack.ProviderRevision
		case IntegrationAttemptResultObservedV2:
			current, gitErr := s.git.TargetHead(ctx, request.RepositoryID, request.TargetRef)
			if gitErr != nil {
				return IntegrationAttemptStateV2{}, &Error{Kind: KindGitFailure, Message: "reobserve integration target", Cause: gitErr}
			}
			if current != attempt.ExpectedOld {
				next := integrationRepairV2(attempt, current, "target moved before compare-and-swap; revalidate through a new operation")
				plan, planErr := buildIntegrationPlanV2(request, binding, operation, operationRevision, observed, nil, next, nil, proof, s.registry)
				if planErr != nil {
					return IntegrationAttemptStateV2{}, planErr
				}
				ack, publishErr := s.publish(ctx, plan)
				if publishErr != nil {
					return IntegrationAttemptStateV2{}, publishErr
				}
				observed.Attempt, observed.AttemptRevision = &next, ack.ProviderRevision
				continue
			}
			next := attempt
			next.LogicalVersion++
			next.Stage = IntegrationAttemptPublishingV2
			next.ObservedTarget = current
			plan, planErr := buildIntegrationPlanV2(request, binding, operation, operationRevision, observed, nil, next, []journalv2.EffectID{attempt.PublishEffectID}, proof, s.registry)
			if planErr != nil {
				return IntegrationAttemptStateV2{}, planErr
			}
			ack, publishErr := s.publish(ctx, plan)
			if publishErr != nil {
				return IntegrationAttemptStateV2{}, publishErr
			}
			sealed, _ := plan.Publication()
			op := sealed.OperationState()
			operation, operationRevision = &op, ack.ProviderRevision
			observed.Attempt, observed.AttemptRevision = &next, ack.ProviderRevision
		case IntegrationAttemptPublishingV2:
			publishErr := s.git.CompareAndSwapTarget(ctx, IntegrationPublishIntentV2{attempt.PublishEffectID, request.OperationID, request.RepositoryID, request.TargetRef, attempt.ExpectedOld, attempt.Result, attempt.TargetGeneration, attempt.FenceTokenHash})
			current, observeErr := s.git.TargetHead(ctx, request.RepositoryID, request.TargetRef)
			if observeErr != nil {
				return IntegrationAttemptStateV2{}, &Error{Kind: KindGitFailure, Message: "reconcile target publication", Cause: observeErr}
			}
			ancestral, ancestorErr := s.git.IsAncestor(ctx, request.RepositoryID, attempt.Result, current)
			if ancestorErr != nil {
				return IntegrationAttemptStateV2{}, &Error{Kind: KindGitFailure, Message: "verify published result", Cause: ancestorErr}
			}
			if !ancestral {
				next := integrationRepairV2(attempt, current, "target does not contain the attributable intended result")
				plan, planErr := buildIntegrationPlanV2(request, binding, operation, operationRevision, observed, nil, next, nil, proof, s.registry)
				if planErr != nil {
					return IntegrationAttemptStateV2{}, planErr
				}
				ack, err := s.publish(ctx, plan)
				if err != nil {
					return IntegrationAttemptStateV2{}, err
				}
				observed.Attempt, observed.AttemptRevision = &next, ack.ProviderRevision
				continue
			}
			_ = publishErr // exact observation reconciles a lost acknowledgement.
			next := attempt
			next.LogicalVersion++
			next.Stage = IntegrationAttemptCompletedV2
			next.ObservedTarget = current
			nextTarget := target
			nextTarget.LogicalVersion++
			nextTarget.Stage = IntegrationTargetReleasedV2
			plan, planErr := buildIntegrationPlanV2(request, binding, operation, operationRevision, observed, &nextTarget, next, []journalv2.EffectID{attempt.CleanupEffectID}, proof, s.registry)
			if planErr != nil {
				return IntegrationAttemptStateV2{}, planErr
			}
			ack, err := s.publish(ctx, plan)
			if err != nil {
				return IntegrationAttemptStateV2{}, err
			}
			_ = ack
			// Cleanup is a durable, attributable effect intent in this terminal
			// publication. A separate effect runner may retry it without changing
			// or withholding integration truth.
			return next, nil
		}
	}
}

type integrationAuthorityProofV2 struct {
	eligibility      journalv2.EligibilityGuardTransition
	assertions       []journalv2.AuthorityAssertionV2
	acceptanceDigest string
}

func (s *IntegrationAuthorityServiceV2) requireIntegrationAuthorityV2(ctx context.Context, request IntegrationAuthorityRequestV2) (integrationAuthorityProofV2, error) {
	epoch, err := s.epochs.CurrentRuntimeEpoch(ctx)
	if err != nil || epoch != request.RuntimeEpochID {
		return integrationAuthorityProofV2{}, acceptanceErrorV2(KindStaleAuthority, "integration_epoch")
	}
	observed, err := s.port.ObserveIntegrationAuthority(ctx, request)
	if err != nil {
		return integrationAuthorityProofV2{}, acceptanceErrorV2(KindEvidenceUnavailable, "integration_authority")
	}
	token, err := observed.Eligibility.Token(request.WorkID, request.WorkRevisionID, workgraph.EligibilityIntegration)
	if err != nil {
		return integrationAuthorityProofV2{}, acceptanceErrorV2(KindRevisionIneligible, "integration_eligibility")
	}
	if observed.Acceptance.State == nil || observed.Acceptance.RecordRevision == 0 {
		return integrationAuthorityProofV2{}, acceptanceErrorV2(KindNotApproved, "integration_acceptance")
	}
	accepted := *observed.Acceptance.State
	digest, _ := journalv2.ReducerStateDigestV2(accepted.CanonicalBytes())
	if accepted.Stage != AcceptanceGrantedV2 || accepted.AcceptanceID != request.AcceptanceID || accepted.ProjectID != request.ProjectID || accepted.MissionID != request.MissionID || accepted.WorkRevisionID != request.WorkRevisionID || accepted.WorkID != request.WorkID || accepted.RepositoryID != request.RepositoryID || accepted.Candidate != request.Candidate || digest.IsZero() {
		return integrationAuthorityProofV2{}, acceptanceErrorV2(KindNotApproved, "integration_acceptance")
	}
	coordinate, _ := acceptanceCoordinateV2(accepted)
	if len(observed.Assertions) != 1 {
		return integrationAuthorityProofV2{}, acceptanceErrorV2(KindStaleAuthority, "integration_assertions")
	}
	assertion := observed.Assertions[0]
	if assertion.Target().Coordinate != coordinate || assertion.ObservedRevision() != observed.Acceptance.RecordRevision || assertion.ExpectedLogicalVersion() != journalv2.LogicalVersion(accepted.LogicalVersion) || assertion.ObservedValueDigest().IsZero() {
		return integrationAuthorityProofV2{}, acceptanceErrorV2(KindStaleAuthority, "integration_acceptance_assertion")
	}
	return integrationAuthorityProofV2{eligibility: token.Transition(), assertions: append([]journalv2.AuthorityAssertionV2(nil), observed.Assertions...), acceptanceDigest: digest.String()}, nil
}

func (s *IntegrationAuthorityServiceV2) publish(ctx context.Context, plan IntegrationPlanV2) (IntegrationAckV2, error) {
	ack, err := s.port.PublishIntegration(ctx, plan)
	if err != nil {
		ack, err = s.port.ReconcileIntegration(ctx, plan)
	}
	if err != nil {
		return IntegrationAckV2{}, acceptanceErrorV2(KindStoreFailure, "integration_publication")
	}
	sealed, _ := plan.Publication()
	if ack.ProviderRevision == 0 || ack.PublicationID != sealed.PublicationID() || ack.PlanDigest != sealed.Digest() || ack.MutationCount != sealed.MutationCount() {
		return IntegrationAckV2{}, acceptanceErrorV2(KindConflict, "integration_ack")
	}
	return ack, nil
}

func prepareIntegrationAuthorityRequestV2(r IntegrationAuthorityRequestV2) (journalv2.OperationBindingV2, IntegrationAttemptStateV2, error) {
	if r.ObservedAt.IsZero() || r.AttemptID == "" || r.AcceptanceID == "" || !validTargetRef(r.TargetRef) || !validCommit(r.Candidate) || r.Governance.PolicyRevision == "" {
		return journalv2.OperationBindingV2{}, IntegrationAttemptStateV2{}, acceptanceErrorV2(KindInvalid, "integration_request")
	}
	coordinate, err := IntegrationAttemptCoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, r.AttemptID)
	if err != nil {
		return journalv2.OperationBindingV2{}, IntegrationAttemptStateV2{}, err
	}
	effect := func(kind string) journalv2.EffectID {
		sum := sha256.Sum256([]byte("spec-kitty/integration/" + kind + "/" + string(r.OperationID)))
		id, _ := journalv2.NewEffectID("effect-" + hex.EncodeToString(sum[:20]))
		return id
	}
	seed := IntegrationAttemptStateV2{SchemaVersion: 1, LogicalVersion: 1, Stage: IntegrationAttemptRequestedV2, AttemptID: r.AttemptID, OperationID: r.OperationID, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, AcceptanceID: r.AcceptanceID, AcceptanceDigest: stringsOfZerosDigestV2(), RepositoryID: r.RepositoryID, TargetRef: r.TargetRef, Candidate: r.Candidate, ExpectedOld: strings.Repeat("0", 40), FenceEffectID: effect("fence"), WorkspaceEffectID: effect("workspace"), MergeEffectID: effect("merge"), PublishEffectID: effect("publish"), CleanupEffectID: effect("cleanup"), RuntimeEpochID: r.RuntimeEpochID, ActorID: r.ActorID, ProcessID: r.ProcessID, Governance: r.Governance, ObservedAt: r.ObservedAt.UTC().Format(time.RFC3339Nano)}
	intent := acceptanceCanonical(struct {
		AttemptID            string           `json:"attempt_id"`
		AcceptanceID         string           `json:"acceptance_id"`
		RepositoryID         ids.RepositoryID `json:"repository_id"`
		TargetRef, Candidate string
	}{r.AttemptID, r.AcceptanceID, r.RepositoryID, r.TargetRef, r.Candidate})
	digest, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "integration.execute", CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: intent})
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: r.OperationID, CommandKind: "integration.execute", CommandSchemaVersion: 1, CanonicalIntentDigest: digest, Scope: coordinate.Scope, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), DoctrineRevision: r.Governance.DoctrineRevision, ProfileRevision: r.Governance.ProfileRevision, PolicyRevision: r.Governance.PolicyRevision, OriginatingRuntimeEpochID: r.RuntimeEpochID, ObservedCreatedAt: r.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
	return binding, seed, err
}

func buildIntegrationPlanV2(r IntegrationAuthorityRequestV2, binding journalv2.OperationBindingV2, priorOperation *journalv2.OperationStateV2, priorOperationRevision uint64, observed IntegrationObservationV2, nextTarget *IntegrationTargetStateV2, nextAttempt IntegrationAttemptStateV2, effectIDs []journalv2.EffectID, proof integrationAuthorityProofV2, registry journalv2.EventRegistryV2) (IntegrationPlanV2, error) {
	// Acceptance digest is filled from the exact authority cited by the caller's
	// immutable acceptance ID; it remains stable across all attempt stages.
	if observed.Attempt != nil {
		nextAttempt.AcceptanceDigest = observed.Attempt.AcceptanceDigest
	}
	if nextAttempt.AcceptanceDigest == stringsOfZerosDigestV2() {
		sum := sha256.Sum256([]byte("acceptance:" + r.AcceptanceID))
		nextAttempt.AcceptanceDigest = hex.EncodeToString(sum[:])
	}
	operationVersion := journalv2.LogicalVersion(1)
	if priorOperation != nil {
		operationVersion = priorOperation.LogicalVersion() + 1
	}
	events := make([]journalv2.EventDraftV2, 0, 3)
	heads := make([]journalv2.HeadTransitionV2, 0, 3)
	governance := []string{"config." + r.Governance.PolicyRevision, "doctrine." + r.Governance.DoctrineRevision, "mission_type." + r.Governance.MissionTypeRevision, "profile." + r.Governance.ProfileRevision}
	sort.Strings(governance)
	add := func(family string, coordinate journalv2.AggregateCoordinate, logical journalv2.LogicalVersion, priorRevision uint64, prior []byte, next []byte, stage string) error {
		priorDigest := ""
		if len(prior) != 0 {
			digest, _ := journalv2.ReducerStateDigestV2(prior)
			priorDigest = digest.String()
		}
		payload := acceptanceCanonical(integrationStateEventV2{SchemaVersion: 1, Kind: stage, PriorDigest: priorDigest, State: next})
		eventID := integrationEventIDV2(family, r.OperationID, uint64(logical))
		digest, _ := journalv2.ReducerStateDigestV2(next)
		events = append(events, journalv2.EventDraftV2{EventID: eventID, EventType: family + "." + stage, Family: family, EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: logical, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: payload})
		heads = append(heads, journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: logical - 1, NextLogicalVersion: logical, EventID: eventID, ExpectedRecordRevision: priorRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: digest, AuthoritySummary: next})
		return nil
	}
	if nextTarget != nil {
		coordinate, _ := IntegrationTargetCoordinateV2(nextTarget.RepositoryID, nextTarget.TargetRef)
		var prior []byte
		if observed.Target != nil {
			prior = observed.Target.CanonicalBytes()
		}
		_ = add("integration_target", coordinate, journalv2.LogicalVersion(nextTarget.LogicalVersion), observed.TargetRevision, prior, nextTarget.CanonicalBytes(), string(nextTarget.Stage))
	}
	attemptCoordinate, _ := IntegrationAttemptCoordinateV2(nextAttempt.ProjectID, nextAttempt.MissionID, nextAttempt.WorkRevisionID, nextAttempt.WorkID, nextAttempt.AttemptID)
	var priorAttempt []byte
	if observed.Attempt != nil {
		priorAttempt = observed.Attempt.CanonicalBytes()
	}
	_ = add("integration_attempt", attemptCoordinate, journalv2.LogicalVersion(nextAttempt.LogicalVersion), observed.AttemptRevision, priorAttempt, nextAttempt.CanonicalBytes(), string(nextAttempt.Stage))
	eventIDs := make([]journalv2.EventID, 0, len(events)+1)
	if priorOperation != nil {
		eventIDs = append(eventIDs, priorOperation.EventIDs()...)
	}
	for _, event := range events {
		eventIDs = append(eventIDs, event.EventID)
	}
	opEventID := integrationEventIDV2("operation", r.OperationID, uint64(operationVersion))
	eventIDs = append(eventIDs, opEventID)
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	allEffects := append([]journalv2.EffectID(nil), effectIDs...)
	if priorOperation != nil {
		allEffects = append(allEffects, priorOperation.EffectIDs()...)
	}
	sort.Slice(allEffects, func(i, j int) bool { return allEffects[i] < allEffects[j] })
	status := journalv2.StatusInProgress
	resultKind := ""
	var result []byte
	if nextAttempt.Stage == IntegrationAttemptCompletedV2 || nextAttempt.Stage == IntegrationAttemptRepairRequiredV2 {
		status = journalv2.StatusResolved
		resultKind = "integration.result"
		result = nextAttempt.CanonicalBytes()
	}
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: r.OperationID, LogicalVersion: operationVersion, Status: status, ResultKind: resultKind, ResultSchemaVersion: func() uint32 {
		if resultKind != "" {
			return 1
		}
		return 0
	}(), CanonicalResult: result, EventIDs: eventIDs, EffectIDs: allEffects})
	if err != nil {
		return IntegrationPlanV2{}, err
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(r.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	events = append(events, journalv2.EventDraftV2{EventID: opEventID, EventType: "operation." + string(status), Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: operationVersion, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload})
	heads = append(heads, journalv2.HeadTransitionV2{Coordinate: opCoordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
		if priorOperation != nil {
			return priorOperation.LogicalVersion()
		}
		return 0
	}(), NextLogicalVersion: operationVersion, EventID: opEventID, ExpectedRecordRevision: priorOperationRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opPayload})
	conditions := make([]journalv2.ProviderCondition, 0, len(heads))
	for _, head := range heads {
		condition, e := journalv2.ConditionForHeadTransitionV2(head)
		if e != nil {
			return IntegrationPlanV2{}, e
		}
		conditions = append(conditions, condition)
	}
	eligibilityCondition, err := journalv2.ConditionForEligibilityGuardTransitionV2(proof.eligibility)
	if err != nil {
		return IntegrationPlanV2{}, err
	}
	conditions = append(conditions, eligibilityCondition)
	effects := make([]journalv2.EffectIntentReference, 0, len(effectIDs))
	for _, effectID := range effectIDs {
		kind := "unknown"
		switch effectID {
		case nextAttempt.FenceEffectID:
			kind = "install_fence"
		case nextAttempt.WorkspaceEffectID:
			kind = "ensure_workspace"
		case nextAttempt.MergeEffectID:
			kind = "merge_candidate"
		case nextAttempt.PublishEffectID:
			kind = "compare_and_swap_target"
		case nextAttempt.CleanupEffectID:
			kind = "cleanup_workspace"
		}
		if kind == "unknown" {
			return IntegrationPlanV2{}, acceptanceErrorV2(KindInvalid, "integration_effect")
		}
		canonical := acceptanceCanonical(struct {
			SchemaVersion   uint16                `json:"schema_version"`
			Kind            string                `json:"kind"`
			EffectID        journalv2.EffectID    `json:"effect_id"`
			OperationID     journalv2.OperationID `json:"operation_id"`
			AttemptID       string                `json:"attempt_id"`
			RepositoryID    ids.RepositoryID      `json:"repository_id"`
			TargetRef       string                `json:"target_ref"`
			ExpectedOld     string                `json:"expected_old"`
			Candidate       string                `json:"candidate"`
			Result          string                `json:"result,omitempty"`
			WorkspacePath   string                `json:"workspace_path,omitempty"`
			Generation      uint64                `json:"generation"`
			FenceTokenHash  string                `json:"fence_token_sha256,omitempty"`
			AcceptanceID    string                `json:"acceptance_id"`
			AcceptanceProof string                `json:"acceptance_digest_sha256"`
		}{1, kind, effectID, r.OperationID, nextAttempt.AttemptID, r.RepositoryID, r.TargetRef, nextAttempt.ExpectedOld, r.Candidate, nextAttempt.Result, nextAttempt.Workspace.Path, nextAttempt.TargetGeneration, nextAttempt.FenceTokenHash, r.AcceptanceID, nextAttempt.AcceptanceDigest})
		digest, _ := journalv2.ReducerStateDigestV2(canonical)
		effects = append(effects, journalv2.EffectIntentReference{EffectID: effectID, Digest: digest, Canonical: canonical})
	}
	input := journalv2.CommitPlanInput{OperationState: operation, Events: events, Heads: heads, EffectIntents: effects, Conditions: conditions, EligibilityGuards: []journalv2.EligibilityGuardTransition{proof.eligibility}, Assertions: proof.assertions}
	if priorOperation == nil {
		input.OperationBinding = &binding
	} else {
		input.PriorOperationState = priorOperation
	}
	sealed, err := journalv2.SealCommitPlanV2(input, registry)
	if err != nil {
		return IntegrationPlanV2{}, err
	}
	return IntegrationPlanV2{sealed}, nil
}

func integrationRepairV2(prior IntegrationAttemptStateV2, observedTarget, action string) IntegrationAttemptStateV2 {
	next := prior
	next.LogicalVersion++
	next.Stage = IntegrationAttemptRepairRequiredV2
	next.ObservedTarget = observedTarget
	next.Conflict = &ConflictEvidence{Base: prior.ExpectedOld, Target: observedTarget, Candidate: prior.Candidate, Result: prior.Result, Workspace: prior.Workspace.Path}
	next.RepairAction = action
	return next
}
func validTargetTransitionV2(old, next IntegrationTargetStageV2) bool {
	return old == IntegrationTargetReservedV2 && next == IntegrationTargetActiveV2 || old == IntegrationTargetActiveV2 && next == IntegrationTargetReleasedV2 || old == IntegrationTargetReleasedV2 && next == IntegrationTargetReservedV2
}

func validIntegrationTargetSuccessorV2(old, next IntegrationTargetStateV2) bool {
	if !validTargetTransitionV2(old.Stage, next.Stage) || old.RepositoryID != next.RepositoryID || old.TargetRef != next.TargetRef {
		return false
	}
	if old.Stage == IntegrationTargetReleasedV2 && next.Stage == IntegrationTargetReservedV2 {
		return next.Generation == old.Generation+1 && next.OperationID != old.OperationID && next.FenceTokenHash == ""
	}
	return next.Generation == old.Generation && sameIntegrationTargetIdentityV2(old, next)
}
func validAttemptTransitionV2(old, next IntegrationAttemptStageV2) bool {
	allowed := map[IntegrationAttemptStageV2][]IntegrationAttemptStageV2{IntegrationAttemptRequestedV2: {IntegrationAttemptAuthorityActiveV2}, IntegrationAttemptAuthorityActiveV2: {IntegrationAttemptResultObservedV2, IntegrationAttemptRepairRequiredV2}, IntegrationAttemptResultObservedV2: {IntegrationAttemptPublishingV2, IntegrationAttemptRepairRequiredV2}, IntegrationAttemptPublishingV2: {IntegrationAttemptCompletedV2, IntegrationAttemptRepairRequiredV2}}
	for _, candidate := range allowed[old] {
		if candidate == next {
			return true
		}
	}
	return false
}
func sameIntegrationTargetIdentityV2(a, b IntegrationTargetStateV2) bool {
	a.LogicalVersion, b.LogicalVersion = 0, 0
	a.Stage, b.Stage = "", ""
	a.FenceTokenHash, b.FenceTokenHash = "", ""
	return bytes.Equal(acceptanceCanonical(a), acceptanceCanonical(b))
}
func sameIntegrationAttemptIdentityV2(a, b IntegrationAttemptStateV2) bool {
	a.LogicalVersion, b.LogicalVersion = 0, 0
	a.Stage, b.Stage = "", ""
	a.ExpectedOld, b.ExpectedOld = "", ""
	a.TargetGeneration, b.TargetGeneration = 0, 0
	a.FenceTokenHash, b.FenceTokenHash = "", ""
	a.Result, b.Result = "", ""
	a.ObservedTarget, b.ObservedTarget = "", ""
	a.Workspace, b.Workspace = WorkspaceFacts{}, WorkspaceFacts{}
	a.Conflict, b.Conflict = nil, nil
	a.RepairAction, b.RepairAction = "", ""
	a.AcceptanceDigest, b.AcceptanceDigest = "", ""
	return bytes.Equal(acceptanceCanonical(a), acceptanceCanonical(b))
}
func sameIntegrationAttemptRequestV2(seed, state IntegrationAttemptStateV2) bool {
	return sameIntegrationAttemptIdentityV2(seed, state)
}
func integrationEventIDV2(kind string, operation journalv2.OperationID, version uint64) journalv2.EventID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("spec-kitty/integration/%s/%s/%d", kind, operation, version)))
	id, _ := journalv2.NewEventID("event-" + hex.EncodeToString(sum[:20]))
	return id
}
