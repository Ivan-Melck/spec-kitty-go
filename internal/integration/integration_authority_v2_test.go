package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type providerJSONCauseV2 struct {
	secret       string
	marshalCalls int
}

func (e *providerJSONCauseV2) Error() string { return e.secret }

func (e *providerJSONCauseV2) MarshalJSON() ([]byte, error) {
	e.marshalCalls++
	return json.Marshal(map[string]string{"provider_secret": e.secret})
}

type integrationPortV2 struct {
	mu          sync.Mutex
	operations  map[journalv2.OperationID]IntegrationOperationResolutionV2
	targets     map[journalv2.AggregateCoordinate]IntegrationTargetStateV2
	targetRevs  map[journalv2.AggregateCoordinate]uint64
	attempts    map[journalv2.AggregateCoordinate]IntegrationAttemptStateV2
	attemptRevs map[journalv2.AggregateCoordinate]uint64
	authority   IntegrationAuthorityObservationV2
	plans       []journalv2.SealedCommitPlanV2
	revision    uint64
	dropAckAt   int
}

func newIntegrationPortV2(authority IntegrationAuthorityObservationV2) *integrationPortV2 {
	return &integrationPortV2{operations: map[journalv2.OperationID]IntegrationOperationResolutionV2{}, targets: map[journalv2.AggregateCoordinate]IntegrationTargetStateV2{}, targetRevs: map[journalv2.AggregateCoordinate]uint64{}, attempts: map[journalv2.AggregateCoordinate]IntegrationAttemptStateV2{}, attemptRevs: map[journalv2.AggregateCoordinate]uint64{}, authority: authority}
}
func (p *integrationPortV2) ResolveIntegrationOperation(_ context.Context, binding journalv2.OperationBindingV2) (IntegrationOperationResolutionV2, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if result, ok := p.operations[binding.OperationID()]; ok {
		return result, nil
	}
	return IntegrationOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, nil
}
func (p *integrationPortV2) ObserveIntegration(_ context.Context, target, attempt journalv2.AggregateCoordinate) (IntegrationObservationV2, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := IntegrationObservationV2{}
	if state, ok := p.targets[target]; ok {
		copyState := state
		result.Target = &copyState
		result.TargetRevision = p.targetRevs[target]
	}
	if state, ok := p.attempts[attempt]; ok {
		copyState := state
		result.Attempt = &copyState
		result.AttemptRevision = p.attemptRevs[attempt]
	}
	return result, nil
}
func (p *integrationPortV2) ObserveIntegrationAuthority(context.Context, IntegrationAuthorityRequestV2) (IntegrationAuthorityObservationV2, error) {
	return p.authority, nil
}
func (p *integrationPortV2) PublishIntegration(_ context.Context, capability IntegrationPlanV2) (IntegrationAckV2, error) {
	sealed, err := capability.Publication()
	if err != nil {
		return IntegrationAckV2{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, head := range sealed.Heads() {
		if head.Coordinate.Kind == "integration_target" && p.targetRevs[head.Coordinate] != head.ExpectedRecordRevision {
			return IntegrationAckV2{}, fmt.Errorf("target compare failed")
		}
		if head.Coordinate.Kind == "integration_attempt" && p.attemptRevs[head.Coordinate] != head.ExpectedRecordRevision {
			return IntegrationAckV2{}, fmt.Errorf("attempt compare failed")
		}
	}
	p.revision++
	for _, event := range sealed.Events() {
		switch event.Family() {
		case "integration_target":
			var prior []byte
			if state, ok := p.targets[event.Coordinate()]; ok {
				prior = state.CanonicalBytes()
			}
			next, reduceErr := ReduceIntegrationTargetEventV2(prior, event)
			if reduceErr != nil {
				return IntegrationAckV2{}, reduceErr
			}
			state, decodeErr := DecodeIntegrationTargetStateV2(next)
			if decodeErr != nil {
				return IntegrationAckV2{}, decodeErr
			}
			p.targets[event.Coordinate()] = state
			p.targetRevs[event.Coordinate()] = p.revision
		case "integration_attempt":
			var prior []byte
			if state, ok := p.attempts[event.Coordinate()]; ok {
				prior = state.CanonicalBytes()
			}
			next, reduceErr := ReduceIntegrationAttemptEventV2(prior, event)
			if reduceErr != nil {
				return IntegrationAckV2{}, reduceErr
			}
			state, decodeErr := DecodeIntegrationAttemptStateV2(next)
			if decodeErr != nil {
				return IntegrationAckV2{}, decodeErr
			}
			p.attempts[event.Coordinate()] = state
			p.attemptRevs[event.Coordinate()] = p.revision
		}
	}
	p.operations[sealed.OperationState().OperationID()] = IntegrationOperationResolutionV2{Kind: AcceptanceOperationExactV2, State: sealed.OperationState(), RecordRevision: p.revision}
	p.plans = append(p.plans, sealed)
	if p.dropAckAt == len(p.plans) {
		return IntegrationAckV2{}, fmt.Errorf("lost acknowledgement")
	}
	return IntegrationAckV2{sealed.PublicationID(), p.revision, sealed.Digest(), sealed.MutationCount()}, nil
}
func (p *integrationPortV2) ReconcileIntegration(_ context.Context, capability IntegrationPlanV2) (IntegrationAckV2, error) {
	sealed, _ := capability.Publication()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.plans) == 0 || p.plans[len(p.plans)-1].PublicationID() != sealed.PublicationID() {
		return IntegrationAckV2{}, errors.New("publication absent")
	}
	return IntegrationAckV2{sealed.PublicationID(), p.revision, sealed.Digest(), sealed.MutationCount()}, nil
}

type integrationGitV2 struct {
	mu                  sync.Mutex
	targets             map[string]string
	result              string
	conflict            bool
	lostPublish         bool
	stealPublish        bool
	advanceAfterPublish bool
	ancestralResult     bool
	corruptPublication  bool
	blockFence          chan struct{}
	fenceEntered        chan struct{}
	fences              map[journalv2.OperationID]IntegrationFenceObservationV2
	publications        map[journalv2.EffectID]IntegrationPublishObservationV2
	cleanups            map[journalv2.EffectID]IntegrationCleanupObservationV2
	lostFence           bool
	lostCleanup         bool
	calls               int
	cleanupErr          error
}

func (g *integrationGitV2) key(repository string, target string) string {
	return repository + "\x00" + target
}

// Concrete methods below intentionally expose only typed observations; no
// command output or prose crosses this port.
func (g *integrationGitV2) InstallFence(_ context.Context, intent IntegrationFenceIntentV2) (IntegrationFenceObservationV2, error) {
	g.mu.Lock()
	g.calls++
	block := g.blockFence
	entered := g.fenceEntered
	g.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if block != nil {
		<-block
	}
	observation := IntegrationFenceObservationV2{TokenDigest: strings.Repeat("a", 64), Generation: intent.Generation}
	g.mu.Lock()
	if g.fences == nil {
		g.fences = map[journalv2.OperationID]IntegrationFenceObservationV2{}
	}
	g.fences[intent.OperationID] = observation
	lost := g.lostFence
	g.lostFence = false
	g.mu.Unlock()
	if lost {
		return IntegrationFenceObservationV2{}, errors.New("lost fence acknowledgement")
	}
	return observation, nil
}
func (g *integrationGitV2) ObserveFence(_ context.Context, intent IntegrationFenceIntentV2) (IntegrationFenceObservationV2, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	observation, found := g.fences[intent.OperationID]
	return observation, found, nil
}
func (g *integrationGitV2) EnsureWorkspace(_ context.Context, intent IntegrationWorkspaceIntentV2) (WorkspaceFacts, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return WorkspaceFacts{Path: "/managed/integration", Registered: true, Detached: true, Clean: true, Head: intent.ExpectedOld, CandidateReachable: true}, nil
}
func (g *integrationGitV2) MergeCandidate(_ context.Context, intent IntegrationMergeIntentV2, workspace WorkspaceFacts) (string, *ConflictEvidence, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.conflict {
		return "", &ConflictEvidence{Base: intent.ExpectedOld, Candidate: intent.Candidate, Workspace: workspace.Path, ConflictFiles: []string{"conflict.go"}}, errors.New("merge conflict")
	}
	return g.result, nil, nil
}
func (g *integrationGitV2) CompareAndSwapTarget(_ context.Context, intent IntegrationPublishIntentV2) (IntegrationPublishObservationV2, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	key := g.key(intent.RepositoryID.String(), intent.TargetRef)
	if g.stealPublish {
		g.targets[key] = strings.Repeat("d", 40)
		g.stealPublish = false
		return IntegrationPublishObservationV2{}, errors.New("target moved by another actor")
	}
	if g.targets[key] != intent.ExpectedOld {
		return IntegrationPublishObservationV2{}, errors.New("target moved")
	}
	g.targets[key] = intent.Result
	observation := IntegrationPublishObservationV2{EffectID: intent.EffectID, OperationID: intent.OperationID, RepositoryID: intent.RepositoryID, TargetRef: intent.TargetRef, ExpectedOld: intent.ExpectedOld, Result: intent.Result, ObservedTarget: intent.Result, Generation: intent.Generation, FenceTokenHash: intent.FenceTokenHash, ProviderMarker: "git-ref-effect:" + string(intent.EffectID)}
	if g.publications == nil {
		g.publications = map[journalv2.EffectID]IntegrationPublishObservationV2{}
	}
	g.publications[intent.EffectID] = observation
	if g.corruptPublication {
		observation.EffectID = "effect-other"
	}
	if g.advanceAfterPublish {
		g.targets[key] = strings.Repeat("d", 40)
	}
	if g.lostPublish {
		g.lostPublish = false
		return IntegrationPublishObservationV2{}, errors.New("lost response")
	}
	return observation, nil
}
func (g *integrationGitV2) ObserveTargetPublication(_ context.Context, intent IntegrationPublishIntentV2) (IntegrationPublishObservationV2, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	observation, found := g.publications[intent.EffectID]
	return observation, found, nil
}
func (g *integrationGitV2) CleanupWorkspace(_ context.Context, intent IntegrationCleanupIntentV2) (IntegrationCleanupObservationV2, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.cleanupErr != nil {
		return IntegrationCleanupObservationV2{}, g.cleanupErr
	}
	observation := IntegrationCleanupObservationV2{
		EffectID:       intent.EffectID,
		OperationID:    intent.OperationID,
		AttemptID:      intent.AttemptID,
		RepositoryID:   intent.RepositoryID,
		TargetRef:      intent.TargetRef,
		WorkspacePath:  intent.WorkspacePath,
		Generation:     intent.Generation,
		FenceTokenHash: intent.FenceTokenHash,
		ProviderMarker: "git-cleanup-effect:" + string(intent.EffectID),
	}
	if g.cleanups == nil {
		g.cleanups = map[journalv2.EffectID]IntegrationCleanupObservationV2{}
	}
	g.cleanups[intent.EffectID] = observation
	if g.lostCleanup {
		g.lostCleanup = false
		return IntegrationCleanupObservationV2{}, errors.New("lost cleanup response")
	}
	return observation, nil
}

func (g *integrationGitV2) ObserveCleanup(_ context.Context, intent IntegrationCleanupIntentV2) (IntegrationCleanupObservationV2, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	observation, found := g.cleanups[intent.EffectID]
	return observation, found, nil
}

func TestIntegrationAuthorityStagesIntentBeforeEffectsAndReplaysV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	port.dropAckAt = 1
	git := newIntegrationGitFixtureV2(request)
	git.cleanupErr = errors.New("cleanup runner unavailable")
	service, err := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Integrate(t.Context(), request)
	if err != nil || result.Stage != IntegrationAttemptCompletedV2 || len(port.plans) != 5 || git.calls != 7 {
		t.Fatalf("result=%+v error=%v plans=%d calls=%d", result, err, len(port.plans), git.calls)
	}
	for _, plan := range port.plans {
		if len(plan.EligibilityGuards()) != 1 || len(plan.Assertions()) != 1 {
			t.Fatalf("authority proof omitted: guards=%d assertions=%d", len(plan.EligibilityGuards()), len(plan.Assertions()))
		}
	}
	gitCalls := git.calls
	replay, err := service.Integrate(t.Context(), request)
	if err != nil || replay.Stage != IntegrationAttemptCompletedV2 || git.calls != gitCalls || len(port.plans) != 5 {
		t.Fatalf("replay=%+v error=%v plans=%d calls=%d", replay, err, len(port.plans), git.calls)
	}
}

