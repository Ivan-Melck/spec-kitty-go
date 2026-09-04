package gapdb

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type workgraphPublicationPortV2 interface {
	Publish(context.Context, journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error)
}

// workgraphAuthorityV2 admits only operation+workgraph plans and delegates the
// single atomic publication to the shared GapDB v2 committer.
type workgraphAuthorityV2 struct{ publisher workgraphPublicationPortV2 }

func newWorkgraphAuthorityV2(coordinator *journalv2.PublicationCoordinatorV2) (*workgraphAuthorityV2, error) {
	if coordinator == nil {
		return nil, invalidV2Plan("workgraph authority coordinator")
	}
	return &workgraphAuthorityV2{publisher: coordinator}, nil
}

func newWorkgraphAuthorityV2ForTest(port workgraphPublicationPortV2) *workgraphAuthorityV2 {
	return &workgraphAuthorityV2{publisher: port}
}

func (authority *workgraphAuthorityV2) Publish(ctx context.Context, capability workgraph.AuthorityPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	if authority == nil || authority.publisher == nil {
		return journalv2.AcknowledgedPublicationV2{}, invalidV2Plan("workgraph authority plan membership")
	}
	plan, err := capability.Publication()
	if err != nil || !workgraphPlanMembershipV2(plan) {
		return journalv2.AcknowledgedPublicationV2{}, invalidV2Plan("workgraph authority plan membership")
	}
	return authority.publisher.Publish(ctx, plan)
}

func workgraphPlanMembershipV2(plan journalv2.SealedCommitPlanV2) bool {
	events := plan.Events()
	if len(events) != 2 {
		return false
	}
	if len(plan.Heads()) != 2 || len(plan.Conditions()) != 2+len(plan.EligibilityGuards()) || len(plan.EffectIntents()) != 0 || len(plan.Checkpoints()) != 0 {
		return false
	}
	operation, workgraph := 0, 0
	for _, event := range events {
		switch event.Family() {
		case "operation":
			operation++
		case "workgraph":
			workgraph++
		default:
			return false
		}
	}
	return operation == 1 && workgraph == 1
}
