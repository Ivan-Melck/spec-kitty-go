package provingground

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	baseintegration "github.com/spec-kitty/go-kitty3/internal/integration"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	"golang.org/x/sys/unix"
)

type integrationJournalRecord struct {
	Record          baseintegration.Record        `json:"record"`
	RequestDigest   string                        `json:"request_digest"`
	CandidateDigest string                        `json:"candidate_digest"`
	CriteriaDigest  string                        `json:"criteria_digest"`
	RuntimeEpochID  controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	Authority       authorityEvidence             `json:"authority"`
	Sequence        uint64                        `json:"sequence"`
}

type IntegrationCoordinator struct {
	reviews *ReviewCoordinator
	service *baseintegration.Service
	faults  faults.Hook
}
type IntegrationItem struct {
	Capsule controljournal.AuthorityCapsuleV1
	Request baseintegration.Request
}
type IntegrationPlan struct {
	OperationID      controljournal.OperationID    `json:"operation_id"`
	PlanDigest       string                        `json:"plan_digest"`
	CandidateDigests []string                      `json:"candidate_digests"`
	Candidates       []string                      `json:"candidates"`
	ItemOperations   []ids.OperationID             `json:"item_operations"`
	TargetRef        string                        `json:"target_ref"`
	RepositoryID     ids.RepositoryID              `json:"repository_id"`
	WorkRevisionID   ids.WorkRevisionID            `json:"work_revision_id"`
	RuntimeEpochID   controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	ExpectedTarget   string                        `json:"expected_target_head"`
	CreatedAt        time.Time                     `json:"created_at"`
}

const (
	planAuthorityActive    = "active"
	planAuthorityCompleted = "completed"
)

type integrationPlanAuthority struct {
	SchemaVersion   uint16          `json:"schema_version"`
	Plan            IntegrationPlan `json:"plan"`
	Status          string          `json:"status"`
	CompletedTarget string          `json:"completed_target,omitempty"`
}

func (a integrationPlanAuthority) complete() bool {
	if a.SchemaVersion != 1 || !a.Plan.complete() {
		return false
	}
	return (a.Status == planAuthorityActive && a.CompletedTarget == "") || (a.Status == planAuthorityCompleted && a.CompletedTarget != "")
}

func (p IntegrationPlan) complete() bool {
	if p.OperationID == "" || p.PlanDigest == "" || p.TargetRef == "" || p.RepositoryID == "" || p.WorkRevisionID == "" || p.RuntimeEpochID == "" || p.ExpectedTarget == "" || p.CreatedAt.IsZero() || len(p.CandidateDigests) == 0 || len(p.CandidateDigests) != len(p.Candidates) || len(p.Candidates) != len(p.ItemOperations) {
		return false
	}
	want := p
	want.PlanDigest = ""
	return p.PlanDigest == digestJSON(want)
}

func NewIntegrationCoordinator(reviews *ReviewCoordinator) (*IntegrationCoordinator, error) {
	if reviews == nil || reviews.store == nil || reviews.repository == nil {
		return nil, refusal(OutcomeInvalidCommand, "integration", "review coordinator and real Git repository are required")
	}
	return &IntegrationCoordinator{reviews: reviews, service: baseintegration.NewService(reviews.store, reviews.repository), faults: faults.Noop{}}, nil
}
func (c *IntegrationCoordinator) WithFaults(h faults.Hook) *IntegrationCoordinator {
	if h != nil {
		c.faults = h
		c.service.WithFaults(h)
	}
	return c
}

func (c *IntegrationCoordinator) Integrate(ctx context.Context, item IntegrationItem) (baseintegration.Record, error) {
	results, err := c.IntegrateApproved(ctx, []IntegrationItem{item})
	if len(results) == 0 {
		return baseintegration.Record{}, err
	}
	return results[0], err
}

