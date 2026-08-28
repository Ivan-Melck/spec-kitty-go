package mission_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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
			service := newDecompositionTestService(t, missionState, plans, provider)
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
	service := newDecompositionTestService(t, missions, plans, provider)
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
		if item.WorkID() == "work-feature" {
			dependencies := item.Dependencies()
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

func TestDecompositionRootAuthorityBindsCompleteRequestBeforeProvider(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*mission.DecompositionCommand, *missionSource)
	}{
		{"state version", func(c *mission.DecompositionCommand, s *missionSource) {
			c.ExpectedMission.StateVersion++
			s.snapshot = c.ExpectedMission
		}},
		{"created at", func(c *mission.DecompositionCommand, s *missionSource) {
			c.ExpectedMission.CreatedAt = c.ExpectedMission.CreatedAt.Add(-time.Second)
			s.snapshot = c.ExpectedMission
		}},
		{"updated at", func(c *mission.DecompositionCommand, s *missionSource) {
			c.ExpectedMission.UpdatedAt = c.ExpectedMission.UpdatedAt.Add(time.Second)
			s.snapshot = c.ExpectedMission
		}},
		{"blocker membership", func(c *mission.DecompositionCommand, s *missionSource) {
			c.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-one"}
			s.snapshot = c.ExpectedMission
		}},
		{"decision membership", func(c *mission.DecompositionCommand, s *missionSource) {
			c.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-one"}
			s.snapshot = c.ExpectedMission
		}},
		{"actor", func(c *mission.DecompositionCommand, _ *missionSource) { c.ActorID = "actor-other" }},
		{"governance", func(c *mission.DecompositionCommand, _ *missionSource) { c.GovernanceContextID = "governance-other" }},
		{"occurred at", func(c *mission.DecompositionCommand, _ *missionSource) { c.OccurredAt = c.OccurredAt.Add(time.Second) }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			command, missions, plans, provider := decompositionFixture(t)
			service := newDecompositionTestService(t, missions, plans, provider)
			if _, err := service.Decompose(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			provider.createCalls, provider.observeCalls, provider.capabilityCalls = 0, 0, 0
			test.mutate(&command, missions)
			_, err := service.Decompose(context.Background(), command)
			var typed *mission.DecompositionError
			if !errors.As(err, &typed) || typed.Kind != mission.DecompositionOperationConflict {
				t.Fatalf("error=%T %v", err, err)
			}
			if provider.capabilityCalls != 0 || provider.createCalls != 0 || provider.observeCalls != 0 {
				t.Fatalf("operation conflict crossed provider boundary: %+v", provider)
			}
		})
	}
}

