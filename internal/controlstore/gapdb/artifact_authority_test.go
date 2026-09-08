package gapdb

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type recordingAuthorityPortV2 struct {
	calls int
	plan  journalv2.SealedCommitPlanV2
}

func (p *recordingAuthorityPortV2) Publish(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.AcknowledgedPublicationV2, error) {
	p.calls++
	p.plan = plan
	return journalv2.AcknowledgedPublicationV2{}, nil
}

func TestArtifactAuthorityAdmitsOnlyExactOwnedSealedPlan(t *testing.T) {
	plan := gapdbArtifactPlanV2(t)
	sealed := artifactSealedPlanV2(t, plan)
	port := &recordingAuthorityPortV2{}
	authority := newArtifactAuthorityV2ForTest(port)
	_, err := authority.Publish(t.Context(), plan)
	if err != nil || port.calls != 1 || port.plan.Digest() != sealed.Digest() {
		t.Fatalf("artifact plan was not delegated exactly once: calls=%d err=%v", port.calls, err)
	}
	if _, err := authority.Publish(t.Context(), artifact.AuthorityPlanV2{}); !IsKind(err, OutcomeInvalid) || port.calls != 1 {
		t.Fatalf("unminted artifact capability admitted: calls=%d err=%v", port.calls, err)
	}
}
func TestArtifactAuthorityRefusesNil(t *testing.T) {
	if _, err := newArtifactAuthorityV2(nil); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("nil coordinator admitted: %v", err)
	}
	var authority *artifactAuthorityV2
	if _, err := authority.Publish(t.Context(), gapdbArtifactPlanV2(t)); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("nil receiver admitted: %v", err)
	}
}
func TestArtifactAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB(t *testing.T) {
	plan := gapdbArtifactPlanV2(t)
	assertGapDBAuthorityPublication(t, artifactSealedPlanV2(t, plan), func(c *journalv2.PublicationCoordinatorV2) (journalv2.AcknowledgedPublicationV2, error) {
		a, _ := newArtifactAuthorityV2(c)
		return a.Publish(t.Context(), plan)
	})
}

func TestArtifactAuthorityCatalogCASAllowsOneWinnerAndIndependentProgress(t *testing.T) {
	shared := newV2AtomicMemory()
	authority := openArtifactAuthorityTestV2(t, shared)
	plans := []artifact.AuthorityPlanV2{
		gapdbArtifactPlanForV2(t, "shared-a", "mission-shared", "artifact-shared-a"),
		gapdbArtifactPlanForV2(t, "shared-b", "mission-shared", "artifact-shared-b"),
	}
	results := publishAuthorityPlansConcurrentlyV2(t, authority, plans)
	succeeded := 0
	for _, err := range results {
		if err == nil {
			succeeded++
		} else if !IsKind(err, OutcomeConflict) {
			t.Fatalf("same catalog contention returned unstable outcome: %v", err)
		}
	}
	if succeeded != 1 || len(shared.records) != artifactSealedPlanV2(t, plans[0]).MutationCount() {
		t.Fatalf("catalog CAS exposed partial or multiple authority: success=%d records=%d", succeeded, len(shared.records))
	}

	independent := newV2AtomicMemory()
	independentAuthority := openArtifactAuthorityTestV2(t, independent)
	independentPlans := []artifact.AuthorityPlanV2{
		gapdbArtifactPlanForV2(t, "independent-a", "mission-independent-a", "artifact-independent-a"),
		gapdbArtifactPlanForV2(t, "independent-b", "mission-independent-b", "artifact-independent-b"),
	}
	for _, err := range publishAuthorityPlansConcurrentlyV2(t, independentAuthority, independentPlans) {
		if err != nil {
			t.Fatalf("independent catalog was globally serialized: %v", err)
		}
	}
}

func TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing(t *testing.T) {
	artifactPlan := gapdbArtifactPlanV2(t)
	workgraphPlan := gapdbWorkgraphPlanV2(t)
	tests := []struct {
		name    string
		plan    journalv2.SealedCommitPlanV2
		publish func(*journalv2.PublicationCoordinatorV2) (journalv2.AcknowledgedPublicationV2, error)
	}{
		{"artifact", artifactSealedPlanV2(t, artifactPlan), func(coordinator *journalv2.PublicationCoordinatorV2) (journalv2.AcknowledgedPublicationV2, error) {
			authority, _ := newArtifactAuthorityV2(coordinator)
			return authority.Publish(t.Context(), artifactPlan)
		}},
		{"workgraph", workgraphSealedPlanV2(t, workgraphPlan), func(coordinator *journalv2.PublicationCoordinatorV2) (journalv2.AcknowledgedPublicationV2, error) {
			authority, _ := newWorkgraphAuthorityV2(coordinator)
			return authority.Publish(t.Context(), workgraphPlan)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			memory := newV2AtomicMemory()
			provider := memory.provider()
			commit := provider.batchFn
			provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
				if _, err := commit(ctx, batch); err != nil {
					return gapdbapi.MutationResult{}, err
				}
				return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
			}
			provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
				memory.mu.Lock()
				defer memory.mu.Unlock()
				return gapdbapi.StatusResult{DatabaseID: "authority-test", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
			}
			adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			tracker, err := gapdbAuthorityProjectionTrackerV2()
			if err != nil {
				t.Fatal(err)
			}
			coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := test.publish(coordinator); !IsKind(err, OutcomeAmbiguous) || coordinator.UnresolvedCount() != 1 {
				t.Fatalf("lost response was not retained for exact recovery: unresolved=%d err=%v", coordinator.UnresolvedCount(), err)
			}
			observation, err := adapter.ReconcileCommitV2(t.Context(), test.plan)
			if err != nil {
				t.Fatal(err)
			}
			ack, err := coordinator.ReconcileComplete(test.plan, observation)
			if err != nil || ack.PublicationID() != test.plan.PublicationID() || ack.MutationCount() != test.plan.MutationCount() || coordinator.UnresolvedCount() != 0 || memory.batchCalls != 1 {
				t.Fatalf("exact recovery failed or republished: ack=%+v unresolved=%d batches=%d err=%v", ack, coordinator.UnresolvedCount(), memory.batchCalls, err)
			}
		})
	}
}

