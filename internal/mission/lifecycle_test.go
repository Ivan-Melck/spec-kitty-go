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

	"github.com/spec-kitty/go-kitty3/internal/artifact"
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
		name        string
		mutate      func(*mission.DefinitionSpec)
		wantField   string
		wantMessage string
		secret      string
	}{
		{name: "duplicate-step", mutate: func(s *mission.DefinitionSpec) { s.Steps = append(s.Steps, s.Steps[0]) },
			wantField: "definition.duplicate_step", wantMessage: "step decomposing violates duplicate_step"},
		{name: "invalid-initial", mutate: func(s *mission.DefinitionSpec) { s.InitialStep = mission.StepKey("seeded-initial-secret/value") },
			wantField: "definition.invalid_initial_step", wantMessage: "field initial_step violates invalid_initial_step", secret: "seeded-initial-secret"},
		{name: "reserved-initial", mutate: func(s *mission.DefinitionSpec) { s.InitialStep = mission.StepReviewReserved },
			wantField: "definition.invalid_initial_step", wantMessage: "step review_reserved violates invalid_initial_step"},
		{name: "absent-initial", mutate: func(s *mission.DefinitionSpec) {
			s.InitialStep = mission.StepPlanning
			removeStep(s, mission.StepPlanning)
		}, wantField: "definition.unknown_initial_step", wantMessage: "step planning violates unknown_initial_step"},
		{name: "missing-terminal", mutate: func(s *mission.DefinitionSpec) { s.TerminalSteps = nil },
			wantField: "definition.missing_terminal_step", wantMessage: "field terminal_steps violates missing_terminal_step"},
		{name: "invalid-terminal", mutate: func(s *mission.DefinitionSpec) { s.TerminalSteps = []mission.StepKey{"seeded-terminal-secret/value"} },
			wantField: "definition.invalid_terminal_step", wantMessage: "terminal_steps index 0 violates invalid_terminal_step", secret: "seeded-terminal-secret"},
		{name: "reserved-terminal", mutate: func(s *mission.DefinitionSpec) { s.TerminalSteps = []mission.StepKey{mission.StepReviewReserved} },
			wantField: "definition.invalid_terminal_step", wantMessage: "step review_reserved violates invalid_terminal_step"},
		{name: "duplicate-terminal", mutate: func(s *mission.DefinitionSpec) { s.TerminalSteps = append(s.TerminalSteps, mission.StepWorkFinalized) },
			wantField: "definition.duplicate_terminal_step", wantMessage: "step work_finalized violates duplicate_terminal_step"},
		{name: "absent-terminal", mutate: func(s *mission.DefinitionSpec) {
			s.TerminalSteps = []mission.StepKey{mission.StepPlanning}
			removeStep(s, mission.StepPlanning)
		}, wantField: "definition.unknown_terminal_step", wantMessage: "step planning violates unknown_terminal_step"},
		{name: "unknown-successor", mutate: func(s *mission.DefinitionSpec) {
			removeStep(s, mission.StepPlanning)
			step(s, mission.StepPlanApproved).Predecessors = nil
		}, wantField: "definition.unknown_successor", wantMessage: "edge specification_approved -> planning violates unknown_successor"},
		{name: "unknown-predecessor", mutate: func(s *mission.DefinitionSpec) {
			removeStep(s, mission.StepPlanning)
			step(s, mission.StepSpecificationApproved).Successors = nil
		}, wantField: "definition.unknown_predecessor", wantMessage: "edge planning -> plan_approved violates unknown_predecessor"},
		{name: "unsupported-successor", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepInitialized).Successors = append(step(s, mission.StepInitialized).Successors, "seeded-successor-secret/value")
		}, wantField: "step_contract.unsupported_adjacent_step", wantMessage: "step initialized successor index 1 violates unsupported_adjacent_step", secret: "seeded-successor-secret"},
		{name: "unsupported-predecessor", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepSpecifying).Predecessors = []mission.StepKey{"seeded-predecessor-secret/value"}
		}, wantField: "step_contract.unsupported_adjacent_step", wantMessage: "step specifying predecessor index 0 violates unsupported_adjacent_step", secret: "seeded-predecessor-secret"},
		{name: "reserved-successor", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepInitialized).Successors = []mission.StepKey{mission.StepReviewReserved}
		}, wantField: "step_contract.unsupported_adjacent_step", wantMessage: "edge initialized -> review_reserved violates unsupported_adjacent_step"},
		{name: "reserved-predecessor", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepSpecifying).Predecessors = []mission.StepKey{mission.StepReviewReserved}
		}, wantField: "step_contract.unsupported_adjacent_step", wantMessage: "edge review_reserved -> specifying violates unsupported_adjacent_step"},
		{name: "asymmetric-edge", mutate: func(s *mission.DefinitionSpec) { step(s, mission.StepSpecifying).Predecessors = nil },
			wantField: "definition.asymmetric_edge", wantMessage: "edge initialized -> specifying violates asymmetric_edge"},
		{name: "self-edge", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepSpecifying).Successors = []mission.StepKey{mission.StepSpecifying}
		}, wantField: "step_contract.self_edge", wantMessage: "edge specifying -> specifying violates self_edge"},
		{name: "duplicate-edge", mutate: func(s *mission.DefinitionSpec) {
			p := step(s, mission.StepInitialized)
			p.Successors = append(p.Successors, p.Successors[0])
		}, wantField: "step_contract.duplicate_successor", wantMessage: "step initialized violates duplicate_successor"},
		{name: "duplicate-predecessor", mutate: func(s *mission.DefinitionSpec) {
			p := step(s, mission.StepSpecifying)
			p.Predecessors = append(p.Predecessors, p.Predecessors[0])
		}, wantField: "step_contract.duplicate_predecessor", wantMessage: "step specifying violates duplicate_predecessor"},
		{name: "disconnected", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepInitialized).Successors = nil
			step(s, mission.StepSpecifying).Predecessors = nil
		}, wantField: "definition.unreachable_step", wantMessage: "step decomposing violates unreachable_step"},
		{name: "cycle", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepWorkDraftReady).Successors = append(step(s, mission.StepWorkDraftReady).Successors, mission.StepSpecifying)
			step(s, mission.StepSpecifying).Predecessors = append(step(s, mission.StepSpecifying).Predecessors, mission.StepWorkDraftReady)
		}, wantField: "definition.cycle", wantMessage: "edge work_draft_ready -> specifying violates cycle"},
		{name: "terminal-successor", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepWorkFinalized).Successors = []mission.StepKey{mission.StepSpecifying}
			step(s, mission.StepSpecifying).Predecessors = append(step(s, mission.StepSpecifying).Predecessors, mission.StepWorkFinalized)
		}, wantField: "definition.terminal_has_successors", wantMessage: "step work_finalized violates terminal_has_successors"},
		{name: "invalid-terminal-outcome", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepWorkFinalized).TerminalOutcome = mission.TerminalOutcome("seeded-outcome-secret")
		}, wantField: "definition.invalid_terminal_outcome", wantMessage: "step work_finalized violates invalid_terminal_outcome", secret: "seeded-outcome-secret"},
		{name: "nonterminal-outcome", mutate: func(s *mission.DefinitionSpec) {
			step(s, mission.StepPlanning).TerminalOutcome = mission.OutcomeWorkFinalized
		}, wantField: "definition.nonterminal_outcome", wantMessage: "step planning violates nonterminal_outcome"},
		{name: "mutating-without-governance", mutate: func(s *mission.DefinitionSpec) { step(s, mission.StepPlanning).GovernanceAction = "" },
			wantField: "step_contract.invalid_governance_action", wantMessage: "step planning violates invalid_governance_action"},
		{name: "invalid-caller-key", mutate: func(s *mission.DefinitionSpec) { s.Steps[0].Key = mission.StepKey("seeded-step-secret/value") },
			wantField: "step_contract.unsupported_key", wantMessage: "step index 0 violates unsupported_key", secret: "seeded-step-secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := definitionSpec(mustDefinition(t))
			test.mutate(&spec)
			_, err := mission.NewDefinition(spec)
			if !mission.IsKind(err, mission.KindDefinitionMalformed) {
				t.Fatalf("mutant=%s err=%v", test.name, err)
			}
			var diagnostic *mission.Error
			if !errors.As(err, &diagnostic) || diagnostic.Field != test.wantField || diagnostic.Message != test.wantMessage {
				t.Fatalf("mutant=%s diagnostic=%+v want field=%q message=%q", test.name, diagnostic, test.wantField, test.wantMessage)
			}
			if diagnostic.Validate() != nil || len(err.Error()) > 256 || test.secret != "" && strings.Contains(err.Error(), test.secret) {
				t.Fatalf("mutant=%s unbounded or disclosed diagnostic=%v", test.name, err)
			}
		})
	}
}

