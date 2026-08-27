package mission

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

// RequestCommitment is the deterministic digest of one semantic mutation
// request. It excludes correlation and presentation data.
type RequestCommitment string

func (c RequestCommitment) String() string { return string(c) }

func (c RequestCommitment) Validate() error { return DefinitionCommitment(c).Validate() }

// TransitionRequest is an explicit compare-and-set request against one exact
// mission-definition commitment.
type TransitionRequest struct {
	MissionID            ids.MissionID
	DefinitionID         ids.MissionDefinitionID
	DefinitionVersion    DefinitionVersion
	DefinitionCommitment DefinitionCommitment
	ExpectedSnapshot     MissionSnapshot
	ExpectedCurrentStep  StepKey
	ExpectedStateVersion uint64
	Successor            StepKey
	OperationID          ids.OperationID
	EventID              ids.MissionEventID
	ActorID              ids.ActorID
	GovernanceContextID  ids.GovernanceContextID
	OccurredAt           time.Time
	Evidence             []EvidenceRef
	WorkRevisionID       ids.WorkRevisionID
}

func (r TransitionRequest) Validate() error {
	if _, err := ids.ParseMissionID(r.MissionID.String()); err != nil {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "mission_id", "mission identity is invalid")
	}
	if _, err := ids.ParseMissionDefinitionID(r.DefinitionID.String()); err != nil || r.DefinitionVersion.Validate() != nil || r.DefinitionCommitment.Validate() != nil {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "definition", "definition selection is invalid")
	}
	if err := r.ExpectedSnapshot.Validate(); err != nil ||
		r.ExpectedSnapshot.MissionID != r.MissionID ||
		r.ExpectedSnapshot.DefinitionID != r.DefinitionID ||
		r.ExpectedSnapshot.DefinitionVersion != r.DefinitionVersion ||
		r.ExpectedSnapshot.DefinitionCommitment != r.DefinitionCommitment ||
		r.ExpectedSnapshot.CurrentStep != r.ExpectedCurrentStep ||
		r.ExpectedSnapshot.StateVersion != r.ExpectedStateVersion {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "expected_snapshot", "expected mission snapshot is invalid")
	}
	if r.ExpectedStateVersion == 0 {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "expected_state_version", "expected state version must be positive")
	}
	if err := r.ExpectedCurrentStep.Validate(); err != nil || r.ExpectedCurrentStep.Reserved() {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "expected_current_step", "expected current step is invalid")
	}
	if err := r.Successor.Validate(); err != nil {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "successor", "successor step is invalid")
	}
	if _, err := ids.ParseOperationID(r.OperationID.String()); err != nil {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "operation_id", "operation identity is invalid")
	}
	if _, err := ids.ParseMissionEventID(r.EventID.String()); err != nil {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "event_id", "event identity is invalid")
	}
	if _, err := ids.ParseActorID(r.ActorID.String()); err != nil {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "actor_id", "actor identity is invalid")
	}
	if r.OccurredAt.IsZero() || r.OccurredAt.Location() != time.UTC {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "occurred_at", "occurrence time must be non-zero UTC")
	}
	seenIDs := make(map[ids.EvidenceID]bool, len(r.Evidence))
	seenKinds := make(map[EvidenceKind]bool, len(r.Evidence))
	for _, evidence := range r.Evidence {
		if err := evidence.Validate(); err != nil {
			return refusal(KindInvalidRequest, RecoveryCorrectInput, "evidence", "evidence reference is invalid")
		}
		if seenIDs[evidence.ID] || seenKinds[evidence.Kind] {
			return refusal(KindInvalidRequest, RecoveryCorrectInput, "evidence", "evidence references must be unique by identity and kind")
		}
		seenIDs[evidence.ID] = true
		seenKinds[evidence.Kind] = true
	}
	if r.WorkRevisionID != "" {
		if _, err := ids.ParseWorkRevisionID(r.WorkRevisionID.String()); err != nil {
			return refusal(KindInvalidRequest, RecoveryCorrectInput, "work_revision_id", "work revision identity is invalid")
		}
	}
	return nil
}

