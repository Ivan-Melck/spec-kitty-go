package gapdb

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

func TestM2OutboxFamilyBoundPublicationCutCampaign(t *testing.T) {
	registry := journalv2.DefaultEventRegistryV2()
	executionSource, effectSource, coordinates := outboxGapDBSourceFixtureV2(t, registry)
	intent, err := controlstore.BindDurableOutboxIntentV2(effectSource, 19, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	command := bindGapDBOutboxCommandV2(t, controlstore.OutboxCommandV2{Action: controlstore.OutboxStartAttemptV2, Intent: intent, AttemptID: "attempt-m2-family-cut", RuntimeEpochID: "epoch-outbox", OperationEventID: "event-m2-outbox-operation", OutboxEventID: "event-m2-outbox"}, "m2-family-cut")
	capability, err := controlstore.BuildOutboxAuthorityPlanV2(command, registry)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := capability.Publication()
	if err != nil {
		t.Fatal(err)
	}
	definitionID := iterationreceipt.DefinitionID("fault/outbox-bound-publication-cuts")
	cuts := []string{"before-publication", "durable-before-acknowledgement", "acknowledgement-before-projection", "lost-acknowledgement", "malformed-acknowledgement"}
	for _, cut := range cuts {
		for trial := 0; trial < 100; trial++ {
			started := time.Now()
			facts, invariants := executeOutboxPublicationCutV2(t, []journalv2.SealedCommitPlanV2{executionSource, effectSource}, capability, plan, cut)
			record, err := iterationreceipt.NewFamilyCut(definitionID, "outbox", cut, trial, fmt.Sprintf("outbox/%s/%03d", cut, trial), plan.Digest().String(), string(plan.PublicationID()), []string{"operation", "outbox"}, "interrupted-reconciled-once", time.Since(started), facts, invariants)
			if err != nil {
				t.Fatal(err)
			}
			iterationreceipt.EmitFamilyCut(t, record)
		}
	}
}

type outboxCutProbeV2 struct {
	publication journalv2.PublicationID
	stage       journalv2.PublicationBoundaryStageV2
	fired       bool
}

func (p *outboxCutProbeV2) PublicationBoundary(_ context.Context, snapshot journalv2.PublicationBoundarySnapshotV2) error {
	if !p.fired && snapshot.Plan.PublicationID() == p.publication && snapshot.Stage == p.stage {
		p.fired = true
		return journalv2.InterruptQualificationPublicationV2(snapshot.Stage)
	}
	return nil
}

type outboxCutPortV2 struct {
	revision    uint64
	target      journalv2.PublicationID
	mode        string
	calls       int
	observation journalv2.CommitObservationV2
}

func (p *outboxCutPortV2) Commit(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	p.calls++
	p.revision++
	observation, err := journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), p.revision, p.revision, plan.MutationCount(), plan.AssertionCount())
	if err != nil {
		return journalv2.CommitObservationV2{}, err
	}
	if plan.PublicationID() != p.target {
		return observation, nil
	}
	p.observation = observation
	switch p.mode {
	case "lost-acknowledgement":
		return journalv2.CommitObservationV2{}, errors.New("qualification outbox acknowledgement lost")
	case "malformed-acknowledgement":
		return journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.RecordSetDigest(), plan.RecordSetDigest(), p.revision, p.revision, plan.MutationCount(), plan.AssertionCount())
	default:
		return observation, nil
	}
}

