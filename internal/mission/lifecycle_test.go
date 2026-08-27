package mission_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
)

func TestMissionIdentityAndClosedLifecycleValues(t *testing.T) {
	t.Parallel()

	parsers := []struct {
		name  string
		parse func(string) error
	}{
		{name: "definition", parse: func(v string) error { _, err := ids.ParseMissionDefinitionID(v); return err }},
		{name: "decision", parse: func(v string) error { _, err := ids.ParseDecisionID(v); return err }},
		{name: "blocker", parse: func(v string) error { _, err := ids.ParseBlockerID(v); return err }},
		{name: "event", parse: func(v string) error { _, err := ids.ParseMissionEventID(v); return err }},
		{name: "evidence", parse: func(v string) error { _, err := ids.ParseEvidenceID(v); return err }},
	}
	for _, parser := range parsers {
		if err := parser.parse(parser.name + ":01"); err != nil {
			t.Fatalf("valid %s ID: %v", parser.name, err)
		}
		for _, value := range []string{"", " ", "../value", "value/path", "value with space"} {
			if err := parser.parse(value); err == nil {
				t.Fatalf("invalid %s ID %q accepted", parser.name, value)
			}
		}
	}

	for _, step := range mission.Phase3StepKeys() {
		if err := step.Validate(); err != nil {
			t.Fatalf("closed step %q rejected: %v", step, err)
		}
		parsed, err := mission.ParseStepKey(step.String())
		if err != nil || parsed != step {
			t.Fatalf("step round trip %q: parsed=%q err=%v", step, parsed, err)
		}
	}
	if _, err := mission.ParseStepKey("checked-markdown-task"); err == nil {
		t.Fatal("unknown step key accepted")
	}
}

