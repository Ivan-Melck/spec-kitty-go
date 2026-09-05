package gapdb

import (
	"context"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type artifactPublicationPortV2 interface {
	Publish(context.Context, journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error)
}

// artifactAuthorityV2 is the narrow translation boundary from artifact-owned
// sealed plans to the sole GapDB v2 committer. It owns no artifact policy and
// exposes neither a raw key nor a generic Get/Put operation.
type artifactAuthorityV2 struct{ publisher artifactPublicationPortV2 }

func newArtifactAuthorityV2(coordinator *journalv2.PublicationCoordinatorV2) (*artifactAuthorityV2, error) {
	if coordinator == nil {
		return nil, invalidV2Plan("artifact authority coordinator")
	}
	return &artifactAuthorityV2{publisher: coordinator}, nil
}

func NewArtifactAuthorityPortV2(coordinator *journalv2.PublicationCoordinatorV2) (artifact.AuthorityPortV2, error) {
	return newArtifactAuthorityV2(coordinator)
}

func newArtifactAuthorityV2ForTest(port artifactPublicationPortV2) *artifactAuthorityV2 {
	return &artifactAuthorityV2{publisher: port}
}

// Publish commits one already-sealed artifact publication. Family filtering
// prevents this capability adapter from becoming a cross-domain god store.
func (authority *artifactAuthorityV2) Publish(ctx context.Context, capability artifact.AuthorityPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	if authority == nil || authority.publisher == nil {
		return journalv2.AcknowledgedPublicationV2{}, invalidV2Plan("artifact authority plan membership")
	}
	plan, err := capability.Publication()
	if err != nil || !artifactPlanMembershipV2(plan) {
		return journalv2.AcknowledgedPublicationV2{}, invalidV2Plan("artifact authority plan membership")
	}
	return authority.publisher.Publish(ctx, plan)
}

func artifactPlanMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	events := plan.Events()
	if len(events) < 2 || len(events) > 3 {
		return false
	}
	if len(plan.Heads()) != len(events) || len(plan.Conditions()) != len(events) || len(plan.EffectIntents()) != 0 || len(plan.EligibilityGuards()) != 0 || len(plan.Checkpoints()) != 0 {
		return false
	}
	operation, catalog, revision := 0, 0, 0
	for _, event := range events {
		switch event.Family() {
		case "operation":
			operation++
		case "artifact_catalog":
			catalog++
		case "artifact_revision":
			revision++
		default:
			return false
		}
	}
	return operation == 1 && revision == 1 && (catalog == 0 && len(events) == 2 || catalog == 1 && len(events) == 3)
}