func TestDecompositionFreshRootRejectsPreexistingChildMutation(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	definition := command.Work[0].Definition
	digest := sha256.Sum256([]byte(command.OperationID.String() + "\x00" + definition.WorkID().String()))
	child := ids.OperationID(fmt.Sprintf("decompose-%x", digest[:16]))
	if _, err := provider.port.CreateDefinition(context.Background(), tracker.CreateWorkDefinitionRequest{OperationID: child, Definition: definition}); err != nil {
		t.Fatal(err)
	}
	result, err := newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
	var typed *mission.DecompositionError
	if !errors.As(err, &typed) || typed.Kind != mission.DecompositionProviderFailure || result.Status != mission.DecompositionAmbiguous {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDecompositionFreshRootRejectsPartiallyRepairedChildMutation(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	provider.createHook = func(_ tracker.CreateWorkDefinitionRequest, mutation tracker.WorkDefinitionMutation) tracker.WorkDefinitionMutation {
		mutation.Replayed = false
		mutation.PriorMutation = true
		return mutation
	}
	result, err := newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
	var typed *mission.DecompositionError
	if !errors.As(err, &typed) || typed.Kind != mission.DecompositionProviderFailure || result.Status != mission.DecompositionAmbiguous {
		t.Fatalf("result=%+v err=%v", result, err)
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
	result, err := newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
	if err == nil || len(result.Findings) == 0 || provider.prepareCalls != 0 || provider.createCalls != 0 {
		t.Fatalf("invalid graph result=%+v error=%v provider=%+v", result, err, provider)
	}
}

func TestDecompositionRefusesUnsupportedRequirementsBeforeRootReservation(t *testing.T) {
	tests := []struct {
		name         string
		capabilities tracker.WorkDefinitionCapabilities
		prepare      func(*testing.T, *mission.DecompositionCommand, *countingDefinitionProvider)
		wantKind     tracker.ErrorKind
	}{
		{
			name:         "guarded update",
			capabilities: tracker.WorkDefinitionCapabilities{Create: true, GuardedUpdate: false, BlockingGraph: true},
			wantKind:     tracker.ErrorGuardedUpdateUnsupported,
			prepare: func(t *testing.T, command *mission.DecompositionCommand, provider *countingDefinitionProvider) {
				created, err := provider.port.CreateDefinition(context.Background(), tracker.CreateWorkDefinitionRequest{OperationID: "operation-capability-seed", Definition: command.Work[0].Definition})
				if err != nil {
					t.Fatal(err)
				}
				reference := created.Observation.ExternalRef()
				command.Work[0].Existing = &reference
				command.Work[0].ExpectedObservation = created.Observation.ProviderObservation().Revision
			},
		},
		{
			name:         "blocking graph",
			capabilities: tracker.WorkDefinitionCapabilities{Create: true, GuardedUpdate: true, BlockingGraph: false},
			wantKind:     tracker.ErrorCapabilityUnsupported,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, missions, plans, provider := decompositionFixture(t)
			if test.prepare != nil {
				test.prepare(t, &command, provider)
			}
			provider.createCalls, provider.updateCalls, provider.observeCalls = 0, 0, 0

			_, err := mission.NewDecompositionService(missions, plans, provider, test.capabilities, provider.authority).Decompose(context.Background(), command)
			var typed *mission.DecompositionError
			if !errors.As(err, &typed) || typed.Kind != mission.DecompositionProviderCapability || !tracker.IsKind(err, test.wantKind) {
				t.Fatalf("error=%T %v", err, err)
			}
			if provider.capabilityCalls != 0 || provider.authority.calls != 0 || len(provider.authority.seen) != 0 || provider.createCalls != 0 || provider.updateCalls != 0 || provider.observeCalls != 0 {
				t.Fatalf("capability refusal crossed authority boundary: provider=%+v authority=%+v", provider, provider.authority)
			}
		})
	}
}

func TestDecompositionUsesTheCompositionCapabilitySnapshot(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	declared := tracker.WorkDefinitionCapabilities{Create: true, GuardedUpdate: true, BlockingGraph: true}
	service := mission.NewDecompositionService(missions, plans, provider, declared, provider.authority)
	provider.capabilityOverride = &tracker.WorkDefinitionCapabilities{}
	provider.capabilityErr = errors.New("request-time capability discovery must not run")
	result, err := service.Decompose(context.Background(), command)
	if err != nil || result.Status != mission.DecompositionComplete || provider.capabilityCalls != 0 || provider.authority.calls != 1 {
		t.Fatalf("result=%+v err=%v provider=%+v authority=%+v", result, err, provider, provider.authority)
	}
}

func TestDecompositionRejectsLocallyDetectableUpdateMappingMismatchesBeforeProviderCalls(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*mission.WorkProposal)
	}{
		{"project identity", func(p *mission.WorkProposal) { p.Existing.ProjectID = "project-other" }},
		{"work identity", func(p *mission.WorkProposal) { p.Existing.WorkID = "work-other" }},
		{"provider identity", func(p *mission.WorkProposal) { p.Existing.Provider = "foreign-provider" }},
		{"metadata schema", func(p *mission.WorkProposal) { p.Existing.MetadataSchema++ }},
		{"expected observation", func(p *mission.WorkProposal) { p.ExpectedObservation++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, missions, plans, provider := decompositionFixture(t)
			created, err := provider.port.CreateDefinition(context.Background(), tracker.CreateWorkDefinitionRequest{OperationID: "operation-seed-update", Definition: command.Work[0].Definition})
			if err != nil {
				t.Fatal(err)
			}
			reference := created.Observation.ExternalRef()
			command.Work[0].Existing = &reference
			command.Work[0].ExpectedObservation = created.Observation.ProviderObservation().Revision
			test.mutate(&command.Work[0])
			_, err = newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
			var typed *mission.DecompositionError
			if !errors.As(err, &typed) || typed.Kind != mission.DecompositionInvalidRequest {
				t.Fatalf("mapping mismatch error = %T %v", err, err)
			}
			if provider.capabilityCalls != 0 || provider.prepareCalls != 0 || provider.createCalls != 0 || provider.updateCalls != 0 || provider.observeCalls != 0 {
				t.Fatalf("mapping mismatch crossed provider boundary: %+v", provider)
			}
		})
	}
}

