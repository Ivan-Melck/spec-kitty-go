package productionruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type runtimeEpochSource struct{ epoch journalv2.RuntimeEpochID }

func (s runtimeEpochSource) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	if s.epoch == "" {
		return "", &Error{Kind: ErrorNotReady, State: HostRecovering, Field: "runtime_epoch"}
	}
	return s.epoch, nil
}

type runtimeCapabilities struct {
	mission     *mission.MissionAuthorityServiceV2
	readiness   *mission.ReadinessAuthorityServiceV2
	execution   *execution.ExecutionAuthorityServiceV2
	workspace   *execution.WorkspaceAuthorityServiceV2
	stage       *workspace.StageAuthorityServiceV2
	handoff     *handoff.AuthorityServiceV2
	review      *review.AuthorityServiceV2
	acceptance  *integration.AcceptanceAuthorityServiceV2
	integration *integration.IntegrationAuthorityServiceV2
	cleanup     *integration.IntegrationCleanupServiceV2
	effect      *execution.EffectAuthorityServiceV2
	artifact    artifact.AuthorityPortV2
	workgraph   workgraph.AuthorityPortV2
	checkpoints *checkpointScheduler
}

func (h *Host) afterMutation(ctx context.Context) error {
	if err := h.capabilities.checkpoints.MaybeCheckpoint(ctx); err != nil {
		h.degrade("checkpoint")
		return &Error{Kind: ErrorProjection, State: HostDegraded, Field: "checkpoint"}
	}
	return h.refreshProjection(ctx)
}

// afterPossibleMutation refreshes from durable truth even when a capability
// returns a typed failure. Effect dispatch and integration cleanup can commit
// intent, ambiguity, or pending-cleanup state before an external operation
// fails. Request cancellation must not leave the host's memory behind those
// acknowledged facts.
func (h *Host) afterPossibleMutation(ctx context.Context, primary error) error {
	refreshContext := ctx
	cancel := func() {}
	if refreshContext == nil || refreshContext.Err() != nil {
		refreshContext, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	}
	defer cancel()
	refreshErr := h.afterMutation(refreshContext)
	if primary != nil {
		return errors.Join(primary, refreshErr)
	}
	return refreshErr
}

// ReconciliationCommands is a callback-scoped typed capability surface. It
// contains no raw adapter, committer, projection mutator, or generic command
// callback and becomes unusable immediately when startup reconciliation ends.
type ReconciliationCommands struct {
	epoch        journalv2.RuntimeEpochID
	capabilities runtimeCapabilities
	active       atomic.Bool
}

func newReconciliationCommands(epoch journalv2.RuntimeEpochID, capabilities runtimeCapabilities) *ReconciliationCommands {
	commands := &ReconciliationCommands{epoch: epoch, capabilities: capabilities}
	commands.active.Store(true)
	return commands
}

func (c *ReconciliationCommands) close() { c.active.Store(false) }
func (c *ReconciliationCommands) RuntimeEpochID() journalv2.RuntimeEpochID {
	if c == nil || !c.active.Load() {
		return ""
	}
	return c.epoch
}
func (c *ReconciliationCommands) admit() error {
	if c == nil || !c.active.Load() {
		return &Error{Kind: ErrorNotReady, State: HostReconciling, Field: "reconciliation_scope"}
	}
	return nil
}

func (c *ReconciliationCommands) ExecuteMission(ctx context.Context, command mission.MissionAuthorityCommandV2) (mission.MissionAuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return mission.MissionAuthorityResultV2{}, err
	}
	return c.capabilities.mission.Execute(ctx, command)
}
func (c *ReconciliationCommands) ExecuteReadiness(ctx context.Context, command mission.ReadinessAuthorityCommandV2) (mission.ReadinessAuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return mission.ReadinessAuthorityResultV2{}, err
	}
	return c.capabilities.readiness.Execute(ctx, command)
}
func (c *ReconciliationCommands) ExecuteWork(ctx context.Context, command execution.ExecutionAuthorityRequestV2) (execution.ExecutionAuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return execution.ExecutionAuthorityResultV2{}, err
	}
	return c.capabilities.execution.Execute(ctx, command)
}
func (c *ReconciliationCommands) ExecuteWorkspace(ctx context.Context, command execution.WorkspaceAuthorityRequestV2) (execution.WorkspaceAuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return execution.WorkspaceAuthorityResultV2{}, err
	}
	return c.capabilities.workspace.Execute(ctx, command)
}
func (c *ReconciliationCommands) ExecuteWorkspaceStage(ctx context.Context, command workspace.StageAuthorityRequestV2) (workspace.StageAuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return workspace.StageAuthorityResultV2{}, err
	}
	return c.capabilities.stage.Execute(ctx, command)
}
func (c *ReconciliationCommands) SubmitHandoff(ctx context.Context, command handoff.AuthorityRequestV2) (handoff.AuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return handoff.AuthorityResultV2{}, err
	}
	return c.capabilities.handoff.Submit(ctx, command)
}
func (c *ReconciliationCommands) SubmitReview(ctx context.Context, command review.AuthorityRequestV2) (review.AuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return review.AuthorityResultV2{}, err
	}
	return c.capabilities.review.Submit(ctx, command)
}
func (c *ReconciliationCommands) ExecuteAcceptance(ctx context.Context, command integration.AcceptanceAuthorityRequestV2) (integration.AcceptanceResultV2, error) {
	if err := c.admit(); err != nil {
		return integration.AcceptanceResultV2{}, err
	}
	return c.capabilities.acceptance.Accept(ctx, command)
}
func (c *ReconciliationCommands) ExecuteIntegration(ctx context.Context, command integration.IntegrationAuthorityRequestV2) (integration.IntegrationAttemptStateV2, error) {
	if err := c.admit(); err != nil {
		return integration.IntegrationAttemptStateV2{}, err
	}
	return c.capabilities.integration.Integrate(ctx, command)
}
func (c *ReconciliationCommands) ExecuteEffect(ctx context.Context, command execution.EffectAuthorityRequestV2) (execution.EffectAuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return execution.EffectAuthorityResultV2{}, err
	}
	return c.capabilities.effect.Execute(ctx, command)
}
func (c *ReconciliationCommands) ExecuteEffectDispatch(ctx context.Context, command execution.EffectDispatchCommandV2, provider execution.EffectDispatcherPortV2) (execution.EffectAuthorityResultV2, error) {
	if err := c.admit(); err != nil {
		return execution.EffectAuthorityResultV2{}, err
	}
	return c.capabilities.effect.ExecuteDispatchV2(ctx, command, provider)
}
func (c *ReconciliationCommands) ExecuteIntegrationCleanup(ctx context.Context, command integration.IntegrationCleanupRequestV2) (integration.IntegrationAttemptStateV2, error) {
	if err := c.admit(); err != nil {
		return integration.IntegrationAttemptStateV2{}, err
	}
	return c.capabilities.cleanup.Cleanup(ctx, command)
}

