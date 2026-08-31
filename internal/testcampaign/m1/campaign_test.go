package m1

import (
	"strings"
	"testing"
)

func TestCrownTrialRequiresExactOneRunOrderedExecution(t *testing.T) {
	artifact := validCrownArtifact(t, "run-crown")
	if err := ValidateCrownArtifact(artifact); err != nil {
		t.Fatal(err)
	}
	mutants := []func(*CrownArtifact){
		func(a *CrownArtifact) { a.Segments = a.Segments[1:] },
		func(a *CrownArtifact) { a.Segments[2], a.Segments[3] = a.Segments[3], a.Segments[2] },
		func(a *CrownArtifact) { a.Segments[4].RunID = "foreign-run" },
		func(a *CrownArtifact) { a.Segments[6].Terminal = false },
		func(a *CrownArtifact) { a.Trial.WorkspaceIsolation.W3Fresh = false },
		func(a *CrownArtifact) { a.Trial.OwnerRestart.AfterRuntimeEpochID = "epoch-before" },
		func(a *CrownArtifact) { a.Trial.Reviews[0].ReviewerActorID = "B" },
		func(a *CrownArtifact) { a.Trial.RestorePlayback.PlaybackAdapterCalls = 1 },
		func(a *CrownArtifact) { a.Trial.ArtifactSHA256 = "" },
		func(a *CrownArtifact) { a.Trial.ArtifactSHA256 = strings.Repeat("d", 64) },
	}
	for i, mutate := range mutants {
		candidate := cloneCrown(t, artifact)
		mutate(&candidate)
		if err := ValidateCrownArtifact(candidate); err == nil {
			t.Fatalf("crown mutant %d passed", i)
		}
	}
}

func TestFocusedSummariesCannotSynthesizeOrderedTrial(t *testing.T) {
	artifact := validCrownArtifact(t, "run-crown")
	artifact.Segments = nil
	if err := ValidateCrownArtifact(artifact); err == nil {
		t.Fatal("ordered trial passed without an actual segment ledger")
	}
}

func validCrownArtifact(t *testing.T, runID string) CrownArtifact {
	t.Helper()
	trial := OrderedTrial{
		SchemaVersion: 1, RunID: runID, TrialID: OrderedTrialID, Started: true, Terminal: true, Outcome: "pass", FR044Satisfied: true, SC013Satisfied: true,
		Identities:         CrownIdentities{CrownParticipant{"actor-a", "process-a", "workspace-crown-w1"}, CrownParticipant{"actor-b", "process-b", "workspace-crown-w2"}, CrownParticipant{"actor-a", "process-c", "workspace-crown-w3"}},
		WorkspaceIsolation: CrownWorkspaceIsolation{true, true, true, 0},
		OwnerRestart:       CrownOwnerRestart{true, "epoch-before", "epoch-after", true},
		OldCapsule:         CrownOldCapsule{"epoch-before", true, "runtime_epoch_conflict"},
		RestorePlayback:    CrownRestorePlayback{strings.Repeat("a", 64), strings.Repeat("a", 64), true, 0, 0},
		Reviews:            []CrownReview{{"candidate-w2", []string{"B"}, "reviewer-one", true, "approved"}, {"candidate-w3", []string{"A"}, "reviewer-two", true, "approved"}},
		Integration:        CrownIntegration{"refs/heads/main", []string{"candidate-w2", "candidate-w3"}, []string{"candidate-w2", "candidate-w3"}, 2, true, 2, 0},
	}
	segments := make([]CrownSegment, len(CrownSegmentIDs))
	for i, id := range CrownSegmentIDs {
		segments[i] = CrownSegment{RunID: runID, Sequence: i + 1, ID: id, BeforeDurableRevision: uint64(i), AfterDurableRevision: uint64(i + 1), Started: true, Terminal: true, Outcome: "pass", ArtifactSHA256: strings.Repeat("c", 64)}
	}
	artifact, err := SealCrownArtifact(CrownArtifact{SchemaVersion: 1, RunID: runID, Segments: segments, Trial: trial})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func cloneCrown(t *testing.T, input CrownArtifact) CrownArtifact {
	t.Helper()
	encoded, err := CanonicalJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	var output CrownArtifact
	if err := jsonUnmarshalStrict(encoded, &output); err != nil {
		t.Fatal(err)
	}
	return output
}
