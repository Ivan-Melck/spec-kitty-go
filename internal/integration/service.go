package integration

import (
	"context"
	"errors"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type Store interface {
	LookupIntegrationOperation(context.Context, Request) (Record, bool, error)
	Review(context.Context, ids.ReviewID) (review.Record, error)
	PrepareIntegrationAuthority(context.Context, Request, string, FenceFacts) (Record, error)
	ActivateIntegrationAuthority(context.Context, Record, FenceFacts) (Record, error)
	RecordIntegrationWorkspace(context.Context, Record, WorkspaceFacts) (Record, error)
	RevalidateIntegration(context.Context, Record, string) (Record, error)
	RecordIntegrationResult(context.Context, Record, string, string) (Record, error)
	RecordIntegrationRepair(context.Context, Record, ConflictEvidence, ErrorKind, string) (Record, error)
	CompleteIntegration(context.Context, Record, string) (Record, error)
	PublishIntegrationCleanupEligibility(context.Context, Record, func() error) (Record, error)
	CompleteIntegrationCleanup(context.Context, Record) (Record, error)
}
type EligibilityFactStore interface {
	WorkEligibilityFact(context.Context, ids.WorkRevisionID, ids.WorkID, workgraph.WorkStatus, workgraph.EligibilityOperation) (workgraph.EligibilityFact, error)
}

type Git interface {
	TargetHead(context.Context) (string, error)
	EstablishIntegrationAuthority(context.Context, func(FenceFacts) (Record, error), func(Record, FenceFacts) (Record, error)) (Record, error)
	EnsureIntegrationWorkspace(context.Context, string, string, string) (WorkspaceFacts, error)
	IntegrateCandidate(context.Context, WorkspaceFacts, string, string) (string, *ConflictEvidence, error)
	VerifyIntegrationResult(context.Context, string, string, string) (bool, error)
	PublishIntegrationTarget(context.Context, Record, string, string, func() error) error
	IsAncestor(context.Context, string, string) (bool, error)
	CleanupIntegrationWorkspace(context.Context, Record) error
}

type Service struct {
	store       Store
	eligibility EligibilityFactStore
	git         Git
	faults      faults.Hook
}

func NewService(store Store, git Git) *Service {
	service := &Service{store: store, git: git, faults: faults.Noop{}}
	if eligibility, ok := any(store).(EligibilityFactStore); ok {
		service.eligibility = eligibility
	}
	return service
}

func (s *Service) WithEligibilityFacts(reader EligibilityFactStore) *Service {
	if reader != nil {
		s.eligibility = reader
	}
	return s
}

func (s *Service) WithFaults(hook faults.Hook) *Service {
	if hook != nil {
		s.faults = hook
	}
	return s
}

func (s *Service) Integrate(ctx context.Context, request Request) (Record, error) {
	if err := request.Validate(); err != nil {
		return Record{}, err
	}
	record, found, err := s.store.LookupIntegrationOperation(ctx, request)
	if err != nil {
		return Record{}, err
	}
	if found && record.Status == StatusDone {
		return s.finishCleanup(ctx, record)
	}
	if found && record.Status == StatusRepairRequired {
		return record, repairError(record)
	}
	revisionID, workID, err := s.eligibilityScope(ctx, request, record, found)
	if err != nil {
		return Record{}, err
	}
	if err := s.requireEligibility(ctx, request, revisionID, workID); err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "IG-00"); err != nil {
		return Record{}, err
	}
	if !found {
		record, err = s.git.EstablishIntegrationAuthority(ctx,
			func(currentFence FenceFacts) (Record, error) {
				target, targetErr := s.git.TargetHead(ctx)
				if targetErr != nil {
					return Record{}, targetErr
				}
				return s.store.PrepareIntegrationAuthority(ctx, request, target, currentFence)
			},
			func(intent Record, installed FenceFacts) (Record, error) {
				return s.store.ActivateIntegrationAuthority(ctx, intent, installed)
			},
		)
		if err != nil {
			return Record{}, err
		}
	}
	if record.FenceObject == "" {
		return Record{}, &Error{Kind: KindRepairRequired, Message: "durable integration authority is missing its repository-first fence facts"}
	}
	if err := s.faults.Reach(ctx, "IG-01"); err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "IG-02"); err != nil {
		return Record{}, err
	}

	facts, err := s.git.EnsureIntegrationWorkspace(ctx, record.OperationID.String(), record.PreflightTarget, record.Candidate)
	if err != nil {
		return Record{}, err
	}
	record, err = s.store.RecordIntegrationWorkspace(ctx, record, facts)
	if err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "IG-03"); err != nil {
		return Record{}, err
	}
	resultCommit := record.ResultCommit
	if resultCommit == "" && facts.Clean && !facts.Conflicted && facts.Head != record.PreflightTarget {
		intended, verifyErr := s.git.VerifyIntegrationResult(ctx, facts.Head, record.PreflightTarget, record.Candidate)
		if verifyErr != nil {
			return Record{}, verifyErr
		}
		if !intended {
			evidence := ConflictEvidence{Base: record.PreflightTarget, Candidate: record.Candidate, Result: facts.Head, Workspace: facts.Path}
			record, err = s.store.RecordIntegrationRepair(ctx, record, evidence, KindRepairRequired, "inspect the unexpected clean workspace commit; only an exact intended merge result may be adopted")
			if err != nil {
				return Record{}, err
			}
			return record, repairError(record)
		}
		resultCommit = facts.Head
		if err := s.faults.Reach(ctx, "IG-04"); err != nil {
			return Record{}, err
		}
	}

	current, err := s.git.TargetHead(ctx)
	if err != nil {
		return Record{}, err
	}
	if resultCommit != "" {
		integrated, ancestryErr := s.git.IsAncestor(ctx, resultCommit, current)
		if ancestryErr != nil {
			return Record{}, ancestryErr
		}
		if integrated {
			return s.recordResultDoneAndCleanup(ctx, record, resultCommit, current)
		}
	}
	if facts.Conflicted || !facts.Clean {
		evidence := ConflictEvidence{Base: record.PreflightTarget, Target: current, Candidate: record.Candidate, Result: record.ResultCommit, Workspace: facts.Path}
		record, err = s.store.RecordIntegrationRepair(ctx, record, evidence, KindRepairRequired, "inspect the preserved managed integration workspace; resolve explicitly or abandon this attempt")
		if err != nil {
			return Record{}, err
		}
		return record, repairError(record)
	}
	if current != record.PreflightTarget {
		evidence := ConflictEvidence{Base: record.PreflightTarget, Target: current, Candidate: record.Candidate, Result: record.ResultCommit, Workspace: facts.Path}
		record, err = s.store.RecordIntegrationRepair(ctx, record, evidence, KindTargetMoved, "revalidate the candidate against the new target in a new integration operation")
		if err != nil {
			return Record{}, err
		}
		return record, repairError(record)
	}
	record, err = s.store.RevalidateIntegration(ctx, record, current)
	if err != nil {
		return Record{}, err
	}
	if resultCommit == "" {
		result, conflict, integrateErr := s.git.IntegrateCandidate(ctx, facts, record.PreflightTarget, record.Candidate)
		if integrateErr != nil {
			if conflict != nil {
				record, err = s.store.RecordIntegrationRepair(ctx, record, *conflict, KindConflict, "resolve the preserved conflict workspace and submit an explicitly reviewed candidate")
				if err != nil {
					return Record{}, err
				}
				return record, repairError(record)
			}
			return Record{}, integrateErr
		}
		resultCommit = result
		if err := s.faults.Reach(ctx, "IG-04"); err != nil {
			return Record{}, err
		}
	}

	current, err = s.git.TargetHead(ctx)
	if err != nil {
		return Record{}, err
	}
	if current != record.PreflightTarget {
		integrated, ancestryErr := s.git.IsAncestor(ctx, resultCommit, current)
		if ancestryErr != nil {
			return Record{}, ancestryErr
		}
		if integrated {
			return s.recordResultDoneAndCleanup(ctx, record, resultCommit, current)
		}
		evidence := ConflictEvidence{Base: record.PreflightTarget, Target: current, Candidate: record.Candidate, Result: resultCommit, Workspace: record.WorkspacePath}
		record, err = s.store.RecordIntegrationRepair(ctx, record, evidence, KindTargetMoved, "revalidate the candidate against the new target in a new integration operation")
		if err != nil {
			return Record{}, err
		}
		return record, repairError(record)
	}
	record, err = s.store.RevalidateIntegration(ctx, record, current)
	if err != nil {
		return Record{}, err
	}
	if err := s.git.PublishIntegrationTarget(ctx, record, record.PreflightTarget, resultCommit, func() error {
		if err := s.faults.Reach(ctx, "IG-05"); err != nil {
			return err
		}
		_, err := s.store.RevalidateIntegration(ctx, record, record.PreflightTarget)
		return err
	}); err != nil {
		if errors.Is(err, faults.ErrCrashRequested) {
			return Record{}, err
		}
		observed, probeErr := s.git.TargetHead(ctx)
		if probeErr != nil {
			return Record{}, errors.Join(err, probeErr)
		}
		if _, authorityErr := s.store.RevalidateIntegration(ctx, record, observed); authorityErr != nil {
			return Record{}, authorityErr
		}
		evidence := ConflictEvidence{Base: record.PreflightTarget, Target: observed, Candidate: record.Candidate, Result: resultCommit, Workspace: record.WorkspacePath, GitMessage: err.Error()}
		record, storeErr := s.store.RecordIntegrationRepair(ctx, record, evidence, KindTargetMoved, "inspect target movement and retry only with a newly validated expected-old target")
		if storeErr != nil {
			return Record{}, errors.Join(err, storeErr)
		}
		return record, repairError(record)
	}
	if err := s.faults.Reach(ctx, "IG-06"); err != nil {
		return Record{}, err
	}
	observed, err := s.git.TargetHead(ctx)
	if err != nil {
		return Record{}, err
	}
	integrated, err := s.git.IsAncestor(ctx, resultCommit, observed)
	if err != nil {
		return Record{}, err
	}
	if !integrated {
		return Record{}, &Error{Kind: KindRepairRequired, Message: "target does not contain the intended result after ref update", Observed: map[string]string{"target": observed}, Expected: map[string]string{"contains": resultCommit}}
	}
	return s.recordResultDoneAndCleanup(ctx, record, resultCommit, observed)
}

