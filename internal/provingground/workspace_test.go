package provingground

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type fakeGit struct {
	mu      sync.Mutex
	root    string
	records map[ids.WorkspaceID]workspace.Record
	probes  map[ids.WorkspaceID]workspace.Probe
}

func newFakeGit(t *testing.T) *fakeGit {
	t.Helper()
	return &fakeGit{root: t.TempDir(), records: make(map[ids.WorkspaceID]workspace.Record), probes: make(map[ids.WorkspaceID]workspace.Probe)}
}

func (g *fakeGit) WorkspacePath(id string) (string, error) { return filepath.Join(g.root, id), nil }
func (g *fakeGit) Create(_ context.Context, record workspace.Record) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.records[record.WorkspaceID]; ok {
		if existing.OwnedBranch != record.OwnedBranch {
			return &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "branch mismatch"}
		}
		return nil
	}
	if err := os.MkdirAll(record.CanonicalPath, 0o700); err != nil {
		return err
	}
	record.Stage, record.Status = workspace.StageReady, "ready"
	record.Registered, record.ObservedHead, record.DependencyHead = true, record.PinnedBase, record.PinnedBase
	record.GitDir, record.IndexPath = filepath.Join(g.root, ".git", string(record.WorkspaceID)), filepath.Join(g.root, ".git", string(record.WorkspaceID), "index")
	g.records[record.WorkspaceID] = record
	g.probes[record.WorkspaceID] = workspace.Probe{Registered: true, Clean: true, Head: record.PinnedBase, Branch: record.OwnedBranch, GitDir: record.GitDir, IndexPath: record.IndexPath}
	return nil
}
func (g *fakeGit) Probe(_ context.Context, record workspace.Record) (workspace.Probe, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.probes[record.WorkspaceID], nil
}
func (g *fakeGit) Inspect(_ context.Context, record workspace.Record) (gitrepo.Inspection, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	probe, ok := g.probes[record.WorkspaceID]
	if !ok {
		return gitrepo.Inspection{Known: true, CanonicalPath: record.CanonicalPath}, nil
	}
	result := gitrepo.Inspection{Known: true, Registered: probe.Registered, Clean: probe.Clean, CanonicalPath: record.CanonicalPath, RecordedBranch: record.OwnedBranch, ObservedBranch: probe.Branch, RecordedHead: record.DependencyHead, ObservedHead: probe.Head}
	if !probe.Registered {
		result.Discrepancies = append(result.Discrepancies, gitrepo.Discrepancy{Fact: "registration", Observed: "absent", Expected: "registered"})
	}
	if probe.Branch != record.OwnedBranch {
		result.Discrepancies = append(result.Discrepancies, gitrepo.Discrepancy{Fact: "branch", Observed: probe.Branch, Expected: record.OwnedBranch})
	}
	return result, nil
}

func workspaceRequest(authority Authority, suffix string, now time.Time) WorkspaceRequest {
	return WorkspaceRequest{OperationID: controljournal.OperationID("bind-" + suffix), Authority: authority, WorkspaceID: controljournal.WorkspaceID("workspace-" + suffix), RepositoryID: "repository-m1", LaneID: ids.LaneID("lane-" + suffix), OwnedBranch: "runs/" + suffix, PinnedBase: "0123456789abcdef0123456789abcdef01234567", Now: now}
}

