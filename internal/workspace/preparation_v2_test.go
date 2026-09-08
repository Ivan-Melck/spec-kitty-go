package workspace

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type preparationJournalProbeV2 struct {
	state StateV2
	trace []string
}

func (p *preparationJournalProbeV2) ObservePreparation(context.Context, BindingV2) (StateV2, error) {
	p.trace = append(p.trace, "observe_durable")
	return p.state, nil
}
func (p *preparationJournalProbeV2) PublishPreparation(_ context.Context, c PreparationCommandV2) (StateV2, error) {
	p.trace = append(p.trace, "publish_"+string(c.Action))
	if c.Prior.LogicalVersion != p.state.LogicalVersion {
		return StateV2{}, workspaceV2Error(KindWorkspaceConflict, "prior", nil)
	}
	next := p.state
	next.LogicalVersion++
	next.Observation = c.Observation
	switch c.Action {
	case PreparationObserveCreatedV2:
		next.Stage = StageWorktreeObservedV2
	case PreparationVerifyBaseV2:
		next.Stage = StageBaseVerifiedV2
	case PreparationRequestDepsV2:
		next.Stage = StageDependenciesRequestedV2
	case PreparationObserveDepsV2:
		next.Stage = StageDependenciesObservedV2
	case PreparationVerifyOwnerV2:
		next.Stage = StageOwnershipVerifiedV2
	case PreparationPublishReadyV2:
		if c.PreTarget == "" || c.PreTarget != c.PostTarget {
			return StateV2{}, workspaceV2Error(KindTargetMoved, "target", nil)
		}
		next.Stage = StageReadyV2
		next.Observation.TargetBefore = c.PreTarget
		next.Observation.TargetAfter = c.PostTarget
	default:
		return StateV2{}, workspaceV2Error(KindInvalid, "action", nil)
	}
	p.state = next
	return next, nil
}

type preparationEffectsProbeV2 struct {
	observation                  ObservationV2
	trace                        []string
	createCalls, dependencyCalls int
	createErr                    error
}

func (p *preparationEffectsProbeV2) ObserveWorkspace(context.Context, BindingV2) (ObservationV2, error) {
	p.trace = append(p.trace, "observe_external")
	return p.observation, nil
}
func (p *preparationEffectsProbeV2) CreateWorkspace(context.Context, BindingV2) error {
	p.trace = append(p.trace, "create")
	p.createCalls++
	p.observation.Registered = true
	p.observation.Head = string(make([]byte, 0))
	if p.createErr != nil {
		err := p.createErr
		p.createErr = nil
		return err
	}
	return nil
}
func (p *preparationEffectsProbeV2) PrepareDependencies(_ context.Context, b BindingV2) error {
	p.trace = append(p.trace, "prepare_dependencies")
	p.dependencyCalls++
	p.observation = readyObservationFixtureV2(b)
	return nil
}
func (p *preparationEffectsProbeV2) WithRepositoryTargetGuard(ctx context.Context, fn func(RepositoryTargetGuardV2) error) error {
	p.trace = append(p.trace, "guard")
	return fn(preparationGuardProbeV2{p: p})
}

type preparationGuardProbeV2 struct{ p *preparationEffectsProbeV2 }

func (g preparationGuardProbeV2) TargetHead(context.Context) (string, error) {
	return g.p.observation.Head, nil
}
func (g preparationGuardProbeV2) ObserveWorkspace(context.Context, BindingV2) (ObservationV2, error) {
	return g.p.observation, nil
}