// TransitionAudit is bounded evidence returned only after durable commit.
type TransitionAudit struct {
	MissionID            ids.MissionID
	OldStep              StepKey
	NewStep              StepKey
	OldStateVersion      uint64
	NewStateVersion      uint64
	OperationID          ids.OperationID
	EventID              ids.MissionEventID
	ActorID              ids.ActorID
	DefinitionCommitment DefinitionCommitment
	GovernanceContextID  ids.GovernanceContextID
	ApprovalEvidenceID   ids.EvidenceID
	OccurredAt           time.Time
}

type TransitionCandidate struct {
	Previous  MissionSnapshot
	Successor MissionSnapshot
	Audit     TransitionAudit
}

// EvaluateTransition is pure: it reads no ambient state and changes neither
// the supplied mission value nor definition.
func EvaluateTransition(instance MissionInstance, definition MissionDefinition, request TransitionRequest) (TransitionCandidate, error) {
	if err := instance.Validate(); err != nil {
		return TransitionCandidate{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "mission", "stored mission state is invalid")
	}
	if err := request.Validate(); err != nil {
		return TransitionCandidate{}, err
	}
	current := instance.Snapshot()
	if request.Successor.Reserved() {
		return TransitionCandidate{}, refusal(KindUnsupportedAction, RecoveryNoFurtherAction, "successor", "requested action is reserved for a later phase")
	}
	if current.MissionID != request.MissionID {
		return TransitionCandidate{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "mission_id", "request is scoped to another mission")
	}
	if current.DefinitionID != request.DefinitionID || current.DefinitionVersion != request.DefinitionVersion || current.DefinitionCommitment != request.DefinitionCommitment {
		return TransitionCandidate{}, refusal(KindDefinitionMismatch, RecoverySelectDefinition, "definition", "request does not name the mission's exact definition")
	}
	if definition.ID() != current.DefinitionID || definition.Version() != current.DefinitionVersion || definition.Commitment() != current.DefinitionCommitment {
		return TransitionCandidate{}, refusal(KindDefinitionMismatch, RecoverySelectDefinition, "definition", "resolved definition does not match stored mission law")
	}
	if current.Lifecycle == LifecycleTerminal || definition.IsTerminal(current.CurrentStep) {
		return TransitionCandidate{}, refusal(KindTerminalMission, RecoveryNoFurtherAction, "current_step", "mission is terminal")
	}
	if current.StateVersion != request.ExpectedStateVersion {
		return TransitionCandidate{}, refusal(KindStateVersionConflict, RecoveryRefreshState, "expected_state_version", "mission state version changed")
	}
	if current.CurrentStep != request.ExpectedCurrentStep {
		return TransitionCandidate{}, refusal(KindStateVersionConflict, RecoveryRefreshState, "expected_current_step", "mission current step changed")
	}
	if !sameMissionSnapshot(current, request.ExpectedSnapshot) {
		return TransitionCandidate{}, refusal(KindStateVersionConflict, RecoveryRefreshState, "expected_snapshot", "mission state changed")
	}
	if request.OccurredAt.Before(current.UpdatedAt) {
		return TransitionCandidate{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "occurred_at", "occurrence time precedes current mission state")
	}
	if len(current.ActiveBlockers) != 0 {
		return TransitionCandidate{}, refusal(KindActiveBlocker, RecoveryClearBlocker, "active_blockers", "mission has an active blocker")
	}
	if len(current.UnresolvedDecisions) != 0 {
		return TransitionCandidate{}, refusal(KindUnresolvedDecision, RecoveryResolveDecision, "unresolved_decisions", "mission has an unresolved required decision")
	}
	contract, found := definition.Step(current.CurrentStep)
	if !found {
		return TransitionCandidate{}, refusal(KindDefinitionMismatch, RecoveryRepairStoredState, "current_step", "stored current step is absent from the selected definition")
	}
	if !containsStep(contract.spec.Successors, request.Successor) {
		return TransitionCandidate{}, refusal(KindIllegalTransition, RecoveryCorrectInput, "successor", "requested successor is not a declared edge")
	}
	destination, found := definition.Step(request.Successor)
	if !found {
		return TransitionCandidate{}, refusal(KindDefinitionMismatch, RecoverySelectDefinition, "successor", "declared successor is absent from the selected definition")
	}
	if destination.spec.Mutating {
		if _, err := ids.ParseGovernanceContextID(request.GovernanceContextID.String()); err != nil {
			return TransitionCandidate{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "governance_context_id", "mutating transition requires governance context")
		}
	}

	evidence := make(map[EvidenceKind]EvidenceRef, len(request.Evidence))
	for _, item := range request.Evidence {
		if item.MissionID != current.MissionID || item.ForStep != current.CurrentStep || item.StateVersion != current.StateVersion {
			return TransitionCandidate{}, refusal(KindMissingEvidence, RecoverySupplyEvidence, "evidence", "evidence is not scoped to the current mission state")
		}
		evidence[item.Kind] = item
	}
	var approvalEvidenceID ids.EvidenceID
	for _, required := range destination.spec.RequiredEvidence {
		item, exists := evidence[required]
		if !exists {
			return TransitionCandidate{}, refusal(KindMissingEvidence, RecoverySupplyEvidence, "evidence", fmt.Sprintf("required evidence %s is absent", required))
		}
		if item.Status != EvidenceAccepted {
			if required == EvidenceSpecificationOK || required == EvidencePlanOK {
				return TransitionCandidate{}, refusal(KindApprovalStale, RecoveryRefreshApproval, "evidence", fmt.Sprintf("required approval %s is not current", required))
			}
			return TransitionCandidate{}, refusal(KindMissingEvidence, RecoverySupplyEvidence, "evidence", fmt.Sprintf("required evidence %s is not accepted", required))
		}
		if required == EvidenceSpecificationOK || required == EvidencePlanOK {
			approvalEvidenceID = item.ID
		}
	}
	if request.Successor == StepWorkFinalized && request.WorkRevisionID == "" {
		return TransitionCandidate{}, refusal(KindMissingEvidence, RecoverySupplyEvidence, "work_revision_id", "finalization requires an immutable work revision")
	}

	successor := current.clone()
	successor.CurrentStep = request.Successor
	successor.StateVersion++
	successor.UpdatedAt = request.OccurredAt
	if request.WorkRevisionID != "" {
		successor.CurrentWorkRevisionID = request.WorkRevisionID
	}
	if definition.IsTerminal(request.Successor) {
		successor.Lifecycle = LifecycleTerminal
		successor.TerminalOutcome = destination.spec.TerminalOutcome
	}
	if err := successor.Validate(); err != nil {
		return TransitionCandidate{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "successor", "transition would construct invalid mission state")
	}
	return TransitionCandidate{
		Previous:  current,
		Successor: successor,
		Audit: TransitionAudit{
			MissionID: current.MissionID, OldStep: current.CurrentStep, NewStep: successor.CurrentStep,
			OldStateVersion: current.StateVersion, NewStateVersion: successor.StateVersion,
			OperationID: request.OperationID, EventID: request.EventID, ActorID: request.ActorID,
			DefinitionCommitment: current.DefinitionCommitment,
			GovernanceContextID:  request.GovernanceContextID, ApprovalEvidenceID: approvalEvidenceID,
			OccurredAt: request.OccurredAt,
		},
	}, nil
}