func assertGapDBAuthorityPublication(t *testing.T, plan journalv2.SealedCommitPlanV2, publish func(*journalv2.PublicationCoordinatorV2) (journalv2.AcknowledgedPublicationV2, error)) {
	t.Helper()
	memory := newV2AtomicMemory()
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := gapdbAuthorityProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := publish(coordinator)
	if err != nil || ack.MutationCount() != plan.MutationCount() || memory.batchCalls != 1 {
		t.Fatalf("complete batch not acknowledged: ack=%+v batches=%d err=%v", ack, memory.batchCalls, err)
	}
	for _, record := range plan.Records() {
		key, err := v2KeyForSealedRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := memory.records[key.String()]; !ok {
			t.Fatalf("atomic publication omitted %s", record.Role())
		}
	}
}

func gapdbAuthorityProjectionTrackerV2() (*journalv2.ProjectionTrackerV2, error) {
	registry := journalv2.DefaultEventRegistryV2()
	owned := append(artifact.ReducerRegistrationsV2(), workgraph.ReducerRegistrationsV2()...)
	byFamily := make(map[string]journalv2.ReducerRegistration, len(owned))
	for _, r := range owned {
		byFamily[r.Family] = r
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "operation" {
			registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2})
			continue
		}
		if r, ok := byFamily[tuple.Family]; ok {
			registrations = append(registrations, r)
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

func gapdbArtifactPlanV2(t *testing.T) artifact.AuthorityPlanV2 {
	t.Helper()
	return gapdbArtifactPlanForV2(t, "gapdb", "mission-gapdb", "artifact-gapdb")
}

func gapdbArtifactPlanForV2(t *testing.T, label, missionID, revisionID string) artifact.AuthorityPlanV2 {
	t.Helper()
	content, err := artifact.CommitContentV2([]byte("GapDB adapter artifact content"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{ID: revisionID, MissionID: missionID, Kind: artifact.KindPlan, RevisionNumber: 1, ContentDigest: content.Digest(), SourceRef: "kitty-specs/plan.md", SourceRevision: "commit-gapdb", CreatedBy: "actor-gapdb", CreatedAt: time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := artifact.PolicyFor(artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: revision.ID(), Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: time.Date(2026, 9, 5, 1, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := artifact.BuildArtifactPublishAuthorityV2(artifact.PublishPlanInputV2{Context: artifact.AuthorityContextV2{ProjectID: "project-gapdb", OperationID: journalv2.OperationID("operation-artifact-" + label), RuntimeEpochID: "epoch-gapdb", ActorID: "actor-gapdb", ProcessID: "process-gapdb", DoctrineRevision: "doctrine-gapdb", ProfileRevision: "profile-gapdb", PolicyRevision: "policy-gapdb", Governance: []string{"governance-gapdb"}, ObservedCreatedAt: "2026-09-05T01:00:00Z", OperationEventID: journalv2.EventID("event-operation-artifact-" + label), CatalogEventID: journalv2.EventID("event-catalog-" + label), RevisionEventID: journalv2.EventID("event-revision-" + label)}, Revision: revision, Validation: validation, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func openArtifactAuthorityTestV2(t *testing.T, memory *v2AtomicMemory) *artifactAuthorityV2 {
	t.Helper()
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := gapdbAuthorityProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newArtifactAuthorityV2(coordinator)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func publishAuthorityPlansConcurrentlyV2(t *testing.T, authority *artifactAuthorityV2, plans []artifact.AuthorityPlanV2) []error {
	t.Helper()
	start := make(chan struct{})
	results := make([]error, len(plans))
	var workers sync.WaitGroup
	workers.Add(len(plans))
	for index := range plans {
		go func(index int) {
			defer workers.Done()
			<-start
			_, results[index] = authority.Publish(t.Context(), plans[index])
		}(index)
	}
	close(start)
	workers.Wait()
	return results
}

func artifactSealedPlanV2(t *testing.T, plan artifact.AuthorityPlanV2) journalv2.SealedCommitPlanV2 {
	t.Helper()
	sealed, err := plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}
