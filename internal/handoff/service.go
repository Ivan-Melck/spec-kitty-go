package handoff

import (
	"context"
	"errors"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type Store interface {
	LookupHandoffOperation(context.Context, Request) (Record, bool, error)
	LookupHandoffRefusal(context.Context, Request) (Refusal, bool, error)
	InspectHandoffAuthority(context.Context, Request) (AuthorityScope, error)
	CommitHandoffRefusal(context.Context, Request, *Error) (Refusal, error)
	CommitHandoff(context.Context, Request, CandidateEvidence) (Record, error)
}
type EligibilityFactStore interface {
	WorkEligibilityFact(context.Context, ids.WorkRevisionID, ids.WorkID, workgraph.WorkStatus, workgraph.EligibilityOperation) (workgraph.EligibilityFact, error)
}
type CandidateVerifier interface {
	Verify(context.Context, AuthorityScope, string) (CandidateEvidence, error)
	RevalidateRefusal(context.Context, Refusal) error
}
type Service struct {
	store       Store
	eligibility EligibilityFactStore
	verifier    CandidateVerifier
	faults      faults.Hook
}

func NewService(store Store, verifier CandidateVerifier) *Service {
	service := &Service{store: store, verifier: verifier, faults: faults.Noop{}}
	if eligibility, ok := any(store).(EligibilityFactStore); ok {
		service.eligibility = eligibility
	}
	return service
}

// WithEligibilityFacts installs the complete mixed-reconciliation reader when
// it is separate from the frozen legacy Store implementation.
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
func (s *Service) Submit(ctx context.Context, request Request) (Record, error) {
	if err := request.Validate(); err != nil {
		return Record{}, err
	}
	if record, found, err := s.store.LookupHandoffOperation(ctx, request); err != nil {
		return Record{}, err
	} else if found {
		return record, nil
	}
	if refusal, found, err := s.store.LookupHandoffRefusal(ctx, request); err != nil {
		return Record{}, err
	} else if found {
		if err := s.verifier.RevalidateRefusal(ctx, refusal); err != nil {
			return Record{}, err
		}
		return Record{}, refusal.Error()
	}
	if err := s.requireEligibility(ctx, request); err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "HR-00"); err != nil {
		return Record{}, err
	}
	scope, err := s.store.InspectHandoffAuthority(ctx, request)
	if err != nil {
		return Record{}, err
	}
	evidence, err := s.verifier.Verify(ctx, scope, request.Candidate)
	if err != nil {
		var mismatch *Error
		if errors.As(err, &mismatch) && mismatch.Cause == nil && mismatch.Kind == KindCandidateMismatch && mismatch.Observed["divergence_kind"] == BranchMovementDivergence && mismatch.Observed["evidence_ref_root"] != "" {
			refusal, commitErr := s.store.CommitHandoffRefusal(ctx, request, mismatch)
			if commitErr != nil {
				return Record{}, commitErr
			}
			return Record{}, refusal.Error()
		}
		return Record{}, err
	}
	if evidence.Candidate != request.Candidate || evidence.PinnedBase != scope.PinnedBase || evidence.OwnedBranch != scope.OwnedBranch || evidence.ObservedBranchHead != request.Candidate {
		return Record{}, &Error{Kind: KindCandidateMismatch, Message: "candidate verification did not bind the expected lane branch and pinned base"}
	}
	if err := s.faults.Reach(ctx, "HR-01"); err != nil {
		return Record{}, err
	}
	record, err := s.store.CommitHandoff(ctx, request, evidence)
	if err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "HR-02"); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Service) requireEligibility(ctx context.Context, request Request) error {
	if s.eligibility == nil {
		return &Error{Kind: KindRevisionIneligible, Message: "complete WorkRevision handoff eligibility reader is unavailable"}
	}
	expected := request.ExpectedEligibility
	if !expected.Complete() || !expected.MatchesComplete(expected.ReconciliationID(), request.WorkRevisionID, expected.NewRevisionID(), request.WorkID, workgraph.StatusActive, expected.Disposition(), workgraph.EligibilityHandoff) {
		return &Error{Kind: KindRevisionIneligible, Message: "complete WorkRevision handoff eligibility is required"}
	}
	fact, err := s.eligibility.WorkEligibilityFact(ctx, request.WorkRevisionID, request.WorkID, workgraph.StatusActive, workgraph.EligibilityHandoff)
	if err != nil {
		return &Error{Kind: KindConflict, Message: "read complete WorkRevision handoff eligibility", Cause: err}
	}
	if !fact.SameComplete(expected) {
		return &Error{Kind: KindRevisionIneligible, Message: "complete WorkRevision handoff eligibility does not match the request"}
	}
	if !fact.Permitted() {
		return &Error{Kind: KindRevisionIneligible, Message: "WorkRevision reconciliation forbids handoff", Observed: map[string]string{"reconciliation_id": fact.ReconciliationID(), "reason": fact.Reason()}, Expected: map[string]string{"operation": string(workgraph.EligibilityHandoff)}}
	}
	return nil
}
