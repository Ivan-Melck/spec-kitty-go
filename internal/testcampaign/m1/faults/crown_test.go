package faults

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/provingground"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
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
	if len(execution.Faults) < 2 || execution.Faults[1].FaultID != "GJ-02" {
		t.Fatalf("ordered crown did not retain its GJ-02 execution: %+v", execution.Faults)
	}
	gj02 := execution.Faults[1]
	if gj02.Facts.HeartbeatWorkID != "work-baseline" || !slices.Equal(gj02.Facts.AuthorityHeadIDs, []string{"work-a", "work-baseline"}) {
		t.Fatalf("GJ-02 did not use one configured authority or polluted its durable census: %+v", gj02.Facts)
	}
	if snapshot := executor.environment.runtime.QualificationSnapshot(); snapshot.PredecessorEpoch == "" || len(snapshot.Authorities) != 1 || snapshot.Authorities[0].WorkID != "work-baseline" {
		t.Fatalf("GJ-02 followed by owner replacement and validated restore did not retain the configured authority census: %+v", snapshot)
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
	barrier := newTestFocusedBarrier(2)
	focused, err := executor.ExecuteFocused(t.Context(), FocusedInput{
		RunID: executor.RunID, CampaignID: "atomic-publication-nfr001",
		AttemptID: "post-ordered-crown-atomic", Index: 0, Seed: 7200,
		Order: [2]int{0, 1}, Barrier: barrier,
	})
	if err != nil {
		t.Fatalf("focused atomic execution after ordered-crown owner restart: %v", err)
	}
	if !focused.Facts["gj01_cut_once"] {
		t.Fatalf("post-restart GJ-01 did not reach the real pre-publication cut exactly once: %v", focused.Facts)
	}
	for index, campaignID := range []string{"lost-response-idempotency-nfr002", "concurrent-claims-nfr003", "competing-integrations-nfr004"} {
		barrier = newTestFocusedBarrier(2)
		focused, err = executor.ExecuteFocused(t.Context(), FocusedInput{
			RunID: executor.RunID, CampaignID: campaignID,
			AttemptID: "post-ordered-crown-" + campaignID, Index: index + 1, Seed: uint64(7201 + index),
			Order: [2]int{index % 2, 1 - index%2}, Barrier: barrier,
		})
		if err != nil {
			t.Fatalf("focused %s after ordered-crown owner restart: %v", campaignID, err)
		}
	}
	if !focused.Facts["one_target_authority"] || !focused.Facts["one_candidate_publication"] {
		t.Fatalf("post-restart integration did not restore sealed predecessor history and serialize publication: %v", focused.Facts)
	}
	barrier = newTestFocusedBarrier(2)
	focused, err = executor.ExecuteFocused(t.Context(), FocusedInput{
		RunID: executor.RunID, CampaignID: "restore-playback-nfr005",
		AttemptID: "post-ordered-crown-restore-playback", Index: 5, Seed: 7205,
		Order: [2]int{1, 0}, Barrier: barrier,
	})
	if err != nil {
		t.Fatalf("focused restore/playback after accumulated ordered history: %v", err)
	}
	if !focused.Facts["restore_ready"] || !focused.Facts["digest_equal"] || !focused.Facts["playback_zero_io"] || !m1.AssertUniversal(t.Context(), focused.Observation).Passed() {
		t.Fatalf("accumulated-history restore/playback did not retain exact evidence: facts=%v", focused.Facts)
	}
	current := executor.environment.workA
	if current == nil {
		t.Fatal("focused qualification authority is absent after integration")
	}
	issuedAt := current.Run.Deadline.Add(-qualificationLeaseTTL)
	afterFormerLimit := issuedAt.Add(2 * time.Minute)
	refreshed, err := executor.environment.runtime.Heartbeat(
		t.Context(),
		"post-minute-qualification-heartbeat",
		current.Authority,
		afterFormerLimit,
		qualificationLeaseTTL,
	)
	if err != nil {
		t.Fatalf("qualification operation crossing the former one-minute limit was refused: %v", err)
	}
	current.Authority = refreshed
	current.Run.Deadline = afterFormerLimit.Add(qualificationLeaseTTL)
	if err := executor.environment.runtime.Authorize(refreshed, "qualification-deadline-probe", current.Run.Deadline); !provingground.IsOutcome(err, provingground.OutcomeStaleLease) {
		t.Fatalf("qualification authority was not refused at its exact declared deadline: %v", err)
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

func TestGJ03ExactWorkspaceIdentityIgnoresOldWorkBindingAndRejectsRelabeling(t *testing.T) {
	snapshot := provingground.QualificationSnapshot{Workspaces: []provingground.WorkspaceBinding{{WorkspaceID: "workspace-crown-w1", WorkID: "work-a", Status: "quarantined"}}}
	if snapshotHasWorkspaceBinding(snapshot, "workspace-gj03") {
		t.Fatal("an old binding for the same WorkID contaminated the GJ-03 exact workspace probe")
	}
	snapshot.Workspaces = append(snapshot.Workspaces, provingground.WorkspaceBinding{WorkspaceID: "workspace-gj03", WorkID: "work-a", Status: "active-bound"})
	if !snapshotHasWorkspaceBinding(snapshot, "workspace-gj03") {
		t.Fatal("the exact cut-created workspace binding was not detected")
	}

	w1 := provingground.Delivery{Authority: controljournal.AuthorityCapsuleV1{WorkspaceID: "workspace-crown-w1"}}
	w2 := provingground.Delivery{Authority: controljournal.AuthorityCapsuleV1{WorkspaceID: "workspace-crown-w2"}}
	gj03 := Record{Outcome: OutcomePass, Facts: Facts{PhysicalWorkspaceID: "workspace-gj03", RecoveredWorkspaceID: "workspace-gj03-recovered"}}
	if err := validateCrownFaultWorkspaceIdentities(w1, w2, gj03, "workspace-crown-w3"); err != nil {
		t.Fatal(err)
	}
	for _, collision := range []controljournal.WorkspaceID{"workspace-gj03", "workspace-gj03-recovered", "workspace-crown-w1", "workspace-crown-w2"} {
		if err := validateCrownFaultWorkspaceIdentities(w1, w2, gj03, collision); err == nil {
			t.Fatalf("definitive W3 relabeling %q was accepted", collision)
		}
	}
	gj03.Facts.RecoveredWorkspaceID = gj03.Facts.PhysicalWorkspaceID
	if err := validateCrownFaultWorkspaceIdentities(w1, w2, gj03, "workspace-crown-w3"); err == nil {
		t.Fatal("GJ-03 orphan was relabeled as its recovered workspace")
	}

	for _, test := range []struct {
		name     string
		headIDs  []string
		required string
		want     bool
	}{
		{"baseline-only", []string{"work-baseline"}, "work-baseline", true},
		{"ordered-crown", []string{"work-a", "work-baseline"}, "work-baseline", true},
		{"missing-target", []string{"work-a"}, "work-baseline", false},
		{"foreign-qualification-probe", []string{"qualification-gj02-mutant", "work-a", "work-baseline"}, "work-baseline", false},
	} {
		t.Run("gj02-authority-census-"+test.name, func(t *testing.T) {
			if got := authorityHeadCensusIsConfigured(test.headIDs, test.required); got != test.want {
				t.Fatalf("authority census result=%v want=%v heads=%v", got, test.want, test.headIDs)
			}
		})
	}
}

func TestCrownCandidateAndReviewEvidenceAreCanonicalContentHashes(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	sourceCommit := strings.Repeat("1", 40)
	for index, test := range []struct {
		label, work, actor, process, workspace, lease, candidate, reviewer string
	}{
		{"w2", "work-baseline", "actor-b", "process-b", "workspace-crown-w2", "lease-crown-w2", strings.Repeat("2", 40), "reviewer-w2"},
		{"w3", "work-a", "actor-a", "process-c", "workspace-crown-w3", "lease-crown-w3", strings.Repeat("3", 40), "reviewer-w3"},
	} {
		delivery := provingground.Delivery{Authority: controljournal.AuthorityCapsuleV1{RunID: "run-crown", WorkspaceID: controljournal.WorkspaceID(test.workspace), LeaseID: controljournal.LeaseID(test.lease), LeaseGeneration: 1, FenceToken: uint64(index + 17), ActorID: controljournal.ActorID(test.actor), ProcessID: controljournal.ProcessID(test.process)}}
		workID := ids.WorkID(test.work)
		handoffFact, err := crownEligibility{}.WorkEligibilityFact(t.Context(), "revision-qualification", workID, workgraph.StatusActive, workgraph.EligibilityHandoff)
		if err != nil {
			t.Fatal(err)
		}
		handoffRequest, handoffEvidence, err := newCrownHandoffRequest(test.label, test.candidate, sourceCommit, delivery, workID, handoffFact, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := handoffRequest.Validate(); err != nil {
			t.Fatalf("%s exact handoff did not validate: %v", test.label, err)
		}
		handoffBytes, err := m1.CanonicalJSON(handoffEvidence)
		if err != nil {
			t.Fatal(err)
		}
		if handoffRequest.EvidenceDigest != m1.SHA256Hex(handoffBytes) || handoffRequest.EvidenceDigest == strings.Repeat(test.label[:1], 64) || handoffEvidence.SourceCommit != sourceCommit || handoffEvidence.Candidate != test.candidate || handoffEvidence.WorkspaceID != test.workspace {
			t.Fatalf("%s handoff evidence is not its canonical content hash", test.label)
		}
		reviewFact, err := crownEligibility{}.WorkEligibilityFact(t.Context(), "revision-qualification", workID, workgraph.StatusHandedOff, workgraph.EligibilityReview)
		if err != nil {
			t.Fatal(err)
		}
		reviewRequest, reviewEvidence, err := newCrownReviewRequest(test.label, test.candidate, test.reviewer, handoffRequest.EvidenceDigest, delivery, workID, reviewFact, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := reviewRequest.Validate(); err != nil {
			t.Fatalf("%s exact review did not validate: %v", test.label, err)
		}
		reviewBytes, err := m1.CanonicalJSON(reviewEvidence)
		if err != nil {
			t.Fatal(err)
		}
		if reviewRequest.EvidenceDigest != m1.SHA256Hex(reviewBytes) || reviewRequest.EvidenceDigest == strings.Repeat("e", 64) || reviewEvidence.CandidateEvidenceSHA256 != handoffRequest.EvidenceDigest || reviewEvidence.Candidate != test.candidate {
			t.Fatalf("%s review evidence is not its canonical content hash", test.label)
		}
	}
}