// IntegrateApproved freezes and journals the exact candidate set before any
// repository fence, then publishes it in deterministic WorkID order. The
// existing integration service and Git adapter retain all mutation authority.
func (c *IntegrationCoordinator) IntegrateApproved(ctx context.Context, items []IntegrationItem) ([]baseintegration.Record, error) {
	if len(items) == 0 {
		return nil, refusal(OutcomeInvalidCommand, "integration-plan", "at least one approved candidate is required")
	}
	var results []baseintegration.Record
	err := c.withPlanAuthority(ctx, func() error {
		var integrateErr error
		results, integrateErr = c.integrateApprovedLocked(ctx, items)
		return integrateErr
	})
	return results, err
}

func (c *IntegrationCoordinator) integrateApprovedLocked(ctx context.Context, items []IntegrationItem) ([]baseintegration.Record, error) {
	for _, item := range items {
		if err := c.prevalidate(item); err != nil {
			return nil, err
		}
	}
	ordered := append([]IntegrationItem(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		ri, _ := c.reviews.Review(ordered[i].Request.ReviewID)
		rj, _ := c.reviews.Review(ordered[j].Request.ReviewID)
		if ri.WorkID != rj.WorkID {
			return ri.WorkID < rj.WorkID
		}
		return ordered[i].Request.Candidate < ordered[j].Request.Candidate
	})

	digests := make([]string, len(ordered))
	candidates := make([]string, len(ordered))
	operations := make([]ids.OperationID, len(ordered))
	seenOperations := make(map[ids.OperationID]struct{}, len(ordered))
	for i, item := range ordered {
		reviewRecord, _ := c.reviews.Review(item.Request.ReviewID)
		c.reviews.store.mu.Lock()
		candidate := c.reviews.store.candidates[reviewRecord.HandoffID]
		c.reviews.store.mu.Unlock()
		digests[i], candidates[i], operations[i] = candidate.CandidateDigest, item.Request.Candidate, item.Request.OperationID
		if item.Request.TargetRef != ordered[0].Request.TargetRef || item.Request.RepositoryID != ordered[0].Request.RepositoryID || reviewRecord.WorkRevisionID != ids.WorkRevisionID(c.reviews.runtime.core.config.WorkRevisionID) {
			return nil, refusal(OutcomeInvalidCommand, "integration-plan", "every candidate must share the exact target, repository, and WorkRevision")
		}
		if _, duplicate := seenOperations[operations[i]]; duplicate {
			return nil, refusal(OutcomeInvalidCommand, "integration-plan", "integration item operations must be unique")
		}
		seenOperations[operations[i]] = struct{}{}
	}
	identity := struct {
		CandidateDigests []string                      `json:"candidate_digests"`
		Candidates       []string                      `json:"candidates"`
		ItemOperations   []ids.OperationID             `json:"item_operations"`
		TargetRef        string                        `json:"target_ref"`
		RepositoryID     ids.RepositoryID              `json:"repository_id"`
		WorkRevisionID   ids.WorkRevisionID            `json:"work_revision_id"`
		RuntimeEpochID   controljournal.RuntimeEpochID `json:"runtime_epoch_id"`
	}{digests, candidates, operations, ordered[0].Request.TargetRef, ordered[0].Request.RepositoryID, ids.WorkRevisionID(c.reviews.runtime.core.config.WorkRevisionID), c.reviews.runtime.core.currentEpoch()}
	operationID := derivedOperationID("integration-plan", digestJSON(identity))

	authorityID := planAuthorityAggregateID(identity.RepositoryID, identity.TargetRef)
	c.reviews.store.mu.Lock()
	authority, authorityExists := c.reviews.store.planAuthorities[authorityID]
	historicPlan, historicKnown := c.reviews.store.plans[operationID]
	c.reviews.store.mu.Unlock()
	if historicKnown && (!authorityExists || authority.Plan.OperationID != operationID) {
		if !planMatchesIdentity(historicPlan, digests, candidates, operations, identity.TargetRef, identity.RepositoryID, identity.WorkRevisionID, identity.RuntimeEpochID) {
			return nil, refusal(OutcomeAuthorityConflict, "integration-plan", "historic integration plan identity was reused with changed input")
		}
		results := make([]baseintegration.Record, 0, len(ordered))
		for _, item := range ordered {
			c.reviews.store.mu.Lock()
			entry, ok := c.reviews.store.integrations[item.Request.OperationID]
			c.reviews.store.mu.Unlock()
			if !ok || entry.Record.Status != baseintegration.StatusDone || entry.RequestDigest != digestJSON(item.Request) {
				return results, &baseintegration.Error{Kind: baseintegration.KindRepairRequired, Message: "historic completed plan lacks its exact durable item outcome"}
			}
			results = append(results, entry.Record)
		}
		return results, nil
	}
	if authorityExists && authority.Status == planAuthorityActive && authority.Plan.OperationID != operationID {
		return nil, &baseintegration.Error{Kind: baseintegration.KindAuthorityBusy, Message: "another immutable integration plan remains active after its last durable transition", Observed: map[string]string{"active_plan": authority.Plan.PlanDigest}, Expected: map[string]string{"plan": digestJSON(identity)}}
	}
	plan := authority.Plan
	recoveringExactPlan := authorityExists && authority.Plan.OperationID == operationID
	if !recoveringExactPlan {
		target, err := c.reviews.repository.TargetHead(ctx)
		if err != nil {
			return nil, err
		}
		plan = IntegrationPlan{OperationID: operationID, CandidateDigests: digests, Candidates: candidates, ItemOperations: operations, TargetRef: identity.TargetRef, RepositoryID: identity.RepositoryID, WorkRevisionID: identity.WorkRevisionID, RuntimeEpochID: identity.RuntimeEpochID, ExpectedTarget: target, CreatedAt: ordered[0].Request.StartedAt.UTC()}
		plan.PlanDigest = digestJSON(plan)
		authority = integrationPlanAuthority{SchemaVersion: 1, Plan: plan, Status: planAuthorityActive}
		if _, err := c.reviews.journal.publish(ctx, journalPublication{OperationID: operationID, CommandType: "integration.plan", ResultType: "integration-plan-authority-v1", AggregateKind: "integration", AggregateID: authorityID, EventType: "integration.plan", Payload: authority, Result: authority}); err != nil {
			return nil, err
		}
		c.reviews.store.mu.Lock()
		c.reviews.store.plans[operationID] = plan
		c.reviews.store.planAuthorities[authorityID] = authority
		c.reviews.store.mu.Unlock()
	} else {
		if !authority.complete() || !planMatchesIdentity(plan, digests, candidates, operations, identity.TargetRef, identity.RepositoryID, identity.WorkRevisionID, identity.RuntimeEpochID) {
			return nil, refusal(OutcomeAuthorityConflict, "integration-plan", "integration plan identity was reused with changed input")
		}
	}
	if err := c.faults.Reach(ctx, "IGP-01"); err != nil {
		return nil, err
	}

	expectedTarget := plan.ExpectedTarget
	results := make([]baseintegration.Record, 0, len(ordered))
	for _, item := range ordered {
		c.reviews.store.mu.Lock()
		prior, known := c.reviews.store.integrations[item.Request.OperationID]
		c.reviews.store.mu.Unlock()
		if known && prior.Record.Status == baseintegration.StatusDone {
			if prior.RequestDigest != digestJSON(item.Request) || prior.Record.PreflightTarget != expectedTarget || prior.Record.Candidate != item.Request.Candidate || prior.Record.ResultCommit == "" {
				return results, &baseintegration.Error{Kind: baseintegration.KindRepairRequired, Message: "completed integration contradicts the immutable plan prefix"}
			}
			expectedTarget = prior.Record.ResultCommit
			results = append(results, prior.Record)
			continue
		}
		current, err := c.reviews.repository.TargetHead(ctx)
		if err != nil {
			return results, err
		}
		// A durable nonterminal item may be in the response-loss window where
		// Git already moved but the result was not recorded. Delegate only that
		// exact operation to integration.Service, which verifies workspace,
		// ancestry, candidate, and target facts before adopting completion.
		allowedPublishedRecovery := known && prior.Record.Status != baseintegration.StatusRepairRequired
		if current != expectedTarget && !allowedPublishedRecovery {
			return results, &baseintegration.Error{Kind: baseintegration.KindTargetMoved, Message: "integration target moved outside the exact plan authority", Observed: map[string]string{"target_head": current}, Expected: map[string]string{"target_head": expectedTarget}}
		}
		record, err := c.service.Integrate(ctx, item.Request)
		if err != nil {
			return results, err
		}
		if record.Status != baseintegration.StatusDone || record.PreflightTarget != expectedTarget || record.ResultCommit == "" {
			return results, &baseintegration.Error{Kind: baseintegration.KindRepairRequired, Message: "integration result does not extend the exact plan prefix"}
		}
		expectedTarget = record.ResultCommit
		results = append(results, record)
		if err := c.faults.Reach(ctx, "IGP-02"); err != nil {
			return results, err
		}
	}
	current, err := c.reviews.repository.TargetHead(ctx)
	if err != nil {
		return results, err
	}
	if current != expectedTarget {
		return results, &baseintegration.Error{Kind: baseintegration.KindTargetMoved, Message: "completed integration plan does not own the observed target", Observed: map[string]string{"target_head": current}, Expected: map[string]string{"target_head": expectedTarget}}
	}
	completed := integrationPlanAuthority{SchemaVersion: 1, Plan: plan, Status: planAuthorityCompleted, CompletedTarget: expectedTarget}
	completionOperation := derivedOperationID("integration-plan-complete", string(plan.OperationID), plan.PlanDigest, expectedTarget)
	if _, err := c.reviews.journal.publish(ctx, journalPublication{OperationID: completionOperation, CommandType: "integration.plan.complete", ResultType: "integration-plan-authority-v1", AggregateKind: "integration", AggregateID: authorityID, EventType: "integration.plan_completed", Payload: completed, Result: completed}); err != nil {
		return results, err
	}
	c.reviews.store.mu.Lock()
	c.reviews.store.planAuthorities[authorityID] = completed
	c.reviews.store.mu.Unlock()
	return results, nil
}

