// Package scenario qualifies the production authority host only through its
// typed command, progress, readiness, and shutdown contracts.
package scenario

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	sidecar "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

const productionScenarioSchemaVersion uint32 = 1

// ProductionConfig makes every external input to a scenario explicit. Tests
// may obtain the executable from an environment variable, but the harness does
// not consult process-global configuration.
type ProductionConfig struct {
	ExecutablePath    string
	RepositoryRoot    string
	Seed              string
	CheckpointEvery   int
	CandidateVerifier handoff.CandidateVerifier
	Reconciler        productionruntime.Reconciler
	IntegrationGit    scenarioGit
	// ExerciseLeaseReplacement enables the crown-only stale-writer sequence.
	// It is explicit so ordinary portable scenarios retain their established
	// topology while the integrated qualification proves the stronger law.
	ExerciseLeaseReplacement bool
	// MutationObserver is qualification-only instrumentation around accepted
	// production Host calls. It receives the exact event families the typed
	// capability plan publishes; ordinary scenarios leave it nil.
	MutationObserver func(MutationObservation)
	// PublicationBoundaryObserver is qualification-only, observer-only
	// instrumentation used to capture the exact sealed production plans.
	PublicationBoundaryObserver journalv2.PublicationBoundaryObserverV2
	// AfterEffectIntent is qualification-only orchestration around a durable
	// effect intent. Returning true means the hook appended authority state
	// outside the live Host projection, so the scenario restarts and adopts the
	// effect before continuing. Ordinary scenarios leave it nil.
	AfterEffectIntent func(context.Context, ids.WorkID) (bool, error)
}

// MutationObservation is one acknowledged typed Host call. Record revision
// movement is retained as evidence about the complete sealed batch; it is not
// mistaken for an operation count because a single authority call may append
// several records atomically.
type MutationObservation struct {
	Families                 []string
	OperationID              string
	Duration                 time.Duration
	Err                      error
	BeforeRecordRevision     uint64
	AfterRecordRevision      uint64
	MutationCount            uint64
	ProjectedThroughRevision uint64
}

// scenarioGit is the narrow test-harness composition seam. Production still
// consumes only IntegrationGitPortV2; the additional methods let the crown
// prove that workspace and candidate facts came from the same repository.
type scenarioGit interface {
	integration.IntegrationGitPortV2
	enable(ids.RepositoryID, string, string) (string, error)
	managedRoot() string
	repositoryPath() string
	prepareWorkspace(context.Context, workspace.BindingV2) (workspace.ObservationV2, error)
	createCandidate(context.Context, workspace.BindingV2, int) (string, error)
	count() int
	terminalEvidence() repositoryTerminalEvidence
}

type portableHarness struct {
	t                   *testing.T
	config              ProductionConfig
	root                string
	runtime             string
	data                string
	host                *productionruntime.Host
	epochs              []journalv2.RuntimeEpochID
	starts              int
	git                 scenarioGit
	crownClockOffset    time.Duration
	observationRevision uint64
}

// absentCapabilityReadiness is an explicit, source-bound absence observer for
// scenario missions that have not yet created capability aggregates. It does
// not manufacture a census; the production host invokes it once per declared
// capability at the exact GapDB capture and seals the complete observation set.
type absentCapabilityReadiness struct{}

func (absentCapabilityReadiness) ObserveCapabilityReadiness(_ context.Context, query mission.CapabilityReadinessQueryV2) (mission.CapabilityReadinessEvidenceV2, error) {
	return mission.CapabilityReadinessEvidenceV2{
		Capture: query.Capture(), ProjectID: query.ProjectID(), MissionID: query.MissionID(), Action: query.Action(),
		ObservedAbsent: true, EvidenceIdentity: "scenario-absence-" + string(query.Action()), EvidenceDigest: strings.Repeat("a", 64),
	}, nil
}

// unavailableDecomposition is the scenario's deterministic external-provider
// boundary. It advertises the contract but never grants tracker authority or
// turns an unavailable mutation into success.
type unavailableDecomposition struct{}

func (unavailableDecomposition) RequireCapabilities(context.Context, []mission.DecompositionEffectKindV2) error {
	return nil
}
func (unavailableDecomposition) ObserveEffect(context.Context, journalv2.EffectID) (mission.DecompositionProviderObservationV2, bool, error) {
	return mission.DecompositionProviderObservationV2{}, false, nil
}
func (unavailableDecomposition) MutateEffect(context.Context, mission.DecompositionProviderMutationV2) (mission.DecompositionProviderMutationOutcomeV2, error) {
	return mission.DecompositionProviderMutationRefusedV2, errors.New("scenario decomposition provider unavailable")
}
func (unavailableDecomposition) CompensateEffect(context.Context, mission.DecompositionProviderMutationV2, mission.DecompositionProviderObservationV2) error {
	return errors.New("scenario decomposition provider unavailable")
}

