//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

// The initial private-only diagnostic does not execute domain capability
// operations. These are fixed fail-closed capability guards, NOT a fake GapDB
// provider. The genuine GapDB sidecar, store, and Host remain unchanged.

import (
	"context"
	"errors"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

var errExpiryDiagnosticCapabilityUnavailable = errors.New("expiry diagnostic capability unavailable")

type expiryDiagnosticReadiness struct{}
type expiryDiagnosticDecomposition struct{}
type expiryDiagnosticVerifier struct{}
type expiryDiagnosticGit struct{}

var (
	_ mission.CapabilityReadinessSourceV2 = expiryDiagnosticReadiness{}
	_ mission.DecompositionProviderV2     = expiryDiagnosticDecomposition{}
	_ handoff.CandidateVerifier           = expiryDiagnosticVerifier{}
	_ integration.IntegrationGitPortV2    = expiryDiagnosticGit{}
)

func (expiryDiagnosticReadiness) ObserveCapabilityReadiness(context.Context, mission.CapabilityReadinessQueryV2) (mission.CapabilityReadinessEvidenceV2, error) {
	return mission.CapabilityReadinessEvidenceV2{}, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticDecomposition) RequireCapabilities(context.Context, []mission.DecompositionEffectKindV2) error {
	return errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticDecomposition) ObserveEffect(context.Context, journalv2.EffectID) (mission.DecompositionProviderObservationV2, bool, error) {
	return mission.DecompositionProviderObservationV2{}, false, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticDecomposition) MutateEffect(context.Context, mission.DecompositionProviderMutationV2) (mission.DecompositionProviderMutationOutcomeV2, error) {
	return "", errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticDecomposition) CompensateEffect(context.Context, mission.DecompositionProviderMutationV2, mission.DecompositionProviderObservationV2) error {
	return errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticVerifier) Verify(context.Context, handoff.AuthorityScope, string) (handoff.CandidateEvidence, error) {
	return handoff.CandidateEvidence{}, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticVerifier) RevalidateRefusal(context.Context, handoff.Refusal) error {
	return errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) TargetHead(context.Context, ids.RepositoryID, string) (string, error) {
	return "", errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) InstallFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	return integration.IntegrationFenceObservationV2{}, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) ObserveFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	return integration.IntegrationFenceObservationV2{}, false, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) EnsureWorkspace(context.Context, integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	return integration.WorkspaceFacts{}, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) MergeCandidate(context.Context, integration.IntegrationMergeIntentV2, integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	return "", nil, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) CompareAndSwapTarget(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	return integration.IntegrationPublishObservationV2{}, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) ObserveTargetPublication(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	return integration.IntegrationPublishObservationV2{}, false, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) CleanupWorkspace(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	return integration.IntegrationCleanupObservationV2{}, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) ObserveCleanup(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	return integration.IntegrationCleanupObservationV2{}, false, errExpiryDiagnosticCapabilityUnavailable
}

func (expiryDiagnosticGit) IsAncestor(context.Context, ids.RepositoryID, string, string) (bool, error) {
	return false, errExpiryDiagnosticCapabilityUnavailable
}