func TestIntegrationRefusesBeforeAnyRepositoryObservationV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	authority.Acceptance.State = nil
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Integrate(t.Context(), request); !IsKind(err, KindNotApproved) || git.calls != 0 || len(port.plans) != 0 {
		t.Fatalf("error=%v git=%d plans=%d", err, git.calls, len(port.plans))
	}
}

func TestIntegrationCannotDowngradeZeroEligibilityBeforeGitV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	authority.Eligibility = workgraph.ObservedEligibilityShardV2{}
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Integrate(t.Context(), request); !IsKind(err, KindRevisionIneligible) || git.calls != 0 || len(port.plans) != 0 {
		t.Fatalf("zero eligibility crossed integration boundary: %v git=%d plans=%d", err, git.calls, len(port.plans))
	}
}

func TestIntegrationConflictAndManualMovementRequireRepairV2(t *testing.T) {
	for name, configure := range map[string]func(*integrationGitV2){"merge conflict": func(g *integrationGitV2) { g.conflict = true }, "manual movement": func(g *integrationGitV2) { g.result = strings.Repeat("c", 40) }} {
		t.Run(name, func(t *testing.T) {
			request, authority := integrationFixtureV2(t)
			port := newIntegrationPortV2(authority)
			git := newIntegrationGitFixtureV2(request)
			configure(git)
			if name == "manual movement" {
				gitAdapter := gitAdapterV2{integrationGitV2: git, onMerge: func() {
					git.mu.Lock()
					git.targets[git.key(request.RepositoryID.String(), request.TargetRef)] = strings.Repeat("d", 40)
					git.mu.Unlock()
				}}
				service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapter, journalv2.DefaultEventRegistryV2())
				result, err := service.Integrate(t.Context(), request)
				if err != nil || result.Stage != IntegrationAttemptRepairRequiredV2 || result.Conflict == nil || len(port.plans) != 4 {
					t.Fatalf("result=%+v error=%v plans=%d", result, err, len(port.plans))
				}
				git.mu.Lock()
				published := len(git.publications)
				git.mu.Unlock()
				if published != 0 {
					t.Fatalf("manual movement reached ref publication: %d", published)
				}
				return
			}
			service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
			result, err := service.Integrate(t.Context(), request)
			if err != nil || result.Stage != IntegrationAttemptRepairRequiredV2 || result.Conflict == nil {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestIntegrationLostRefUpdateResponseIsOneAdmissionV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	git.lostPublish = true
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	result, err := service.Integrate(t.Context(), request)
	if err != nil || result.Stage != IntegrationAttemptCompletedV2 || len(port.plans) != 5 {
		t.Fatalf("result=%+v error=%v plans=%d", result, err, len(port.plans))
	}
}

func TestIntegrationRefPublicationNeedsAttributableEffectNotAncestryV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	git.stealPublish = true
	git.ancestralResult = true // another actor's descendant contains our result
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	result, err := service.Integrate(t.Context(), request)
	if err != nil || result.Stage != IntegrationAttemptRepairRequiredV2 || result.ObservedTarget != strings.Repeat("d", 40) {
		t.Fatalf("unattributable ancestry result=%+v err=%v", result, err)
	}
	git.mu.Lock()
	_, attributed := git.publications[result.PublishEffectID]
	git.mu.Unlock()
	if attributed {
		t.Fatal("failed CAS fabricated an attributable publish observation")
	}
}

func TestIntegrationPostPublishAdvancementPreservesExactAdmissionV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	git.advanceAfterPublish = true
	git.ancestralResult = true
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	result, err := service.Integrate(t.Context(), request)
	if err != nil || result.Stage != IntegrationAttemptCompletedV2 || result.ObservedTarget != strings.Repeat("d", 40) {
		t.Fatalf("attributable advanced publication result=%+v err=%v", result, err)
	}
}

func TestIntegrationRejectsSubstitutedPublishObservationV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	git.corruptPublication = true
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Integrate(t.Context(), request); !IsKind(err, KindStaleAuthority) {
		t.Fatalf("substituted publication observation accepted: %v", err)
	}
}

