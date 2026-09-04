package handoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
	"unicode/utf8"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

const (
	HandoffEventSchemaV2  = uint32(1)
	HandoffReducerV2      = uint32(1)
	handoffResultSchemaV2 = uint32(1)
)

type StageV2 string

const (
	StageSubmissionRequestedV2 StageV2 = "submission_requested"
	StageRepositoryObservedV2  StageV2 = "repository_observed"
	StageCandidateRecordedV2   StageV2 = "candidate_recorded"
	StageCandidateReplacedV2   StageV2 = "candidate_replaced"
	StageCandidateRefusedV2    StageV2 = "candidate_refused"
)

// CandidateReplacementV2 is the immutable lineage carried by a successor
// handoff. The prior state remains in the journal; PredecessorStateDigest binds
// the successor to those exact bytes without embedding an unbounded history.
type CandidateReplacementV2 struct {
	PredecessorHandoffID      ids.HandoffID           `json:"predecessor_handoff_id"`
	PredecessorCandidate      string                  `json:"predecessor_candidate"`
	PredecessorLogicalVersion uint64                  `json:"predecessor_logical_version"`
	PredecessorStateDigest    string                  `json:"predecessor_state_digest"`
	Reason                    string                  `json:"reason"`
	OperationID               journalv2.OperationID   `json:"operation_id"`
	ActorID                   ids.ActorID             `json:"actor_id"`
	ProcessID                 ids.ProcessID           `json:"process_id"`
	GovernanceContextID       ids.GovernanceContextID `json:"governance_context_id"`
}

func (r CandidateReplacementV2) validate() error {
	if _, err := ids.ParseHandoffID(r.PredecessorHandoffID.String()); err != nil {
		return authorityErrorV2(KindInvalid, "replacement_handoff", err)
	}
	if !ValidCommit(r.PredecessorCandidate) || r.PredecessorLogicalVersion == 0 || !ValidDigest(r.PredecessorStateDigest) || !validBoundedTextV2(r.Reason, 1024) {
		return authorityErrorV2(KindInvalid, "replacement_lineage", nil)
	}
	for _, parse := range []func() error{
		func() error { _, e := journalv2.NewOperationID(string(r.OperationID)); return e },
		func() error { _, e := ids.ParseActorID(r.ActorID.String()); return e },
		func() error { _, e := ids.ParseProcessID(r.ProcessID.String()); return e },
		func() error { _, e := ids.ParseGovernanceContextID(r.GovernanceContextID.String()); return e },
	} {
		if err := parse(); err != nil {
			return authorityErrorV2(KindInvalid, "replacement_authority", err)
		}
	}
	return nil
}

type StateV2 struct {
	SchemaVersion              uint16                          `json:"schema_version"`
	LogicalVersion             uint64                          `json:"logical_version"`
	Stage                      StageV2                         `json:"stage"`
	HandoffID                  ids.HandoffID                   `json:"handoff_id"`
	ProjectID                  ids.ProjectID                   `json:"project_id"`
	MissionID                  ids.MissionID                   `json:"mission_id"`
	WorkRevisionID             ids.WorkRevisionID              `json:"work_revision_id"`
	WorkID                     ids.WorkID                      `json:"work_id"`
	RunID                      ids.RunID                       `json:"run_id"`
	RuntimeEpochID             journalv2.RuntimeEpochID        `json:"runtime_epoch_id"`
	WorkspaceID                ids.WorkspaceID                 `json:"workspace_id"`
	LeaseID                    ids.LeaseID                     `json:"lease_id"`
	LeaseGeneration            uint64                          `json:"lease_generation"`
	Fence                      string                          `json:"fence"`
	ActorID                    ids.ActorID                     `json:"actor_id"`
	ProcessID                  ids.ProcessID                   `json:"process_id"`
	RepositoryID               ids.RepositoryID                `json:"repository_id"`
	LaneID                     ids.LaneID                      `json:"lane_id"`
	OwnedBranch                string                          `json:"owned_branch"`
	PinnedBase                 string                          `json:"pinned_base"`
	Candidate                  string                          `json:"candidate"`
	Contributors               []ids.ActorID                   `json:"contributors"`
	ContributorDigest          string                          `json:"contributor_digest"`
	CriterionIDs               []string                        `json:"criterion_ids"`
	CriteriaDigest             string                          `json:"criteria_digest"`
	WorkRevisionCriteriaDigest string                          `json:"work_revision_criteria_digest"`
	EvidenceDigest             string                          `json:"evidence_digest"`
	RepositoryEvidence         CandidateEvidence               `json:"repository_evidence"`
	Refusal                    *CandidateRefusalV2             `json:"refusal,omitempty"`
	Replacement                *CandidateReplacementV2         `json:"replacement,omitempty"`
	Governance                 execution.ExecutionGovernanceV2 `json:"governance"`
	ObservedAt                 string                          `json:"observed_at"`
}