func executeOutboxPublicationCutV2(t *testing.T, prefix []journalv2.SealedCommitPlanV2, capability controlstore.OutboxAuthorityPlanV2, target journalv2.SealedCommitPlanV2, cut string) (map[string]any, map[string]any) {
	t.Helper()
	tracker, coordinator, port, authority := newOutboxCutReplayV2(t, target, cut)
	for _, prerequisite := range prefix {
		if _, err := coordinator.Publish(t.Context(), prerequisite); err != nil {
			t.Fatal(err)
		}
	}
	beforeCalls, beforeLedger := port.calls, tracker.LedgerSize()
	if _, err := authority.PublishOutbox(t.Context(), capability); err == nil {
		t.Fatalf("outbox cut %s did not interrupt", cut)
	}
	wantCalls := beforeCalls + 1
	if cut == "before-publication" {
		wantCalls = beforeCalls
	}
	if port.calls != wantCalls || coordinator.UnresolvedCount() != 1 || tracker.LedgerSize() != beforeLedger {
		t.Fatalf("outbox cut %s escaped boundary: calls=%d/%d unresolved=%d ledger=%d/%d", cut, port.calls, wantCalls, coordinator.UnresolvedCount(), tracker.LedgerSize(), beforeLedger)
	}
	if cut == "before-publication" {
		tracker, coordinator, port, authority = newOutboxCutReplayV2(t, target, "")
		for _, prerequisite := range prefix {
			if _, err := coordinator.Publish(t.Context(), prerequisite); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := authority.PublishOutbox(t.Context(), capability); err != nil {
			t.Fatal(err)
		}
	} else {
		if port.observation.PublicationID() == "" {
			t.Fatal("outbox cut omitted durable observation")
		}
		foreign, _ := journalv2.NewCommitObservationWithAssertionsV2(target.PublicationID(), target.RecordSetDigest(), target.RecordSetDigest(), port.observation.Revision(), port.observation.DurableThroughRevision(), target.MutationCount(), target.AssertionCount())
		if _, err := coordinator.ReconcileComplete(target, foreign); err == nil {
			t.Fatal("outbox substituted observation reconciled")
		}
		if _, err := coordinator.ReconcileComplete(target, port.observation); err != nil {
			t.Fatal(err)
		}
		if _, err := coordinator.ReconcileComplete(target, port.observation); err == nil {
			t.Fatal("outbox duplicate reconciliation succeeded")
		}
	}
	if coordinator.UnresolvedCount() != 0 || tracker.LedgerSize() != len(prefix)+1 {
		t.Fatal("outbox cut did not reach one terminal projection")
	}
	return map[string]any{"cut": cut, "qualified_family": "outbox", "plan_index": 2, "provider_calls": port.calls, "publication_id": target.PublicationID(), "emitted_families": []string{"operation", "outbox"}}, map[string]any{"unresolved": 0, "ledger_size": tracker.LedgerSize(), "expected_ledger_size": len(prefix) + 1, "one_terminal_projection": true, "substitution_refused": true}
}

func newOutboxCutReplayV2(t *testing.T, target journalv2.SealedCommitPlanV2, cut string) (*journalv2.ProjectionTrackerV2, *journalv2.PublicationCoordinatorV2, *outboxCutPortV2, *outboxAuthorityV2) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	tracker, err := journalv2.NewProjectionTrackerV2(outboxProjectionReducersV2(t, registry), journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	port := &outboxCutPortV2{target: target.PublicationID(), mode: cut}
	var probe journalv2.QualificationPublicationBoundaryProbeV2
	switch cut {
	case "before-publication":
		probe = &outboxCutProbeV2{publication: target.PublicationID(), stage: journalv2.PublicationRegisteredBeforeCommitV2}
	case "durable-before-acknowledgement":
		probe = &outboxCutProbeV2{publication: target.PublicationID(), stage: journalv2.PublicationDurableObservedBeforeAckV2}
	case "acknowledgement-before-projection":
		probe = &outboxCutProbeV2{publication: target.PublicationID(), stage: journalv2.PublicationAckCreatedBeforeProjectionV2}
	}
	coordinator, err := journalv2.NewQualificationPublicationCoordinatorV2(port, tracker, probe)
	if err != nil {
		t.Fatal(err)
	}
	return tracker, coordinator, port, newOutboxAuthorityV2ForTest(coordinator)
}

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
