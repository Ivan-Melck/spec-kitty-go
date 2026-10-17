package scenario

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// TestTwoItemRealGitGapDBCrown runs the complete two-item authority lifecycle
// against one admitted external GapDB owner and one concrete Git repository.
// Unlike the portable semantic reference, no Git result is synthesized.
func TestTwoItemRealGitGapDBCrown(t *testing.T) {
	uninterrupted := runTwoItemRealGitGapDBCrown(t, false)
	t.Run("restart-every-durable-boundary", func(t *testing.T) {
		restarted := runTwoItemRealGitGapDBCrown(t, true)
		if err := validateTerminalRestartEquivalence(uninterrupted, restarted); err != nil {
			t.Fatalf("real-provider restart terminal semantics differ: %v\nuninterrupted=%+v\nrestarted=%+v", err, uninterrupted, restarted)
		}
	})
}

func runTwoItemRealGitGapDBCrown(t *testing.T, restartEveryBoundary bool) terminalSemanticProgress {
	t.Helper()
	driver := newRealScenarioGit(t)
	effectReconciler := &scenarioEffectReconciler{}
	h := newPortableHarness(t, ProductionConfig{
		ExecutablePath: productionExecutable(t), Seed: t.Name(),
		CandidateVerifier: gitrepo.NewHandoffVerifier(), IntegrationGit: driver, Reconciler: effectReconciler,
		ExerciseLeaseReplacement: true,
	})
	projectID, missionID := "project-two-item", "mission-two-item"
	revision, governance := twoItemRevision(t, projectID, missionID)
	missionResult := h.createMission(projectID, missionID, "operation-mission-create")
	assertUniversalInvariants(t, h, projectID, missionID, "mission-publication")
	restartAfterDurableBoundary(t, h, projectID, missionID, "mission-created", restartEveryBoundary)
	result := continueTwoItemMissionScenario(t, h, missionResult, revision, governance, effectReconciler, restartEveryBoundary)
	if result.Lifecycle != "completed" || len(result.Repository.Targets) != 1 || len(result.Repository.Publications) != 2 {
		t.Fatalf("real crown terminal evidence=%+v", result)
	}
	head := realScenarioGitCommand(t, driver.primary, "rev-parse", "refs/heads/main")
	for _, name := range []string{"item-a.txt", "item-b.txt"} {
		if got := realScenarioGitCommand(t, driver.primary, "show", head+":"+name); got != "accepted" {
			t.Fatalf("integrated %s=%q", name, got)
		}
	}
	return result
}

type realScenarioGit struct {
	mu           sync.Mutex
	repository   *gitrepo.Repository
	primary      string
	managed      string
	base         string
	calls        []string
	fences       map[journalv2.OperationID]integration.FenceFacts
	publications map[journalv2.EffectID]integration.IntegrationPublishObservationV2
	cleanups     map[journalv2.EffectID]integration.IntegrationCleanupObservationV2
	candidates   map[ids.WorkspaceID]string
	results      map[journalv2.OperationID]string
}