type CreateRequest struct {
	MissionID         ids.MissionID
	ProjectID         ids.ProjectID
	DefinitionID      ids.MissionDefinitionID
	DefinitionVersion DefinitionVersion
	OperationID       ids.OperationID
	ActorID           ids.ActorID
	OccurredAt        time.Time
}

type CreateCommit struct {
	Instance          MissionSnapshot
	OperationID       ids.OperationID
	RequestCommitment RequestCommitment
	ActorID           ids.ActorID
	OccurredAt        time.Time
}

type CreateResult struct {
	Instance MissionSnapshot
	Replayed bool
}

type TransitionCommit struct {
	Candidate         TransitionCandidate
	OperationID       ids.OperationID
	RequestCommitment RequestCommitment
}

type TransitionResult struct {
	Instance MissionSnapshot
	Audit    TransitionAudit
	Replayed bool
}

// Store is the aggregate-shaped atomic persistence port. Implementations must
// compare mission scope, definition commitment, current step, expected state
// version, operation identity, and request commitment in one durable boundary.
type Store interface {
	CreateMission(context.Context, CreateCommit) (CreateResult, error)
	LoadMission(context.Context, ids.MissionID) (MissionSnapshot, error)
	ReplayTransition(context.Context, ids.MissionID, ids.OperationID, RequestCommitment) (TransitionResult, bool, error)
	CommitTransition(context.Context, TransitionCommit) (TransitionResult, error)
}