func (s StateV2) Validate() error {
	if s.SchemaVersion != 1 || s.LogicalVersion == 0 || s.Stage == "" || s.Governance.ContextID == "" {
		return authorityErrorV2(KindInvalid, "handoff_state", nil)
	}
	governance, governanceErr := execution.ExecutionGovernanceFromContextV2(s.Governance.Context)
	if governanceErr != nil || !bytes.Equal(mustCanonicalV2(governance), mustCanonicalV2(s.Governance)) {
		return authorityErrorV2(KindInvalid, "handoff_governance", governanceErr)
	}
	for _, parse := range []func() error{
		func() error { _, e := ids.ParseHandoffID(s.HandoffID.String()); return e },
		func() error { _, e := ids.ParseProjectID(s.ProjectID.String()); return e },
		func() error { _, e := ids.ParseMissionID(s.MissionID.String()); return e },
		func() error { _, e := ids.ParseWorkRevisionID(s.WorkRevisionID.String()); return e },
		func() error { _, e := ids.ParseWorkID(s.WorkID.String()); return e },
		func() error { _, e := ids.ParseRunID(s.RunID.String()); return e },
		func() error { _, e := ids.ParseWorkspaceID(s.WorkspaceID.String()); return e },
		func() error { _, e := ids.ParseLeaseID(s.LeaseID.String()); return e },
		func() error { _, e := ids.ParseActorID(s.ActorID.String()); return e },
		func() error { _, e := ids.ParseProcessID(s.ProcessID.String()); return e },
		func() error { _, e := ids.ParseRepositoryID(s.RepositoryID.String()); return e },
		func() error { _, e := ids.ParseLaneID(s.LaneID.String()); return e },
		func() error { _, e := journalv2.NewRuntimeEpochID(string(s.RuntimeEpochID)); return e },
	} {
		if err := parse(); err != nil {
			return authorityErrorV2(KindInvalid, "handoff_identity", err)
		}
	}
	if s.LeaseGeneration == 0 || len(s.Fence) < 16 || len(s.Fence) > journalv2.MaxIdentityBytes || !ValidCommit(s.Candidate) || !ValidCommit(s.PinnedBase) || !ValidDigest(s.CriteriaDigest) || !ValidDigest(s.WorkRevisionCriteriaDigest) || !ValidDigest(s.EvidenceDigest) || !ValidDigest(s.ContributorDigest) || !validBoundedTextV2(s.OwnedBranch, journalv2.MaxIdentityBytes) {
		return authorityErrorV2(KindInvalid, "handoff_evidence", nil)
	}
	criteria, err := CanonicalCriterionIDsV2(s.CriterionIDs)
	if err != nil || !stringSlicesEqualV2(criteria, s.CriterionIDs) || criterionSetDigestV2(criteria) != s.CriteriaDigest {
		return authorityErrorV2(KindInvalid, "criteria", err)
	}
	contributors, err := CanonicalContributors(s.Contributors)
	if err != nil || len(contributors) == 0 || !actorSlicesEqualV2(contributors, s.Contributors) || contributorDigestV2(contributors) != s.ContributorDigest {
		return authorityErrorV2(KindInvalid, "contributors", err)
	}
	foundSubmitter := false
	for _, contributor := range contributors {
		foundSubmitter = foundSubmitter || contributor == s.ActorID
	}
	if !foundSubmitter {
		return authorityErrorV2(KindInvalid, "contributors", nil)
	}
	if _, err := time.Parse(time.RFC3339Nano, s.ObservedAt); err != nil {
		return authorityErrorV2(KindInvalid, "observed_at", err)
	}
	zeroEvidence := s.RepositoryEvidence == (CandidateEvidence{})
	if s.Replacement != nil {
		if s.Replacement.validate() != nil || s.Replacement.OperationID == "" || s.Replacement.ActorID != s.ActorID || s.Replacement.ProcessID != s.ProcessID || s.Replacement.GovernanceContextID != s.Governance.ContextID || s.Replacement.PredecessorHandoffID == s.HandoffID || s.Replacement.PredecessorCandidate == s.Candidate {
			return authorityErrorV2(KindInvalid, "replacement", nil)
		}
	}
	switch s.Stage {
	case StageSubmissionRequestedV2:
		if !zeroEvidence || s.Refusal != nil || s.Replacement != nil {
			return authorityErrorV2(KindInvalid, "repository_evidence", nil)
		}
	case StageCandidateReplacedV2:
		if !zeroEvidence || s.Refusal != nil || s.Replacement == nil {
			return authorityErrorV2(KindInvalid, "replacement", nil)
		}
	case StageRepositoryObservedV2, StageCandidateRecordedV2:
		if s.Refusal != nil || s.RepositoryEvidence.Candidate != s.Candidate || s.RepositoryEvidence.PinnedBase != s.PinnedBase || s.RepositoryEvidence.OwnedBranch != s.OwnedBranch || s.RepositoryEvidence.ObservedBranchHead != s.Candidate {
			return authorityErrorV2(KindCandidateMismatch, "repository_evidence", nil)
		}
	case StageCandidateRefusedV2:
		if !zeroEvidence || s.Refusal == nil || s.Refusal.Validate(s) != nil {
			return authorityErrorV2(KindInvalid, "candidate_refusal", nil)
		}
	default:
		return authorityErrorV2(KindInvalid, "handoff_stage", nil)
	}
	return nil
}

type CandidateRefusalV2 struct {
	Kind                 ErrorKind `json:"kind"`
	Candidate            string    `json:"candidate"`
	OwnedBranch          string    `json:"owned_branch"`
	ObservedBranchHead   string    `json:"observed_branch_head,omitempty"`
	DivergenceKind       string    `json:"divergence_kind,omitempty"`
	EvidenceRefRoot      string    `json:"evidence_ref_root,omitempty"`
	EvidenceCandidateRef string    `json:"evidence_candidate_ref,omitempty"`
	EvidenceObservedRef  string    `json:"evidence_observed_ref,omitempty"`
}

func (r CandidateRefusalV2) Validate(state StateV2) error {
	if r.Kind != KindCandidateMismatch || r.Candidate != state.Candidate || r.OwnedBranch != state.OwnedBranch || !ValidCommit(r.Candidate) || !validBoundedTextV2(r.OwnedBranch, journalv2.MaxIdentityBytes) {
		return authorityErrorV2(KindInvalid, "candidate_refusal", nil)
	}
	for _, value := range []string{r.DivergenceKind, r.EvidenceRefRoot, r.EvidenceCandidateRef, r.EvidenceObservedRef} {
		if value != "" && (!utf8.ValidString(value) || len(value) > 1024) {
			return authorityErrorV2(KindInvalid, "candidate_refusal", nil)
		}
	}
	if r.ObservedBranchHead != "" && !ValidCommit(r.ObservedBranchHead) {
		return authorityErrorV2(KindInvalid, "candidate_refusal", nil)
	}
	return nil
}