func TestEvaluateTransitionRequiresExactExpectedSnapshot(t *testing.T) {
	t.Parallel()

	definition := mustDefinition(t)
	base := snapshotAt(t, definition, mission.StepInitialized, 5)
	tests := []struct {
		name    string
		current mission.MissionSnapshot
		mutate  func(*mission.TransitionRequest)
	}{
		{name: "project", current: func() mission.MissionSnapshot {
			current := cloneSnapshot(base)
			current.ProjectID = mustProjectID(t, "project.instance-only")
			return current
		}()},
		{name: "created-at", current: base, mutate: func(request *mission.TransitionRequest) {
			request.ExpectedSnapshot.CreatedAt = request.ExpectedSnapshot.CreatedAt.Add(-time.Hour)
		}},
		{name: "updated-at", current: func() mission.MissionSnapshot {
			current := cloneSnapshot(base)
			current.UpdatedAt = current.UpdatedAt.Add(time.Millisecond)
			return current
		}()},
		{name: "active-blockers", current: func() mission.MissionSnapshot {
			current := cloneSnapshot(base)
			current.ActiveBlockers = []ids.BlockerID{mustBlockerID(t, "blocker.instance-only")}
			return current
		}()},
		{name: "unresolved-decisions", current: base, mutate: func(request *mission.TransitionRequest) {
			request.ExpectedSnapshot.UnresolvedDecisions = []ids.DecisionID{mustDecisionID(t, "decision.expected-only")}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestFor(t, definition, base, mission.StepSpecifying, "snapshot-cas-"+test.name)
			if test.mutate != nil {
				test.mutate(&request)
			}
			_, err := mission.EvaluateTransition(mustInstance(t, test.current), definition, request)
			assertSnapshotConflict(t, err)
		})
	}

	reconciliation := snapshotAt(t, definition, mission.StepReconciliationRequired, 8)
	request := requestFor(t, definition, reconciliation, mission.StepWorkFinalized, "snapshot-cas-work-revision")
	reconciliation.CurrentWorkRevisionID = mustWorkRevisionID(t, "revision.instance-only")
	_, err := mission.EvaluateTransition(mustInstance(t, reconciliation), definition, request)
	assertSnapshotConflict(t, err)

	invalidLifecycle := requestFor(t, definition, base, mission.StepSpecifying, "snapshot-cas-lifecycle")
	invalidLifecycle.ExpectedSnapshot.Lifecycle = mission.LifecycleTerminal
	invalidLifecycle.ExpectedSnapshot.TerminalOutcome = mission.OutcomeWorkFinalized
	if _, err := mission.EvaluateTransition(mustInstance(t, base), definition, invalidLifecycle); !mission.IsKind(err, mission.KindInvalidRequest) {
		t.Fatalf("invalid lifecycle expectation err=%v", err)
	}
}

