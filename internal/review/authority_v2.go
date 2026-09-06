package review

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
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

const (
	ReviewEventSchemaV2  = uint32(1)
	ReviewReducerV2      = uint32(1)
	reviewResultSchemaV2 = uint32(1)
)

type StageV2 string

const (
	StageAttemptRequestedV2  StageV2 = "attempt_requested"
	StageCriterionRecordedV2 StageV2 = "criterion_recorded"
	StageVerdictRecordedV2   StageV2 = "verdict_recorded"
)

type CriterionEvidenceV2 struct {
	CriterionID    string          `json:"criterion_id"`
	Status         CriterionStatus `json:"status"`
	EvidenceDigest string          `json:"evidence_digest"`
	ArtifactDigest string          `json:"artifact_digest"`
	Candidate      string          `json:"candidate"`
	PolicyRevision string          `json:"policy_revision"`
}

func (e CriterionEvidenceV2) validate() error {
	if !boundedTextV2(e.CriterionID, journalv2.MaxIdentityBytes) || (e.Status != Passed && e.Status != Failed) || !handoff.ValidDigest(e.EvidenceDigest) || !handoff.ValidDigest(e.ArtifactDigest) || !handoff.ValidCommit(e.Candidate) || !boundedTextV2(e.PolicyRevision, journalv2.MaxIdentityBytes) {
		return reviewAuthorityErrorV2(KindInvalid, "criterion", nil)
	}
	return nil
}

// IndependenceExceptionV2 is an explicit, narrow, content-addressed policy
// fact. It waives only the ActorID inequality predicate for one exact review.
type IndependenceExceptionV2 struct {
	SchemaVersion     uint16             `json:"schema_version"`
	ExceptionID       string             `json:"exception_id"`
	PolicyRevision    string             `json:"policy_revision"`
	ProjectID         ids.ProjectID      `json:"project_id"`
	MissionID         ids.MissionID      `json:"mission_id"`
	WorkRevisionID    ids.WorkRevisionID `json:"work_revision_id"`
	WorkID            ids.WorkID         `json:"work_id"`
	HandoffID         ids.HandoffID      `json:"handoff_id"`
	Candidate         string             `json:"candidate"`
	ReviewerActorID   ids.ActorID        `json:"reviewer_actor_id"`
	Reason            string             `json:"reason"`
	AuthorizerActorID ids.ActorID        `json:"authorizer_actor_id"`
	EffectiveFrom     string             `json:"effective_from"`
	EffectiveThrough  string             `json:"effective_through"`
}

func (e IndependenceExceptionV2) Validate() error {
	if e.SchemaVersion != 1 || !boundedTextV2(e.ExceptionID, journalv2.MaxIdentityBytes) || !boundedTextV2(e.PolicyRevision, journalv2.MaxIdentityBytes) || !boundedTextV2(e.Reason, 1024) || !handoff.ValidCommit(e.Candidate) {
		return reviewAuthorityErrorV2(KindInvalid, "policy_exception", nil)
	}
	for _, parse := range []func() error{func() error { _, x := ids.ParseProjectID(e.ProjectID.String()); return x }, func() error { _, x := ids.ParseMissionID(e.MissionID.String()); return x }, func() error { _, x := ids.ParseWorkRevisionID(e.WorkRevisionID.String()); return x }, func() error { _, x := ids.ParseWorkID(e.WorkID.String()); return x }, func() error { _, x := ids.ParseHandoffID(e.HandoffID.String()); return x }, func() error { _, x := ids.ParseActorID(e.ReviewerActorID.String()); return x }, func() error { _, x := ids.ParseActorID(e.AuthorizerActorID.String()); return x }} {
		if err := parse(); err != nil {
			return reviewAuthorityErrorV2(KindInvalid, "policy_exception_identity", err)
		}
	}
	from, err := time.Parse(time.RFC3339Nano, e.EffectiveFrom)
	if err != nil || from.Location() != time.UTC {
		return reviewAuthorityErrorV2(KindInvalid, "policy_exception_time", err)
	}
	through, err := time.Parse(time.RFC3339Nano, e.EffectiveThrough)
	if err != nil || through.Location() != time.UTC || !through.After(from) {
		return reviewAuthorityErrorV2(KindInvalid, "policy_exception_time", err)
	}
	return nil
}
func (e IndependenceExceptionV2) CanonicalBytes() []byte {
	if e.Validate() != nil {
		return nil
	}
	raw, _ := json.Marshal(e)
	b, _ := journalv2.CanonicalObject(raw)
	return b
}
func (e IndependenceExceptionV2) Digest() journalv2.Digest {
	d, _ := journalv2.ReducerStateDigestV2(e.CanonicalBytes())
	return d
}
func DecodeIndependenceExceptionV2(encoded []byte) (IndependenceExceptionV2, error) {
	var e IndependenceExceptionV2
	if err := strictReviewJSONV2(encoded, &e); err != nil {
		return e, err
	}
	if e.Validate() != nil || !bytes.Equal(e.CanonicalBytes(), encoded) {
		return IndependenceExceptionV2{}, reviewAuthorityErrorV2(KindInvalid, "policy_exception", nil)
	}
	return e, nil
}