func TestDefinitionRegistryIsCanonicalExactAndImmutable(t *testing.T) {
	t.Parallel()

	definition := mustDefinition(t)
	input := definitionSpec(definition)
	constructed, err := mission.NewDefinition(input)
	if err != nil {
		t.Fatal(err)
	}
	originalCommitment := constructed.Commitment()
	input.Steps[0].Successors = nil
	input.TerminalSteps[0] = mission.StepInitialized
	if constructed.Commitment() != originalCommitment || len(constructed.Steps()) != len(definition.Steps()) {
		t.Fatal("definition constructor retained caller-owned collection storage")
	}

	reordered := definitionSpec(definition)
	reverse(reordered.Steps)
	reverse(reordered.TerminalSteps)
	for i := range reordered.Steps {
		reverse(reordered.Steps[i].RequiredEvidence)
		reverse(reordered.Steps[i].Successors)
	}
	equivalent, err := mission.NewDefinition(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if equivalent.Commitment() != definition.Commitment() || !reflect.DeepEqual(equivalent.Steps(), definition.Steps()) {
		t.Fatalf("equivalent order changed definition law: %s != %s", equivalent.Commitment(), definition.Commitment())
	}

	semantic := definitionSpec(definition)
	for i := range semantic.Steps {
		if semantic.Steps[i].Key == mission.StepPlanning {
			semantic.Steps[i].GovernanceAction = "mission.plan.changed"
		}
	}
	changed, err := mission.NewDefinition(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Commitment() == definition.Commitment() {
		t.Fatal("semantic mutation did not change commitment")
	}

	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(softwaredev.DefinitionID, softwaredev.DefinitionVersion)
	if err != nil {
		t.Fatal(err)
	}
	mutated := resolved.Steps()
	mutated[0] = mission.StepContract{}
	again, err := registry.Resolve(softwaredev.DefinitionID, softwaredev.DefinitionVersion)
	if err != nil || again.Commitment() != definition.Commitment() || len(again.Steps()) != len(definition.Steps()) {
		t.Fatalf("caller mutation changed registry: definition=%+v err=%v", again, err)
	}

	unknown := mustDefinitionID(t, "unknown-method")
	if _, err := registry.Resolve(unknown, 1); !mission.IsKind(err, mission.KindDefinitionNotFound) {
		t.Fatalf("unknown definition: %v", err)
	}
	if _, err := registry.Resolve(softwaredev.DefinitionID, 999); !mission.IsKind(err, mission.KindDefinitionVersionUnsupported) {
		t.Fatalf("unsupported version: %v", err)
	}
	if _, err := registry.Resolve("", 0); !mission.IsKind(err, mission.KindDefinitionMalformed) {
		t.Fatalf("malformed selection: %v", err)
	}
	empty, err := mission.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.Resolve(softwaredev.DefinitionID, softwaredev.DefinitionVersion); !mission.IsKind(err, mission.KindDefinitionNotFound) {
		t.Fatalf("removed production definition did not fail closed: %v", err)
	}
	if _, err := mission.NewRegistry(definition, definition); !mission.IsKind(err, mission.KindDuplicateDefinition) {
		t.Fatalf("duplicate exact definition: %v", err)
	}
	if _, err := mission.NewRegistry(definition, changed); !mission.IsKind(err, mission.KindDefinitionConflict) {
		t.Fatalf("conflicting exact definition: %v", err)
	}
}

func TestDefinitionGraphMutantsFailClosedWithExactRuleContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*mission.DefinitionSpec)
		want   string
	}{
		{name: "duplicate-step", mutate: func(s *mission.DefinitionSpec) { s.Steps = append(s.Steps, s.Steps[0]) }, want: "duplicate step"},
		{name: "missing-initial", mutate: func(s *mission.DefinitionSpec) { s.InitialStep = mission.StepReviewReserved }, want: "initial step"},
		{name: "missing-terminal", mutate: func(s *mission.DefinitionSpec) { s.TerminalSteps = nil }, want: "terminal"},
		{name: "unknown-successor", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepInitialized).Successors = []mission.StepKey{mission.StepReviewReserved}
		}, want: "adjacent"},
		{name: "asymmetric-edge", mutate: func(s *mission.DefinitionSpec) { step(s, mission.StepSpecifying).Predecessors = nil }, want: "asymmetric"},
		{name: "self-edge", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepSpecifying).Successors = []mission.StepKey{mission.StepSpecifying}
		}, want: "self-edge"},
		{name: "duplicate-edge", mutate: func(s *mission.DefinitionSpec) {
			p := step(s, mission.StepInitialized)
			p.Successors = append(p.Successors, p.Successors[0])
		}, want: "duplicate successor"},
		{name: "disconnected", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepInitialized).Successors = nil
			step(s, mission.StepSpecifying).Predecessors = nil
		}, want: "unreachable"},
		{name: "cycle", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepWorkDraftReady).Successors = append(step(s, mission.StepWorkDraftReady).Successors, mission.StepSpecifying)
			step(s, mission.StepSpecifying).Predecessors = append(step(s, mission.StepSpecifying).Predecessors, mission.StepWorkDraftReady)
		}, want: "cycle"},
		{name: "terminal-successor", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepWorkFinalized).Successors = []mission.StepKey{mission.StepSpecifying}
			step(s, mission.StepSpecifying).Predecessors = append(step(s, mission.StepSpecifying).Predecessors, mission.StepWorkFinalized)
		}, want: "terminal step"},
		{name: "mutating-without-governance", mutate: func(s *mission.DefinitionSpec) { step(s, mission.StepPlanning).GovernanceAction = "" }, want: "governance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := definitionSpec(mustDefinition(t))
			test.mutate(&spec)
			_, err := mission.NewDefinition(spec)
			if !mission.IsKind(err, mission.KindDefinitionMalformed) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mutant=%s err=%v want context %q", test.name, err, test.want)
			}
		})
	}
}

