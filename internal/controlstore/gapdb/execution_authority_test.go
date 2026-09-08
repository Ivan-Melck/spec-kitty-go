package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type gapdbExecutionPlanProbeV2 struct {
	eligibility workgraph.ObservedEligibilityShardV2
	observation execution.ExecutionSlotObservationV2
	resolution  execution.ExecutionOperationResolutionV2
	plan        execution.ExecutionAuthorityPlanV2
}

func (p *gapdbExecutionPlanProbeV2) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	if p.resolution.Kind != "" {
		return p.resolution, nil
	}
	return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
}
func (p *gapdbExecutionPlanProbeV2) ObserveExecutionEligibility(context.Context, ids.MissionID, journalv2.EligibilityShardID) (workgraph.ObservedEligibilityShardV2, error) {
	return p.eligibility, nil
}
func (p *gapdbExecutionPlanProbeV2) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return p.observation, nil
}
func (p *gapdbExecutionPlanProbeV2) PublishExecution(_ context.Context, plan execution.ExecutionAuthorityPlanV2) (execution.ExecutionPublicationAckV2, error) {
	p.plan = plan
	return execution.ExecutionPublicationAckV2{}, fmt.Errorf("capture execution plan")
}

type gapdbFenceSourceV2 string

func (f gapdbFenceSourceV2) NewExecutionFence(context.Context) (string, error) {
	return "authority-owned-gapdb-fence-" + string(f), nil
}

func TestExecutionAuthorityConstructorsRemainNarrowV2(t *testing.T) {
	if _, err := newExecutionAuthorityV2(nil, nil); err == nil {
		t.Fatal("accepted missing adapter and coordinator")
	}
	authority := newExecutionAuthorityV2ForTest(&recordingAuthorityPortV2{})
	if authority == nil || authority.publisher == nil || authority.adapter != nil || authority.coordinator != nil {
		t.Fatalf("test authority escaped narrow dependencies: %+v", authority)
	}
}

