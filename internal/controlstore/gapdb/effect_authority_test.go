package gapdb

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type gapdbEffectPlanProbeV2 struct {
	plan              execution.EffectAuthorityPlanV2
	slot              execution.ExecutionSlotStateV2
	executionRevision uint64
}

func (p *gapdbEffectPlanProbeV2) ResolveEffectOperation(context.Context, journalv2.OperationBindingV2) (execution.EffectOperationResolutionV2, error) {
	return execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
}
func (p *gapdbEffectPlanProbeV2) ObserveEffectState(context.Context, journalv2.AggregateCoordinate) (execution.EffectStateObservationV2, error) {
	return execution.EffectStateObservationV2{}, nil
}
func (p *gapdbEffectPlanProbeV2) ObserveEffectExecution(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return execution.ExecutionSlotObservationV2{State: &p.slot, RecordRevision: p.executionRevision}, nil
}
func (p *gapdbEffectPlanProbeV2) PublishEffect(_ context.Context, plan execution.EffectAuthorityPlanV2) (execution.EffectPublicationAckV2, error) {
	p.plan = plan
	return execution.EffectPublicationAckV2{}, fmt.Errorf("capture")
}

type gapdbEpochV2 struct{ epoch journalv2.RuntimeEpochID }

func (e gapdbEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return e.epoch, nil
}

func TestEffectAuthorityAdmitsOnlyExactAtomicSemanticPlanV2(t *testing.T) {
	plan := gapdbEffectPlanFixtureV2(t)
	sealed, _ := plan.Publication()
	if !effectPlanMembershipV2(sealed) {
		t.Fatal("valid effect plan rejected")
	}
	port := &recordingAuthorityPortV2{}
	authority := newEffectAuthorityV2ForTest(port)
	_, err := authority.PublishEffect(t.Context(), plan)
	if port.calls != 1 || port.plan.Digest() != sealed.Digest() {
		t.Fatalf("delegation calls=%d err=%v", port.calls, err)
	}
	if _, err := authority.PublishEffect(t.Context(), execution.EffectAuthorityPlanV2{}); !IsKind(err, OutcomeInvalid) || port.calls != 1 {
		t.Fatalf("unminted plan accepted calls=%d err=%v", port.calls, err)
	}
	ids := []string{"M-ADAPTER-POLICY-INTERPRETATION", "M-SPLIT-BATCH"}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if got := fmt.Sprintf("%x", sum); got != "011a6a6fe71480f358f09461e45a0f76576551ee8a00719c634874811854c412" {
		t.Fatalf("mutant census count=%d digest=%s", len(ids), got)
	}
}

func TestWP07RequiredMutantManifestV2(t *testing.T) {
	ids := []string{
		"M-ADAPTER-POLICY-INTERPRETATION",
		"M-BLIND-RETRY",
		"M-EPOCH-FIRST-REPLAY",
		"M-NAKED-INTENT",
		"M-OPTIMISTIC-EFFECT-COMPLETION",
		"M-PROCESS-ONLY-IDENTITY",
		"M-RESULT-BEFORE-DURABLE-ACK",
		"M-SPLIT-BATCH",
		"M-STALE-LEASE",
		"M-UNSAFE-COMPENSATION",
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("required mutant manifest is not canonical: %v", ids)
	}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if got := fmt.Sprintf("%x", sum); got != "08dfc5a22736400f64ada33a547ba1b828df8258cb9b82e6aa01508cdff5dc6d" {
		t.Fatalf("required mutant manifest count=%d digest=%s", len(ids), got)
	}
}

func TestEffectAuthorityCommitsWholePlanThroughGapDBOnceV2(t *testing.T) {
	plan := gapdbEffectPlanFixtureV2(t)
	sealed, _ := plan.Publication()
	memory := newV2AtomicMemory()
	memory.revision = 11
	for _, head := range sealed.Heads() {
		if head.Coordinate.Kind != "execution_slot" {
			continue
		}
		key, err := v2AggregateHeadKey(head.Coordinate)
		if err != nil {
			t.Fatal(err)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), []byte(`{"seed":true}`), gapdbapi.Revision(head.ExpectedRecordRevision), nil)
	}
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := adapter.CommitV2(t.Context(), sealed)
	if err != nil {
		t.Fatal(err)
	}
	if observation.PublicationID() != sealed.PublicationID() || memory.batchCalls != 1 {
		t.Fatalf("publication=%s calls=%d", observation.PublicationID(), memory.batchCalls)
	}
	for _, record := range sealed.Records() {
		key, err := v2KeyForSealedRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := memory.records[key.String()]; !ok {
			t.Fatalf("missing committed record %s", key.String())
		}
	}
}