func refusalFromErrorV2(state StateV2, err error) CandidateRefusalV2 {
	result := CandidateRefusalV2{Kind: KindCandidateMismatch, Candidate: state.Candidate, OwnedBranch: state.OwnedBranch}
	var typed *Error
	if errors.As(err, &typed) {
		result.Kind = typed.Kind
		result.ObservedBranchHead = boundedFactV2(typed.Observed["branch_head"], 40)
		result.DivergenceKind = boundedFactV2(typed.Observed["divergence_kind"], 128)
		result.EvidenceRefRoot = boundedFactV2(typed.Observed["evidence_ref_root"], 1024)
		result.EvidenceCandidateRef = boundedFactV2(typed.Observed["evidence_candidate_ref"], 1024)
		result.EvidenceObservedRef = boundedFactV2(typed.Observed["evidence_observed_ref"], 1024)
	}
	if result.Kind != KindCandidateMismatch {
		result.Kind = KindCandidateMismatch
	}
	return result
}

func boundedFactV2(value string, max int) string {
	if len(value) > max || !utf8.ValidString(value) {
		return ""
	}
	return value
}

func refusalErrorV2(state StateV2) error {
	if state.Refusal == nil {
		return authorityErrorV2(KindCandidateMismatch, "candidate_refused", nil)
	}
	return &Error{Kind: state.Refusal.Kind, Message: "handoff candidate was immutably refused", Observed: map[string]string{"candidate": state.Refusal.Candidate, "branch_head": state.Refusal.ObservedBranchHead, "divergence_kind": state.Refusal.DivergenceKind, "evidence_ref_root": state.Refusal.EvidenceRefRoot}, Expected: map[string]string{"candidate": state.Refusal.Candidate, "branch_head": state.Refusal.Candidate}}
}

func (s StateV2) CanonicalBytes() []byte { b, _ := encodeStateV2(s); return b }

func encodeStateV2(s StateV2) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(raw)
}

func DecodeStateV2(encoded []byte) (StateV2, error) {
	var state StateV2
	if err := strictJSONV2(encoded, &state); err != nil {
		return StateV2{}, err
	}
	canonical, err := encodeStateV2(state)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return StateV2{}, authorityErrorV2(KindInvalid, "handoff_state", err)
	}
	state.Contributors = append([]ids.ActorID(nil), state.Contributors...)
	state.CriterionIDs = append([]string(nil), state.CriterionIDs...)
	return state, nil
}

type EventV2 struct {
	SchemaVersion uint16  `json:"schema_version"`
	Kind          string  `json:"kind"`
	PriorDigest   string  `json:"prior_digest,omitempty"`
	State         StateV2 `json:"state"`
}

func encodeEventV2(event EventV2) ([]byte, error) {
	if event.SchemaVersion != 1 || !validBoundedTextV2(event.Kind, 64) || event.State.Validate() != nil {
		return nil, authorityErrorV2(KindInvalid, "handoff_event", nil)
	}
	raw, _ := json.Marshal(event)
	return journalv2.CanonicalObject(raw)
}

func DecodeEventV2(encoded []byte) (EventV2, error) {
	var event EventV2
	if err := strictJSONV2(encoded, &event); err != nil {
		return EventV2{}, err
	}
	canonical, err := encodeEventV2(event)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return EventV2{}, authorityErrorV2(KindInvalid, "handoff_event", err)
	}
	return event, nil
}

func CoordinateV2(projectID ids.ProjectID, missionID ids.MissionID, revisionID ids.WorkRevisionID, workID ids.WorkID) (journalv2.AggregateCoordinate, error) {
	return journalv2.NewAggregateCoordinate("handoff_slot", journalv2.AggregateID(workID), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(projectID), MissionID: journalv2.MissionID(missionID), WorkRevisionID: journalv2.WorkRevisionID(revisionID), WorkID: journalv2.WorkID(workID)})
}

func ReduceEventV2(prior []byte, envelope journalv2.EventEnvelopeV2) ([]byte, error) {
	if envelope.Family() != "handoff_slot" || envelope.EventSchemaVersion() != HandoffEventSchemaV2 || envelope.ReducerVersion() != HandoffReducerV2 {
		return nil, authorityErrorV2(KindInvalid, "handoff_tuple", nil)
	}
	event, err := DecodeEventV2(envelope.Payload())
	if err != nil {
		return nil, err
	}
	next := event.State
	coordinate, err := CoordinateV2(next.ProjectID, next.MissionID, next.WorkRevisionID, next.WorkID)
	if err != nil || coordinate != envelope.Coordinate() || envelope.LogicalVersion() != journalv2.LogicalVersion(next.LogicalVersion) || envelope.EventType() != "handoff_slot."+event.Kind || envelope.ActorID() != journalv2.ActorID(next.ActorID) || envelope.ProcessID() != journalv2.ProcessID(next.ProcessID) || envelope.CommittedRuntimeEpochID() != next.RuntimeEpochID || envelope.WorkRevisionID() != journalv2.WorkRevisionID(next.WorkRevisionID) || envelope.PolicyRevision() != next.Governance.PolicyRevision {
		return nil, authorityErrorV2(KindStaleAuthority, "handoff_event_authority", err)
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.LogicalVersion != 1 || next.Stage != StageSubmissionRequestedV2 || event.Kind != string(StageSubmissionRequestedV2) || event.PriorDigest != "" {
			return nil, authorityErrorV2(KindConflict, "handoff_initial", nil)
		}
		return next.CanonicalBytes(), nil
	}
	previous, err := DecodeStateV2(prior)
	if err != nil || next.LogicalVersion != previous.LogicalVersion+1 {
		return nil, authorityErrorV2(KindConflict, "handoff_transition", err)
	}
	digest, _ := journalv2.ReducerStateDigestV2(previous.CanonicalBytes())
	if event.PriorDigest != digest.String() {
		return nil, authorityErrorV2(KindConflict, "handoff_prior", nil)
	}
	replacementStart := (previous.Stage == StageCandidateRecordedV2 || previous.Stage == StageCandidateRefusedV2) && next.Stage == StageCandidateReplacedV2 && validReplacementTransitionV2(previous, next)
	continued := sameImmutableHandoffV2(previous, next) && ((previous.Stage == StageSubmissionRequestedV2 || previous.Stage == StageCandidateReplacedV2) && (next.Stage == StageRepositoryObservedV2 || next.Stage == StageCandidateRefusedV2) || previous.Stage == StageRepositoryObservedV2 && next.Stage == StageCandidateRecordedV2)
	allowed := replacementStart || continued
	if !allowed || event.Kind != string(next.Stage) {
		return nil, authorityErrorV2(KindConflict, "handoff_transition", nil)
	}
	return next.CanonicalBytes(), nil
}

