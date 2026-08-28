package mission

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestNextResultSevenKindStrictRoundTrip(t *testing.T) {
	base := NextBase{"mission-next", 4, StepPlanning}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	decision, _ := NewDecision(DecisionParams{ID: "decision-next", MissionID: base.MissionID, Step: base.Step, Question: "Choose?", Mode: DecisionClosed, Options: []string{"b", "a"}, CreatedBy: "actor", GovernanceContextID: "governance", CreatedAt: now})
	blocker, _ := NewBlocker(BlockerParams{ID: "blocker-next", MissionID: base.MissionID, Step: base.Step, Kind: BlockerDependency, Summary: "Dependency is unresolved.", RecoveryActions: []BlockerRecovery{RecoveryResolveDependency}, ObservedAt: now})
	must := func(value NextResult, err error) NextResult {
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	values := []NextResult{
		must(NewQueryResult(base, NextDiagnostic{Kind: "required_input", RetrySafety: RetrySafe, Recovery: RecoverySupplyEvidence, Message: "Input is required."})),
		must(NewActionResult(base, ActionDescriptor{ActionKey: StepPlanApproved.String(), Mutating: true, GovernanceAction: "mission.approve-plan", Successor: StepPlanApproved})),
		must(NewWorkReadyResult(base, "revision-next", []ids.WorkID{"work-b", "work-a"})),
		must(NewReviewReadyResult(base, "revision-next", []ids.WorkID{"work-a"})),
		must(NewDecisionRequiredResult(base, decision)),
		must(NewBlockedResult(base, blocker)),
		must(NewTerminalResult(base, OutcomeWorkFinalized)),
	}
	if len(values) != 7 {
		t.Fatal("kind census changed")
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", value.Kind, err)
		}
		var restored NextResult
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatalf("unmarshal %s: %v", value.Kind, err)
		}
		reencoded, _ := json.Marshal(restored)
		if !bytes.Equal(encoded, reencoded) {
			t.Fatalf("%s not canonical", value.Kind)
		}
	}
}

func TestNextResultRejectsIllegalFieldsUnknownDuplicateAndMutation(t *testing.T) {
	base := NextBase{"mission-next", 1, StepPlanning}
	work := []ids.WorkID{"work-a"}
	ready, err := NewWorkReadyResult(base, "revision-next", work)
	if err != nil {
		t.Fatal(err)
	}
	work[0] = "changed"
	if ready.ReadyWorkIDs[0] != "work-a" {
		t.Fatal("ready work not copied")
	}
	mutants := []NextResult{
		{SchemaVersion: 1, Kind: "unknown", MissionID: base.MissionID, MissionStateVersion: 1, StepKey: base.Step},
		{SchemaVersion: 1, Kind: NextAction, MissionID: base.MissionID, MissionStateVersion: 1, StepKey: base.Step, Action: &ActionDescriptor{ActionKey: StepPlanning.String(), Mutating: true, GovernanceAction: "mission.plan", Successor: StepPlanning}, TerminalOutcome: OutcomeWorkFinalized},
		{SchemaVersion: 1, Kind: NextWorkReady, MissionID: base.MissionID, MissionStateVersion: 1, StepKey: base.Step, WorkRevisionID: "revision", ReadyWorkIDs: []ids.WorkID{"work-a", "work-a"}},
		{SchemaVersion: 1, Kind: NextReviewReady, MissionID: base.MissionID, MissionStateVersion: 1, StepKey: base.Step},
	}
	for i, mutant := range mutants {
		if mutant.Validate() == nil {
			t.Fatalf("mutant %d accepted", i)
		}
	}
	badJSON := []string{
		`{"schema_version":1,"schema_version":1,"kind":"terminal","mission_id":"mission","mission_state_version":1,"step_key":"work_finalized","terminal_outcome":"work_finalized"}`,
		`{"schema_version":1,"kind":"terminal","mission_id":"mission","mission_state_version":1,"step_key":"work_finalized","terminal_outcome":"work_finalized","unknown":true}`,
		`{"schema_version":1,"kind":"work_ready","mission_id":"mission","mission_state_version":1,"step_key":"work_finalized","work_revision_id":"revision","ready_work_ids":null}`,
	}
	for i, encoded := range badJSON {
		var value NextResult
		if json.Unmarshal([]byte(encoded), &value) == nil {
			t.Fatalf("wire mutant %d accepted", i)
		}
	}
}