func TestIntegrationExternalEvidenceAndDiagnosticsAreBoundedV2(t *testing.T) {
	secret := strings.Repeat("credential-and-command-output", 500)
	err := boundedGitFailureV2("observe integration target", errors.New(secret))
	if strings.Contains(err.Error(), "credential") || len(err.Error()) > 128 || !errors.Is(err, errors.Unwrap(err)) {
		t.Fatalf("unbounded public diagnostic: %q", err.Error())
	}
	request, _ := integrationFixtureV2(t)
	state := IntegrationAttemptStateV2{SchemaVersion: 1, LogicalVersion: 2, Stage: IntegrationAttemptAuthorityActiveV2, AttemptID: request.AttemptID, OperationID: request.OperationID, ProjectID: request.ProjectID, MissionID: request.MissionID, WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID, AcceptanceID: request.AcceptanceID, AcceptanceDigest: strings.Repeat("a", 64), RepositoryID: request.RepositoryID, TargetRef: request.TargetRef, Candidate: request.Candidate, ExpectedOld: strings.Repeat("b", 40), TargetGeneration: 1, FenceTokenHash: strings.Repeat("c", 64), FenceEffectID: "effect-fence", WorkspaceEffectID: "effect-workspace", MergeEffectID: "effect-merge", PublishEffectID: "effect-publish", CleanupEffectID: "effect-cleanup", RuntimeEpochID: request.RuntimeEpochID, ActorID: request.ActorID, ProcessID: request.ProcessID, Governance: request.Governance, ObservedAt: request.ObservedAt.Format(time.RFC3339Nano)}
	state.Stage = IntegrationAttemptResultObservedV2
	state.Result = strings.Repeat("d", 40)
	state.ObservedTarget = state.ExpectedOld
	state.Workspace = WorkspaceFacts{Path: strings.Repeat("x", maxIntegrationWorkspacePathV2+1), Registered: true, Detached: true, Clean: true, Head: state.Result, CandidateReachable: true}
	if state.Validate() == nil {
		t.Fatal("oversized workspace evidence entered durable state")
	}
	state.Workspace = WorkspaceFacts{}
	state.Stage = IntegrationAttemptRepairRequiredV2
	state.Conflict = &ConflictEvidence{Base: state.ExpectedOld, Candidate: state.Candidate, GitMessage: strings.Repeat("x", maxIntegrationExternalTextV2+1)}
	state.RepairAction = "repair"
	if state.Validate() == nil {
		t.Fatal("oversized conflict evidence entered durable state")
	}
}