func TestEveryDeclaredEdgeAndNoUndeclaredEdgeAdvances(t *testing.T) {
	t.Parallel()

	definition := mustDefinition(t)
	declared := make(map[string]bool)
	for _, from := range definition.Steps() {
		for _, to := range from.Spec().Successors {
			declared[from.Key().String()+"->"+to.String()] = true
			current := snapshotAt(t, definition, from.Key(), 7)
			candidate, err := mission.EvaluateTransition(mustInstance(t, current), definition, requestFor(t, definition, current, to, "declared"))
			if err != nil {
				t.Fatalf("declared edge %s -> %s rejected: %v", from.Key(), to, err)
			}
			if candidate.Successor.CurrentStep != to || candidate.Successor.StateVersion != current.StateVersion+1 || candidate.Previous.CurrentStep != from.Key() {
				t.Fatalf("edge %s -> %s produced invalid candidate: %+v", from.Key(), to, candidate)
			}
		}
	}

	for _, from := range definition.Steps() {
		for _, to := range mission.Phase3StepKeys() {
			key := from.Key().String() + "->" + to.String()
			if declared[key] {
				continue
			}
			current := snapshotAt(t, definition, from.Key(), 11)
			_, err := mission.EvaluateTransition(mustInstance(t, current), definition, requestFor(t, definition, current, to, "undeclared"))
			if err == nil {
				t.Fatalf("undeclared edge %s advanced", key)
			}
		}
	}
}

func TestSpecificationAndPlanGatesRequireEveryTypedFact(t *testing.T) {
	t.Parallel()

	definition := mustDefinition(t)
	for _, test := range []struct{ from, to mission.StepKey }{
		{from: mission.StepSpecifying, to: mission.StepSpecificationApproved},
		{from: mission.StepPlanning, to: mission.StepPlanApproved},
	} {
		current := snapshotAt(t, definition, test.from, 3)
		complete := requestFor(t, definition, current, test.to, "gate")
		for index, omitted := range complete.Evidence {
			request := cloneRequest(complete)
			request.OperationID = mustOperationID(t, fmt.Sprintf("omit.%d", index))
			request.EventID = mustEventID(t, fmt.Sprintf("omit.%d", index))
			request.Evidence = append(request.Evidence[:index:index], request.Evidence[index+1:]...)
			if _, err := mission.EvaluateTransition(mustInstance(t, current), definition, request); !mission.IsKind(err, mission.KindMissingEvidence) {
				t.Fatalf("from=%s omitted=%s err=%v", test.from, omitted.Kind, err)
			}
		}
		approvalIndex := -1
		for index, evidence := range complete.Evidence {
			if evidence.Kind == mission.EvidenceSpecificationOK || evidence.Kind == mission.EvidencePlanOK {
				approvalIndex = index
				break
			}
		}
		if approvalIndex < 0 {
			t.Fatal("fixture has no approval evidence")
		}
		stale := cloneRequest(complete)
		stale.Evidence[approvalIndex].Status = mission.EvidenceStale
		if _, err := mission.EvaluateTransition(mustInstance(t, current), definition, stale); !mission.IsKind(err, mission.KindApprovalStale) {
			t.Fatalf("stale approval from=%s err=%v", test.from, err)
		}
	}
}

