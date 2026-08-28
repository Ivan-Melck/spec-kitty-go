package mission

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

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type AuthorityActionKind string

const (
	AuthorityExpireRun           AuthorityActionKind = "expire_run_authority"
	AuthorityQuarantineWorkspace AuthorityActionKind = "quarantine_workspace"
)

type AuthorityAction struct {
	Kind             AuthorityActionKind   `json:"kind"`
	WorkID           ids.WorkID            `json:"work_id"`
	Status           workgraph.WorkStatus  `json:"work_status"`
	Disposition      workgraph.Disposition `json:"disposition"`
	ReconciliationID ids.OperationID       `json:"reconciliation_id"`
	OldRevisionID    ids.WorkRevisionID    `json:"old_revision_id"`
	NewRevisionID    ids.WorkRevisionID    `json:"new_revision_id"`
	Reason           string                `json:"reason"`
}

type SuccessorPublicationCommand struct {
	MissionID                 ids.MissionID
	ProjectID                 ids.ProjectID
	ExpectedMission           MissionSnapshot
	ExpectedCurrentGeneration uint64
	ExpectedOld               workgraph.RevisionV2
	Candidate                 workgraph.RevisionV2
	Drift                     workgraph.DriftReport
	ReconciliationID          ids.OperationID
	Reconciliation            workgraph.MixedReconciliation
	OperationID               ids.OperationID
	ActorID                   ids.ActorID
	GovernanceContextID       ids.GovernanceContextID
	Rationale                 string
	OccurredAt                time.Time
}

type SuccessorPublicationResult struct {
	Revision       workgraph.RevisionV2
	Drift          workgraph.DriftReport
	Reconciliation workgraph.MixedReconciliation
	Mission        MissionSnapshot
	Actions        []AuthorityAction
	RequestDigest  string
	Replayed       bool
}

type SuccessorPublicationCommit struct {
	MissionID                 ids.MissionID
	ProjectID                 ids.ProjectID
	ExpectedMission           MissionSnapshot
	ExpectedCurrentGeneration uint64
	ExpectedOld               workgraph.RevisionV2
	Candidate                 workgraph.RevisionV2
	Drift                     workgraph.DriftReport
	ReconciliationID          ids.OperationID
	Reconciliation            workgraph.MixedReconciliation
	OperationID               ids.OperationID
	ActorID                   ids.ActorID
	GovernanceContextID       ids.GovernanceContextID
	Rationale                 string
	OccurredAt                time.Time
	Actions                   []AuthorityAction
	RequestDigest             string
}

// SuccessorPublicationStore owns one atomic aggregate mutation. Implementations
// publish the successor, reconciliation, action facts, audit/outbox evidence,
// operation record, and current-generation pointer together or publish nothing.
type SuccessorPublicationStore interface {
	ReplaySuccessorPublication(context.Context, ids.MissionID, ids.OperationID, string) (SuccessorPublicationResult, bool, error)
	PublishSuccessorAtomically(context.Context, SuccessorPublicationCommit) (SuccessorPublicationResult, error)
}

type SuccessorPublicationService struct{ store SuccessorPublicationStore }

func NewSuccessorPublicationService(store SuccessorPublicationStore) *SuccessorPublicationService {
	return &SuccessorPublicationService{store: store}
}

func (service *SuccessorPublicationService) Publish(ctx context.Context, command SuccessorPublicationCommand) (SuccessorPublicationResult, error) {
	if service == nil || service.store == nil {
		return SuccessorPublicationResult{}, refusal(KindInvalidRequest, RecoveryCorrectInput, "service", "successor publication store is required")
	}
	actions, requestDigest, err := validateSuccessorPublication(command)
	if err != nil {
		return SuccessorPublicationResult{}, err
	}
	if replay, found, replayErr := service.store.ReplaySuccessorPublication(ctx, command.MissionID, command.OperationID, requestDigest); replayErr != nil {
		return SuccessorPublicationResult{}, normalizeSuccessorStoreError(replayErr)
	} else if found {
		if err := validateSuccessorResult(command, actions, requestDigest, replay); err != nil {
			return SuccessorPublicationResult{}, err
		}
		replay.Replayed = true
		return cloneSuccessorResult(replay), nil
	}
	result, err := service.store.PublishSuccessorAtomically(ctx, SuccessorPublicationCommit{
		MissionID: command.MissionID, ProjectID: command.ProjectID, ExpectedMission: command.ExpectedMission.clone(),
		ExpectedCurrentGeneration: command.ExpectedCurrentGeneration, ExpectedOld: command.ExpectedOld, Candidate: command.Candidate,
		Drift: command.Drift, ReconciliationID: command.ReconciliationID, Reconciliation: command.Reconciliation,
		OperationID: command.OperationID, ActorID: command.ActorID, GovernanceContextID: command.GovernanceContextID,
		Rationale: command.Rationale, OccurredAt: command.OccurredAt, Actions: cloneAuthorityActions(actions), RequestDigest: requestDigest,
	})
	if err != nil {
		return SuccessorPublicationResult{}, normalizeSuccessorStoreError(err)
	}
	if err := validateSuccessorResult(command, actions, requestDigest, result); err != nil {
		return SuccessorPublicationResult{}, err
	}
	return cloneSuccessorResult(result), nil
}