func TestIntegrationProviderCauseIsRedactedFromJSONAndRemainsClassifiableV2(t *testing.T) {
	cause := &providerJSONCauseV2{secret: strings.Repeat("private-provider-output", 1024)}
	err := boundedGitFailureV2("observe integration target", cause)
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if len(encoded) >= 4096 || bytes.Contains(encoded, []byte("private-provider-output")) || cause.marshalCalls != 0 {
		t.Fatalf("provider cause crossed JSON boundary: bytes=%d calls=%d prefix=%q", len(encoded), cause.marshalCalls, encoded[:min(len(encoded), 128)])
	}
	if !errors.Is(err, cause) {
		t.Fatal("bounded diagnostic lost errors.Is classification")
	}
	var typed *providerJSONCauseV2
	if !errors.As(err, &typed) || typed != cause {
		t.Fatal("bounded diagnostic lost errors.As classification")
	}
}

func TestIntegrationLostFenceResponseReconcilesByExactObservationV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	git.lostFence = true
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	result, err := service.Integrate(t.Context(), request)
	if err != nil || result.Stage != IntegrationAttemptCompletedV2 || len(git.fences) != 1 || len(port.plans) != 5 {
		t.Fatalf("result=%+v error=%v fences=%d plans=%d", result, err, len(git.fences), len(port.plans))
	}
}

func TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		request, authority := integrationFixtureV2(t)
		request.OperationID = journalv2.OperationID(fmt.Sprintf("operation-lost-authority-%03d", trial))
		request.AttemptID = fmt.Sprintf("attempt-lost-authority-%03d", trial)
		port := newIntegrationPortV2(authority)
		port.dropAckAt = trial%5 + 1
		git := newIntegrationGitFixtureV2(request)
		service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
		result, err := service.Integrate(t.Context(), request)
		if err != nil || result.Stage != IntegrationAttemptCompletedV2 || len(port.plans) != 5 {
			t.Fatalf("trial=%d stage=%d result=%+v error=%v plans=%d", trial, port.dropAckAt, result, err, len(port.plans))
		}
	}
}

func TestIntegrationTargetAuthorityAdmitsOneOfOneHundredContendersV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	git.blockFence = make(chan struct{})
	git.fenceEntered = make(chan struct{}, 1)
	winner, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	winnerDone := make(chan error, 1)
	go func() {
		_, err := winner.Integrate(t.Context(), request)
		winnerDone <- err
	}()
	<-git.fenceEntered

	var group sync.WaitGroup
	results := make(chan error, 99)
	for index := 1; index < 100; index++ {
		contender := request
		contender.OperationID = journalv2.OperationID(fmt.Sprintf("operation-integration-%03d", index))
		contender.AttemptID = fmt.Sprintf("attempt-%03d", index)
		group.Add(1)
		go func(candidate IntegrationAuthorityRequestV2) {
			defer group.Done()
			service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(candidate.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
			_, err := service.Integrate(t.Context(), candidate)
			results <- err
		}(contender)
	}
	group.Wait()
	close(results)
	for err := range results {
		if !IsKind(err, KindConflict) {
			t.Fatalf("contender was not refused by target CAS: %v", err)
		}
	}
	close(git.blockFence)
	if err := <-winnerDone; err != nil {
		t.Fatalf("target authority winner failed: %v", err)
	}
	if len(port.plans) != 5 {
		t.Fatalf("unexpected admitted publication count: %d", len(port.plans))
	}
}

