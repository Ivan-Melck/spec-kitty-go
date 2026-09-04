package execution

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type executionPortProbeV2 struct {
	resolution  ExecutionOperationResolutionV2
	eligibility workgraph.ObservedEligibilityShardV2
	observation ExecutionSlotObservationV2
	trace       []string
	plans       []ExecutionAuthorityPlanV2
	publishErr  error
}

func (p *executionPortProbeV2) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (ExecutionOperationResolutionV2, error) {
	p.trace = append(p.trace, "resolve")
	return p.resolution, nil
}
func (p *executionPortProbeV2) ObserveExecutionEligibility(context.Context, ids.MissionID, journalv2.EligibilityShardID) (workgraph.ObservedEligibilityShardV2, error) {
	p.trace = append(p.trace, "eligibility")
	return p.eligibility, nil
}
func (p *executionPortProbeV2) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (ExecutionSlotObservationV2, error) {
	p.trace = append(p.trace, "slot")
	return p.observation, nil
}
func (p *executionPortProbeV2) PublishExecution(_ context.Context, plan ExecutionAuthorityPlanV2) (ExecutionPublicationAckV2, error) {
	p.trace = append(p.trace, "publish")
	p.plans = append(p.plans, plan)
	if p.publishErr != nil {
		return ExecutionPublicationAckV2{}, p.publishErr
	}
	sealed, _ := plan.Publication()
	return ExecutionPublicationAckV2{sealed.PublicationID(), sealed.Digest(), sealed.MutationCount(), 17}, nil
}

type executionEpochProbeV2 struct {
	epoch journalv2.RuntimeEpochID
	calls int
}
type fixedFenceSourceV2 string

func (f fixedFenceSourceV2) NewExecutionFence(context.Context) (string, error) { return string(f), nil }

func (p *executionEpochProbeV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	p.calls++
	return p.epoch, nil
}

func TestExecutionAuthorityAdmitsWithExactEligibilityAndOneBatchV2(t *testing.T) {
	request, eligibility := executionRequestFixtureV2(t, ExecutionAdmitV2)
	port := &executionPortProbeV2{resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}, eligibility: eligibility}
	epoch := &executionEpochProbeV2{epoch: request.RuntimeEpochID}
	service, err := NewExecutionAuthorityServiceV2(port, epoch, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), request)
	if err != nil || result.State.Status != ExecutionActiveV2 || result.State.Generation != 1 || result.Replayed {
		t.Fatalf("admit=%+v err=%v", result, err)
	}
	if strings.Join(port.trace, ",") != "resolve,slot,eligibility,publish" || epoch.calls != 1 || len(port.plans) != 1 {
		t.Fatalf("ordering trace=%v epoch=%d plans=%d", port.trace, epoch.calls, len(port.plans))
	}
	sealed, err := port.plans[0].Publication()
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed.Events()) != 2 || len(sealed.Heads()) != 2 || len(sealed.EligibilityGuards()) != 1 || len(sealed.Conditions()) != 3 || sealed.MutationCount() != 7 {
		t.Fatalf("incomplete atomic membership events=%d heads=%d guards=%d conditions=%d mutations=%d", len(sealed.Events()), len(sealed.Heads()), len(sealed.EligibilityGuards()), len(sealed.Conditions()), sealed.MutationCount())
	}
	var slotEvent journalv2.EventEnvelopeV2
	for _, event := range sealed.Events() {
		if event.Family() == "execution_slot" {
			slotEvent = event
		}
	}
	reduced, err := ReduceExecutionSlotEventV2(nil, slotEvent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reduced), request.LeaseID.String()) {
		t.Fatal("reducer lost lease authority")
	}
}

