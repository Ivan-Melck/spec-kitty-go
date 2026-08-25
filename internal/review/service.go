package review

import (
	"context"

	"github.com/spec-kitty/go-kitty3/internal/faults"
)

type Store interface {
	LookupReviewOperation(context.Context, Request) (Record, bool, error)
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