func TestTransitionRefusalsAreTypedDeterministicAndNonMutating(t *testing.T) {
	t.Parallel()

	definition := mustDefinition(t)
	base := snapshotAt(t, definition, mission.StepInitialized, 5)
	request := requestFor(t, definition, base, mission.StepSpecifying, "stable")

	tests := []struct {
		name           string
		kind           mission.ErrorKind
		mutateSnapshot func(*mission.MissionSnapshot)
		mutateRequest  func(*mission.TransitionRequest)
	}{
		{name: "wrong-mission", kind: mission.KindInvalidRequest, mutateRequest: func(r *mission.TransitionRequest) { r.MissionID = mustMissionID(t, "mission.other") }},
		{name: "definition-mismatch", kind: mission.KindDefinitionMismatch, mutateRequest: func(r *mission.TransitionRequest) {
			r.DefinitionCommitment = mission.DefinitionCommitment(strings.Repeat("a", 64))
		}},
		{name: "stale-version", kind: mission.KindStateVersionConflict, mutateRequest: func(r *mission.TransitionRequest) { r.ExpectedStateVersion-- }},
		{name: "active-blocker", kind: mission.KindActiveBlocker, mutateSnapshot: func(s *mission.MissionSnapshot) { s.ActiveBlockers = []ids.BlockerID{mustBlockerID(t, "blocker.1")} }},
		{name: "open-decision", kind: mission.KindUnresolvedDecision, mutateSnapshot: func(s *mission.MissionSnapshot) {
			s.UnresolvedDecisions = []ids.DecisionID{mustDecisionID(t, "decision.1")}
		}},
		{name: "reserved", kind: mission.KindUnsupportedAction, mutateRequest: func(r *mission.TransitionRequest) { r.Successor = mission.StepExecuteReserved }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := base
			candidateRequest := cloneRequest(request)
			if test.mutateSnapshot != nil {
				test.mutateSnapshot(&current)
			}
			if test.mutateRequest != nil {
				test.mutateRequest(&candidateRequest)
			}
			candidateInstance := mustInstance(t, current)
			before := candidateInstance.Snapshot()
			_, first := mission.EvaluateTransition(candidateInstance, definition, candidateRequest)
			_, second := mission.EvaluateTransition(candidateInstance, definition, candidateRequest)
			if !mission.IsKind(first, test.kind) || first.Error() != second.Error() {
				t.Fatalf("kind/determinism first=%v second=%v", first, second)
			}
			if !reflect.DeepEqual(before, candidateInstance.Snapshot()) {
				t.Fatal("refusal mutated supplied mission")
			}
		})
	}

	terminal := snapshotAt(t, definition, mission.StepWorkFinalized, 9)
	_, err := mission.EvaluateTransition(mustInstance(t, terminal), definition, requestFor(t, definition, terminal, mission.StepSpecifying, "terminal"))
	if !mission.IsKind(err, mission.KindTerminalMission) {
		t.Fatalf("terminal escape err=%v", err)
	}
	for _, hostile := range []string{"done", "[x] task", "refs/heads/main", "commit abc", "spec-kitty next"} {
		if _, err := mission.ParseStepKey(hostile); err == nil {
			t.Fatalf("hostile presentation value became authority: %q", hostile)
		}
	}
}

func TestMissionSnapshotCopiesInputsAndRejectsImpossibleState(t *testing.T) {
	t.Parallel()

	definition := mustDefinition(t)
	snapshot := snapshotAt(t, definition, mission.StepPlanning, 4)
	blockers := []ids.BlockerID{mustBlockerID(t, "blocker.copy")}
	snapshot.ActiveBlockers = blockers
	instance := mustInstance(t, snapshot)
	blockers[0] = mustBlockerID(t, "blocker.changed")
	copy := instance.Snapshot()
	copy.ActiveBlockers[0] = mustBlockerID(t, "blocker.output-changed")
	if got := instance.Snapshot().ActiveBlockers[0].String(); got != "blocker.copy" {
		t.Fatalf("mission collection leaked mutable backing: %s", got)
	}

	invalids := []mission.MissionSnapshot{
		func() mission.MissionSnapshot { s := snapshot; s.StateVersion = 0; return s }(),
		func() mission.MissionSnapshot {
			s := snapshot
			s.TerminalOutcome = mission.OutcomeWorkFinalized
			return s
		}(),
		func() mission.MissionSnapshot {
			s := snapshot
			s.CurrentWorkRevisionID = mustWorkRevisionID(t, "revision.early")
			return s
		}(),
		func() mission.MissionSnapshot { s := snapshot; s.UpdatedAt = s.CreatedAt.Add(-time.Second); return s }(),
		func() mission.MissionSnapshot {
			s := snapshot
			s.ActiveBlockers = []ids.BlockerID{mustBlockerID(t, "blocker.duplicate"), mustBlockerID(t, "blocker.duplicate")}
			return s
		}(),
		func() mission.MissionSnapshot {
			s := snapshot
			s.UnresolvedDecisions = []ids.DecisionID{mustDecisionID(t, "decision.duplicate"), mustDecisionID(t, "decision.duplicate")}
			return s
		}(),
		func() mission.MissionSnapshot {
			s := snapshot
			s.CurrentStep = mission.StepWorkFinalized
			s.CurrentWorkRevisionID = mustWorkRevisionID(t, "revision.nonterminal")
			return s
		}(),
		func() mission.MissionSnapshot {
			s := snapshot
			s.CurrentStep = mission.StepWorkFinalized
			s.Lifecycle = mission.LifecycleTerminal
			s.TerminalOutcome = mission.OutcomeWorkFinalized
			return s
		}(),
	}
	for index, invalid := range invalids {
		if _, err := mission.NewMissionInstance(invalid); err == nil {
			t.Fatalf("invalid mission fixture %d accepted", index)
		}
	}
}