type StateV2 struct {
	SchemaVersion              uint16                          `json:"schema_version"`
	LogicalVersion             uint64                          `json:"logical_version"`
	Stage                      StageV2                         `json:"stage"`
	ReviewID                   ids.ReviewID                    `json:"review_id"`
	HandoffID                  ids.HandoffID                   `json:"handoff_id"`
	ProjectID                  ids.ProjectID                   `json:"project_id"`
	MissionID                  ids.MissionID                   `json:"mission_id"`
	WorkRevisionID             ids.WorkRevisionID              `json:"work_revision_id"`
	WorkID                     ids.WorkID                      `json:"work_id"`
	RuntimeEpochID             journalv2.RuntimeEpochID        `json:"runtime_epoch_id"`
	Candidate                  string                          `json:"candidate"`
	HandoffContributorDigest   string                          `json:"handoff_contributor_digest"`
	ReviewerActorID            ids.ActorID                     `json:"reviewer_actor_id"`
	ReviewerProcessID          ids.ProcessID                   `json:"reviewer_process_id"`
	PolicyRevision             string                          `json:"policy_revision"`
	CriterionSetDigest         string                          `json:"criterion_set_digest"`
	WorkRevisionCriteriaDigest string                          `json:"work_revision_criteria_digest"`
	CriterionIDs               []string                        `json:"criterion_ids"`
	Criteria                   []CriterionEvidenceV2           `json:"criteria"`
	Verdict                    Verdict                         `json:"verdict,omitempty"`
	Exception                  *IndependenceExceptionV2        `json:"independence_exception,omitempty"`
	Governance                 execution.ExecutionGovernanceV2 `json:"governance"`
	ObservedAt                 string                          `json:"observed_at"`
}

func (s StateV2) Validate() error {
	if s.SchemaVersion != 1 || s.LogicalVersion == 0 || !handoff.ValidCommit(s.Candidate) || !handoff.ValidDigest(s.HandoffContributorDigest) || !handoff.ValidDigest(s.CriterionSetDigest) || !handoff.ValidDigest(s.WorkRevisionCriteriaDigest) || !boundedTextV2(s.PolicyRevision, journalv2.MaxIdentityBytes) {
		return reviewAuthorityErrorV2(KindInvalid, "review_state", nil)
	}
	governance, governanceErr := execution.ExecutionGovernanceFromContextV2(s.Governance.Context)
	if governanceErr != nil || !bytes.Equal(mustReviewCanonicalV2(governance), mustReviewCanonicalV2(s.Governance)) {
		return reviewAuthorityErrorV2(KindInvalid, "review_governance", governanceErr)
	}
	for _, parse := range []func() error{func() error { _, e := ids.ParseReviewID(s.ReviewID.String()); return e }, func() error { _, e := ids.ParseHandoffID(s.HandoffID.String()); return e }, func() error { _, e := ids.ParseProjectID(s.ProjectID.String()); return e }, func() error { _, e := ids.ParseMissionID(s.MissionID.String()); return e }, func() error { _, e := ids.ParseWorkRevisionID(s.WorkRevisionID.String()); return e }, func() error { _, e := ids.ParseWorkID(s.WorkID.String()); return e }, func() error { _, e := ids.ParseActorID(s.ReviewerActorID.String()); return e }, func() error { _, e := ids.ParseProcessID(s.ReviewerProcessID.String()); return e }, func() error { _, e := journalv2.NewRuntimeEpochID(string(s.RuntimeEpochID)); return e }} {
		if err := parse(); err != nil {
			return reviewAuthorityErrorV2(KindInvalid, "review_identity", err)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, s.ObservedAt); err != nil {
		return reviewAuthorityErrorV2(KindInvalid, "observed_at", err)
	}
	if len(s.CriterionIDs) == 0 || criterionIDsDigestV2(s.CriterionIDs) != s.CriterionSetDigest {
		return reviewAuthorityErrorV2(KindInvalid, "criterion_digest", nil)
	}
	for i, criterionID := range s.CriterionIDs {
		if !boundedTextV2(criterionID, journalv2.MaxIdentityBytes) || i > 0 && s.CriterionIDs[i-1] >= criterionID {
			return reviewAuthorityErrorV2(KindInvalid, "criterion_ids", nil)
		}
	}
	for i, criterion := range s.Criteria {
		if criterion.validate() != nil || criterion.Candidate != s.Candidate || criterion.PolicyRevision != s.PolicyRevision || i > 0 && s.Criteria[i-1].CriterionID >= criterion.CriterionID {
			return reviewAuthorityErrorV2(KindInvalid, "criteria", nil)
		}
		index := sort.SearchStrings(s.CriterionIDs, criterion.CriterionID)
		if index == len(s.CriterionIDs) || s.CriterionIDs[index] != criterion.CriterionID {
			return reviewAuthorityErrorV2(KindInvalid, "criterion_unknown", nil)
		}
	}
	switch s.Stage {
	case StageAttemptRequestedV2:
		if len(s.Criteria) != 0 || s.Verdict != "" {
			return reviewAuthorityErrorV2(KindInvalid, "attempt", nil)
		}
	case StageCriterionRecordedV2:
		if len(s.Criteria) == 0 || s.Verdict != "" {
			return reviewAuthorityErrorV2(KindInvalid, "criterion_stage", nil)
		}
	case StageVerdictRecordedV2:
		if len(s.Criteria) != len(s.CriterionIDs) || !validVerdictForEvidenceV2(s.Verdict, s.Criteria) {
			return reviewAuthorityErrorV2(KindInvalid, "verdict", nil)
		}
	default:
		return reviewAuthorityErrorV2(KindInvalid, "stage", nil)
	}
	if s.Exception != nil {
		if s.Exception.Validate() != nil || s.Exception.PolicyRevision != s.PolicyRevision || s.Exception.ProjectID != s.ProjectID || s.Exception.MissionID != s.MissionID || s.Exception.WorkRevisionID != s.WorkRevisionID || s.Exception.WorkID != s.WorkID || s.Exception.HandoffID != s.HandoffID || s.Exception.Candidate != s.Candidate || s.Exception.ReviewerActorID != s.ReviewerActorID {
			return reviewAuthorityErrorV2(KindInvalid, "policy_exception_scope", nil)
		}
	}
	return nil
}
func (s StateV2) CanonicalBytes() []byte { b, _ := encodeReviewStateV2(s); return b }
func encodeReviewStateV2(s StateV2) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(s)
	return journalv2.CanonicalObject(raw)
}
func DecodeStateV2(encoded []byte) (StateV2, error) {
	var s StateV2
	if err := strictReviewJSONV2(encoded, &s); err != nil {
		return s, err
	}
	b, err := encodeReviewStateV2(s)
	if err != nil || !bytes.Equal(b, encoded) {
		return StateV2{}, reviewAuthorityErrorV2(KindInvalid, "review_state", err)
	}
	s.Criteria = append([]CriterionEvidenceV2(nil), s.Criteria...)
	s.CriterionIDs = append([]string(nil), s.CriterionIDs...)
	if s.Exception != nil {
		x := *s.Exception
		s.Exception = &x
	}
	return s, nil
}