func ReducerRegistrationsV2() []journalv2.ReducerRegistration {
	sum := sha256.Sum256([]byte("spec-kitty/handoff-reducer/1.1/replacement-lineage"))
	digest, _ := journalv2.ParseDigest("handoff_reducer", hex.EncodeToString(sum[:]))
	return []journalv2.ReducerRegistration{{Family: "handoff_slot", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/handoff", ImplementationDigest: digest, Reduce: ReduceEventV2}}
}

type AuthorityRequestV2 struct {
	OperationID                journalv2.OperationID
	HandoffID                  ids.HandoffID
	ProjectID                  ids.ProjectID
	MissionID                  ids.MissionID
	WorkRevisionID             ids.WorkRevisionID
	WorkID                     ids.WorkID
	RunID                      ids.RunID
	RuntimeEpochID             journalv2.RuntimeEpochID
	Workspace                  workspace.BindingV2
	LeaseID                    ids.LeaseID
	LeaseGeneration            uint64
	Fence                      string
	ActorID                    ids.ActorID
	ProcessID                  ids.ProcessID
	RepositoryID               ids.RepositoryID
	RepositoryPath             string
	LaneID                     ids.LaneID
	OwnedBranch                string
	PinnedBase                 string
	Candidate                  string
	Contributors               []ids.ActorID
	CriterionIDs               []string
	CriteriaDigest             string
	WorkRevisionCriteriaDigest string
	EvidenceDigest             string
	Replacement                *CandidateReplacementRequestV2
	Governance                 execution.ExecutionGovernanceV2
	ObservedAt                 time.Time
}

// CandidateReplacementRequestV2 binds a new operation to one exact terminal
// predecessor. The service verifies this lineage against the current handoff
// head before publishing the replacement transition.
type CandidateReplacementRequestV2 struct {
	PredecessorHandoffID      ids.HandoffID
	PredecessorCandidate      string
	PredecessorLogicalVersion uint64
	PredecessorStateDigest    string
	Reason                    string
}

type OperationResolutionKindV2 uint8

const (
	OperationAbsentV2 OperationResolutionKindV2 = iota + 1
	OperationExactV2
	OperationConflictV2
)

type OperationResolutionV2 struct {
	Kind           OperationResolutionKindV2
	State          journalv2.OperationStateV2
	RecordRevision uint64
}
type ObservationV2 struct {
	State             *StateV2
	RecordRevision    uint64
	LastPublicationID journalv2.PublicationID
}
type AuthorityObservationV2 struct {
	Eligibility  workgraph.ObservedEligibilityShardV2
	Execution    execution.ExecutionSlotObservationV2
	Workspace    execution.WorkspaceAuthorityObservationV2
	WorkRevision WorkRevisionObservationV2
	Assertions   []journalv2.AuthorityAssertionV2
}

// WorkRevisionObservationV2 is the provider-neutral current immutable graph
// authority consumed by handoff and review.
type WorkRevisionObservationV2 struct {
	Revision       workgraph.RevisionV2
	RecordRevision uint64
	Assertion      journalv2.AuthorityAssertionV2
}

type AuthorityPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p AuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, authorityErrorV2(KindInvalid, "handoff_plan", nil)
	}
	return p.sealed, nil
}

type AuthorityAckV2 struct {
	PublicationID    journalv2.PublicationID
	ProviderRevision uint64
	PlanDigest       journalv2.Digest
	MutationCount    int
}

type AuthorityPortV2 interface {
	ResolveHandoffOperation(context.Context, journalv2.OperationBindingV2) (OperationResolutionV2, error)
	ObserveHandoff(context.Context, journalv2.AggregateCoordinate) (ObservationV2, error)
	ObserveHandoffAuthority(context.Context, AuthorityRequestV2) (AuthorityObservationV2, error)
	PublishHandoff(context.Context, AuthorityPlanV2) (AuthorityAckV2, error)
	ReconcileHandoff(context.Context, AuthorityPlanV2) (AuthorityAckV2, error)
}
type RuntimeEpochSourceV2 interface {
	CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error)
}

type AuthorityResultV2 struct {
	State         StateV2
	PublicationID journalv2.PublicationID
	Replayed      bool
}

type AuthorityServiceV2 struct {
	port     AuthorityPortV2
	epochs   RuntimeEpochSourceV2
	verifier CandidateVerifier
	registry journalv2.EventRegistryV2
}

func NewAuthorityServiceV2(port AuthorityPortV2, epochs RuntimeEpochSourceV2, verifier CandidateVerifier, registry journalv2.EventRegistryV2) (*AuthorityServiceV2, error) {
	if port == nil || epochs == nil || verifier == nil {
		return nil, authorityErrorV2(KindInvalid, "handoff_dependencies", nil)
	}
	if _, err := registry.Resolve("handoff_slot", 1, 1); err != nil {
		return nil, err
	}
	return &AuthorityServiceV2{port, epochs, verifier, registry}, nil
}

