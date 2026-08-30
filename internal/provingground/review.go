package provingground

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type candidateJournalRecord struct {
	Record          handoff.Record                `json:"record"`
	Authority       authorityEvidence             `json:"authority"`
	Criteria        []string                      `json:"criteria"`
	CandidateDigest string                        `json:"candidate_digest"`
	CriteriaDigest  string                        `json:"criteria_digest"`
	RequestDigest   string                        `json:"request_digest"`
	RuntimeEpochID  controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
}

type reviewJournalRecord struct {
	Record          basereview.Record             `json:"record"`
	CandidateDigest string                        `json:"candidate_digest"`
	CriteriaDigest  string                        `json:"criteria_digest"`
	RequestDigest   string                        `json:"request_digest"`
	RuntimeEpochID  controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
}

type handoffRefusalJournalRecord struct {
	Refusal        handoff.Refusal               `json:"refusal"`
	RequestDigest  string                        `json:"request_digest"`
	RepositoryPath string                        `json:"repository_path"`
	RuntimeEpochID controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
}

// ReviewCoordinator composes immutable Git handoff and the existing review
// service over the admitted journal. The candidate capsule belongs to the
// implementation; the review ActorID deliberately does not.
type ReviewCoordinator struct {
	runtime    *Runtime
	journal    *workflowJournal
	repository *gitrepo.Repository
	store      *workflowStore
	handoff    *handoff.Service
	review     *basereview.Service
}

type workflowStore struct {
	mu              sync.Mutex
	journal         *workflowJournal
	runtime         *Runtime
	repository      *gitrepo.Repository
	eligibility     WorkflowEligibilityAuthority
	criteria        map[ids.WorkID][]string
	candidates      map[ids.HandoffID]candidateJournalRecord
	candidateOps    map[ids.OperationID]ids.HandoffID
	refusals        map[ids.OperationID]handoffRefusalJournalRecord
	reviews         map[ids.ReviewID]reviewJournalRecord
	reviewOps       map[ids.OperationID]ids.ReviewID
	plans           map[controljournal.OperationID]IntegrationPlan
	planAuthorities map[controljournal.AggregateID]integrationPlanAuthority
	integrations    map[ids.OperationID]integrationJournalRecord
}

// WorkflowEligibilityAuthority is the complete executable-graph authority.
// The proving ground never manufactures permission and intentionally has no
// legacy/force-ready fallback.
type WorkflowEligibilityAuthority interface {
	WorkEligibilityFact(context.Context, ids.WorkRevisionID, ids.WorkID, workgraph.WorkStatus, workgraph.EligibilityOperation) (workgraph.EligibilityFact, error)
}

func NewReviewCoordinator(ctx context.Context, runtime *Runtime, app *application.Service, port storegapdb.Port, repository *gitrepo.Repository, eligibility WorkflowEligibilityAuthority, criteria map[ids.WorkID][]string) (*ReviewCoordinator, error) {
	if runtime == nil || app == nil || port == nil || repository == nil || eligibility == nil || len(criteria) == 0 {
		return nil, refusal(OutcomeInvalidCommand, "review", "runtime, admitted journal, real Git repository, exact eligibility authority, and criteria are required")
	}
	return newReviewCoordinator(ctx, runtime, app, port, repository, eligibility, criteria)
}
func newReviewCoordinator(ctx context.Context, runtime *Runtime, app batchApplier, port workflowJournalPort, repository *gitrepo.Repository, eligibility WorkflowEligibilityAuthority, criteria map[ids.WorkID][]string) (*ReviewCoordinator, error) {
	if runtime == nil || app == nil || port == nil || repository == nil || eligibility == nil {
		return nil, refusal(OutcomeInvalidCommand, "review", "complete review composition is required")
	}
	store := &workflowStore{journal: newWorkflowJournal(runtime.core.config, app, port), runtime: runtime, repository: repository, eligibility: eligibility, criteria: make(map[ids.WorkID][]string), candidates: make(map[ids.HandoffID]candidateJournalRecord), candidateOps: make(map[ids.OperationID]ids.HandoffID), refusals: make(map[ids.OperationID]handoffRefusalJournalRecord), reviews: make(map[ids.ReviewID]reviewJournalRecord), reviewOps: make(map[ids.OperationID]ids.ReviewID), plans: make(map[controljournal.OperationID]IntegrationPlan), planAuthorities: make(map[controljournal.AggregateID]integrationPlanAuthority), integrations: make(map[ids.OperationID]integrationJournalRecord)}
	for workID, values := range criteria {
		if _, err := ids.ParseWorkID(workID.String()); err != nil || len(values) == 0 {
			return nil, refusal(OutcomeInvalidCommand, "review", "criteria must name admitted work with at least one criterion")
		}
		copyValues := append([]string(nil), values...)
		sort.Strings(copyValues)
		for i, v := range copyValues {
			if v == "" || (i > 0 && copyValues[i-1] == v) {
				return nil, refusal(OutcomeInvalidCommand, "review", "criteria IDs must be unique and nonempty")
			}
		}
		store.criteria[workID] = copyValues
	}
	if err := store.restore(ctx); err != nil {
		return nil, err
	}
	return &ReviewCoordinator{runtime: runtime, journal: store.journal, repository: repository, store: store, handoff: handoff.NewService(store, gitrepo.NewHandoffVerifier()), review: basereview.NewService(store)}, nil
}