func TestServiceCreateReplayConflictTransitionRaceAndStoreFailure(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	service := mission.NewService(registry, store)
	createdAt := fixtureTime()
	create := mission.CreateRequest{
		MissionID: mustMissionID(t, "mission.service"), ProjectID: mustProjectID(t, "project.service"),
		DefinitionID: definition.ID(), DefinitionVersion: definition.Version(),
		OperationID: mustOperationID(t, "operation.create"), ActorID: mustActorID(t, "actor.create"), OccurredAt: createdAt,
	}
	first, err := service.Create(context.Background(), create)
	if err != nil || first.Replayed || first.Instance.StateVersion != 1 || first.Instance.DefinitionCommitment != definition.Commitment() {
		t.Fatalf("create=%+v err=%v", first, err)
	}
	replay, err := service.Create(context.Background(), create)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Instance, first.Instance) {
		t.Fatalf("create replay=%+v err=%v", replay, err)
	}
	altered := create
	altered.ActorID = mustActorID(t, "actor.changed")
	if _, err := service.Create(context.Background(), altered); !mission.IsKind(err, mission.KindIdempotencyConflict) {
		t.Fatalf("altered create replay err=%v", err)
	}

	baseRequest := requestFor(t, definition, first.Instance, mission.StepSpecifying, "race-a")
	peerRequest := cloneRequest(baseRequest)
	peerRequest.OperationID = mustOperationID(t, "operation.race-b")
	peerRequest.EventID = mustEventID(t, "event.race-b")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, request := range []mission.TransitionRequest{baseRequest, peerRequest} {
		wg.Add(1)
		go func(request mission.TransitionRequest) {
			defer wg.Done()
			_, err := service.Transition(context.Background(), request)
			errs <- err
		}(request)
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case mission.IsKind(err, mission.KindStateVersionConflict):
			conflicts++
		default:
			t.Fatalf("race error=%v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("race wins=%d conflicts=%d", wins, conflicts)
	}

	store.mu.Lock()
	winningRequest := baseRequest
	if _, ok := store.transitions[winningRequest.OperationID]; !ok {
		winningRequest = peerRequest
	}
	storedBeforeFailure := store.snapshot
	store.mu.Unlock()
	exactReplay, err := service.Transition(context.Background(), winningRequest)
	if err != nil || !exactReplay.Replayed || !reflect.DeepEqual(exactReplay.Instance, storedBeforeFailure) {
		t.Fatalf("transition replay=%+v err=%v", exactReplay, err)
	}
	alteredReplay := cloneRequest(winningRequest)
	alteredReplay.ActorID = mustActorID(t, "actor.altered-replay")
	if _, err := service.Transition(context.Background(), alteredReplay); !mission.IsKind(err, mission.KindIdempotencyConflict) {
		t.Fatalf("altered transition replay err=%v", err)
	}

	store.mu.Lock()
	store.failCommit = errors.New("seeded secret must not escape")
	store.mu.Unlock()
	next := requestFor(t, definition, storedBeforeFailure, mission.StepSpecificationApproved, "store-failure")
	_, err = service.Transition(context.Background(), next)
	if !mission.IsKind(err, mission.KindPersistenceUnavailable) || strings.Contains(err.Error(), "seeded secret") {
		t.Fatalf("store failure disclosure err=%v", err)
	}
	store.mu.Lock()
	storedAfterFailure := store.snapshot
	store.mu.Unlock()
	if !reflect.DeepEqual(storedBeforeFailure, storedAfterFailure) {
		t.Fatal("failed commit changed authoritative state")
	}
}

