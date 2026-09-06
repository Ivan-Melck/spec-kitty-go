package gapdb

import (
	"context"
	"errors"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
)

func TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2(t *testing.T) {
	registry := journalv2.DefaultEventRegistryV2()
	_, source, coordinates := outboxGapDBSourceFixtureV2(t, registry)
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

func TestOutboxGapDBFaultsPreserveStableIdentityAndNeverMintPermissionV2(t *testing.T) {
	t.Run("precommit_failure", func(t *testing.T) {
		h := newOutboxGapDBFaultHarnessV2(t, "precommit")
		calls := 0
		h.provider.batchFn = func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
			calls++
			return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Cause: errors.New("failed before commit")}
		}
		if _, err := h.authority.PublishOutbox(t.Context(), h.capability); err == nil {
			t.Fatal("precommit failure fabricated an acknowledgement")
		}
		if h.memory.revision != 19 || calls != 2 || h.coordinator.UnresolvedCount() != 1 {
			t.Fatalf("revision=%d attempts=%d unresolved=%d", h.memory.revision, calls, h.coordinator.UnresolvedCount())
		}
	})

	t.Run("persistent_ambiguity", func(t *testing.T) {
		h := newOutboxGapDBFaultHarnessV2(t, "persistent")
		h.provider.batchFn = func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
			return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: errors.New("outcome unknown")}
		}
		h.provider.getFn = func(context.Context, string) (gapdbapi.Record, error) {
			return gapdbapi.Record{}, &gapdbapi.TransportError{Operation: "get", Ambiguous: true, Cause: errors.New("reconciliation unavailable")}
		}
		if _, err := h.authority.PublishOutbox(t.Context(), h.capability); err == nil {
			t.Fatal("persistent ambiguity fabricated an acknowledgement")
		}
		if h.memory.revision != 19 || h.coordinator.UnresolvedCount() != 1 {
			t.Fatalf("revision=%d unresolved=%d", h.memory.revision, h.coordinator.UnresolvedCount())
		}
		// Without a durable outbox state there is no opaque continuation proof,
		// and therefore no command can mint retry or compensation permission.
		if _, err := controlstore.RecoverDurableOutboxIntentV2(controlstore.OutboxDeliveryStateV2{}); err == nil {
			t.Fatal("unresolved ambiguity minted a continuation intent")
		}
		for _, action := range []controlstore.OutboxActionV2{controlstore.OutboxPermitRetryV2, controlstore.OutboxPermitCompensationV2} {
			command := controlstore.OutboxCommandV2{Action: action, OperationEventID: "event-no-proof-operation", OutboxEventID: "event-no-proof-outbox"}
			if _, err := controlstore.BuildOutboxAuthorityPlanV2(command, journalv2.DefaultEventRegistryV2()); err == nil {
				t.Fatalf("%s authority was minted without durable reconciliation", action)
			}
		}
	})

	t.Run("definitive_absence_replans_same_publication", func(t *testing.T) {
		h := newOutboxGapDBFaultHarnessV2(t, "absent")
		commit := h.provider.batchFn
		calls := 0
		h.provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
			calls++
			if calls == 1 {
				return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: errors.New("lost before apply")}
			}
			return commit(ctx, batch)
		}
		ack, err := h.authority.PublishOutbox(t.Context(), h.capability)
		if err != nil || ack.AuthorityRevision != 20 || calls != 2 || h.coordinator.UnresolvedCount() != 0 {
			t.Fatalf("ack=%+v attempts=%d unresolved=%d err=%v", ack, calls, h.coordinator.UnresolvedCount(), err)
		}
		binding := h.plan.BindingCreate()
		resolved, resolveErr := h.authority.ResolveOutboxOperation(t.Context(), *binding)
		if ack.PublicationID != h.plan.PublicationID() || resolveErr != nil || resolved.Kind != controlstore.OutboxOperationExactV2 || resolved.State.OperationID() != h.plan.OperationState().OperationID() {
			t.Fatalf("replanning changed stable identity: ack=%+v resolution=%+v err=%v", ack, resolved, resolveErr)
		}
	})

	t.Run("malformed_ack_reconciles_committed_publication", func(t *testing.T) {
		h := newOutboxGapDBFaultHarnessV2(t, "malformed")
		commit := h.provider.batchFn
		calls := 0
		h.provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
			calls++
			applied, err := commit(ctx, batch)
			if err == nil {
				applied.MutationCount--
			}
			return applied, err
		}
		ack, err := h.authority.PublishOutbox(t.Context(), h.capability)
		if err != nil || ack.AuthorityRevision != 20 || calls != 1 || h.coordinator.UnresolvedCount() != 0 {
			t.Fatalf("ack=%+v attempts=%d unresolved=%d err=%v", ack, calls, h.coordinator.UnresolvedCount(), err)
		}
	})
}

type outboxGapDBFaultHarnessV2 struct {
	memory      *v2AtomicMemory
	provider    *fakeProvider
	coordinator *journalv2.PublicationCoordinatorV2
	authority   *outboxAuthorityV2
	capability  controlstore.OutboxAuthorityPlanV2
	plan        journalv2.SealedCommitPlanV2
}

func newOutboxGapDBFaultHarnessV2(t *testing.T, suffix string) outboxGapDBFaultHarnessV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	executionSource, source, coordinates := outboxGapDBSourceFixtureV2(t, registry)
	memory := newV2AtomicMemory()
	memory.revision = 17
	for _, guard := range executionSource.EligibilityGuards() {
		key, err := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		if err != nil {
			t.Fatal(err)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	}
	provider := memory.provider()
	provider.statusFn = gapdbMemoryStatusV2(memory, "db-outbox-fault-"+suffix)
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reducers := outboxProjectionReducersV2(t, registry)
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), executionSource); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	intent, err := controlstore.BindDurableOutboxIntentV2(source, 19, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	command := bindGapDBOutboxCommandV2(t, controlstore.OutboxCommandV2{Action: controlstore.OutboxStartAttemptV2, Intent: intent, AttemptID: "attempt-" + suffix, RuntimeEpochID: "epoch-outbox", OperationEventID: journalv2.EventID("event-" + suffix + "-operation"), OutboxEventID: journalv2.EventID("event-" + suffix + "-outbox")}, suffix)
	capability, err := controlstore.BuildOutboxAuthorityPlanV2(command, registry)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := capability.Publication()
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newOutboxAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	return outboxGapDBFaultHarnessV2{memory, provider, coordinator, authority, capability, plan}
}

func bindGapDBOutboxCommandV2(t *testing.T, command controlstore.OutboxCommandV2, suffix string) controlstore.OutboxCommandV2 {
	t.Helper()
	bound, err := controlstore.BindOutboxCommandOperationV2(command, controlstore.OutboxOperationCoordinatesV2{OperationID: journalv2.OperationID("operation-outbox-" + suffix), ProcessID: "process-outbox", DoctrineRevision: "doctrine-outbox", ProfileRevision: "profile-outbox", PolicyRevision: "policy-outbox", ObservedCreatedAt: "2026-09-05T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