type EventV2 struct {
	SchemaVersion uint16  `json:"schema_version"`
	Kind          string  `json:"kind"`
	PriorDigest   string  `json:"prior_digest,omitempty"`
	State         StateV2 `json:"state"`
}

func encodeReviewEventV2(e EventV2) ([]byte, error) {
	if e.SchemaVersion != 1 || !boundedTextV2(e.Kind, 64) || e.State.Validate() != nil {
		return nil, reviewAuthorityErrorV2(KindInvalid, "event", nil)
	}
	raw, _ := json.Marshal(e)
	return journalv2.CanonicalObject(raw)
}
func DecodeEventV2(encoded []byte) (EventV2, error) {
	var e EventV2
	if err := strictReviewJSONV2(encoded, &e); err != nil {
		return e, err
	}
	b, err := encodeReviewEventV2(e)
	if err != nil || !bytes.Equal(b, encoded) {
		return EventV2{}, reviewAuthorityErrorV2(KindInvalid, "event", err)
	}
	return e, nil
}

func CoordinateV2(project ids.ProjectID, mission ids.MissionID, revision ids.WorkRevisionID, work ids.WorkID, reviewID ids.ReviewID) (journalv2.AggregateCoordinate, error) {
	return journalv2.NewAggregateCoordinate("review_slot", journalv2.AggregateID(reviewID), journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: journalv2.ProjectID(project), MissionID: journalv2.MissionID(mission), WorkRevisionID: journalv2.WorkRevisionID(revision), WorkID: journalv2.WorkID(work)})
}

