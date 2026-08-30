package provingground

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
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

type fixedExecutionIdentities struct {
	run   ids.RunID
	lease ids.LeaseID
}

func (f fixedExecutionIdentities) RunID() (ids.RunID, error)     { return f.run, nil }
func (f fixedExecutionIdentities) LeaseID() (ids.LeaseID, error) { return f.lease, nil }

type fixedExecutionClock struct{ now time.Time }

func (f fixedExecutionClock) Now() time.Time { return f.now }

type sequenceExecutionIdentities struct {
	mu     sync.Mutex
	run    ids.RunID
	leases []ids.LeaseID
	next   int
}

func (s *sequenceExecutionIdentities) RunID() (ids.RunID, error) { return s.run, nil }
func (s *sequenceExecutionIdentities) LeaseID() (ids.LeaseID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.leases) {
		return "", context.Canceled
	}
	lease := s.leases[s.next]
	s.next++
	return lease, nil
}

func TestPublicReplaceWorkspacePrevalidatesAndCreatesFreshW3WhileBContinues(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	git := newFakeGit(t)
	core := mustService(t, &recordingApplier{}, git)
	identities := &sequenceExecutionIdentities{run: "run-m1", leases: []ids.LeaseID{"lease-public-a", "lease-public-b", "lease-public-c"}}
	authorityRepository := &executionAuthorityRepository{core: core}
	runtime := &Runtime{core: core, execution: execution.NewService(authorityRepository, identities, fixedExecutionClock{now}), authority: authorityRepository}
	governance := testGovernanceContext(t, now)
	startA := execution.StartRequest{OperationID: "claim-public-a", ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: "work-a", ActorID: "actor-a", ProcessID: "process-a", Now: now, LeaseTTL: time.Minute, GovernanceContext: governance}
	startB := execution.StartRequest{OperationID: "claim-public-b", ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: "work-b", ActorID: "actor-b", ProcessID: "process-b", Now: now, LeaseTTL: time.Minute, GovernanceContext: governance}
	w1, err := runtime.StartWorkspace(t.Context(), startA, WorkspaceSpec{OperationID: "bind-public-w1", WorkspaceID: "workspace-public-w1", RepositoryID: "repository-m1", LaneID: "lane-public-w1", OwnedBranch: "runs/public-w1", PinnedBase: "0123456789abcdef0123456789abcdef01234567", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := runtime.StartWorkspace(t.Context(), startB, WorkspaceSpec{OperationID: "bind-public-w2", WorkspaceID: "workspace-public-w2", RepositoryID: "repository-m1", LaneID: "lane-public-w2", OwnedBranch: "runs/public-w2", PinnedBase: "0123456789abcdef0123456789abcdef01234567", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*controljournal.AuthorityCapsuleV1){func(c *controljournal.AuthorityCapsuleV1) { c.RuntimeEpochID = "epoch-other" }, func(c *controljournal.AuthorityCapsuleV1) { c.AuthorityHeadRecordRevision++ }} {
		changed := w1.Authority
		mutate(&changed)
		if _, err := runtime.Heartbeat(t.Context(), "heartbeat-public-mutant", changed, now.Add(2*time.Second), time.Minute); err == nil {
			t.Fatal("heartbeat accepted stale epoch/head capsule")
		}
		if err := runtime.Expire(t.Context(), "expire-public-capsule-mutant", changed, w1.Run.Deadline); err == nil {
			t.Fatal("expiry accepted stale epoch/head capsule")
		}
		if binding, _ := runtime.Binding(w1.Authority.WorkspaceID); binding.Status != "active-bound" {
			t.Fatalf("capsule mutant quarantined W1: %+v", binding)
		}
	}
	refreshed, err := runtime.Heartbeat(t.Context(), "heartbeat-public-a", w1.Authority, now.Add(2*time.Second), time.Minute)
	if err != nil || refreshed.AuthorityHeadRecordRevision == w1.Authority.AuthorityHeadRecordRevision {
		t.Fatalf("public heartbeat did not refresh exact capsule: before=%+v after=%+v err=%v", w1.Authority, refreshed, err)
	}
	w1.Authority = refreshed
	w1.Run.Deadline = now.Add(2*time.Second + time.Minute)
	startC := execution.StartRequest{OperationID: "claim-public-c", ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: "work-a", ActorID: "actor-a", ProcessID: "process-c", Now: w1.Run.Deadline.Add(time.Second), LeaseTTL: time.Minute, GovernanceContext: governance}
	specW3 := WorkspaceSpec{OperationID: "bind-public-w3", WorkspaceID: "workspace-public-w3", RepositoryID: "repository-m1", LaneID: "lane-public-w3", OwnedBranch: "runs/public-w3", PinnedBase: "0123456789abcdef0123456789abcdef01234567", Now: startC.Now.Add(time.Second)}
	mutants := []func(*execution.StartRequest, *WorkspaceSpec){
		func(_ *execution.StartRequest, s *WorkspaceSpec) { s.WorkspaceID = w1.Authority.WorkspaceID },
		func(s *execution.StartRequest, _ *WorkspaceSpec) { s.ProcessID = "process-a" },
		func(s *execution.StartRequest, _ *WorkspaceSpec) { s.ActorID = "actor-b" },
	}
	for index, mutate := range mutants {
		changedStart, changedSpec := startC, specW3
		mutate(&changedStart, &changedSpec)
		if _, err := runtime.ReplaceWorkspace(t.Context(), controljournal.OperationID("expire-public-mutant-"+string(rune('a'+index))), w1.Authority, w1.Run.Deadline, changedStart, changedSpec); !IsOutcome(err, OutcomeInvalidCommand) {
			t.Fatalf("prevalidation mutant %d: %v", index, err)
		}
		if binding, _ := runtime.Binding(w1.Authority.WorkspaceID); binding.Status != "active-bound" {
			t.Fatalf("mutant %d quarantined W1: %+v", index, binding)
		}
	}
	w3, err := runtime.ReplaceWorkspace(t.Context(), "expire-public-a", w1.Authority, w1.Run.Deadline, startC, specW3)
	if err != nil {
		t.Fatal(err)
	}
	if w3.Authority.ProcessID != "process-c" || w3.Authority.WorkspaceID == w1.Authority.WorkspaceID || w3.Authority.LeaseID == w1.Authority.LeaseID {
		t.Fatalf("replacement not fresh: w1=%+v w3=%+v", w1.Authority, w3.Authority)
	}
	if binding, _ := runtime.Binding(w1.Authority.WorkspaceID); binding.Status != "quarantined" {
		t.Fatalf("W1 not quarantined: %+v", binding)
	}
	if binding, _ := runtime.Binding(w2.Authority.WorkspaceID); binding.Status != "active-bound" {
		t.Fatalf("B/W2 stopped: %+v", binding)
	}
}

func TestPublicExactCapsuleAdmissionCannotBeErasedByConcurrentHeadAdvance(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	core := mustService(t, &recordingApplier{}, newFakeGit(t))
	stale, err := core.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-a", "toctou", now))
	if err != nil {
		t.Fatal(err)
	}
	repository := &executionAuthorityRepository{core: core}
	release, err := repository.bindExact("expire-toctou", stale)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	refreshed, err := core.Heartbeat(t.Context(), "heartbeat-toctou", stale, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	service := execution.NewService(repository, fixedExecutionIdentities{run: "run-m1", lease: "lease-unused"}, fixedExecutionClock{now})
	err = service.Expire(t.Context(), execution.ExpireRequest{SchemaVersion: execution.ResultSchemaVersion, OperationID: "expire-toctou", ActorID: "actor-a", ProcessID: "process-a", LeaseID: ids.LeaseID(stale.LeaseID), Generation: int64(stale.Generation), Token: "1", Now: refreshed.Deadline})
	if !IsOutcome(err, OutcomeStaleLease) {
		t.Fatalf("stale exact capsule was replaced by current head: %v", err)
	}
	if current, ok := core.Current("work-a"); !ok || current.AuthorityHeadRecordRevision != refreshed.AuthorityHeadRecordRevision {
		t.Fatalf("stale exact expiry damaged refreshed authority: %+v %v", current, ok)
	}
}

func TestRealGapDBExecutionGitGJ04AndExpireExactRetryAfterSealedRestart(t *testing.T) {
	binary := os.Getenv("SPEC_KITTY_M1_GAPDBD")
	if binary == "" {
		t.Skip("set SPEC_KITTY_M1_GAPDBD to the exact admitted gapdbd")
	}
	binary = privateAdmittedBinary(t, binary)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	runtimeRoot := privateProvingGroundRoot(t)
	dataRoot := privateProvingGroundRoot(t)
	lifecycle, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
		ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repoRoot, "docs", "programs", "evidence", "m1-admission.json"),
		RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(dataRoot, "database"), SupervisorRunID: "run-m1",
		ProductPolicySHA256: strings.Repeat("a", 64), StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Errorf("stop GapDB: %v", err)
		}
	}()
	adapter, connection, err := storegapdb.Dial(lifecycle.Provenance().ConfiguredSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	events, err := EventRegistryV1()
	if err != nil {
		t.Fatal(err)
	}
	reducers, err := ReducerRegistryV1()
	if err != nil {
		t.Fatal(err)
	}
	eventDigest, _ := events.Digest()
	reducerDigest, _ := reducers.Digest()
	binding, err := lifecycle.ProductRestoreBindingV1()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := application.NewRestoreReaderV1(adapter, "project-m1", "mission-m1")
	if err != nil {
		t.Fatal(err)
	}
	pristine, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{
		Reader: reader, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest},
		Binding: binding, EventRegistry: events, Reducers: reducers,
	})
	if err != nil || !pristine.Pristine {
		t.Fatalf("pristine restore=%+v err=%v", pristine, err)
	}
	pristineAdmission, err := pristine.AdmissionV1()
	if err != nil {
		t.Fatal(err)
	}
	productProof, err := lifecycle.MarkProductRecovered(pristineAdmission)
	if err != nil {
		t.Fatal(err)
	}
	startupProof, err := lifecycle.BindOwnerStartupEpochV1("epoch-one")
	if err != nil {
		t.Fatal(err)
	}
	epochAdmission, result, err := application.AdmitRuntimeEpochV1(t.Context(), adapter, application.EpochAdmissionInputV1{
		ProjectID: "project-m1", MissionID: "mission-m1", OperationID: "admit-proving-ground", EventID: "event-admit-proving-ground",
		StartupProof: startupProof, ProductRestoreProof: productProof,
	})
	if err != nil || result.Kind != application.OutcomeCommitted {
		t.Fatalf("epoch admission=%+v err=%v", result, err)
	}
	app, err := application.NewService(adapter, epochAdmission)
	if err != nil {
		t.Fatal(err)
	}
	primary, base := newRealPrimary(t)
	repository, err := gitrepo.Bind(t.Context(), gitrepo.Runner{}, "repository-m1", "project-m1", primary, filepath.Join(t.TempDir(), "workspaces"), "refs/heads/main", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	primaryBefore := repository.Binding().Primary
	config := testConfig(t)
	identities := &sequenceExecutionIdentities{run: "run-m1", leases: []ids.LeaseID{"lease-real-b", "lease-real-gj04"}}
	runtime, err := NewRuntime(config, app, repository, identities, fixedExecutionClock{now})
	if err != nil {
		t.Fatal(err)
	}
	governance := testGovernanceContext(t, now)
	startB := execution.StartRequest{CorrelationID: "real-b", OperationID: "claim-real-b", ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: "work-b", ActorID: "actor-b", ProcessID: "process-b", Now: now, LeaseTTL: time.Minute, GovernanceContext: governance}
	specB := WorkspaceSpec{OperationID: "bind-real-w2", WorkspaceID: "workspace-real-w2", RepositoryID: "repository-m1", LaneID: "lane-real-w2", OwnedBranch: "runs/real-w2", PinnedBase: base, Now: now.Add(time.Second)}
	deliveryB, err := runtime.StartWorkspace(t.Context(), startB, specB)
	if err != nil {
		t.Fatal(err)
	}
	runtime.core.WithFaults(&cutHook{id: "GJ-04", remaining: 1})
	start := execution.StartRequest{CorrelationID: "real-gj04", OperationID: "claim-real-gj04", ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: "work-a", ActorID: "actor-a", ProcessID: "process-a", Now: now, LeaseTTL: time.Minute, GovernanceContext: governance}
	spec := WorkspaceSpec{OperationID: "bind-real-gj04", WorkspaceID: "workspace-real-gj04", RepositoryID: "repository-m1", LaneID: "lane-real-gj04", OwnedBranch: "runs/real-gj04", PinnedBase: base, Now: now.Add(time.Second)}
	if _, err := runtime.StartWorkspace(t.Context(), start, spec); !IsOutcome(err, OutcomeAmbiguous) {
		t.Fatalf("GJ-04 result=%v", err)
	}
	if _, bound := runtime.Binding(spec.WorkspaceID); !bound {
		t.Fatal("GJ-04 did not leave durable authority and binding")
	}
	restoreReader, _ := application.NewRestoreReaderV1(adapter, "project-m1", "mission-m1")
	restored, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{
		Reader: restoreReader, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest},
		Binding: binding, EventRegistry: events, Reducers: reducers,
	})
	if err != nil {
		t.Fatal(err)
	}
	restartedIdentities := &sequenceExecutionIdentities{run: "run-m1", leases: []ids.LeaseID{"lease-replay-unused", "lease-real-c"}}
	restarted, err := RestoreRuntimeValidated(t.Context(), config, app, repository, adapter, restored, restartedIdentities, fixedExecutionClock{now})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := restarted.StartWorkspace(t.Context(), start, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !delivery.Run.Replayed {
		t.Fatal("GJ-04 capsule was not replayed")
	}
	startC := execution.StartRequest{CorrelationID: "real-c", OperationID: "claim-real-c", ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: "work-a", ActorID: "actor-a", ProcessID: "process-c", Now: delivery.Run.Deadline.Add(time.Second), LeaseTTL: time.Minute, GovernanceContext: governance}
	specC := WorkspaceSpec{OperationID: "bind-real-w3", WorkspaceID: "workspace-real-w3", RepositoryID: "repository-m1", LaneID: "lane-real-w3", OwnedBranch: "runs/real-w3", PinnedBase: base, Now: startC.Now.Add(time.Second)}
	deliveryC, err := restarted.ReplaceWorkspace(t.Context(), "expire-real-restart", delivery.Authority, delivery.Run.Deadline, startC, specC)
	if err != nil {
		t.Fatal(err)
	}
	if deliveryC.Authority.ProcessID != "process-c" || deliveryC.Authority.WorkspaceID == delivery.Authority.WorkspaceID {
		t.Fatalf("fresh W3 was not delivered: %+v", deliveryC.Authority)
	}
	if got, _ := restarted.Binding(deliveryB.Authority.WorkspaceID); got.Status != "active-bound" {
		t.Fatalf("B/W2 stopped: %+v", got)
	}
	postExpireReader, _ := application.NewRestoreReaderV1(adapter, "project-m1", "mission-m1")
	postExpire, err := checkpoint.RestoreV1(t.Context(), checkpoint.RestoreInputV1{
		Reader: postExpireReader, Source: checkpoint.SourceIdentityV1{ProjectID: "project-m1", MissionID: "mission-m1", RunID: "run-m1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest},
		Binding: binding, EventRegistry: events, Reducers: reducers,
	})
	if err != nil {
		t.Fatal(err)
	}
	afterProcessLoss, err := RestoreRuntimeValidated(t.Context(), config, app, repository, adapter, postExpire, restoredIdentities("run-m1", "lease-real-gj04"), fixedExecutionClock{now})
	if err != nil {
		t.Fatal(err)
	}
	if err := afterProcessLoss.Expire(t.Context(), "expire-real-restart", delivery.Authority, delivery.Run.Deadline); err != nil {
		t.Fatalf("committed expiry did not survive process reconstruction: %v", err)
	}
	if got, _ := afterProcessLoss.Binding(spec.WorkspaceID); got.Status != "quarantined" {
		t.Fatalf("restored expiry lost quarantine: %+v", got)
	}
	if got, _ := afterProcessLoss.Binding(specC.WorkspaceID); got.Status != "active-bound" {
		t.Fatalf("restored W3 missing: %+v", got)
	}
	if err := afterProcessLoss.Authorize(delivery.Authority, "handoff", delivery.Run.Deadline.Add(time.Second)); !IsOutcome(err, OutcomeStaleLease) {
		t.Fatalf("exact expired authority capsule survived sealed reconstruction: %v", err)
	}
	primaryAfter, err := repository.SnapshotPrimary(t.Context())
	if err != nil || primaryAfter != primaryBefore {
		t.Fatalf("primary checkout changed: before=%+v after=%+v err=%v", primaryBefore, primaryAfter, err)
	}
}

func restoredIdentities(run ids.RunID, lease ids.LeaseID) fixedExecutionIdentities {
	return fixedExecutionIdentities{run: run, lease: lease}
}

func testGovernanceContext(t *testing.T, now time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.CommitSHA256([]byte("proving-ground-governance-v1"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindConfig, "proving-ground", "v1", commitment)
	if err != nil {
		t.Fatal(err)
	}
	value, err := governancecontext.NewContext("context-proving-ground", governancecontext.SchemaVersion, []governancecontext.Entry{entry}, now)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newRealPrimary(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "primary")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runRealGit(t, root, "init", "-b", "main")
	runRealGit(t, root, "config", "user.name", "Spec Kitty")
	runRealGit(t, root, "config", "user.email", "spec-kitty@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("primary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runRealGit(t, root, "add", "README.md")
	runRealGit(t, root, "commit", "-m", "initial")
	return root, runRealGit(t, root, "rev-parse", "HEAD")
}

func runRealGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func privateProvingGroundRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "spk-proving-ground-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func privateAdmittedBinary(t *testing.T, source string) string {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	root := privateProvingGroundRoot(t)
	destination := filepath.Join(root, "gapdbd")
	if err := os.WriteFile(destination, content, 0o500); err != nil {
		t.Fatal(err)
	}
	return destination
}