func planMatchesIdentity(plan IntegrationPlan, digests, candidates []string, operations []ids.OperationID, targetRef string, repositoryID ids.RepositoryID, revisionID ids.WorkRevisionID, epochID controljournal.RuntimeEpochID) bool {
	want := plan
	want.CandidateDigests, want.Candidates, want.ItemOperations = digests, candidates, operations
	want.TargetRef, want.RepositoryID, want.WorkRevisionID, want.RuntimeEpochID = targetRef, repositoryID, revisionID, epochID
	want.PlanDigest = ""
	want.PlanDigest = digestJSON(want)
	return plan.complete() && reflect.DeepEqual(plan, want)
}

func planAuthorityAggregateID(repositoryID ids.RepositoryID, targetRef string) controljournal.AggregateID {
	return controljournal.AggregateID("plan-authority-" + digestJSON(struct {
		RepositoryID ids.RepositoryID `json:"repository_id"`
		TargetRef    string           `json:"target_ref"`
	}{repositoryID, targetRef})[:32])
}

// withPlanAuthority is the repository-scoped, cross-process critical section
// for an exact candidate set. The durable IntegrationPlan records its facts;
// this flock prevents another Spec Kitty plan or singleton integration from
// interleaving between candidate publications.
func (c *IntegrationCoordinator) withPlanAuthority(ctx context.Context, run func() error) error {
	binding := c.reviews.repository.Binding()
	name := "spec-kitty-integration-plan-" + digestJSON(struct {
		RepositoryID ids.RepositoryID `json:"repository_id"`
		TargetRef    string           `json:"target_ref"`
	}{binding.RepositoryID, binding.TargetRef})[:32] + ".lock"
	lock, err := os.OpenFile(filepath.Join(binding.CommonDir, name), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return &baseintegration.Error{Kind: baseintegration.KindGitFailure, Message: "open integration-plan authority lock", Cause: err}
	}
	defer lock.Close()
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return &baseintegration.Error{Kind: baseintegration.KindGitFailure, Message: "acquire integration-plan authority lock", Cause: err}
		}
		select {
		case <-ctx.Done():
			return &baseintegration.Error{Kind: baseintegration.KindGitFailure, Message: "wait for integration-plan authority lock", Cause: ctx.Err()}
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	return run()
}

