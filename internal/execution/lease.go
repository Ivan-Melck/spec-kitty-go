package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type AuthorityRepository interface {
	LookupStartOperation(context.Context, StartRequest) (bool, error)
	Claim(context.Context, StartRequest, ids.RunID, ids.LeaseID) (RunCapsule, error)
	Heartbeat(context.Context, HeartbeatRequest) (time.Time, error)
	Expire(context.Context, ExpireRequest) error
}

func (s *Service) LookupStartOperation(ctx context.Context, request StartRequest) (bool, error) {
	if err := request.Validate(); err != nil {
		return false, err
	}
	request = withGovernanceCommandDigest(request)
	return s.repository.LookupStartOperation(ctx, request)
}

func (s *Service) Heartbeat(ctx context.Context, request HeartbeatRequest) (time.Time, error) {
	if request.Now.IsZero() {
		request.Now = s.clock.Now()
	}
	if request.CorrelationID == "" {
		request.CorrelationID = request.OperationID.String()
	}
	if err := request.Validate(); err != nil {
		return time.Time{}, err
	}
	return s.repository.Heartbeat(ctx, request)
}

func (s *Service) Expire(ctx context.Context, request ExpireRequest) error {
	if request.Now.IsZero() {
		request.Now = s.clock.Now()
	}
	if request.CorrelationID == "" {
		request.CorrelationID = request.OperationID.String()
	}
	if err := request.Validate(); err != nil {
		return err
	}
	return s.repository.Expire(ctx, request)
}

type IdentityGenerator interface {
	RunID() (ids.RunID, error)
	LeaseID() (ids.LeaseID, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository AuthorityRepository
	identities IdentityGenerator
	clock      Clock
	faults     faults.Hook
}

func NewService(repository AuthorityRepository, identities IdentityGenerator, clock Clock) *Service {
	return &Service{repository: repository, identities: identities, clock: clock, faults: faults.Noop{}}
}

func (s *Service) WithFaults(hook faults.Hook) *Service {
	s.faults = hook
	return s
}

func (s *Service) Start(ctx context.Context, request StartRequest) (RunCapsule, error) {
	if request.Now.IsZero() {
		request.Now = s.clock.Now()
	}
	if err := request.Validate(); err != nil {
		return RunCapsule{}, err
	}
	request = withGovernanceCommandDigest(request)
	if err := s.faults.Reach(ctx, "SR-00"); err != nil {
		return RunCapsule{}, err
	}
	runID, err := s.identities.RunID()
	if err != nil {
		return RunCapsule{}, identityError(request, "run_id", err)
	}
	leaseID, err := s.identities.LeaseID()
	if err != nil {
		return RunCapsule{}, identityError(request, "lease_id", err)
	}
	capsule, err := s.repository.Claim(ctx, request, runID, leaseID)
	if err != nil {
		return RunCapsule{}, err
	}
	capsule, err = bindRunGovernanceContext(request, capsule)
	if err != nil {
		return RunCapsule{}, err
	}
	if err := s.faults.Reach(ctx, "SR-02"); err != nil {
		return RunCapsule{}, err
	}
	return capsule, nil
}

// StartPrepared completes a StartRun whose operation envelope was durably
// recorded at SR-01. Claim-only callers continue to use Start.
func (s *Service) StartPrepared(ctx context.Context, request StartRequest) (RunCapsule, error) {
	if request.Now.IsZero() {
		request.Now = s.clock.Now()
	}
	if err := request.Validate(); err != nil {
		return RunCapsule{}, err
	}
	request = withGovernanceCommandDigest(request)
	runID, err := s.identities.RunID()
	if err != nil {
		return RunCapsule{}, identityError(request, "run_id", err)
	}
	leaseID, err := s.identities.LeaseID()
	if err != nil {
		return RunCapsule{}, identityError(request, "lease_id", err)
	}
	capsule, err := s.repository.Claim(ctx, request, runID, leaseID)
	if err != nil {
		return RunCapsule{}, err
	}
	capsule, err = bindRunGovernanceContext(request, capsule)
	if err != nil {
		return RunCapsule{}, err
	}
	if err := s.faults.Reach(ctx, "SR-02"); err != nil {
		return RunCapsule{}, err
	}
	return capsule, nil
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type RandomIdentities struct{ Reader io.Reader }

func (g RandomIdentities) RunID() (ids.RunID, error) {
	value, err := randomValue(g.Reader, "run")
	if err != nil {
		return "", err
	}
	return ids.ParseRunID(value)
}
func (g RandomIdentities) LeaseID() (ids.LeaseID, error) {
	value, err := randomValue(g.Reader, "lease")
	if err != nil {
		return "", err
	}
	return ids.ParseLeaseID(value)
}

func randomValue(reader io.Reader, prefix string) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	var value [16]byte
	if _, err := io.ReadFull(reader, value[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func identityError(request StartRequest, field string, cause error) error {
	diagnostic := NewDiagnostic(KindInvalidCommand, "could not generate "+field, RetrySafeNewOperation, request.CorrelationID, request.OperationID, nil, map[string]string{"identity_source": "unavailable"}, map[string]string{"identity_source": "cryptographic randomness"}, request.Now)
	diagnostic.RecoveryActions = []string{"retry with a healthy cryptographic random source"}
	diagnostic.Cause = cause
	return diagnostic
}

func bindRunGovernanceContext(request StartRequest, capsule RunCapsule) (RunCapsule, error) {
	wantID := request.GovernanceContext.ID()
	wantAggregate := request.GovernanceContext.Aggregate()
	if (capsule.GovernanceContextID != "" && capsule.GovernanceContextID != wantID) ||
		(capsule.GovernanceContextAggregate.Algorithm() != "" && capsule.GovernanceContextAggregate != wantAggregate) {
		return RunCapsule{}, NewDiagnostic(KindIdempotencyConflict, "repository returned a run bound to different governance context", RetryNever,
			request.CorrelationID, request.OperationID,
			map[string]string{"operation_id": request.OperationID.String(), "run_id": capsule.RunID.String()},
			map[string]string{"governance_context_id": capsule.GovernanceContextID.String(), "governance_context_aggregate": capsule.GovernanceContextAggregate.Algorithm() + ":" + capsule.GovernanceContextAggregate.Digest()},
			map[string]string{"governance_context_id": wantID.String(), "governance_context_aggregate": wantAggregate.Algorithm() + ":" + wantAggregate.Digest()}, request.Now)
	}
	capsule.GovernanceContextID = wantID
	capsule.GovernanceContextAggregate = wantAggregate
	if capsule.AggregateIDs == nil {
		capsule.AggregateIDs = map[string]string{}
	}
	capsule.AggregateIDs["governance_context_id"] = wantID.String()
	return capsule, nil
}

func withGovernanceCommandDigest(request StartRequest) StartRequest {
	if request.CommandDigest != "" {
		return request
	}
	aggregate := request.GovernanceContext.Aggregate()
	request.CommandDigest = "governance-context/" + request.GovernanceContext.ID().String() + "/" + aggregate.Algorithm() + "/" + aggregate.Digest()
	return request
}