func assertSnapshotConflict(t *testing.T, err error) {
	t.Helper()
	var diagnostic *mission.Error
	if !errors.As(err, &diagnostic) || diagnostic.Kind != mission.KindStateVersionConflict ||
		diagnostic.Recovery != mission.RecoveryRefreshState || diagnostic.Field != "expected_snapshot" ||
		diagnostic.Message != "mission state changed" {
		t.Fatalf("snapshot mismatch diagnostic=%+v rendered=%v", diagnostic, err)
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
			r.ExpectedSnapshot.DefinitionCommitment = r.DefinitionCommitment
		}},
		{name: "stale-version", kind: mission.KindStateVersionConflict, mutateRequest: func(r *mission.TransitionRequest) {
			r.ExpectedStateVersion--
			r.ExpectedSnapshot.StateVersion = r.ExpectedStateVersion
		}},
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
				candidateRequest.ExpectedSnapshot = cloneSnapshot(current)
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
		OperationID: mustOperationID(t, "operation.create"), ActorID: mustActorID(t, "actor.create"),
		ProcessID: ids.ProcessID("process.create"), Governance: serviceGovernance(t, "governance.create", createdAt), OccurredAt: createdAt,
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
	laterRequest := requestFor(t, definition, storedBeforeFailure, mission.StepSpecificationApproved, "later-transition")
	laterRequest.Evidence = withoutApprovalEvidence(laterRequest.Evidence)
	approvalSource := freshArtifactApprovalSource(t, laterRequest.MissionID, artifact.KindSpecification, "later-transition")
	store.artifactSource = approvalSource
	laterService := mission.NewServiceWithArtifactApprovals(registry, store, approvalSource)
	later, err := laterService.Transition(context.Background(), laterRequest)
	if err != nil {
		t.Fatal(err)
	}
	historicalReplay, err := service.Transition(context.Background(), winningRequest)
	if err != nil || !historicalReplay.Replayed || !reflect.DeepEqual(historicalReplay, exactReplay) {
		t.Fatalf("historical replay=%+v err=%v", historicalReplay, err)
	}

	store.mu.Lock()
	store.failCommit = errors.New("seeded secret must not escape")
	store.mu.Unlock()
	next := requestFor(t, definition, later.Instance, mission.StepPlanning, "store-failure")
	_, err = service.Transition(context.Background(), next)
	if !mission.IsKind(err, mission.KindPersistenceUnavailable) || strings.Contains(err.Error(), "seeded secret") {
		t.Fatalf("store failure disclosure err=%v", err)
	}
	store.mu.Lock()
	storedAfterFailure := store.snapshot
	store.mu.Unlock()
	if !reflect.DeepEqual(later.Instance, storedAfterFailure) {
		t.Fatal("failed commit changed authoritative state")
	}
}

func TestServiceDerivesOnlyExactArtifactApprovalEvidence(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		current      mission.StepKey
		successor    mission.StepKey
		artifactKind artifact.Kind
	}{
		{name: "specification", current: mission.StepSpecifying, successor: mission.StepSpecificationApproved, artifactKind: artifact.KindSpecification},
		{name: "plan", current: mission.StepPlanning, successor: mission.StepPlanApproved, artifactKind: artifact.KindPlan},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := snapshotAt(t, definition, test.current, 7)
			store := newMemoryStore()
			store.snapshot = current
			source := freshArtifactApprovalSource(t, current.MissionID, test.artifactKind, "exact-"+test.name)
			store.artifactSource = source
			service := mission.NewServiceWithArtifactApprovals(registry, store, source)
			request := requestFor(t, definition, current, test.successor, "exact-"+test.name)
			request.Evidence = withoutApprovalEvidence(request.Evidence)

			result, err := service.Transition(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Instance.CurrentStep != test.successor || result.Audit.ApprovalEvidenceID.String() != source.approvals[0].ID() {
				t.Fatalf("transition=%+v approval=%q", result, source.approvals[0].ID())
			}
			if source.observationCalls != 1 {
				t.Fatalf("artifact source observations=%d", source.observationCalls)
			}
			if source.observedMissionID != current.MissionID.String() || source.observedKind != test.artifactKind {
				t.Fatalf("artifact source scope=(%s,%s)", source.observedMissionID, source.observedKind)
			}
			replay, err := service.Transition(context.Background(), request)
			if err != nil || !replay.Replayed || replay.Audit != result.Audit {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
			if source.observationCalls != 1 {
				t.Fatal("durable replay re-read mutable artifact authority")
			}
		})
	}
}