func TestExecutionAuthorityReplayAndCollisionPrecedeEpochAndCapabilityReadsV2(t *testing.T) {
	request, eligibility := executionRequestFixtureV2(t, ExecutionAdmitV2)
	seedPort := &executionPortProbeV2{resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}, eligibility: eligibility}
	seedEpoch := &executionEpochProbeV2{epoch: request.RuntimeEpochID}
	service, _ := NewExecutionAuthorityServiceV2(seedPort, seedEpoch, journalv2.DefaultEventRegistryV2())
	if _, err := service.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	sealed, _ := seedPort.plans[0].Publication()

	for _, test := range []struct {
		name       string
		resolution ExecutionOperationResolutionV2
		wantReplay bool
		wantKind   ErrorKind
	}{
		{"terminal replay", ExecutionOperationResolutionV2{Kind: ExecutionOperationExactV2, State: sealed.OperationState()}, true, ""},
		{"binding collision", ExecutionOperationResolutionV2{Kind: ExecutionOperationConflictV2}, false, KindIdempotencyConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			port := &executionPortProbeV2{resolution: test.resolution}
			epochs := &executionEpochProbeV2{epoch: "epoch-rotated"}
			svc, _ := NewExecutionAuthorityServiceV2(port, epochs, journalv2.DefaultEventRegistryV2())
			result, err := svc.Execute(t.Context(), request)
			if test.wantReplay {
				if err != nil || !result.Replayed || result.State.Governance.AggregateDigest == "" {
					t.Fatalf("replay=%+v err=%v", result, err)
				}
			} else if !IsKind(err, test.wantKind) {
				t.Fatalf("err=%v", err)
			}
			if epochs.calls != 0 || strings.Join(port.trace, ",") != "resolve" {
				t.Fatalf("crossed authority boundary epoch=%d trace=%v", epochs.calls, port.trace)
			}
		})
	}

	stalePort := &executionPortProbeV2{resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}}
	staleEpoch := &executionEpochProbeV2{epoch: "epoch-replacement"}
	staleService, _ := NewExecutionAuthorityServiceV2(stalePort, staleEpoch, journalv2.DefaultEventRegistryV2())
	if _, err := staleService.Execute(t.Context(), request); !IsKind(err, KindStaleAuthority) {
		t.Fatalf("absent predecessor err=%v", err)
	}
	if strings.Join(stalePort.trace, ",") != "resolve" {
		t.Fatalf("stale epoch read capability: %v", stalePort.trace)
	}
}

func TestExecutionLeaseTransitionsFenceStaleAuthorityAndKeepReplacementDistinctV2(t *testing.T) {
	request, _ := executionRequestFixtureV2(t, ExecutionAdmitV2)
	governance, err := ExecutionGovernanceFromContextV2(request.Governance)
	if err != nil {
		t.Fatal(err)
	}
	active, err := decideExecutionTransitionV2(request, governance, nil)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := request
	heartbeat.Kind = ExecutionHeartbeatV2
	heartbeat.OperationID = "operation-heartbeat"
	heartbeat.OperationEventID = "event-operation-heartbeat"
	heartbeat.SlotEventID = "event-slot-heartbeat"
	heartbeat.ObservedAt = request.ObservedAt.Add(30 * time.Second)
	heartbeat.Deadline = request.Deadline.Add(time.Minute)
	renewed, err := decideExecutionTransitionV2(heartbeat, governance, &active)
	if err != nil || renewed.LogicalVersion != 2 {
		t.Fatalf("renew=%+v err=%v", renewed, err)
	}
	stale := heartbeat
	stale.Fence = "0123456789abcdef-stale"
	if _, err := decideExecutionTransitionV2(stale, governance, &active); !IsKind(err, KindStaleAuthority) {
		t.Fatalf("stale heartbeat=%v", err)
	}
	expire := heartbeat
	expire.Kind = ExecutionExpireV2
	expire.ObservedAt = heartbeat.Deadline
	expired, err := decideExecutionTransitionV2(expire, governance, &renewed)
	if err != nil || expired.Status != ExecutionExpiredV2 {
		t.Fatalf("expire=%+v err=%v", expired, err)
	}
	replace := request
	replace.Kind = ExecutionReplaceV2
	replace.OperationID = "operation-replace"
	replace.OperationEventID = "event-operation-replace"
	replace.SlotEventID = "event-slot-replace"
	replace.RunID = "run-2"
	replace.LeaseID = "lease-2"
	replace.ProcessID = "process-2"
	replace.RuntimeEpochID = "epoch-2"
	replace.Generation = 2
	replace.Fence = "fedcba9876543210"
	replace.ObservedAt = expire.ObservedAt.Add(time.Second)
	replace.Deadline = replace.ObservedAt.Add(time.Minute)
	replaced, err := decideExecutionTransitionV2(replace, governance, &expired)
	if err != nil || replaced.Generation != 2 || replaced.LeaseID == expired.LeaseID || replaced.Fence == expired.Fence {
		t.Fatalf("replace=%+v err=%v", replaced, err)
	}
	if _, err := decideExecutionTransitionV2(heartbeat, governance, &replaced); !IsKind(err, KindStaleAuthority) {
		t.Fatalf("predecessor regained authority: %v", err)
	}
}

