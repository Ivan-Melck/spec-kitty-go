package review

import (
	"context"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type Store interface {
	LookupReviewOperation(context.Context, Request) (Record, bool, error)
	WorkEligibility(context.Context, ids.WorkRevisionID, ids.WorkID, workgraph.WorkStatus) (workgraph.Consequence, string, error)
	InspectReview(context.Context, Request) (Scope, error)
	CommitReview(context.Context, Request) (Record, error)
}
type Service struct {
	store  Store
	faults faults.Hook
}

func NewService(store Store) *Service { return &Service{store: store, faults: faults.Noop{}} }
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
	if record, found, err := s.store.LookupReviewOperation(ctx, request); err != nil {
		return Record{}, err
	} else if found {
		return record, nil
	}
	if err := s.requireEligibility(ctx, request); err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "HR-03"); err != nil {
		return Record{}, err
	}
	scope, err := s.store.InspectReview(ctx, request)
	if err != nil {
		return Record{}, err
	}
	if scope.Handoff.Candidate != request.Candidate {
		return Record{}, &Error{Kind: KindCandidateMismatch, Message: "review candidate differs from immutable handoff"}
	}
	if err := ValidateCriterionSet(request.Criteria, scope.CriterionIDs); err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "HR-04"); err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "HR-05"); err != nil {
		return Record{}, err
	}
	record, err := s.store.CommitReview(ctx, request)
	if err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "HR-06"); err != nil {
		return Record{}, err
	}
	if err := s.faults.Reach(ctx, "HR-07"); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Service) requireEligibility(ctx context.Context, request Request) error {
	consequence, reconciliationID, err := s.store.WorkEligibility(ctx, request.WorkRevisionID, request.WorkID, workgraph.StatusHandedOff)
	if err != nil {
		return &Error{Kind: KindConflict, Message: "read WorkRevision review eligibility", Cause: err}
	}
	fact, err := workgraph.NewLegacyEligibilityFact(reconciliationID, request.WorkRevisionID, request.WorkID, workgraph.StatusHandedOff, workgraph.EligibilityReview, consequence)
	if err != nil || !fact.MatchesLegacy(request.WorkRevisionID, request.WorkID, workgraph.StatusHandedOff, workgraph.EligibilityReview) {
		return &Error{Kind: KindRevisionIneligible, Message: "WorkRevision review eligibility is malformed"}
	}
	if !fact.Permitted() {
		return &Error{Kind: KindRevisionIneligible, Message: "WorkRevision reconciliation forbids review", Observed: map[string]string{"reconciliation_id": fact.ReconciliationID(), "reason": fact.Reason()}, Expected: map[string]string{"operation": string(workgraph.EligibilityReview)}}
	}
	return nil
}
