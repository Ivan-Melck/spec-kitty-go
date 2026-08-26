package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/integration"
)

var errRepositoryFenceAcquired = errors.New("repository fence acquired before refusal")

func TestIntegrationRefusesInvalidRequestBeforeRepositoryFenceAcquisition(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	fence := &integrationFenceObserver{}
	_, refusal := integration.NewService(store, fence).Integrate(ctx, invalidIntegrationRequest())
	if err := preFenceRefusalViolation(refusal, fence.Acquisitions()); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationEarlyRefusalOrderingMutantIsRejected(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	request := invalidIntegrationRequest()
	productionFence := &integrationFenceObserver{}
	_, productionRefusal := integration.NewService(store, productionFence).Integrate(ctx, request)
	if err := preFenceRefusalViolation(productionRefusal, productionFence.Acquisitions()); err != nil {
		t.Fatalf("production ordering: %v", err)
	}

	fence := &integrationFenceObserver{}
	_, refusal := fence.EstablishIntegrationAuthority(ctx,
		func(current integration.FenceFacts) (integration.Record, error) {
			return store.PrepareIntegrationAuthority(ctx, request, "", current)
		},
		func(intent integration.Record, installed integration.FenceFacts) (integration.Record, error) {
			return store.ActivateIntegrationAuthority(ctx, intent, installed)
		},
	)
	if !integration.IsKind(refusal, integration.KindInvalid) {
		t.Fatalf("late-refusal diagnostic = %#v, want %s", refusal, integration.KindInvalid)
	}
	if refusal.Error() != productionRefusal.Error() {
		t.Fatalf("ordering mutant changed diagnostic: production=%q mutant=%q", productionRefusal, refusal)
	}
	violation := preFenceRefusalViolation(refusal, fence.Acquisitions())
	if !errors.Is(violation, errRepositoryFenceAcquired) {
		t.Fatalf("ordering mutant survived: violation=%v acquisitions=%d", violation, fence.Acquisitions())
	}
}

func invalidIntegrationRequest() integration.Request {
	return integration.Request{
		SchemaVersion: execution.ResultSchemaVersion,
		CorrelationID: "correlation-refusal",
		OperationID:   "operation-refusal",
		ReviewID:      "review-refusal",
		RepositoryID:  "repository-refusal",
		TargetRef:     "refs/heads/main",
		ActorID:       "actor-refusal",
		ProcessID:     "process-refusal",
		Candidate:     "not-an-object-id",
		LeaseTTL:      time.Minute,
		StartedAt:     time.Now().UTC(),
	}
}

func preFenceRefusalViolation(refusal error, acquisitions int) error {
	if !integration.IsKind(refusal, integration.KindInvalid) {
		return fmt.Errorf("integration refusal = %#v, want %s", refusal, integration.KindInvalid)
	}
	if acquisitions != 0 {
		return fmt.Errorf("%w: got %d acquisitions, want 0", errRepositoryFenceAcquired, acquisitions)
	}
	return nil
}

type integrationFenceObserver struct {
	acquisitions atomic.Int64
}

func (o *integrationFenceObserver) Acquisitions() int {
	return int(o.acquisitions.Load())
}

func (o *integrationFenceObserver) TargetHead(context.Context) (string, error) {
	panic("unexpected TargetHead after invalid integration request")
}

func (o *integrationFenceObserver) EstablishIntegrationAuthority(
	_ context.Context,
	prepare func(integration.FenceFacts) (integration.Record, error),
	activate func(integration.Record, integration.FenceFacts) (integration.Record, error),
) (integration.Record, error) {
	o.acquisitions.Add(1)
	intent, err := prepare(integration.FenceFacts{})
	if err != nil {
		return integration.Record{}, err
	}
	return activate(intent, integration.FenceFacts{})
}

func (o *integrationFenceObserver) EnsureIntegrationWorkspace(context.Context, string, string, string) (integration.WorkspaceFacts, error) {
	panic("unexpected EnsureIntegrationWorkspace after invalid integration request")
}

func (o *integrationFenceObserver) IntegrateCandidate(context.Context, integration.WorkspaceFacts, string, string) (string, *integration.ConflictEvidence, error) {
	panic("unexpected IntegrateCandidate after invalid integration request")
}

func (o *integrationFenceObserver) VerifyIntegrationResult(context.Context, string, string, string) (bool, error) {
	panic("unexpected VerifyIntegrationResult after invalid integration request")
}

func (o *integrationFenceObserver) PublishIntegrationTarget(context.Context, integration.Record, string, string, func() error) error {
	panic("unexpected PublishIntegrationTarget after invalid integration request")
}

func (o *integrationFenceObserver) IsAncestor(context.Context, string, string) (bool, error) {
	panic("unexpected IsAncestor after invalid integration request")
}

func (o *integrationFenceObserver) CleanupIntegrationWorkspace(context.Context, integration.Record) error {
	panic("unexpected CleanupIntegrationWorkspace after invalid integration request")
}
