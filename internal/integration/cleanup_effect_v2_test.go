package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type cleanupPortV2 struct {
	*integrationPortV2
}

func (p cleanupPortV2) ResolveIntegrationCleanupOperation(ctx context.Context, binding journalv2.OperationBindingV2) (IntegrationOperationResolutionV2, error) {
	return p.ResolveIntegrationOperation(ctx, binding)
}

func (p cleanupPortV2) ObserveIntegrationCleanupAuthority(_ context.Context, request IntegrationCleanupRequestV2) (IntegrationCleanupAuthorityObservationV2, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	attemptCoordinate, err := IntegrationAttemptCoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, request.AttemptID)
	if err != nil {
		return IntegrationCleanupAuthorityObservationV2{}, err
	}
	attempt, found := p.attempts[attemptCoordinate]
	if !found {
		return IntegrationCleanupAuthorityObservationV2{}, errors.New("attempt absent")
	}
	source, found := p.operations[request.IntegrationOperationID]
	if !found {
		return IntegrationCleanupAuthorityObservationV2{}, errors.New("source operation absent")
	}
	sourceValue, err := journalv2.EncodeOperationStateV2(source.State)
	if err != nil {
		return IntegrationCleanupAuthorityObservationV2{}, err
	}
	sourceDigest, err := journalv2.ReducerStateDigestV2(sourceValue)
	if err != nil {
		return IntegrationCleanupAuthorityObservationV2{}, err
	}
	return IntegrationCleanupAuthorityObservationV2{
		Attempt:                       attempt,
		AttemptRecordRevision:         p.attemptRevs[attemptCoordinate],
		SourceOperation:               source.State,
		SourceOperationRecordRevision: source.RecordRevision,
		SourceOperationValueDigest:    sourceDigest,
	}, nil
}

func (p cleanupPortV2) PublishIntegrationCleanup(ctx context.Context, plan IntegrationCleanupPlanV2) (IntegrationAckV2, error) {
	return p.PublishIntegration(ctx, IntegrationPlanV2(plan))
}

func (p cleanupPortV2) ReconcileIntegrationCleanup(ctx context.Context, plan IntegrationCleanupPlanV2) (IntegrationAckV2, error) {
	return p.ReconcileIntegration(ctx, IntegrationPlanV2(plan))
}

func integrationCleanupFixtureV2(t *testing.T) (IntegrationCleanupRequestV2, *integrationPortV2, *integrationGitV2) {
	t.Helper()
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	integrationService, err := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := integrationService.Integrate(t.Context(), request)
	if err != nil || result.Stage != IntegrationAttemptCompletedV2 || result.CleanupStatus != CleanupEligible {
		t.Fatalf("integration result=%+v error=%v", result, err)
	}
	return IntegrationCleanupRequestV2{
		CleanupOperationID:     "operation-cleanup-1",
		IntegrationOperationID: request.OperationID,
		ProjectID:              request.ProjectID,
		MissionID:              request.MissionID,
		WorkRevisionID:         request.WorkRevisionID,
		WorkID:                 request.WorkID,
		AttemptID:              request.AttemptID,
		RepositoryID:           request.RepositoryID,
		TargetRef:              request.TargetRef,
		RuntimeEpochID:         request.RuntimeEpochID,
		ActorID:                "actor-cleanup",
		ProcessID:              "process-cleanup",
		Governance:             request.Governance,
		ObservedAt:             request.ObservedAt.Add(time.Minute),
	}, port, git
}