func TestExecutionAuthorityMintsGrantFenceAndEligibilityOnlyForGrantsV2(t *testing.T) {
	request, eligibility := executionRequestFixtureV2(t, ExecutionAdmitV2)
	port := &executionPortProbeV2{resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}, eligibility: eligibility}
	epochs := &executionEpochProbeV2{epoch: request.RuntimeEpochID}
	service, err := NewExecutionAuthorityServiceWithFenceSourceV2(port, epochs, fixedFenceSourceV2("authority-minted-fence-0001"), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.State.Fence != "authority-minted-fence-0001" || admitted.State.Fence == request.Fence {
		t.Fatalf("caller selected fence %q", admitted.State.Fence)
	}
	heartbeat := request
	heartbeat.Kind = ExecutionHeartbeatV2
	heartbeat.OperationID = "operation-guard-heartbeat"
	heartbeat.OperationEventID = "event-guard-heartbeat-op"
	heartbeat.SlotEventID = "event-guard-heartbeat"
	heartbeat.Fence = admitted.State.Fence
	heartbeat.ObservedAt = request.ObservedAt.Add(10 * time.Second)
	heartbeat.Deadline = request.Deadline.Add(time.Minute)
	port.resolution = ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}
	port.observation = ExecutionSlotObservationV2{State: &admitted.State, RecordRevision: 29}
	port.trace = nil
	if _, err := service.Execute(t.Context(), heartbeat); err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	if len(sealed.EligibilityGuards()) != 0 || len(sealed.Conditions()) != 2 || strings.Join(port.trace, ",") != "resolve,slot,publish" {
		t.Fatalf("heartbeat guard matrix guards=%d conditions=%d trace=%v", len(sealed.EligibilityGuards()), len(sealed.Conditions()), port.trace)
	}
}

func TestExecutionStableIntentBindsPresentedFenceExceptMintedGrantFenceV2(t *testing.T) {
	request, _ := executionRequestFixtureV2(t, ExecutionHeartbeatV2)
	first, _ := prepareExecutionRequestV2(request)
	changed := request
	changed.Fence = "different-presented-fence"
	second, _ := prepareExecutionRequestV2(changed)
	encoded, _ := journalv2.EncodeOperationBindingV2(second.binding)
	if resolution, _ := journalv2.ResolveOperationBinding(first.binding, encoded, true); resolution != journalv2.BindingConflict {
		t.Fatal("heartbeat fence omitted from stable intent")
	}
	request.Kind = ExecutionAdmitV2
	changed = request
	changed.Fence = "caller-cannot-select-other"
	first, _ = prepareExecutionRequestV2(request)
	second, _ = prepareExecutionRequestV2(changed)
	encoded, _ = journalv2.EncodeOperationBindingV2(second.binding)
	if resolution, _ := journalv2.ResolveOperationBinding(first.binding, encoded, true); resolution != journalv2.BindingExact {
		t.Fatal("caller fence polluted minted grant intent")
	}
}

func TestWP07ExecutableExecutionMutantsV2(t *testing.T) {
	request, eligibility := executionRequestFixtureV2(t, ExecutionAdmitV2)
	governance, _ := ExecutionGovernanceFromContextV2(request.Governance)
	active, err := decideExecutionTransitionV2(request, governance, nil)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := request
	heartbeat.Kind = ExecutionHeartbeatV2
	heartbeat.ObservedAt = request.ObservedAt.Add(10 * time.Second)
	heartbeat.Deadline = request.Deadline.Add(time.Minute)
	mutants := []struct {
		name   string
		mutate func(*ExecutionAuthorityRequestV2)
	}{{"M-STALE-LEASE", func(v *ExecutionAuthorityRequestV2) { v.Fence = "stale-fence-0123456789" }}, {"M-PROCESS-ONLY-IDENTITY", func(v *ExecutionAuthorityRequestV2) { v.ActorID = "actor-other" }}, {"M-EXPIRED-HEARTBEAT", func(v *ExecutionAuthorityRequestV2) { v.ObservedAt = request.Deadline }}}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			candidate := heartbeat
			mutant.mutate(&candidate)
			if _, err := decideExecutionTransitionV2(candidate, governance, &active); !IsKind(err, KindStaleAuthority) {
				t.Fatalf("mutant survived: %v", err)
			}
		})
	}
	expire := request
	expire.Kind = ExecutionExpireV2
	expire.ObservedAt = request.Deadline
	expired, err := decideExecutionTransitionV2(expire, governance, &active)
	if err != nil {
		t.Fatal(err)
	}
	replace := request
	replace.Kind, replace.RunID, replace.LeaseID, replace.Generation = ExecutionReplaceV2, "run-replacement", "lease-replacement", 2
	replace.ObservedAt = request.Deadline.Add(-time.Nanosecond)
	replace.Deadline = request.Deadline.Add(time.Minute)
	if _, err := decideExecutionTransitionV2(replace, governance, &expired); !IsKind(err, KindStaleAuthority) {
		t.Fatalf("M-EARLY-REPLACEMENT survived: %v", err)
	}
	seedPort := &executionPortProbeV2{resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationAbsentV2}, eligibility: eligibility}
	seedEpoch := &executionEpochProbeV2{epoch: request.RuntimeEpochID}
	seedService, _ := NewExecutionAuthorityServiceV2(seedPort, seedEpoch, journalv2.DefaultEventRegistryV2())
	if _, err := seedService.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	sealed, _ := seedPort.plans[0].Publication()
	replayPort := &executionPortProbeV2{resolution: ExecutionOperationResolutionV2{Kind: ExecutionOperationExactV2, State: sealed.OperationState()}}
	replayEpoch := &executionEpochProbeV2{epoch: "epoch-mutant-rotated"}
	replayService, _ := NewExecutionAuthorityServiceV2(replayPort, replayEpoch, journalv2.DefaultEventRegistryV2())
	if result, err := replayService.Execute(t.Context(), request); err != nil || !result.Replayed || replayEpoch.calls != 0 || strings.Join(replayPort.trace, ",") != "resolve" {
		t.Fatalf("M-EPOCH-FIRST-REPLAY survived: result=%+v epoch_calls=%d trace=%v err=%v", result, replayEpoch.calls, replayPort.trace, err)
	}
	ids := []string{"M-EPOCH-FIRST-REPLAY", "M-PROCESS-ONLY-IDENTITY", "M-STALE-LEASE"}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if got := fmt.Sprintf("%x", sum); got != "3be8e19ae852612101ac05963fb180af04a036e46b8f501621b6c33874b5b960" {
		t.Fatalf("mutant census count=%d digest=%s", len(ids), got)
	}
}

