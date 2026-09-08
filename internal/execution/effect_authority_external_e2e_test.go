package execution_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type externalRuntimeEpochV2 struct{ id journalv2.RuntimeEpochID }

func (e externalRuntimeEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return e.id, nil
}

type externalFenceSourceV2 struct{}

func (externalFenceSourceV2) NewExecutionFence(context.Context) (string, error) {
	return "external-fence-0123456789abcdef", nil
}

type externalDurableJournalV2 struct{ revision uint64 }

func (p *externalDurableJournalV2) Commit(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	p.revision++
	return journalv2.NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), p.revision, p.revision, plan.MutationCount())
}

// externalAuthorityPortV2 is a public-contract-only composition of the real
// publication coordinator and reducers. It deliberately uses no execution
// package internals, so this test detects an unusable exported service seam.
type externalAuthorityPortV2 struct {
	coordinator       *journalv2.PublicationCoordinatorV2
	eligibility       workgraph.ObservedEligibilityShardV2
	operations        map[journalv2.OperationID]journalv2.OperationStateV2
	operationRevision map[journalv2.OperationID]uint64
	execution         *execution.ExecutionSlotStateV2
	executionRevision uint64
	executionPub      journalv2.PublicationID
	effect            *execution.ExternalEffectStateV2
	effectRevision    uint64
	effectPub         journalv2.PublicationID
}

func (p *externalAuthorityPortV2) ResolveExecutionOperation(_ context.Context, binding journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	state, ok := p.operations[binding.OperationID()]
	if !ok {
		return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
	}
	return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationExactV2, State: state, RecordRevision: p.operationRevision[binding.OperationID()]}, nil
}

func (p *externalAuthorityPortV2) ResolveEffectOperation(_ context.Context, binding journalv2.OperationBindingV2) (execution.EffectOperationResolutionV2, error) {
	state, ok := p.operations[binding.OperationID()]
	if !ok {
		return execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
	}
	return execution.EffectOperationResolutionV2{Kind: execution.ExecutionOperationExactV2, State: state, RecordRevision: p.operationRevision[binding.OperationID()]}, nil
}

func (p *externalAuthorityPortV2) ObserveExecutionEligibility(context.Context, ids.MissionID, journalv2.EligibilityShardID) (workgraph.ObservedEligibilityShardV2, error) {
	return p.eligibility, nil
}

func (p *externalAuthorityPortV2) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return execution.ExecutionSlotObservationV2{State: p.execution, RecordRevision: p.executionRevision, LastPublicationID: p.executionPub}, nil
}

func (p *externalAuthorityPortV2) ObserveEffectExecution(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return execution.ExecutionSlotObservationV2{State: p.execution, RecordRevision: p.executionRevision, LastPublicationID: p.executionPub}, nil
}

func (p *externalAuthorityPortV2) ObserveEffectState(context.Context, journalv2.AggregateCoordinate) (execution.EffectStateObservationV2, error) {
	return execution.EffectStateObservationV2{State: p.effect, RecordRevision: p.effectRevision, LastPublicationID: p.effectPub}, nil
}

func (p *externalAuthorityPortV2) PublishExecution(ctx context.Context, capability execution.ExecutionAuthorityPlanV2) (execution.ExecutionPublicationAckV2, error) {
	plan, err := capability.Publication()
	if err != nil {
		return execution.ExecutionPublicationAckV2{}, err
	}
	ack, err := p.coordinator.Publish(ctx, plan)
	if err != nil {
		return execution.ExecutionPublicationAckV2{}, err
	}
	p.capture(plan, ack.ProviderRevision())
	return execution.NewExecutionPublicationAckV2(capability, ack)
}

func (p *externalAuthorityPortV2) PublishEffect(ctx context.Context, capability execution.EffectAuthorityPlanV2) (execution.EffectPublicationAckV2, error) {
	plan, err := capability.Publication()
	if err != nil {
		return execution.EffectPublicationAckV2{}, err
	}
	ack, err := p.coordinator.Publish(ctx, plan)
	if err != nil {
		return execution.EffectPublicationAckV2{}, err
	}
	p.capture(plan, ack.ProviderRevision())
	return execution.NewEffectPublicationAckV2(capability, ack)
}