func (c *IntegrationCoordinator) prevalidate(item IntegrationItem) error {
	if err := item.Request.Validate(); err != nil {
		return err
	}
	reviewRecord, ok := c.reviews.Review(item.Request.ReviewID)
	if !ok || reviewRecord.Verdict != basereview.Approved || reviewRecord.Candidate != item.Request.Candidate {
		return &baseintegration.Error{Kind: baseintegration.KindNotApproved, Message: "exact approved review is required before repository fencing"}
	}
	c.reviews.store.mu.Lock()
	candidate, exists := c.reviews.store.candidates[reviewRecord.HandoffID]
	c.reviews.store.mu.Unlock()
	if !exists || candidate.Authority != evidenceFromCapsule(item.Capsule) {
		return &baseintegration.Error{Kind: baseintegration.KindNotApproved, Message: "approved candidate capsule is absent or changed"}
	}
	if err := c.reviews.runtime.Authorize(item.Capsule, "integration", item.Request.StartedAt); err != nil {
		return err
	}
	return nil
}

func (s *workflowStore) LookupIntegrationOperation(_ context.Context, r baseintegration.Request) (baseintegration.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.integrations[r.OperationID]
	if !ok {
		return baseintegration.Record{}, false, nil
	}
	if entry.RequestDigest != digestJSON(r) {
		return baseintegration.Record{}, false, &baseintegration.Error{Kind: baseintegration.KindIdempotencyConflict, Message: "integration operation was reused with changed input"}
	}
	return entry.Record, true, nil
}
func (s *workflowStore) Review(_ context.Context, id ids.ReviewID) (basereview.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.reviews[id]
	if !ok {
		return basereview.Record{}, &baseintegration.Error{Kind: baseintegration.KindNotApproved, Message: "review not found"}
	}
	return entry.Record, nil
}