func ReduceEventV2(prior []byte, envelope journalv2.EventEnvelopeV2) ([]byte, error) {
	if envelope.Family() != "review_slot" || envelope.EventSchemaVersion() != 1 || envelope.ReducerVersion() != 1 {
		return nil, reviewAuthorityErrorV2(KindInvalid, "tuple", nil)
	}
	e, err := DecodeEventV2(envelope.Payload())
	if err != nil {
		return nil, err
	}
	next := e.State
	c, _ := CoordinateV2(next.ProjectID, next.MissionID, next.WorkRevisionID, next.WorkID, next.ReviewID)
	if c != envelope.Coordinate() || envelope.LogicalVersion() != journalv2.LogicalVersion(next.LogicalVersion) || envelope.EventType() != "review_slot."+e.Kind || envelope.ActorID() != journalv2.ActorID(next.ReviewerActorID) || envelope.ProcessID() != journalv2.ProcessID(next.ReviewerProcessID) || envelope.WorkRevisionID() != journalv2.WorkRevisionID(next.WorkRevisionID) || envelope.PolicyRevision() != next.PolicyRevision || envelope.CommittedRuntimeEpochID() != next.RuntimeEpochID {
		return nil, reviewAuthorityErrorV2(KindStaleAuthority, "event_authority", nil)
	}
	if len(prior) == 0 || bytes.Equal(prior, []byte(`{}`)) {
		if next.Stage != StageAttemptRequestedV2 || next.LogicalVersion != 1 || e.PriorDigest != "" {
			return nil, reviewAuthorityErrorV2(KindConflict, "initial", nil)
		}
		return next.CanonicalBytes(), nil
	}
	p, err := DecodeStateV2(prior)
	if err != nil || !sameReviewIdentityV2(p, next) || next.LogicalVersion != p.LogicalVersion+1 || p.Stage == StageVerdictRecordedV2 {
		return nil, reviewAuthorityErrorV2(KindConflict, "transition", err)
	}
	d, _ := journalv2.ReducerStateDigestV2(p.CanonicalBytes())
	if e.PriorDigest != d.String() {
		return nil, reviewAuthorityErrorV2(KindConflict, "prior", nil)
	}
	if next.Stage == StageCriterionRecordedV2 {
		if len(next.Criteria) != len(p.Criteria)+1 || !criteriaPrefixV2(p.Criteria, next.Criteria) {
			return nil, reviewAuthorityErrorV2(KindConflict, "criterion", nil)
		}
	} else if next.Stage == StageVerdictRecordedV2 {
		if p.Stage != StageCriterionRecordedV2 || !criteriaEqualV2(p.Criteria, next.Criteria) {
			return nil, reviewAuthorityErrorV2(KindConflict, "verdict", nil)
		}
	} else {
		return nil, reviewAuthorityErrorV2(KindConflict, "stage", nil)
	}
	return next.CanonicalBytes(), nil
}
func ReducerRegistrationsV2() []journalv2.ReducerRegistration {
	sum := sha256.Sum256([]byte("spec-kitty/review-reducer/1.1/work-revision-policy"))
	d, _ := journalv2.ParseDigest("review_reducer", hex.EncodeToString(sum[:]))
	return []journalv2.ReducerRegistration{{Family: "review_slot", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/review", ImplementationDigest: d, Reduce: ReduceEventV2}}
}

type AuthorityRequestV2 struct {
	OperationID                journalv2.OperationID
	ReviewID                   ids.ReviewID
	HandoffID                  ids.HandoffID
	ProjectID                  ids.ProjectID
	MissionID                  ids.MissionID
	WorkRevisionID             ids.WorkRevisionID
	WorkID                     ids.WorkID
	RuntimeEpochID             journalv2.RuntimeEpochID
	Workspace                  workspace.BindingV2
	ReviewerActorID            ids.ActorID
	ReviewerProcessID          ids.ProcessID
	Candidate                  string
	HandoffContributorDigest   string
	WorkRevisionCriteriaDigest string
	Criteria                   []CriterionEvidenceV2
	Verdict                    Verdict
	PolicyRevision             string
	Exception                  *IndependenceExceptionV2
	Governance                 execution.ExecutionGovernanceV2
	ObservedAt                 time.Time
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
type ExceptionObservationV2 struct {
	Fact      MissionPolicyFactObservationV2
	Assertion journalv2.AuthorityAssertionV2
}

// MissionPolicyFactObservationV2 is a provider-neutral snapshot of the
// current mission fact head. The adapter translates the mission authority;
// review owns policy interpretation and never parses mission wire.
type MissionPolicyFactObservationV2 struct {
	ProjectID      journalv2.ProjectID
	MissionID      journalv2.MissionID
	FactID         string
	Kind           string
	Status         string
	LogicalVersion journalv2.LogicalVersion
	DetailDigest   journalv2.Digest
	RecordRevision uint64
	Found          bool
}
type AuthorityObservationV2 struct {
	Eligibility  workgraph.ObservedEligibilityShardV2
	Execution    execution.ExecutionSlotObservationV2
	Workspace    execution.WorkspaceAuthorityObservationV2
	Handoff      handoff.ObservationV2
	WorkRevision handoff.WorkRevisionObservationV2
	Exception    ExceptionObservationV2
	Assertions   []journalv2.AuthorityAssertionV2
}
type AuthorityPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p AuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, reviewAuthorityErrorV2(KindInvalid, "plan", nil)
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
	ResolveReviewOperation(context.Context, journalv2.OperationBindingV2) (OperationResolutionV2, error)
	ObserveReview(context.Context, journalv2.AggregateCoordinate) (ObservationV2, error)
	ObserveReviewAuthority(context.Context, AuthorityRequestV2) (AuthorityObservationV2, error)
	PublishReview(context.Context, AuthorityPlanV2) (AuthorityAckV2, error)
	ReconcileReview(context.Context, AuthorityPlanV2) (AuthorityAckV2, error)
}
type RuntimeEpochSourceV2 interface {
	CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error)
}
type AuthorityResultV2 struct {
	State          StateV2
	PublicationID  journalv2.PublicationID
	RecordRevision uint64
	Replayed       bool
}
type AuthorityServiceV2 struct {
	port     AuthorityPortV2
	epochs   RuntimeEpochSourceV2
	registry journalv2.EventRegistryV2
}

func NewAuthorityServiceV2(port AuthorityPortV2, epochs RuntimeEpochSourceV2, registry journalv2.EventRegistryV2) (*AuthorityServiceV2, error) {
	if port == nil || epochs == nil {
		return nil, reviewAuthorityErrorV2(KindInvalid, "dependencies", nil)
	}
	if _, err := registry.Resolve("review_slot", 1, 1); err != nil {
		return nil, err
	}
	return &AuthorityServiceV2{port, epochs, registry}, nil
}

func (s *AuthorityServiceV2) Submit(ctx context.Context, r AuthorityRequestV2) (AuthorityResultV2, error) {
	binding, seed, err := prepareReviewRequestV2(r)
	if err != nil {
		return AuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveReviewOperation(ctx, binding)
	if err != nil {
		return AuthorityResultV2{}, reviewAuthorityErrorV2(KindConflict, "operation_resolution", err)
	}
	if resolution.Kind == OperationConflictV2 {
		return AuthorityResultV2{}, reviewAuthorityErrorV2(KindIdempotencyConflict, "operation_id", nil)
	}
	coordinate, _ := CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, r.ReviewID)
	desiredCriteria := append([]CriterionEvidenceV2(nil), r.Criteria...)
	sort.Slice(desiredCriteria, func(i, j int) bool { return desiredCriteria[i].CriterionID < desiredCriteria[j].CriterionID })
	var current *StateV2
	var reviewRevision uint64
	var operation *journalv2.OperationStateV2
	var operationRevision uint64
	if resolution.Kind == OperationExactV2 {
		if resolution.State.Status() == journalv2.StatusResolved {
			state, e := DecodeStateV2(resolution.State.Result())
			if e != nil || state.Stage != StageVerdictRecordedV2 || !sameReviewIdentityV2(seed, state) || !reviewResultMatchesRequestV2(r, state) {
				return AuthorityResultV2{}, reviewAuthorityErrorV2(KindConflict, "operation_result", e)
			}
			return AuthorityResultV2{State: state, PublicationID: resolution.State.LastPublicationID(), RecordRevision: resolution.RecordRevision, Replayed: true}, nil
		}
		observed, e := s.port.ObserveReview(ctx, coordinate)
		if e != nil || observed.State == nil {
			return AuthorityResultV2{}, reviewAuthorityErrorV2(KindConflict, "review_resume", e)
		}
		current, reviewRevision, operation, operationRevision = observed.State, observed.RecordRevision, &resolution.State, resolution.RecordRevision
		if len(current.Criteria) > len(desiredCriteria) || !criteriaEqualV2(current.Criteria, desiredCriteria[:len(current.Criteria)]) {
			return AuthorityResultV2{}, reviewAuthorityErrorV2(KindConflict, "review_resume_evidence", nil)
		}
	} else if resolution.Kind != OperationAbsentV2 {
		return AuthorityResultV2{}, reviewAuthorityErrorV2(KindConflict, "operation_resolution", nil)
	}
	for {
		if epoch, e := s.epochs.CurrentRuntimeEpoch(ctx); e != nil || epoch != r.RuntimeEpochID {
			return AuthorityResultV2{}, reviewAuthorityErrorV2(KindStaleAuthority, "runtime_epoch", e)
		}
		authority, e := s.port.ObserveReviewAuthority(ctx, r)
		if e != nil {
			return AuthorityResultV2{}, reviewAuthorityErrorV2(KindStaleAuthority, "authority", e)
		}
		eligibility, assertions, e := validateReviewAuthorityV2(r, seed, authority)
		if e != nil {
			return AuthorityResultV2{}, e
		}
		next := seed
		next.LogicalVersion = 1
		next.Stage = StageAttemptRequestedV2
		if current != nil {
			next.LogicalVersion = current.LogicalVersion + 1
			next.Criteria = append([]CriterionEvidenceV2(nil), current.Criteria...)
			next.Exception = current.Exception
			if len(current.Criteria) < len(desiredCriteria) {
				next.Stage = StageCriterionRecordedV2
				next.Criteria = append(next.Criteria, desiredCriteria[len(current.Criteria)])
				sort.Slice(next.Criteria, func(i, j int) bool { return next.Criteria[i].CriterionID < next.Criteria[j].CriterionID })
			} else {
				next.Stage = StageVerdictRecordedV2
				next.Verdict = r.Verdict
			}
		}
		plan, e := buildReviewPlanV2(r, binding, current, reviewRevision, operation, operationRevision, next, eligibility, assertions, s.registry)
		if e != nil {
			return AuthorityResultV2{}, e
		}
		ack, e := s.port.PublishReview(ctx, plan)
		sealed, _ := plan.Publication()
		if e != nil {
			ack, e = s.port.ReconcileReview(ctx, plan)
			if e != nil {
				return AuthorityResultV2{}, reviewAuthorityErrorV2(KindConflict, "publication", e)
			}
		}
		if ack.ProviderRevision == 0 || ack.PublicationID != sealed.PublicationID() || ack.PlanDigest != sealed.Digest() || ack.MutationCount != sealed.MutationCount() {
			return AuthorityResultV2{}, reviewAuthorityErrorV2(KindConflict, "publication", e)
		}
		copy := next
		current = &copy
		reviewRevision = ack.ProviderRevision
		state := sealed.OperationState()
		operation = &state
		operationRevision = ack.ProviderRevision
		if next.Stage == StageVerdictRecordedV2 {
			return AuthorityResultV2{State: next, PublicationID: ack.PublicationID, RecordRevision: ack.ProviderRevision}, nil
		}
	}
}

func prepareReviewRequestV2(r AuthorityRequestV2) (journalv2.OperationBindingV2, StateV2, error) {
	if r.ObservedAt.IsZero() || r.Workspace.Validate() != nil || !handoff.ValidCommit(r.Candidate) || !handoff.ValidDigest(r.HandoffContributorDigest) || !handoff.ValidDigest(r.WorkRevisionCriteriaDigest) || r.PolicyRevision != r.Governance.PolicyRevision {
		return journalv2.OperationBindingV2{}, StateV2{}, reviewAuthorityErrorV2(KindInvalid, "request", nil)
	}
	criteria := append([]CriterionEvidenceV2(nil), r.Criteria...)
	sort.Slice(criteria, func(i, j int) bool { return criteria[i].CriterionID < criteria[j].CriterionID })
	for i, c := range criteria {
		if c.validate() != nil || c.Candidate != r.Candidate || c.PolicyRevision != r.PolicyRevision || i > 0 && criteria[i-1].CriterionID == c.CriterionID {
			return journalv2.OperationBindingV2{}, StateV2{}, reviewAuthorityErrorV2(KindInvalid, "criteria", nil)
		}
	}
	if !validVerdictForEvidenceV2(r.Verdict, criteria) {
		return journalv2.OperationBindingV2{}, StateV2{}, reviewAuthorityErrorV2(KindInvalid, "verdict", nil)
	}
	criterionIDs := make([]string, len(criteria))
	for i := range criteria {
		criterionIDs[i] = criteria[i].CriterionID
	}
	seed := StateV2{SchemaVersion: 1, LogicalVersion: 1, Stage: StageAttemptRequestedV2, ReviewID: r.ReviewID, HandoffID: r.HandoffID, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, RuntimeEpochID: r.RuntimeEpochID, Candidate: r.Candidate, HandoffContributorDigest: r.HandoffContributorDigest, ReviewerActorID: r.ReviewerActorID, ReviewerProcessID: r.ReviewerProcessID, PolicyRevision: r.PolicyRevision, CriterionSetDigest: criterionIDsDigestV2(criterionIDs), WorkRevisionCriteriaDigest: r.WorkRevisionCriteriaDigest, CriterionIDs: criterionIDs, Criteria: nil, Governance: r.Governance, ObservedAt: r.ObservedAt.UTC().Format(time.RFC3339Nano)}
	if r.Exception != nil {
		x := *r.Exception
		seed.Exception = &x
	}
	if err := seed.Validate(); err != nil {
		return journalv2.OperationBindingV2{}, StateV2{}, err
	}
	coordinate, _ := CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, r.ReviewID)
	intent := struct {
		Seed     StateV2               `json:"seed"`
		Criteria []CriterionEvidenceV2 `json:"criteria"`
		Verdict  Verdict               `json:"verdict"`
	}{seed, criteria, r.Verdict}
	d, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "review.submit", CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: mustReviewCanonicalV2(intent)})
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: r.OperationID, CommandKind: "review.submit", CommandSchemaVersion: 1, CanonicalIntentDigest: d, Scope: coordinate.Scope, ActorID: journalv2.ActorID(r.ReviewerActorID), ProcessID: journalv2.ProcessID(r.ReviewerProcessID), DoctrineRevision: r.Governance.DoctrineRevision, ProfileRevision: r.Governance.ProfileRevision, PolicyRevision: r.PolicyRevision, OriginatingRuntimeEpochID: r.RuntimeEpochID, ObservedCreatedAt: r.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)})
	return binding, seed, err
}