func (s *Service) eligibilityScope(ctx context.Context, request Request, record Record, found bool) (ids.WorkRevisionID, ids.WorkID, error) {
	if found {
		if record.ReviewID != request.ReviewID || record.Candidate != request.Candidate {
			return "", "", &Error{Kind: KindNotApproved, Message: "integration operation differs from its immutable approved review"}
		}
		return record.WorkRevisionID, record.WorkID, nil
	}
	approved, err := s.store.Review(ctx, request.ReviewID)
	if err != nil {
		return "", "", &Error{Kind: KindNotApproved, Message: "read immutable approved review", Cause: err}
	}
	if approved.ReviewID != request.ReviewID || approved.Verdict != review.Approved || approved.WorkRevisionID == "" || approved.WorkID == "" || approved.Candidate != request.Candidate {
		return "", "", &Error{Kind: KindNotApproved, Message: "integration requires the exact immutable approved review"}
	}
	return approved.WorkRevisionID, approved.WorkID, nil
}

func (s *Service) requireEligibility(ctx context.Context, request Request, revisionID ids.WorkRevisionID, workID ids.WorkID) error {
	if s.eligibility == nil {
		return &Error{Kind: KindRevisionIneligible, Message: "complete WorkRevision integration eligibility reader is unavailable"}
	}
	expected := request.ExpectedEligibility
	if !expected.Complete() || !expected.MatchesComplete(expected.ReconciliationID(), revisionID, expected.NewRevisionID(), workID, workgraph.StatusApproved, expected.Disposition(), workgraph.EligibilityIntegration) {
		return &Error{Kind: KindRevisionIneligible, Message: "complete WorkRevision integration eligibility is required"}
	}
	fact, err := s.eligibility.WorkEligibilityFact(ctx, revisionID, workID, workgraph.StatusApproved, workgraph.EligibilityIntegration)
	if err != nil {
		return &Error{Kind: KindStoreFailure, Message: "read complete WorkRevision integration eligibility", Cause: err}
	}
	if !fact.SameComplete(expected) {
		return &Error{Kind: KindRevisionIneligible, Message: "complete WorkRevision integration eligibility does not match the approved review"}
	}
	if !fact.Permitted() {
		return &Error{Kind: KindRevisionIneligible, Message: "WorkRevision reconciliation forbids integration", Observed: map[string]string{"reconciliation_id": fact.ReconciliationID(), "reason": fact.Reason()}, Expected: map[string]string{"operation": string(workgraph.EligibilityIntegration)}}
	}
	return nil
}