func (s *workflowStore) PrepareIntegrationAuthority(ctx context.Context, r baseintegration.Request, target string, current baseintegration.FenceFacts) (baseintegration.Record, error) {
	s.mu.Lock()
	reviewEntry, ok := s.reviews[r.ReviewID]
	candidate := s.candidates[reviewEntry.Record.HandoffID]
	prior, priorKnown := s.integrations[current.OperationID]
	for operation, existing := range s.integrations {
		if operation != r.OperationID && existing.Record.Status == baseintegration.StatusDone && existing.Record.RepositoryID == r.RepositoryID && existing.Record.TargetRef == r.TargetRef && existing.Record.Candidate == r.Candidate {
			s.mu.Unlock()
			return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindIdempotencyConflict, Message: "approved candidate was already published by another integration operation"}
		}
	}
	s.mu.Unlock()
	if !ok || reviewEntry.Record.Verdict != basereview.Approved || reviewEntry.Record.Candidate != r.Candidate {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindNotApproved, Message: "immutable approved review is required"}
	}
	if current.Object != "" && (!priorKnown || prior.Record.Status != baseintegration.StatusDone) && current.Deadline.After(r.StartedAt) {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindAuthorityBusy, Message: "repository target authority is held by another live operation"}
	}
	generation := current.Generation + 1
	token := digestJSON(struct {
		Operation  ids.OperationID `json:"operation"`
		Generation int64           `json:"generation"`
		Target     string          `json:"target"`
		Candidate  string          `json:"candidate"`
	}{r.OperationID, generation, target, r.Candidate})
	fenceDigest := digestJSON(struct {
		Request     baseintegration.Request `json:"request"`
		Target      string                  `json:"target"`
		Predecessor string                  `json:"predecessor"`
	}{r, target, current.Object})
	record := baseintegration.Record{OperationID: r.OperationID, ReviewID: r.ReviewID, HandoffID: reviewEntry.Record.HandoffID, MissionID: ids.MissionID(s.runtime.core.config.MissionID), WorkRevisionID: reviewEntry.Record.WorkRevisionID, WorkID: reviewEntry.Record.WorkID, RepositoryID: r.RepositoryID, TargetRef: r.TargetRef, ActorID: r.ActorID, ProcessID: r.ProcessID, Candidate: r.Candidate, PreflightTarget: target, MutationGeneration: generation, FenceRef: current.Ref, FencePredecessor: current.Object, FenceRequestDigest: fenceDigest, Token: token, LeaseDeadline: r.StartedAt.Add(r.LeaseTTL).UTC(), Status: baseintegration.StatusAuthorizing, StartedAt: r.StartedAt.UTC(), CleanupStatus: baseintegration.CleanupPending}
	entry := integrationJournalRecord{Record: record, RequestDigest: digestJSON(r), CandidateDigest: candidate.CandidateDigest, CriteriaDigest: candidate.CriteriaDigest, RuntimeEpochID: candidate.RuntimeEpochID, Authority: candidate.Authority, Sequence: 1}
	if err := s.publishIntegration(ctx, entry, "integration.intent"); err != nil {
		return baseintegration.Record{}, err
	}
	s.mu.Lock()
	s.integrations[r.OperationID] = entry
	s.mu.Unlock()
	return record, nil
}

