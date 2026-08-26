package fake

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestFakeCapabilityReportIsHonestAndValid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		capability tracker.CapabilityName
		exercise   func(context.Context, *Provider, tracker.ExternalWorkRef) error
	}{
		{tracker.CapabilityCatalogRead, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			record, err := tracker.CatalogReader(provider).ReadWork(ctx, reference)
			if err == nil && (len(record.Criteria()) != 1 || record.ExternalRef.WorkID != reference.WorkID) {
				t.Fatalf("catalog record = %+v criteria=%+v", record, record.Criteria())
			}
			return err
		}},
		{tracker.CapabilityCatalogCreate, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			_, err := tracker.CatalogCreator(provider).CreateWork(ctx, tracker.CreateWorkRequest{ProjectID: reference.ProjectID, WorkID: "work-new", OperationID: "operation-create", Title: "new work", MetadataSchema: 1})
			return err
		}},
		{tracker.CapabilityCatalogUpdate, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			_, err := tracker.CatalogUpdater(provider).UpdateWork(ctx, tracker.UpdateWorkRequest{ExternalRef: reference, OperationID: "operation-update", RequiredGuard: tracker.UpdateUnguarded})
			return err
		}},
		{tracker.CapabilityBlockingGraph, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			_, err := tracker.Graph(provider).ListDependencies(ctx, reference)
			return err
		}},
		{tracker.CapabilityDiscussion, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			port := tracker.Discussion(provider)
			appended, err := port.AppendDiscussion(ctx, tracker.AppendDiscussionRequest{ExternalRef: reference, OperationID: "operation-discussion", Author: "agent-a", Body: "durable note", ExpectedObservation: 1})
			if err != nil {
				return err
			}
			reopened, err := Open(provider.store.path)
			if err != nil {
				return err
			}
			entries, err := tracker.Discussion(NewProvider(reopened)).ListDiscussion(ctx, reference)
			if err == nil && (len(entries) != 1 || entries[0].Author != appended.Entry.Author || entries[0].Body != appended.Entry.Body || entries[0].OperationMarker != appended.Entry.OperationMarker || entries[0].Observation != appended.Entry.Observation) {
				t.Fatalf("persisted discussion entries = %+v, appended = %+v", entries, appended.Entry)
			}
			return err
		}},
		{tracker.CapabilityAssignment, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			port := tracker.AssignmentProjection(provider)
			if _, err := port.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: reference, OperationID: "operation-assignment", Assignee: "agent-a", ExpectedObservation: 1, RequiredGuarantee: tracker.AssignmentAtomicClaim}); err != nil {
				return err
			}
			observed, err := port.ObserveAssignment(ctx, reference)
			if err == nil && observed.Assignee != "agent-a" {
				t.Fatalf("assignment observation = %+v", observed)
			}
			return err
		}},
		{tracker.CapabilityObservationRevision, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			record, err := tracker.CatalogReader(provider).ReadWork(ctx, reference)
			if err == nil && (record.Observation.Revision == 0 || record.Observation.ProviderToken == "") {
				t.Fatalf("observation identity = %+v", record.Observation)
			}
			return err
		}},
		{tracker.CapabilitySafeCompensation, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			port := tracker.AssignmentProjection(provider)
			assigned, err := port.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: reference, OperationID: "operation-compensate", Assignee: "agent-a", ExpectedObservation: 1, RequiredGuarantee: tracker.AssignmentAtomicClaim})
			if err != nil {
				return err
			}
			_, err = port.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{ExternalRef: reference, OperationID: "operation-recovery", ExpectedObservation: assigned.Observation.Observation.Revision, ExpectedAssignee: "agent-a", ExpectedMarker: "operation-compensate", RestoreAssignee: "original"})
			return err
		}},
		{tracker.CapabilityConcurrentWriters, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			inspection, err := tracker.Inspector(provider).Inspect(ctx, tracker.InspectRequest{ProjectID: reference.ProjectID})
			if err == nil && (inspection.Binding.WorkspaceIdentity == "" || inspection.Binding.ConcurrencyMode != tracker.ConcurrencyServerMultiwriter || inspection.Health != tracker.HealthHealthy || inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersProviderNative || len(inspection.Diagnostics()) != 0) {
				t.Fatalf("inspection binding = %+v", inspection)
			}
			return err
		}},
		{tracker.CapabilityPortableExport, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			_, err := tracker.Exporter(provider).ExportPortable(ctx, tracker.ExportRequest{ProjectID: reference.ProjectID}, io.Discard)
			return err
		}},
		{tracker.CapabilityRestorableBackup, func(ctx context.Context, provider *Provider, reference tracker.ExternalWorkRef) error {
			_, err := tracker.Exporter(provider).Backup(ctx, tracker.BackupRequest{ProjectID: reference.ProjectID, DestinationPath: "/backup"})
			return err
		}},
	}
	if len(tests) != 11 {
		t.Fatalf("capability conformance table has %d entries, want complete 11-field report", len(tests))
	}
	seen := make(map[tracker.CapabilityName]struct{}, len(tests))
	for _, test := range tests {
		test := test
		if _, duplicate := seen[test.capability]; duplicate {
			t.Fatalf("capability conformance table repeats %s", test.capability)
		}
		seen[test.capability] = struct{}{}
		t.Run(test.capability.String(), func(t *testing.T) {
			provider, _, _ := conformanceProvider(t)
			caps, err := provider.Capabilities(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := caps.Validate(); err != nil {
				t.Fatal(err)
			}
			declaredErr := caps.Require(test.capability)
			declaredSupported := declaredErr == nil
			if !declaredSupported && !tracker.IsKind(declaredErr, tracker.ErrorCapabilityUnsupported) {
				t.Fatalf("declared support mismatch: %v", declaredErr)
			}
			exerciseErr := test.exercise(ctx, provider, portableReference())
			if declaredSupported && exerciseErr != nil {
				t.Fatalf("supported operation failed: %v", exerciseErr)
			}
			if !declaredSupported && !tracker.IsKind(exerciseErr, tracker.ErrorCapabilityUnsupported) {
				t.Fatalf("unsupported operation = %v", exerciseErr)
			}
		})
	}
}

