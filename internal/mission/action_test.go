package mission

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestActionCommandIsSeparateGovernedAndReplaySafe(t *testing.T) {
	snapshot := nextSnapshotAt(t, nextActionDefinition(t), StepInitialized, 7)
	store := newActionMemoryStore(snapshot)
	var prepares atomic.Int64
	handler := ActionHandlerFunc(func(_ context.Context, c ActionCommand, s NextSnapshot) (ActionOutcome, error) {
		prepares.Add(1)
		return (AdvanceActionHandler{}).PrepareAction(context.Background(), c, s)
	})
	service := NewActionService(store, map[string]ActionHandler{"mission.plan": handler})
	command := actionCommandFixture(t, snapshot)
	first, err := service.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if first.Replayed || first.Outcome.Mission.StateVersion != 8 || store.commits != 1 || prepares.Load() != 1 {
		t.Fatalf("first result/store: %+v commits=%d prepares=%d", first, store.commits, prepares.Load())
	}
	reads := store.observes
	command.CorrelationID = "correlation-retry"
	replay, err := service.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Replayed || replay.Receipt != first.Receipt || store.observes != reads || store.commits != 1 || prepares.Load() != 1 {
		t.Fatal("exact replay performed mutable observation or preparation")
	}

	conflict := command
	conflict.PayloadDigest = strings.Repeat("b", 64)
	if _, err := service.Execute(context.Background(), conflict); !IsKind(err, KindIdempotencyConflict) {
		t.Fatalf("changed same operation: %v", err)
	}
	if store.observes != reads || store.commits != 1 || prepares.Load() != 1 {
		t.Fatal("operation conflict crossed mutation boundary")
	}
}

func TestActionCommandDigestBindsAuthorityAndCompleteExpectedStateButNotCorrelation(t *testing.T) {
	snapshot := nextSnapshotAt(t, nextActionDefinition(t), StepInitialized, 7)
	base := actionCommandFixture(t, snapshot)
	want, err := validateActionCommand(base)
	if err != nil {
		t.Fatal(err)
	}
	correlation := base
	correlation.CorrelationID = "different-correlation"
	got, _ := validateActionCommand(correlation)
	if got != want {
		t.Fatal("correlation became idempotency authority")
	}
	mutants := map[string]func(*ActionCommand){
		"operation": func(c *ActionCommand) { c.OperationID = "operation-other" }, "mission": func(c *ActionCommand) { c.MissionID = "mission-other"; c.ExpectedMission.MissionID = "mission-other" },
		"project": func(c *ActionCommand) { c.ExpectedMission.ProjectID = "project-other" }, "definition": func(c *ActionCommand) {
			c.ExpectedMission.DefinitionCommitment = DefinitionCommitment(strings.Repeat("c", 64))
		},
		"lifecycle": func(c *ActionCommand) {
			c.ExpectedMission.Lifecycle = LifecycleTerminal
			c.ExpectedMission.TerminalOutcome = OutcomeWorkFinalized
			c.ExpectedMission.CurrentStep = StepWorkFinalized
			c.ExpectedMission.CurrentWorkRevisionID = "revision-other"
		},
		"step": func(c *ActionCommand) { c.ExpectedMission.CurrentStep = StepSpecifying }, "revision": func(c *ActionCommand) {
			c.ExpectedMission.CurrentWorkRevisionID = "revision-other"
			c.ExpectedMission.CurrentStep = StepReconciliationRequired
		},
		"state": func(c *ActionCommand) { c.ExpectedMission.StateVersion++ }, "created": func(c *ActionCommand) { c.ExpectedMission.CreatedAt = c.ExpectedMission.CreatedAt.Add(-time.Second) }, "updated": func(c *ActionCommand) { c.ExpectedMission.UpdatedAt = c.ExpectedMission.UpdatedAt.Add(time.Second) },
		"blockers": func(c *ActionCommand) { c.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-x"} }, "decisions": func(c *ActionCommand) { c.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-x"} },
		"action": func(c *ActionCommand) { c.ActionKey = StepSpecifying.String() }, "successor": func(c *ActionCommand) { c.Successor = StepSpecifying }, "governance action": func(c *ActionCommand) { c.GovernanceAction = "mission.specify" },
		"actor": func(c *ActionCommand) { c.ActorID = "actor-other" }, "process": func(c *ActionCommand) { c.ProcessID = "process-other" }, "payload": func(c *ActionCommand) { c.PayloadDigest = strings.Repeat("d", 64) }, "time": func(c *ActionCommand) { c.OccurredAt = c.OccurredAt.Add(time.Second) },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			digest, err := validateActionCommand(candidate)
			if err == nil && digest == want {
				t.Fatal("semantic field not bound")
			}
		})
	}
	// Canonical mission reference sets remain set-valued without mutating caller order.
	base.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-b", "blocker-a"}
	base.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-b", "decision-a"}
	before := base.ExpectedMission.clone()
	left, _ := validateActionCommand(base)
	base.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-a", "blocker-b"}
	base.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-a", "decision-b"}
	right, _ := validateActionCommand(base)
	if left != right || !reflect.DeepEqual(before.ActiveBlockers, []ids.BlockerID{"blocker-b", "blocker-a"}) {
		t.Fatal("mission set identity is not canonical/defensive")
	}
}

