package gapdb

import (
	"context"
	"errors"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
)

func TestAcceptanceAndIntegrationGapDBProductionServicesAreCallableV2(t *testing.T) {
	memory := newV2AtomicMemory()
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	config := wp10RestoreConfigV2(t)
	projection, err := handoffReviewProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	epoch := gapdbHandoffEpochV2("epoch-acceptance-integration")
	acceptance, err := adapter.AcceptanceAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, epoch)
	if err != nil || acceptance == nil {
		t.Fatalf("acceptance service=%v error=%v", acceptance, err)
	}
	integrator, err := adapter.IntegrationAuthorityServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, epoch, gapdbIntegrationGitNoopV2{})
	if err != nil || integrator == nil {
		t.Fatalf("integration service=%v error=%v", integrator, err)
	}
	cleanup, err := adapter.IntegrationCleanupServiceV2(coordinator, config.EventRegistry, config.Reducers, config.HostAuthorityID, config.Budgets, epoch, gapdbIntegrationGitNoopV2{})
	if err != nil || cleanup == nil {
		t.Fatalf("integration cleanup service=%v error=%v", cleanup, err)
	}
}

func TestAcceptanceAndIntegrationGapDBRejectUnsealedCapabilitiesV2(t *testing.T) {
	authority := &acceptanceAuthorityV2{}
	if _, err := authority.PublishAcceptance(t.Context(), integration.AcceptancePlanV2{}); err == nil {
		t.Fatal("acceptance adapter admitted an unsealed capability")
	}
	integrator := &integrationAuthorityV2{}
	if _, err := integrator.PublishIntegration(t.Context(), integration.IntegrationPlanV2{}); err == nil {
		t.Fatal("integration adapter admitted an unsealed capability")
	}
	cleanup := &integrationCleanupAuthorityV2{}
	if _, err := cleanup.PublishIntegrationCleanup(t.Context(), integration.IntegrationCleanupPlanV2{}); err == nil {
		t.Fatal("integration cleanup adapter admitted an unsealed capability")
	}
}

type gapdbIntegrationGitNoopV2 struct{}

func (gapdbIntegrationGitNoopV2) TargetHead(context.Context, ids.RepositoryID, string) (string, error) {
	return "", errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) InstallFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	return integration.IntegrationFenceObservationV2{}, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) ObserveFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	return integration.IntegrationFenceObservationV2{}, false, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) EnsureWorkspace(context.Context, integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	return integration.WorkspaceFacts{}, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) MergeCandidate(context.Context, integration.IntegrationMergeIntentV2, integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	return "", nil, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) CompareAndSwapTarget(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	return integration.IntegrationPublishObservationV2{}, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) ObserveTargetPublication(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	return integration.IntegrationPublishObservationV2{}, false, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) CleanupWorkspace(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	return integration.IntegrationCleanupObservationV2{}, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) ObserveCleanup(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	return integration.IntegrationCleanupObservationV2{}, false, errors.New("not invoked by composition")
}
func (gapdbIntegrationGitNoopV2) IsAncestor(context.Context, ids.RepositoryID, string, string) (bool, error) {
	return false, errors.New("not invoked by composition")
}
