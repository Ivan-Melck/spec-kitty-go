package mission_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestDecompositionRefusesIneligiblePlanBeforeProviderAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*mission.DecompositionCommand, *missionSource, *planSource)
	}{
		{"absent", func(_ *mission.DecompositionCommand, _ *missionSource, p *planSource) { p.err = errors.New("absent") }},
		{"rejected", func(c *mission.DecompositionCommand, _ *missionSource, p *planSource) {
			p.observation = approvalObservation(t, c.MissionID, artifact.KindPlan, c.PlanRevisionID, c.PlanContentDigest, artifact.VerdictRejected)
		}},
		{"stale", func(c *mission.DecompositionCommand, _ *missionSource, p *planSource) {
			current := artifactRevision(t, c.MissionID, artifact.KindPlan, "plan-current", strings.Repeat("b", 64))
			old := artifactApproval(t, "approval-old", c.PlanRevisionID, artifact.VerdictApproved)
			p.observation = mustCurrentObservation(t, current, []artifact.ArtifactApproval{old})
		}},
		{"wrong mission", func(c *mission.DecompositionCommand, _ *missionSource, p *planSource) {
			p.observation = approvalObservation(t, "mission-other", artifact.KindPlan, c.PlanRevisionID, c.PlanContentDigest, artifact.VerdictApproved)
		}},
		{"wrong kind", func(c *mission.DecompositionCommand, _ *missionSource, p *planSource) {
			p.observation = approvalObservation(t, c.MissionID, artifact.KindSpecification, c.PlanRevisionID, c.PlanContentDigest, artifact.VerdictApproved)
		}},
		{"superseded", func(c *mission.DecompositionCommand, _ *missionSource, p *planSource) {
			p.observation = approvalObservation(t, c.MissionID, artifact.KindPlan, "plan-current", strings.Repeat("b", 64), artifact.VerdictApproved)
		}},
		{"wrong mission state", func(c *mission.DecompositionCommand, m *missionSource, _ *planSource) {
			changed := m.snapshot
			changed.CurrentStep = mission.StepPlanning
			m.snapshot = changed
			c.ExpectedMission = changed
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, missionState, plans, provider := decompositionFixture(t)
			test.mutate(&command, missionState, plans)
			service := mission.NewDecompositionService(missionState, plans, provider)
			if _, err := service.Decompose(context.Background(), command); err == nil {
				t.Fatal("ineligible decomposition accepted")
			}
			if provider.prepareCalls != 0 || provider.createCalls != 0 || provider.updateCalls != 0 || provider.capabilityCalls != 0 {
				t.Fatalf("provider consulted before plan eligibility: %+v", provider)
			}
		})
	}
}

func TestDecompositionCreatesTwoItemGraphAndReplaysExactly(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	service := mission.NewDecompositionService(missions, plans, provider)
	first, err := service.Decompose(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != mission.DecompositionComplete || len(first.Observations) != 2 || len(first.Findings) != 0 || first.Replayed {
		t.Fatalf("first result=%+v", first)
	}
	got := first.Draft.Work()
	if len(got) != 2 {
		t.Fatalf("draft work=%+v", got)
	}
	featureFound := false
	for _, item := range got {
		if item.Definition().WorkID() == "work-feature" {
			dependencies := item.Definition().Dependencies()
			featureFound = len(dependencies) == 1 && dependencies[0] == "work-foundation"
		}
	}
	if !featureFound {
		t.Fatalf("draft feature dependency missing: %+v", got)
	}
	second, err := service.Decompose(context.Background(), command)
	if err != nil || !second.Replayed || second.RequestDigest != first.RequestDigest || len(second.Observations) != 2 {
		t.Fatalf("replay=%+v err=%v", second, err)
	}
	beforeCreates := provider.createCalls
	changed := command
	changed.Work = append([]mission.WorkProposal(nil), command.Work...)
	definition := changed.Work[1].Definition
	changedDefinition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Title: "Changed title", Description: definition.Description(), DesignContext: definition.DesignContext(), Criteria: definition.Criteria(), Dependencies: definition.Dependencies(), WriteScopes: definition.WriteScopes(), Provenance: definition.Provenance(), MetadataSchema: definition.MetadataSchema()})
	if err != nil {
		t.Fatal(err)
	}
	changed.Work[1].Definition = changedDefinition
	result, err := service.Decompose(context.Background(), changed)
	var typed *mission.DecompositionError
	if !errors.As(err, &typed) || typed.Kind != mission.DecompositionOperationConflict || provider.createCalls != beforeCreates || result.RequestDigest == first.RequestDigest {
		t.Fatalf("changed replay result=%+v error=%v creates=%d", result, err, provider.createCalls)
	}
}