// SubmitCandidate accepts only W2 (Actor B/Process B) and W3 (Actor A/Process
// C). Process A's quarantinable W1 can never cross this boundary.
func (c *ReviewCoordinator) SubmitCandidate(ctx context.Context, capsule controljournal.AuthorityCapsuleV1, request handoff.Request) (handoff.Record, error) {
	if c == nil {
		return handoff.Record{}, refusal(OutcomeInvalidCommand, "candidate", "review coordinator is absent")
	}
	if err := c.runtime.Authorize(capsule, "handoff", request.SubmittedAt); err != nil {
		return handoff.Record{}, err
	}
	if capsule.ProcessID == c.runtime.core.config.Actors.ProcessA || (capsule.ProcessID != c.runtime.core.config.Actors.ProcessB && capsule.ProcessID != c.runtime.core.config.Actors.ProcessC) {
		return handoff.Record{}, refusal(OutcomeStaleLease, "candidate", "only W2 or fresh W3 may form immutable candidate evidence")
	}
	if request.ProjectID.String() != string(capsule.ProjectID) || request.MissionID.String() != string(capsule.MissionID) || request.WorkRevisionID.String() != string(capsule.WorkRevisionID) || request.RunID.String() != string(capsule.RunID) || request.WorkspaceID.String() != string(capsule.WorkspaceID) || request.LeaseID.String() != string(capsule.LeaseID) || request.LeaseGeneration != int64(capsule.LeaseGeneration) || request.Token != fmt.Sprint(capsule.FenceToken) || request.ActorID.String() != string(capsule.ActorID) || request.ProcessID.String() != string(capsule.ProcessID) {
		return handoff.Record{}, refusal(OutcomeStaleLease, "candidate", "handoff request differs from the exact current capsule")
	}
	c.store.bindCandidateAuthority(request.HandoffID, evidenceFromCapsule(capsule))
	return c.handoff.Submit(ctx, request)
}

func (c *ReviewCoordinator) SubmitReview(ctx context.Context, candidateCapsule controljournal.AuthorityCapsuleV1, request basereview.Request) (basereview.Record, error) {
	if c == nil {
		return basereview.Record{}, refusal(OutcomeInvalidCommand, "review", "review coordinator is absent")
	}
	if err := c.runtime.Authorize(candidateCapsule, "review", request.SubmittedAt); err != nil {
		return basereview.Record{}, err
	}
	c.store.mu.Lock()
	candidate, ok := c.store.candidates[request.HandoffID]
	c.store.mu.Unlock()
	if !ok || candidate.Record.Candidate != request.Candidate || candidate.Record.WorkRevisionID != request.WorkRevisionID || candidate.Record.WorkID != request.WorkID || candidate.Authority != evidenceFromCapsule(candidateCapsule) {
		return basereview.Record{}, &basereview.Error{Kind: basereview.KindCandidateMismatch, Message: "review does not bind the immutable candidate and current capsule"}
	}
	return c.review.Submit(ctx, request)
}

