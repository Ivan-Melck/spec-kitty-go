package controlstore

import (
	"context"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type OutboxOperationResolutionKindV2 uint8

const (
	OutboxOperationAbsentV2 OutboxOperationResolutionKindV2 = iota + 1
	OutboxOperationExactV2
	OutboxOperationConflictV2
)

type OutboxOperationResolutionV2 struct {
	Kind           OutboxOperationResolutionKindV2
	State          journalv2.OperationStateV2
	RecordRevision uint64
}
type OutboxStateObservationV2 struct {
	State             *OutboxDeliveryStateV2
	RecordRevision    uint64
	LastPublicationID journalv2.PublicationID
}

// OutboxAuthorityPortV2 is deliberately capability-shaped. It exposes no raw
// key, scan, repository, provider revision, or generic append operation.
type OutboxAuthorityPortV2 interface {
	ResolveOutboxOperation(context.Context, journalv2.OperationBindingV2) (OutboxOperationResolutionV2, error)
	ObserveOutboxState(context.Context, journalv2.AggregateCoordinate) (OutboxStateObservationV2, error)
	PublishOutbox(context.Context, OutboxAuthorityPlanV2) (OutboxPublicationAckV2, error)
}

type OutboxAuthorityPlanV2 struct{ sealed journalv2.SealedCommitPlanV2 }

func (p OutboxAuthorityPlanV2) Publication() (journalv2.SealedCommitPlanV2, error) {
	if p.sealed.Digest().IsZero() {
		return journalv2.SealedCommitPlanV2{}, outboxError("invalid_plan")
	}
	return p.sealed, nil
}

type OutboxPublicationAckV2 struct {
	PublicationID     journalv2.PublicationID
	PlanDigest        journalv2.Digest
	AuthorityRevision uint64
	MutationCount     int
}

func NewOutboxPublicationAckV2(plan OutboxAuthorityPlanV2, ack journalv2.AcknowledgedPublicationV2) (OutboxPublicationAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || ack.PublicationID() != sealed.PublicationID() || ack.PlanDigest() != sealed.Digest() || ack.MutationCount() != sealed.MutationCount() || ack.ProviderRevision() == 0 || ack.DurableThroughRevision() < ack.ProviderRevision() {
		return OutboxPublicationAckV2{}, outboxError("invalid_acknowledgement")
	}
	return OutboxPublicationAckV2{ack.PublicationID(), ack.PlanDigest(), ack.ProviderRevision(), ack.MutationCount()}, nil
}
