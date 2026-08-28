package mission

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecisionLifecycleIsBoundedCanonicalAndImmutable(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	options := []string{"beta", "alpha"}
	decision, err := NewDecision(DecisionParams{ID: "decision-1", MissionID: "mission-1", Step: StepPlanning, Question: "Which design is approved?", Mode: DecisionClosed, Options: options, CreatedBy: "actor-author", GovernanceContextID: "governance-create", CreatedAt: now})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	options[0] = "changed"
	if !reflect.DeepEqual(decision.Options(), []string{"alpha", "beta"}) || decision.Status() != DecisionOpen || decision.Version() != 1 {
		t.Fatal("decision was not canonical and defensively copied")
	}
	resolved, err := decision.Transition(DecisionTransition{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: DecisionResolved, Answer: "alpha", Rationale: "Selected against the approved architecture.", ActorID: "actor-decider", GovernanceContextID: "governance-resolve", OccurredAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if decision.Status() != DecisionOpen || resolved.Status() != DecisionResolved || resolved.Version() != 2 || resolved.Resolution().Answer != "alpha" {
		t.Fatal("decision transition rewrote history or lost resolution")
	}
	restored, err := RestoreDecision(baseDecisionParams(decision), resolved.Status(), resolved.Version(), resolved.Resolution())
	if err != nil || restored.Resolution() != resolved.Resolution() {
		t.Fatalf("restore terminal decision: %v", err)
	}
	if _, err := resolved.Transition(DecisionTransition{ExpectedStatus: DecisionResolved, ExpectedVersion: 2, Status: DecisionCanceled, Rationale: "rewrite", ActorID: "actor-decider", GovernanceContextID: "governance-resolve", OccurredAt: now.Add(2 * time.Minute)}); !IsKind(err, KindIllegalTransition) {
		t.Fatalf("terminal rewrite should fail, got %v", err)
	}
	for _, status := range []DecisionStatus{DecisionResolved, DecisionDeferred, DecisionCanceled} {
		transition := DecisionTransition{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: status, Rationale: "Bounded durable disposition.", ActorID: "actor-decider", GovernanceContextID: "governance-resolve", OccurredAt: now.Add(time.Minute)}
		if status == DecisionResolved {
			transition.Answer = "beta"
		}
		if _, err := decision.Transition(transition); err != nil {
			t.Fatalf("transition %s: %v", status, err)
		}
	}
}

func baseDecisionParams(d Decision) DecisionParams {
	return DecisionParams{ID: d.ID(), MissionID: d.MissionID(), Step: d.Step(), Question: d.Question(), Mode: d.Mode(), Options: d.Options(), CreatedBy: d.CreatedBy(), GovernanceContextID: d.GovernanceContextID(), CreatedAt: d.CreatedAt()}
}

func TestDecisionRefusesStaleInvalidAndPrivateContent(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	base := DecisionParams{ID: "decision-1", MissionID: "mission-1", Step: StepPlanning, Question: "Choose one?", Mode: DecisionClosed, Options: []string{"a", "b"}, CreatedBy: "actor-author", GovernanceContextID: "governance-create", CreatedAt: now}
	mutants := []func(*DecisionParams){
		func(p *DecisionParams) { p.Question = strings.Repeat("x", 513) },
		func(p *DecisionParams) { p.Options = []string{"same", "same"} },
		func(p *DecisionParams) { p.Mode = DecisionFreeform },
		func(p *DecisionParams) { p.CreatedAt = p.CreatedAt.In(time.FixedZone("not-utc", 1)) },
	}
	for i, mutate := range mutants {
		candidate := base
		mutate(&candidate)
		if _, err := NewDecision(candidate); !IsKind(err, KindInvalidRequest) {
			t.Fatalf("mutant %d: %v", i, err)
		}
	}
	decision, _ := NewDecision(base)
	bad := []DecisionTransition{
		{ExpectedStatus: DecisionOpen, ExpectedVersion: 2, Status: DecisionResolved, Answer: "a", Rationale: "valid", ActorID: "actor", GovernanceContextID: "gov", OccurredAt: now.Add(time.Minute)},
		{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: DecisionResolved, Answer: "outside", Rationale: "valid", ActorID: "actor", GovernanceContextID: "gov", OccurredAt: now.Add(time.Minute)},
		{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: DecisionResolved, Answer: "a", Rationale: "password=private", ActorID: "actor", GovernanceContextID: "gov", OccurredAt: now.Add(time.Minute)},
	}
	for i, transition := range bad {
		if _, err := decision.Transition(transition); err == nil {
			t.Fatalf("transition mutant %d accepted", i)
		}
	}
	freeform, err := NewDecision(DecisionParams{ID: "decision-free", MissionID: "mission-1", Step: StepPlanning, Question: "Supply a bounded name", Mode: DecisionFreeform, CreatedBy: "actor", GovernanceContextID: "gov", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := freeform.Transition(DecisionTransition{ExpectedStatus: DecisionOpen, ExpectedVersion: 1, Status: DecisionResolved, Answer: "custom", Rationale: "bounded", ActorID: "actor", GovernanceContextID: "gov", OccurredAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
}

func TestBlockerLifecycleRecoveryAndStableOrdering(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	recovery := []BlockerRecovery{RecoveryRefreshAuthority, RecoveryCorrectGovernance}
	blocker, err := NewBlocker(BlockerParams{ID: "blocker-b", MissionID: "mission-1", WorkID: "work-1", Step: StepPlanning, Kind: BlockerGovernance, Summary: "Governance evidence must be refreshed.", RecoveryActions: recovery, ObservedAt: now})
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	recovery[0] = RecoveryRetryOperation
	if !reflect.DeepEqual(blocker.RecoveryActions(), []BlockerRecovery{RecoveryCorrectGovernance, RecoveryRefreshAuthority}) {
		t.Fatal("recovery was not canonical")
	}
	cleared, err := blocker.Clear(BlockerClear{ExpectedStatus: BlockerActive, ExpectedVersion: 1, ActorID: "actor-clear", GovernanceContextID: "governance-clear", Reason: "Corrected the sealed authority.", OccurredAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if blocker.Status() != BlockerActive || cleared.Status() != BlockerCleared || cleared.Version() != 2 {
		t.Fatal("blocker history was mutated")
	}
	if _, err := cleared.Clear(BlockerClear{ExpectedStatus: BlockerCleared, ExpectedVersion: 2, ActorID: "actor", GovernanceContextID: "gov", Reason: "again", OccurredAt: now.Add(2 * time.Minute)}); !IsKind(err, KindIllegalTransition) {
		t.Fatalf("repeat clear: %v", err)
	}

	a, _ := NewBlocker(BlockerParams{ID: "blocker-a", MissionID: "mission-1", Step: StepPlanning, Kind: BlockerDependency, Summary: "Dependency is unresolved.", RecoveryActions: []BlockerRecovery{RecoveryResolveDependency}, ObservedAt: now})
	ordered := ActiveBlockers([]Blocker{blocker, cleared, a})
	if len(ordered) != 2 || ordered[0].ID() != "blocker-a" || ordered[1].ID() != "blocker-b" {
		t.Fatal("active blocker order/status filter is unstable")
	}
}

func TestBlockerRejectsRawFailureAndInvalidRecovery(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	base := BlockerParams{ID: "blocker-1", MissionID: "mission-1", Step: StepPlanning, Kind: BlockerDependency, Summary: "Dependency is unresolved.", RecoveryActions: []BlockerRecovery{RecoveryResolveDependency}, ObservedAt: now}
	mutants := []func(*BlockerParams){
		func(p *BlockerParams) { p.Summary = "panic: stack trace password=secret" },
		func(p *BlockerParams) { p.RecoveryActions = nil },
		func(p *BlockerParams) {
			p.RecoveryActions = []BlockerRecovery{RecoveryResolveDependency, RecoveryResolveDependency}
		},
		func(p *BlockerParams) { p.RecoveryActions = []BlockerRecovery{RecoveryCorrectGovernance} },
		func(p *BlockerParams) { p.WorkID = "bad work" },
	}
	for i, mutate := range mutants {
		candidate := base
		mutate(&candidate)
		if _, err := NewBlocker(candidate); err == nil {
			t.Fatalf("mutant %d accepted", i)
		}
	}
}