func (c *ReviewCoordinator) Candidate(id ids.HandoffID) (handoff.Record, bool) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	v, ok := c.store.candidates[id]
	return v.Record, ok
}
func (c *ReviewCoordinator) CandidateDigest(id ids.HandoffID) (string, bool) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	v, ok := c.store.candidates[id]
	return v.CandidateDigest, ok
}
func (c *ReviewCoordinator) Review(id ids.ReviewID) (basereview.Record, bool) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	v, ok := c.store.reviews[id]
	return v.Record, ok
}

func (s *workflowStore) bindCandidateAuthority(id ids.HandoffID, authority authorityEvidence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.candidates[id]
	if existing.Record.HandoffID == "" {
		existing.Authority = authority
		s.candidates[id] = existing
	}
}

func (s *workflowStore) WorkEligibility(_ context.Context, _ ids.WorkRevisionID, _ ids.WorkID, _ workgraph.WorkStatus) (workgraph.Consequence, string, error) {
	return workgraph.Consequence{}, "", refusal(OutcomeInvalidCommand, "eligibility", "legacy eligibility is unavailable; complete executable-graph authority is required")
}
func (s *workflowStore) WorkEligibilityFact(ctx context.Context, revision ids.WorkRevisionID, work ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	if s.eligibility == nil {
		return workgraph.EligibilityFact{}, refusal(OutcomeInvalidCommand, "eligibility", "complete executable-graph authority is absent")
	}
	fact, err := s.eligibility.WorkEligibilityFact(ctx, revision, work, status, operation)
	if err != nil {
		return workgraph.EligibilityFact{}, err
	}
	if !fact.Complete() || !fact.MatchesComplete(fact.ReconciliationID(), revision, fact.NewRevisionID(), work, status, fact.Disposition(), operation) {
		return workgraph.EligibilityFact{}, refusal(OutcomeInvalidCommand, "eligibility", "executable-graph authority returned a mismatched or incomplete fact")
	}
	return fact, nil
}

func (s *workflowStore) LookupHandoffOperation(_ context.Context, r handoff.Request) (handoff.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.candidateOps[r.OperationID]
	if !ok {
		return handoff.Record{}, false, nil
	}
	c := s.candidates[id]
	if c.RequestDigest != digestJSON(r) {
		return handoff.Record{}, false, &handoff.Error{Kind: handoff.KindIdempotencyConflict, Message: "handoff operation was reused with changed input"}
	}
	return c.Record, true, nil
}
func (s *workflowStore) LookupHandoffRefusal(_ context.Context, request handoff.Request) (handoff.Refusal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.refusals[request.OperationID]
	if !ok {
		return handoff.Refusal{}, false, nil
	}
	if entry.RequestDigest != digestJSON(request) {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindIdempotencyConflict, Message: "refused handoff operation was reused with changed input"}
	}
	result := entry.Refusal
	result.RepositoryPath = entry.RepositoryPath
	return result, true, nil
}
func (s *workflowStore) CommitHandoffRefusal(ctx context.Context, request handoff.Request, mismatch *handoff.Error) (handoff.Refusal, error) {
	if mismatch == nil || mismatch.Kind != handoff.KindCandidateMismatch || mismatch.Cause != nil || mismatch.Observed["divergence_kind"] != handoff.BranchMovementDivergence || mismatch.Observed["evidence_ref_root"] == "" {
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindConflict, Message: "only a completely preserved branch-movement refusal may be journaled"}
	}
	refused := handoff.Refusal{
		OperationID:     request.OperationID,
		Kind:            mismatch.Kind,
		Message:         mismatch.Message,
		Observed:        copyStringMap(mismatch.Observed),
		Expected:        copyStringMap(mismatch.Expected),
		RecoveryActions: append([]string(nil), mismatch.RecoveryActions...),
		OccurredAt:      request.SubmittedAt.UTC(),
		RepositoryPath:  s.repository.Binding().PrimaryPath,
	}
	entry := handoffRefusalJournalRecord{Refusal: refused, RequestDigest: digestJSON(request), RepositoryPath: refused.RepositoryPath, RuntimeEpochID: s.runtime.core.currentEpoch()}
	entry.Refusal.RepositoryPath = ""

	s.mu.Lock()
	prior, exists := s.refusals[request.OperationID]
	s.mu.Unlock()
	if exists && !reflect.DeepEqual(prior, entry) {
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindIdempotencyConflict, Message: "handoff refusal is immutable"}
	}
	if _, err := s.journal.publish(ctx, journalPublication{OperationID: controljournal.OperationID(request.OperationID), CommandType: "candidate.refuse", ResultType: "handoff-refusal-v1", AggregateKind: "candidate", AggregateID: controljournal.AggregateID(request.HandoffID), EventType: "candidate.refused", Payload: entry, Result: entry}); err != nil {
		return handoff.Refusal{}, err
	}
	s.mu.Lock()
	s.refusals[request.OperationID] = entry
	s.mu.Unlock()
	return refused, nil
}