func executionRequestFixtureV2(t *testing.T, kind ExecutionCommandKindV2) (ExecutionAuthorityRequestV2, workgraph.ObservedEligibilityShardV2) {
	t.Helper()
	revision := executionRevisionFixtureV2(t)
	shards, err := workgraph.BuildInitialEligibilityGuardShards(revision, "operation-workgraph", []workgraph.EligibilityOperation{workgraph.EligibilityStart, workgraph.EligibilityHandoff, workgraph.EligibilityReview, workgraph.EligibilityAcceptance, workgraph.EligibilityIntegration})
	if err != nil {
		t.Fatal(err)
	}
	var observed workgraph.ObservedEligibilityShardV2
	for _, shard := range shards {
		candidate, e := workgraph.NewObservedEligibilityShardV2(mustEligibilityBytesV2(t, shard), 17)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = candidate.Token("work-1", revision.ID(), workgraph.EligibilityStart); e == nil {
			observed = candidate
			break
		}
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	return ExecutionAuthorityRequestV2{Kind: kind, OperationID: "operation-admit", ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), WorkRevisionID: revision.ID(), WorkID: "work-1", RunID: "run-1", ActorID: "actor-1", ProcessID: "process-1", LeaseID: "lease-1", Generation: 1, Fence: "0123456789abcdef", ObservedAt: now, Deadline: now.Add(time.Minute), RuntimeEpochID: "epoch-1", Governance: executionGovernanceContextFixtureV2(t), OperationEventID: "event-operation-admit", SlotEventID: "event-slot-admit"}, observed
}

func mustEligibilityBytesV2(t *testing.T, shard workgraph.EligibilityGuardShardV2) []byte {
	t.Helper()
	value, err := shard.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func executionRevisionFixtureV2(t *testing.T) workgraph.RevisionV2 {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-1", WorkID: "work-1", Title: "Execute work", Description: "Exercise execution authority.", DesignContext: "Use a lease.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-1", Text: "passes", Outcome: "green", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/execution", Domain: "execution", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "scope-1", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-1", "mission-1", "artifact-plan", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-1", Kind: workgraph.ScopePlanConcern, MissionID: "mission-1"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("work-1"))
	final, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-1", Provider: "fake", ExternalID: "external-1", Revision: "revision-1", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", sum), CriterionEvidenceHints: map[string]string{"criterion-1": "go test"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: final, PlanningSourceCommit: strings.Repeat("b", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-1", FinalizedAt: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), GovernanceContextID: "governance-1"})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func executionGovernanceContextFixtureV2(t *testing.T) governancecontext.Context {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind                 governancecontext.EntryKind
		ref, revision, value string
	}{{governancecontext.KindConfig, "config.default", "policy-r1", "config"}, {governancecontext.KindDoctrine, "doctrine.core", "doctrine-r1", "doctrine"}, {governancecontext.KindProfile, "profile.implementer", "profile-r1", "profile"}, {governancecontext.KindMissionType, "mission-type.software", "mission-type-r1", "mission"}} {
		commitment, err := governancecontext.CommitSHA256([]byte(item.value))
		if err != nil {
			t.Fatal(err)
		}
		entry, err := governancecontext.NewEntry(item.kind, item.ref, item.revision, commitment)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	value, err := governancecontext.NewContext("governance-1", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