func newPortableHarness(t *testing.T, config ProductionConfig) *portableHarness {
	t.Helper()
	if config.ExecutablePath == "" || !filepath.IsAbs(config.ExecutablePath) {
		t.Fatal("portable scenario requires an explicit absolute production executable")
	}
	if config.RepositoryRoot == "" {
		config.RepositoryRoot = repositoryRoot(t)
	}
	if config.Seed == "" {
		config.Seed = t.Name()
	}
	root, err := os.MkdirTemp("/tmp", "spk-scenario-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	git := config.IntegrationGit
	if git == nil {
		git = newAttributableGit()
	}
	h := &portableHarness{
		t: t, config: config, root: root, runtime: runtimeRoot,
		data: filepath.Join(root, "authority"), git: git,
	}
	h.start()
	t.Cleanup(func() {
		h.close()
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove scenario root: %v", err)
		}
	})
	return h
}

func (h *portableHarness) start() {
	h.t.Helper()
	h.starts++
	verifier := h.config.CandidateVerifier
	if verifier == nil {
		verifier = closedCandidateVerifier{}
	}
	config := productionruntime.Config{
		ControlRoot:                          h.data,
		HostAuthorityID:                      journalv2.HostAuthorityID("host-" + shortDigest(h.config.Seed)),
		AuthorityBindingOperationID:          "bind-" + shortDigest(h.config.Seed),
		M1AdoptionRecordPath:                 filepath.Join(h.config.RepositoryRoot, "docs", "programs", "evidence", "m1-adoption.json"),
		AssertionExtensionAdoptionRecordPath: filepath.Join(h.config.RepositoryRoot, "docs", "programs", "evidence", "m1-assertion-extension-adoption.json"),
		Sidecar: sidecar.LifecycleConfig{
			ExecutablePath:      h.config.ExecutablePath,
			AdmissionRecordPath: filepath.Join(h.config.RepositoryRoot, "docs", "programs", "evidence", "m1-assertion-extension-admission.json"),
			RuntimeRoot:         h.runtime,
			DataDirectory:       h.data,
			SupervisorRunID:     fmt.Sprintf("scenario-%s-%d", shortDigest(h.config.Seed), h.starts),
			ProductPolicySHA256: strings.Repeat("e", 64),
		},
		CapabilityReadiness:         absentCapabilityReadiness{},
		DecompositionProvider:       unavailableDecomposition{},
		CandidateVerifier:           verifier,
		IntegrationGit:              h.git,
		Reconciler:                  h.config.Reconciler,
		CheckpointEvery:             h.config.CheckpointEvery,
		MonitorInterval:             25 * time.Millisecond,
		PublicationBoundaryObserver: h.config.PublicationBoundaryObserver,
	}
	started := time.Now()
	host, err := productionruntime.Start(h.t.Context(), config)
	if err != nil {
		h.t.Fatalf("start production authority host: %v", err)
	}
	status := host.Status()
	if status.SchemaVersion != productionScenarioSchemaVersion || status.State != productionruntime.HostReady || status.DatabaseID == "" || status.RuntimeEpochID == "" || status.ProjectionRevision == 0 {
		_ = host.Close(context.Background())
		h.t.Fatalf("incomplete typed readiness: %+v", status)
	}
	h.host = host
	h.observeMutation([]string{"runtime_host", "operation"}, fmt.Sprintf("runtime-host-start-%d", h.starts), started, err)
	h.epochs = append(h.epochs, status.RuntimeEpochID)
}

func (h *portableHarness) observeMutation(families []string, operation string, started time.Time, err error) {
	if h != nil && h.config.MutationObserver != nil {
		after := uint64(0)
		if h.host != nil {
			after = h.host.LiveProjectionCursor().Revision
		}
		before := h.observationRevision
		mutationCount := uint64(0)
		if after >= before {
			mutationCount = after - before
		}
		h.config.MutationObserver(MutationObservation{
			Families: append([]string(nil), families...), OperationID: operation,
			Duration: time.Since(started), Err: err,
			BeforeRecordRevision: before, AfterRecordRevision: after,
			MutationCount: mutationCount, ProjectedThroughRevision: after,
		})
		if err == nil {
			h.observationRevision = after
		}
	}
}