func (s *workflowStore) InspectHandoffAuthority(_ context.Context, r handoff.Request) (handoff.AuthorityScope, error) {
	s.mu.Lock()
	bound := s.candidates[r.HandoffID].Authority
	s.mu.Unlock()
	capsule, err := bound.capsule()
	if err != nil {
		return handoff.AuthorityScope{}, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "candidate authority is absent"}
	}
	if err := s.runtime.Authorize(capsule, "handoff", r.SubmittedAt); err != nil {
		return handoff.AuthorityScope{}, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "candidate authority is stale", Cause: err}
	}
	binding, ok := s.runtime.Binding(capsule.WorkspaceID)
	if !ok || binding.CanonicalPath == "" {
		return handoff.AuthorityScope{}, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "workspace binding is unavailable"}
	}
	criteria := s.criteria[r.WorkID]
	return handoff.AuthorityScope{RepositoryID: binding.RepositoryID, RepositoryPath: binding.CanonicalPath, OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, LaneID: binding.LaneID, CriteriaDigest: digestJSON(criteria), OperationID: r.OperationID, HandoffID: r.HandoffID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID}, nil
}

func (s *workflowStore) CommitHandoff(ctx context.Context, r handoff.Request, e handoff.CandidateEvidence) (handoff.Record, error) {
	s.mu.Lock()
	authority := s.candidates[r.HandoffID].Authority
	prior, alreadyExists := s.candidates[r.HandoffID]
	s.mu.Unlock()
	contributors, _ := handoff.CanonicalContributors(r.Contributors)
	criteria := append([]string(nil), s.criteria[r.WorkID]...)
	binding, bound := s.runtime.Binding(controljournal.WorkspaceID(r.WorkspaceID))
	if !bound {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "workspace binding disappeared before candidate publication"}
	}
	record := handoff.Record{HandoffID: r.HandoffID, HandoffGeneration: 1, ProjectID: r.ProjectID, MissionID: r.MissionID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, RunID: r.RunID, WorkspaceID: r.WorkspaceID, RepositoryID: s.repository.Binding().RepositoryID, LeaseID: r.LeaseID, LeaseGeneration: r.LeaseGeneration, LaneID: binding.LaneID, Candidate: r.Candidate, PinnedBase: e.PinnedBase, Contributors: contributors, ContributorDigest: digestJSON(contributors), CriteriaDigest: digestJSON(criteria), EvidenceDigest: r.EvidenceDigest, Verification: e, SubmittedAt: r.SubmittedAt.UTC()}
	entry := candidateJournalRecord{Record: record, Authority: authority, Criteria: criteria, CandidateDigest: digestJSON(struct {
		Candidate    string                        `json:"candidate"`
		Evidence     string                        `json:"evidence"`
		Contributors []ids.ActorID                 `json:"contributors"`
		Epoch        controljournal.RuntimeEpochID `json:"epoch"`
	}{record.Candidate, record.EvidenceDigest, contributors, authority.RuntimeEpochID}), CriteriaDigest: record.CriteriaDigest, RequestDigest: digestJSON(r), RuntimeEpochID: authority.RuntimeEpochID}
	if alreadyExists && prior.Record.HandoffID != "" && !reflect.DeepEqual(prior, entry) {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindIdempotencyConflict, Message: "candidate identity is already bound to different immutable evidence"}
	}
	if _, err := s.journal.publish(ctx, journalPublication{OperationID: controljournal.OperationID(r.OperationID), CommandType: "candidate.create", ResultType: "candidate-v1", AggregateKind: "candidate", AggregateID: controljournal.AggregateID(r.HandoffID), EventType: "candidate.created", Payload: entry, Result: entry}); err != nil {
		return handoff.Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.candidates[r.HandoffID]; ok && old.Record.HandoffID != "" && !reflect.DeepEqual(old, entry) {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindIdempotencyConflict, Message: "candidate is immutable"}
	}
	s.candidates[r.HandoffID] = entry
	s.candidateOps[r.OperationID] = r.HandoffID
	return record, nil
}