func TestDecompositionValidatesCompleteGraphBeforeFirstProviderWrite(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	feature := command.Work[1].Definition
	invalid, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: feature.ProjectID(), WorkID: feature.WorkID(), Title: feature.Title(), Description: feature.Description(), DesignContext: feature.DesignContext(), Criteria: feature.Criteria(), Dependencies: []ids.WorkID{"work-absent"}, WriteScopes: feature.WriteScopes(), Provenance: feature.Provenance(), MetadataSchema: feature.MetadataSchema()})
	if err != nil {
		t.Fatal(err)
	}
	command.Work[1].Definition = invalid
	result, err := mission.NewDecompositionService(missions, plans, provider).Decompose(context.Background(), command)
	if err == nil || len(result.Findings) == 0 || provider.prepareCalls != 0 || provider.createCalls != 0 {
		t.Fatalf("invalid graph result=%+v error=%v provider=%+v", result, err, provider)
	}
}

func TestDecompositionSurfacesTimeoutAfterApplyAsAmbiguous(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	if err := provider.store.Inject(context.Background(), fake.FailureTimeoutAfterApply); err != nil {
		t.Fatal(err)
	}
	result, err := mission.NewDecompositionService(missions, plans, provider).Decompose(context.Background(), command)
	if err == nil || result.Status != mission.DecompositionAmbiguous || result.Recovery != mission.DecompositionObserveThenReconcile || len(result.Observations) != 1 || result.FailedWorkID == "" {
		t.Fatalf("ambiguous result=%+v error=%v", result, err)
	}
	if _, observeErr := provider.ObserveDefinition(context.Background(), result.Observations[0].ExternalRef()); observeErr != nil {
		t.Fatalf("ambiguous successor is not observable: %v", observeErr)
	}
}

func TestDecompositionSurfacesPartialProviderFailureWithoutClaimingRollback(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	provider.failCreateAt = 2
	result, err := mission.NewDecompositionService(missions, plans, provider).Decompose(context.Background(), command)
	if err == nil || result.Status != mission.DecompositionPartial || result.Recovery != mission.DecompositionObserveThenReconcile || len(result.Observations) != 1 || result.Observations[0].Definition().WorkID() != "work-foundation" || result.FailedWorkID != "work-feature" {
		t.Fatalf("partial result=%+v error=%v", result, err)
	}
	if _, observeErr := provider.ObserveDefinition(context.Background(), result.Observations[0].ExternalRef()); observeErr != nil {
		t.Fatalf("successful predecessor was rolled back or lost: %v", observeErr)
	}
}

type missionSource struct{ snapshot mission.MissionSnapshot }

func (s *missionSource) LoadMission(context.Context, ids.MissionID) (mission.MissionSnapshot, error) {
	return s.snapshot, nil
}

type planSource struct {
	observation artifact.CurrentApprovalObservation
	err         error
}

func (s *planSource) ObserveCurrentApproval(context.Context, string, artifact.Kind) (artifact.CurrentApprovalObservation, error) {
	return s.observation, s.err
}

type countingDefinitionProvider struct {
	port                                                                  *fake.Provider
	store                                                                 *fake.Store
	capabilityCalls, prepareCalls, createCalls, updateCalls, observeCalls int
	failCreateAt                                                          int
}

func (p *countingDefinitionProvider) DefinitionCapabilities(ctx context.Context) (tracker.WorkDefinitionCapabilities, error) {
	p.capabilityCalls++
	return p.port.DefinitionCapabilities(ctx)
}
func (p *countingDefinitionProvider) PrepareDefinitionOperation(ctx context.Context, id ids.OperationID, digest string) error {
	p.prepareCalls++
	return p.port.PrepareDefinitionOperation(ctx, id, digest)
}
func (p *countingDefinitionProvider) CreateDefinition(ctx context.Context, r tracker.CreateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	p.createCalls++
	if p.failCreateAt != 0 && p.createCalls == p.failCreateAt {
		return tracker.WorkDefinitionMutation{}, &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "injected provider outage"}
	}
	return p.port.CreateDefinition(ctx, r)
}
func (p *countingDefinitionProvider) UpdateDefinition(ctx context.Context, r tracker.UpdateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	p.updateCalls++
	return p.port.UpdateDefinition(ctx, r)
}
func (p *countingDefinitionProvider) ObserveDefinition(ctx context.Context, r tracker.ExternalWorkRef) (tracker.WorkDefinitionObservation, error) {
	p.observeCalls++
	return p.port.ObserveDefinition(ctx, r)
}