func TestReleasedTargetCanBeAcquiredByNextOperationGenerationV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	first, err := service.Integrate(t.Context(), request)
	if err != nil || first.Stage != IntegrationAttemptCompletedV2 {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	request.OperationID = "operation-integration-next"
	request.AttemptID = "attempt-next"
	git.result = strings.Repeat("e", 40)
	service, _ = NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	second, err := service.Integrate(t.Context(), request)
	if err != nil || second.Stage != IntegrationAttemptCompletedV2 || second.ExpectedOld != strings.Repeat("c", 40) {
		t.Fatalf("second=%+v error=%v", second, err)
	}
	coordinate, _ := IntegrationTargetCoordinateV2(request.RepositoryID, request.TargetRef)
	target := port.targets[coordinate]
	if target.Stage != IntegrationTargetReleasedV2 || target.Generation != 2 || target.LogicalVersion != 6 || len(port.plans) != 10 {
		t.Fatalf("target=%+v plans=%d", target, len(port.plans))
	}
}

func TestIndependentTargetsProgressConcurrentlyWithoutGlobalLockV2(t *testing.T) {
	left, authority := integrationFixtureV2(t)
	right := left
	right.OperationID = "operation-integration-right"
	right.AttemptID = "attempt-right"
	right.TargetRef = "refs/heads/release"
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(left)
	git.targets[git.key(right.RepositoryID.String(), right.TargetRef)] = strings.Repeat("a", 40)
	results := make(chan error, 2)
	for _, request := range []IntegrationAuthorityRequestV2{left, right} {
		go func(candidate IntegrationAuthorityRequestV2) {
			service, _ := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(candidate.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
			result, err := service.Integrate(t.Context(), candidate)
			if err == nil && result.Stage != IntegrationAttemptCompletedV2 {
				err = fmt.Errorf("unexpected stage %s", result.Stage)
			}
			results <- err
		}(request)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("independent target failed: %v", err)
		}
	}
	if len(port.plans) != 10 {
		t.Fatalf("independent publications=%d", len(port.plans))
	}
}

