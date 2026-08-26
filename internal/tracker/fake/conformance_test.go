package fake

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestFakeCapabilityReportIsHonestAndValid(t *testing.T) {
	t.Parallel()
	provider, _, _ := conformanceProvider(t)
	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := caps.Validate(); err != nil {
		t.Fatal(err)
	}
	if caps.CatalogRead != tracker.SupportSupported || caps.CatalogCreate != tracker.SupportUnsupported || caps.CatalogUpdate != tracker.UpdateUnsupported {
		t.Fatalf("catalog capabilities = %+v", caps)
	}
	if caps.Assignment != tracker.AssignmentAtomicClaim || caps.Discussion != tracker.DiscussionIdempotent || caps.SafeCompensation != tracker.CompensationConditional {
		t.Fatalf("mutation capabilities = %+v", caps)
	}
	if caps.PortableExport != tracker.SupportUnsupported || caps.RestorableBackup != tracker.SupportUnsupported {
		t.Fatalf("test controls promoted to production capabilities: %+v", caps)
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
	if err := provider.RequireCapability(tracker.CapabilityCatalogCreate); !tracker.IsKind(err, tracker.ErrorCapabilityUnsupported) {
		t.Fatalf("unsupported create = %v", err)
	}
	if err := provider.RequireCatalogUpdate(tracker.UpdateGuarded); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) {
		t.Fatalf("unsupported guarded update = %v", err)
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
	request := tracker.CommentRequest{WorkKey: "external-1", ExpectedRevision: 1, Body: "durable observation", Marker: "operation-comment"}
	applied, err := provider.CompareAndAddComment(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := NewProvider(reopened).CompareAndAddComment(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Revision != applied.Revision {
		t.Fatalf("discussion replay = %+v, %v", replayed, err)
	}
	changed := request
	changed.ExpectedRevision = applied.Revision
	changed.Body = "different semantic input"
	if _, err := NewProvider(reopened).CompareAndAddComment(ctx, changed); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("discussion marker reuse = %v", err)
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
	if err := store.Seed(context.Background(), WorkSeed{Key: "external-1", Title: "portable contract", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	return NewProvider(store), store, path
}