func (s *AuthorityServiceV2) Submit(ctx context.Context, request AuthorityRequestV2) (AuthorityResultV2, error) {
	binding, seed, err := prepareRequestV2(request)
	if err != nil {
		return AuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveHandoffOperation(ctx, binding)
	if err != nil {
		return AuthorityResultV2{}, authorityErrorV2(KindConflict, "operation_resolution", err)
	}
	if resolution.Kind == OperationConflictV2 {
		return AuthorityResultV2{}, authorityErrorV2(KindIdempotencyConflict, "operation_id", nil)
	}
	coordinate, _ := CoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID)
	var current *StateV2
	var handoffRevision uint64
	var operation *journalv2.OperationStateV2
	var operationRevision uint64
	if resolution.Kind == OperationExactV2 {
		if resolution.State.Status() == journalv2.StatusResolved || resolution.State.Status() == journalv2.StatusRefused {
			state, decodeErr := DecodeStateV2(resolution.State.Result())
			if decodeErr != nil || (state.Stage != StageCandidateRecordedV2 && state.Stage != StageCandidateRefusedV2) || !sameImmutableHandoffV2(seed, state) {
				return AuthorityResultV2{}, authorityErrorV2(KindConflict, "operation_result", decodeErr)
			}
			result := AuthorityResultV2{State: state, PublicationID: resolution.State.LastPublicationID(), Replayed: true}
			if state.Stage == StageCandidateRefusedV2 {
				return result, refusalErrorV2(state)
			}
			return result, nil
		}
		observed, observeErr := s.port.ObserveHandoff(ctx, coordinate)
		if observeErr != nil || observed.State == nil {
			return AuthorityResultV2{}, authorityErrorV2(KindConflict, "handoff_resume", observeErr)
		}
		current, handoffRevision, operation, operationRevision = observed.State, observed.RecordRevision, &resolution.State, resolution.RecordRevision
	} else if resolution.Kind != OperationAbsentV2 {
		return AuthorityResultV2{}, authorityErrorV2(KindConflict, "operation_resolution", nil)
	}
	if resolution.Kind == OperationAbsentV2 && request.Replacement != nil {
		observed, observeErr := s.port.ObserveHandoff(ctx, coordinate)
		if observeErr != nil || observed.State == nil || !replacementMatchesPriorV2(*request.Replacement, *observed.State) {
			return AuthorityResultV2{}, authorityErrorV2(KindConflict, "replacement_lineage", observeErr)
		}
		current, handoffRevision = observed.State, observed.RecordRevision
	}
	for {
		if current != nil && current.Stage == StageCandidateRecordedV2 && request.Replacement == nil {
			return AuthorityResultV2{State: *current, PublicationID: operation.LastPublicationID(), Replayed: true}, nil
		}
		if epoch, epochErr := s.epochs.CurrentRuntimeEpoch(ctx); epochErr != nil || epoch != request.RuntimeEpochID {
			return AuthorityResultV2{}, authorityErrorV2(KindStaleAuthority, "runtime_epoch", epochErr)
		}
		authority, observeErr := s.port.ObserveHandoffAuthority(ctx, request)
		if observeErr != nil {
			return AuthorityResultV2{}, authorityErrorV2(KindStaleAuthority, "authority", observeErr)
		}
		eligibility, gateErr := validateAuthorityV2(request, authority)
		if gateErr != nil {
			return AuthorityResultV2{}, gateErr
		}
		next := seed
		next.LogicalVersion = 1
		next.Stage = StageSubmissionRequestedV2
		if current != nil {
			next.LogicalVersion = current.LogicalVersion + 1
			switch current.Stage {
			case StageCandidateRecordedV2, StageCandidateRefusedV2:
				if request.Replacement == nil || !replacementMatchesPriorV2(*request.Replacement, *current) {
					return AuthorityResultV2{}, authorityErrorV2(KindConflict, "replacement_lineage", nil)
				}
				next.Stage = StageCandidateReplacedV2
			case StageSubmissionRequestedV2:
				scope := AuthorityScope{RepositoryID: request.RepositoryID, RepositoryPath: request.RepositoryPath, OwnedBranch: request.OwnedBranch, PinnedBase: request.PinnedBase, LaneID: request.LaneID, CriteriaDigest: request.CriteriaDigest, OperationID: ids.OperationID(request.OperationID), HandoffID: request.HandoffID, WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID}
				evidence, verifyErr := VerifyCandidateAuthority(ctx, s.verifier, scope, request.Candidate)
				if verifyErr != nil {
					next.Stage = StageCandidateRefusedV2
					refusal := refusalFromErrorV2(next, verifyErr)
					next.Refusal = &refusal
					break
				}
				next.Stage, next.RepositoryEvidence = StageRepositoryObservedV2, evidence
			case StageCandidateReplacedV2:
				scope := AuthorityScope{RepositoryID: request.RepositoryID, RepositoryPath: request.RepositoryPath, OwnedBranch: request.OwnedBranch, PinnedBase: request.PinnedBase, LaneID: request.LaneID, CriteriaDigest: request.CriteriaDigest, OperationID: ids.OperationID(request.OperationID), HandoffID: request.HandoffID, WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID}
				evidence, verifyErr := VerifyCandidateAuthority(ctx, s.verifier, scope, request.Candidate)
				if verifyErr != nil {
					next.Stage = StageCandidateRefusedV2
					refusal := refusalFromErrorV2(next, verifyErr)
					next.Refusal = &refusal
					break
				}
				next.Stage, next.RepositoryEvidence = StageRepositoryObservedV2, evidence
			case StageRepositoryObservedV2:
				next.Stage, next.RepositoryEvidence = StageCandidateRecordedV2, current.RepositoryEvidence
			default:
				return AuthorityResultV2{}, authorityErrorV2(KindConflict, "handoff_stage", nil)
			}
		}
		plan, planErr := buildPlanV2(request, binding, current, handoffRevision, operation, operationRevision, next, authority, eligibility, s.registry)
		if planErr != nil {
			return AuthorityResultV2{}, planErr
		}
		ack, publishErr := s.port.PublishHandoff(ctx, plan)
		sealed, _ := plan.Publication()
		if publishErr != nil {
			ack, publishErr = s.port.ReconcileHandoff(ctx, plan)
			if publishErr != nil {
				return AuthorityResultV2{}, authorityErrorV2(KindConflict, "handoff_publication", publishErr)
			}
		}
		if ack.ProviderRevision == 0 || ack.PublicationID != sealed.PublicationID() || ack.PlanDigest != sealed.Digest() || ack.MutationCount != sealed.MutationCount() {
			return AuthorityResultV2{}, authorityErrorV2(KindConflict, "handoff_publication", publishErr)
		}
		nextCopy := next
		current = &nextCopy
		handoffRevision = ack.ProviderRevision
		state := sealed.OperationState()
		operation, operationRevision = &state, ack.ProviderRevision
		if next.Stage == StageCandidateRecordedV2 {
			return AuthorityResultV2{State: next, PublicationID: ack.PublicationID}, nil
		}
		if next.Stage == StageCandidateRefusedV2 {
			return AuthorityResultV2{State: next, PublicationID: ack.PublicationID}, refusalErrorV2(next)
		}
	}
}