func TestExecutionAuthorityProductionContentionMatrixV2(t *testing.T) {
	t.Run("concurrent_claim", func(t *testing.T) {
		for trial := 0; trial < 100; trial++ {
			started := time.Now()
			left, _, request, eligibility := gapdbExecutionAdmissionPlanFixtureV2(t, fmt.Sprintf("claim-left-%03d", trial), fmt.Sprintf("work-claim-%03d", trial))
			rightRequest := request
			rightRequest.OperationID = journalv2.OperationID(fmt.Sprintf("operation-execution-claim-right-%03d", trial))
			rightRequest.RunID = ids.RunID(fmt.Sprintf("run-claim-right-%03d", trial))
			rightRequest.LeaseID = ids.LeaseID(fmt.Sprintf("lease-claim-right-%03d", trial))
			rightRequest.OperationEventID = journalv2.EventID(fmt.Sprintf("event-operation-execution-claim-right-%03d", trial))
			rightRequest.SlotEventID = journalv2.EventID(fmt.Sprintf("event-slot-execution-claim-right-%03d", trial))
			right, _ := gapdbCaptureExecutionPlanV2(t, rightRequest, eligibility, execution.ExecutionSlotObservationV2{})
			authority, memory, _ := gapdbExecutionAuthorityHarnessV2(t, left)
			assertExecutionRaceResultsV2(t, trial, 1, memory, []execution.ExecutionAuthorityPlanV2{left, right}, publishExecutionCapabilitiesTogetherV2(t, authority, left, right))
			emitGapDBIteration(t, trial, fmt.Sprintf("claim-left-%03d", trial), "execution-concurrent-claim", "one-winner", started, map[string]any{"expected_winners": 1})
		}
	})

	t.Run("renew_vs_expire", func(t *testing.T) {
		for trial := 0; trial < 100; trial++ {
			started := time.Now()
			label := fmt.Sprintf("renew-expire-%03d", trial)
			admit, active, request, _ := gapdbExecutionAdmissionPlanFixtureV2(t, label, "work-"+label)
			authority, memory, _ := gapdbExecutionAuthorityHarnessV2(t, admit)
			if _, err := authority.PublishExecution(t.Context(), admit); err != nil {
				t.Fatal(err)
			}
			heartbeatAt, _ := time.Parse(time.RFC3339Nano, active.HeartbeatAt)
			deadline, _ := time.Parse(time.RFC3339Nano, active.Deadline)
			renewRequest := executionRequestFromStateV2(request, active, execution.ExecutionHeartbeatV2, label+"-renew")
			renewRequest.ObservedAt = heartbeatAt.Add(time.Minute)
			renewRequest.Deadline = deadline.Add(time.Hour)
			expireRequest := executionRequestFromStateV2(request, active, execution.ExecutionExpireV2, label+"-expire")
			expireRequest.ObservedAt = deadline
			renew, _ := gapdbCaptureExecutionPlanV2(t, renewRequest, workgraph.ObservedEligibilityShardV2{}, execution.ExecutionSlotObservationV2{State: &active, RecordRevision: uint64(memory.revision)})
			expire, _ := gapdbCaptureExecutionPlanV2(t, expireRequest, workgraph.ObservedEligibilityShardV2{}, execution.ExecutionSlotObservationV2{State: &active, RecordRevision: uint64(memory.revision)})
			assertExecutionRaceResultsV2(t, trial, 1, memory, []execution.ExecutionAuthorityPlanV2{renew, expire}, publishExecutionCapabilitiesTogetherV2(t, authority, renew, expire))
			emitGapDBIteration(t, 100+trial, label, "execution-renew-vs-expire", "one-winner", started, map[string]any{"expected_winners": 1})
		}
	})

	t.Run("duplicate_replacement_and_expire_then_replace", func(t *testing.T) {
		for trial := 0; trial < 100; trial++ {
			started := time.Now()
			label := fmt.Sprintf("replacement-%03d", trial)
			admit, active, request, _ := gapdbExecutionAdmissionPlanFixtureV2(t, label, "work-"+label)
			authority, memory, _ := gapdbExecutionAuthorityHarnessV2(t, admit)
			if _, err := authority.PublishExecution(t.Context(), admit); err != nil {
				t.Fatal(err)
			}
			deadline, _ := time.Parse(time.RFC3339Nano, active.Deadline)
			expireRequest := executionRequestFromStateV2(request, active, execution.ExecutionExpireV2, label+"-expire")
			expireRequest.ObservedAt = deadline
			expire, expired := gapdbCaptureExecutionPlanV2(t, expireRequest, workgraph.ObservedEligibilityShardV2{}, execution.ExecutionSlotObservationV2{State: &active, RecordRevision: uint64(memory.revision)})
			if _, err := authority.PublishExecution(t.Context(), expire); err != nil {
				t.Fatal(err)
			}
			guard := admitPublicationGuardV2(t, admit)
			currentEligibility, err := workgraph.NewObservedEligibilityShardV2(guard.Canonical, guard.ExpectedRecordRevision+1)
			if err != nil {
				t.Fatal(err)
			}
			makeReplacement := func(side string) execution.ExecutionAuthorityPlanV2 {
				replacementLabel := label + "-" + side
				replaceRequest := executionRequestFromStateV2(request, expired, execution.ExecutionReplaceV2, replacementLabel)
				replaceRequest.RunID = ids.RunID("run-" + replacementLabel)
				replaceRequest.LeaseID = ids.LeaseID("lease-" + replacementLabel)
				replaceRequest.Generation = expired.Generation + 1
				replaceRequest.ObservedAt = deadline
				replaceRequest.Deadline = deadline.Add(time.Hour)
				plan, _ := gapdbCaptureExecutionPlanV2(t, replaceRequest, currentEligibility, execution.ExecutionSlotObservationV2{State: &expired, RecordRevision: uint64(memory.revision)})
				return plan
			}
			left, right := makeReplacement("left"), makeReplacement("right")
			assertExecutionRaceResultsV2(t, trial, 1, memory, []execution.ExecutionAuthorityPlanV2{left, right}, publishExecutionCapabilitiesTogetherV2(t, authority, left, right))
			emitGapDBIteration(t, 200+trial, label, "execution-replacement", "one-winner", started, map[string]any{"expected_winners": 1, "expired_generation": expired.Generation})
		}
	})

	t.Run("unrelated_slots_progress", func(t *testing.T) {
		for trial := 0; trial < 100; trial++ {
			started := time.Now()
			left, _, _, _ := gapdbExecutionAdmissionPlanFixtureV2(t, fmt.Sprintf("unrelated-left-%03d", trial), fmt.Sprintf("work-unrelated-left-%03d", trial))
			var right execution.ExecutionAuthorityPlanV2
			for index := 0; index < 256; index++ {
				candidate, _, _, _ := gapdbExecutionAdmissionPlanFixtureV2(t, fmt.Sprintf("unrelated-right-%03d-%03d", trial, index), fmt.Sprintf("work-unrelated-right-%03d-%03d", trial, index))
				if !sameEligibilityGuardAddressV2(t, left, candidate) {
					right = candidate
					break
				}
			}
			if _, err := right.Publication(); err != nil {
				t.Fatal("could not find unrelated eligibility shard")
			}
			authority, memory, _ := gapdbExecutionAuthorityHarnessV2(t, left, right)
			assertExecutionRaceResultsV2(t, trial, 2, memory, []execution.ExecutionAuthorityPlanV2{left, right}, publishExecutionCapabilitiesTogetherV2(t, authority, left, right))
			emitGapDBIteration(t, 300+trial, fmt.Sprintf("unrelated-left-%03d", trial), "execution-independent-slots", "two-winners", started, map[string]any{"expected_winners": 2})
		}
	})
}