// ExecuteMission applies the common runtime barrier, then delegates policy to
// the mission capability and refreshes query memory from acknowledged durable
// truth before returning.
func (h *Host) ExecuteMission(ctx context.Context, expected journalv2.RuntimeEpochID, command mission.MissionAuthorityCommandV2) (mission.MissionAuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return mission.MissionAuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.mission.Execute(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) ExecuteReadiness(ctx context.Context, expected journalv2.RuntimeEpochID, command mission.ReadinessAuthorityCommandV2) (mission.ReadinessAuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return mission.ReadinessAuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.readiness.Execute(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) ExecuteWork(ctx context.Context, expected journalv2.RuntimeEpochID, command execution.ExecutionAuthorityRequestV2) (execution.ExecutionAuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return execution.ExecutionAuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.execution.Execute(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) ExecuteWorkspace(ctx context.Context, expected journalv2.RuntimeEpochID, command execution.WorkspaceAuthorityRequestV2) (execution.WorkspaceAuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return execution.WorkspaceAuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.workspace.Execute(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) ExecuteWorkspaceStage(ctx context.Context, expected journalv2.RuntimeEpochID, command workspace.StageAuthorityRequestV2) (workspace.StageAuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return workspace.StageAuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.stage.Execute(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) SubmitHandoff(ctx context.Context, expected journalv2.RuntimeEpochID, command handoff.AuthorityRequestV2) (handoff.AuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return handoff.AuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.handoff.Submit(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) SubmitReview(ctx context.Context, expected journalv2.RuntimeEpochID, command review.AuthorityRequestV2) (review.AuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return review.AuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.review.Submit(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) ExecuteAcceptance(ctx context.Context, expected journalv2.RuntimeEpochID, command integration.AcceptanceAuthorityRequestV2) (integration.AcceptanceResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return integration.AcceptanceResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.acceptance.Accept(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) ExecuteIntegration(ctx context.Context, expected journalv2.RuntimeEpochID, command integration.IntegrationAuthorityRequestV2) (integration.IntegrationAttemptStateV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return integration.IntegrationAttemptStateV2{}, err
	}
	defer release()
	result, err := h.capabilities.integration.Integrate(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) ExecuteEffect(ctx context.Context, expected journalv2.RuntimeEpochID, command execution.EffectAuthorityRequestV2) (execution.EffectAuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return execution.EffectAuthorityResultV2{}, err
	}
	defer release()
	result, err := h.capabilities.effect.Execute(ctx, command)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

// ExecuteEffectDispatch is the sole production route from durable effect
// permission through provider observation/mutation and durable outcome.
// Memory is refreshed even when the typed result requires a decision.
func (h *Host) ExecuteEffectDispatch(ctx context.Context, expected journalv2.RuntimeEpochID, command execution.EffectDispatchCommandV2, provider execution.EffectDispatcherPortV2) (execution.EffectAuthorityResultV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return execution.EffectAuthorityResultV2{}, err
	}
	defer release()
	result, commandErr := h.capabilities.effect.ExecuteDispatchV2(ctx, command, provider)
	return result, h.afterPossibleMutation(ctx, commandErr)
}

// ExecuteIntegrationCleanup is the separately retryable durable cleanup route.
// CleanupPending remains observable even when the Git effect returns an error.
func (h *Host) ExecuteIntegrationCleanup(ctx context.Context, expected journalv2.RuntimeEpochID, command integration.IntegrationCleanupRequestV2) (integration.IntegrationAttemptStateV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return integration.IntegrationAttemptStateV2{}, err
	}
	defer release()
	result, commandErr := h.capabilities.cleanup.Cleanup(ctx, command)
	return result, h.afterPossibleMutation(ctx, commandErr)
}

func (h *Host) PublishArtifact(ctx context.Context, expected journalv2.RuntimeEpochID, plan artifact.AuthorityPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return journalv2.AcknowledgedPublicationV2{}, err
	}
	defer release()
	result, err := h.capabilities.artifact.Publish(ctx, plan)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}

func (h *Host) PublishWorkgraph(ctx context.Context, expected journalv2.RuntimeEpochID, plan workgraph.AuthorityPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return journalv2.AcknowledgedPublicationV2{}, err
	}
	defer release()
	result, err := h.capabilities.workgraph.Publish(ctx, plan)
	if err == nil {
		err = h.afterMutation(ctx)
	}
	return result, err
}