func (s *workflowStore) ActivateIntegrationAuthority(ctx context.Context, record baseintegration.Record, fence baseintegration.FenceFacts) (baseintegration.Record, error) {
	if fence.OperationID != record.OperationID || fence.Generation != record.MutationGeneration || fence.PreflightTarget != record.PreflightTarget || fence.RequestDigest != record.FenceRequestDigest {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindAuthorityBusy, Message: "installed repository fence differs from durable intent"}
	}
	record.FenceRef, record.FenceObject = fence.Ref, fence.Object
	record.Status = baseintegration.StatusPrepared
	return s.updateIntegration(ctx, record, "integration.activated")
}
func (s *workflowStore) RecordIntegrationWorkspace(ctx context.Context, record baseintegration.Record, facts baseintegration.WorkspaceFacts) (baseintegration.Record, error) {
	if !facts.Registered || !facts.Detached || !facts.CandidateReachable {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindRepairRequired, Message: "integration workspace facts are incomplete"}
	}
	record.WorkspacePath = facts.Path
	return s.updateIntegration(ctx, record, "integration.workspace")
}
func (s *workflowStore) RevalidateIntegration(ctx context.Context, record baseintegration.Record, target string) (baseintegration.Record, error) {
	s.mu.Lock()
	reviewEntry, rok := s.reviews[record.ReviewID]
	entry, ok := s.integrations[record.OperationID]
	candidate := s.candidates[reviewEntry.Record.HandoffID]
	s.mu.Unlock()
	if !ok || !rok || reviewEntry.Record.Verdict != basereview.Approved || reviewEntry.Record.Candidate != record.Candidate || entry.CandidateDigest != candidate.CandidateDigest {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindNotApproved, Message: "review or candidate changed before publication"}
	}
	capsule, err := entry.Authority.capsule()
	if err != nil || s.runtime.Authorize(capsule, "integration-publication", record.StartedAt) != nil {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindNotApproved, Message: "candidate capsule is stale before publication", Cause: err}
	}
	if target != record.PreflightTarget {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindTargetMoved, Message: "target moved after preflight", Observed: map[string]string{"target": target}, Expected: map[string]string{"target": record.PreflightTarget}}
	}
	return s.updateIntegration(ctx, record, "integration.revalidated")
}
func (s *workflowStore) RecordIntegrationResult(ctx context.Context, record baseintegration.Record, result, observed string) (baseintegration.Record, error) {
	record.ResultCommit, record.ObservedTarget, record.Status = result, observed, baseintegration.StatusResultReady
	return s.updateIntegration(ctx, record, "integration.result")
}
func (s *workflowStore) RecordIntegrationRepair(ctx context.Context, record baseintegration.Record, e baseintegration.ConflictEvidence, kind baseintegration.ErrorKind, action string) (baseintegration.Record, error) {
	record.Status, record.RepairKind, record.RepairAction, record.Evidence = baseintegration.StatusRepairRequired, string(kind), boundedDiagnostic(action), e
	return s.updateIntegration(ctx, record, "integration.repair")
}
func (s *workflowStore) CompleteIntegration(ctx context.Context, record baseintegration.Record, target string) (baseintegration.Record, error) {
	record.Status, record.ObservedTarget, record.DoneAt = baseintegration.StatusDone, target, record.StartedAt.Add(7*time.Nanosecond).UTC()
	return s.updateIntegration(ctx, record, "integration.completed")
}
func (s *workflowStore) PublishIntegrationCleanupEligibility(ctx context.Context, record baseintegration.Record, before func() error) (baseintegration.Record, error) {
	if before != nil {
		if err := before(); err != nil {
			return baseintegration.Record{}, err
		}
	}
	record.CleanupStatus, record.CleanupEligibleAt = baseintegration.CleanupEligible, record.StartedAt.Add(8*time.Nanosecond).UTC()
	return s.updateIntegration(ctx, record, "integration.cleanup_eligible")
}
func (s *workflowStore) CompleteIntegrationCleanup(ctx context.Context, record baseintegration.Record) (baseintegration.Record, error) {
	record.CleanupStatus, record.CleanupCompletedAt = baseintegration.CleanupComplete, record.StartedAt.Add(9*time.Nanosecond).UTC()
	return s.updateIntegration(ctx, record, "integration.cleanup_completed")
}