func validateReviewAuthorityV2(r AuthorityRequestV2, prepared StateV2, o AuthorityObservationV2) (journalv2.EligibilityGuardTransition, []journalv2.AuthorityAssertionV2, error) {
	token, err := o.Eligibility.Token(r.WorkID, r.WorkRevisionID, workgraph.EligibilityReview)
	if err != nil {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindRevisionIneligible, "eligibility", err)
	}
	if o.Execution.State == nil || o.Workspace.State == nil || o.Handoff.State == nil {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindStaleAuthority, "authority", nil)
	}
	e := *o.Execution.State
	if e.ProjectID != r.ProjectID || e.MissionID != r.MissionID || e.WorkRevisionID != r.WorkRevisionID || e.WorkID != r.WorkID || e.RuntimeEpochID != r.RuntimeEpochID || e.WorkspaceID != r.Workspace.WorkspaceID || e.Status != execution.ExecutionActiveV2 || e.ActorID != r.Workspace.ActorID || e.ProcessID != r.Workspace.ProcessID || e.LeaseID != r.Workspace.LeaseID || e.Generation != r.Workspace.Generation || e.Fence != r.Workspace.Fence || !bytes.Equal(mustReviewCanonicalV2(e.Governance), mustReviewCanonicalV2(r.Governance)) {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindStaleAuthority, "execution", nil)
	}
	deadline, _ := time.Parse(time.RFC3339Nano, e.Deadline)
	if !r.ObservedAt.Before(deadline) {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindStaleAuthority, "lease", nil)
	}
	capsule, _ := workspace.NewAuthorityCapsuleV2(r.Workspace)
	if workspace.ValidateCurrentV2(*o.Workspace.State, capsule) != nil {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindStaleAuthority, "workspace", nil)
	}
	h := *o.Handoff.State
	criteria, revisionDigest, revisionErr := handoffCriterionAuthorityV2(o.WorkRevision.Revision, r.WorkID)
	if revisionErr != nil || o.WorkRevision.RecordRevision == 0 || o.WorkRevision.Revision.ID() != r.WorkRevisionID || revisionDigest != r.WorkRevisionCriteriaDigest || !stringSlicesEqualReviewV2(criteria, prepared.CriterionIDs) {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindRevisionIneligible, "work_revision_criteria", revisionErr)
	}
	if h.Stage != handoff.StageCandidateRecordedV2 || h.HandoffID != r.HandoffID || h.ProjectID != r.ProjectID || h.MissionID != r.MissionID || h.WorkRevisionID != r.WorkRevisionID || h.WorkID != r.WorkID || h.Candidate != r.Candidate || h.ContributorDigest != r.HandoffContributorDigest || h.WorkRevisionCriteriaDigest != r.WorkRevisionCriteriaDigest || !stringSlicesEqualReviewV2(h.CriterionIDs, prepared.CriterionIDs) || !bytes.Equal(mustReviewCanonicalV2(h.Governance), mustReviewCanonicalV2(r.Governance)) {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindCandidateMismatch, "handoff", nil)
	}
	sameActor := false
	for _, a := range h.Contributors {
		sameActor = sameActor || a == r.ReviewerActorID
	}
	assertions := append([]journalv2.AuthorityAssertionV2(nil), o.Assertions...)
	if sameActor {
		fact := o.Exception.Fact
		if r.Exception == nil || !fact.Found || fact.RecordRevision == 0 || fact.Kind != "decision" || fact.Status != "resolved" || fact.FactID != r.Exception.ExceptionID || fact.ProjectID != journalv2.ProjectID(r.ProjectID) || fact.MissionID != journalv2.MissionID(r.MissionID) || fact.LogicalVersion == 0 || o.Exception.Assertion.ExpectedLogicalVersion() != fact.LogicalVersion || fact.DetailDigest != r.Exception.Digest() || !exceptionEffectiveV2(*r.Exception, r.ObservedAt) {
			return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindReviewerNotIndependent, "reviewer_actor_id", nil)
		}
		assertions = append(assertions, o.Exception.Assertion)
	} else if r.Exception != nil {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindInvalid, "unnecessary_exception", nil)
	}
	want := 4
	if sameActor {
		want++
	}
	if len(assertions) != want {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindStaleAuthority, "assertions", nil)
	}
	executionCoordinate, _ := execution.ExecutionSlotCoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	workspaceCoordinate, _ := workspace.WorkspaceCoordinateV2(r.Workspace)
	handoffCoordinate, _ := handoff.CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID)
	workgraphCoordinate, _ := journalv2.NewAggregateCoordinate("workgraph", journalv2.AggregateID(r.MissionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(r.ProjectID), MissionID: journalv2.MissionID(r.MissionID)})
	wantedAssertions := map[journalv2.AggregateCoordinate]uint64{executionCoordinate: o.Execution.RecordRevision, workspaceCoordinate: o.Workspace.RecordRevision, handoffCoordinate: o.Handoff.RecordRevision, workgraphCoordinate: o.WorkRevision.RecordRevision}
	if sameActor {
		exceptionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+r.Exception.ExceptionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(r.ProjectID), MissionID: journalv2.MissionID(r.MissionID)})
		wantedAssertions[exceptionCoordinate] = o.Exception.Fact.RecordRevision
	}
	for _, assertion := range assertions {
		target := assertion.Target()
		revision, exists := wantedAssertions[target.Coordinate]
		if !exists || target.Kind != journalv2.ConditionTargetAggregateHead || assertion.Kind() != journalv2.ConditionRecordRevision || assertion.ObservedRevision() != revision || target.Coordinate == workgraphCoordinate && !sameReviewAssertionV2(assertion, o.WorkRevision.Assertion) {
			return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindStaleAuthority, "assertions", nil)
		}
		delete(wantedAssertions, target.Coordinate)
	}
	if len(wantedAssertions) != 0 {
		return journalv2.EligibilityGuardTransition{}, nil, reviewAuthorityErrorV2(KindStaleAuthority, "assertions", nil)
	}
	return token.Transition(), assertions, nil
}