func TestExecutionAndEffectAuthoritiesProductionConstructorsCommitAndRecoverAmbiguityV2(t *testing.T) {
	for _, test := range []struct {
		name      string
		ambiguous bool
	}{{name: "acknowledged"}, {name: "lost_ack", ambiguous: true}} {
		t.Run(test.name, func(t *testing.T) {
			executionCapability, slot := gapdbExecutionAdmissionPlanFixtureV2(t, "production-"+test.name, "work-production-"+test.name)
			executionPlan, err := executionCapability.Publication()
			if err != nil {
				t.Fatal(err)
			}
			capability := gapdbEffectPlanFixtureForSlotV2(t, "production-"+test.name, slot, 18)
			sealed, err := capability.Publication()
			if err != nil {
				t.Fatal(err)
			}
			memory := newV2AtomicMemory()
			memory.revision = 17
			for _, guard := range executionPlan.EligibilityGuards() {
				key, keyErr := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
				if keyErr != nil {
					t.Fatal(keyErr)
				}
				memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
			}
			provider := memory.provider()
			if test.ambiguous {
				commit := provider.batchFn
				provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
					_, _ = commit(ctx, batch)
					return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
				}
				provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
					memory.mu.Lock()
					defer memory.mu.Unlock()
					return gapdbapi.StatusResult{DatabaseID: "db-effect-production", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
				}
			}
			adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			projection, err := gapdbExecutionProjectionTrackerV2()
			if err != nil {
				t.Fatal(err)
			}
			coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
			if err != nil {
				t.Fatal(err)
			}
			authority, err := newEffectAuthorityV2(adapter, coordinator)
			if err != nil {
				t.Fatal(err)
			}
			executionAuthority, err := newExecutionAuthorityV2(adapter, coordinator)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executionAuthority.PublishExecution(t.Context(), executionCapability); err != nil {
				t.Fatal(err)
			}
			ack, err := authority.PublishEffect(t.Context(), capability)
			if err != nil {
				t.Fatal(err)
			}
			if coordinator.UnresolvedCount() != 0 || memory.batchCalls != 2 {
				t.Fatalf("unresolved=%d batch_calls=%d ack=%+v", coordinator.UnresolvedCount(), memory.batchCalls, ack)
			}
			prefix, err := coordinator.SealProjectionPrefix()
			if err != nil || prefix.CoveredThroughPublicationID() != sealed.PublicationID() {
				t.Fatalf("covered=%s want=%s err=%v", prefix.CoveredThroughPublicationID(), sealed.PublicationID(), err)
			}
		})
	}
}

func gapdbExecutionProjectionTrackerV2() (*journalv2.ProjectionTrackerV2, error) {
	registry := journalv2.DefaultEventRegistryV2()
	owned := append(execution.ExecutionReducerRegistrationsV2(), execution.EffectReducerRegistrationsV2()...)
	byFamily := make(map[string]journalv2.ReducerRegistration, len(owned))
	for _, registration := range owned {
		byFamily[registration.Family] = registration
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "operation" {
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2})
			continue
		}
		if registration, ok := byFamily[tuple.Family]; ok {
			registrations = append(registrations, registration)
			continue
		}
		sum := sha256.Sum256([]byte("test-only-unused-reducer/" + tuple.Family))
		digest, _ := journalv2.ParseDigest("test_reducer", fmt.Sprintf("%x", sum))
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		return nil, err
	}
	return journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
}