func prepareRequestV2(r AuthorityRequestV2) (journalv2.OperationBindingV2, StateV2, error) {
	contributors, err := CanonicalContributors(r.Contributors)
	if err != nil || len(contributors) == 0 || r.ObservedAt.IsZero() || r.Workspace.Validate() != nil {
		return journalv2.OperationBindingV2{}, StateV2{}, authorityErrorV2(KindInvalid, "handoff_request", err)
	}
	criteria, criteriaErr := CanonicalCriterionIDsV2(r.CriterionIDs)
	if criteriaErr != nil || criterionSetDigestV2(criteria) != r.CriteriaDigest || !ValidDigest(r.WorkRevisionCriteriaDigest) {
		return journalv2.OperationBindingV2{}, StateV2{}, authorityErrorV2(KindInvalid, "handoff_criteria", criteriaErr)
	}
	state := StateV2{SchemaVersion: 1, LogicalVersion: 1, Stage: StageSubmissionRequestedV2, HandoffID: r.HandoffID, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, RunID: r.RunID, RuntimeEpochID: r.RuntimeEpochID, WorkspaceID: r.Workspace.WorkspaceID, LeaseID: r.LeaseID, LeaseGeneration: r.LeaseGeneration, Fence: r.Fence, ActorID: r.ActorID, ProcessID: r.ProcessID, RepositoryID: r.RepositoryID, LaneID: r.LaneID, OwnedBranch: r.OwnedBranch, PinnedBase: r.PinnedBase, Candidate: r.Candidate, Contributors: contributors, ContributorDigest: contributorDigestV2(contributors), CriterionIDs: criteria, CriteriaDigest: r.CriteriaDigest, WorkRevisionCriteriaDigest: r.WorkRevisionCriteriaDigest, EvidenceDigest: r.EvidenceDigest, Governance: r.Governance, ObservedAt: r.ObservedAt.UTC().Format(time.RFC3339Nano)}
	if r.Replacement != nil {
		state.Stage = StageCandidateReplacedV2
		state.Replacement = &CandidateReplacementV2{PredecessorHandoffID: r.Replacement.PredecessorHandoffID, PredecessorCandidate: r.Replacement.PredecessorCandidate, PredecessorLogicalVersion: r.Replacement.PredecessorLogicalVersion, PredecessorStateDigest: r.Replacement.PredecessorStateDigest, Reason: r.Replacement.Reason, OperationID: r.OperationID, ActorID: r.ActorID, ProcessID: r.ProcessID, GovernanceContextID: r.Governance.ContextID}
	}
	if err := state.Validate(); err != nil {
		return journalv2.OperationBindingV2{}, StateV2{}, err
	}
	coordinate, _ := CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	intent, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "handoff.submit", CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: state.CanonicalBytes()})
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: r.OperationID, CommandKind: "handoff.submit", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: coordinate.Scope, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), DoctrineRevision: r.Governance.DoctrineRevision, ProfileRevision: r.Governance.ProfileRevision, PolicyRevision: r.Governance.PolicyRevision, OriginatingRuntimeEpochID: r.RuntimeEpochID, ObservedCreatedAt: r.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
	return binding, state, err
}

func validateAuthorityV2(r AuthorityRequestV2, observed AuthorityObservationV2) (journalv2.EligibilityGuardTransition, error) {
	token, err := observed.Eligibility.Token(r.WorkID, r.WorkRevisionID, workgraph.EligibilityHandoff)
	if err != nil {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindRevisionIneligible, "eligibility", err)
	}
	if observed.Execution.State == nil || observed.Execution.RecordRevision == 0 {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindStaleAuthority, "execution", nil)
	}
	e := *observed.Execution.State
	if e.ProjectID != r.ProjectID || e.MissionID != r.MissionID || e.WorkRevisionID != r.WorkRevisionID || e.WorkID != r.WorkID || e.RunID != r.RunID || e.RuntimeEpochID != r.RuntimeEpochID || e.WorkspaceID != r.Workspace.WorkspaceID || e.LeaseID != r.LeaseID || e.Generation != r.LeaseGeneration || e.Fence != r.Fence || e.ActorID != r.ActorID || e.ProcessID != r.ProcessID || e.Status != execution.ExecutionActiveV2 || !sameGovernanceV2(e.Governance, r.Governance) {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindStaleAuthority, "execution", nil)
	}
	deadline, _ := time.Parse(time.RFC3339Nano, e.Deadline)
	if !r.ObservedAt.Before(deadline) || observed.Workspace.State == nil || observed.Workspace.RecordRevision == 0 {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindStaleAuthority, "lease_or_workspace", nil)
	}
	capsule, err := workspace.NewAuthorityCapsuleV2(r.Workspace)
	if err != nil || workspace.ValidateCurrentV2(*observed.Workspace.State, capsule) != nil {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindStaleAuthority, "workspace", err)
	}
	criteria, revisionDigest, revisionErr := criterionAuthorityV2(observed.WorkRevision.Revision, r.WorkID)
	if revisionErr != nil || observed.WorkRevision.RecordRevision == 0 || observed.WorkRevision.Revision.ID() != r.WorkRevisionID || observed.WorkRevision.Revision.ProjectID() != r.ProjectID || observed.WorkRevision.Revision.MissionID() != r.MissionID || revisionDigest != r.WorkRevisionCriteriaDigest || !stringSlicesEqualV2(criteria, mustCanonicalCriterionIDsV2(r.CriterionIDs)) {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindRevisionIneligible, "work_revision_criteria", revisionErr)
	}
	if len(observed.Assertions) != 3 {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindStaleAuthority, "authority_assertions", nil)
	}
	executionCoordinate, _ := execution.ExecutionSlotCoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	workspaceCoordinate, _ := workspace.WorkspaceCoordinateV2(r.Workspace)
	workgraphCoordinate, _ := journalv2.NewAggregateCoordinate("workgraph", journalv2.AggregateID(r.MissionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(r.ProjectID), MissionID: journalv2.MissionID(r.MissionID)})
	want := map[journalv2.AggregateCoordinate]uint64{executionCoordinate: observed.Execution.RecordRevision, workspaceCoordinate: observed.Workspace.RecordRevision, workgraphCoordinate: observed.WorkRevision.RecordRevision}
	for _, assertion := range observed.Assertions {
		target := assertion.Target()
		revision, ok := want[target.Coordinate]
		if !ok || target.Kind != journalv2.ConditionTargetAggregateHead || assertion.Kind() != journalv2.ConditionRecordRevision || assertion.ObservedRevision() != revision || target.Coordinate == workgraphCoordinate && !sameAuthorityAssertionV2(assertion, observed.WorkRevision.Assertion) {
			return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindStaleAuthority, "authority_assertions", nil)
		}
		delete(want, target.Coordinate)
	}
	if len(want) != 0 {
		return journalv2.EligibilityGuardTransition{}, authorityErrorV2(KindStaleAuthority, "authority_assertions", nil)
	}
	return token.Transition(), nil
}

