package gapdb

import (
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
)

func TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2(t *testing.T) {
	registry := journalv2.DefaultEventRegistryV2()
	source, coordinates := outboxGapDBSourceFixtureV2(t, registry)
	intent, err := controlstore.BindDurableOutboxIntentV2(source, 1, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	command := controlstore.OutboxCommandV2{
		Action: controlstore.OutboxStartAttemptV2, Intent: intent,
		AttemptID: "attempt-adapter", RuntimeEpochID: "epoch-outbox",
		OperationEventID: "event-adapter-operation", OutboxEventID: "event-adapter-outbox",
	}
	command = bindGapDBOutboxCommandV2(t, command, "adapter")
	capability, err := controlstore.BuildOutboxAuthorityPlanV2(command, registry)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := capability.Publication()
	if !outboxTransportMembershipV2(sealed) {
		t.Fatal("exact outbox capability was rejected")
	}
	port := &recordingAuthorityPortV2{}
	authority := newOutboxAuthorityV2ForTest(port)
	_, _ = authority.PublishOutbox(t.Context(), capability)
	if port.calls != 1 || port.plan.Digest() != sealed.Digest() {
		t.Fatalf("calls=%d delegated=%s want=%s", port.calls, port.plan.Digest(), sealed.Digest())
	}
	if _, err := authority.PublishOutbox(t.Context(), controlstore.OutboxAuthorityPlanV2{}); !IsKind(err, OutcomeInvalid) || port.calls != 1 {
		t.Fatalf("unminted plan reached provider: calls=%d err=%v", port.calls, err)
	}
}

func bindGapDBOutboxCommandV2(t *testing.T, command controlstore.OutboxCommandV2, suffix string) controlstore.OutboxCommandV2 {
	t.Helper()
	bound, err := controlstore.BindOutboxCommandOperationV2(command, controlstore.OutboxOperationCoordinatesV2{OperationID: journalv2.OperationID("operation-outbox-" + suffix), ProcessID: "process-outbox", DoctrineRevision: "doctrine-outbox", ProfileRevision: "profile-outbox", PolicyRevision: "policy-outbox", ObservedCreatedAt: "2026-09-05T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