func TestPreparationIntentPrecedesActionAndRestartReconcilesV2(t *testing.T) {
	b := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	initial := StateV2{SchemaVersion: 1, LogicalVersion: 1, Binding: b, Stage: StageCreationRequestedV2, EffectID: "effect-create", OccurredAt: "2026-09-04T12:00:00Z"}
	journal := &preparationJournalProbeV2{state: initial}
	effects := &preparationEffectsProbeV2{observation: ObservationV2{}, createErr: errors.New("lost create response")}
	service, _ := NewPreparationServiceV2(journal, effects)
	if _, err := service.EnsureReady(t.Context(), b); err == nil {
		t.Fatal("lost response accepted")
	}
	if effects.createCalls != 1 || journal.state.Stage != StageCreationRequestedV2 {
		t.Fatalf("intent/action chronology failed: state=%s calls=%d", journal.state.Stage, effects.createCalls)
	}
	// The external action happened despite its lost response. Restart observes it
	// and advances the same durable identity without creating a second directory.
	effects.observation.Head = b.PinnedBase
	result, err := service.EnsureReady(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stage != StageReadyV2 || effects.createCalls != 1 || effects.dependencyCalls != 1 {
		t.Fatalf("result=%s create=%d deps=%d", result.Stage, effects.createCalls, effects.dependencyCalls)
	}
	if journal.trace[0] != "observe_durable" || effects.trace[0] != "observe_external" {
		t.Fatal("durable intent was not observed before adapter action")
	}
}

func TestReadyRequiresTwoMatchingPrePublicationObservationsUnderGuardV2(t *testing.T) {
	b := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	state := StateV2{SchemaVersion: 1, LogicalVersion: 6, Binding: b, Stage: StageOwnershipVerifiedV2, EffectID: "effect-create", Observation: readyObservationFixtureV2(b), OccurredAt: "2026-09-04T12:00:00Z"}
	journal := &preparationJournalProbeV2{state: state}
	effects := &preparationEffectsProbeV2{observation: readyObservationFixtureV2(b)}
	service, _ := NewPreparationServiceV2(journal, effects)
	result, err := service.EnsureReady(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stage != StageReadyV2 || result.Observation.TargetBefore != b.PinnedBase || result.Observation.TargetAfter != b.PinnedBase {
		t.Fatalf("missing guard evidence: %+v", result.Observation)
	}
	effects.observation.Head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	journal.state = state
	if _, err := service.EnsureReady(t.Context(), b); !IsKind(err, KindTargetMoved) {
		t.Fatalf("got %v", err)
	}
}

func TestPreparationRestartFromEveryDurableStageIsCanonicalV2(t *testing.T) {
	binding := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	stages := []StageV2{StageCreationRequestedV2, StageWorktreeObservedV2, StageBaseVerifiedV2, StageDependenciesRequestedV2, StageDependenciesObservedV2, StageOwnershipVerifiedV2, StageReadyV2}
	var canonical []byte
	for index, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			state := StateV2{SchemaVersion: 1, LogicalVersion: uint64(index + 1), Binding: binding, Stage: stage, EffectID: "effect-create", Observation: observationForStageFixtureV2(binding, stage), OccurredAt: "2026-09-04T12:00:00Z"}
			journal := &preparationJournalProbeV2{state: state}
			effects := &preparationEffectsProbeV2{observation: readyObservationFixtureV2(binding)}
			service, err := NewPreparationServiceV2(journal, effects)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.EnsureReady(t.Context(), binding)
			if err != nil {
				t.Fatal(err)
			}
			if result.Stage != StageReadyV2 || !bindingEqualV2(result.Binding, binding) {
				t.Fatalf("result=%+v", result)
			}
			stateBytes := result.CanonicalBytes()
			if index == 0 {
				canonical = append([]byte(nil), stateBytes...)
			} else if !bytes.Equal(canonicalWorkspaceResultV2(canonical), canonicalWorkspaceResultV2(stateBytes)) {
				t.Fatal("restart changed canonical ready authority")
			}
		})
	}
}

func TestPreparationRefusesRepositoryObservationDriftV2(t *testing.T) {
	binding := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
	for name, mutate := range map[string]func(*ObservationV2){
		"target":       func(v *ObservationV2) { v.Head = strings.Repeat("b", 40) },
		"branch":       func(v *ObservationV2) { v.Branch = "kitty/foreign" },
		"dependency":   func(v *ObservationV2) { v.DependencyHead = strings.Repeat("b", 40) },
		"gitdir":       func(v *ObservationV2) { v.GitDirIdentity = "" },
		"index":        func(v *ObservationV2) { v.IndexIdentity = "" },
		"registration": func(v *ObservationV2) { v.Registered = false },
		"cleanliness":  func(v *ObservationV2) { v.Clean = false },
	} {
		t.Run(name, func(t *testing.T) {
			observation := readyObservationFixtureV2(binding)
			mutate(&observation)
			state := StateV2{SchemaVersion: 1, LogicalVersion: 5, Binding: binding, Stage: StageDependenciesObservedV2, EffectID: "effect-create", Observation: observation, OccurredAt: "2026-09-04T12:00:00Z"}
			journal := &preparationJournalProbeV2{state: state}
			effects := &preparationEffectsProbeV2{observation: observation}
			service, _ := NewPreparationServiceV2(journal, effects)
			if _, err := service.EnsureReady(t.Context(), binding); err == nil {
				t.Fatal("drift reached ready")
			}
			if journal.state.Stage == StageReadyV2 {
				t.Fatal("drift published ready")
			}
		})
	}
}

func canonicalWorkspaceResultV2(encoded []byte) []byte {
	state, err := DecodeStateV2(encoded)
	if err != nil {
		return nil
	}
	state.LogicalVersion = 1
	state.OccurredAt = "2026-09-04T12:00:00Z"
	return state.CanonicalBytes()
}

func readyObservationFixtureV2(b BindingV2) ObservationV2 {
	return ObservationV2{Registered: true, Head: b.PinnedBase, Branch: b.OwnedBranch, GitDirIdentity: "gitdir-identity", IndexIdentity: "index-identity", Clean: true, DependencyHead: b.PinnedBase, TargetBefore: b.PinnedBase, TargetAfter: b.PinnedBase}
}