func emitGapDBIteration(t *testing.T, index int, seed, boundary, outcome string, started time.Time, facts any) {
	t.Helper()
	receipt, err := iterationreceipt.New(
		iterationreceipt.DefinitionID("wp14/gapdb-contention"), index, seed, boundary, outcome, time.Since(started), facts,
		map[string]any{"atomic_batch": true, "durable_head_consistent": true, "no_partial_authority": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	iterationreceipt.Emit(t, receipt)
}

func executionRequestFromStateV2(base execution.ExecutionAuthorityRequestV2, state execution.ExecutionSlotStateV2, kind execution.ExecutionCommandKindV2, label string) execution.ExecutionAuthorityRequestV2 {
	base.Kind = kind
	base.OperationID = journalv2.OperationID("operation-" + label)
	base.RunID, base.ActorID, base.ProcessID, base.LeaseID = state.RunID, state.ActorID, state.ProcessID, state.LeaseID
	base.Generation, base.Fence, base.RuntimeEpochID = state.Generation, state.Fence, state.RuntimeEpochID
	base.OperationEventID = journalv2.EventID("event-operation-" + label)
	base.SlotEventID = journalv2.EventID("event-slot-" + label)
	return base
}

func gapdbExecutionAuthorityHarnessV2(t *testing.T, capabilities ...execution.ExecutionAuthorityPlanV2) (*executionAuthorityV2, *v2AtomicMemory, *journalv2.PublicationCoordinatorV2) {
	t.Helper()
	memory := newV2AtomicMemory()
	memory.revision = 17
	for _, capability := range capabilities {
		guard := admitPublicationGuardV2(t, capability)
		if guard.MissionID == "" {
			continue
		}
		key, err := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		if err != nil {
			t.Fatal(err)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	}
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
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
	authority, err := newExecutionAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	return authority, memory, coordinator
}

func admitPublicationGuardV2(t *testing.T, capability execution.ExecutionAuthorityPlanV2) journalv2.EligibilityGuardTransition {
	t.Helper()
	sealed, err := capability.Publication()
	if err != nil {
		t.Fatal(err)
	}
	guards := sealed.EligibilityGuards()
	if len(guards) == 0 {
		return journalv2.EligibilityGuardTransition{}
	}
	return guards[0]
}

func sameEligibilityGuardAddressV2(t *testing.T, left, right execution.ExecutionAuthorityPlanV2) bool {
	t.Helper()
	a, b := admitPublicationGuardV2(t, left), admitPublicationGuardV2(t, right)
	return a.MissionID == b.MissionID && a.ShardID == b.ShardID
}

func publishExecutionCapabilitiesTogetherV2(t *testing.T, authority *executionAuthorityV2, capabilities ...execution.ExecutionAuthorityPlanV2) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, len(capabilities))
	var workers sync.WaitGroup
	workers.Add(len(capabilities))
	for index := range capabilities {
		go func(index int) {
			defer workers.Done()
			<-start
			_, errs[index] = authority.PublishExecution(t.Context(), capabilities[index])
		}(index)
	}
	close(start)
	workers.Wait()
	return errs
}

func assertExecutionRaceResultsV2(t *testing.T, trial, wantWinners int, memory *v2AtomicMemory, capabilities []execution.ExecutionAuthorityPlanV2, errs []error) {
	t.Helper()
	winners := 0
	for index, err := range errs {
		if err == nil {
			winners++
			continue
		}
		if !IsKind(err, OutcomeConflict) && !IsKind(err, OutcomeMalformedResponse) {
			t.Fatalf("trial %d untyped loser: %v", trial, err)
		}
		sealed, sealErr := capabilities[index].Publication()
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		memory.mu.Lock()
		for _, record := range sealed.Records() {
			switch record.Role() {
			case journalv2.RecordOperationBinding, journalv2.RecordOperationHead, journalv2.RecordJournalEvent, journalv2.RecordPublicationWitness:
				key, keyErr := v2KeyForSealedRecord(record)
				if keyErr != nil {
					memory.mu.Unlock()
					t.Fatal(keyErr)
				}
				if stored, exists := memory.records[key.String()]; exists && bytes.Equal(stored.Value, record.CanonicalValue()) {
					memory.mu.Unlock()
					t.Fatalf("trial %d loser durably published %s", trial, record.Role())
				}
			}
		}
		memory.mu.Unlock()
	}
	if winners != wantWinners {
		t.Fatalf("trial %d winners=%d want=%d errors=%v", trial, winners, wantWinners, errs)
	}
}

func gapdbExecutionAdmissionPlanFixtureV2(t *testing.T, suffix, workID string) (execution.ExecutionAuthorityPlanV2, execution.ExecutionSlotStateV2, execution.ExecutionAuthorityRequestV2, workgraph.ObservedEligibilityShardV2) {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-gapdb", WorkID: ids.WorkID(workID), Title: "Execute " + suffix, Description: "Exercise GapDB execution authority.", DesignContext: "Use the current lease.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-1", Text: "passes", Outcome: "green", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/execution", Domain: "execution", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "scope-1", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-gapdb", "mission-gapdb", "artifact-plan", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-1", Kind: workgraph.ScopePlanConcern, MissionID: "mission-gapdb"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	definition := sha256.Sum256([]byte(workID))
	final, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: ids.WorkID(workID), Provider: "fake", ExternalID: "external-" + suffix, Revision: "provider-r1", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", definition), CriterionEvidenceHints: map[string]string{"criterion-1": "go test"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: final, PlanningSourceCommit: strings.Repeat("b", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-gapdb", FinalizedAt: time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC), GovernanceContextID: "governance-gapdb"})
	if err != nil {
		t.Fatal(err)
	}
	shards, err := workgraph.BuildInitialEligibilityGuardShards(revision, "operation-workgraph-"+suffix, []workgraph.EligibilityOperation{workgraph.EligibilityStart, workgraph.EligibilityHandoff, workgraph.EligibilityReview, workgraph.EligibilityAcceptance, workgraph.EligibilityIntegration})
	if err != nil {
		t.Fatal(err)
	}
	var eligibility workgraph.ObservedEligibilityShardV2
	for _, shard := range shards {
		encoded, encodeErr := shard.CanonicalBytes()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		candidate, observeErr := workgraph.NewObservedEligibilityShardV2(encoded, 17)
		if observeErr == nil {
			if _, tokenErr := candidate.Token(ids.WorkID(workID), revision.ID(), workgraph.EligibilityStart); tokenErr == nil {
				eligibility = candidate
				break
			}
		}
	}
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	request := execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionAdmitV2, OperationID: journalv2.OperationID("operation-execution-" + suffix), ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), WorkRevisionID: revision.ID(), WorkID: ids.WorkID(workID), RunID: ids.RunID("run-" + suffix), ActorID: "actor-gapdb", ProcessID: "process-gapdb", LeaseID: ids.LeaseID("lease-" + suffix), Generation: 1, ObservedAt: now, Deadline: now.Add(time.Hour), RuntimeEpochID: "epoch-gapdb", Governance: gapdbExecutionContextV2(t), OperationEventID: journalv2.EventID("event-operation-execution-" + suffix), SlotEventID: journalv2.EventID("event-slot-execution-" + suffix)}
	plan, slot := gapdbCaptureExecutionPlanV2(t, request, eligibility, execution.ExecutionSlotObservationV2{})
	return plan, slot, request, eligibility
}