func TestServiceRefusesMissingRejectedStaleAndForgedArtifactApprovals(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	current := snapshotAt(t, definition, mission.StepSpecifying, 9)
	baseRequest := requestFor(t, definition, current, mission.StepSpecificationApproved, "artifact-refusal")
	callerApproval := baseRequest.Evidence[len(baseRequest.Evidence)-1]
	baseRequest.Evidence = withoutApprovalEvidence(baseRequest.Evidence)
	fresh := freshArtifactApprovalSource(t, current.MissionID, artifact.KindSpecification, "refusal")
	rejected := *fresh
	rejected.approvals = []artifact.ArtifactApproval{mustArtifactApproval(t, "approval.rejected", rejected.revision.ID(), artifact.VerdictRejected)}
	stale := *fresh
	stale.approvals = []artifact.ArtifactApproval{mustArtifactApproval(t, "approval.stale", "artifact.historical", artifact.VerdictApproved)}
	missingRevision := *fresh
	missingRevision.currentError = &artifact.Error{Kind: artifact.ErrorNotFound, Field: "artifact_revision_id", Recovery: artifact.RecoveryProvideEvidence, Message: "artifact revision was not found"}
	missingApproval := *fresh
	missingApproval.approvals = nil
	mismatched := *fresh
	mismatched.revision = mustArtifactRevision(t, mustMissionID(t, "mission.another"), artifact.KindSpecification, "mismatched")

	tests := []struct {
		name      string
		source    mission.ArtifactApprovalSource
		mutate    func(*mission.TransitionRequest)
		wantKind  mission.ErrorKind
		wantField string
	}{
		{name: "source-absent", wantKind: mission.KindMissingEvidence, wantField: "artifact_approval"},
		{name: "revision-missing", source: &missingRevision, wantKind: mission.KindMissingEvidence, wantField: "artifact_approval"},
		{name: "approval-missing", source: &missingApproval, wantKind: mission.KindMissingEvidence, wantField: "artifact_approval"},
		{name: "rejected", source: &rejected, wantKind: mission.KindApprovalStale, wantField: "artifact_approval"},
		{name: "stale", source: &stale, wantKind: mission.KindApprovalStale, wantField: "artifact_approval"},
		{name: "source-scope-mismatch", source: &mismatched, wantKind: mission.KindPersistenceContract, wantField: "artifact_approval"},
		{name: "forged-caller-evidence", source: fresh, mutate: func(request *mission.TransitionRequest) {
			request.Evidence = append(request.Evidence, callerApproval)
		}, wantKind: mission.KindInvalidRequest, wantField: "evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			store.snapshot = current
			request := cloneRequest(baseRequest)
			if test.mutate != nil {
				test.mutate(&request)
			}
			service := mission.NewServiceWithArtifactApprovals(registry, store, test.source)
			_, err := service.Transition(context.Background(), request)
			var diagnostic *mission.Error
			if !errors.As(err, &diagnostic) || diagnostic.Kind != test.wantKind || diagnostic.Field != test.wantField {
				t.Fatalf("diagnostic=%+v rendered=%v", diagnostic, err)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.commitCalls != 0 {
				t.Fatalf("refusal reached commit port %d times", store.commitCalls)
			}
		})
	}
	if fresh.observationCalls != 0 {
		t.Fatal("forged caller evidence reached artifact authority")
	}
}

