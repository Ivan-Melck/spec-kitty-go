package faults

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/provingground"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
)

func TestCrownInternalSegmentOrderPreservesDefinitiveW3(t *testing.T) {
	want := []string{
		"concurrent-a-a-w1-b-b-w2", "gj-01", "gj-02",
		"expire-quarantine-w1", "gj-03", "gj-04", "replace-a-c-w3",
		"zombie-write-refusal", "candidate-w2", "candidate-w3", "review-w2",
		"review-w3", "integrate-w2", "integrate-w3", "gj-05", "owner-restart",
		"restore-new-epoch", "predecessor-refusal", "playback-equality",
	}
	if !slices.Equal(crownInternalSegmentIDs, want) {
		t.Fatalf("internal crown choreography drifted:\n got %v\nwant %v", crownInternalSegmentIDs, want)
	}
	index := func(id string) int { return slices.Index(crownInternalSegmentIDs, id) }
	if !(index("gj-04") < index("replace-a-c-w3") && index("replace-a-c-w3") < index("zombie-write-refusal") && index("zombie-write-refusal") < index("candidate-w3")) {
		t.Fatal("fault attempts must precede the definitive W3, whose exact workspace must survive zombie isolation into candidacy")
	}
}

func TestProductionOrderedCrownTrialValidatesFromActualExecution(t *testing.T) {
	if os.Getenv("SPEC_KITTY_M1_CROWN_INTEGRATION") == "" {
		t.Skip("exact ordered crown integration is opt-in")
	}
	lifecycle := startFaultLifecycle(t)
	executor := &ProductionExecutor{Root: faultModuleRoot(t), Lifecycle: lifecycle, RunID: "run-fault-production"}
	t.Cleanup(func() { _ = executor.Close() })
	execution, err := executor.RunOrderedCrown(t.Context(), CrownInput{RunID: executor.RunID, Seed: 7100})
	if err != nil {
		t.Fatal(err)
	}
	segments := make([]m1.CrownSegment, 0, len(m1.CrownSegmentIDs))
	for i, id := range m1.CrownSegmentIDs {
		segment := m1.CrownSegment{RunID: executor.RunID, Sequence: i + 1, ID: id, Started: true, Terminal: true, Outcome: "pass", ArtifactSHA256: m1.SHA256Hex([]byte(id))}
		if internal := slices.Index(crownInternalSegmentIDs, id); internal >= 0 {
			segment = execution.Segments[internal]
			segment.Sequence = i + 1
		}
		segments = append(segments, segment)
	}
	artifact, err := m1.SealCrownArtifact(m1.CrownArtifact{SchemaVersion: 1, RunID: executor.RunID, Segments: segments, Trial: execution.Trial})
	if err != nil {
		t.Fatal(err)
	}
	if err := m1.ValidateCrownArtifact(artifact); err != nil {
		t.Fatalf("actual driver trial did not validate: %v", err)
	}
}

func TestOrderedCrownRejectsMissingImmutableIdentityBeforeProductAccess(t *testing.T) {
	executor := &ProductionExecutor{RunID: "umbrella-run"}
	_, err := executor.RunOrderedCrown(context.Background(), CrownInput{RunID: "foreign-run", Seed: 1})
	if err == nil {
		t.Fatal("foreign umbrella RunID was accepted")
	}
	_, err = executor.RunOrderedCrown(context.Background(), CrownInput{RunID: "umbrella-run"})
	if err == nil {
		t.Fatal("zero deterministic seed was accepted")
	}
}

func TestCrownParticipantUsesExactAuthorityIdentity(t *testing.T) {
	delivery := provingground.Delivery{Authority: controljournal.AuthorityCapsuleV1{ActorID: "actor-exact", ProcessID: "process-exact", WorkspaceID: "workspace-exact"}}
	got := crownParticipant(delivery)
	if got.ActorID != "actor-exact" || got.ProcessID != "process-exact" || got.WorkspaceID != "workspace-exact" {
		t.Fatalf("participant identity was translated instead of observed: %+v", got)
	}
}