func TestDecompositionAcceptsAnExactlyAttributedGuardedUpdate(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	created, err := provider.port.CreateDefinition(context.Background(), tracker.CreateWorkDefinitionRequest{OperationID: "operation-seed-exact-update", Definition: command.Work[0].Definition})
	if err != nil {
		t.Fatal(err)
	}
	reference := created.Observation.ExternalRef()
	command.Work[0].Existing = &reference
	command.Work[0].ExpectedObservation = created.Observation.ProviderObservation().Revision
	result, err := newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
	if err != nil || result.Status != mission.DecompositionComplete || len(result.Observations) != 2 || provider.updateCalls != 1 || provider.createCalls != 1 {
		t.Fatalf("exact update result=%+v error=%v provider=%+v", result, err, provider)
	}
	for _, observed := range result.Observations {
		if observed.Definition().WorkID() == command.Work[0].Definition.WorkID() && observed.ExternalRef().ExternalID != reference.ExternalID {
			t.Fatalf("guarded update changed external attribution: %+v", observed.ExternalRef())
		}
	}
}

func TestDecompositionRejectsUnattributedProviderResultsWithoutAdoptingState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *countingDefinitionProvider)
	}{
		{"wrong provider", func(t *testing.T, p *countingDefinitionProvider) {
			p.createHook = func(_ tracker.CreateWorkDefinitionRequest, mutation tracker.WorkDefinitionMutation) tracker.WorkDefinitionMutation {
				mutation.Observation = rewriteDefinitionObservation(t, mutation.Observation, mutation.Observation.Definition(), func(ref *tracker.ExternalWorkRef) { ref.Provider = "foreign-provider" })
				return mutation
			}
		}},
		{"swapped observation", func(_ *testing.T, p *countingDefinitionProvider) {
			var first tracker.WorkDefinitionObservation
			p.createHook = func(_ tracker.CreateWorkDefinitionRequest, mutation tracker.WorkDefinitionMutation) tracker.WorkDefinitionMutation {
				if first.ProviderObservation().Revision == 0 {
					first = mutation.Observation
					return mutation
				}
				mutation.Observation = first
				return mutation
			}
		}},
		{"zero observation success", func(_ *testing.T, p *countingDefinitionProvider) {
			p.createHook = func(_ tracker.CreateWorkDefinitionRequest, mutation tracker.WorkDefinitionMutation) tracker.WorkDefinitionMutation {
				mutation.Observation = tracker.WorkDefinitionObservation{}
				return mutation
			}
		}},
		{"request content mismatch", func(t *testing.T, p *countingDefinitionProvider) {
			p.createHook = func(_ tracker.CreateWorkDefinitionRequest, mutation tracker.WorkDefinitionMutation) tracker.WorkDefinitionMutation {
				definition := mutation.Observation.Definition()
				changed, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Title: "Provider changed title", Description: definition.Description(), DesignContext: definition.DesignContext(), Criteria: definition.Criteria(), Dependencies: definition.Dependencies(), WriteScopes: definition.WriteScopes(), Provenance: definition.Provenance(), MetadataSchema: definition.MetadataSchema(), ProviderMetadata: definition.ProviderMetadata()})
				if err != nil {
					t.Fatal(err)
				}
				mutation.Observation = rewriteDefinitionObservation(t, mutation.Observation, changed, nil)
				return mutation
			}
		}},
		{"operation attribution mismatch", func(t *testing.T, p *countingDefinitionProvider) {
			p.createHook = func(_ tracker.CreateWorkDefinitionRequest, mutation tracker.WorkDefinitionMutation) tracker.WorkDefinitionMutation {
				mutation.Observation = rewriteDefinitionObservation(t, mutation.Observation, mutation.Observation.Definition(), func(ref *tracker.ExternalWorkRef) { ref.CreatedByOperation = "operation-foreign" })
				return mutation
			}
		}},
		{"re-observation provider mismatch", func(t *testing.T, p *countingDefinitionProvider) {
			p.observeHook = func(_ tracker.ExternalWorkRef, observation tracker.WorkDefinitionObservation) tracker.WorkDefinitionObservation {
				return rewriteDefinitionObservation(t, observation, observation.Definition(), func(ref *tracker.ExternalWorkRef) { ref.Provider = "foreign-provider" })
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, missions, plans, provider := decompositionFixture(t)
			test.setup(t, provider)
			result, err := newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
			var typed *mission.DecompositionError
			if !errors.As(err, &typed) || typed.Kind != mission.DecompositionProviderFailure || typed.Recovery != mission.DecompositionObserveThenReconcile || typed.Field != "provider_attribution" {
				t.Fatalf("attribution error = %T %+v result=%+v", err, typed, result)
			}
			if result.Status != mission.DecompositionAmbiguous || result.Recovery != mission.DecompositionObserveThenReconcile || result.Draft.Work() != nil {
				t.Fatalf("unattributed state was accepted: %+v", result)
			}
			if len(typed.Field) > 160 || len(typed.Message) > 256 {
				t.Fatalf("unbounded attribution diagnostic: %+v", typed)
			}
		})
	}
}