// ArtifactApprovalSource is the read-only artifact authority needed by mission
// approval transitions. It deliberately excludes publication, approval writes,
// persistence transactions, and adapter construction.
type ArtifactApprovalSource interface {
	CurrentRevision(context.Context, string, artifact.Kind) (artifact.ArtifactRevision, error)
	Approvals(context.Context, string, artifact.Kind) ([]artifact.ArtifactApproval, error)
}

type Service struct {
	registry          Registry
	store             Store
	artifactApprovals ArtifactApprovalSource
}

func NewService(registry Registry, store Store) *Service {
	return &Service{registry: registry, store: store}
}

// NewServiceWithArtifactApprovals composes the same mission service with the
// read-only source required for specification and plan approval gates.
func NewServiceWithArtifactApprovals(registry Registry, store Store, source ArtifactApprovalSource) *Service {
	return &Service{registry: registry, store: store, artifactApprovals: source}
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (CreateResult, error) {
	if s == nil || s.store == nil {
		return CreateResult{}, refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "store", "mission persistence is unavailable")
	}
	if _, err := ids.ParseMissionID(request.MissionID.String()); err != nil {
		return CreateResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "mission_id", "mission identity is invalid")
	}
	if _, err := ids.ParseProjectID(request.ProjectID.String()); err != nil {
		return CreateResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "project_id", "project identity is invalid")
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return CreateResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "operation_id", "operation identity is invalid")
	}
	if _, err := ids.ParseActorID(request.ActorID.String()); err != nil {
		return CreateResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "actor_id", "actor identity is invalid")
	}
	if request.OccurredAt.IsZero() || request.OccurredAt.Location() != time.UTC {
		return CreateResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "occurred_at", "creation time must be non-zero UTC")
	}
	definition, err := s.registry.Resolve(request.DefinitionID, request.DefinitionVersion)
	if err != nil {
		return CreateResult{}, err
	}
	snapshot := MissionSnapshot{
		MissionID: request.MissionID, ProjectID: request.ProjectID,
		DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), DefinitionCommitment: definition.Commitment(),
		Lifecycle: LifecycleOpen, CurrentStep: definition.InitialStep(), StateVersion: 1,
		CreatedAt: request.OccurredAt, UpdatedAt: request.OccurredAt,
	}
	if _, err := NewMissionInstance(snapshot); err != nil {
		return CreateResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "mission", "creation would construct invalid mission state")
	}
	commitment, err := digestRequest(struct {
		MissionID            ids.MissionID
		ProjectID            ids.ProjectID
		DefinitionID         ids.MissionDefinitionID
		DefinitionVersion    DefinitionVersion
		DefinitionCommitment DefinitionCommitment
		ActorID              ids.ActorID
		OccurredAt           string
	}{request.MissionID, request.ProjectID, definition.ID(), definition.Version(), definition.Commitment(), request.ActorID, request.OccurredAt.Format(time.RFC3339Nano)})
	if err != nil {
		return CreateResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "request", "creation request cannot be committed")
	}
	result, err := s.store.CreateMission(ctx, CreateCommit{Instance: snapshot.clone(), OperationID: request.OperationID, RequestCommitment: commitment, ActorID: request.ActorID, OccurredAt: request.OccurredAt})
	if err != nil {
		return CreateResult{}, normalizeStoreError(err)
	}
	if err := result.Instance.Validate(); err != nil {
		return CreateResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "mission", "store returned invalid created mission state")
	}
	if !sameMissionSnapshot(snapshot, result.Instance) {
		return CreateResult{}, refusal(KindPersistenceContract, RecoveryRepairStoredState, "store.create", "mission store violated create postconditions")
	}
	return CreateResult{Instance: result.Instance.clone(), Replayed: result.Replayed}, nil
}

