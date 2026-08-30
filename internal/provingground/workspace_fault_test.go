package provingground

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type cutHook struct {
	mu        sync.Mutex
	id        string
	remaining int
}

func (h *cutHook) Reach(_ context.Context, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == h.id && h.remaining > 0 {
		h.remaining--
		return errors.New("injected " + id)
	}
	return nil
}

func TestGJ03OrphanHasNoAuthorityAndCannotBePromoted(t *testing.T) {
	git := newFakeGit(t)
	journal := newJournalFixture()
	service := mustService(t, journal, git).WithFaults(&cutHook{id: "GJ-03", remaining: 1})
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "gj03", now))
	request := workspaceRequest(authority, "gj03-w1", now.Add(time.Second))
	if _, err := service.EnsureWorkspace(t.Context(), request); !IsOutcome(err, OutcomeAmbiguous) {
		t.Fatalf("GJ-03 result=%v", err)
	}
	if _, durable := service.Binding(request.WorkspaceID); durable {
		t.Fatal("GJ-03 orphan became durable")
	}
	physical := git.records[idsWorkspace(request.WorkspaceID)]
	// Discard all process-local state, then rebuild current authority from the
	// sealed journal. Attribution must come from durable binding absence plus
	// the exact reservation and registered Git facts, never an orphan map.
	events, _ := EventRegistryV1()
	reducers, _ := ReducerRegistryV1()
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	restored, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{
		Reader: journal, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest},
		Binding: checkpoint.RestoreBindingV1{DatabaseID: "database-gj03", HostInstanceID: "host-gj03"}, EventRegistry: events, Reducers: reducers,
	})
	if err != nil {
		t.Fatal(err)
	}
	freshProcess, err := restoreValidated(t.Context(), testConfig(t), journal, git, journal, restored)
	if err != nil {
		t.Fatal(err)
	}
	if result := freshProcess.ReconcileWorkspace(t.Context(), request.WorkspaceID, physical); result.Class != WorkspaceOrphanUnbound {
		t.Fatalf("classification=%+v", result)
	}
	if _, err := freshProcess.EnsureWorkspace(t.Context(), request); !IsOutcome(err, OutcomeWorkspaceNotReady) {
		t.Fatalf("orphan-reuse mutant survived: %v", err)
	}
	fresh := request
	fresh.WorkspaceID, fresh.OperationID, fresh.OwnedBranch, fresh.LaneID = "workspace-gj03-w3", "bind-gj03-w3", "runs/gj03-w3", "lane-gj03-w3"
	if _, err := freshProcess.EnsureWorkspace(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
}

func TestGJ04RetryReturnsSameBindingWithoutDuplicateWorkspace(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git).WithFaults(&cutHook{id: "GJ-04", remaining: 1})
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "gj04", now))
	request := workspaceRequest(authority, "gj04-w1", now.Add(time.Second))
	if _, err := service.EnsureWorkspace(t.Context(), request); !IsOutcome(err, OutcomeAmbiguous) {
		t.Fatalf("GJ-04 result=%v", err)
	}
	first, ok := service.Binding(request.WorkspaceID)
	if !ok {
		t.Fatal("durable binding missing")
	}
	replayed, err := service.EnsureWorkspace(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.RecordRevision != first.RecordRevision || len(git.records) != 1 {
		t.Fatalf("duplicate/rebound workspace: first=%+v replay=%+v count=%d", first, replayed, len(git.records))
	}
}

func TestGJ04ReplayBindsRepositoryLaneGitAuthorityAndTimeAcrossRestore(t *testing.T) {
	git := newFakeGit(t)
	journal := newJournalFixture()
	core := mustService(t, journal, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, _ := core.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "gj04-digest", now))
	request := workspaceRequest(authority, "gj04-digest", now.Add(time.Second))
	if _, err := core.EnsureWorkspace(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	assertMutants := func(t *testing.T, target *service) {
		t.Helper()
		mutants := []func(*WorkspaceRequest){
			func(r *WorkspaceRequest) { r.RepositoryID = "repository-other" }, func(r *WorkspaceRequest) { r.LaneID = "lane-other" },
			func(r *WorkspaceRequest) { r.OwnedBranch = "runs/other" }, func(r *WorkspaceRequest) { r.PinnedBase = "base-other" },
			func(r *WorkspaceRequest) { r.WorkspaceID = "workspace-other" }, func(r *WorkspaceRequest) { r.Authority.FenceToken++ },
			func(r *WorkspaceRequest) { r.Authority.RuntimeEpochID = "epoch-other" }, func(r *WorkspaceRequest) { r.Authority.AuthorityHeadRecordRevision++ },
			func(r *WorkspaceRequest) { r.Now = r.Now.Add(time.Nanosecond) },
		}
		for index, mutate := range mutants {
			changed := request
			mutate(&changed)
			if _, err := target.EnsureWorkspace(t.Context(), changed); err == nil {
				t.Fatalf("workspace replay mutant %d survived", index)
			}
		}
	}
	assertMutants(t, core)
	events, _ := EventRegistryV1()
	reducers, _ := ReducerRegistryV1()
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	restored, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{Reader: journal, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest}, Binding: checkpoint.RestoreBindingV1{DatabaseID: "database-gj04-digest", HostInstanceID: "host-gj04-digest"}, EventRegistry: events, Reducers: reducers})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := restoreValidated(t.Context(), testConfig(t), journal, git, journal, restored)
	if err != nil {
		t.Fatal(err)
	}
	assertMutants(t, fresh)
	replayed, err := fresh.EnsureWorkspace(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.RepositoryID != request.RepositoryID || replayed.LaneID != request.LaneID || replayed.OwnedBranch != request.OwnedBranch || replayed.PinnedBase != request.PinnedBase {
		t.Fatalf("durable replay changed binding: %+v", replayed)
	}
}

func TestReconcileWorkspaceManualMovementAndMissingRegistrationFailClosed(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "reconcile", now))
	request := workspaceRequest(authority, "reconcile", now.Add(time.Second))
	_, err := service.EnsureWorkspace(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	record := git.records[idsWorkspace(request.WorkspaceID)]
	probe := git.probes[idsWorkspace(request.WorkspaceID)]
	probe.Branch = "manual/move"
	git.probes[idsWorkspace(request.WorkspaceID)] = probe
	if result := service.ReconcileWorkspace(t.Context(), request.WorkspaceID, record); result.Class != WorkspaceIndeterminate {
		t.Fatalf("manual movement was adopted: %+v", result)
	}
	probe.Branch, probe.Registered = record.OwnedBranch, false
	git.probes[idsWorkspace(request.WorkspaceID)] = probe
	if result := service.ReconcileWorkspace(t.Context(), request.WorkspaceID, record); result.Class != WorkspaceIndeterminate {
		t.Fatalf("missing registration was adopted: %+v", result)
	}
}

func idsWorkspace(id controljournal.WorkspaceID) ids.WorkspaceID { return ids.WorkspaceID(id) }