func (s *Service) recordResultDoneAndCleanup(ctx context.Context, record Record, resultCommit, observed string) (Record, error) {
	record, err := s.store.RecordIntegrationResult(ctx, record, resultCommit, observed)
	if err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "IG-07"); err != nil {
		return Record{}, err
	}
	record, err = s.store.CompleteIntegration(ctx, record, observed)
	if err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "IG-08"); err != nil {
		return Record{}, err
	}
	return s.finishCleanup(ctx, record)
}

func (s *Service) finishCleanup(ctx context.Context, record Record) (Record, error) {
	if record.CleanupStatus == CleanupComplete {
		return record, nil
	}
	var err error
	record, err = s.store.PublishIntegrationCleanupEligibility(ctx, record, func() error {
		return s.faults.Reach(ctx, "IG-09")
	})
	if err != nil {
		return Record{}, err
	}
	if err := s.git.CleanupIntegrationWorkspace(ctx, record); err != nil {
		return record, err
	}
	return s.store.CompleteIntegrationCleanup(ctx, record)
}

func repairError(record Record) error {
	kind := KindRepairRequired
	if record.RepairKind != "" {
		kind = ErrorKind(record.RepairKind)
	}
	return &Error{Kind: kind, Message: "integration requires explicit repair", Observed: map[string]string{
		"base": record.Evidence.Base, "target": record.Evidence.Target, "candidate": record.Evidence.Candidate,
		"result": record.Evidence.Result, "workspace": record.Evidence.Workspace,
	}, Expected: map[string]string{"repair_action": record.RepairAction}}
}