func TestEffectAuthoritySameSlotAndUnrelatedSlotContentionCampaignV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		left := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("left-%03d", trial), "work-shared")
		right := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("right-%03d", trial), "work-shared")
		leftSealed, _ := left.Publication()
		rightSealed, _ := right.Publication()
		memory := newV2AtomicMemory()
		seedEffectExecutionHeadsV2(t, memory, leftSealed, rightSealed)
		adapter, _ := newAdapter(memory.provider(), "/explicit/socket", time.Second)
		errs := commitEffectPlansTogetherV2(t, adapter, leftSealed, rightSealed)
		winners := 0
		var winner journalv2.SealedCommitPlanV2
		for index, err := range errs {
			if err == nil {
				winners++
				winner = []journalv2.SealedCommitPlanV2{leftSealed, rightSealed}[index]
			}
		}
		if winners != 1 {
			t.Fatalf("trial %d same-slot winners=%d errors=%v", trial, winners, errs)
		}
		var executionHead journalv2.HeadTransitionV2
		for _, head := range winner.Heads() {
			if head.Coordinate.Kind == "execution_slot" {
				executionHead = head
			}
		}
		key, _ := v2AggregateHeadKey(executionHead.Coordinate)
		if record := memory.records[key.String()]; record.Revision == 0 {
			t.Fatalf("trial %d lost winning execution authority", trial)
		}

		unrelatedLeft := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("unrelated-left-%03d", trial), "work-left")
		unrelatedRight := gapdbEffectPlanFixtureNamedV2(t, fmt.Sprintf("unrelated-right-%03d", trial), "work-right")
		unrelatedLeftSealed, _ := unrelatedLeft.Publication()
		unrelatedRightSealed, _ := unrelatedRight.Publication()
		memory = newV2AtomicMemory()
		seedEffectExecutionHeadsV2(t, memory, unrelatedLeftSealed, unrelatedRightSealed)
		adapter, _ = newAdapter(memory.provider(), "/explicit/socket", time.Second)
		for index, err := range commitEffectPlansTogetherV2(t, adapter, unrelatedLeftSealed, unrelatedRightSealed) {
			if err != nil {
				t.Fatalf("trial %d unrelated plan %d failed: %v", trial, index, err)
			}
		}
	}
}

func TestAuthorityAdapterReconcilesRegisteredAmbiguousPublicationV2(t *testing.T) {
	artifactPlan := gapdbArtifactPlanV2(t)
	plan, err := artifactPlan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	memory := newV2AtomicMemory()
	provider := memory.provider()
	commit := provider.batchFn
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		_, _ = commit(ctx, batch)
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
	}
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "db-authority-reconcile", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := gapdbAuthorityProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), plan); err == nil || coordinator.UnresolvedCount() != 1 {
		t.Fatalf("ambiguous publish err=%v unresolved=%d", err, coordinator.UnresolvedCount())
	}
	ack, err := reconcileRegisteredPublicationV2(t.Context(), adapter, coordinator, plan)
	if err != nil || ack.PublicationID() != plan.PublicationID() || coordinator.UnresolvedCount() != 0 {
		t.Fatalf("ack=%s unresolved=%d err=%v", ack.PublicationID(), coordinator.UnresolvedCount(), err)
	}
	prefix, err := coordinator.SealProjectionPrefix()
	if err != nil || prefix.CoveredThroughPublicationID() != plan.PublicationID() {
		t.Fatalf("prefix covered=%s err=%v", prefix.CoveredThroughPublicationID(), err)
	}
}

func TestAuthorityAdapterNeverDeliversAfterPreRegistrationRefusalV2(t *testing.T) {
	capability := gapdbEffectPlanFixtureV2(t)
	plan, _ := capability.Publication()
	batchCalls := 0
	provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		batchCalls++
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, err := gapdbAuthorityProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, projection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), plan); err == nil || batchCalls != 1 {
		t.Fatalf("failed to establish registered publication: calls=%d err=%v", batchCalls, err)
	}
	authority, err := newEffectAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.PublishEffect(t.Context(), capability); err == nil || batchCalls != 1 {
		t.Fatalf("pre-registration refusal reached provider: calls=%d err=%v", batchCalls, err)
	}
}

func commitEffectPlansTogetherV2(t *testing.T, adapter *Adapter, plans ...journalv2.SealedCommitPlanV2) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, len(plans))
	var workers sync.WaitGroup
	workers.Add(len(plans))
	for index := range plans {
		go func(index int) {
			defer workers.Done()
			<-start
			_, errs[index] = adapter.CommitV2(t.Context(), plans[index])
		}(index)
	}
	close(start)
	workers.Wait()
	return errs
}

func seedEffectExecutionHeadsV2(t *testing.T, memory *v2AtomicMemory, plans ...journalv2.SealedCommitPlanV2) {
	t.Helper()
	memory.revision = 11
	for _, plan := range plans {
		for _, head := range plan.Heads() {
			if head.Coordinate.Kind != "execution_slot" {
				continue
			}
			key, err := v2AggregateHeadKey(head.Coordinate)
			if err != nil {
				t.Fatal(err)
			}
			memory.records[key.String()] = gapdbapi.NewRecord(key.String(), []byte(`{"seed":true}`), gapdbapi.Revision(head.ExpectedRecordRevision), nil)
		}
	}
}

