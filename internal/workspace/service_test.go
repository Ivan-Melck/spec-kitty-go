package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestEnsureReadyPersistsAndProbesEveryStage(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	git := &fakeGit{path: "/runtime/workspaces/workspace-1", base: "0123456789abcdef0123456789abcdef01234567", branch: "runs/work-1", dependencyHead: "abcdef0123456789abcdef0123456789abcdef01"}
	hook := &recordingHook{}
	service := NewService(store, git).WithFaults(hook)
	request := reservationFixture()
	record, err := service.EnsureReady(context.Background(), request)
	if err != nil || !record.Ready() {
		t.Fatalf("ready record=%+v err=%v", record, err)
	}
	if got, want := store.transitions, []Stage{StageWorktreeCreated, StageBaseVerified, StageDependenciesPrepared, StageOwnershipVerified, StageReady}; !equalStages(got, want) {
		t.Fatalf("transitions=%v want=%v", got, want)
	}
	if got, want := hook.boundaries, []string{"SR-05", "SR-06", "SR-07", "SR-08", "SR-09"}; !equalStrings(got, want) {
		t.Fatalf("fault boundaries=%v want=%v", got, want)
	}
	if git.createCalls != 1 || git.prepareCalls != 1 || git.probeCalls < 4 {
		t.Fatalf("Git calls create=%d prepare=%d probe=%d", git.createCalls, git.prepareCalls, git.probeCalls)
	}
}

func TestEnsureReadyRecoversCrashAfterWorktreeEffectWithoutExistenceAsReadiness(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	git := &fakeGit{path: "/runtime/workspaces/workspace-1", base: "0123456789abcdef0123456789abcdef01234567", branch: "runs/work-1", dependencyHead: "abcdef0123456789abcdef0123456789abcdef01"}
	hook := &crashOnceHook{boundary: "SR-06"}
	service := NewService(store, git).WithFaults(hook)
	request := reservationFixture()
	if _, err := service.EnsureReady(context.Background(), request); !errors.Is(err, faults.ErrCrashRequested) {
		t.Fatalf("first attempt error=%v", err)
	}
	if store.record.Stage != StageReserved || !git.created {
		t.Fatalf("crash state stage=%s created=%v", store.record.Stage, git.created)
	}
	record, err := service.EnsureReady(context.Background(), request)
	if err != nil || !record.Ready() || git.createCalls != 2 {
		t.Fatalf("recovery record=%+v err=%v create_calls=%d", record, err, git.createCalls)
	}

	otherStore := newMemoryStore()
	unregistered := &fakeGit{path: git.path, base: git.base, branch: git.branch, dependencyHead: git.dependencyHead, hideRegistration: true}
	_, err = NewService(otherStore, unregistered).EnsureReady(context.Background(), request)
	if !IsKind(err, KindWorkspaceNotReady) || otherStore.record.Stage != StageReserved {
		t.Fatalf("unregistered directory error=%v stage=%s", err, otherStore.record.Stage)
	}
}

func TestValidateAuthenticatesFinalizedLaneAndRepositoryTargetBeforeReservation(t *testing.T) {
	t.Parallel()
	request := reservationFixture()
	store := newMemoryStore()
	git := &fakeGit{path: "/runtime/workspaces/workspace-1", base: request.PinnedBase, branch: request.OwnedBranch, dependencyHead: request.PinnedBase}
	service := NewService(store, git)

	wrongLane := request
	wrongLane.LaneID = "lane-invented"
	if err := service.Validate(context.Background(), wrongLane); !IsKind(err, KindInvalid) {
		t.Fatalf("invented lane error=%v", err)
	}
	staleBase := request
	staleBase.PinnedBase = "9999999999999999999999999999999999999999"
	if err := service.Validate(context.Background(), staleBase); !IsKind(err, KindTargetMoved) {
		t.Fatalf("stale base error=%v", err)
	}
	if store.reserveCalls != 0 || git.createCalls != 0 {
		t.Fatalf("preflight published workspace: reserve=%d create=%d", store.reserveCalls, git.createCalls)
	}
}