func sameAuthorityAssertionV2(a, b journalv2.AuthorityAssertionV2) bool {
	return a.Target() == b.Target() && a.Kind() == b.Kind() && a.ObservedRevision() == b.ObservedRevision() && a.ExpectedLogicalVersion() == b.ExpectedLogicalVersion() && a.ObservedValueDigest() == b.ObservedValueDigest()
}

func buildPlanV2(r AuthorityRequestV2, binding journalv2.OperationBindingV2, prior *StateV2, priorHandoffRevision uint64, priorOperation *journalv2.OperationStateV2, operationRevision uint64, next StateV2, authority AuthorityObservationV2, eligibility journalv2.EligibilityGuardTransition, registry journalv2.EventRegistryV2) (AuthorityPlanV2, error) {
	coordinate, _ := CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	version := journalv2.LogicalVersion(next.LogicalVersion)
	opVersion := journalv2.LogicalVersion(1)
	if priorOperation != nil {
		opVersion = priorOperation.LogicalVersion() + 1
	}
	opEventID := deterministicEventIDV2("operation", r.OperationID, uint64(opVersion))
	handoffEventID := deterministicEventIDV2("handoff", r.OperationID, uint64(version))
	eventIDs := []journalv2.EventID{opEventID, handoffEventID}
	if priorOperation != nil {
		eventIDs = append(priorOperation.EventIDs(), eventIDs...)
	}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	status := journalv2.StatusInProgress
	var resultKind string
	var result []byte
	if next.Stage == StageCandidateRecordedV2 {
		status, resultKind, result = journalv2.StatusResolved, "handoff.result", next.CanonicalBytes()
	} else if next.Stage == StageCandidateRefusedV2 {
		status, resultKind, result = journalv2.StatusRefused, "handoff.result", next.CanonicalBytes()
	}
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: r.OperationID, LogicalVersion: opVersion, Status: status, ResultKind: resultKind, ResultSchemaVersion: func() uint32 {
		if resultKind != "" {
			return handoffResultSchemaV2
		}
		return 0
	}(), CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	priorDigest := ""
	expectedHandoffRevision := uint64(0)
	if prior != nil {
		d, _ := journalv2.ReducerStateDigestV2(prior.CanonicalBytes())
		priorDigest = d.String()
		expectedHandoffRevision = priorHandoffRevision
	}
	eventPayload, err := encodeEventV2(EventV2{1, string(next.Stage), priorDigest, next})
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(r.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	handoffDigest, _ := journalv2.ReducerStateDigestV2(next.CanonicalBytes())
	governance := []string{"config." + r.Governance.PolicyRevision, "doctrine." + r.Governance.DoctrineRevision, "mission_type." + r.Governance.MissionTypeRevision, "profile." + r.Governance.ProfileRevision}
	sort.Strings(governance)
	events := []journalv2.EventDraftV2{
		{EventID: opEventID, EventType: "operation." + string(status), Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: opVersion, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: opPayload},
		{EventID: handoffEventID, EventType: "handoff_slot." + string(next.Stage), Family: "handoff_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ActorID), ProcessID: journalv2.ProcessID(r.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.Governance.PolicyRevision, GovernanceRevisions: governance, Payload: eventPayload},
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: opCoordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
			if priorOperation != nil {
				return priorOperation.LogicalVersion()
			}
			return 0
		}(), NextLogicalVersion: opVersion, EventID: opEventID, ExpectedRecordRevision: operationRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opPayload},
		{Coordinate: coordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
			if prior != nil {
				return journalv2.LogicalVersion(prior.LogicalVersion)
			}
			return 0
		}(), NextLogicalVersion: version, EventID: handoffEventID, ExpectedRecordRevision: expectedHandoffRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: handoffDigest, AuthoritySummary: next.CanonicalBytes()},
	}
	conditions := make([]journalv2.ProviderCondition, 0, 2)
	for _, h := range heads {
		c, e := journalv2.ConditionForHeadTransitionV2(h)
		if e != nil {
			return AuthorityPlanV2{}, e
		}
		conditions = append(conditions, c)
	}
	eligibilityCondition, err := journalv2.ConditionForEligibilityGuardTransitionV2(eligibility)
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	conditions = append(conditions, eligibilityCondition)
	input := journalv2.CommitPlanInput{OperationState: operation, Events: events, Heads: heads, Conditions: conditions, EligibilityGuards: []journalv2.EligibilityGuardTransition{eligibility}, Assertions: append([]journalv2.AuthorityAssertionV2(nil), authority.Assertions...)}
	if priorOperation == nil {
		input.OperationBinding = &binding
	} else {
		input.PriorOperationState = priorOperation
	}
	sealed, err := journalv2.SealCommitPlanV2(input, registry)
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	return AuthorityPlanV2{sealed}, nil
}