func (s *workflowStore) updateIntegration(ctx context.Context, record baseintegration.Record, event string) (baseintegration.Record, error) {
	s.mu.Lock()
	entry, ok := s.integrations[record.OperationID]
	s.mu.Unlock()
	if !ok {
		return baseintegration.Record{}, &baseintegration.Error{Kind: baseintegration.KindStoreFailure, Message: "integration intent is absent"}
	}
	entry.Record = record
	entry.Sequence++
	if err := s.publishIntegration(ctx, entry, event); err != nil {
		return baseintegration.Record{}, err
	}
	s.mu.Lock()
	s.integrations[record.OperationID] = entry
	s.mu.Unlock()
	return record, nil
}
func (s *workflowStore) publishIntegration(ctx context.Context, entry integrationJournalRecord, event string) error {
	op := derivedOperationID("integration-transition", entry.Record.OperationID.String(), event, fmt.Sprint(entry.Sequence), digestJSON(entry.Record))
	_, err := s.journal.publish(ctx, journalPublication{OperationID: op, CommandType: event, ResultType: "integration-v1", AggregateKind: "integration", AggregateID: controljournal.AggregateID(entry.Record.OperationID), EventType: controljournal.EventType(event), Payload: entry, Result: entry})
	return err
}

func IntegrationAttributionCampaign(ctx context.Context, repetitions int, trial func(context.Context, int) error) error {
	return RunFocusedCampaign(ctx, repetitions, trial)
}
func CompetingIntegrationCampaign(ctx context.Context, repetitions int, trial func(context.Context, int) error) error {
	return RunFocusedCampaign(ctx, repetitions, trial)
}

var _ baseintegration.Store = (*workflowStore)(nil)