func TestDecompositionSurfacesTimeoutAfterApplyAsAmbiguous(t *testing.T) {
	command, missions, plans, provider := decompositionFixture(t)
	if err := provider.store.Inject(context.Background(), fake.FailureTimeoutAfterApply); err != nil {
		t.Fatal(err)
	}
	result, err := newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
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
	result, err := newDecompositionTestService(t, missions, plans, provider).Decompose(context.Background(), command)
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
	authority                                                             *countingDecompositionAuthority
	capabilityCalls, prepareCalls, createCalls, updateCalls, observeCalls int
	failCreateAt                                                          int
	createHook                                                            func(tracker.CreateWorkDefinitionRequest, tracker.WorkDefinitionMutation) tracker.WorkDefinitionMutation
	observeHook                                                           func(tracker.ExternalWorkRef, tracker.WorkDefinitionObservation) tracker.WorkDefinitionObservation
	capabilityOverride                                                    *tracker.WorkDefinitionCapabilities
	capabilityErr                                                         error
}

func (p *countingDefinitionProvider) DefinitionCapabilities(ctx context.Context) (tracker.WorkDefinitionCapabilities, error) {
	p.capabilityCalls++
	if p.capabilityErr != nil {
		return tracker.WorkDefinitionCapabilities{}, p.capabilityErr
	}
	if p.capabilityOverride != nil {
		return *p.capabilityOverride, nil
	}
	return p.port.DefinitionCapabilities(ctx)
}
func (p *countingDefinitionProvider) CreateDefinition(ctx context.Context, r tracker.CreateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	p.createCalls++
	if p.failCreateAt != 0 && p.createCalls == p.failCreateAt {
		return tracker.WorkDefinitionMutation{}, &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "injected provider outage"}
	}
	mutation, err := p.port.CreateDefinition(ctx, r)
	if err == nil && p.createHook != nil {
		mutation = p.createHook(r, mutation)
	}
	return mutation, err
}
func (p *countingDefinitionProvider) UpdateDefinition(ctx context.Context, r tracker.UpdateWorkDefinitionRequest) (tracker.WorkDefinitionMutation, error) {
	p.updateCalls++
	return p.port.UpdateDefinition(ctx, r)
}
func (p *countingDefinitionProvider) ObserveDefinition(ctx context.Context, r tracker.ExternalWorkRef) (tracker.WorkDefinitionObservation, error) {
	p.observeCalls++
	observation, err := p.port.ObserveDefinition(ctx, r)
	if err == nil && p.observeHook != nil {
		observation = p.observeHook(r, observation)
	}
	return observation, err
}