func TestNextIsOneReadPureDeterministicAndUsesDestinationLaw(t *testing.T) {
	definition := nextActionDefinition(t)
	snapshot := nextSnapshotAt(t, definition, StepInitialized, 1)
	source := &nextSourceSpy{snapshot: snapshot}
	var first NextResult
	for i := 0; i < 100; i++ {
		got, err := Next(context.Background(), source, snapshot.Mission.MissionID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != NextAction || got.Action.ActionKey != StepPlanning.String() || got.Action.GovernanceAction != "mission.plan" || got.Action.Successor != StepPlanning {
			t.Fatalf("unexpected action: %+v", got)
		}
		if i == 0 {
			first = got
		} else if !reflect.DeepEqual(first, got) {
			t.Fatal("unchanged snapshot changed result")
		}
	}
	if source.reads != 100 {
		t.Fatalf("reads=%d", source.reads)
	}
	// The source has no write/provider/projection method: purity is structural,
	// while the counter proves exactly one aggregate observation per query.
}

func TestNextPrecedenceCensusAndWorkDraftRouting(t *testing.T) {
	definition := nextActionDefinition(t)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	snapshot := nextSnapshotAt(t, definition, StepInitialized, 2)
	decision, _ := NewDecision(DecisionParams{ID: "decision-next", MissionID: snapshot.Mission.MissionID, Step: StepInitialized, Question: "Choose?", Mode: DecisionClosed, Options: []string{"a", "b"}, CreatedBy: "actor", GovernanceContextID: "governance", CreatedAt: now})
	blocker, _ := NewBlocker(BlockerParams{ID: "blocker-next", MissionID: snapshot.Mission.MissionID, Step: StepInitialized, Kind: BlockerDependency, Summary: "Dependency unresolved.", RecoveryActions: []BlockerRecovery{RecoveryResolveDependency}, ObservedAt: now})
	snapshot.Decisions = []Decision{decision}
	snapshot.Blockers = []Blocker{blocker}
	snapshot.Mission.UnresolvedDecisions = []ids.DecisionID{decision.ID()}
	snapshot.Mission.ActiveBlockers = []ids.BlockerID{blocker.ID()}
	got, err := Next(context.Background(), &nextSourceSpy{snapshot: snapshot}, snapshot.Mission.MissionID)
	if err != nil || got.Kind != NextBlocked {
		t.Fatalf("blocker precedence: %+v %v", got, err)
	}
	snapshot.Blockers = nil
	snapshot.Mission.ActiveBlockers = nil
	got, err = Next(context.Background(), &nextSourceSpy{snapshot: snapshot}, snapshot.Mission.MissionID)
	if err != nil || got.Kind != NextDecisionRequired {
		t.Fatalf("decision precedence: %+v %v", got, err)
	}
	snapshot.Mission.UnresolvedDecisions = nil
	if _, err := Next(context.Background(), &nextSourceSpy{snapshot: snapshot}, snapshot.Mission.MissionID); !IsKind(err, KindInvalidStoredState) {
		t.Fatalf("census bypass accepted: %v", err)
	}

	draftDefinition := nextDraftDefinition(t)
	draft := nextSnapshotAt(t, draftDefinition, StepWorkDraftReady, 3)
	finalize, _ := Next(context.Background(), &nextSourceSpy{snapshot: draft}, draft.Mission.MissionID)
	if finalize.Action.Successor != StepWorkFinalized || finalize.Action.GovernanceAction != "mission.finalize-work" {
		t.Fatalf("finalize route: %+v", finalize)
	}
	draft.WorkDraftFindings = []workgraph.Finding{{Kind: workgraph.FindingMissingContext, Severity: workgraph.SeverityError, Field: "description", Message: "bounded", Recovery: workgraph.RecoveryReviseWork}}
	reconcile, _ := Next(context.Background(), &nextSourceSpy{snapshot: draft}, draft.Mission.MissionID)
	if reconcile.Action.Successor != StepReconciliationRequired || reconcile.Action.GovernanceAction != "mission.reconcile" {
		t.Fatalf("reconcile route: %+v", reconcile)
	}
}

func TestNextFinalizedUsesExactRevisionAndNeverReviewReady(t *testing.T) {
	definition := nextFinalizedDefinition(t)
	snapshot := nextSnapshotAt(t, definition, StepWorkFinalized, 9)
	revision := nextRevision(t, snapshot.Mission, definition)
	snapshot.CurrentWorkRevision = &revision
	snapshot.Mission.CurrentWorkRevisionID = revision.ID()
	snapshot.Mission.Lifecycle = LifecycleTerminal
	snapshot.Mission.TerminalOutcome = OutcomeWorkFinalized
	for i := 0; i < 10; i++ {
		got, err := Next(context.Background(), &nextSourceSpy{snapshot: snapshot}, snapshot.Mission.MissionID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != NextWorkReady || got.WorkRevisionID != revision.ID() || !reflect.DeepEqual(got.ReadyWorkIDs, []ids.WorkID{"work-a"}) {
			t.Fatalf("ready: %+v", got)
		}
		if got.Kind == NextReviewReady {
			t.Fatal("production emitted review_ready")
		}
	}
	other := revision
	snapshot.CurrentWorkRevision = &other
	snapshot.Mission.CurrentWorkRevisionID = "revision-other"
	if _, err := Next(context.Background(), &nextSourceSpy{snapshot: snapshot}, snapshot.Mission.MissionID); !IsKind(err, KindInvalidStoredState) {
		t.Fatalf("substituted revision: %v", err)
	}
}

func TestNextUsesExactCurrentArtifactApprovalFacts(t *testing.T) {
	d := mustDefinition(t, []StepContractSpec{{Key: StepSpecifying, ApprovalPolicy: ApprovalNone, Successors: []StepKey{StepSpecificationApproved}}, {Key: StepSpecificationApproved, RequiredInputs: []ArtifactKind{ArtifactSpecification}, RequiredEvidence: []EvidenceKind{EvidenceSpecificationOK}, ValidationPolicy: []ValidationRequirement{ValidationSpecificationComplete}, ApprovalPolicy: ApprovalSpecification, Predecessors: []StepKey{StepSpecifying}, Successors: []StepKey{StepWorkFinalized}, GovernanceAction: "mission.approve-specification", Mutating: true}, {Key: StepWorkFinalized, ApprovalPolicy: ApprovalNone, Predecessors: []StepKey{StepSpecificationApproved}, GovernanceAction: "mission.finalize-work", Mutating: true, TerminalOutcome: OutcomeWorkFinalized}}, StepSpecifying)
	s := nextSnapshotAt(t, d, StepSpecifying, 3)
	baseSource := &nextSourceSpy{snapshot: s}
	got, err := Next(context.Background(), baseSource, s.Mission.MissionID)
	if err != nil || got.Kind != NextQuery || got.Diagnostic.Kind != "required_input" {
		t.Fatalf("missing input: %+v %v", got, err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	revision, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{ID: "artifact-spec-r1", MissionID: s.Mission.MissionID.String(), Kind: artifact.KindSpecification, RevisionNumber: 1, ContentDigest: artifact.ContentCommitment([]byte("spec")), SourceRef: "spec.md", SourceRevision: "commit-a", CreatedBy: "actor", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := artifact.NewCurrentApprovalObservation(revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.ArtifactObservations = []artifact.CurrentApprovalObservation{observation}
	got, err = Next(context.Background(), &nextSourceSpy{snapshot: s}, s.Mission.MissionID)
	if err != nil || got.Kind != NextQuery || got.Diagnostic.Kind != "approval_required" {
		t.Fatalf("missing approval: %+v %v", got, err)
	}
	approval, err := artifact.NewArtifactApproval(artifact.ArtifactApprovalParams{ID: "approval-spec-r1", ArtifactRevisionID: revision.ID(), Verdict: artifact.VerdictApproved, ActorID: "actor-review", EvidenceDigest: strings.Repeat("b", 64), GovernanceContextID: "governance-review", SubmittedAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	observation, _ = artifact.NewCurrentApprovalObservation(revision, []artifact.ArtifactApproval{approval})
	s.ArtifactObservations = []artifact.CurrentApprovalObservation{observation}
	got, err = Next(context.Background(), &nextSourceSpy{snapshot: s}, s.Mission.MissionID)
	if err != nil || got.Kind != NextAction || got.Action.GovernanceAction != "mission.approve-specification" {
		t.Fatalf("fresh approval: %+v %v", got, err)
	}
	foreign, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{ID: "artifact-other", MissionID: "mission-other", Kind: artifact.KindSpecification, RevisionNumber: 1, ContentDigest: artifact.ContentCommitment([]byte("spec")), SourceRef: "spec.md", SourceRevision: "commit-a", CreatedBy: "actor", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	foreignObservation, _ := artifact.NewCurrentApprovalObservation(foreign, nil)
	s.ArtifactObservations = []artifact.CurrentApprovalObservation{foreignObservation}
	if _, err := Next(context.Background(), &nextSourceSpy{snapshot: s}, s.Mission.MissionID); !IsKind(err, KindInvalidStoredState) {
		t.Fatalf("foreign observation accepted: %v", err)
	}
}

type nextSourceSpy struct {
	snapshot NextSnapshot
	reads    int
}

func (s *nextSourceSpy) ObserveNext(context.Context, ids.MissionID) (NextSnapshot, error) {
	s.reads++
	return s.snapshot, nil
}
func nextSnapshotAt(t *testing.T, d MissionDefinition, step StepKey, version uint64) NextSnapshot {
	t.Helper()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	s := MissionSnapshot{MissionID: "mission-next", ProjectID: "project-next", DefinitionID: d.ID(), DefinitionVersion: d.Version(), DefinitionCommitment: d.Commitment(), Lifecycle: LifecycleOpen, CurrentStep: step, StateVersion: version, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	return NextSnapshot{Mission: s, Definition: d}
}
func nextActionDefinition(t *testing.T) MissionDefinition {
	return mustDefinition(t, []StepContractSpec{{Key: StepInitialized, ApprovalPolicy: ApprovalNone, Successors: []StepKey{StepPlanning}}, {Key: StepPlanning, ApprovalPolicy: ApprovalNone, Predecessors: []StepKey{StepInitialized}, Successors: []StepKey{StepWorkFinalized}, GovernanceAction: "mission.plan", Mutating: true}, {Key: StepWorkFinalized, ApprovalPolicy: ApprovalNone, Predecessors: []StepKey{StepPlanning}, GovernanceAction: "mission.finalize-work", Mutating: true, TerminalOutcome: OutcomeWorkFinalized}}, StepInitialized)
}
func nextDraftDefinition(t *testing.T) MissionDefinition {
	return mustDefinition(t, []StepContractSpec{{Key: StepInitialized, ApprovalPolicy: ApprovalNone, Successors: []StepKey{StepWorkDraftReady}}, {Key: StepWorkDraftReady, ApprovalPolicy: ApprovalNone, Predecessors: []StepKey{StepInitialized}, Successors: []StepKey{StepReconciliationRequired, StepWorkFinalized}, GovernanceAction: "mission.validate-work", Mutating: true}, {Key: StepReconciliationRequired, ApprovalPolicy: ApprovalNone, Predecessors: []StepKey{StepWorkDraftReady}, Successors: []StepKey{StepWorkFinalized}, GovernanceAction: "mission.reconcile", Mutating: true}, {Key: StepWorkFinalized, ApprovalPolicy: ApprovalNone, Predecessors: []StepKey{StepWorkDraftReady, StepReconciliationRequired}, GovernanceAction: "mission.finalize-work", Mutating: true, TerminalOutcome: OutcomeWorkFinalized}}, StepInitialized)
}
func nextFinalizedDefinition(t *testing.T) MissionDefinition {
	return mustDefinition(t, []StepContractSpec{{Key: StepInitialized, ApprovalPolicy: ApprovalNone, Successors: []StepKey{StepWorkFinalized}}, {Key: StepWorkFinalized, ApprovalPolicy: ApprovalNone, Predecessors: []StepKey{StepInitialized}, GovernanceAction: "mission.finalize-work", Mutating: true, TerminalOutcome: OutcomeWorkFinalized}}, StepInitialized)
}
func mustDefinition(t *testing.T, steps []StepContractSpec, initial StepKey) MissionDefinition {
	t.Helper()
	d, err := NewDefinition(DefinitionSpec{ID: "software-development", Version: 1, InitialStep: initial, TerminalSteps: []StepKey{StepWorkFinalized}, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func nextRevision(t *testing.T, s MissionSnapshot, d MissionDefinition) workgraph.RevisionV2 {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: s.ProjectID, WorkID: "work-a", Title: "Implement A", Description: "Implement the bounded unit.", DesignContext: "Follow the plan.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-a", Text: "Run tests.", Outcome: "Tests pass.", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/a", Domain: "mission", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "scope-a", Kind: workgraph.ScopeRequiredOutcome}}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := workgraph.NewDraftObservation(s.ProjectID, s.MissionID, "plan-r1", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-a", Kind: workgraph.ScopeRequiredOutcome, MissionID: s.MissionID}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := workgraph.CollapseLanes(observation)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.NewRevisionV2(workgraph.RevisionV2Params{ID: "revision-next", ProjectID: s.ProjectID, MissionID: s.MissionID, Generation: 1, PlanningSourceCommit: strings.Repeat("a", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: d.ID().String(), Version: uint64(d.Version()), Digest: d.Commitment().String()}, DependencyPolicy: workgraph.DependencyApprovedOrDone, WorkItems: []workgraph.WorkItemV2Params{{WorkID: "work-a", Title: "Implement A", Description: "Implement the bounded unit.", DesignContext: "Follow the plan.", Criteria: []workgraph.CriterionV2Params{{ID: "criterion-a", Text: "Run tests.", Outcome: "Tests pass.", VerifierKind: "test", Required: true}}, WriteScopes: []workgraph.NormalizedWriteScope{{Path: "internal/a", Domain: "mission", Access: workgraph.ScopeWriteExclusive}}}}, Lanes: lanes, ProviderObservations: []workgraph.ProviderObservationV2{{WorkID: "work-a", Provider: "fake", ExternalID: "external-a", Revision: "r1"}}, FinalizedBy: "actor", FinalizedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC), GovernanceContextID: "governance"})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