func newRealScenarioGit(t *testing.T) *realScenarioGit {
	t.Helper()
	root := t.TempDir()
	primary := filepath.Join(root, "repository")
	managed := filepath.Join(root, "managed")
	if err := os.Mkdir(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	realScenarioGitCommand(t, primary, "init", "-b", "main")
	realScenarioGitCommand(t, primary, "config", "user.name", "M2 Crown")
	realScenarioGitCommand(t, primary, "config", "user.email", "m2@example.invalid")
	if err := os.WriteFile(filepath.Join(primary, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	realScenarioGitCommand(t, primary, "add", "base.txt")
	realScenarioGitCommand(t, primary, "commit", "-m", "base")
	base := realScenarioGitCommand(t, primary, "rev-parse", "HEAD")
	repository, err := gitrepo.Bind(t.Context(), gitrepo.Runner{}, "repository-two-item", "project-two-item", primary, managed, "refs/heads/main", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	return &realScenarioGit{repository: repository, primary: primary, managed: managed, base: base, fences: map[journalv2.OperationID]integration.FenceFacts{}, publications: map[journalv2.EffectID]integration.IntegrationPublishObservationV2{}, cleanups: map[journalv2.EffectID]integration.IntegrationCleanupObservationV2{}, candidates: map[ids.WorkspaceID]string{}, results: map[journalv2.OperationID]string{}}
}

func (g *realScenarioGit) record(name string) {
	g.mu.Lock()
	g.calls = append(g.calls, name)
	g.mu.Unlock()
}
func (g *realScenarioGit) count() int             { g.mu.Lock(); defer g.mu.Unlock(); return len(g.calls) }
func (g *realScenarioGit) managedRoot() string    { return g.managed }
func (g *realScenarioGit) repositoryPath() string { return g.primary }
func (g *realScenarioGit) enable(repository ids.RepositoryID, target, _ string) (string, error) {
	if repository != "repository-two-item" || target != "refs/heads/main" {
		return "", fmt.Errorf("real crown repository coordinates differ")
	}
	return g.base, nil
}

func (g *realScenarioGit) prepareWorkspace(ctx context.Context, binding workspace.BindingV2) (workspace.ObservationV2, error) {
	if err := binding.Validate(); err != nil {
		return workspace.ObservationV2{}, fmt.Errorf("real crown binding: %w", err)
	}
	effects, err := gitrepo.NewWorkspaceAuthorityEffectsV2(g.repository)
	if err != nil {
		return workspace.ObservationV2{}, err
	}
	repositoryBinding := g.repository.Binding()
	expectedPath, pathErr := g.repository.WorkspacePath(binding.WorkspaceID.String())
	if pathErr != nil || binding.RepositoryID != repositoryBinding.RepositoryID || binding.ProjectID != repositoryBinding.ProjectID || binding.ManagedRoot != repositoryBinding.ManagedRoot || binding.CanonicalPath != expectedPath {
		return workspace.ObservationV2{}, fmt.Errorf("real crown binding mismatch: binding=%+v repository=%+v expected_path=%s err=%v", binding, repositoryBinding, expectedPath, pathErr)
	}
	if err := effects.CreateWorkspace(ctx, binding); err != nil {
		return workspace.ObservationV2{}, err
	}
	observed, err := effects.ObserveWorkspace(ctx, binding)
	if err != nil {
		return workspace.ObservationV2{}, err
	}
	observed.DependencyHead, observed.TargetBefore, observed.TargetAfter = observed.Head, g.base, g.base
	return observed, nil
}

func (g *realScenarioGit) createCandidate(_ context.Context, binding workspace.BindingV2, index int) (string, error) {
	name := "item-a.txt"
	if index == 1 {
		name = "item-b.txt"
	}
	if err := os.WriteFile(filepath.Join(binding.CanonicalPath, name), []byte("accepted\n"), 0o600); err != nil {
		return "", err
	}
	if _, err := runRealGit(binding.CanonicalPath, "add", name); err != nil {
		return "", err
	}
	if _, err := runRealGit(binding.CanonicalPath, "commit", "-m", "candidate "+name); err != nil {
		return "", err
	}
	candidate, err := runRealGit(binding.CanonicalPath, "rev-parse", "HEAD")
	if err == nil {
		g.mu.Lock()
		g.candidates[binding.WorkspaceID] = candidate
		g.mu.Unlock()
		if index == 1 {
			_, err = runRealGit(g.primary, "checkout", "--detach", g.base)
		}
	}
	return candidate, err
}

func (g *realScenarioGit) TargetHead(ctx context.Context, repository ids.RepositoryID, target string) (string, error) {
	g.record("target_head")
	if repository != "repository-two-item" || target != "refs/heads/main" {
		return "", fmt.Errorf("foreign repository target")
	}
	return g.repository.TargetHead(ctx)
}

func (g *realScenarioGit) InstallFence(ctx context.Context, intent integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	g.record("install_fence")
	prior, err := g.repository.IntegrationFence(ctx)
	if err != nil {
		return integration.IntegrationFenceObservationV2{}, err
	}
	token := "m2-real-fence-" + string(intent.OperationID)
	record := integration.Record{OperationID: ids.OperationID(intent.OperationID), RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef, PreflightTarget: intent.ExpectedOld, MutationGeneration: int64(intent.Generation), FencePredecessor: prior.Object, FenceRequestDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(intent)))), Token: token, LeaseDeadline: time.Now().UTC().Add(time.Hour)}
	installed, err := g.repository.InstallIntegrationFence(ctx, record, prior)
	if err != nil {
		return integration.IntegrationFenceObservationV2{}, err
	}
	g.mu.Lock()
	g.fences[intent.OperationID] = installed
	g.mu.Unlock()
	return integration.IntegrationFenceObservationV2{TokenDigest: installed.TokenDigest, Generation: uint64(installed.Generation)}, nil
}

func (g *realScenarioGit) ObserveFence(ctx context.Context, intent integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	g.record("observe_fence")
	observed, err := g.repository.IntegrationFence(ctx)
	if err != nil {
		return integration.IntegrationFenceObservationV2{}, false, err
	}
	if string(observed.OperationID) != string(intent.OperationID) || observed.Generation != int64(intent.Generation) {
		return integration.IntegrationFenceObservationV2{}, false, nil
	}
	return integration.IntegrationFenceObservationV2{TokenDigest: observed.TokenDigest, Generation: uint64(observed.Generation)}, true, nil
}

func (g *realScenarioGit) EnsureWorkspace(ctx context.Context, intent integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	g.record("ensure_workspace")
	return g.repository.EnsureIntegrationWorkspace(ctx, string(intent.OperationID), intent.ExpectedOld, intent.Candidate)
}

func (g *realScenarioGit) MergeCandidate(ctx context.Context, intent integration.IntegrationMergeIntentV2, facts integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	g.record("merge_candidate")
	result, conflict, err := g.repository.IntegrateCandidate(ctx, facts, intent.ExpectedOld, intent.Candidate)
	if err == nil {
		g.mu.Lock()
		g.results[intent.OperationID] = result
		g.mu.Unlock()
	}
	return result, conflict, err
}

func (g *realScenarioGit) CompareAndSwapTarget(ctx context.Context, intent integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	g.record("publish_target")
	g.mu.Lock()
	fence, ok := g.fences[intent.OperationID]
	g.mu.Unlock()
	if !ok || fence.TokenDigest != intent.FenceTokenHash {
		return integration.IntegrationPublishObservationV2{}, fmt.Errorf("missing exact real Git fence")
	}
	record := integration.Record{OperationID: ids.OperationID(intent.OperationID), FenceRef: fence.Ref, FenceObject: fence.Object}
	if err := g.repository.PublishIntegrationTarget(ctx, record, intent.ExpectedOld, intent.Result, nil); err != nil {
		return integration.IntegrationPublishObservationV2{}, err
	}
	observation := integration.IntegrationPublishObservationV2{EffectID: intent.EffectID, OperationID: intent.OperationID, RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef, ExpectedOld: intent.ExpectedOld, Result: intent.Result, ObservedTarget: intent.Result, Generation: intent.Generation, FenceTokenHash: intent.FenceTokenHash, ProviderMarker: "git-ref:" + intent.Result}
	g.mu.Lock()
	g.publications[intent.EffectID] = observation
	g.mu.Unlock()
	return observation, nil
}

func (g *realScenarioGit) ObserveTargetPublication(ctx context.Context, intent integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	g.record("observe_target")
	g.mu.Lock()
	observation, ok := g.publications[intent.EffectID]
	g.mu.Unlock()
	if !ok {
		return observation, false, nil
	}
	head, err := g.repository.TargetHead(ctx)
	return observation, err == nil && head == intent.Result, err
}

func (g *realScenarioGit) CleanupWorkspace(_ context.Context, intent integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	g.record("cleanup_workspace")
	observation := integration.IntegrationCleanupObservationV2{EffectID: intent.EffectID, OperationID: intent.OperationID, AttemptID: intent.AttemptID, RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef, WorkspacePath: intent.WorkspacePath, Generation: intent.Generation, FenceTokenHash: intent.FenceTokenHash, ProviderMarker: "git-cleanup:" + intent.AttemptID}
	g.mu.Lock()
	g.cleanups[intent.EffectID] = observation
	g.mu.Unlock()
	return observation, nil
}

func (g *realScenarioGit) ObserveCleanup(_ context.Context, intent integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	g.record("observe_cleanup")
	g.mu.Lock()
	defer g.mu.Unlock()
	observation, ok := g.cleanups[intent.EffectID]
	return observation, ok, nil
}

func (g *realScenarioGit) IsAncestor(ctx context.Context, repository ids.RepositoryID, ancestor, current string) (bool, error) {
	g.record("is_ancestor")
	if repository != "repository-two-item" {
		return false, fmt.Errorf("foreign repository")
	}
	return g.repository.IsAncestor(ctx, ancestor, current)
}

func (g *realScenarioGit) terminalEvidence() repositoryTerminalEvidence {
	g.mu.Lock()
	defer g.mu.Unlock()
	evidence := repositoryTerminalEvidence{Targets: []string{"repository-two-item\x00refs/heads/main\x00"}}
	head, _ := runRealGit(g.primary, "rev-parse", "refs/heads/main")
	evidence.Targets[0] += head
	for effect, observation := range g.publications {
		evidence.Publications = append(evidence.Publications, string(effect)+"\x00"+observation.ExpectedOld+"\x00"+observation.Result+"\x00"+observation.ObservedTarget+"\x00"+observation.ProviderMarker)
	}
	sort.Strings(evidence.Publications)
	return evidence
}

func runRealGit(directory string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=M2 Crown", "GIT_AUTHOR_EMAIL=m2@example.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=M2 Crown", "GIT_COMMITTER_EMAIL=m2@example.invalid", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func realScenarioGitCommand(t *testing.T, directory string, args ...string) string {
	t.Helper()
	output, err := runRealGit(directory, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

var _ scenarioGit = (*realScenarioGit)(nil)
var _ handoff.CandidateVerifier = gitrepo.NewHandoffVerifier()