func sameReviewAssertionV2(a, b journalv2.AuthorityAssertionV2) bool {
	return a.Target() == b.Target() && a.Kind() == b.Kind() && a.ObservedRevision() == b.ObservedRevision() && a.ExpectedLogicalVersion() == b.ExpectedLogicalVersion() && a.ObservedValueDigest() == b.ObservedValueDigest()
}

func stringSlicesEqualReviewV2(a, b []string) bool {
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

func buildReviewPlanV2(r AuthorityRequestV2, binding journalv2.OperationBindingV2, prior *StateV2, priorReviewRevision uint64, priorOperation *journalv2.OperationStateV2, operationRevision uint64, next StateV2, eligibility journalv2.EligibilityGuardTransition, assertions []journalv2.AuthorityAssertionV2, registry journalv2.EventRegistryV2) (AuthorityPlanV2, error) {
	version := journalv2.LogicalVersion(next.LogicalVersion)
	opID := reviewEventIDV2("operation", r.OperationID, uint64(version))
	reviewEventID := reviewEventIDV2("review", r.OperationID, uint64(version))
	idsList := []journalv2.EventID{opID, reviewEventID}
	if priorOperation != nil {
		idsList = append(priorOperation.EventIDs(), idsList...)
	}
	sort.Slice(idsList, func(i, j int) bool { return idsList[i] < idsList[j] })
	status := journalv2.StatusInProgress
	var kind string
	var result []byte
	if next.Stage == StageVerdictRecordedV2 {
		status = journalv2.StatusResolved
		kind = "review.result"
		result = next.CanonicalBytes()
	}
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: r.OperationID, LogicalVersion: version, Status: status, ResultKind: kind, ResultSchemaVersion: func() uint32 {
		if kind != "" {
			return reviewResultSchemaV2
		}
		return 0
	}(), CanonicalResult: result, EventIDs: idsList})
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	opPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	priorDigest := ""
	if prior != nil {
		d, _ := journalv2.ReducerStateDigestV2(prior.CanonicalBytes())
		priorDigest = d.String()
	}
	reviewPayload, err := encodeReviewEventV2(EventV2{1, string(next.Stage), priorDigest, next})
	if err != nil {
		return AuthorityPlanV2{}, err
	}
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(r.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	reviewCoordinate, _ := CoordinateV2(r.ProjectID, r.MissionID, r.WorkRevisionID, r.WorkID, r.ReviewID)
	opDigest, _ := journalv2.ReducerStateDigestV2(opPayload)
	reviewDigest, _ := journalv2.ReducerStateDigestV2(next.CanonicalBytes())
	gov := []string{"config." + r.Governance.PolicyRevision, "doctrine." + r.Governance.DoctrineRevision, "mission_type." + r.Governance.MissionTypeRevision, "profile." + r.Governance.ProfileRevision}
	sort.Strings(gov)
	events := []journalv2.EventDraftV2{{EventID: opID, EventType: "operation." + string(status), Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ReviewerActorID), ProcessID: journalv2.ProcessID(r.ReviewerProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.PolicyRevision, GovernanceRevisions: gov, Payload: opPayload}, {EventID: reviewEventID, EventType: "review_slot." + string(next.Stage), Family: "review_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: reviewCoordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: r.RuntimeEpochID, ActorID: journalv2.ActorID(r.ReviewerActorID), ProcessID: journalv2.ProcessID(r.ReviewerProcessID), WorkRevisionID: journalv2.WorkRevisionID(r.WorkRevisionID), PolicyRevision: r.PolicyRevision, GovernanceRevisions: gov, Payload: reviewPayload}}
	heads := []journalv2.HeadTransitionV2{{Coordinate: opCoordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
		if priorOperation != nil {
			return priorOperation.LogicalVersion()
		}
		return 0
	}(), NextLogicalVersion: version, EventID: opID, ExpectedRecordRevision: operationRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: opPayload}, {Coordinate: reviewCoordinate, ExpectedLogicalVersion: func() journalv2.LogicalVersion {
		if prior != nil {
			return journalv2.LogicalVersion(prior.LogicalVersion)
		}
		return 0
	}(), NextLogicalVersion: version, EventID: reviewEventID, ExpectedRecordRevision: priorReviewRevision, CommittedRuntimeEpochID: r.RuntimeEpochID, CanonicalStateDigest: reviewDigest, AuthoritySummary: next.CanonicalBytes()}}
	conditions := make([]journalv2.ProviderCondition, 0, 3)
	for _, h := range heads {
		c, e := journalv2.ConditionForHeadTransitionV2(h)
		if e != nil {
			return AuthorityPlanV2{}, e
		}
		conditions = append(conditions, c)
	}
	ec, e := journalv2.ConditionForEligibilityGuardTransitionV2(eligibility)
	if e != nil {
		return AuthorityPlanV2{}, e
	}
	conditions = append(conditions, ec)
	input := journalv2.CommitPlanInput{OperationState: operation, Events: events, Heads: heads, Conditions: conditions, EligibilityGuards: []journalv2.EligibilityGuardTransition{eligibility}, Assertions: assertions}
	if priorOperation == nil {
		input.OperationBinding = &binding
	} else {
		input.PriorOperationState = priorOperation
	}
	sealed, e := journalv2.SealCommitPlanV2(input, registry)
	if e != nil {
		return AuthorityPlanV2{}, e
	}
	return AuthorityPlanV2{sealed}, nil
}

func criterionIDsDigestV2(criteria []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("spec-kitty/review-criteria/v1\x00"))
	for _, c := range criteria {
		fmt.Fprintf(h, "%d:%s", len(c), c)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func handoffCriterionAuthorityV2(revision workgraph.RevisionV2, workID ids.WorkID) ([]string, string, error) {
	if revision.SchemaVersion() != workgraph.RevisionSchemaV3 || !handoff.ValidDigest(revision.CriteriaDigest()) {
		return nil, "", reviewAuthorityErrorV2(KindRevisionIneligible, "work_revision", nil)
	}
	for _, item := range revision.WorkItems() {
		if item.WorkID() != workID {
			continue
		}
		criterionIDs := make([]string, 0, len(item.Criteria()))
		for _, criterion := range item.Criteria() {
			criterionIDs = append(criterionIDs, criterion.ID())
		}
		canonical, err := handoff.CanonicalCriterionIDsV2(criterionIDs)
		return canonical, revision.CriteriaDigest(), err
	}
	return nil, "", reviewAuthorityErrorV2(KindRevisionIneligible, "work_revision_work", nil)
}
func validVerdictForEvidenceV2(v Verdict, c []CriterionEvidenceV2) bool {
	if len(c) == 0 {
		return false
	}
	failed := false
	for _, e := range c {
		failed = failed || e.Status == Failed
	}
	return v == Approved && !failed || v == ChangesRequested && failed
}
func reviewResultMatchesRequestV2(request AuthorityRequestV2, state StateV2) bool {
	criteria := append([]CriterionEvidenceV2(nil), request.Criteria...)
	sort.Slice(criteria, func(i, j int) bool { return criteria[i].CriterionID < criteria[j].CriterionID })
	return state.Verdict == request.Verdict && criteriaEqualV2(state.Criteria, criteria)
}
func criteriaPrefixV2(a, b []CriterionEvidenceV2) bool {
	return len(a) < len(b) && criteriaEqualV2(a, b[:len(a)])
}
func criteriaEqualV2(a, b []CriterionEvidenceV2) bool {
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
func sameReviewIdentityV2(a, b StateV2) bool {
	a.LogicalVersion, b.LogicalVersion = 0, 0
	a.Stage, b.Stage = "", ""
	a.Criteria, b.Criteria = nil, nil
	a.CriterionIDs, b.CriterionIDs = nil, nil
	a.Verdict, b.Verdict = "", ""
	return bytes.Equal(mustReviewCanonicalV2(a), mustReviewCanonicalV2(b))
}
func exceptionEffectiveV2(e IndependenceExceptionV2, at time.Time) bool {
	from, x := time.Parse(time.RFC3339Nano, e.EffectiveFrom)
	through, y := time.Parse(time.RFC3339Nano, e.EffectiveThrough)
	return x == nil && y == nil && !at.Before(from) && at.Before(through)
}
func reviewEventIDV2(kind string, op journalv2.OperationID, version uint64) journalv2.EventID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("spec-kitty/review/%s/%s/%d", kind, op, version)))
	return journalv2.EventID("event-review-" + hex.EncodeToString(sum[:16]))
}
func boundedTextV2(v string, max int) bool { return v != "" && len(v) <= max && utf8.ValidString(v) }
func mustReviewCanonicalV2(v any) []byte {
	raw, _ := json.Marshal(v)
	b, _ := journalv2.CanonicalObject(raw)
	return b
}
func strictReviewJSONV2(encoded []byte, out any) error {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return reviewAuthorityErrorV2(KindInvalid, "payload_budget", nil)
	}
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return reviewAuthorityErrorV2(KindInvalid, "payload", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return reviewAuthorityErrorV2(KindInvalid, "payload_trailing", err)
	}
	b, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(b, encoded) {
		return reviewAuthorityErrorV2(KindInvalid, "payload_canonical", err)
	}
	return nil
}
func reviewAuthorityErrorV2(kind ErrorKind, field string, _ error) error {
	return &Error{Kind: kind, Message: "review authority refused: " + field}
}