// gitAdapterV2 supplies the concrete ids.RepositoryID method signatures while
// retaining optional deterministic boundary injection in tests.
type gitAdapterV2 struct {
	*integrationGitV2
	onMerge func()
}

func (g gitAdapterV2) TargetHead(_ context.Context, repositoryID ids.RepositoryID, target string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return g.targets[g.key(repositoryID.String(), target)], nil
}
func (g gitAdapterV2) IsAncestor(_ context.Context, _ ids.RepositoryID, ancestor, current string) (bool, error) {
	return ancestor == current || g.ancestralResult, nil
}
func (g gitAdapterV2) MergeCandidate(ctx context.Context, intent IntegrationMergeIntentV2, workspace WorkspaceFacts) (string, *ConflictEvidence, error) {
	result, conflict, err := g.integrationGitV2.MergeCandidate(ctx, intent, workspace)
	if g.onMerge != nil {
		g.onMerge()
	}
	return result, conflict, err
}

func integrationFixtureV2(t *testing.T) (IntegrationAuthorityRequestV2, IntegrationAuthorityObservationV2) {
	acceptanceRequest, acceptanceAuthority := acceptanceFixtureV2(t)
	port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: acceptanceAuthority, revision: 40}
	service, _ := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(acceptanceRequest.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	accepted, err := service.Accept(t.Context(), acceptanceRequest)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := acceptanceCoordinateV2(accepted.State)
	assertion := acceptanceAssertionV2(t, coordinate, 50, journalv2.LogicalVersion(accepted.State.LogicalVersion), accepted.State.CanonicalBytes())
	request := IntegrationAuthorityRequestV2{OperationID: "operation-integration-1", AttemptID: "attempt-1", ProjectID: accepted.State.ProjectID, MissionID: accepted.State.MissionID, WorkRevisionID: accepted.State.WorkRevisionID, WorkID: accepted.State.WorkID, AcceptanceID: accepted.State.AcceptanceID, RepositoryID: "repository-1", TargetRef: "refs/heads/main", Candidate: accepted.State.Candidate, RuntimeEpochID: accepted.State.RuntimeEpochID, ActorID: "actor-integrator", ProcessID: "process-integrator", Governance: accepted.State.Governance, ObservedAt: acceptanceRequest.ObservedAt.Add(time.Minute)}
	return request, IntegrationAuthorityObservationV2{Acceptance: AcceptanceObservationV2{State: &accepted.State, RecordRevision: 50}, Eligibility: acceptanceAuthority.Eligibility, Assertions: []journalv2.AuthorityAssertionV2{assertion}}
}
func newIntegrationGitFixtureV2(request IntegrationAuthorityRequestV2) *integrationGitV2 {
	return &integrationGitV2{targets: map[string]string{request.RepositoryID.String() + "\x00" + request.TargetRef: strings.Repeat("a", 40)}, result: strings.Repeat("c", 40)}
}