func TestServiceRefusalDoesNotCallCommitPort(t *testing.T) {
	definition := mustDefinition(t)
	registry, _ := mission.NewRegistry(definition)
	for _, test := range []struct {
		name   string
		kind   mission.ErrorKind
		mutate func(*mission.MissionSnapshot)
	}{
		{name: "blocker", kind: mission.KindActiveBlocker, mutate: func(s *mission.MissionSnapshot) { s.ActiveBlockers = []ids.BlockerID{mustBlockerID(t, "blocker.port")} }},
		{name: "decision", kind: mission.KindUnresolvedDecision, mutate: func(s *mission.MissionSnapshot) {
			s.UnresolvedDecisions = []ids.DecisionID{mustDecisionID(t, "decision.port")}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			store.snapshot = snapshotAt(t, definition, mission.StepInitialized, 1)
			test.mutate(&store.snapshot)
			service := mission.NewService(registry, store)
			request := requestFor(t, definition, store.snapshot, mission.StepSpecifying, "refusal-"+test.name)
			if _, err := service.Transition(context.Background(), request); !mission.IsKind(err, test.kind) {
				t.Fatalf("transition err=%v", err)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.commitCalls != 0 {
				t.Fatalf("refusal reached commit port %d times", store.commitCalls)
			}
		})
	}
}

type memoryStore struct {
	mu          sync.Mutex
	snapshot    mission.MissionSnapshot
	createOps   map[ids.OperationID]storedCreate
	transitions map[ids.OperationID]storedTransition
	failCommit  error
	commitCalls int
}

type storedCreate struct {
	commitment mission.RequestCommitment
	result     mission.CreateResult
}
type storedTransition struct {
	commitment mission.RequestCommitment
	result     mission.TransitionResult
}

func newMemoryStore() *memoryStore {
	return &memoryStore{createOps: make(map[ids.OperationID]storedCreate), transitions: make(map[ids.OperationID]storedTransition)}
}

func (s *memoryStore) CreateMission(_ context.Context, commit mission.CreateCommit) (mission.CreateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.createOps[commit.OperationID]; ok {
		if prior.commitment != commit.RequestCommitment {
			return mission.CreateResult{}, domainError(mission.KindIdempotencyConflict)
		}
		result := prior.result
		result.Replayed = true
		return result, nil
	}
	if s.snapshot.MissionID != "" {
		return mission.CreateResult{}, domainError(mission.KindAlreadyExists)
	}
	s.snapshot = cloneSnapshot(commit.Instance)
	result := mission.CreateResult{Instance: cloneSnapshot(s.snapshot)}
	s.createOps[commit.OperationID] = storedCreate{commit.RequestCommitment, result}
	return result, nil
}

func (s *memoryStore) LoadMission(_ context.Context, id ids.MissionID) (mission.MissionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.MissionID == "" || s.snapshot.MissionID != id {
		return mission.MissionSnapshot{}, domainError(mission.KindNotFound)
	}
	return cloneSnapshot(s.snapshot), nil
}

func (s *memoryStore) ReplayTransition(_ context.Context, id ids.MissionID, operation ids.OperationID, commitment mission.RequestCommitment) (mission.TransitionResult, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, ok := s.transitions[operation]
	if !ok {
		return mission.TransitionResult{}, false, nil
	}
	if prior.result.Instance.MissionID != id {
		return mission.TransitionResult{}, false, domainError(mission.KindIdempotencyConflict)
	}
	if prior.commitment != commitment {
		return mission.TransitionResult{}, false, domainError(mission.KindIdempotencyConflict)
	}
	result := prior.result
	result.Instance = cloneSnapshot(result.Instance)
	result.Replayed = true
	return result, true, nil
}

func (s *memoryStore) CommitTransition(_ context.Context, commit mission.TransitionCommit) (mission.TransitionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitCalls++
	if prior, ok := s.transitions[commit.OperationID]; ok {
		if prior.commitment != commit.RequestCommitment {
			return mission.TransitionResult{}, domainError(mission.KindIdempotencyConflict)
		}
		result := prior.result
		result.Replayed = true
		return result, nil
	}
	if s.failCommit != nil {
		return mission.TransitionResult{}, s.failCommit
	}
	previous := commit.Candidate.Previous
	if s.snapshot.MissionID != previous.MissionID || s.snapshot.DefinitionCommitment != previous.DefinitionCommitment || s.snapshot.CurrentStep != previous.CurrentStep || s.snapshot.StateVersion != previous.StateVersion {
		return mission.TransitionResult{}, domainError(mission.KindStateVersionConflict)
	}
	if len(s.snapshot.ActiveBlockers) != 0 {
		return mission.TransitionResult{}, domainError(mission.KindActiveBlocker)
	}
	if len(s.snapshot.UnresolvedDecisions) != 0 {
		return mission.TransitionResult{}, domainError(mission.KindUnresolvedDecision)
	}
	s.snapshot = cloneSnapshot(commit.Candidate.Successor)
	result := mission.TransitionResult{Instance: cloneSnapshot(s.snapshot), Audit: commit.Candidate.Audit}
	s.transitions[commit.OperationID] = storedTransition{commit.RequestCommitment, result}
	return result, nil
}

func domainError(kind mission.ErrorKind) error {
	return &mission.Error{Kind: kind, Recovery: mission.RecoveryRefreshState, Field: "memory_store", Message: "bounded test-store refusal"}
}

func cloneSnapshot(s mission.MissionSnapshot) mission.MissionSnapshot {
	s.ActiveBlockers = append([]ids.BlockerID(nil), s.ActiveBlockers...)
	s.UnresolvedDecisions = append([]ids.DecisionID(nil), s.UnresolvedDecisions...)
	return s
}

func mustDefinition(t *testing.T) mission.MissionDefinition {
	t.Helper()
	d, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func definitionSpec(definition mission.MissionDefinition) mission.DefinitionSpec {
	steps := definition.Steps()
	specs := make([]mission.StepContractSpec, len(steps))
	for i := range steps {
		specs[i] = steps[i].Spec()
	}
	return mission.DefinitionSpec{ID: definition.ID(), Version: definition.Version(), Steps: specs, InitialStep: definition.InitialStep(), TerminalSteps: definition.TerminalSteps()}
}

func step(spec *mission.DefinitionSpec, key mission.StepKey) *mission.StepContractSpec {
	for i := range spec.Steps {
		if spec.Steps[i].Key == key {
			return &spec.Steps[i]
		}
	}
	panic("step fixture missing: " + key.String())
}

func reverse[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func fixtureTime() time.Time { return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC) }

func snapshotAt(t *testing.T, definition mission.MissionDefinition, key mission.StepKey, version uint64) mission.MissionSnapshot {
	t.Helper()
	snapshot := mission.MissionSnapshot{
		MissionID: mustMissionID(t, "mission.fixture"), ProjectID: mustProjectID(t, "project.fixture"),
		DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), DefinitionCommitment: definition.Commitment(),
		Lifecycle: mission.LifecycleOpen, CurrentStep: key, StateVersion: version,
		CreatedAt: fixtureTime(), UpdatedAt: fixtureTime().Add(time.Duration(version-1) * time.Second),
	}
	if definition.IsTerminal(key) {
		snapshot.Lifecycle = mission.LifecycleTerminal
		snapshot.TerminalOutcome = mission.OutcomeWorkFinalized
		snapshot.CurrentWorkRevisionID = mustWorkRevisionID(t, "revision.terminal")
	}
	return snapshot
}

func requestFor(t *testing.T, definition mission.MissionDefinition, current mission.MissionSnapshot, successor mission.StepKey, suffix string) mission.TransitionRequest {
	t.Helper()
	destination, _ := definition.Step(successor)
	request := mission.TransitionRequest{
		MissionID: current.MissionID, DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), DefinitionCommitment: definition.Commitment(),
		ExpectedStateVersion: current.StateVersion, Successor: successor,
		OperationID: mustOperationID(t, "operation."+suffix), EventID: mustEventID(t, "event."+suffix),
		ActorID: mustActorID(t, "actor.fixture"), GovernanceContextID: mustGovernanceID(t, "governance.fixture"),
		OccurredAt: current.UpdatedAt.Add(time.Second),
	}
	for index, kind := range destination.Spec().RequiredEvidence {
		request.Evidence = append(request.Evidence, mission.EvidenceRef{
			ID: mustEvidenceID(t, fmt.Sprintf("evidence.%s.%d", suffix, index)), Kind: kind, Status: mission.EvidenceAccepted,
			MissionID: current.MissionID, ForStep: current.CurrentStep, StateVersion: current.StateVersion,
		})
	}
	if successor == mission.StepWorkFinalized {
		request.WorkRevisionID = mustWorkRevisionID(t, "revision."+suffix)
	}
	return request
}

