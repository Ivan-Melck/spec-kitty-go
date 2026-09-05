package productionruntime

import (
	"context"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
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
	effect      *execution.EffectAuthorityServiceV2
	artifact    storegapdb.ArtifactAuthorityPortV2
	workgraph   storegapdb.WorkgraphAuthorityPortV2
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
	}
	return result, err
}

func (h *Host) PublishArtifact(ctx context.Context, expected journalv2.RuntimeEpochID, plan artifact.AuthorityPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	release, err := h.beginCommand(expected)
	if err != nil {
		return journalv2.AcknowledgedPublicationV2{}, err
	}
	defer release()
	result, err := h.capabilities.artifact.Publish(ctx, plan)
	if err == nil {
		err = h.refreshProjection(ctx)
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
		err = h.refreshProjection(ctx)
	}
	return result, err
}