func validateSuccessorPublication(command SuccessorPublicationCommand) ([]AuthorityAction, string, error) {
	if _, err := ids.ParseMissionID(command.MissionID.String()); err != nil {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "mission_id", "mission identity is invalid")
	}
	if _, err := ids.ParseProjectID(command.ProjectID.String()); err != nil {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "project_id", "project identity is invalid")
	}
	if _, err := ids.ParseOperationID(command.OperationID.String()); err != nil {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "operation_id", "operation identity is invalid")
	}
	if _, err := ids.ParseOperationID(command.ReconciliationID.String()); err != nil {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "reconciliation_id", "reconciliation identity is invalid")
	}
	if _, err := ids.ParseActorID(command.ActorID.String()); err != nil {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "actor_id", "actor identity is invalid")
	}
	if _, err := ids.ParseGovernanceContextID(command.GovernanceContextID.String()); err != nil {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "governance_context_id", "governance context identity is invalid")
	}
	if !boundedPublicationText(command.Rationale, 4096) || command.OccurredAt.IsZero() || command.OccurredAt.Location() != time.UTC {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "authority", "bounded rationale and UTC occurrence time are required")
	}
	if command.ExpectedMission.Validate() != nil || command.ExpectedMission.MissionID != command.MissionID || command.ExpectedMission.ProjectID != command.ProjectID || command.ExpectedMission.Lifecycle != LifecycleTerminal || command.ExpectedMission.CurrentStep != StepWorkFinalized || command.ExpectedMission.TerminalOutcome != OutcomeWorkFinalized || command.ExpectedMission.CurrentWorkRevisionID != command.ExpectedOld.ID() {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "expected_mission", "mission must name the exact finalized predecessor")
	}
	if command.OccurredAt.Before(command.ExpectedMission.UpdatedAt) || command.ExpectedCurrentGeneration == 0 || command.ExpectedCurrentGeneration != command.ExpectedOld.Generation() {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "expected_generation", "expected current generation is invalid")
	}
	oldBytes, oldErr := strictRevisionBytes(command.ExpectedOld)
	candidateBytes, candidateErr := strictRevisionBytes(command.Candidate)
	if oldErr != nil || candidateErr != nil || len(oldBytes) == 0 || len(candidateBytes) == 0 {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "work_revision", "predecessor and successor must be normalized v2 revisions")
	}
	definition := command.Candidate.MissionDefinition()
	oldDefinition := command.ExpectedOld.MissionDefinition()
	if command.ExpectedOld.ProjectID() != command.ProjectID || command.Candidate.ProjectID() != command.ProjectID || command.ExpectedOld.MissionID() != command.MissionID || command.Candidate.MissionID() != command.MissionID || command.Candidate.Generation() != command.ExpectedOld.Generation()+1 || command.Candidate.PredecessorID() != command.ExpectedOld.ID() || definition != oldDefinition || definition.Key != command.ExpectedMission.DefinitionID.String() || definition.Version != uint64(command.ExpectedMission.DefinitionVersion) || definition.Digest != command.ExpectedMission.DefinitionCommitment.String() || command.Candidate.DependencyPolicy() != command.ExpectedOld.DependencyPolicy() || command.Candidate.FinalizedBy() != command.ActorID || command.Candidate.GovernanceContextID() != command.GovernanceContextID || !command.Candidate.FinalizedAt().Equal(command.OccurredAt) {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "successor", "successor is outside predecessor authority")
	}
	driftBytes, driftErr := command.Drift.Encode()
	if driftErr != nil || command.Drift.SemanticStatus() != workgraph.SemanticChanged || command.Drift.MissionID() != command.MissionID || command.Drift.BaseRevisionID() != command.ExpectedOld.ID() || command.Drift.CandidateRevisionID() != command.Candidate.ID() || command.Drift.BaseGraphDigest() != command.ExpectedOld.GraphDigest() || command.Drift.CandidateGraphDigest() != command.Candidate.GraphDigest() {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "drift", "changed drift evidence must bind the exact revision pair")
	}
	plan := command.Reconciliation
	if plan.ID() != command.ReconciliationID.String() || plan.DriftReportID() != command.Drift.ID() || plan.OldRevisionID() != command.ExpectedOld.ID() || plan.NewRevisionID() != command.Candidate.ID() || plan.OldGraphDigest() != command.ExpectedOld.GraphDigest() || plan.NewGraphDigest() != command.Candidate.GraphDigest() || plan.OldCriteriaDigest() != command.ExpectedOld.CriteriaDigest() || plan.NewCriteriaDigest() != command.Candidate.CriteriaDigest() || plan.ActorID() != command.ActorID || plan.Rationale() != command.Rationale || !plan.OccurredAt().Equal(command.OccurredAt) || plan.RequestDigest() == "" || len(plan.Decisions()) == 0 {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "reconciliation", "mixed reconciliation does not bind complete successor authority")
	}
	actions, actionErr := deriveAuthorityActions(command.ReconciliationID, command.ExpectedOld.ID(), command.Candidate.ID(), plan)
	if actionErr != nil {
		return nil, "", actionErr
	}
	digest, digestErr := successorCommandDigest(command, oldBytes, candidateBytes, driftBytes, actions)
	if digestErr != nil {
		return nil, "", refusal(KindInvalidRequest, RecoveryCorrectInput, "request", "successor publication cannot be committed")
	}
	return actions, digest, nil
}