func (s *Service) Transition(ctx context.Context, request TransitionRequest) (TransitionResult, error) {
	if s == nil || s.store == nil {
		return TransitionResult{}, refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "store", "mission persistence is unavailable")
	}
	if err := request.Validate(); err != nil {
		return TransitionResult{}, err
	}
	if containsCallerApprovalEvidence(request.Evidence) {
		return TransitionResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "evidence", "artifact approval evidence must be derived from artifact authority")
	}
	commitment, err := transitionRequestCommitment(request)
	if err != nil {
		return TransitionResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "request", "transition request cannot be committed")
	}
	definition, err := s.registry.Resolve(request.DefinitionID, request.DefinitionVersion)
	if err != nil {
		return TransitionResult{}, err
	}
	if definition.Commitment() != request.DefinitionCommitment {
		return TransitionResult{}, refusal(KindDefinitionMismatch, RecoverySelectDefinition, "definition_commitment", "request definition commitment does not match registered law")
	}
	prior, found, err := s.store.ReplayTransition(ctx, request.MissionID, request.OperationID, commitment)
	if err != nil {
		return TransitionResult{}, normalizeStoreError(err)
	}
	if found {
		if err := validateReplayPostcondition(definition, request, prior); err != nil {
			return TransitionResult{}, err
		}
		prior.Instance = prior.Instance.clone()
		prior.Replayed = true
		return prior, nil
	}
	snapshot, err := s.store.LoadMission(ctx, request.MissionID)
	if err != nil {
		return TransitionResult{}, normalizeStoreError(err)
	}
	instance, err := NewMissionInstance(snapshot)
	if err != nil {
		return TransitionResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "mission", "store returned invalid mission state")
	}
	if !sameMissionSnapshot(request.ExpectedSnapshot, snapshot) {
		return TransitionResult{}, refusal(KindStateVersionConflict, RecoveryRefreshState, "expected_snapshot", "mission state changed")
	}
	definition, err = s.registry.Resolve(snapshot.DefinitionID, snapshot.DefinitionVersion)
	if err != nil {
		return TransitionResult{}, err
	}
	evaluatedRequest, err := s.withArtifactApprovalEvidence(ctx, definition, request)
	if err != nil {
		return TransitionResult{}, err
	}
	candidate, err := EvaluateTransition(instance, definition, evaluatedRequest)
	if err != nil {
		return TransitionResult{}, err
	}
	result, err := s.store.CommitTransition(ctx, TransitionCommit{Candidate: candidate, OperationID: request.OperationID, RequestCommitment: commitment})
	if err != nil {
		return TransitionResult{}, normalizeStoreError(err)
	}
	if err := result.Instance.Validate(); err != nil {
		return TransitionResult{}, refusal(KindInvalidStoredState, RecoveryRepairStoredState, "mission", "store returned invalid transition state")
	}
	if !sameMissionSnapshot(candidate.Successor, result.Instance) || candidate.Audit != result.Audit {
		return TransitionResult{}, refusal(KindPersistenceContract, RecoveryRepairStoredState, "store.commit", "mission store violated transition postconditions")
	}
	return TransitionResult{Instance: result.Instance.clone(), Audit: result.Audit, Replayed: result.Replayed}, nil
}

func containsCallerApprovalEvidence(evidence []EvidenceRef) bool {
	for _, item := range evidence {
		if item.Kind == EvidenceSpecificationOK || item.Kind == EvidencePlanOK {
			return true
		}
	}
	return false
}