func gapdbEffectPlanFixtureV2(t *testing.T) execution.EffectAuthorityPlanV2 {
	return gapdbEffectPlanFixtureNamedV2(t, "gapdb", "work-gapdb")
}

func gapdbEffectPlanFixtureNamedV2(t *testing.T, suffix string, workID string) execution.EffectAuthorityPlanV2 {
	t.Helper()
	governance := gapdbExecutionGovernanceV2(t)
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	slot := execution.ExecutionSlotStateV2{SchemaVersion: 1, ProjectID: "project-gapdb", MissionID: "mission-gapdb", WorkRevisionID: "revision-gapdb", WorkID: ids.WorkID(workID), LogicalVersion: 1, RunID: "run-gapdb", ActorID: "actor-gapdb", ProcessID: "process-gapdb", LeaseID: "lease-gapdb", Generation: 1, Fence: "gapdb-fence-0123456789", HeartbeatAt: now.Format(time.RFC3339Nano), Deadline: now.Add(time.Hour).Format(time.RFC3339Nano), RuntimeEpochID: "epoch-gapdb", Status: execution.ExecutionActiveV2, Governance: governance}
	return gapdbEffectPlanFixtureForSlotV2(t, suffix, slot, 11)
}

func gapdbEffectPlanFixtureForSlotV2(t *testing.T, suffix string, slot execution.ExecutionSlotStateV2, executionRevision uint64) execution.EffectAuthorityPlanV2 {
	t.Helper()
	now, err := time.Parse(time.RFC3339Nano, slot.HeartbeatAt)
	if err != nil {
		t.Fatal(err)
	}
	governance := slot.Governance
	target := tracker.ExternalWorkRef{ProjectID: slot.ProjectID, WorkID: slot.WorkID, Provider: "fake", ExternalID: "external-" + suffix, MetadataSchema: 1, CreatedByOperation: ids.OperationID("operation-create-" + suffix), LastObservation: 1}
	intent := execution.EffectIntentV2{EffectID: journalv2.EffectID("effect-" + suffix), OperationID: journalv2.OperationID("operation-effect-" + suffix), Target: target, IntendedValue: "actor-gapdb", ExpectedProviderRevision: 1, ExpectedOperationMarker: "", RequiredGuarantee: tracker.AssignmentAtomicClaim, ActorID: slot.ActorID, ProcessID: slot.ProcessID, Governance: governance, OriginRuntimeEpochID: slot.RuntimeEpochID, OriginObservedCreatedAt: now.Add(time.Second).Format(time.RFC3339)}
	intent.CanonicalRequestDigest, _ = execution.EffectRequestDigestV2(intent)
	probe := &gapdbEffectPlanProbeV2{slot: slot, executionRevision: executionRevision}
	service, err := execution.NewEffectAuthorityServiceV2(probe, gapdbEpochV2{slot.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.Execute(t.Context(), execution.EffectAuthorityRequestV2{Action: execution.EffectRecordIntentV2, Intent: intent, RuntimeEpochID: slot.RuntimeEpochID, ExecutionSlot: slot, ObservedAt: now.Add(time.Second), OperationEventID: journalv2.EventID("event-effect-" + suffix + "-op"), EffectEventID: journalv2.EventID("event-effect-" + suffix)})
	if _, err := probe.plan.Publication(); err != nil {
		t.Fatal(err)
	}
	return probe.plan
}

func gapdbExecutionGovernanceV2(t *testing.T) execution.ExecutionGovernanceV2 {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind          governancecontext.EntryKind
		ref, revision string
	}{{governancecontext.KindConfig, "config", "policy-r1"}, {governancecontext.KindDoctrine, "doctrine", "doctrine-r1"}, {governancecontext.KindProfile, "profile", "profile-r1"}, {governancecontext.KindMissionType, "mission", "mission-r1"}} {
		sum := sha256.Sum256([]byte(item.ref))
		commitment, _ := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, fmt.Sprintf("%x", sum))
		entry, err := governancecontext.NewEntry(item.kind, item.ref, item.revision, commitment)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	ctx, err := governancecontext.NewContext("governance-gapdb", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	value, err := execution.ExecutionGovernanceFromContextV2(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