func TestValidateBindingRejectsASecondPhysicalRepositoryBeforeWorkspaceMutation(t *testing.T) {
	t.Parallel()
	request := reservationFixture()
	store := newMemoryStore()
	git := &fakeGit{path: "/runtime/workspaces/workspace-1", base: request.PinnedBase, branch: request.OwnedBranch, dependencyHead: request.PinnedBase, commonDir: "/clone/.git", primaryPath: "/clone"}
	service := NewService(store, git)

	err := service.ValidateBinding(context.Background(), request)
	var bindingError *Error
	if !errors.As(err, &bindingError) || bindingError.Kind != KindBindingConflict {
		t.Fatalf("binding error=%v", err)
	}
	if bindingError.Observed["common_dir"] != "/clone/.git" || bindingError.Expected["common_dir"] != "/repo/.git" || bindingError.Observed["primary_path"] != "/clone" || bindingError.Expected["primary_path"] != "/repo" {
		t.Fatalf("incomplete physical binding facts: %+v", bindingError)
	}
	if _, err := service.EnsureReady(context.Background(), request); !IsKind(err, KindBindingConflict) {
		t.Fatalf("EnsureReady bypassed binding authentication: %v", err)
	}
	if store.reserveCalls != 0 || git.createCalls != 0 {
		t.Fatalf("binding mismatch mutated workspace: reserve=%d create=%d", store.reserveCalls, git.createCalls)
	}
}

func TestReadyReplayReprobesAndQuarantinesDrift(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	request := reservationFixture()
	git := &fakeGit{path: "/runtime/workspaces/workspace-1", base: request.PinnedBase, branch: request.OwnedBranch, dependencyHead: "abcdef0123456789abcdef0123456789abcdef01"}
	service := NewService(store, git)
	if record, err := service.EnsureReady(context.Background(), request); err != nil || !record.Ready() {
		t.Fatalf("initial readiness=%+v err=%v", record, err)
	}
	git.branch = "runs/intruder"
	if _, err := service.EnsureReady(context.Background(), request); !IsKind(err, KindWorkspaceNotReady) {
		t.Fatalf("drift replay error=%v", err)
	}
	if store.record.Status != "quarantined" {
		t.Fatalf("drifted workspace status=%q", store.record.Status)
	}
	git.branch = request.OwnedBranch
	if _, err := service.EnsureReady(context.Background(), request); !IsKind(err, KindWorkspaceNotReady) {
		t.Fatalf("quarantined replay error=%v", err)
	}
}

func TestTargetMovementAfterFinalReadCannotPublishReady(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	request := reservationFixture()
	git := &fakeGit{path: "/runtime/workspaces/workspace-1", base: request.PinnedBase, branch: request.OwnedBranch, dependencyHead: "abcdef0123456789abcdef0123456789abcdef01", moveOnProbeCall: 5}
	_, err := NewService(store, git).EnsureReady(context.Background(), request)
	if !IsKind(err, KindTargetMoved) {
		t.Fatalf("publication-window movement error=%v", err)
	}
	if store.record.Stage == StageReady || store.record.Ready() {
		t.Fatalf("target movement published readiness: %+v", store.record)
	}
}

func reservationFixture() Reservation {
	return Reservation{
		OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", RunID: "run-1", WorkspaceID: "workspace-1", RepositoryID: "repository-1", LaneID: "lane-1",
		Authority: Authority{LeaseID: "lease-1", Generation: 1, Token: "token-1"}, OwnedBranch: "runs/work-1",
		PinnedBase: "0123456789abcdef0123456789abcdef01234567", Dependencies: []Dependency{{WorkID: "work-dependency", Candidate: "1111111111111111111111111111111111111111"}},
		Now: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
	}
}

type memoryStore struct {
	mu           sync.Mutex
	binding      RepositoryBinding
	record       Record
	transitions  []Stage
	reserveCalls int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{binding: RepositoryBinding{
		RepositoryID: "repository-1", ProjectID: "project-1", CommonDir: "/repo/.git", PrimaryPath: "/repo",
		ManagedRoot: "/runtime/workspaces", TargetRef: "refs/heads/main",
		Primary: PrimarySnapshot{Head: "0123456789abcdef0123456789abcdef01234567", IndexDigest: "index", TreeDigest: "tree"},
	}}
}