func TestFakeUnsupportedCapabilityRefusesBeforeAnyStoreEffect(t *testing.T) {
	t.Parallel()
	provider, store, path := conformanceProvider(t)
	ctx := context.Background()
	if err := store.Inject(ctx, FailureUnavailable); err != nil {
		t.Fatal(err)
	}
	before := snapshotStore(t, store, path)
	reference := portableReference()
	if _, err := tracker.CatalogCreator(provider).CreateWork(ctx, tracker.CreateWorkRequest{ProjectID: reference.ProjectID, WorkID: "work-new", OperationID: "operation-create", Title: "new work", MetadataSchema: 1}); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported create = %v", err)
	}
	if _, err := tracker.CatalogUpdater(provider).UpdateWork(ctx, tracker.UpdateWorkRequest{ExternalRef: reference, OperationID: "operation-update", RequiredGuard: tracker.UpdateGuarded}); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) {
		t.Fatalf("unsupported guarded update = %v", err)
	}
	if _, err := tracker.Graph(provider).ListDependencies(ctx, reference); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported graph = %v", err)
	}
	edge := tracker.DependencyRequest{ProjectID: reference.ProjectID, OperationID: "operation-edge", Dependency: tracker.DependencyRef{BlockedWorkID: reference.WorkID, BlockingWorkID: "work-0", Kind: tracker.DependencyBlocks}}
	if _, err := tracker.Graph(provider).PutBlockingDependency(ctx, edge); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported graph add = %v", err)
	}
	if _, err := tracker.Graph(provider).RemoveBlockingDependency(ctx, edge); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported graph remove = %v", err)
	}
	if _, err := tracker.Graph(provider).ReadyProjection(ctx, tracker.ReadyRequest{ProjectID: reference.ProjectID}); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported readiness = %v", err)
	}
	if _, err := tracker.Exporter(provider).ExportPortable(ctx, tracker.ExportRequest{ProjectID: reference.ProjectID}, io.Discard); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported export = %v", err)
	}
	if _, err := tracker.Exporter(provider).Backup(ctx, tracker.BackupRequest{ProjectID: reference.ProjectID, DestinationPath: "/backup"}); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported backup = %v", err)
	}
	if _, err := tracker.Exporter(provider).InspectBackup(ctx, tracker.BackupInspectRequest{ProjectID: reference.ProjectID, BackupID: "backup-1", Path: "/backup"}); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported backup inspection = %v", err)
	}
	after := snapshotStore(t, store, path)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("unsupported request mutated store:\nbefore=%+v\nafter=%+v", before, after)
	}
	work, err := provider.Read(ctx, "external-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: work.Key, ExpectedRevision: work.Revision, Assignee: "agent-a", Marker: "operation-after-refusal"})
	if !tracker.IsKind(err, tracker.ErrorUnavailable) {
		t.Fatalf("queued failure was consumed by refusal: %v", err)
	}
}