func TestIntegrationCleanupIsIndependentRetryableAuthorityV2(t *testing.T) {
	request, rawPort, git := integrationCleanupFixtureV2(t)
	port := cleanupPortV2{integrationPortV2: rawPort}
	service, err := NewIntegrationCleanupServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	git.lostCleanup = true
	result, err := service.Cleanup(t.Context(), request)
	if err != nil || result.CleanupStatus != CleanupComplete || result.CleanupMarker == "" {
		t.Fatalf("cleanup result=%+v error=%v plans=%d", result, err, len(rawPort.plans))
	}
	plans, calls := len(rawPort.plans), git.calls
	replay, err := service.Cleanup(t.Context(), request)
	if err != nil || replay.CleanupStatus != CleanupComplete || len(rawPort.plans) != plans || git.calls != calls {
		t.Fatalf("replay=%+v error=%v plans=%d calls=%d", replay, err, len(rawPort.plans), git.calls)
	}
}

func TestIntegrationCleanupFailureCannotRewriteIntegrationTruthV2(t *testing.T) {
	request, rawPort, git := integrationCleanupFixtureV2(t)
	port := cleanupPortV2{integrationPortV2: rawPort}
	service, err := NewIntegrationCleanupServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	git.cleanupErr = errors.New("provider emitted an enormous and private diagnostic")
	_, err = service.Cleanup(t.Context(), request)
	if err == nil || err.Error() != "git_failure: cleanup integration workspace" {
		t.Fatalf("unexpected bounded error: %v plans=%d", err, len(rawPort.plans))
	}
	attemptCoordinate, _ := IntegrationAttemptCoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, request.AttemptID)
	pending := rawPort.attempts[attemptCoordinate]
	if pending.Stage != IntegrationAttemptCompletedV2 || pending.CleanupStatus != CleanupPending || pending.Result == "" || pending.ObservedTarget == "" {
		t.Fatalf("cleanup changed integration truth: %+v", pending)
	}
	git.cleanupErr = nil
	result, err := service.Cleanup(t.Context(), request)
	if err != nil || result.CleanupStatus != CleanupComplete || result.Stage != IntegrationAttemptCompletedV2 {
		t.Fatalf("retry result=%+v error=%v", result, err)
	}
}

func TestIntegrationCleanupRejectsSubstitutedSourceOperationV2(t *testing.T) {
	request, rawPort, _ := integrationCleanupFixtureV2(t)
	port := cleanupPortV2{integrationPortV2: rawPort}
	baseline, err := port.ObserveIntegrationCleanupAuthority(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("value digest", func(t *testing.T) {
		authority := baseline
		authority.SourceOperationValueDigest, _ = journalv2.ReducerStateDigestV2([]byte(`{"other":true}`))
		if err := validateIntegrationCleanupAuthorityV2(request, IntegrationOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority); err == nil {
			t.Fatal("substituted source operation digest accepted")
		}
	})
	t.Run("cleanup effect membership", func(t *testing.T) {
		authority := baseline
		effects := make([]journalv2.EffectID, 0, len(authority.SourceOperation.EffectIDs()))
		for _, effectID := range authority.SourceOperation.EffectIDs() {
			if effectID != authority.Attempt.CleanupEffectID {
				effects = append(effects, effectID)
			}
		}
		authority.SourceOperation, err = journalv2.NewOperationStateV2(journalv2.OperationStateInput{
			OperationID:         authority.SourceOperation.OperationID(),
			LogicalVersion:      authority.SourceOperation.LogicalVersion(),
			Status:              authority.SourceOperation.Status(),
			ResultKind:          authority.SourceOperation.ResultKind(),
			ResultSchemaVersion: authority.SourceOperation.ResultSchemaVersion(),
			CanonicalResult:     authority.SourceOperation.Result(),
			EventIDs:            authority.SourceOperation.EventIDs(),
			EffectIDs:           effects,
			LastPublicationID:   authority.SourceOperation.LastPublicationID(),
		})
		if err != nil {
			t.Fatal(err)
		}
		value, _ := journalv2.EncodeOperationStateV2(authority.SourceOperation)
		authority.SourceOperationValueDigest, _ = journalv2.ReducerStateDigestV2(value)
		if err := validateIntegrationCleanupAuthorityV2(request, IntegrationOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority); err == nil {
			t.Fatal("source operation without cleanup effect accepted")
		}
	})
}
