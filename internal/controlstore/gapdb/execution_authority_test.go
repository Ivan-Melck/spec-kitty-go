package gapdb

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type gapdbExecutionPlanProbeV2 struct {
	eligibility workgraph.ObservedEligibilityShardV2
	plan        execution.ExecutionAuthorityPlanV2
}

func (*gapdbExecutionPlanProbeV2) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
}
func (p *gapdbExecutionPlanProbeV2) ObserveExecutionEligibility(context.Context, ids.MissionID, journalv2.EligibilityShardID) (workgraph.ObservedEligibilityShardV2, error) {
	return p.eligibility, nil
}
func (*gapdbExecutionPlanProbeV2) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return execution.ExecutionSlotObservationV2{}, nil
}
func (p *gapdbExecutionPlanProbeV2) PublishExecution(_ context.Context, plan execution.ExecutionAuthorityPlanV2) (execution.ExecutionPublicationAckV2, error) {
	p.plan = plan
	return execution.ExecutionPublicationAckV2{}, fmt.Errorf("capture execution plan")
}

type gapdbFenceSourceV2 struct{}

func (gapdbFenceSourceV2) NewExecutionFence(context.Context) (string, error) {
	return "authority-owned-gapdb-fence-0123456789", nil
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

func gapdbExecutionAdmissionPlanFixtureV2(t *testing.T, suffix, workID string) (execution.ExecutionAuthorityPlanV2, execution.ExecutionSlotStateV2) {
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
	probe := &gapdbExecutionPlanProbeV2{eligibility: eligibility}
	service, err := execution.NewExecutionAuthorityServiceWithFenceSourceV2(probe, gapdbEpochV2{epoch: request.RuntimeEpochID}, gapdbFenceSourceV2{}, journalv2.DefaultEventRegistryV2())
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