func (s *workflowStore) LookupReviewOperation(_ context.Context, r basereview.Request) (basereview.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.reviewOps[r.OperationID]
	if !ok {
		return basereview.Record{}, false, nil
	}
	entry := s.reviews[id]
	if entry.RequestDigest != digestJSON(r) {
		return basereview.Record{}, false, &basereview.Error{Kind: basereview.KindIdempotencyConflict, Message: "review operation was reused with changed input"}
	}
	return entry.Record, true, nil
}
func (s *workflowStore) InspectReview(_ context.Context, r basereview.Request) (basereview.Scope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.candidates[r.HandoffID]
	if !ok {
		return basereview.Scope{}, &basereview.Error{Kind: basereview.KindNotFound, Message: "candidate not found"}
	}
	return basereview.Scope{Handoff: c.Record, CriterionIDs: append([]string(nil), c.Criteria...)}, nil
}
func (s *workflowStore) CommitReview(ctx context.Context, r basereview.Request) (basereview.Record, error) {
	s.mu.Lock()
	candidate, ok := s.candidates[r.HandoffID]
	prior, reviewExists := s.reviews[r.ReviewID]
	s.mu.Unlock()
	if !ok {
		return basereview.Record{}, &basereview.Error{Kind: basereview.KindNotFound, Message: "candidate not found"}
	}
	for _, actor := range candidate.Record.Contributors {
		if actor == r.ActorID {
			return basereview.Record{}, &basereview.Error{Kind: basereview.KindReviewerNotIndependent, Message: "review ActorID contributed to the candidate"}
		}
	}
	if r.Candidate != candidate.Record.Candidate || r.WorkRevisionID != candidate.Record.WorkRevisionID || r.WorkID != candidate.Record.WorkID {
		return basereview.Record{}, &basereview.Error{Kind: basereview.KindCandidateMismatch, Message: "review differs from immutable candidate"}
	}
	record := basereview.Record{ReviewID: r.ReviewID, HandoffID: r.HandoffID, WorkRevisionID: r.WorkRevisionID, WorkID: r.WorkID, Candidate: r.Candidate, ReviewerActorID: r.ActorID, ReviewerProcessID: r.ProcessID, Verdict: r.Verdict, Criteria: append([]basereview.CriterionResult(nil), r.Criteria...), EvidenceDigest: r.EvidenceDigest, SubmittedAt: r.SubmittedAt.UTC()}
	entry := reviewJournalRecord{Record: record, CandidateDigest: candidate.CandidateDigest, CriteriaDigest: candidate.CriteriaDigest, RequestDigest: digestJSON(r), RuntimeEpochID: candidate.RuntimeEpochID}
	if reviewExists && !reflect.DeepEqual(prior, entry) {
		return basereview.Record{}, &basereview.Error{Kind: basereview.KindIdempotencyConflict, Message: "review identity is already bound to a different verdict"}
	}
	if _, err := s.journal.publish(ctx, journalPublication{OperationID: controljournal.OperationID(r.OperationID), CommandType: "review.submit", ResultType: "review-v1", AggregateKind: "review", AggregateID: controljournal.AggregateID(r.ReviewID), EventType: "review.recorded", Payload: entry, Result: entry}); err != nil {
		return basereview.Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, exists := s.reviews[r.ReviewID]; exists && !reflect.DeepEqual(old, entry) {
		return basereview.Record{}, &basereview.Error{Kind: basereview.KindIdempotencyConflict, Message: "review is immutable"}
	}
	s.reviews[r.ReviewID] = entry
	s.reviewOps[r.OperationID] = r.ReviewID
	return record, nil
}

func (s *workflowStore) restore(ctx context.Context) error {
	for _, kind := range []controljournal.AggregateKind{"candidate", "review", "integration"} {
		events, err := s.journal.loadAggregateEvents(ctx, kind)
		if err != nil {
			return err
		}
		for _, event := range events {
			switch event.AggregateKind {
			case "candidate":
				switch event.EventType {
				case "candidate.created":
					var x candidateJournalRecord
					if json.Unmarshal(event.Payload(), &x) != nil || x.Record.HandoffID == "" {
						return refusal(OutcomeIndeterminate, "workflow-restore", "candidate event is malformed")
					}
					s.candidates[x.Record.HandoffID] = x
					s.candidateOps[operationFromDigestEvent(event)] = x.Record.HandoffID
				case "candidate.refused":
					var x handoffRefusalJournalRecord
					if json.Unmarshal(event.Payload(), &x) != nil || x.Refusal.OperationID == "" || x.RequestDigest == "" || x.RepositoryPath == "" || x.RuntimeEpochID == "" {
						return refusal(OutcomeIndeterminate, "workflow-restore", "handoff refusal event is malformed")
					}
					if ids.OperationID(event.OperationID) != x.Refusal.OperationID {
						return refusal(OutcomeIndeterminate, "workflow-restore", "handoff refusal operation membership is contradictory")
					}
					s.refusals[x.Refusal.OperationID] = x
				default:
					return refusal(OutcomeIndeterminate, "workflow-restore", "candidate event type is not admitted")
				}
			case "review":
				var x reviewJournalRecord
				if json.Unmarshal(event.Payload(), &x) != nil || x.Record.ReviewID == "" {
					return refusal(OutcomeIndeterminate, "workflow-restore", "review event is malformed")
				}
				s.reviews[x.Record.ReviewID] = x
				s.reviewOps[operationFromDigestEvent(event)] = x.Record.ReviewID
			case "integration":
				if event.EventType == "integration.plan" || event.EventType == "integration.plan_completed" {
					var authority integrationPlanAuthority
					if json.Unmarshal(event.Payload(), &authority) != nil || !authority.complete() {
						return refusal(OutcomeIndeterminate, "workflow-restore", "integration plan event is malformed")
					}
					if event.EventType == "integration.plan" && (authority.Status != planAuthorityActive || controljournal.OperationID(event.OperationID) != authority.Plan.OperationID) {
						return refusal(OutcomeIndeterminate, "workflow-restore", "integration plan acquisition membership is contradictory")
					}
					if event.EventType == "integration.plan_completed" && authority.Status != planAuthorityCompleted {
						return refusal(OutcomeIndeterminate, "workflow-restore", "integration plan completion membership is contradictory")
					}
					if event.EventType == "integration.plan_completed" && controljournal.OperationID(event.OperationID) != derivedOperationID("integration-plan-complete", string(authority.Plan.OperationID), authority.Plan.PlanDigest, authority.CompletedTarget) {
						return refusal(OutcomeIndeterminate, "workflow-restore", "integration plan completion operation is contradictory")
					}
					if event.AggregateID != planAuthorityAggregateID(authority.Plan.RepositoryID, authority.Plan.TargetRef) {
						return refusal(OutcomeIndeterminate, "workflow-restore", "integration plan authority coordinate is contradictory")
					}
					s.plans[authority.Plan.OperationID] = authority.Plan
					s.planAuthorities[event.AggregateID] = authority
					continue
				}
				var x integrationJournalRecord
				if json.Unmarshal(event.Payload(), &x) != nil || x.Record.OperationID == "" {
					return refusal(OutcomeIndeterminate, "workflow-restore", "integration event is malformed")
				}
				s.integrations[x.Record.OperationID] = x
			}
		}
	}
	return nil
}
func operationFromDigestEvent(e controljournal.EventEnvelopeV1) ids.OperationID {
	return ids.OperationID(e.OperationID)
}
func digestJSON(v any) string {
	encoded, _ := json.Marshal(v)
	canonical, err := controljournal.CanonicalJSONObject(encoded)
	if err != nil {
		canonical = encoded
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func copyStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

var _ handoff.Store = (*workflowStore)(nil)
var _ basereview.Store = (*workflowStore)(nil)