func TestActionRefusesStaleBlockerDecisionAndWrongGovernanceBeforeMutation(t *testing.T) {
	definition := nextActionDefinition(t)
	snapshot := nextSnapshotAt(t, definition, StepInitialized, 7)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	decision, _ := NewDecision(DecisionParams{ID: "decision-action", MissionID: snapshot.Mission.MissionID, Step: snapshot.Mission.CurrentStep, Question: "Choose?", Mode: DecisionClosed, Options: []string{"a", "b"}, CreatedBy: "actor", GovernanceContextID: "governance", CreatedAt: now})
	blocker, _ := NewBlocker(BlockerParams{ID: "blocker-action", MissionID: snapshot.Mission.MissionID, Step: snapshot.Mission.CurrentStep, Kind: BlockerDependency, Summary: "Dependency unresolved.", RecoveryActions: []BlockerRecovery{RecoveryResolveDependency}, ObservedAt: now})
	tests := []struct {
		name           string
		mutateSnapshot func(*NextSnapshot)
		mutateCommand  func(*ActionCommand)
		kind           ErrorKind
	}{
		{"stale", func(s *NextSnapshot) { s.Mission.StateVersion++ }, nil, KindStateVersionConflict},
		{"decision", func(s *NextSnapshot) {
			s.Decisions = []Decision{decision}
			s.Mission.UnresolvedDecisions = []ids.DecisionID{decision.ID()}
		}, nil, KindUnresolvedDecision},
		{"blocker", func(s *NextSnapshot) {
			s.Blockers = []Blocker{blocker}
			s.Mission.ActiveBlockers = []ids.BlockerID{blocker.ID()}
		}, nil, KindActiveBlocker},
		{"wrong actor", nil, func(c *ActionCommand) { c.ActorID = "actor-other" }, KindInvalidRequest},
		{"unsupported", nil, func(c *ActionCommand) { c.GovernanceAction = "mission.specify" }, KindInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := snapshot
			if test.mutateSnapshot != nil {
				test.mutateSnapshot(&current)
			}
			store := newActionMemoryStore(current)
			var calls atomic.Int64
			service := NewActionService(store, map[string]ActionHandler{"mission.plan": ActionHandlerFunc(func(context.Context, ActionCommand, NextSnapshot) (ActionOutcome, error) {
				calls.Add(1)
				return ActionOutcome{}, nil
			})})
			command := actionCommandFixture(t, snapshot)
			if test.name == "decision" || test.name == "blocker" {
				command.ExpectedMission = current.Mission.clone()
			}
			if test.mutateCommand != nil {
				test.mutateCommand(&command)
			}
			_, err := service.Execute(context.Background(), command)
			if !IsKind(err, test.kind) {
				t.Fatalf("kind: %v", err)
			}
			if store.commits != 0 || calls.Load() != 0 {
				t.Fatal("refusal crossed mutation boundary")
			}
		})
	}
}

func TestActionConcurrentDuplicateCommitsOnceAndReceiptDetectsSubstitution(t *testing.T) {
	snapshot := nextSnapshotAt(t, nextActionDefinition(t), StepInitialized, 7)
	store := newActionMemoryStore(snapshot)
	service := NewActionService(store, map[string]ActionHandler{"mission.plan": AdvanceActionHandler{}})
	command := actionCommandFixture(t, snapshot)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.Execute(context.Background(), command); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if store.commits != 1 {
		t.Fatalf("commits=%d", store.commits)
	}

	for name, mutate := range map[string]func(*ActionResult){"mission": func(r *ActionResult) { r.Outcome.Mission.StateVersion++ }, "receipt": func(r *ActionResult) { r.Receipt = ActionReceipt(strings.Repeat("f", 64)) }, "digest": func(r *ActionResult) { r.RequestDigest = strings.Repeat("e", 64) }, "operation": func(r *ActionResult) { r.OperationID = "operation-other" }} {
		t.Run(name, func(t *testing.T) {
			bad := newActionMemoryStore(snapshot)
			first, err := serviceResultForTest(command, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&first)
			bad.results[command.OperationID] = first
			bad.ignoreDigest = true
			svc := NewActionService(bad, map[string]ActionHandler{"mission.plan": AdvanceActionHandler{}})
			if _, err := svc.Execute(context.Background(), command); !IsKind(err, KindPersistenceContract) {
				t.Fatalf("substitute accepted: %v", err)
			}
			if bad.observes != 0 || bad.commits != 0 {
				t.Fatal("replay substitute caused mutable read")
			}
		})
	}
}