func TestWorkspaceW1W2DistinctAndDurablyBound(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	a, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "wa", now))
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Claim(t.Context(), claimRequest("work-b", "actor-b", "process-b", "wb", now))
	if err != nil {
		t.Fatal(err)
	}
	w1, err := service.EnsureWorkspace(t.Context(), workspaceRequest(a, "w1", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	w2, err := service.EnsureWorkspace(t.Context(), workspaceRequest(b, "w2", now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if w1.WorkspaceID == w2.WorkspaceID || w1.CanonicalPath == w2.CanonicalPath || w1.OwnedBranch == w2.OwnedBranch || w1.GitDir == w2.GitDir || w1.IndexPath == w2.IndexPath {
		t.Fatalf("workspaces not isolated: w1=%+v w2=%+v", w1, w2)
	}
	assertAuthorityInvariants(t, service)
}

func TestReplacementActorAProcessCReceivesFreshW3WhileBContinues(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	a, _ := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "ra", now))
	b, _ := service.Claim(t.Context(), claimRequest("work-b", "actor-b", "process-b", "rb", now))
	w1, _ := service.EnsureWorkspace(t.Context(), workspaceRequest(a, "w1", now.Add(time.Second)))
	w2, _ := service.EnsureWorkspace(t.Context(), workspaceRequest(b, "w2", now.Add(time.Second)))
	if err := service.Expire(t.Context(), "expire-a", a, a.Deadline); err != nil {
		t.Fatal(err)
	}
	cRequest := claimRequest("work-a", "actor-a", "process-c", "rc", a.Deadline.Add(time.Second))
	cRequest.RuntimeEpochID = "epoch-one"
	c, err := service.Claim(t.Context(), cRequest)
	if err != nil {
		t.Fatal(err)
	}
	w3, err := service.EnsureWorkspace(t.Context(), workspaceRequest(c, "w3", cRequest.Now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if c.ActorID != a.ActorID || c.ProcessID == a.ProcessID || w1.WorkspaceID == w3.WorkspaceID || w1.CanonicalPath == w3.CanonicalPath {
		t.Fatalf("replacement identity wrong: a=%+v c=%+v w1=%+v w3=%+v", a, c, w1, w3)
	}
	if currentB, ok := service.Current("work-b"); !ok || currentB.LeaseID != b.LeaseID {
		t.Fatalf("B stopped during A replacement: %+v", currentB)
	}
	if retained, ok := service.Binding(w2.WorkspaceID); !ok || retained.Status != "active-bound" || retained.RecordRevision != w2.RecordRevision {
		t.Fatalf("W2 changed during A replacement: %+v", retained)
	}
	if quarantined, _ := service.Binding(w1.WorkspaceID); quarantined.Status != "quarantined" {
		t.Fatalf("W1 not quarantined: %+v", quarantined)
	}
}

type blockingGit struct {
	*fakeGit
	created chan struct{}
	release <-chan struct{}
}

func (g *blockingGit) Create(ctx context.Context, record workspace.Record) error {
	if err := g.fakeGit.Create(ctx, record); err != nil {
		return err
	}
	close(g.created)
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWorkspaceBindRaceWithExpiryRefusesAndLeavesOrphan(t *testing.T) {
	base := newFakeGit(t)
	release := make(chan struct{})
	git := &blockingGit{fakeGit: base, created: make(chan struct{}), release: release}
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "bind-race", now))
	if err != nil {
		t.Fatal(err)
	}
	request := workspaceRequest(authority, "bind-race", now.Add(time.Second))
	result := make(chan error, 1)
	go func() { _, err := service.EnsureWorkspace(t.Context(), request); result <- err }()
	<-git.created
	if err := service.Expire(t.Context(), "expire-bind-race", authority, authority.Deadline); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !IsOutcome(err, OutcomeStaleLease) {
		t.Fatalf("expired bind survived: %v", err)
	}
	if _, ok := service.Binding(request.WorkspaceID); ok {
		t.Fatal("expired bind became durable")
	}
	physical := base.records[ids.WorkspaceID(request.WorkspaceID)]
	if reconciled := service.ReconcileWorkspace(t.Context(), request.WorkspaceID, physical); reconciled.Class != WorkspaceOrphanUnbound {
		t.Fatalf("physical race result=%+v", reconciled)
	}
}

func TestWorkspaceReuseMutantSecondBindingForSameLeaseRefuses(t *testing.T) {
	git := newFakeGit(t)
	service := mustService(t, &recordingApplier{}, git)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, err := service.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "reuse-mutant", now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsureWorkspace(t.Context(), workspaceRequest(authority, "reuse-w1", now.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsureWorkspace(t.Context(), workspaceRequest(authority, "reuse-w2", now.Add(2*time.Second))); !IsOutcome(err, OutcomeWorkspaceNotReady) {
		t.Fatalf("second workspace binding survived: %v", err)
	}
	if len(service.Projection().Workspaces) != 1 {
		t.Fatalf("durable workspace count=%d", len(service.Projection().Workspaces))
	}
}