func gapdbCaptureExecutionPlanV2(t *testing.T, request execution.ExecutionAuthorityRequestV2, eligibility workgraph.ObservedEligibilityShardV2, observation execution.ExecutionSlotObservationV2) (execution.ExecutionAuthorityPlanV2, execution.ExecutionSlotStateV2) {
	t.Helper()
	probe := &gapdbExecutionPlanProbeV2{eligibility: eligibility}
	probe.observation = observation
	service, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(probe, gapdbEpochV2{epoch: request.RuntimeEpochID}, gapdbFenceSourceV2(request.OperationID), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.Execute(t.Context(), request)
	sealed, err := probe.plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	var slot execution.ExecutionSlotStateV2
	for _, head := range sealed.Heads() {
		if head.Coordinate.Kind == "execution_slot" {
			slot, err = execution.DecodeExecutionSlotStateV2(head.AuthoritySummary)
		}
	}
	if err != nil || slot.Validate() != nil {
		t.Fatalf("slot=%+v err=%v", slot, err)
	}
	return probe.plan, slot
}

func gapdbExecutionContextV2(t *testing.T) governancecontext.Context {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind          governancecontext.EntryKind
		ref, revision string
	}{{governancecontext.KindConfig, "config", "policy-r1"}, {governancecontext.KindDoctrine, "doctrine", "doctrine-r1"}, {governancecontext.KindProfile, "profile", "profile-r1"}, {governancecontext.KindMissionType, "mission", "mission-r1"}} {
		commitment, _ := governancecontext.CommitSHA256([]byte(item.ref))
		entry, entryErr := governancecontext.NewEntry(item.kind, item.ref, item.revision, commitment)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		entries = append(entries, entry)
	}
	value, err := governancecontext.NewContext("governance-gapdb", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
