package scenario

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

type scriptedAssignmentProjection struct {
	base        tracker.AssignmentProjection
	observation tracker.AssignmentObservation
	err         error
	observes    int
	mutations   int
	compensates int
}

func (p *scriptedAssignmentProjection) ObserveAssignment(context.Context, tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	p.observes++
	return p.observation, p.err
}

func (p *scriptedAssignmentProjection) ProjectAssignment(ctx context.Context, request tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.mutations++
	return p.base.ProjectAssignment(ctx, request)
}

func (p *scriptedAssignmentProjection) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.compensates++
	return p.base.CompensateAssignment(ctx, request)
}

func TestDishonestFakeTrackerMatrixPreservesAuthorityV2(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{"unavailable", "unavailable"},
		{"stale_revision", "stale"},
		{"contradictory_value", "contradictory"},
		{"unattributed_marker", "unattributed"},
		{"malicious_human_change", "malicious"},
		{"permanently_ambiguous", "ambiguous"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
			h.createMission("project-dishonest", "mission-dishonest", "operation-create-dishonest")
			before := assertUniversalInvariants(t, h, "project-dishonest", "mission-dishonest", "before-"+test.mode)
			beforeStatus := h.host.Status()

			store, err := trackerfake.Open(filepath.Join(h.root, "dishonest-tracker.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Seed(t.Context(), trackerfake.WorkSeed{Key: "ticket-dishonest", Title: "Dishonest provider matrix"}); err != nil {
				t.Fatal(err)
			}
			base := trackerfake.NewProvider(store)
			target := tracker.ExternalWorkRef{ProjectID: "project-dishonest", WorkID: "work-dishonest", Provider: "fake", ExternalID: "ticket-dishonest", MetadataSchema: 1, CreatedByOperation: "operation-decompose", LastObservation: 1}
			initial, err := base.ObserveAssignment(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			applied, err := base.ProjectAssignment(t.Context(), tracker.ProjectAssignmentRequest{ExternalRef: target, OperationID: "operation-intended", Assignee: "actor-intended", ExpectedObservation: 1, RequiredGuarantee: tracker.AssignmentAtomicClaim})
			if err != nil {
				t.Fatal(err)
			}
			target.LastObservation = applied.Observation.Observation.Revision
			scripted := &scriptedAssignmentProjection{base: base, observation: applied.Observation}
			switch test.mode {
			case "unavailable":
				scripted.err = &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "scripted unavailable"}
			case "stale":
				scripted.observation = initial
			case "contradictory":
				changed, changeErr := base.ProjectAssignment(t.Context(), tracker.ProjectAssignmentRequest{ExternalRef: target, OperationID: "operation-other", Assignee: "actor-other", ExpectedObservation: target.LastObservation, RequiredGuarantee: tracker.AssignmentAtomicClaim})
				if changeErr != nil {
					t.Fatal(changeErr)
				}
				scripted.observation = changed.Observation
			case "unattributed":
				scripted.observation = initial
			case "malicious":
				changed, changeErr := base.ProjectAssignment(t.Context(), tracker.ProjectAssignmentRequest{ExternalRef: target, OperationID: "operation-human", Assignee: "human-owner", ExpectedObservation: target.LastObservation, RequiredGuarantee: tracker.AssignmentAtomicClaim})
				if changeErr != nil {
					t.Fatal(changeErr)
				}
				scripted.observation = changed.Observation
			case "ambiguous":
				scripted.err = &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "scripted permanent ambiguity"}
			}
			dispatcher, err := execution.NewAssignmentEffectDispatcherV2(scripted)
			if err != nil {
				t.Fatal(err)
			}
			attempts := 1
			if test.mode == "ambiguous" {
				attempts = 3
			}
			for attempt := 0; attempt < attempts; attempt++ {
				observation, observeErr := dispatcher.ObserveEffect(t.Context(), target)
				attributed := observeErr == nil && observation.ProviderRevision == target.LastObservation && observation.Value == "actor-intended" && observation.OperationMarker == "operation-intended"
				if attributed {
					t.Fatalf("%s observation was incorrectly accepted as current attributable intent", test.mode)
				}
			}
			if scripted.mutations != 0 || scripted.compensates != 0 {
				t.Fatalf("uncertain observation caused blind external action: mutate=%d compensate=%d", scripted.mutations, scripted.compensates)
			}
			after := assertUniversalInvariants(t, h, "project-dishonest", "mission-dishonest", "after-"+test.mode)
			afterStatus := h.host.Status()
			if string(before) == "" || string(after) == "" || h.git.count() != 0 || beforeStatus.ProjectionRevision != afterStatus.ProjectionRevision || beforeStatus.ProjectionSHA256 != afterStatus.ProjectionSHA256 {
				t.Fatalf("dishonest observation escaped the authority boundary")
			}
		})
	}
}

var _ tracker.AssignmentProjection = (*scriptedAssignmentProjection)(nil)