func cloneRequest(r mission.TransitionRequest) mission.TransitionRequest {
	r.Evidence = append([]mission.EvidenceRef(nil), r.Evidence...)
	return r
}

func mustInstance(t *testing.T, snapshot mission.MissionSnapshot) mission.MissionInstance {
	t.Helper()
	instance, err := mission.NewMissionInstance(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func mustMissionID(t *testing.T, v string) ids.MissionID {
	t.Helper()
	id, err := ids.ParseMissionID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustProjectID(t *testing.T, v string) ids.ProjectID {
	t.Helper()
	id, err := ids.ParseProjectID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustDefinitionID(t *testing.T, v string) ids.MissionDefinitionID {
	t.Helper()
	id, err := ids.ParseMissionDefinitionID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustOperationID(t *testing.T, v string) ids.OperationID {
	t.Helper()
	id, err := ids.ParseOperationID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustEventID(t *testing.T, v string) ids.MissionEventID {
	t.Helper()
	id, err := ids.ParseMissionEventID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustActorID(t *testing.T, v string) ids.ActorID {
	t.Helper()
	id, err := ids.ParseActorID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustGovernanceID(t *testing.T, v string) ids.GovernanceContextID {
	t.Helper()
	id, err := ids.ParseGovernanceContextID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustEvidenceID(t *testing.T, v string) ids.EvidenceID {
	t.Helper()
	id, err := ids.ParseEvidenceID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustWorkRevisionID(t *testing.T, v string) ids.WorkRevisionID {
	t.Helper()
	id, err := ids.ParseWorkRevisionID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustBlockerID(t *testing.T, v string) ids.BlockerID {
	t.Helper()
	id, err := ids.ParseBlockerID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustDecisionID(t *testing.T, v string) ids.DecisionID {
	t.Helper()
	id, err := ids.ParseDecisionID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