func (h *portableHarness) restart() (journalv2.RuntimeEpochID, journalv2.RuntimeEpochID) {
	h.t.Helper()
	prior := h.host.RuntimeEpochID()
	if err := h.host.Close(h.t.Context()); err != nil {
		h.t.Fatalf("close production authority host: %v", err)
	}
	h.host = nil
	h.start()
	current := h.host.RuntimeEpochID()
	if current == prior {
		h.t.Fatalf("restart reused runtime epoch %q", current)
	}
	return prior, current
}

func (h *portableHarness) close() {
	if h.host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.host.Close(ctx); err != nil {
		h.t.Errorf("close production authority host: %v", err)
	}
	h.host = nil
}

func (h *portableHarness) createMission(project, missionID, operation string) mission.ReadinessAuthorityResultV2 {
	h.t.Helper()
	now := time.Date(2026, 9, 5, 12, 0, h.starts, 0, time.UTC)
	digest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"portable-scenario"}`))
	if err != nil {
		h.t.Fatal(err)
	}
	state, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: journalv2.ProjectID(project), MissionID: journalv2.MissionID(missionID),
		DefinitionRevision: "definition-1", DefinitionDigest: digest,
		Version: 1, Lifecycle: mission.MissionOpen,
		LastOperationID: journalv2.OperationID(operation), ObservedAt: now.Format(time.RFC3339),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	started := time.Now()
	result, err := h.host.ExecuteReadiness(h.t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: journalv2.OperationID(operation),
		ProjectID: journalv2.ProjectID(project), MissionID: journalv2.MissionID(missionID),
		ActorID: "actor-scenario", ProcessID: "process-scenario",
		OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: "doctrine-1",
		ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: now.Format(time.RFC3339),
		EventType: mission.ReadinessEventCreatedV2, Updated: state,
	})
	h.observeMutation([]string{"mission", "operation"}, operation, started, err)
	if err != nil {
		h.t.Fatalf("create mission %s: %v", missionID, err)
	}
	if result.PublicationID() == "" {
		h.t.Fatalf("create mission %s returned no publication", missionID)
	}
	return result
}

// recordMissionActivity supplies a real, reducer-validated mutation against a
// bounded mission census. Performance qualification can therefore observe
// 10,000 unique accepted operations without exceeding the frozen aggregate
// budget by manufacturing 10,000 throwaway missions.
func (h *portableHarness) recordMissionActivity(prior mission.ReadinessAuthorityResultV2, operation string, sequence int) mission.ReadinessAuthorityResultV2 {
	h.t.Helper()
	previous := prior.State()
	definitionDigest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"portable-scenario"}`))
	if err != nil {
		h.t.Fatal(err)
	}
	observedAt := time.Date(2026, 9, 5, 12, 1, h.starts, 0, time.UTC).Format(time.RFC3339)
	next, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: previous.ProjectID(), MissionID: previous.MissionID(), DefinitionRevision: "definition-1", DefinitionDigest: definitionDigest,
		Version: previous.Version() + 1, Lifecycle: previous.Lifecycle(), CurrentWorkRevision: previous.CurrentWorkRevision(), ActivityCount: previous.ActivityCount() + 1,
		ActiveFacts: previous.ActiveFacts(), LastOperationID: journalv2.OperationID(operation), ObservedAt: observedAt,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	resultDigest, err := journalv2.ReducerStateDigestV2([]byte(fmt.Sprintf(`{"sequence":%d}`, sequence)))
	if err != nil {
		h.t.Fatal(err)
	}
	started := time.Now()
	result, err := h.host.ExecuteReadiness(h.t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: journalv2.OperationID(operation), ProjectID: previous.ProjectID(), MissionID: previous.MissionID(),
		ActorID: "actor-scenario", ProcessID: "process-scenario", OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: observedAt, EventType: mission.ReadinessEventActivityRecordedV2, Expected: previous, Updated: next,
		Activity: &mission.MissionActivityV2{ActivityID: fmt.Sprintf("activity-performance-%05d", sequence), Action: mission.ActionRef{ID: mission.ActionArtifactPublish, Scope: mission.AuthorityScope{Kind: mission.ScopeArtifact, Identity: "performance"}}, Outcome: "succeeded", ResultDigest: resultDigest.String()},
	})
	h.observeMutation([]string{"mission", "operation"}, operation, started, err)
	if err != nil {
		h.t.Fatalf("record mission activity %s: %v", previous.MissionID(), err)
	}
	if result.PublicationID() == "" {
		h.t.Fatalf("record mission activity %s returned no publication", previous.MissionID())
	}
	return result
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate scenario source")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func shortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:8])
}