func sameImmutableHandoffV2(a, b StateV2) bool {
	a.LogicalVersion, b.LogicalVersion = 0, 0
	a.Stage, b.Stage = "", ""
	a.RepositoryEvidence, b.RepositoryEvidence = CandidateEvidence{}, CandidateEvidence{}
	a.Refusal, b.Refusal = nil, nil
	return bytes.Equal(mustCanonicalV2(a), mustCanonicalV2(b))
}

func validReplacementTransitionV2(prior, next StateV2) bool {
	if next.Replacement == nil || next.Replacement.validate() != nil {
		return false
	}
	digest, err := journalv2.ReducerStateDigestV2(prior.CanonicalBytes())
	if err != nil {
		return false
	}
	r := *next.Replacement
	return r.PredecessorHandoffID == prior.HandoffID && r.PredecessorCandidate == prior.Candidate && r.PredecessorLogicalVersion == prior.LogicalVersion && r.PredecessorStateDigest == digest.String() &&
		prior.ProjectID == next.ProjectID && prior.MissionID == next.MissionID && prior.WorkRevisionID == next.WorkRevisionID && prior.WorkID == next.WorkID && prior.RepositoryID == next.RepositoryID && prior.LaneID == next.LaneID && prior.PinnedBase == next.PinnedBase &&
		prior.WorkRevisionCriteriaDigest == next.WorkRevisionCriteriaDigest && stringSlicesEqualV2(prior.CriterionIDs, next.CriterionIDs) && prior.CriteriaDigest == next.CriteriaDigest
}

func replacementMatchesPriorV2(request CandidateReplacementRequestV2, prior StateV2) bool {
	digest, err := journalv2.ReducerStateDigestV2(prior.CanonicalBytes())
	return err == nil && (prior.Stage == StageCandidateRecordedV2 || prior.Stage == StageCandidateRefusedV2) && request.PredecessorHandoffID == prior.HandoffID && request.PredecessorCandidate == prior.Candidate && request.PredecessorLogicalVersion == prior.LogicalVersion && request.PredecessorStateDigest == digest.String() && validBoundedTextV2(request.Reason, 1024)
}

func criterionAuthorityV2(revision workgraph.RevisionV2, workID ids.WorkID) ([]string, string, error) {
	if revision.SchemaVersion() != workgraph.RevisionSchemaV3 || !ValidDigest(revision.CriteriaDigest()) {
		return nil, "", authorityErrorV2(KindRevisionIneligible, "work_revision", nil)
	}
	for _, item := range revision.WorkItems() {
		if item.WorkID() != workID {
			continue
		}
		ids := make([]string, 0, len(item.Criteria()))
		for _, criterion := range item.Criteria() {
			ids = append(ids, criterion.ID())
		}
		canonical, err := CanonicalCriterionIDsV2(ids)
		if err != nil {
			return nil, "", err
		}
		return canonical, revision.CriteriaDigest(), nil
	}
	return nil, "", authorityErrorV2(KindRevisionIneligible, "work_revision_work", nil)
}

func mustCanonicalCriterionIDsV2(values []string) []string {
	canonical, _ := CanonicalCriterionIDsV2(values)
	return canonical
}
func sameGovernanceV2(a, b execution.ExecutionGovernanceV2) bool {
	return bytes.Equal(mustCanonicalV2(a), mustCanonicalV2(b))
}
func contributorDigestV2(values []ids.ActorID) string {
	h := sha256.New()
	_, _ = h.Write([]byte("spec-kitty/handoff-contributors/v1\x00"))
	for _, v := range values {
		fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func actorSlicesEqualV2(a, b []ids.ActorID) bool {
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
func CanonicalCriterionIDsV2(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	for _, value := range result {
		if !validBoundedTextV2(value, journalv2.MaxIdentityBytes) {
			return nil, authorityErrorV2(KindInvalid, "criterion_id", nil)
		}
	}
	sort.Strings(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, authorityErrorV2(KindInvalid, "criterion_duplicate", nil)
		}
	}
	if len(result) == 0 {
		return nil, authorityErrorV2(KindInvalid, "criterion_empty", nil)
	}
	return result, nil
}
func CriterionSetDigestV2(values []string) (string, error) {
	canonical, err := CanonicalCriterionIDsV2(values)
	if err != nil {
		return "", err
	}
	return criterionSetDigestV2(canonical), nil
}
func criterionSetDigestV2(values []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("spec-kitty/handoff-criteria/v1\x00"))
	for _, value := range values {
		fmt.Fprintf(h, "%d:%s", len(value), value)
	}
	return hex.EncodeToString(h.Sum(nil))
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
func validBoundedTextV2(v string, max int) bool {
	return v != "" && len(v) <= max && utf8.ValidString(v)
}
func deterministicEventIDV2(kind string, op journalv2.OperationID, version uint64) journalv2.EventID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("spec-kitty/%s/%s/%d", kind, op, version)))
	return journalv2.EventID("event-" + kind + "-" + hex.EncodeToString(sum[:16]))
}
func mustCanonicalV2(v any) []byte {
	raw, _ := json.Marshal(v)
	canonical, _ := journalv2.CanonicalObject(raw)
	return canonical
}
func strictJSONV2(encoded []byte, out any) error {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return authorityErrorV2(KindInvalid, "payload_budget", nil)
	}
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return authorityErrorV2(KindInvalid, "payload", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return authorityErrorV2(KindInvalid, "payload_trailing", err)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return authorityErrorV2(KindInvalid, "payload_canonical", err)
	}
	return nil
}
func authorityErrorV2(kind ErrorKind, field string, _ error) error {
	return &Error{Kind: kind, Message: "handoff authority refused: " + field}
}