func TestActionGovernanceHistorySurvivesNewContexts(t *testing.T) {
	snapshot := nextSnapshotAt(t, nextActionDefinition(t), StepInitialized, 7)
	command := actionCommandFixture(t, snapshot)
	encoded, err := json.Marshal(command.Governance)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		_ = actionGovernanceContext(t, ids.GovernanceContextID("governance-new-"+string(rune('a'+i%26))), "profile-r2")
		var restored governancecontext.MissionAction
		if err := json.Unmarshal(encoded, &restored); err != nil || !restored.Equal(command.Governance) {
			t.Fatalf("reconstruction %d: %v", i, err)
		}
	}
}

func TestDecisionAndBlockerActionsCommitFactAndMissionCASOnce(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"decision", "blocker"} {
		t.Run(kind, func(t *testing.T) {
			snapshot := nextSnapshotAt(t, nextActionDefinition(t), StepInitialized, 7)
			command := actionCommandFixture(t, snapshot)
			handlers := map[string]ActionHandler{}
			if kind == "decision" {
				fact, _ := NewDecision(DecisionParams{ID: "decision-action", MissionID: snapshot.Mission.MissionID, Step: snapshot.Mission.CurrentStep, Question: "Choose?", Mode: DecisionClosed, Options: []string{"a", "b"}, CreatedBy: "actor", GovernanceContextID: "governance", CreatedAt: now})
				snapshot.Decisions = []Decision{fact}
				snapshot.Mission.UnresolvedDecisions = []ids.DecisionID{fact.ID()}
				command.ExpectedMission = snapshot.Mission.clone()
				command.ActionKey = fact.ID().String()
				command.Successor = snapshot.Mission.CurrentStep
				command.GovernanceAction = "mission.resolve-decision"
				command.Governance = actionGovernance(t, snapshot.Mission, command.GovernanceAction)
				handlers[command.GovernanceAction] = DecisionActionHandler{Transition: DecisionTransition{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: DecisionResolved, Answer: "a", Rationale: "Approved selection.", ActorID: command.ActorID, GovernanceContextID: command.Governance.ContextID(), OccurredAt: command.OccurredAt}}
			} else {
				fact, _ := NewBlocker(BlockerParams{ID: "blocker-action", MissionID: snapshot.Mission.MissionID, Step: snapshot.Mission.CurrentStep, Kind: BlockerDependency, Summary: "Dependency unresolved.", RecoveryActions: []BlockerRecovery{RecoveryResolveDependency}, ObservedAt: now})
				snapshot.Blockers = []Blocker{fact}
				snapshot.Mission.ActiveBlockers = []ids.BlockerID{fact.ID()}
				command.ExpectedMission = snapshot.Mission.clone()
				command.ActionKey = fact.ID().String()
				command.Successor = snapshot.Mission.CurrentStep
				command.GovernanceAction = "mission.clear-blocker"
				command.Governance = actionGovernance(t, snapshot.Mission, command.GovernanceAction)
				handlers[command.GovernanceAction] = BlockerActionHandler{Clear: BlockerClear{ExpectedStatus: BlockerActive, ExpectedVersion: 1, ActorID: command.ActorID, GovernanceContextID: command.Governance.ContextID(), Reason: "Dependency recovered.", OccurredAt: command.OccurredAt}}
			}
			store := newActionMemoryStore(snapshot)
			result, err := NewActionService(store, handlers).Execute(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome.Mission.StateVersion != 8 || store.commits != 1 {
				t.Fatalf("aggregate CAS: %+v", result)
			}
			if kind == "decision" && (result.Outcome.Decision == nil || len(result.Outcome.Mission.UnresolvedDecisions) != 0) {
				t.Fatal("decision fact/reference not atomic")
			}
			if kind == "blocker" && (result.Outcome.Blocker == nil || len(result.Outcome.Mission.ActiveBlockers) != 0) {
				t.Fatal("blocker fact/reference not atomic")
			}
		})
	}
}