func decompositionFixture(t *testing.T) (mission.DecompositionCommand, *missionSource, *planSource, *countingDefinitionProvider) {
	t.Helper()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	snapshot := mission.MissionSnapshot{MissionID: "mission-kitty", ProjectID: "project-kitty", DefinitionID: "software-dev", DefinitionVersion: 1, DefinitionCommitment: mission.DefinitionCommitment(strings.Repeat("c", 64)), Lifecycle: mission.LifecycleOpen, CurrentStep: mission.StepPlanApproved, StateVersion: 5, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)}
	planDigest := strings.Repeat("a", 64)
	command := mission.DecompositionCommand{MissionID: snapshot.MissionID, ProjectID: snapshot.ProjectID, ExpectedMission: snapshot, PlanRevisionID: "plan-revision-1", PlanContentDigest: planDigest, ApprovedScope: []workgraph.ApprovedScope{{ID: "plan-foundation", Kind: tracker.ScopePlanConcern, MissionID: snapshot.MissionID}, {ID: "plan-feature", Kind: tracker.ScopePlanConcern, MissionID: snapshot.MissionID}, {ID: "requirement-portable", Kind: tracker.ScopeRequiredOutcome, MissionID: snapshot.MissionID}}, Work: []mission.WorkProposal{{Definition: missionDefinition(t, "work-foundation", nil, "plan-foundation", false)}, {Definition: missionDefinition(t, "work-feature", []ids.WorkID{"work-foundation"}, "plan-feature", true)}}, ActorID: "actor-implementer", OperationID: "operation-decompose", GovernanceContextID: "governance-implement", OccurredAt: now}
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &countingDefinitionProvider{port: fake.NewProvider(store), store: store}
	plans := &planSource{observation: approvalObservation(t, snapshot.MissionID, artifact.KindPlan, command.PlanRevisionID, planDigest, artifact.VerdictApproved)}
	return command, &missionSource{snapshot: snapshot}, plans, provider
}
func missionDefinition(t *testing.T, workID ids.WorkID, deps []ids.WorkID, concern string, requirement bool) tracker.WorkDefinition {
	t.Helper()
	provenance := []tracker.ScopeRef{{ID: concern, Kind: tracker.ScopePlanConcern}}
	if requirement {
		provenance = append(provenance, tracker.ScopeRef{ID: "requirement-portable", Kind: tracker.ScopeRequiredOutcome})
	}
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: "project-kitty", WorkID: workID, Title: "Implement " + workID.String(), Description: "Deliver one independently understandable work outcome.", DesignContext: "Use only provider-neutral contracts and stable identity.", Criteria: []tracker.WorkCriterion{{ID: "criterion-" + workID.String(), Text: "Verify the result", Outcome: "the result persists and is observable", Verifier: tracker.VerifierTest, Required: true}}, Dependencies: deps, WriteScopes: []tracker.WriteScope{{Path: "internal/" + workID.String(), Domain: workID.String(), Access: tracker.ScopeWriteExclusive}}, Provenance: provenance, MetadataSchema: 1})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
func approvalObservation(t *testing.T, missionID ids.MissionID, kind artifact.Kind, revisionID, digest string, verdict artifact.ApprovalVerdict) artifact.CurrentApprovalObservation {
	t.Helper()
	revision := artifactRevision(t, missionID, kind, revisionID, digest)
	approval := artifactApproval(t, "approval-"+revisionID, revision.ID(), verdict)
	return mustCurrentObservation(t, revision, []artifact.ArtifactApproval{approval})
}
func artifactRevision(t *testing.T, missionID ids.MissionID, kind artifact.Kind, id, digest string) artifact.ArtifactRevision {
	t.Helper()
	revision, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{ID: id, MissionID: missionID.String(), Kind: kind, RevisionNumber: 1, ContentDigest: digest, SourceRef: "kitty-specs/plan.md", SourceRevision: "source-plan", CreatedBy: "actor-planner", CreatedAt: time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
func artifactApproval(t *testing.T, id, revisionID string, verdict artifact.ApprovalVerdict) artifact.ArtifactApproval {
	t.Helper()
	approval, err := artifact.NewArtifactApproval(artifact.ArtifactApprovalParams{ID: id, ArtifactRevisionID: revisionID, Verdict: verdict, ActorID: "actor-reviewer", EvidenceDigest: strings.Repeat("e", 64), GovernanceContextID: "governance-review", SubmittedAt: time.Date(2026, 8, 27, 11, 30, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}
func mustCurrentObservation(t *testing.T, revision artifact.ArtifactRevision, approvals []artifact.ArtifactApproval) artifact.CurrentApprovalObservation {
	t.Helper()
	observation, err := artifact.NewCurrentApprovalObservation(revision, approvals)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