func deriveAuthorityActions(reconciliationID ids.OperationID, oldID, newID ids.WorkRevisionID, plan workgraph.MixedReconciliation) ([]AuthorityAction, error) {
	actions := make([]AuthorityAction, 0)
	for _, decision := range plan.Decisions() {
		status, ok := plan.AssignedStatus(decision.OldWorkID())
		if !ok {
			return nil, refusal(KindInvalidRequest, RecoveryCorrectInput, "reconciliation", "decision lacks an assigned old-work status")
		}
		consequence, ok := plan.Consequence(decision.OldWorkID(), status)
		if !ok || !boundedPublicationText(consequence.Reason, 256) {
			return nil, refusal(KindInvalidRequest, RecoveryCorrectInput, "reconciliation", "decision lacks a bounded derived consequence")
		}
		appendAction := func(kind AuthorityActionKind) {
			actions = append(actions, AuthorityAction{Kind: kind, WorkID: decision.OldWorkID(), Status: status, Disposition: decision.Disposition(), ReconciliationID: reconciliationID, OldRevisionID: oldID, NewRevisionID: newID, Reason: consequence.Reason})
		}
		if consequence.ExpireAuthority {
			appendAction(AuthorityExpireRun)
		}
		if consequence.QuarantineWorkspace {
			appendAction(AuthorityQuarantineWorkspace)
		}
	}
	sort.Slice(actions, func(i, j int) bool {
		if actions[i].WorkID == actions[j].WorkID {
			return actions[i].Kind < actions[j].Kind
		}
		return actions[i].WorkID < actions[j].WorkID
	})
	return actions, nil
}

func successorCommandDigest(command SuccessorPublicationCommand, oldBytes, candidateBytes, driftBytes []byte, actions []AuthorityAction) (string, error) {
	type consequenceWire struct {
		Status      workgraph.WorkStatus  `json:"status"`
		Consequence workgraph.Consequence `json:"consequence"`
	}
	type decisionWire struct {
		OldWorkID    ids.WorkID            `json:"old_work_id"`
		Disposition  workgraph.Disposition `json:"disposition"`
		NewWorkIDs   []ids.WorkID          `json:"new_work_ids"`
		ForkEdges    []workgraph.ForkEdge  `json:"fork_edges"`
		Rationale    string                `json:"rationale"`
		Assigned     workgraph.WorkStatus  `json:"assigned_status"`
		Consequences []consequenceWire     `json:"consequences"`
	}
	statuses := []workgraph.WorkStatus{workgraph.StatusActive, workgraph.StatusHandedOff, workgraph.StatusApproved, workgraph.StatusIntegrated}
	decisions := make([]decisionWire, 0, len(command.Reconciliation.Decisions()))
	for _, decision := range command.Reconciliation.Decisions() {
		assigned, _ := command.Reconciliation.AssignedStatus(decision.OldWorkID())
		wire := decisionWire{OldWorkID: decision.OldWorkID(), Disposition: decision.Disposition(), NewWorkIDs: decision.NewWorkIDs(), ForkEdges: decision.ForkEdges(), Rationale: decision.Rationale(), Assigned: assigned}
		for _, status := range statuses {
			consequence, _ := command.Reconciliation.Consequence(decision.OldWorkID(), status)
			wire.Consequences = append(wire.Consequences, consequenceWire{status, consequence})
		}
		decisions = append(decisions, wire)
	}
	payload, err := json.Marshal(struct {
		MissionID            ids.MissionID           `json:"mission_id"`
		ProjectID            ids.ProjectID           `json:"project_id"`
		ExpectedMission      missionSnapshotIdentity `json:"expected_mission"`
		ExpectedGeneration   uint64                  `json:"expected_generation"`
		OldBytes             []byte                  `json:"old_revision_bytes"`
		CandidateBytes       []byte                  `json:"candidate_revision_bytes"`
		OldGraphDigest       string                  `json:"old_graph_digest"`
		NewGraphDigest       string                  `json:"new_graph_digest"`
		DriftBytes           []byte                  `json:"drift_bytes"`
		ReconciliationID     ids.OperationID         `json:"reconciliation_id"`
		ReconciliationDigest string                  `json:"reconciliation_digest"`
		Decisions            []decisionWire          `json:"decisions"`
		OperationID          ids.OperationID         `json:"operation_id"`
		ActorID              ids.ActorID             `json:"actor_id"`
		GovernanceContextID  ids.GovernanceContextID `json:"governance_context_id"`
		Rationale            string                  `json:"rationale"`
		OccurredAt           string                  `json:"occurred_at"`
		Actions              []AuthorityAction       `json:"actions"`
	}{command.MissionID, command.ProjectID, canonicalMissionSnapshotIdentity(command.ExpectedMission), command.ExpectedCurrentGeneration, oldBytes, candidateBytes, command.ExpectedOld.GraphDigest(), command.Candidate.GraphDigest(), driftBytes, command.ReconciliationID, command.Reconciliation.RequestDigest(), decisions, command.OperationID, command.ActorID, command.GovernanceContextID, command.Rationale, command.OccurredAt.Format(time.RFC3339Nano), actions})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("spec-kitty/successor-publication/v1\x00"), payload...))
	return hex.EncodeToString(sum[:]), nil
}