func actionCommandFixture(t *testing.T, s NextSnapshot) ActionCommand {
	t.Helper()
	governance := actionGovernance(t, s.Mission, "mission.plan")
	return ActionCommand{SchemaVersion: 1, CorrelationID: "correlation-1", OperationID: "operation-action", MissionID: s.Mission.MissionID, ExpectedMission: s.Mission.clone(), ActionKey: StepPlanning.String(), Successor: StepPlanning, GovernanceAction: "mission.plan", ActorID: "actor-action", ProcessID: "process-action", Governance: governance, PayloadDigest: strings.Repeat("a", 64), OccurredAt: s.Mission.UpdatedAt.Add(time.Minute)}
}
func actionGovernance(t *testing.T, s MissionSnapshot, action string) governancecontext.MissionAction {
	t.Helper()
	source := actionGovernanceContext(t, "governance-action", "profile-r1")
	selected := source.Entries()
	if strings.Contains(action, "decision") || action == "mission.clear-blocker" {
		minimal := make([]governancecontext.Entry, 0, 3)
		for _, entry := range selected {
			if entry.Kind() != governancecontext.KindConfig {
				minimal = append(minimal, entry)
			}
		}
		selected = minimal
	}
	value, err := governancecontext.NewMissionAction(governancecontext.MissionActionParams{Source: source, MissionID: s.MissionID, StepKey: s.CurrentStep.String(), ActionKey: action, ActorID: "actor-action", SelectedEntries: selected})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func actionGovernanceContext(t *testing.T, id ids.GovernanceContextID, profileRevision string) governancecontext.Context {
	t.Helper()
	entry := func(kind governancecontext.EntryKind, ref, revision, content string) governancecontext.Entry {
		commit, _ := governancecontext.CommitSHA256([]byte(content))
		value, err := governancecontext.NewEntry(kind, ref, revision, commit)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	value, err := governancecontext.NewContext(id, governancecontext.SchemaVersion, []governancecontext.Entry{entry(governancecontext.KindConfig, "config.default", "config-r1", "config"), entry(governancecontext.KindDoctrine, "doctrine.core", "doctrine-r1", "doctrine"), entry(governancecontext.KindProfile, "profile.implementer", profileRevision, profileRevision), entry(governancecontext.KindMissionType, "mission-type.software-development", "v1", "mission-type")}, time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type actionMemoryStore struct {
	mu           sync.Mutex
	snapshot     NextSnapshot
	results      map[ids.OperationID]ActionResult
	observes     int
	commits      int
	ignoreDigest bool
}

func newActionMemoryStore(s NextSnapshot) *actionMemoryStore {
	return &actionMemoryStore{snapshot: s, results: map[ids.OperationID]ActionResult{}}
}
func (s *actionMemoryStore) ReplayAction(_ context.Context, _ ids.MissionID, op ids.OperationID, digest string) (ActionResult, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[op]
	if !ok {
		return ActionResult{}, false, nil
	}
	if r.RequestDigest != digest && !s.ignoreDigest {
		return ActionResult{}, false, refusal(KindIdempotencyConflict, RecoveryCorrectInput, "operation_id", "operation content conflicts")
	}
	return cloneActionResult(r), true, nil
}
func (s *actionMemoryStore) ObserveAction(context.Context, ids.MissionID) (NextSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observes++
	return cloneNextSnapshot(s.snapshot), nil
}
func (s *actionMemoryStore) CommitAction(_ context.Context, c ActionCommit) (ActionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.results[c.OperationID]; ok {
		if prior.RequestDigest != c.RequestDigest {
			return ActionResult{}, refusal(KindIdempotencyConflict, RecoveryCorrectInput, "operation_id", "operation content conflicts")
		}
		prior.Replayed = true
		return cloneActionResult(prior), nil
	}
	if !sameMissionSnapshot(s.snapshot.Mission, c.Expected) {
		return ActionResult{}, refusal(KindStateVersionConflict, RecoveryRefreshState, "expected_mission", "mission changed")
	}
	s.commits++
	s.snapshot.Mission = c.Outcome.Mission.clone()
	r := ActionResult{OperationID: c.OperationID, MissionID: c.MissionID, ActionKey: c.ActionKey, RequestDigest: c.RequestDigest, Outcome: cloneActionOutcome(c.Outcome), Receipt: c.Receipt}
	s.results[c.OperationID] = r
	return cloneActionResult(r), nil
}
func serviceResultForTest(c ActionCommand, snapshot NextSnapshot) (ActionResult, error) {
	digest, err := validateActionCommand(c)
	if err != nil {
		return ActionResult{}, err
	}
	outcome, err := (AdvanceActionHandler{}).PrepareAction(context.Background(), c, snapshot)
	if err != nil {
		return ActionResult{}, err
	}
	receipt, err := computeActionReceipt(c.OperationID, c.MissionID, c.ActionKey, digest, outcome)
	return ActionResult{OperationID: c.OperationID, MissionID: c.MissionID, ActionKey: c.ActionKey, RequestDigest: digest, Outcome: outcome, Receipt: receipt}, err
}