func (s *Service) withArtifactApprovalEvidence(ctx context.Context, definition MissionDefinition, request TransitionRequest) (TransitionRequest, error) {
	destination, found := definition.Step(request.Successor)
	if !found {
		return request, nil
	}
	approvalKind, artifactKind, required := approvalRequirement(destination.spec.RequiredEvidence)
	if !required {
		return request, nil
	}
	if s.artifactApprovals == nil {
		return TransitionRequest{}, refusal(KindMissingEvidence, RecoverySupplyEvidence, "artifact_approval", "current artifact approval evidence is absent")
	}
	revision, err := s.artifactApprovals.CurrentRevision(ctx, request.MissionID.String(), artifactKind)
	if err != nil {
		if artifact.IsKind(err, artifact.ErrorNotFound) {
			return TransitionRequest{}, refusal(KindMissingEvidence, RecoverySupplyEvidence, "artifact_approval", "current artifact approval evidence is absent")
		}
		return TransitionRequest{}, refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "artifact_approval", "artifact approval evidence could not be loaded")
	}
	if revision.MissionID() != request.MissionID.String() || revision.Kind() != artifactKind {
		return TransitionRequest{}, refusal(KindPersistenceContract, RecoveryRepairStoredState, "artifact_approval", "artifact source returned a revision outside the requested scope")
	}
	approvals, err := s.artifactApprovals.Approvals(ctx, request.MissionID.String(), artifactKind)
	if err != nil {
		return TransitionRequest{}, refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "artifact_approval", "artifact approval evidence could not be loaded")
	}
	freshness, err := artifact.EvaluateFreshness(revision.ID(), approvals)
	if err != nil {
		return TransitionRequest{}, refusal(KindPersistenceContract, RecoveryRepairStoredState, "artifact_approval", "artifact source returned invalid approval evidence")
	}
	switch freshness.Status {
	case artifact.FreshnessFresh:
		approvalID, err := ids.ParseEvidenceID(freshness.ApprovalID)
		if err != nil {
			return TransitionRequest{}, refusal(KindPersistenceContract, RecoveryRepairStoredState, "artifact_approval", "artifact source returned an invalid approval identity")
		}
		result := request
		result.Evidence = append(append([]EvidenceRef(nil), request.Evidence...), EvidenceRef{
			ID: approvalID, Kind: approvalKind, Status: EvidenceAccepted,
			MissionID: request.MissionID, ForStep: request.ExpectedCurrentStep,
			StateVersion: request.ExpectedStateVersion,
		})
		return result, nil
	case artifact.FreshnessRejected, artifact.FreshnessStale:
		return TransitionRequest{}, refusal(KindApprovalStale, RecoveryRefreshApproval, "artifact_approval", "current artifact revision lacks a fresh approval")
	case artifact.FreshnessMissing:
		return TransitionRequest{}, refusal(KindMissingEvidence, RecoverySupplyEvidence, "artifact_approval", "current artifact approval evidence is absent")
	default:
		return TransitionRequest{}, refusal(KindPersistenceContract, RecoveryRepairStoredState, "artifact_approval", "artifact source returned an unsupported freshness result")
	}
}

func approvalRequirement(required []EvidenceKind) (EvidenceKind, artifact.Kind, bool) {
	for _, kind := range required {
		switch kind {
		case EvidenceSpecificationOK:
			return kind, artifact.KindSpecification, true
		case EvidencePlanOK:
			return kind, artifact.KindPlan, true
		}
	}
	return "", "", false
}

func transitionRequestCommitment(request TransitionRequest) (RequestCommitment, error) {
	evidence := append([]EvidenceRef(nil), request.Evidence...)
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Kind != evidence[j].Kind {
			return evidence[i].Kind < evidence[j].Kind
		}
		return evidence[i].ID.String() < evidence[j].ID.String()
	})
	return digestRequest(struct {
		MissionID            ids.MissionID
		DefinitionID         ids.MissionDefinitionID
		DefinitionVersion    DefinitionVersion
		DefinitionCommitment DefinitionCommitment
		ExpectedSnapshot     MissionSnapshot
		ExpectedCurrentStep  StepKey
		ExpectedStateVersion uint64
		Successor            StepKey
		EventID              ids.MissionEventID
		ActorID              ids.ActorID
		GovernanceContextID  ids.GovernanceContextID
		OccurredAt           string
		Evidence             []EvidenceRef
		WorkRevisionID       ids.WorkRevisionID
	}{request.MissionID, request.DefinitionID, request.DefinitionVersion, request.DefinitionCommitment, request.ExpectedSnapshot.clone(),
		request.ExpectedCurrentStep, request.ExpectedStateVersion, request.Successor, request.EventID, request.ActorID,
		request.GovernanceContextID, request.OccurredAt.Format(time.RFC3339Nano), evidence, request.WorkRevisionID})
}