func (p *externalAuthorityPortV2) capture(plan journalv2.SealedCommitPlanV2, revision uint64) {
	operation := plan.OperationState()
	p.operations[operation.OperationID()] = operation
	p.operationRevision[operation.OperationID()] = revision
	for _, head := range plan.Heads() {
		switch head.Coordinate.Kind {
		case "execution_slot":
			state, err := execution.DecodeExecutionSlotStateV2(head.AuthoritySummary)
			if err == nil {
				p.execution = &state
				p.executionRevision = revision
				p.executionPub = plan.PublicationID()
			}
		case "external_effect":
			state, err := execution.DecodeExternalEffectStateV2(head.AuthoritySummary)
			if err == nil {
				p.effect = &state
				p.effectRevision = revision
				p.effectPub = plan.PublicationID()
			}
		}
	}
}

func TestExternalProviderRunsEndToEndThroughPublicServicesAndRealFakeTrackerV2(t *testing.T) {
	request, eligibility := externalExecutionRequestV2(t)
	port := newExternalAuthorityPortV2(t, eligibility)
	epoch := externalRuntimeEpochV2{request.RuntimeEpochID}
	executionService, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(port, epoch, externalFenceSourceV2{}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := executionService.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	store, err := trackerfake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	target := tracker.ExternalWorkRef{ProjectID: admitted.State.ProjectID, WorkID: admitted.State.WorkID, Provider: "fake", ExternalID: "ticket-black-box", MetadataSchema: 1, CreatedByOperation: "operation-create-black-box", LastObservation: 1}
	if err := store.Seed(t.Context(), trackerfake.WorkSeed{Key: target.ExternalID, Title: "black-box target"}); err != nil {
		t.Fatal(err)
	}
	intent := execution.EffectIntentV2{EffectID: "effect-black-box", OperationID: "operation-effect-black-box", Target: target, IntendedValue: "actor-black-box", ExpectedProviderRevision: 1, RequiredGuarantee: tracker.AssignmentAtomicClaim, ActorID: admitted.State.ActorID, ProcessID: admitted.State.ProcessID, Governance: admitted.State.Governance, OriginRuntimeEpochID: admitted.State.RuntimeEpochID, OriginObservedCreatedAt: request.ObservedAt.Add(time.Second).Format(time.RFC3339Nano)}
	intent.CanonicalRequestDigest, err = execution.EffectRequestDigestV2(intent)
	if err != nil {
		t.Fatal(err)
	}
	effectService, err := execution.NewEffectAuthorityServiceV2(port, epoch, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	effectRequest := execution.EffectAuthorityRequestV2{Action: execution.EffectRecordIntentV2, Intent: intent, RuntimeEpochID: admitted.State.RuntimeEpochID, ExecutionSlot: admitted.State, ObservedAt: request.ObservedAt.Add(time.Second), OperationEventID: "event-black-box-intent-operation", EffectEventID: "event-black-box-intent-effect"}
	recorded, err := effectService.Execute(t.Context(), effectRequest)
	if err != nil {
		t.Fatal(err)
	}
	effectRequest.Action = execution.EffectStartAttemptV2
	effectRequest.ExecutionSlot = recorded.ExecutionSlot
	effectRequest.OperationEventID = "event-black-box-attempt-operation"
	effectRequest.EffectEventID = "event-black-box-attempt-effect"
	dispatcher := externalEffectDispatcherV2{projection: trackerfake.NewProvider(store)}
	result, err := effectService.ExecuteDispatchV2(t.Context(), execution.EffectDispatchCommandV2{Authorization: effectRequest, ObservationOperationEventID: "event-black-box-observed-operation", ObservationEffectEventID: "event-black-box-observed-effect", ObservationAt: request.ObservedAt.Add(2 * time.Second)}, dispatcher)
	if err != nil || result.State.AppliedObservation == nil || result.State.AppliedObservation.Value != intent.IntendedValue || result.State.AppliedObservation.OperationMarker != string(intent.OperationID) || result.State.AppliedObservation.CanonicalDigest == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	observed, err := dispatcher.ObserveEffect(t.Context(), target)
	if err != nil || observed.Value != intent.IntendedValue || observed.OperationMarker != string(intent.OperationID) || observed.ProviderRevision != result.State.AppliedObservation.ProviderRevision {
		t.Fatalf("tracker observation=%+v err=%v", observed, err)
	}
}

func newExternalAuthorityPortV2(t *testing.T, eligibility workgraph.ObservedEligibilityShardV2) *externalAuthorityPortV2 {
	t.Helper()
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
		sum := sha256.Sum256([]byte("external-test-unused-reducer/" + tuple.Family))
		digest, err := journalv2.ParseDigest("external_test_reducer", fmt.Sprintf("%x", sum))
		if err != nil {
			t.Fatal(err)
		}
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	durable := &externalDurableJournalV2{revision: 40}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(durable, projection)
	if err != nil {
		t.Fatal(err)
	}
	return &externalAuthorityPortV2{coordinator: coordinator, eligibility: eligibility, operations: make(map[journalv2.OperationID]journalv2.OperationStateV2), operationRevision: make(map[journalv2.OperationID]uint64)}
}

func externalExecutionRequestV2(t *testing.T) (execution.ExecutionAuthorityRequestV2, workgraph.ObservedEligibilityShardV2) {
	t.Helper()
	revision := externalRevisionV2(t)
	shards, err := workgraph.BuildInitialEligibilityGuardShards(revision, "operation-workgraph-black-box", []workgraph.EligibilityOperation{workgraph.EligibilityStart, workgraph.EligibilityHandoff, workgraph.EligibilityReview, workgraph.EligibilityAcceptance, workgraph.EligibilityIntegration})
	if err != nil {
		t.Fatal(err)
	}
	var observed workgraph.ObservedEligibilityShardV2
	for _, shard := range shards {
		canonical, encodeErr := shard.CanonicalBytes()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		candidate, observeErr := workgraph.NewObservedEligibilityShardV2(canonical, 17)
		if observeErr == nil {
			if _, tokenErr := candidate.Token("work-black-box", revision.ID(), workgraph.EligibilityStart); tokenErr == nil {
				observed = candidate
				break
			}
		}
	}
	if observed.RecordRevision() == 0 {
		t.Fatal("eligible work shard was not found")
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	return execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionAdmitV2, OperationID: "operation-admit-black-box", ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), WorkRevisionID: revision.ID(), WorkID: "work-black-box", RunID: "run-black-box", ActorID: "actor-black-box", ProcessID: "process-black-box", LeaseID: "lease-black-box", Generation: 1, ObservedAt: now, Deadline: now.Add(time.Minute), RuntimeEpochID: "epoch-black-box", Governance: externalGovernanceContextV2(t), OperationEventID: "event-admit-black-box-operation", SlotEventID: "event-admit-black-box-slot"}, observed
}

func externalRevisionV2(t *testing.T) workgraph.RevisionV2 {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-black-box", WorkID: "work-black-box", Title: "Black-box execution", Description: "Exercise public execution and effect services.", DesignContext: "External provider portability.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-black-box", Text: "passes", Outcome: "green", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/execution", Domain: "execution", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "scope-black-box", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-black-box", "mission-black-box", "artifact-plan-black-box", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-black-box", Kind: workgraph.ScopePlanConcern, MissionID: "mission-black-box"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("work-black-box"))
	final, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-black-box", Provider: "fake", ExternalID: "ticket-black-box", Revision: "revision-1", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", sum), CriterionEvidenceHints: map[string]string{"criterion-black-box": "go test"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: final, PlanningSourceCommit: strings.Repeat("b", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-black-box", FinalizedAt: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), GovernanceContextID: "governance-black-box"})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func externalGovernanceContextV2(t *testing.T) governancecontext.Context {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind                 governancecontext.EntryKind
		ref, revision, value string
	}{{governancecontext.KindConfig, "config.black-box", "policy-r1", "config"}, {governancecontext.KindDoctrine, "doctrine.black-box", "doctrine-r1", "doctrine"}, {governancecontext.KindProfile, "profile.black-box", "profile-r1", "profile"}, {governancecontext.KindMissionType, "mission-type.black-box", "mission-r1", "mission"}} {
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
	value, err := governancecontext.NewContext("governance-black-box", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