func validateSuccessorResult(command SuccessorPublicationCommand, actions []AuthorityAction, requestDigest string, result SuccessorPublicationResult) error {
	expectedMission := command.ExpectedMission.clone()
	expectedMission.CurrentWorkRevisionID = command.Candidate.ID()
	expectedMission.StateVersion++
	expectedMission.UpdatedAt = command.OccurredAt
	wantRevision, _ := command.Candidate.Encode()
	gotRevision, revisionErr := result.Revision.Encode()
	wantDrift, _ := command.Drift.Encode()
	gotDrift, driftErr := result.Drift.Encode()
	if revisionErr != nil || driftErr != nil || !bytes.Equal(wantRevision, gotRevision) || !bytes.Equal(wantDrift, gotDrift) || !sameMixedReconciliation(command.Reconciliation, result.Reconciliation) || result.Mission.Validate() != nil || !sameMissionSnapshot(expectedMission, result.Mission) || !sameAuthorityActions(actions, result.Actions) || result.RequestDigest != requestDigest {
		return refusal(KindPersistenceContract, RecoveryRepairStoredState, "store.publish", "store returned an outcome outside publication authority")
	}
	return nil
}

func strictRevisionBytes(revision workgraph.RevisionV2) ([]byte, error) {
	encoded, err := revision.Encode()
	if err != nil {
		return nil, err
	}
	_, err = workgraph.DecodeRevisionV2(encoded)
	return encoded, err
}

func sameMixedReconciliation(left, right workgraph.MixedReconciliation) bool {
	return left.ID() == right.ID() && left.RequestDigest() == right.RequestDigest() && left.DriftReportID() == right.DriftReportID() && left.OldRevisionID() == right.OldRevisionID() && left.NewRevisionID() == right.NewRevisionID()
}
func sameAuthorityActions(left, right []AuthorityAction) bool {
	left, right = cloneAuthorityActions(left), cloneAuthorityActions(right)
	sort.Slice(left, func(i, j int) bool {
		if left[i].WorkID == left[j].WorkID {
			return left[i].Kind < left[j].Kind
		}
		return left[i].WorkID < left[j].WorkID
	})
	sort.Slice(right, func(i, j int) bool {
		if right[i].WorkID == right[j].WorkID {
			return right[i].Kind < right[j].Kind
		}
		return right[i].WorkID < right[j].WorkID
	})
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func cloneAuthorityActions(input []AuthorityAction) []AuthorityAction {
	return append([]AuthorityAction(nil), input...)
}
func cloneSuccessorResult(result SuccessorPublicationResult) SuccessorPublicationResult {
	result.Mission = result.Mission.clone()
	result.Actions = cloneAuthorityActions(result.Actions)
	return result
}
func normalizeSuccessorStoreError(err error) error {
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return refusal(KindPersistenceUnavailable, RecoveryRetryPersistence, "store", "successor publication outcome is unavailable")
}
func boundedPublicationText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