func TestFakeStaleObservationAndMarkerReuseCannotAuthorizeMutation(t *testing.T) {
	t.Parallel()
	provider, _, _ := conformanceProvider(t)
	ctx := context.Background()
	if _, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "external-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.CompareAndAddComment(ctx, tracker.CommentRequest{WorkKey: "external-1", ExpectedRevision: 1, Body: "stale", Marker: "operation-stale"}); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("stale observation = %v", err)
	}
	if _, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "external-1", ExpectedRevision: 2, Assignee: "agent-b", Marker: "operation-a"}); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("marker reuse = %v", err)
	}
}

func TestFakeAmbiguousApplyIsResolvedByObservationAcrossReopen(t *testing.T) {
	t.Parallel()
	provider, store, path := conformanceProvider(t)
	ctx := context.Background()
	if err := store.Inject(ctx, FailureTimeoutAfterApply); err != nil {
		t.Fatal(err)
	}
	request := tracker.AssignmentRequest{WorkKey: "external-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-ambiguous"}
	if mutation, err := provider.CompareAndSetAssignment(ctx, request); !tracker.IsKind(err, tracker.ErrorAmbiguous) || mutation.Revision == 0 {
		t.Fatalf("ambiguous result = %+v, %v", mutation, err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := NewProvider(reopened).Read(ctx, request.WorkKey)
	if err != nil || observed.Assignee != request.Assignee || observed.AssignmentMarker != request.Marker {
		t.Fatalf("post-observation = %+v, %v", observed, err)
	}
	replayed, err := NewProvider(reopened).CompareAndSetAssignment(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Revision != observed.Revision {
		t.Fatalf("observed replay = %+v, %v", replayed, err)
	}
}

func TestFakeDiscussionReplayIsCoherentAcrossReopen(t *testing.T) {
	t.Parallel()
	provider, _, path := conformanceProvider(t)
	ctx := context.Background()
	reference := portableReference()
	request := tracker.AppendDiscussionRequest{
		ExternalRef: reference, OperationID: "operation-comment", Author: "agent-a",
		Body: "durable observation", ExpectedObservation: 1,
	}
	applied, err := tracker.Discussion(provider).AppendDiscussion(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := tracker.Discussion(NewProvider(reopened)).AppendDiscussion(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Entry != applied.Entry || replayed.Observation.Revision != applied.Observation.Revision {
		t.Fatalf("discussion replay = %+v, %v", replayed, err)
	}
	changed := request
	changed.ExpectedObservation = applied.Observation.Revision
	changed.Body = "different semantic input"
	if _, err := tracker.Discussion(NewProvider(reopened)).AppendDiscussion(ctx, changed); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("discussion marker reuse = %v", err)
	}
}

func TestFakeDiscussionRejectsAuthorChangeWithoutMutation(t *testing.T) {
	t.Parallel()
	provider, store, path := conformanceProvider(t)
	ctx := context.Background()
	reference := portableReference()
	request := tracker.AppendDiscussionRequest{
		ExternalRef: reference, OperationID: "operation-comment-author", Author: "agent-a",
		Body: "durable observation", ExpectedObservation: 1,
	}
	applied, err := tracker.Discussion(provider).AppendDiscussion(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Inject(ctx, FailureUnavailable); err != nil {
		t.Fatal(err)
	}
	before := snapshotStore(t, store, path)

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	request.Author = "agent-b"
	request.ExpectedObservation = applied.Observation.Revision
	if _, err := tracker.Discussion(NewProvider(reopened)).AppendDiscussion(ctx, request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("same marker and body with different author = %v", err)
	}
	after := snapshotStore(t, reopened, path)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("author-conflict request mutated store:\nbefore=%+v\nafter=%+v", before, after)
	}
	entries, err := tracker.Discussion(NewProvider(reopened)).ListDiscussion(ctx, reference)
	if err != nil || len(entries) != 1 || entries[0].Author != "agent-a" {
		t.Fatalf("persisted author changed: entries=%+v error=%v", entries, err)
	}
}

func TestFakeSafeCompensationRequiresRevisionValueAndMarker(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*tracker.CompensationRequest){
		func(request *tracker.CompensationRequest) { request.ExpectedRevision++ },
		func(request *tracker.CompensationRequest) { request.ExpectedAssignee = "different-agent" },
		func(request *tracker.CompensationRequest) { request.Marker = "different-operation" },
	} {
		mutate := mutate
		t.Run("guard", func(t *testing.T) {
			provider, _, _ := conformanceProvider(t)
			ctx := context.Background()
			assigned, err := provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{WorkKey: "external-1", ExpectedRevision: 1, Assignee: "agent-a", Marker: "operation-a"})
			if err != nil {
				t.Fatal(err)
			}
			request := tracker.CompensationRequest{WorkKey: "external-1", ExpectedRevision: assigned.Revision, ExpectedAssignee: "agent-a", Marker: "operation-a", RestoreAssignee: "original"}
			mutate(&request)
			if _, err := provider.CompareBeforeCompensate(ctx, request); !tracker.IsKind(err, tracker.ErrorAttributionLost) {
				t.Fatalf("unguarded compensation = %v", err)
			}
			observed, err := provider.Read(ctx, "external-1")
			if err != nil || observed.Assignee != "agent-a" || observed.Revision != assigned.Revision {
				t.Fatalf("guard failure changed state: %+v, %v", observed, err)
			}
		})
	}
}

type conformanceSnapshot struct {
	File       string
	Work       diskWork
	Failures   []FailureMode
	History    []HistoryEntry
	Compensate map[string]tracker.Revision
}

func snapshotStore(t *testing.T, store *Store, path string) conformanceSnapshot {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result conformanceSnapshot
	result.File = string(bytes)
	if err := store.read(context.Background(), func(state diskState) error {
		result.Work = *state.Works["external-1"]
		result.Work.Criteria = clone(result.Work.Criteria)
		result.Work.Dependencies = clone(result.Work.Dependencies)
		result.Work.Comments = append([]tracker.Comment(nil), result.Work.Comments...)
		result.Failures = append([]FailureMode(nil), state.Failures...)
		result.History = append([]HistoryEntry(nil), state.History...)
		result.Compensate = make(map[string]tracker.Revision, len(state.Compensated))
		for key, revision := range state.Compensated {
			result.Compensate[key] = revision
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func conformanceProvider(t *testing.T) (*Provider, *Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tracker.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(context.Background(), WorkSeed{Key: "external-1", Title: "portable contract", Criteria: []string{"observable result"}, Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	return NewProvider(store), store, path
}

func portableReference() tracker.ExternalWorkRef {
	return tracker.ExternalWorkRef{ProjectID: "project-1", WorkID: "work-1", Provider: "durable_fake", ExternalID: "external-1", MetadataSchema: 1}
}