func TestServiceRejectsSuccessorPublicationAtEveryApprovalCommitBoundary(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []string{"before-aggregate-observation", "after-observation-before-commit", "inside-commit-boundary"} {
		t.Run(boundary, func(t *testing.T) {
			current := snapshotAt(t, definition, mission.StepSpecifying, 12)
			store := newMemoryStore()
			store.snapshot = current
			source := freshArtifactApprovalSource(t, current.MissionID, artifact.KindSpecification, "toctou-r1-"+boundary)
			store.artifactSource = source
			publishSuccessor := func(source *artifactApprovalSource) {
				source.revision = mustArtifactRevision(t, current.MissionID, artifact.KindSpecification, "toctou-r2-"+boundary)
			}
			switch boundary {
			case "before-aggregate-observation":
				source.beforeObserve = publishSuccessor
			case "after-observation-before-commit":
				source.afterObserve = publishSuccessor
			case "inside-commit-boundary":
				store.beforeAuthorityCheck = func() { publishSuccessor(source) }
			}
			request := requestFor(t, definition, current, mission.StepSpecificationApproved, "toctou-"+boundary)
			request.Evidence = withoutApprovalEvidence(request.Evidence)
			service := mission.NewServiceWithArtifactApprovals(registry, store, source)

			_, err := service.Transition(context.Background(), request)
			if !mission.IsKind(err, mission.KindApprovalStale) {
				t.Fatalf("publication boundary advanced with err=%v", err)
			}
			if source.observationCalls != 1 {
				t.Fatalf("boundary used non-aggregate or best-effort rereads observations=%d", source.observationCalls)
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			if !reflect.DeepEqual(store.snapshot, current) || len(store.transitions) != 0 {
				t.Fatalf("stale approval changed mission state snapshot=%+v transitions=%d", store.snapshot, len(store.transitions))
			}
		})
	}
}

func TestApprovalReplayRejectsMissingAndSubstitutedImmutableAuthority(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	current := snapshotAt(t, definition, mission.StepSpecifying, 15)
	base := newMemoryStore()
	base.snapshot = current
	source := freshArtifactApprovalSource(t, current.MissionID, artifact.KindSpecification, "replay-authority")
	base.artifactSource = source
	service := mission.NewServiceWithArtifactApprovals(registry, base, source)
	request := requestFor(t, definition, current, mission.StepSpecificationApproved, "replay-authority")
	request.Evidence = withoutApprovalEvidence(request.Evidence)
	committed, err := service.Transition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !committed.Authority.Required() || committed.Audit.AuthorityCommitment != committed.Authority.Commitment() {
		t.Fatalf("commit omitted immutable authority: %+v", committed)
	}

	alteredRevisionEvidence, err := artifact.NewCurrentApprovalEvidence(
		current.MissionID.String(), artifact.KindSpecification, "artifact.substituted", committed.Authority.ArtifactApproval().ApprovalID(),
	)
	if err != nil {
		t.Fatal(err)
	}
	alteredRevisionAuthority, err := mission.NewTransitionAuthority(committed.Authority.RequestCommitment(), alteredRevisionEvidence)
	if err != nil {
		t.Fatal(err)
	}
	alteredApprovalEvidence, err := artifact.NewCurrentApprovalEvidence(
		current.MissionID.String(), artifact.KindSpecification, committed.Authority.ArtifactApproval().ArtifactRevisionID(), "approval.substituted",
	)
	if err != nil {
		t.Fatal(err)
	}
	alteredApprovalAuthority, err := mission.NewTransitionAuthority(committed.Authority.RequestCommitment(), alteredApprovalEvidence)
	if err != nil {
		t.Fatal(err)
	}

	mutants := []struct {
		name   string
		mutate func(*mission.TransitionResult)
	}{
		{name: "missing-authority", mutate: func(result *mission.TransitionResult) { result.Authority = mission.TransitionAuthority{} }},
		{name: "altered-authority-revision", mutate: func(result *mission.TransitionResult) { result.Authority = alteredRevisionAuthority }},
		{name: "altered-authority-approval", mutate: func(result *mission.TransitionResult) { result.Authority = alteredApprovalAuthority }},
		{name: "altered-audit-revision", mutate: func(result *mission.TransitionResult) { result.Audit.ArtifactRevisionID = "artifact.substituted" }},
		{name: "altered-audit-approval", mutate: func(result *mission.TransitionResult) {
			result.Audit.ApprovalEvidenceID = mustEvidenceID(t, "approval.substituted")
		}},
		{name: "altered-audit-commitment", mutate: func(result *mission.TransitionResult) {
			result.Audit.AuthorityCommitment = mission.TransitionAuthorityCommitment(strings.Repeat("a", 64))
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			wrong := &mutatingStore{memoryStore: base, replayMutator: mutant.mutate}
			_, err := mission.NewServiceWithArtifactApprovals(registry, wrong, source).Transition(context.Background(), request)
			if !mission.IsKind(err, mission.KindPersistenceContract) {
				t.Fatalf("substituted replay authority accepted: %v", err)
			}
		})
	}
	replay, err := service.Transition(context.Background(), request)
	if err != nil || !replay.Replayed || replay.Authority != committed.Authority || replay.Audit != committed.Audit {
		t.Fatalf("exact durable replay=%+v err=%v", replay, err)
	}
	if source.observationCalls != 1 {
		t.Fatal("replay consulted mutable artifact state")
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

func TestServiceRejectsStoreResultAuthorityMutants(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*mission.CreateResult)
	}{
		{name: "another-mission", mutate: func(result *mission.CreateResult) {
			result.Instance.MissionID = mustMissionID(t, "mission.store-mutant")
		}},
		{name: "another-definition", mutate: func(result *mission.CreateResult) {
			result.Instance.DefinitionID = mustDefinitionID(t, "definition.store-mutant")
		}},
	} {
		t.Run("create-"+test.name, func(t *testing.T) {
			store := &mutatingStore{memoryStore: newMemoryStore(), createMutator: test.mutate}
			service := mission.NewService(registry, store)
			_, err := service.Create(context.Background(), createRequest(t, definition, "create-"+test.name))
			if !mission.IsKind(err, mission.KindPersistenceContract) {
				t.Fatalf("wrong create result err=%v", err)
			}
		})
	}

	commitMutants := []struct {
		name   string
		mutate func(*mission.TransitionResult)
	}{
		{name: "another-successor", mutate: func(result *mission.TransitionResult) {
			result.Instance.CurrentStep = mission.StepPlanning
		}},
		{name: "another-state-version", mutate: func(result *mission.TransitionResult) {
			result.Instance.StateVersion++
		}},
		{name: "another-operation", mutate: func(result *mission.TransitionResult) {
			result.Audit.OperationID = mustOperationID(t, "operation.store-mutant")
		}},
		{name: "another-actor", mutate: func(result *mission.TransitionResult) {
			result.Audit.ActorID = mustActorID(t, "actor.store-mutant")
		}},
		{name: "another-event", mutate: func(result *mission.TransitionResult) {
			result.Audit.EventID = mustEventID(t, "event.store-mutant")
		}},
		{name: "another-governance", mutate: func(result *mission.TransitionResult) {
			result.Audit.GovernanceContextID = mustGovernanceID(t, "governance.store-mutant")
		}},
		{name: "another-definition-commitment", mutate: func(result *mission.TransitionResult) {
			result.Audit.DefinitionCommitment = mission.DefinitionCommitment(strings.Repeat("a", 64))
		}},
	}
	for _, test := range commitMutants {
		t.Run("commit-"+test.name, func(t *testing.T) {
			store := &mutatingStore{memoryStore: newMemoryStore(), commitMutator: test.mutate}
			service := mission.NewService(registry, store)
			created, err := service.Create(context.Background(), createRequest(t, definition, "commit-"+test.name))
			if err != nil {
				t.Fatal(err)
			}
			request := requestFor(t, definition, created.Instance, mission.StepSpecifying, "commit-"+test.name)
			if _, err := service.Transition(context.Background(), request); !mission.IsKind(err, mission.KindPersistenceContract) {
				t.Fatalf("wrong commit result err=%v", err)
			}
		})
	}

	t.Run("replay-from-another-operation", func(t *testing.T) {
		base := newMemoryStore()
		service := mission.NewService(registry, base)
		created, err := service.Create(context.Background(), createRequest(t, definition, "replay-mutant"))
		if err != nil {
			t.Fatal(err)
		}
		request := requestFor(t, definition, created.Instance, mission.StepSpecifying, "replay-mutant")
		if _, err := service.Transition(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		wrong := &mutatingStore{memoryStore: base, replayMutator: func(result *mission.TransitionResult) {
			result.Audit.OperationID = mustOperationID(t, "operation.another")
		}}
		if _, err := mission.NewService(registry, wrong).Transition(context.Background(), request); !mission.IsKind(err, mission.KindPersistenceContract) {
			t.Fatalf("wrong replay result err=%v", err)
		}
	})

	t.Run("replay-with-rewritten-snapshot", func(t *testing.T) {
		base := newMemoryStore()
		service := mission.NewService(registry, base)
		created, err := service.Create(context.Background(), createRequest(t, definition, "replay-snapshot-mutant"))
		if err != nil {
			t.Fatal(err)
		}
		request := requestFor(t, definition, created.Instance, mission.StepSpecifying, "replay-snapshot-mutant")
		if _, err := service.Transition(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		wrong := &mutatingStore{memoryStore: base, replayMutator: func(result *mission.TransitionResult) {
			result.Instance.ProjectID = mustProjectID(t, "project.rewritten")
		}}
		if _, err := mission.NewService(registry, wrong).Transition(context.Background(), request); !mission.IsKind(err, mission.KindPersistenceContract) {
			t.Fatalf("rewritten replay snapshot err=%v", err)
		}
	})
}

func TestStoreErrorsAndDefinitionDiagnosticsAreCanonicalAndBounded(t *testing.T) {
	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	store := &mutatingStore{
		memoryStore: newMemoryStore(),
		replayError: &mission.Error{
			Kind: mission.KindStateVersionConflict, Recovery: mission.RecoveryNoFurtherAction,
			Field: strings.Repeat("field", 100), Message: "seeded-store-secret-7e3d" + strings.Repeat("x", 500),
		},
	}
	store.snapshot = snapshotAt(t, definition, mission.StepInitialized, 1)
	request := requestFor(t, definition, store.snapshot, mission.StepSpecifying, "malicious-store-error")
	_, err = mission.NewService(registry, store).Transition(context.Background(), request)
	var diagnostic *mission.Error
	if !errors.As(err, &diagnostic) || diagnostic.Kind != mission.KindStateVersionConflict {
		t.Fatalf("normalized error=%v", err)
	}
	if diagnostic.Recovery != mission.RecoveryRefreshState || diagnostic.Field != "store" || strings.Contains(err.Error(), "seeded-store-secret") || len(err.Error()) > 256 {
		t.Fatalf("unbounded or noncanonical error=%+v rendered=%q", diagnostic, err)
	}

	for _, test := range []struct {
		name   string
		spec   mission.StepContractSpec
		secret string
	}{
		{name: "step-key", spec: mission.StepContractSpec{Key: mission.StepKey("seeded-definition-secret"), ApprovalPolicy: mission.ApprovalNone}, secret: "seeded-definition-secret"},
		{name: "governance-action", spec: mission.StepContractSpec{Key: mission.StepPlanning, ApprovalPolicy: mission.ApprovalNone, Mutating: true, GovernanceAction: "seeded-governance-secret/value"}, secret: "seeded-governance-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := mission.NewStepContract(test.spec)
			if err == nil || strings.Contains(err.Error(), test.secret) || len(err.Error()) > 256 {
				t.Fatalf("definition diagnostic=%q", err)
			}
		})
	}

	maliciousDefinition := definitionSpec(definition)
	maliciousDefinition.TerminalSteps = []mission.StepKey{mission.StepKey("seeded-terminal-secret")}
	if _, err := mission.NewDefinition(maliciousDefinition); err == nil || strings.Contains(err.Error(), "seeded-terminal-secret") || len(err.Error()) > 256 {
		t.Fatalf("terminal definition diagnostic=%q", err)
	}
}

func TestTransitionAuditExactCensusAndReplayPreservation(t *testing.T) {
	t.Run("authority-commitment-vector", func(t *testing.T) {
		evidence, err := artifact.NewCurrentApprovalEvidence("mission-vector", artifact.KindSpecification, "artifact-vector", "approval-vector")
		if err != nil {
			t.Fatal(err)
		}
		authority, err := mission.NewTransitionAuthority(mission.RequestCommitment(strings.Repeat("a", 64)), evidence)
		if err != nil {
			t.Fatal(err)
		}
		if mission.TransitionAuthorityCommitmentAlgorithm != "sha256:spec-kitty-transition-authority-v1" || authority.Commitment() != "47596e62ab6f042aefec0ecc929c11c51ba2b7970f31924b02944e140a7f49a3" {
			t.Fatalf("transition authority vector drifted: algorithm=%q commitment=%s", mission.TransitionAuthorityCommitmentAlgorithm, authority.Commitment())
		}
	})

	definition := mustDefinition(t)
	registry, err := mission.NewRegistry(definition)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	service := mission.NewService(registry, store)
	created, err := service.Create(context.Background(), createRequest(t, definition, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	request := requestFor(t, definition, created.Instance, mission.StepSpecifying, "audit")
	result, err := service.Transition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	want := mission.TransitionAudit{
		MissionID: request.MissionID, OldStep: request.ExpectedCurrentStep, NewStep: request.Successor,
		OldStateVersion: request.ExpectedStateVersion, NewStateVersion: request.ExpectedStateVersion + 1,
		OperationID: request.OperationID, EventID: request.EventID, ActorID: request.ActorID,
		DefinitionCommitment: request.DefinitionCommitment,
		GovernanceContextID:  request.GovernanceContextID, OccurredAt: request.OccurredAt,
	}
	if result.Audit != want {
		t.Fatalf("audit=%+v want=%+v", result.Audit, want)
	}
	replay, err := service.Transition(context.Background(), request)
	if err != nil || !replay.Replayed || replay.Audit != want || !reflect.DeepEqual(replay.Instance, result.Instance) {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

type artifactApprovalSource struct {
	revision          artifact.ArtifactRevision
	approvals         []artifact.ArtifactApproval
	currentError      error
	observationCalls  int
	observedMissionID string
	observedKind      artifact.Kind
	beforeObserve     func(*artifactApprovalSource)
	afterObserve      func(*artifactApprovalSource)
}

func (s *artifactApprovalSource) ObserveCurrentApproval(_ context.Context, missionID string, kind artifact.Kind) (artifact.CurrentApprovalObservation, error) {
	if s.beforeObserve != nil {
		s.beforeObserve(s)
	}
	s.observationCalls++
	s.observedMissionID = missionID
	s.observedKind = kind
	if s.currentError != nil {
		return artifact.CurrentApprovalObservation{}, s.currentError
	}
	observation, err := artifact.NewCurrentApprovalObservation(s.revision, s.approvals)
	if s.afterObserve != nil {
		s.afterObserve(s)
	}
	return observation, err
}

func freshArtifactApprovalSource(t *testing.T, missionID ids.MissionID, kind artifact.Kind, suffix string) *artifactApprovalSource {
	t.Helper()
	revision := mustArtifactRevision(t, missionID, kind, suffix)
	return &artifactApprovalSource{
		revision:  revision,
		approvals: []artifact.ArtifactApproval{mustArtifactApproval(t, "approval."+suffix, revision.ID(), artifact.VerdictApproved)},
	}
}

func mustArtifactRevision(t *testing.T, missionID ids.MissionID, kind artifact.Kind, suffix string) artifact.ArtifactRevision {
	t.Helper()
	revision, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{
		ID: "artifact." + suffix, MissionID: missionID.String(), Kind: kind, RevisionNumber: 2,
		ContentDigest: artifact.ContentCommitment([]byte("content-" + suffix)), SourceRef: "kitty-specs/" + suffix + ".md",
		SourceRevision: "source." + suffix, CreatedBy: "actor.artifact", CreatedAt: fixtureTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func mustArtifactApproval(t *testing.T, id, revisionID string, verdict artifact.ApprovalVerdict) artifact.ArtifactApproval {
	t.Helper()
	approval, err := artifact.NewArtifactApproval(artifact.ArtifactApprovalParams{
		ID: id, ArtifactRevisionID: revisionID, Verdict: verdict, ActorID: "actor.reviewer",
		EvidenceDigest: strings.Repeat("a", 64), GovernanceContextID: "governance.review", SubmittedAt: fixtureTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func withoutApprovalEvidence(evidence []mission.EvidenceRef) []mission.EvidenceRef {
	result := make([]mission.EvidenceRef, 0, len(evidence))
	for _, item := range evidence {
		if item.Kind != mission.EvidenceSpecificationOK && item.Kind != mission.EvidencePlanOK {
			result = append(result, item)
		}
	}
	return result
}

type mutatingStore struct {
	*memoryStore
	createMutator func(*mission.CreateResult)
	commitMutator func(*mission.TransitionResult)
	replayMutator func(*mission.TransitionResult)
	replayError   error
}

func (s *mutatingStore) CreateMission(ctx context.Context, commit mission.CreateCommit) (mission.CreateResult, error) {
	result, err := s.memoryStore.CreateMission(ctx, commit)
	if err == nil && s.createMutator != nil {
		s.createMutator(&result)
	}
	return result, err
}

func (s *mutatingStore) ReplayTransition(ctx context.Context, id ids.MissionID, operation ids.OperationID, commitment mission.RequestCommitment) (mission.TransitionResult, bool, error) {
	if s.replayError != nil {
		return mission.TransitionResult{}, false, s.replayError
	}
	result, found, err := s.memoryStore.ReplayTransition(ctx, id, operation, commitment)
	if err == nil && found && s.replayMutator != nil {
		s.replayMutator(&result)
	}
	return result, found, err
}

func (s *mutatingStore) CommitTransition(ctx context.Context, commit mission.TransitionCommit) (mission.TransitionResult, error) {
	result, err := s.memoryStore.CommitTransition(ctx, commit)
	if err == nil && s.commitMutator != nil {
		s.commitMutator(&result)
	}
	return result, err
}

type memoryStore struct {
	mu                   sync.Mutex
	snapshot             mission.MissionSnapshot
	createOps            map[ids.OperationID]storedCreate
	transitions          map[ids.OperationID]storedTransition
	failCommit           error
	commitCalls          int
	artifactSource       *artifactApprovalSource
	beforeAuthorityCheck func()
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
	result := mission.CreateResult{Instance: cloneSnapshot(s.snapshot), ActorID: commit.ActorID, ProcessID: commit.ProcessID, Governance: commit.Governance, AuthorityRecorded: true}
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
	if commit.Authority.Required() {
		if s.beforeAuthorityCheck != nil {
			s.beforeAuthorityCheck()
		}
		if s.artifactSource == nil || commit.Authority.Validate() != nil || commit.Authority.RequestCommitment() != commit.RequestCommitment {
			return mission.TransitionResult{}, domainError(mission.KindPersistenceContract)
		}
		evidence := commit.Authority.ArtifactApproval()
		if s.artifactSource.revision.ID() != evidence.ArtifactRevisionID() || s.artifactSource.revision.MissionID() != evidence.MissionID() || s.artifactSource.revision.Kind() != evidence.Kind() {
			return mission.TransitionResult{}, domainError(mission.KindApprovalStale)
		}
		matched := false
		for _, approval := range s.artifactSource.approvals {
			if approval.ID() == evidence.ApprovalID() && approval.ArtifactRevisionID() == evidence.ArtifactRevisionID() && approval.Verdict() == artifact.VerdictApproved {
				matched = true
				break
			}
		}
		if !matched || commit.Candidate.Audit.ArtifactRevisionID != evidence.ArtifactRevisionID() ||
			commit.Candidate.Audit.ApprovalEvidenceID.String() != evidence.ApprovalID() ||
			commit.Candidate.Audit.AuthorityCommitment != commit.Authority.Commitment() {
			return mission.TransitionResult{}, domainError(mission.KindApprovalStale)
		}
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
	result := mission.TransitionResult{Instance: cloneSnapshot(s.snapshot), Audit: commit.Candidate.Audit, Authority: commit.Authority}
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

func removeStep(spec *mission.DefinitionSpec, key mission.StepKey) {
	for index := range spec.Steps {
		if spec.Steps[index].Key == key {
			spec.Steps = append(spec.Steps[:index], spec.Steps[index+1:]...)
			return
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

func createRequest(t *testing.T, definition mission.MissionDefinition, suffix string) mission.CreateRequest {
	t.Helper()
	return mission.CreateRequest{
		MissionID: mustMissionID(t, "mission."+suffix), ProjectID: mustProjectID(t, "project."+suffix),
		DefinitionID: definition.ID(), DefinitionVersion: definition.Version(),
		OperationID: mustOperationID(t, "operation.create."+suffix), ActorID: mustActorID(t, "actor.create."+suffix),
		ProcessID: ids.ProcessID("process.create." + suffix), Governance: serviceGovernance(t, "governance.create."+suffix, fixtureTime()), OccurredAt: fixtureTime(),
	}
}

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
		ExpectedSnapshot:    current,
		ExpectedCurrentStep: current.CurrentStep, ExpectedStateVersion: current.StateVersion, Successor: successor,
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
	r.ExpectedSnapshot = cloneSnapshot(r.ExpectedSnapshot)
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