func decompositionFixture(t *testing.T) (mission.DecompositionCommand, *missionSource, *planSource, *countingDefinitionProvider) {
	t.Helper()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	snapshot := mission.MissionSnapshot{MissionID: "mission-kitty", ProjectID: "project-kitty", DefinitionID: "software-dev", DefinitionVersion: 1, DefinitionCommitment: mission.DefinitionCommitment(strings.Repeat("c", 64)), Lifecycle: mission.LifecycleOpen, CurrentStep: mission.StepPlanApproved, StateVersion: 5, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)}
	planDigest := strings.Repeat("a", 64)
	command := mission.DecompositionCommand{MissionID: snapshot.MissionID, ProjectID: snapshot.ProjectID, Provider: "durable_fake", ExpectedMission: snapshot, PlanRevisionID: "plan-revision-1", PlanContentDigest: planDigest, ApprovedScope: []workgraph.ApprovedScope{{ID: "plan-foundation", Kind: workgraph.ScopePlanConcern, MissionID: snapshot.MissionID}, {ID: "plan-feature", Kind: workgraph.ScopePlanConcern, MissionID: snapshot.MissionID}, {ID: "requirement-portable", Kind: workgraph.ScopeRequiredOutcome, MissionID: snapshot.MissionID}}, Work: []mission.WorkProposal{{Definition: missionDefinition(t, "work-foundation", nil, "plan-foundation", false)}, {Definition: missionDefinition(t, "work-feature", []ids.WorkID{"work-foundation"}, "plan-feature", true)}}, ActorID: "actor-implementer", OperationID: "operation-decompose", GovernanceContextID: "governance-implement", OccurredAt: now}
	store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &countingDefinitionProvider{port: fake.NewProvider(store), store: store, authority: &countingDecompositionAuthority{seen: map[ids.OperationID]string{}}}
	plans := &planSource{observation: approvalObservation(t, snapshot.MissionID, artifact.KindPlan, command.PlanRevisionID, planDigest, artifact.VerdictApproved)}
	return command, &missionSource{snapshot: snapshot}, plans, provider
}

func newDecompositionTestService(t *testing.T, missions *missionSource, plans *planSource, provider *countingDefinitionProvider) *mission.DecompositionService {
	t.Helper()
	capabilities, err := provider.port.DefinitionCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return mission.NewDecompositionService(missions, plans, provider, capabilities, provider.authority)
}

type countingDecompositionAuthority struct {
	seen  map[ids.OperationID]string
	calls int
}

func (a *countingDecompositionAuthority) ReserveDecompositionOperation(_ context.Context, request mission.DecompositionOperationRequest) (mission.DecompositionOperationReservation, error) {
	a.calls++
	if prior, exists := a.seen[request.OperationID]; exists {
		if prior != request.RequestDigest {
			return mission.DecompositionOperationReservation{}, &mission.DecompositionError{Kind: mission.DecompositionOperationConflict, Recovery: mission.DecompositionCorrectInput, Field: "operation_id", Message: "operation identity was reused"}
		}
		return mission.DecompositionOperationReservation{Replayed: true}, nil
	}
	a.seen[request.OperationID] = request.RequestDigest
	return mission.DecompositionOperationReservation{}, nil
}

func rewriteDefinitionObservation(t *testing.T, source tracker.WorkDefinitionObservation, definition tracker.WorkDefinition, mutate func(*tracker.ExternalWorkRef)) tracker.WorkDefinitionObservation {
	t.Helper()
	reference := source.ExternalRef()
	if mutate != nil {
		mutate(&reference)
	}
	rewritten, err := tracker.NewWorkDefinitionObservation(definition, reference, source.ProviderObservation(), source.ObservedAt())
	if err != nil {
		t.Fatal(err)
	}
	return rewritten
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