func productionExecutable(t *testing.T) string {
	t.Helper()
	path := os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE")
	if path == "" {
		path = os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	}
	if path == "" {
		t.Skip("set SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE to run production-host scenarios")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// closedCandidateVerifier is intentionally incapable of inventing repository
// evidence. Scenarios that need a candidate provide a source-bound verifier.
type closedCandidateVerifier struct{}

func (closedCandidateVerifier) Verify(context.Context, handoff.AuthorityScope, string) (handoff.CandidateEvidence, error) {
	return handoff.CandidateEvidence{}, errors.New("candidate evidence was not configured")
}
func (closedCandidateVerifier) RevalidateRefusal(context.Context, handoff.Refusal) error { return nil }

// sourceBoundCandidateVerifier returns only the immutable facts present in the
// authority scope and exact candidate supplied by the production command. It
// is deliberately deterministic: scenarios test the kernel protocol, not a
// particular Git executable's output format.
type sourceBoundCandidateVerifier struct{}

func (sourceBoundCandidateVerifier) Verify(_ context.Context, scope handoff.AuthorityScope, candidate string) (handoff.CandidateEvidence, error) {
	return handoff.CandidateEvidence{
		Candidate: candidate, PinnedBase: scope.PinnedBase,
		OwnedBranch: scope.OwnedBranch, ObservedBranchHead: candidate,
	}, nil
}
func (sourceBoundCandidateVerifier) RevalidateRefusal(context.Context, handoff.Refusal) error {
	return nil
}

type attributableGit struct {
	mu           sync.Mutex
	calls        []string
	enabled      bool
	targets      map[string]string
	fences       map[journalv2.OperationID]integration.IntegrationFenceObservationV2
	publications map[journalv2.EffectID]integration.IntegrationPublishObservationV2
	cleanups     map[journalv2.EffectID]integration.IntegrationCleanupObservationV2
}

type repositoryTerminalEvidence struct {
	Targets      []string
	Publications []string
}

func newAttributableGit() *attributableGit { return &attributableGit{} }
func integrationTargetKey(repository ids.RepositoryID, target string) string {
	return repository.String() + "\x00" + target
}
func (g *attributableGit) enable(repository ids.RepositoryID, target, head string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = true
	g.targets = map[string]string{integrationTargetKey(repository, target): head}
	g.fences = make(map[journalv2.OperationID]integration.IntegrationFenceObservationV2)
	g.publications = make(map[journalv2.EffectID]integration.IntegrationPublishObservationV2)
	g.cleanups = make(map[journalv2.EffectID]integration.IntegrationCleanupObservationV2)
	return head, nil
}
func (g *attributableGit) managedRoot() string    { return "" }
func (g *attributableGit) repositoryPath() string { return "" }
func (g *attributableGit) prepareWorkspace(_ context.Context, binding workspace.BindingV2) (workspace.ObservationV2, error) {
	return workspace.ObservationV2{Registered: true, Head: binding.PinnedBase, Branch: binding.OwnedBranch, GitDirIdentity: "gitdir-" + binding.WorkID.String(), IndexIdentity: "index-" + binding.WorkID.String(), Clean: true, DependencyHead: binding.PinnedBase, TargetBefore: binding.PinnedBase, TargetAfter: binding.PinnedBase}, nil
}
func (g *attributableGit) createCandidate(_ context.Context, _ workspace.BindingV2, index int) (string, error) {
	return fmt.Sprintf("%040x", index+11), nil
}
func (g *attributableGit) record(name string) {
	g.mu.Lock()
	g.calls = append(g.calls, name)
	g.mu.Unlock()
}
func (g *attributableGit) count() int { g.mu.Lock(); defer g.mu.Unlock(); return len(g.calls) }
func (g *attributableGit) terminalEvidence() repositoryTerminalEvidence {
	g.mu.Lock()
	defer g.mu.Unlock()
	evidence := repositoryTerminalEvidence{}
	for key, head := range g.targets {
		evidence.Targets = append(evidence.Targets, key+"\x00"+head)
	}
	for effectID, observation := range g.publications {
		evidence.Publications = append(evidence.Publications, string(effectID)+"\x00"+observation.ExpectedOld+"\x00"+observation.Result+"\x00"+observation.ObservedTarget+"\x00"+observation.ProviderMarker)
	}
	sort.Strings(evidence.Targets)
	sort.Strings(evidence.Publications)
	return evidence
}
func (g *attributableGit) TargetHead(_ context.Context, repository ids.RepositoryID, target string) (string, error) {
	g.record("target_head")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return "", errors.New("repository evidence not configured")
	}
	return g.targets[integrationTargetKey(repository, target)], nil
}
func (g *attributableGit) InstallFence(_ context.Context, intent integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	g.record("install_fence")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return integration.IntegrationFenceObservationV2{}, errors.New("repository evidence not configured")
	}
	observation := integration.IntegrationFenceObservationV2{TokenDigest: strings.Repeat("a", 64), Generation: intent.Generation}
	g.fences[intent.OperationID] = observation
	return observation, nil
}
func (g *attributableGit) ObserveFence(_ context.Context, intent integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	g.record("observe_fence")
	g.mu.Lock()
	defer g.mu.Unlock()
	observation, found := g.fences[intent.OperationID]
	return observation, found, nil
}
func (g *attributableGit) EnsureWorkspace(_ context.Context, intent integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	g.record("ensure_workspace")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return integration.WorkspaceFacts{}, errors.New("repository evidence not configured")
	}
	return integration.WorkspaceFacts{Path: "/managed/scenario-integration", Registered: true, Detached: true, Clean: true, Head: intent.ExpectedOld, CandidateReachable: true}, nil
}
func (g *attributableGit) MergeCandidate(_ context.Context, intent integration.IntegrationMergeIntentV2, _ integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	g.record("merge_candidate")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return "", nil, errors.New("repository evidence not configured")
	}
	// A deterministic merge result remains source-bound to both inputs while
	// satisfying the Git object-ID contract.
	sum := sha256.Sum256([]byte(intent.ExpectedOld + "\x00" + intent.Candidate))
	return fmt.Sprintf("%x", sum[:20]), nil, nil
}
func (g *attributableGit) CompareAndSwapTarget(_ context.Context, intent integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	g.record("publish_target")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled || g.targets[integrationTargetKey(intent.RepositoryID, intent.TargetRef)] != intent.ExpectedOld {
		return integration.IntegrationPublishObservationV2{}, errors.New("target moved")
	}
	g.targets[integrationTargetKey(intent.RepositoryID, intent.TargetRef)] = intent.Result
	observation := integration.IntegrationPublishObservationV2{
		EffectID: intent.EffectID, OperationID: intent.OperationID,
		RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef,
		ExpectedOld: intent.ExpectedOld, Result: intent.Result, ObservedTarget: intent.Result,
		Generation: intent.Generation, FenceTokenHash: intent.FenceTokenHash,
		ProviderMarker: "scenario-ref:" + string(intent.EffectID),
	}
	g.publications[intent.EffectID] = observation
	return observation, nil
}
func (g *attributableGit) ObserveTargetPublication(_ context.Context, intent integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	g.record("observe_target")
	g.mu.Lock()
	defer g.mu.Unlock()
	observation, found := g.publications[intent.EffectID]
	return observation, found, nil
}
func (g *attributableGit) CleanupWorkspace(_ context.Context, intent integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	g.record("cleanup_workspace")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return integration.IntegrationCleanupObservationV2{}, errors.New("repository evidence not configured")
	}
	observation := integration.IntegrationCleanupObservationV2{
		EffectID: intent.EffectID, OperationID: intent.OperationID, AttemptID: intent.AttemptID,
		RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef, WorkspacePath: intent.WorkspacePath,
		Generation: intent.Generation, FenceTokenHash: intent.FenceTokenHash,
		ProviderMarker: "scenario-cleanup:" + string(intent.EffectID),
	}
	g.cleanups[intent.EffectID] = observation
	return observation, nil
}
func (g *attributableGit) ObserveCleanup(_ context.Context, intent integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	g.record("observe_cleanup")
	g.mu.Lock()
	defer g.mu.Unlock()
	observation, found := g.cleanups[intent.EffectID]
	return observation, found, nil
}
func (g *attributableGit) IsAncestor(_ context.Context, _ ids.RepositoryID, ancestor, current string) (bool, error) {
	g.record("is_ancestor")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return false, errors.New("repository evidence not configured")
	}
	return ancestor == current, nil
}