func validateReplayPostcondition(definition MissionDefinition, request TransitionRequest, result TransitionResult) error {
	if err := result.Instance.Validate(); err != nil {
		return refusal(KindInvalidStoredState, RecoveryRepairStoredState, "mission", "store returned invalid replay state")
	}
	expected, err := NewMissionInstance(request.ExpectedSnapshot)
	if err != nil {
		return refusal(KindInvalidRequest, RecoveryCorrectInput, "expected_snapshot", "expected mission snapshot is invalid")
	}
	replayRequest := request
	destination, found := definition.Step(request.Successor)
	if found {
		approvalKind, _, required := approvalRequirement(destination.spec.RequiredEvidence)
		if required {
			if _, err := ids.ParseEvidenceID(result.Audit.ApprovalEvidenceID.String()); err != nil {
				return refusal(KindPersistenceContract, RecoveryRepairStoredState, "store.replay", "mission store omitted authoritative approval evidence")
			}
			replayRequest.Evidence = append(append([]EvidenceRef(nil), request.Evidence...), EvidenceRef{
				ID: result.Audit.ApprovalEvidenceID, Kind: approvalKind, Status: EvidenceAccepted,
				MissionID: request.MissionID, ForStep: request.ExpectedCurrentStep,
				StateVersion: request.ExpectedStateVersion,
			})
		}
	}
	candidate, err := EvaluateTransition(expected, definition, replayRequest)
	if err != nil || !sameMissionSnapshot(candidate.Successor, result.Instance) || candidate.Audit != result.Audit {
		return refusal(KindPersistenceContract, RecoveryRepairStoredState, "store.replay", "mission store violated replay postconditions")
	}
	return nil
}

func sameMissionSnapshot(left, right MissionSnapshot) bool {
	if left.MissionID != right.MissionID || left.ProjectID != right.ProjectID ||
		left.DefinitionID != right.DefinitionID || left.DefinitionVersion != right.DefinitionVersion ||
		left.DefinitionCommitment != right.DefinitionCommitment || left.Lifecycle != right.Lifecycle ||
		left.CurrentStep != right.CurrentStep || left.CurrentWorkRevisionID != right.CurrentWorkRevisionID ||
		left.StateVersion != right.StateVersion || !left.CreatedAt.Equal(right.CreatedAt) ||
		!left.UpdatedAt.Equal(right.UpdatedAt) || left.TerminalOutcome != right.TerminalOutcome ||
		len(left.ActiveBlockers) != len(right.ActiveBlockers) || len(left.UnresolvedDecisions) != len(right.UnresolvedDecisions) {
		return false
	}
	for index := range left.ActiveBlockers {
		if left.ActiveBlockers[index] != right.ActiveBlockers[index] {
			return false
		}
	}
	for index := range left.UnresolvedDecisions {
		if left.UnresolvedDecisions[index] != right.UnresolvedDecisions[index] {
			return false
		}
	}
	return true
}

func digestRequest(value any) (RequestCommitment, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return RequestCommitment(fmt.Sprintf("%x", digest)), nil
}

func normalizeStoreError(err error) error {
	if err == nil {
		return nil
	}
	var domain *Error
	if errors.As(err, &domain) {
		if recovery, message, ok := canonicalStoreDiagnostic(domain.Kind); ok {
			return refusal(domain.Kind, recovery, "store", message)
		}
	}
	return refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "store", "mission persistence operation failed")
}

func canonicalStoreDiagnostic(kind ErrorKind) (RecoveryCategory, string, bool) {
	switch kind {
	case KindNotFound:
		return RecoveryCorrectInput, "mission was not found", true
	case KindAlreadyExists:
		return RecoveryCorrectInput, "mission already exists", true
	case KindDefinitionMismatch:
		return RecoverySelectDefinition, "stored definition does not match", true
	case KindStateVersionConflict:
		return RecoveryRefreshState, "mission state version changed", true
	case KindIdempotencyConflict:
		return RecoveryCorrectInput, "operation identity was reused with different input", true
	case KindInvalidStoredState, KindPersistenceContract:
		return RecoveryRepairStoredState, "mission persistence state requires repair", true
	case KindPersistenceUnavailable:
		return RecoveryRetryPersistence, "mission persistence operation failed", true
	case KindActiveBlocker:
		return RecoveryClearBlocker, "mission has an active blocker", true
	case KindUnresolvedDecision:
		return RecoveryResolveDecision, "mission has an unresolved decision", true
	default:
		return "", "", false
	}
}