func (s *memoryStore) BindRepository(_ context.Context, binding RepositoryBinding) error {
	s.binding = binding
	return nil
}
func (s *memoryStore) Repository(_ context.Context, _ ids.RepositoryID) (RepositoryBinding, error) {
	return s.binding, nil
}
func (s *memoryStore) WorkAuthority(context.Context, ids.WorkRevisionID, ids.WorkID) (WorkAuthority, error) {
	return WorkAuthority{ProjectID: "project-1", MissionID: "mission-1", LaneID: "lane-1"}, nil
}
func (s *memoryStore) ReserveWorkspace(_ context.Context, reservation Reservation, path string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserveCalls++
	if s.record.WorkspaceID == "" {
		s.record = Record{Reservation: reservation, CanonicalPath: path, Stage: StageReserved, Status: "preparing", CreatedAt: reservation.Now, UpdatedAt: reservation.Now}
	}
	return s.record, nil
}
func (s *memoryStore) QuarantineWorkspace(context.Context, ids.WorkspaceID, Authority, string, time.Time) error {
	s.record.Status = "quarantined"
	return nil
}
func (s *memoryStore) Workspace(_ context.Context, _ ids.WorkspaceID) (Record, error) {
	return s.record, nil
}
func (s *memoryStore) AdvanceWorkspace(_ context.Context, _ ids.WorkspaceID, _ Authority, expected, target Stage, probe Probe, dependencyHead string, now time.Time) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Stage != expected {
		return Record{}, &Error{Kind: KindWorkspaceConflict, Message: "unexpected stage"}
	}
	s.record.Stage, s.record.Registered, s.record.ObservedHead = target, probe.Registered, probe.Head
	if dependencyHead != "" {
		s.record.DependencyHead = dependencyHead
	}
	if probe.GitDir != "" {
		s.record.GitDir = probe.GitDir
	}
	if probe.IndexPath != "" {
		s.record.IndexPath = probe.IndexPath
	}
	if target == StageReady {
		s.record.Status = "ready"
	}
	s.record.UpdatedAt = now
	s.transitions = append(s.transitions, target)
	return s.record, nil
}

type fakeGit struct {
	path, base, branch, dependencyHead    string
	commonDir, primaryPath                string
	created, hideRegistration             bool
	createCalls, probeCalls, prepareCalls int
	moveOnProbeCall                       int
}

func (g *fakeGit) WorkspacePath(string) (string, error) { return g.path, nil }
func (g *fakeGit) Binding() RepositoryBinding {
	commonDir, primaryPath := g.commonDir, g.primaryPath
	if commonDir == "" {
		commonDir = "/repo/.git"
	}
	if primaryPath == "" {
		primaryPath = "/repo"
	}
	return RepositoryBinding{RepositoryID: "repository-1", ProjectID: "project-1", CommonDir: commonDir, PrimaryPath: primaryPath, ManagedRoot: "/runtime/workspaces", TargetRef: "refs/heads/main", Primary: PrimarySnapshot{Head: g.base, IndexDigest: "index", TreeDigest: "tree"}}
}
func (g *fakeGit) TargetHead(context.Context) (string, error) { return g.base, nil }
func (g *fakeGit) WithTargetAuthority(_ context.Context, publish func(string) error) error {
	return publish(g.base)
}
func (g *fakeGit) Create(context.Context, Record) error {
	g.createCalls++
	g.created = true
	return nil
}
func (g *fakeGit) Probe(context.Context, Record) (Probe, error) {
	g.probeCalls++
	if !g.created || g.hideRegistration {
		return Probe{}, nil
	}
	head := g.base
	if g.prepareCalls > 0 {
		head = g.dependencyHead
	}
	probe := Probe{Registered: true, Head: head, Branch: g.branch, GitDir: "/git/worktrees/workspace-1", IndexPath: "/git/worktrees/workspace-1/index", Clean: true}
	if g.moveOnProbeCall == g.probeCalls {
		g.base = "9999999999999999999999999999999999999999"
	}
	return probe, nil
}
func (g *fakeGit) PrepareDependencies(context.Context, Record, []Dependency) (string, error) {
	g.prepareCalls++
	return g.dependencyHead, nil
}

type recordingHook struct{ boundaries []string }

func (h *recordingHook) Reach(_ context.Context, boundary string) error {
	h.boundaries = append(h.boundaries, boundary)
	return nil
}

type crashOnceHook struct {
	boundary string
	crashed  bool
}

func (h *crashOnceHook) Reach(_ context.Context, boundary string) error {
	if boundary == h.boundary && !h.crashed {
		h.crashed = true
		return faults.ErrCrashRequested
	}
	return nil
}

func equalStages(left, right []Stage) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
