package scenario

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

var (
	phase1StartFaults         = []string{"SR-00", "SR-01", "SR-02", "SR-03", "SR-04", "SR-05", "SR-06", "SR-07", "SR-08", "SR-09"}
	phase1HandoffReviewFaults = []string{"HR-00", "HR-01", "HR-02", "HR-03", "HR-04", "HR-05", "HR-06", "HR-07"}
	phase1IntegrationFaults   = []string{"IG-00", "IG-01", "IG-02", "IG-03", "IG-04", "IG-05", "IG-06", "IG-07", "IG-08", "IG-09"}
)

func TestPhase1NamedFaultBoundaryMatrix(t *testing.T) {
	t.Run("start-run", testPhase1StartRunFaults)
	t.Run("handoff-review", testPhase1HandoffReviewFaults)
	t.Run("integration", testPhase1IntegrationFaults)
}

func TestPhase1UniversalAntiVacuityControls(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "phase1-universal-mutant")
	expected := newPhase1ExpectedState()
	expected.addCapsule(capsule, true)
	base := universalAssertionState{
		ScenarioID: "phase1-universal-mutant", FaultID: "MUTANT", Database: fixture.database, TrackerPath: fixture.tracker,
		RepositoryPath: fixture.repository, Repository: fixture.primary, ExpectedPrimary: fixture.primary.Binding().Primary,
		ExpectedWorkspaces: expected.workspaces, ExpectedAuthority: expected.authority,
	}
	t.Run("deleted-stale-probe", func(t *testing.T) {
		mutant := base
		mutant.RequireStaleProbe = true
		if err := assertUniversalInvariants(context.Background(), mutant); err == nil || !strings.Contains(err.Error(), "invariant=stale_token_refusal") || !strings.Contains(err.Error(), "required stale probe is absent") {
			t.Fatalf("missing stale-probe mutant survived: %v", err)
		}
	})
	t.Run("nil-workspace-expectation", func(t *testing.T) {
		mutant := base
		mutant.ExpectedWorkspaces = nil
		if err := assertUniversalInvariants(context.Background(), mutant); err == nil || !strings.Contains(err.Error(), "invariant=immutable_workspace_binding") || !strings.Contains(err.Error(), "expected workspace map is absent") {
			t.Fatalf("nil workspace-expectation mutant survived: %v", err)
		}
	})
	t.Run("nil-authority-expectation", func(t *testing.T) {
		mutant := base
		mutant.ExpectedAuthority = nil
		if err := assertUniversalInvariants(context.Background(), mutant); err == nil || !strings.Contains(err.Error(), "invariant=one_valid_lease") || !strings.Contains(err.Error(), "expected authority map is absent") {
			t.Fatalf("nil authority-expectation mutant survived: %v", err)
		}
	})
	t.Run("unexpected-workspace-row", func(t *testing.T) {
		mutant := base
		mutant.ExpectedWorkspaces = map[ids.WorkspaceID]workspaceBindingFact{}
		if err := assertUniversalInvariants(context.Background(), mutant); err == nil || !strings.Contains(err.Error(), "invariant=immutable_workspace_binding") {
			t.Fatalf("workspace deletion mutant survived: %v", err)
		}
	})
	t.Run("unexpected-active-lease", func(t *testing.T) {
		mutant := base
		mutant.ExpectedAuthority = map[string]activeLeaseFact{}
		if err := assertUniversalInvariants(context.Background(), mutant); err == nil || !strings.Contains(err.Error(), "invariant=one_valid_lease") {
			t.Fatalf("authority deletion mutant survived: %v", err)
		}
	})
	staleSentinel := func(counter *atomic.Int64) func(context.Context) error {
		return func(context.Context) error {
			counter.Add(1)
			return &execution.DiagnosticError{
				Kind: execution.KindStaleAuthority, Message: "sentinel stale refusal", RetrySafety: execution.RetryDecisionRequired,
				RecoveryActions: []string{"inspect sentinel authority"},
			}
		}
	}
	t.Run("assert-phase1-universal-wiring", func(t *testing.T) {
		var invoked atomic.Int64
		assertPhase1Universal(t, context.Background(), fixture, "MUTANT-ASSERT-WIRING", expected, []string{candidate}, staleSentinel(&invoked), true)
		if invoked.Load() != 1 {
			t.Fatalf("assertPhase1Universal stale sentinel invoked=%d want=1", invoked.Load())
		}
	})
	t.Run("phase1-universal-state-wiring", func(t *testing.T) {
		var invoked atomic.Int64
		state := phase1UniversalState(t, fixture, "MUTANT-STATE-WIRING", fixture.primary.Binding().Primary, expected, []string{candidate})
		state.StaleProbe, state.RequireStaleProbe = staleSentinel(&invoked), true
		if err := assertUniversalInvariants(context.Background(), state); err != nil {
			t.Fatal(err)
		}
		if invoked.Load() != 1 {
			t.Fatalf("phase1UniversalState stale sentinel invoked=%d want=1", invoked.Load())
		}
	})
}

func TestPhase1TwoAgentTortureChamber(t *testing.T) {
	fixture := newWorkspaceScenario(t, []workspaceItem{
		{work: "work-phase1-a", lane: "lane-phase1-a", tracker: "TK-PHASE1-A"},
		{work: "work-phase1-b", lane: "lane-phase1-b", tracker: "TK-PHASE1-B"},
	})
	for _, boundary := range []string{"SR-00", "WORK", "EDIT"} {
		if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("block\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	aInput := workspaceStart{operation: "operation-phase1-a1", work: "work-phase1-a", lane: "lane-phase1-a", actor: "actor-phase1-a", process: "process-phase1-a1", workspace: "workspace-phase1-a1", branch: "runs/phase1-a1", trackerWork: "TK-PHASE1-A", editFile: "agent-a-old.txt", editContent: "old generation\n"}
	bInput := workspaceStart{operation: "operation-phase1-b", work: "work-phase1-b", lane: "lane-phase1-b", actor: "actor-phase1-b", process: "process-phase1-b", workspace: "workspace-phase1-b", branch: "runs/phase1-b", trackerWork: "TK-PHASE1-B", editFile: "agent-b.txt", editContent: "agent b\n"}
	aProcess, bProcess := fixture.start(ctx, t, aInput), fixture.start(ctx, t, bInput)
	waitAndRelease(t, ctx, fixture.barrier, "SR-00", 2)
	waitAndRelease(t, ctx, fixture.barrier, "WORK", 2)
	waitAndRelease(t, ctx, fixture.barrier, "EDIT", 2)
	aResponse, bResponse := waitWorkspaceProcess(t, aProcess), waitWorkspaceProcess(t, bProcess)
	if !aResponse.OK || aResponse.Capsule == nil || aResponse.Work == nil || !bResponse.OK || bResponse.Capsule == nil || bResponse.Work == nil {
		t.Fatalf("scenario=phase1-two-agent fault=concurrent-start a=%+v b=%+v recovery=inspect both operation IDs", aResponse, bResponse)
	}
	expectedPrimary := fixture.primary.Binding().Primary
	expectedState := newPhase1ExpectedState()
	expectedState.addCapsule(aResponse.Capsule, true)
	expectedState.addCapsule(bResponse.Capsule, true)
	state := phase1UniversalState(t, fixture, "CONCURRENT-CLAIM", expectedPrimary, expectedState, []string{aResponse.Work.Commit, bResponse.Work.Commit})
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if aResponse.Capsule.RunID == bResponse.Capsule.RunID || aResponse.Capsule.Token == bResponse.Capsule.Token ||
		aResponse.Capsule.WorkspaceID == bResponse.Capsule.WorkspaceID || aResponse.Capsule.WorkspacePath == bResponse.Capsule.WorkspacePath ||
		aResponse.Capsule.OwnedBranch == bResponse.Capsule.OwnedBranch || aResponse.Capsule.GitDir == bResponse.Capsule.GitDir ||
		aResponse.Capsule.IndexPath == bResponse.Capsule.IndexPath || aResponse.Capsule.LeaseID == bResponse.Capsule.LeaseID ||
		aResponse.Work.Commit == bResponse.Work.Commit {
		t.Fatalf("scenario=phase1-two-agent fault=concurrent-start overlapping_authority a=%+v b=%+v recovery=quarantine both workspaces", aResponse.Capsule, bResponse.Capsule)
	}
	if aResponse.Work.StartedAt.IsZero() || aResponse.Work.EndedAt.IsZero() || bResponse.Work.StartedAt.IsZero() || bResponse.Work.EndedAt.IsZero() ||
		!aResponse.Work.StartedAt.Before(bResponse.Work.EndedAt) || !bResponse.Work.StartedAt.Before(aResponse.Work.EndedAt) {
		t.Fatalf("scenario=phase1-two-agent fault=concurrent-start non_overlapping_intervals a=%+v b=%+v", aResponse.Work, bResponse.Work)
	}
	oldHandoffID := "handoff-phase1-a-old"
	if handoff := runHandoffHelper(t, fixture, aResponse.Capsule, aResponse.Work.Commit, oldHandoffID, "operation-handoff-phase1-a-old", ""); !handoff.OK {
		t.Fatalf("scenario=phase1-two-agent fault=PRE-EXPIRY-HANDOFF response=%+v", handoff)
	}

	if err := os.WriteFile(filepath.Join(fixture.barrier, "ZOMBIE.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zombie := startZombieProcess(t, ctx, fixture, *aResponse.Capsule)
	t.Cleanup(func() {
		if zombie.command.ProcessState == nil {
			_ = zombie.command.Process.Signal(syscall.SIGCONT)
			_ = zombie.command.Process.Kill()
		}
	})
	if err := waitForBoundaryReached(ctx, fixture.barrier, "ZOMBIE", 1); err != nil {
		t.Fatal(err)
	}
	if err := zombie.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	expireAuthorityProcess(t, ctx, fixture.database, *aResponse.Capsule, aInput, aResponse.Capsule.Deadline)
	expectedState.addCapsule(aResponse.Capsule, false)
	stalePublicationProbe := actualStalePublicationProbe(t, fixture, *aResponse.Capsule, aResponse.Work.Commit, oldHandoffID, "phase1-a-old")
	state = phase1UniversalState(t, fixture, "LEASE-EXPIRED", expectedPrimary, expectedState, []string{aResponse.Work.Commit, bResponse.Work.Commit})
	state.StaleProbe, state.RequireStaleProbe = stalePublicationProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	state = phase1UniversalState(t, fixture, "STALE-PUBLICATION", expectedPrimary, expectedState, []string{aResponse.Work.Commit, bResponse.Work.Commit})
	state.StaleProbe, state.RequireStaleProbe = stalePublicationProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	staleRecovery := recoverWorkspaceOperation(t, ctx, fixture, aInput.operation)
	state = phase1UniversalState(t, fixture, "STALE-RECOVERY", expectedPrimary, expectedState, []string{aResponse.Work.Commit, bResponse.Work.Commit})
	state.StaleProbe, state.RequireStaleProbe = stalePublicationProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if !staleRecovery.OK || staleRecovery.Decision == nil || staleRecovery.Decision.Disposition != execution.RecoveryExpired || staleRecovery.Decision.Diagnostic == nil || staleRecovery.Decision.Diagnostic.Kind != execution.KindStaleAuthority {
		t.Fatalf("scenario=phase1-two-agent fault=STALE-RECOVERY response=%+v", staleRecovery)
	}

	trackerStore, err := fake.Open(fixture.tracker)
	if err != nil {
		t.Fatal(err)
	}
	a2Input := workspaceStart{operation: "operation-phase1-a2", work: "work-phase1-a", lane: "lane-phase1-a", actor: "actor-phase1-a2", process: "process-phase1-a2", workspace: "workspace-phase1-a2", branch: "runs/phase1-a2", trackerWork: "TK-PHASE1-A", trackerRevision: 2, at: "2026-08-24T12:01:00Z", editFile: "agent-a.txt", editContent: "replacement generation\n"}
	if err := os.WriteFile(filepath.Join(fixture.barrier, "SR-02.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a2Process := fixture.start(ctx, t, a2Input)
	if err := waitForBoundaryReached(ctx, fixture.barrier, "SR-02", 1); err != nil {
		t.Fatal(err)
	}
	a2PreFault := loadPhase1RecoveryRecord(t, fixture.database, ids.OperationID(a2Input.operation))
	expectedState.addRecoveryAuthority(a2PreFault)
	if err := trackerStore.Inject(ctx, fake.FailureUnavailable); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.barrier, "SR-02.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outage := waitWorkspaceProcess(t, a2Process)
	state = phase1UniversalState(t, fixture, "TRACKER-OUTAGE", expectedPrimary, expectedState, []string{aResponse.Work.Commit, bResponse.Work.Commit})
	state.StaleProbe, state.RequireStaleProbe = stalePublicationProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if outage.OK || outage.Error == nil || outage.Error.Kind != execution.KindTrackerUnavailable || outage.Error.RetrySafety != execution.RetrySafeSameOperation {
		t.Fatalf("scenario=phase1-two-agent fault=TRACKER-OUTAGE response=%+v recovery=retry the identical operation after provider recovery", outage)
	}
	if _, err := os.Stat(filepath.Join(fixture.managedRoot, a2Input.workspace)); !os.IsNotExist(err) {
		t.Fatalf("scenario=phase1-two-agent fault=TRACKER-OUTAGE phantom_workspace=%s stat=%v", a2Input.workspace, err)
	}
	if assigned, err := fake.NewProvider(trackerStore).Read(ctx, a2Input.trackerWork); err != nil || assigned.Revision != 2 || assigned.Assignee != aInput.actor || assigned.AssignmentMarker != aInput.operation {
		t.Fatalf("scenario=phase1-two-agent fault=TRACKER-OUTAGE assignment=%+v err=%v recovery=preserve the prior attributable assignment", assigned, err)
	}
	a2Response := waitWorkspaceProcess(t, fixture.start(ctx, t, a2Input))
	if !a2Response.OK || a2Response.Capsule == nil || a2Response.Work == nil || a2Response.Capsule.Generation != aResponse.Capsule.Generation+1 {
		t.Fatalf("scenario=phase1-two-agent fault=TRACKER-RECOVERY response=%+v recovery=inspect the exact start operation", a2Response)
	}
	expectedState.addCapsule(a2Response.Capsule, true)
	w2Before := scenarioGit(t, a2Response.Capsule.WorkspacePath, "rev-parse", "HEAD")
	if err := zombie.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.barrier, "ZOMBIE.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zombieResponse := waitZombieProcess(t, zombie)
	state = phase1UniversalState(t, fixture, "ZOMBIE-RESUME-PRECHECK", expectedPrimary, expectedState, []string{aResponse.Work.Commit, a2Response.Work.Commit, bResponse.Work.Commit})
	state.StaleProbe, state.RequireStaleProbe = stalePublicationProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if !zombieResponse.OK || zombieResponse.Result == nil || zombieResponse.Result.HeartbeatRefusal != execution.KindStaleAuthority || zombieResponse.Result.ExpireRefusal != execution.KindStaleAuthority || zombieResponse.Result.WorkspaceRefusal != workspace.KindStaleAuthority {
		t.Fatalf("scenario=phase1-two-agent fault=ZOMBIE-RESUME result=%+v recovery=quarantine the expired workspace", zombieResponse)
	}
	if got := scenarioGit(t, a2Response.Capsule.WorkspacePath, "rev-parse", "HEAD"); got != w2Before {
		t.Fatalf("scenario=phase1-two-agent fault=ZOMBIE-RESUME replacement_before=%s replacement_after=%s zombie_commit=%s recovery=quarantine both workspaces", w2Before, got, zombieResponse.Result.Commit)
	}
	state = phase1UniversalState(t, fixture, "ZOMBIE-RESUME", expectedPrimary, expectedState, []string{aResponse.Work.Commit, a2Response.Work.Commit, bResponse.Work.Commit, zombieResponse.Result.Commit})
	state.StaleProbe, state.RequireStaleProbe = stalePublicationProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	inspection := runWorkspaceMaintenanceHelper(t, ctx, fixture, "workspace-inspect", aInput, aResponse.Capsule.WorkspaceID.String(), fixture.managedRoot)
	if inspection.OK || inspection.Error == nil || inspection.Error.Kind != execution.KindCleanupRefused || inspection.Inspection == nil || inspection.Inspection.ObservedHead != zombieResponse.Result.Commit || !containsCommit(inspection.Inspection.PreservedCommits, zombieResponse.Result.Commit) {
		t.Fatalf("scenario=phase1-two-agent fault=QUARANTINED-INSPECTION response=%+v recovery=preserve the exact zombie commit", inspection)
	}

	if err := trackerStore.HumanAssign(ctx, "TK-PHASE1-B", "human-owner"); err != nil {
		t.Fatal(err)
	}
	compensation := runPhase1Compensation(t, fixture, bInput, 2)
	attributionProbe := func(ctx context.Context) error {
		work, err := fake.NewProvider(trackerStore).Read(ctx, "TK-PHASE1-B")
		if err != nil {
			return err
		}
		if work.Assignee != "human-owner" || work.AssignmentMarker != "" {
			return fmt.Errorf("work=%s revision=%d assignee=%q marker=%q", work.Key, work.Revision, work.Assignee, work.AssignmentMarker)
		}
		return nil
	}
	state = phase1UniversalState(t, fixture, "ATTRIBUTION-CHANGE", expectedPrimary, expectedState, []string{a2Response.Work.Commit, bResponse.Work.Commit, zombieResponse.Result.Commit})
	state.StaleProbe, state.RequireStaleProbe, state.AttributionProbe = stalePublicationProbe, true, attributionProbe
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if compensation.OK || compensation.Error == nil || compensation.Error.Kind != execution.KindExternalAttributionLost || compensation.Error.RetrySafety != execution.RetryDecisionRequired {
		t.Fatalf("scenario=phase1-two-agent fault=ATTRIBUTION-CHANGE response=%+v recovery=preserve the human assignment", compensation)
	}

	handoffA, handoffB := "handoff-phase1-a", "handoff-phase1-b"
	if response := runHandoffHelper(t, fixture, a2Response.Capsule, a2Response.Work.Commit, handoffA, "operation-handoff-phase1-a", ""); !response.OK {
		t.Fatalf("scenario=phase1-two-agent fault=HANDOFF-A response=%+v", response)
	}
	if response := runHandoffHelper(t, fixture, bResponse.Capsule, bResponse.Work.Commit, handoffB, "operation-handoff-phase1-b", ""); !response.OK {
		t.Fatalf("scenario=phase1-two-agent fault=HANDOFF-B response=%+v", response)
	}
	selfReview := runReviewHelper(t, fixture, a2Response.Capsule, a2Response.Work.Commit, handoffA, "review-phase1-self", "operation-review-phase1-self", a2Response.Capsule.AggregateIDs["actor_id"], "process-phase1-self", review.Approved, "")
	state = phase1UniversalState(t, fixture, "SELF-REVIEW", expectedPrimary, expectedState, []string{a2Response.Work.Commit, bResponse.Work.Commit, zombieResponse.Result.Commit})
	state.StaleProbe, state.RequireStaleProbe, state.AttributionProbe = stalePublicationProbe, true, attributionProbe
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if selfReview.OK || selfReview.Error == nil || selfReview.Error.Kind != execution.ErrorKind(review.KindReviewerNotIndependent) {
		t.Fatalf("scenario=phase1-two-agent fault=SELF-REVIEW response=%+v recovery=use a distinct reviewer ActorID", selfReview)
	}
	reviewA := runReviewHelper(t, fixture, a2Response.Capsule, a2Response.Work.Commit, handoffA, "review-phase1-a", "operation-review-phase1-a", "actor-phase1-reviewer-a", "process-phase1-reviewer-a", review.Approved, "")
	reviewB := runReviewHelper(t, fixture, bResponse.Capsule, bResponse.Work.Commit, handoffB, "review-phase1-b", "operation-review-phase1-b", "actor-phase1-reviewer-b", "process-phase1-reviewer-b", review.Approved, "")
	state = phase1UniversalState(t, fixture, "INDEPENDENT-REVIEW", expectedPrimary, expectedState, []string{a2Response.Work.Commit, bResponse.Work.Commit, zombieResponse.Result.Commit})
	state.StaleProbe, state.RequireStaleProbe, state.AttributionProbe = stalePublicationProbe, true, attributionProbe
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	for label, checked := range map[string]struct {
		response  helpercmd.Response
		actor     ids.ActorID
		process   ids.ProcessID
		candidate string
		work      ids.WorkID
	}{
		"A": {reviewA, "actor-phase1-reviewer-a", "process-phase1-reviewer-a", a2Response.Work.Commit, a2Response.Capsule.WorkID},
		"B": {reviewB, "actor-phase1-reviewer-b", "process-phase1-reviewer-b", bResponse.Work.Commit, bResponse.Capsule.WorkID},
	} {
		record := checked.response.Judgment
		if !checked.response.OK || record == nil || record.Review == nil || record.Review.ReviewerActorID != checked.actor || record.Review.ReviewerProcessID != checked.process || record.Review.Candidate != checked.candidate || record.Review.WorkRevisionID != "revision-1" || record.Review.WorkID != checked.work || record.Review.Verdict != review.Approved || len(record.Review.Criteria) != 1 || record.Review.Criteria[0].Status != review.Passed || record.Review.EvidenceDigest == "" {
			t.Fatalf("scenario=phase1-two-agent fault=REVIEW-%s response=%+v", label, checked.response)
		}
	}

	if err := os.WriteFile(filepath.Join(fixture.repository, "manual.txt"), []byte("manual branch movement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, fixture.repository, "add", "manual.txt")
	scenarioGit(t, fixture.repository, "commit", "-m", "manual target movement")
	manualTarget := scenarioGit(t, fixture.repository, "rev-parse", "HEAD")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "HEAD")
	expectedPrimary, err = fixture.primary.SnapshotPrimary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state = phase1UniversalState(t, fixture, "MANUAL-TARGET-MOVEMENT", expectedPrimary, expectedState, []string{a2Response.Work.Commit, bResponse.Work.Commit, zombieResponse.Result.Commit, manualTarget})
	state.StaleProbe, state.RequireStaleProbe, state.AttributionProbe = stalePublicationProbe, true, attributionProbe
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-01.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	argsA := integrationArgs(fixture, fixture.database, "operation-integrate-phase1-a", "review-phase1-a", a2Response.Work.Commit, "actor-phase1-integrator-a", "process-phase1-integrator-a", time.Now().UTC())
	argsB := integrationArgs(fixture, fixture.database, "operation-integrate-phase1-b", "review-phase1-b", bResponse.Work.Commit, "actor-phase1-integrator-b", "process-phase1-integrator-b", time.Now().UTC())
	integrationA := startIntegrationProcess(t, ctx, argsA)
	if err := waitForBoundaryReached(ctx, fixture.barrier, "IG-01", 1); err != nil {
		t.Fatal(err)
	}
	blockedB := runExternalIntegration(t, argsB)
	state = phase1UniversalState(t, fixture, "SIMULTANEOUS-INTEGRATION", expectedPrimary, expectedState, []string{a2Response.Work.Commit, bResponse.Work.Commit, zombieResponse.Result.Commit, manualTarget})
	state.StaleProbe, state.RequireStaleProbe, state.AttributionProbe = stalePublicationProbe, true, attributionProbe
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if blockedB.OK || blockedB.Error == nil || blockedB.Error.Kind != execution.ErrorKind(integration.KindAuthorityBusy) {
		t.Fatalf("scenario=phase1-two-agent fault=SIMULTANEOUS-INTEGRATION response=%+v recovery=retry after target authority releases", blockedB)
	}
	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-01.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	completedA, err := integrationA.wait()
	if err != nil || !completedA.OK || completedA.Integration == nil || completedA.Integration.Record.PreflightTarget != manualTarget {
		t.Fatalf("scenario=phase1-two-agent fault=INTEGRATION-A response=%+v error=%v manual_target=%s", completedA, err, manualTarget)
	}
	if err := os.WriteFile(filepath.Join(fixture.barrier, "IG-01.action"), []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	completedB := runExternalIntegration(t, argsB)
	if !completedB.OK || completedB.Integration == nil || completedB.Integration.Record.PreflightTarget != completedA.Integration.Record.ResultCommit {
		t.Fatalf("scenario=phase1-two-agent fault=INTEGRATION-B response=%+v first=%+v", completedB, completedA)
	}
	finalTarget := completedB.Integration.Record.ObservedTarget
	for label, commit := range map[string]string{"manual": manualTarget, "candidate_a": a2Response.Work.Commit, "candidate_b": bResponse.Work.Commit} {
		if mergeBase := scenarioGit(t, fixture.repository, "merge-base", commit, finalTarget); mergeBase != commit {
			t.Fatalf("scenario=phase1-two-agent fault=FINAL-REACHABILITY label=%s commit=%s final=%s merge_base=%s", label, commit, finalTarget, mergeBase)
		}
	}
	state = phase1UniversalState(t, fixture, "SERIAL-INTEGRATION-COMPLETE", expectedPrimary, expectedState, []string{a2Response.Work.Commit, bResponse.Work.Commit, zombieResponse.Result.Commit, manualTarget, finalTarget})
	state.StaleProbe, state.RequireStaleProbe, state.AttributionProbe = stalePublicationProbe, true, attributionProbe
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
}

func TestPhase1OwnedBranchMovementRefusesHandoff(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "phase1-owned-branch-movement")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	expectedPrimary := fixture.primary.Binding().Primary

	// Move the owned branch without manufacturing a fixture evidence ref. The
	// product must preserve both facts before it persists the refusal.
	scenarioGit(t, capsule.WorkspacePath, "reset", "--hard", fixture.base)
	expectedState := newPhase1ExpectedState()
	expectedState.addCapsule(capsule, true)
	state := phase1UniversalState(t, fixture, "OWNED-BRANCH-MOVEMENT", expectedPrimary, expectedState, nil)
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if observed := scenarioGit(t, fixture.repository, "rev-parse", capsule.OwnedBranch); observed != fixture.base {
		t.Fatalf("scenario=phase1-owned-branch-movement fault=BRANCH-MOVEMENT observed=%s expected=%s", observed, fixture.base)
	}

	const operation = "operation-handoff-branch-moved"
	response := runHandoffHelper(t, fixture, capsule, candidate, "handoff-branch-moved", operation, "")
	state = phase1UniversalState(t, fixture, "OWNED-BRANCH-REFUSAL", expectedPrimary, expectedState, []string{candidate, fixture.base})
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	assertMovedBranchRefusal(t, response, capsule, candidate, fixture.base, operation)
	root := response.Error.ObservedState["evidence_ref_root"]
	refsBefore := strings.Fields(scenarioGit(t, fixture.repository, "for-each-ref", "--format=%(refname)", root+"/"))
	if len(refsBefore) != 2 || scenarioGit(t, fixture.repository, "rev-parse", root+"/candidate^{commit}") != candidate || scenarioGit(t, fixture.repository, "rev-parse", root+"/observed^{commit}") != fixture.base {
		t.Fatalf("scenario=phase1-owned-branch-movement product_refs=%v response=%+v", refsBefore, response)
	}

	// Changed live branch and expired implementation authority cannot turn the
	// same OperationID into success. Deleting one exact product ref is repaired
	// transactionally from the immutable refusal while its object remains.
	scenarioGit(t, capsule.WorkspacePath, "reset", "--hard", candidate)
	scenarioGit(t, fixture.repository, "update-ref", "-d", root+"/candidate")
	expiryStore, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	expiry := execution.ExpireRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-expire-phase1-owned-branch", OperationID: capsule.OperationID,
		ActorID: ids.ActorID(capsule.AggregateIDs["actor_id"]), ProcessID: ids.ProcessID(capsule.AggregateIDs["process_id"]),
		LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token, Now: capsule.Deadline,
	}
	if err := execution.NewService(expiryStore, execution.RandomIdentities{}, execution.SystemClock{}).Expire(ctx, expiry); err != nil {
		_ = expiryStore.Close()
		t.Fatal(err)
	}
	if err := expiryStore.Close(); err != nil {
		t.Fatal(err)
	}
	expectedState.addCapsule(capsule, false)
	replay := runHandoffHelper(t, fixture, capsule, candidate, "handoff-branch-moved", operation, "")
	state = phase1UniversalState(t, fixture, "OWNED-BRANCH-REFUSAL-REPLAY", expectedPrimary, expectedState, []string{candidate, fixture.base})
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	assertMovedBranchRefusal(t, replay, capsule, candidate, fixture.base, operation)
	if !reflect.DeepEqual(response.Error.ObservedState, replay.Error.ObservedState) || !reflect.DeepEqual(response.Error.ExpectedState, replay.Error.ExpectedState) || !reflect.DeepEqual(response.Error.RecoveryActions, replay.Error.RecoveryActions) {
		t.Fatalf("scenario=phase1-owned-branch-movement refusal_drift first=%+v replay=%+v", response.Error, replay.Error)
	}
	refsAfter := strings.Fields(scenarioGit(t, fixture.repository, "for-each-ref", "--format=%(refname)", root+"/"))
	if !reflect.DeepEqual(refsBefore, refsAfter) || scenarioGit(t, fixture.repository, "rev-parse", root+"/candidate^{commit}") != candidate {
		t.Fatalf("scenario=phase1-owned-branch-movement repaired_refs before=%v after=%v", refsBefore, refsAfter)
	}

	db, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for label, query := range map[string]string{
		"accepted_handoff": `SELECT COUNT(*) FROM handoffs WHERE handoff_id='handoff-branch-moved'`,
		"refusal":          `SELECT COUNT(*) FROM handoff_refusals WHERE operation_id='operation-handoff-branch-moved'`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 0
		if label == "refusal" {
			want = 1
		}
		if count != want {
			t.Fatalf("scenario=phase1-owned-branch-movement %s=%d want=%d", label, count, want)
		}
	}

	// A product evidence ref that points elsewhere is external state. Replay
	// refuses it and never overwrites the mutation.
	scenarioGit(t, fixture.repository, "update-ref", root+"/observed", candidate)
	conflict := runHandoffHelper(t, fixture, capsule, candidate, "handoff-branch-moved", operation, "")
	state = phase1UniversalState(t, fixture, "OWNED-BRANCH-EVIDENCE-MUTATED", expectedPrimary, expectedState, []string{candidate, fixture.base})
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if conflict.OK || conflict.Error == nil || conflict.Error.Kind != execution.ErrorKind(handoff.KindCandidateMismatch) || conflict.Error.RetrySafety != execution.RetryDecisionRequired || conflict.Error.ObservedState["preservation_kind"] != handoff.EvidenceRefMutated || conflict.Error.ObservedState["evidence_observed_ref_head"] != candidate || conflict.Error.ExpectedState["evidence_observed_ref_head"] != fixture.base || len(conflict.Error.RecoveryActions) < 2 {
		t.Fatalf("scenario=phase1-owned-branch-movement mutation_conflict=%+v", conflict)
	}
	if got := scenarioGit(t, fixture.repository, "rev-parse", root+"/observed^{commit}"); got != candidate {
		t.Fatalf("scenario=phase1-owned-branch-movement mutated_ref_overwritten=%s", got)
	}
}

func TestPhase1ApprovedBeforeExpiryRemainsIntegrable(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "phase1-approved-before-expiry")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const handoffID = "handoff-phase1-approved-before-expiry"
	const reviewID = "review-phase1-approved-before-expiry"

	handoffResponse := runHandoffHelper(t, fixture, capsule, candidate, handoffID, "operation-handoff-phase1-approved-before-expiry", "")
	if !handoffResponse.OK || handoffResponse.Judgment == nil || handoffResponse.Judgment.Handoff == nil {
		t.Fatalf("scenario=phase1-approved-before-expiry handoff=%+v", handoffResponse)
	}
	reviewResponse := runReviewHelper(t, fixture, capsule, candidate, handoffID, reviewID, "operation-review-phase1-approved-before-expiry", "actor-phase1-approved-reviewer", "process-phase1-approved-reviewer", review.Approved, "")
	if !reviewResponse.OK || reviewResponse.Judgment == nil || reviewResponse.Judgment.Review == nil || reviewResponse.Judgment.Review.Verdict != review.Approved {
		t.Fatalf("scenario=phase1-approved-before-expiry review=%+v", reviewResponse)
	}

	expiryStore, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	expiry := execution.ExpireRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-expire-phase1-approved", OperationID: capsule.OperationID,
		ActorID: ids.ActorID(capsule.AggregateIDs["actor_id"]), ProcessID: ids.ProcessID(capsule.AggregateIDs["process_id"]),
		LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token, Now: capsule.Deadline,
	}
	if err := execution.NewService(expiryStore, execution.RandomIdentities{}, execution.SystemClock{}).Expire(ctx, expiry); err != nil {
		_ = expiryStore.Close()
		t.Fatal(err)
	}
	if err := expiryStore.Close(); err != nil {
		t.Fatal(err)
	}

	scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
	expectedPrimary, err := fixture.primary.SnapshotPrimary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectedState := newPhase1ExpectedState()
	expectedState.addCapsule(capsule, false)
	staleProbe := actualStalePublicationProbe(t, fixture, *capsule, candidate, handoffID, "approved-before-expiry")
	state := phase1UniversalState(t, fixture, "APPROVED-SOURCE-LEASE-EXPIRED", expectedPrimary, expectedState, []string{candidate})
	state.StaleProbe, state.RequireStaleProbe = staleProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}

	providerStore, err := fake.Open(fixture.tracker)
	if err != nil {
		t.Fatal(err)
	}
	const trackerKey = "TK-phase1-approved-before-expiry"
	beforeTracker, err := fake.NewProvider(providerStore).Read(ctx, trackerKey)
	if err != nil {
		t.Fatal(err)
	}
	response := runExternalIntegration(t, integrationArgs(fixture, fixture.database, "operation-integrate-phase1-approved-before-expiry", reviewID, candidate, "actor-phase1-approved-integrator", "process-phase1-approved-integrator", time.Now().UTC()))
	state = phase1UniversalState(t, fixture, "APPROVED-AFTER-SOURCE-LEASE-EXPIRY", expectedPrimary, expectedState, []string{candidate})
	state.StaleProbe, state.RequireStaleProbe = staleProbe, true
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Integration == nil || response.Integration.Record.Status != integration.StatusDone || response.Integration.Record.ReviewID != ids.ReviewID(reviewID) || response.Integration.Record.Candidate != candidate {
		t.Fatalf("scenario=phase1-approved-before-expiry integration=%+v", response)
	}
	afterTracker, err := fake.NewProvider(providerStore).Read(ctx, trackerKey)
	if err != nil {
		t.Fatal(err)
	}
	if beforeTracker.Revision != afterTracker.Revision || beforeTracker.Assignee != afterTracker.Assignee {
		t.Fatalf("scenario=phase1-approved-before-expiry tracker became integration authority before=%+v after=%+v", beforeTracker, afterTracker)
	}

	db, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sourceLeaseStatus string
	var integrated int
	if err := db.QueryRowContext(ctx, `SELECT status FROM leases WHERE lease_id=?`, capsule.LeaseID).Scan(&sourceLeaseStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM integrations WHERE review_id=? AND status='done'`, reviewID).Scan(&integrated); err != nil {
		t.Fatal(err)
	}
	if sourceLeaseStatus != "expired" || integrated != 1 {
		t.Fatalf("scenario=phase1-approved-before-expiry source_lease=%s integrations=%d", sourceLeaseStatus, integrated)
	}
}

func testPhase1CrashExpiryReplacement(t *testing.T, boundary string) {
	t.Helper()
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-expiry", lane: "lane-phase1-expiry", tracker: "TK-PHASE1-EXPIRY"}})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	input := workspaceStart{operation: "operation-phase1-expiry-old-" + boundary, work: "work-phase1-expiry", lane: "lane-phase1-expiry", actor: "actor-phase1-expiry-old", process: "process-phase1-expiry-old", workspace: "workspace-phase1-expiry-old-" + boundary, branch: "runs/phase1-expiry-old-" + boundary, trackerWork: "TK-PHASE1-EXPIRY"}
	crashWorkspaceAt(t, ctx, fixture, boundary, input)
	record := loadPhase1RecoveryRecord(t, fixture.database, ids.OperationID(input.operation))
	preExpiryExpected := newPhase1ExpectedState()
	preExpiryExpected.addRecoveryAuthority(record)
	if record.WorkspaceStage != "" {
		preExpiryExpected.addRecoveryWorkspace(fixture, record)
	}
	assertPhase1Universal(t, ctx, fixture, boundary+"-PRE-EXPIRY", preExpiryExpected, nil, nil, false)
	assertPhase1StartBoundaryFact(t, ctx, fixture, input, boundary)

	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	record, err = control.LoadStartRun(ctx, ids.OperationID(input.operation))
	if err != nil {
		_ = control.Close()
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	expired := recoverWorkspaceOperationAt(t, ctx, fixture, input.operation, record.Deadline)
	expiredExpected := preExpiryExpected.clone()
	delete(expiredExpected.authority, leaseFactKey(record.Request.Claim.WorkRevisionID, record.Request.Claim.WorkID))
	assertPhase1Universal(t, ctx, fixture, boundary+"-EXPIRED", expiredExpected, nil, nil, false)
	if !expired.OK || expired.Decision == nil || expired.Decision.Disposition != execution.RecoveryExpired || expired.Decision.Diagnostic == nil || expired.Decision.Diagnostic.Kind != execution.KindStaleAuthority {
		t.Fatalf("scenario=phase1-expiry fault=%s recovery=%+v", boundary, expired)
	}

	providerStore, err := fake.Open(fixture.tracker)
	if err != nil {
		t.Fatal(err)
	}
	providerWork, err := fake.NewProvider(providerStore).Read(ctx, input.trackerWork)
	if err != nil {
		t.Fatal(err)
	}
	replacementInput := workspaceStart{operation: "operation-phase1-expiry-new-" + boundary, work: input.work, lane: input.lane, actor: "actor-phase1-expiry-new", process: "process-phase1-expiry-new", workspace: "workspace-phase1-expiry-new-" + boundary, branch: "runs/phase1-expiry-new-" + boundary, trackerWork: input.trackerWork, trackerRevision: int64(providerWork.Revision), at: record.Deadline.Add(time.Second).Format(time.RFC3339Nano)}
	replacement := waitWorkspaceProcess(t, fixture.start(ctx, t, replacementInput))
	if replacement.Capsule == nil {
		t.Fatalf("scenario=phase1-expiry fault=%s replacement omitted capsule: %+v", boundary, replacement)
	}
	finalExpected := expiredExpected.clone()
	finalExpected.addCapsule(replacement.Capsule, true)
	assertPhase1Universal(t, ctx, fixture, boundary+"-REPLACED", finalExpected, nil, nil, false)
	if !replacement.OK || replacement.Capsule == nil || replacement.Capsule.WorkspaceID != ids.WorkspaceID(replacementInput.workspace) || replacement.Capsule.RunID == record.RunID || replacement.Capsule.LeaseID == record.LeaseID || replacement.Capsule.Generation != record.Generation+1 {
		t.Fatalf("scenario=phase1-expiry fault=%s replacement=%+v old=%+v", boundary, replacement, record)
	}
	if replacement.Capsule.WorkspacePath == filepath.Join(fixture.managedRoot, input.workspace) || replacement.Capsule.OwnedBranch == input.branch {
		t.Fatalf("scenario=phase1-expiry fault=%s replacement reused old workspace: %+v", boundary, replacement.Capsule)
	}
	if boundary == "SR-02" {
		if len(preExpiryExpected.workspaces) != 0 || len(finalExpected.workspaces) != 1 {
			t.Fatalf("scenario=phase1-expiry fault=%s phantom workspace pre=%v final=%v", boundary, preExpiryExpected.workspaces, finalExpected.workspaces)
		}
		return
	}
	oldWorkspace, ok := finalExpected.workspaces[ids.WorkspaceID(input.workspace)]
	if !ok || oldWorkspace.Path == replacement.Capsule.WorkspacePath || oldWorkspace.Branch == replacement.Capsule.OwnedBranch {
		t.Fatalf("scenario=phase1-expiry fault=%s old_binding=%+v replacement=%+v", boundary, oldWorkspace, replacement.Capsule)
	}
	control, err = sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	oldRecord, err := control.Workspace(ctx, ids.WorkspaceID(input.workspace))
	_ = control.Close()
	if err != nil || !workspace.IsQuarantined(oldRecord) {
		t.Fatalf("scenario=phase1-expiry fault=%s old_workspace=%+v err=%v", boundary, oldRecord, err)
	}
}

func testPhase1WorktreeMismatchDecision(t *testing.T) {
	t.Helper()
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-mismatch", lane: "lane-phase1-mismatch", tracker: "TK-PHASE1-MISMATCH"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := workspaceStart{operation: "operation-phase1-mismatch", work: "work-phase1-mismatch", lane: "lane-phase1-mismatch", actor: "actor-phase1-mismatch", process: "process-phase1-mismatch", workspace: "workspace-phase1-mismatch", branch: "runs/phase1-mismatch", trackerWork: "TK-PHASE1-MISMATCH"}
	crashWorkspaceAt(t, ctx, fixture, "SR-06", input)
	record := loadPhase1RecoveryRecord(t, fixture.database, ids.OperationID(input.operation))
	expected := newPhase1ExpectedState()
	expected.addRecoveryAuthority(record)
	expected.addRecoveryWorkspace(fixture, record)
	assertPhase1Universal(t, ctx, fixture, "SR-06-PRE-MISMATCH", expected, nil, nil, false)
	assertPhase1StartBoundaryFact(t, ctx, fixture, input, "SR-06")
	path := filepath.Join(fixture.managedRoot, input.workspace)
	scenarioGit(t, path, "checkout", "-b", "runs/phase1-mismatch-unexpected")
	decision := recoverWorkspaceOperationAt(t, ctx, fixture, input.operation, time.Date(2026, 8, 24, 12, 0, 30, 0, time.UTC))
	assertPhase1Universal(t, ctx, fixture, "SR-06-MISMATCH", expected, nil, nil, false)
	if !decision.OK || decision.Decision == nil || decision.Decision.Disposition != execution.RecoveryDecisionRequired || decision.Decision.Diagnostic == nil || decision.Decision.Diagnostic.Kind != execution.KindWorkspaceNotReady || decision.Decision.RetrySafety != execution.RetryDecisionRequired {
		t.Fatalf("scenario=phase1-worktree-mismatch decision=%+v", decision)
	}
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	workspaceRecord, err := control.Workspace(ctx, ids.WorkspaceID(input.workspace))
	_ = control.Close()
	if err != nil || workspaceRecord.Ready() || workspaceRecord.Status == "ready" {
		t.Fatalf("scenario=phase1-worktree-mismatch workspace=%+v err=%v", workspaceRecord, err)
	}
}

func testPhase1QuarantinedOutputInspection(t *testing.T) {
	t.Helper()
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-quarantine", lane: "lane-phase1-quarantine", tracker: "TK-PHASE1-QUARANTINE"}})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	oldInput := workspaceStart{operation: "operation-phase1-quarantine-old", work: "work-phase1-quarantine", lane: "lane-phase1-quarantine", actor: "actor-phase1-quarantine-old", process: "process-phase1-quarantine-old", workspace: "workspace-phase1-quarantine-old", branch: "runs/phase1-quarantine-old", trackerWork: "TK-PHASE1-QUARANTINE", editFile: "old.txt", editContent: "old\n"}
	oldResponse := waitWorkspaceProcess(t, fixture.start(ctx, t, oldInput))
	if !oldResponse.OK || oldResponse.Capsule == nil || oldResponse.Work == nil {
		t.Fatalf("old start=%+v", oldResponse)
	}
	oldHandoffID := "handoff-phase1-quarantine-old"
	if handoff := runHandoffHelper(t, fixture, oldResponse.Capsule, oldResponse.Work.Commit, oldHandoffID, "operation-handoff-phase1-quarantine-old", ""); !handoff.OK {
		t.Fatalf("old handoff=%+v", handoff)
	}
	expireAuthorityProcess(t, ctx, fixture.database, *oldResponse.Capsule, oldInput, oldResponse.Capsule.Deadline)
	newInput := workspaceStart{operation: "operation-phase1-quarantine-new", work: oldInput.work, lane: oldInput.lane, actor: "actor-phase1-quarantine-new", process: "process-phase1-quarantine-new", workspace: "workspace-phase1-quarantine-new", branch: "runs/phase1-quarantine-new", trackerWork: oldInput.trackerWork, trackerRevision: 2, at: oldResponse.Capsule.Deadline.Add(time.Second).Format(time.RFC3339Nano)}
	newResponse := waitWorkspaceProcess(t, fixture.start(ctx, t, newInput))
	if !newResponse.OK || newResponse.Capsule == nil {
		t.Fatalf("replacement=%+v", newResponse)
	}
	newHead := scenarioGit(t, newResponse.Capsule.WorkspacePath, "rev-parse", "HEAD")
	zombie := startZombieProcess(t, ctx, fixture, *oldResponse.Capsule)
	zombieResponse := waitZombieProcess(t, zombie)
	expected := newPhase1ExpectedState()
	expected.addCapsule(oldResponse.Capsule, false)
	expected.addCapsule(newResponse.Capsule, true)
	assertPhase1Universal(t, ctx, fixture, "QUARANTINED-OUTPUT", expected, []string{oldResponse.Work.Commit}, nil, false)
	if !zombieResponse.OK || zombieResponse.Result == nil || zombieResponse.Result.Commit == "" {
		t.Fatalf("zombie output=%+v", zombieResponse)
	}
	if got := scenarioGit(t, newResponse.Capsule.WorkspacePath, "rev-parse", "HEAD"); got != newHead {
		t.Fatalf("quarantined output mutated replacement: before=%s after=%s", newHead, got)
	}
	inspection := runWorkspaceMaintenanceHelper(t, ctx, fixture, "workspace-inspect", oldInput, oldResponse.Capsule.WorkspaceID.String(), fixture.managedRoot)
	cleanup := runWorkspaceMaintenanceHelper(t, ctx, fixture, "workspace-cleanup", oldInput, oldResponse.Capsule.WorkspaceID.String(), fixture.managedRoot)
	assertPhase1Universal(t, ctx, fixture, "QUARANTINED-OUTPUT-INSPECTED", expected, []string{oldResponse.Work.Commit, zombieResponse.Result.Commit}, actualStalePublicationProbe(t, fixture, *oldResponse.Capsule, oldResponse.Work.Commit, oldHandoffID, "quarantine"), true)
	if inspection.OK || inspection.Error == nil || inspection.Inspection == nil || inspection.Inspection.ObservedHead != zombieResponse.Result.Commit || !containsCommit(inspection.Inspection.PreservedCommits, zombieResponse.Result.Commit) || cleanup.OK || cleanup.Error == nil || cleanup.Error.Kind != execution.KindCleanupRefused {
		t.Fatalf("quarantined inspection=%+v cleanup=%+v", inspection, cleanup)
	}
}

func recoverWorkspaceOperationAt(t *testing.T, ctx context.Context, fixture workspaceScenario, operation string, at time.Time) helpercmd.RecoveryResponse {
	t.Helper()
	command := exec.CommandContext(ctx, helperBinary, "recover-start",
		"--db", fixture.database, "--tracker-store", fixture.tracker, "--repository", fixture.repository,
		"--workspace-root", fixture.managedRoot, "--barrier", fixture.barrier,
		"--operation", operation, "--project", "project-1", "--repository-id", "repository-1",
		"--correlation-id", "recover-"+operation, "--at", at.UTC().Format(time.RFC3339Nano))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("recover helper: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	var response helpercmd.RecoveryResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode recovery %q: %v", stdout.String(), err)
	}
	return response
}

func TestPhase1MergeConflictPreservesEvidence(t *testing.T) {
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-conflict", lane: "lane-phase1-conflict", tracker: "TK-PHASE1-CONFLICT"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response := waitWorkspaceProcess(t, fixture.start(ctx, t, workspaceStart{
		operation: "operation-start-phase1-conflict", work: "work-phase1-conflict", lane: "lane-phase1-conflict", actor: "actor-phase1-conflict", process: "process-phase1-conflict",
		workspace: "workspace-phase1-conflict", branch: "runs/phase1-conflict", trackerWork: "TK-PHASE1-CONFLICT", editFile: "base.txt", editContent: "candidate conflict\n",
	}))
	if !response.OK || response.Capsule == nil || response.Work == nil {
		t.Fatalf("scenario=phase1-conflict fault=START response=%+v", response)
	}
	approveMissionCandidate(t, fixture, response.Capsule, "mission-1", response.Work.Commit, "phase1-conflict")
	if err := os.WriteFile(filepath.Join(fixture.repository, "base.txt"), []byte("manual target conflict\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, fixture.repository, "add", "base.txt")
	scenarioGit(t, fixture.repository, "commit", "-m", "manual conflicting target")
	target := scenarioGit(t, fixture.repository, "rev-parse", "HEAD")
	scenarioGit(t, fixture.repository, "checkout", "--detach", "HEAD")
	expectedPrimary, err := fixture.primary.SnapshotPrimary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	operation := "operation-integrate-phase1-conflict"
	code, conflict := runIntegrationInProcess(t, integrationArgs(fixture, fixture.database, operation, "review-integration-phase1-conflict", response.Work.Commit, "actor-phase1-conflict-integrator", "process-phase1-conflict-integrator", time.Now().UTC()))
	expectedState := newPhase1ExpectedState()
	expectedState.addCapsule(response.Capsule, true)
	state := phase1UniversalState(t, fixture, "MERGE-CONFLICT-PRECHECK", expectedPrimary, expectedState, []string{response.Work.Commit, target})
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if code != 0 || conflict.OK || conflict.Error == nil || conflict.Error.Kind != execution.ErrorKind(integration.KindConflict) || conflict.Error.RetrySafety != execution.RetryDecisionRequired {
		t.Fatalf("scenario=phase1-conflict fault=MERGE-CONFLICT code=%d response=%+v recovery=preserve the detached integration workspace", code, conflict)
	}
	var status, repairKind, repairAction, workspacePath string
	var evidenceJSON []byte
	db, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status,repair_kind,repair_action,workspace_path,evidence_json FROM integrations WHERE operation_id=?`, operation).Scan(&status, &repairKind, &repairAction, &workspacePath, &evidenceJSON); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	var evidence integration.ConflictEvidence
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil || status != string(integration.StatusRepairRequired) || repairKind != string(integration.KindConflict) || repairAction == "" || evidence.Target != target || evidence.Candidate != response.Work.Commit || len(evidence.ConflictFiles) != 1 || evidence.ConflictFiles[0] != "base.txt" {
		t.Fatalf("scenario=phase1-conflict fault=MERGE-CONFLICT status=%s kind=%s action=%q workspace=%s evidence=%+v decode=%v", status, repairKind, repairAction, workspacePath, evidence, err)
	}
	if info, err := os.Stat(workspacePath); err != nil || !info.IsDir() {
		t.Fatalf("scenario=phase1-conflict fault=CONFLICT-EVIDENCE workspace=%s stat=%v", workspacePath, err)
	}
	if observed := scenarioGit(t, fixture.repository, "rev-parse", "refs/heads/main"); observed != target {
		t.Fatalf("scenario=phase1-conflict fault=TARGET-MOVED expected_target=%s observed_target=%s recovery=restore only through explicit integration reconciliation", target, observed)
	}
	adapter, err := gitrepo.Bind(ctx, gitrepo.Runner{}, "repository-1", "project-1", fixture.repository, fixture.managedRoot, "refs/heads/main", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	cleanupRecord := integration.Record{OperationID: ids.OperationID(operation), Candidate: response.Work.Commit, WorkspacePath: workspacePath, Status: integration.StatusRepairRequired}
	cleanupErr := adapter.CleanupIntegrationWorkspace(ctx, cleanupRecord)
	state = phase1UniversalState(t, fixture, "MERGE-CONFLICT-CLEANUP", expectedPrimary, expectedState, []string{response.Work.Commit, target})
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
	if !integration.IsKind(cleanupErr, integration.KindRepairRequired) {
		t.Fatalf("scenario=phase1-conflict fault=CLEANUP-REFUSAL operation=%s workspace=%s error=%v recovery=retain the repair-required workspace", operation, workspacePath, cleanupErr)
	}
	state = phase1UniversalState(t, fixture, "MERGE-CONFLICT", expectedPrimary, expectedState, []string{response.Work.Commit, target})
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
}

func testPhase1StartRunFaults(t *testing.T) {
	for index, boundary := range phase1StartFaults {
		boundary := boundary
		t.Run(boundary, func(t *testing.T) {
			fixture := newWorkspaceScenario(t, []workspaceItem{
				{work: "work-phase1-start", lane: "lane-phase1-start", tracker: "TK-PHASE1-START"},
				{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"},
			})
			stale := provisionPhase1StaleAuthority(t, fixture, "start-"+boundary)
			staleProbe := stale.probe(t, fixture, "start-"+boundary)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{
				operation: fmt.Sprintf("operation-phase1-start-%02d", index), work: "work-phase1-start", lane: "lane-phase1-start",
				actor: "actor-phase1-start", process: fmt.Sprintf("process-phase1-start-%02d", index),
				workspace: fmt.Sprintf("workspace-phase1-start-%02d", index), branch: fmt.Sprintf("runs/phase1-start-%02d", index), trackerWork: "TK-PHASE1-START",
			}
			crashWorkspaceAt(t, ctx, fixture, boundary, input)
			faultExpected := stale.expected.clone()
			var preRecovery execution.StartRecoveryRecord
			if boundary != "SR-00" {
				preRecovery = loadPhase1RecoveryRecord(t, fixture.database, ids.OperationID(input.operation))
				if preRecovery.LeaseStatus == "active" {
					faultExpected.addRecoveryAuthority(preRecovery)
				}
				if preRecovery.WorkspaceStage != "" {
					faultExpected.addRecoveryWorkspace(fixture, preRecovery)
				}
			}
			assertPhase1Universal(t, ctx, fixture, boundary, faultExpected, []string{stale.candidate}, staleProbe, true)
			assertPhase1StartBoundaryFact(t, ctx, fixture, input, boundary, 1)

			if boundary == "SR-00" {
				response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
				recoveredExpected := stale.expected.clone()
				if response.Capsule != nil {
					recoveredExpected.addCapsule(response.Capsule, true)
				}
				assertPhase1Universal(t, ctx, fixture, boundary+"-RECOVERED", recoveredExpected, []string{stale.candidate}, staleProbe, true)
				if !response.OK || response.Capsule == nil {
					t.Fatalf("scenario=phase1-start fault=%s retry=%+v", boundary, response)
				}
			} else {
				recoveredExpected := stale.expected.clone()
				response := recoverWorkspaceOperation(t, ctx, fixture, input.operation)
				if response.Decision != nil && response.Decision.Capsule != nil {
					recoveredExpected.addCapsule(response.Decision.Capsule, true)
				}
				assertPhase1Universal(t, ctx, fixture, boundary+"-RECOVERED", recoveredExpected, []string{stale.candidate}, staleProbe, true)
				if !response.OK || response.Decision == nil || response.Decision.Disposition != execution.RecoveryComplete || response.Decision.Capsule == nil {
					t.Fatalf("scenario=phase1-start fault=%s recovery=%+v", boundary, response)
				}
				if (preRecovery.RunID != "" && response.Decision.Capsule.RunID != preRecovery.RunID) || (preRecovery.LeaseID != "" && response.Decision.Capsule.LeaseID != preRecovery.LeaseID) || response.Decision.Capsule.WorkspaceID != ids.WorkspaceID(input.workspace) {
					t.Fatalf("scenario=phase1-start fault=%s recovered identity=%+v expected_run=%s expected_lease=%s expected_workspace=%s", boundary, response.Decision.Capsule, preRecovery.RunID, preRecovery.LeaseID, input.workspace)
				}
			}
		})
	}
}

func assertPhase1StartBoundaryFact(t *testing.T, ctx context.Context, fixture workspaceScenario, input workspaceStart, boundary string, baselineOperations ...int) {
	t.Helper()
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	snapshot, err := control.AuthoritySnapshot(ctx, ids.WorkID(input.work), "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	if boundary == "SR-00" {
		wantOperations := 0
		if len(baselineOperations) != 0 {
			wantOperations = baselineOperations[0]
		}
		if snapshot.Operations != wantOperations || snapshot.Runs != 0 || snapshot.ActiveLeases != 0 {
			t.Fatalf("scenario=phase1-start fault=%s operations=%d runs=%d active_leases=%d recovery=retry the original command", boundary, snapshot.Operations, snapshot.Runs, snapshot.ActiveLeases)
		}
		return
	}
	recovery, err := control.LoadStartRun(ctx, ids.OperationID(input.operation))
	if err != nil || recovery.TerminalRecorded {
		t.Fatalf("scenario=phase1-start fault=%s recovery_state=%+v error=%v recovery=inspect the durable operation", boundary, recovery, err)
	}
	if boundary == "SR-01" {
		if recovery.OperationStatus != "started" || recovery.LeaseStatus != "" || snapshot.Runs != 0 || snapshot.ActiveLeases != 0 {
			t.Fatalf("scenario=phase1-start fault=%s operation=%s lease=%s runs=%d active=%d recovery=resume the recorded operation", boundary, recovery.OperationStatus, recovery.LeaseStatus, snapshot.Runs, snapshot.ActiveLeases)
		}
		return
	}
	if recovery.OperationStatus != "succeeded" || recovery.LeaseStatus != "active" || snapshot.Runs != 1 || snapshot.ActiveLeases != 1 {
		t.Fatalf("scenario=phase1-start fault=%s operation=%s lease=%s runs=%d active=%d run_id=%s lease_id=%s generation=%d recovery=repair the authority fact before workspace recovery", boundary, recovery.OperationStatus, recovery.LeaseStatus, snapshot.Runs, snapshot.ActiveLeases, recovery.RunID, recovery.LeaseID, recovery.Generation)
	}
	if boundary < "SR-05" {
		if recovery.WorkspaceStatus != "" || recovery.WorkspaceStage != "" {
			t.Fatalf("scenario=phase1-start fault=%s workspace_status=%s workspace_stage=%s recovery=remove phantom readiness", boundary, recovery.WorkspaceStatus, recovery.WorkspaceStage)
		}
		return
	}
	wantStage := workspace.StageReserved
	if boundary == "SR-07" || boundary == "SR-08" {
		wantStage = workspace.StageBaseVerified
	} else if boundary == "SR-09" {
		wantStage = workspace.StageReady
	}
	if recovery.WorkspaceStage != wantStage || (boundary != "SR-09" && recovery.WorkspaceStatus == "ready") {
		t.Fatalf("scenario=phase1-start fault=%s workspace_status=%s workspace_stage=%s expected_stage=%s recovery=re-probe real Git facts", boundary, recovery.WorkspaceStatus, recovery.WorkspaceStage, wantStage)
	}
	path := filepath.Join(fixture.managedRoot, input.workspace)
	_, statErr := os.Stat(path)
	if boundary == "SR-05" && !os.IsNotExist(statErr) {
		t.Fatalf("scenario=phase1-start fault=%s workspace=%s stat=%v recovery=preserve the reservation without inventing a worktree", boundary, path, statErr)
	}
	if boundary >= "SR-06" && statErr != nil {
		t.Fatalf("scenario=phase1-start fault=%s workspace=%s stat=%v recovery=recover from the registered Git worktree", boundary, path, statErr)
	}
}

func testPhase1HandoffReviewFaults(t *testing.T) {
	for _, boundary := range phase1HandoffReviewFaults {
		boundary := boundary
		t.Run(boundary, func(t *testing.T) {
			targetWork, targetLane, targetTracker := ids.WorkID("work-phase1-"+boundary), ids.LaneID("lane-phase1-"+boundary), "TK-phase1-"+boundary
			fixture := newWorkspaceScenario(t, []workspaceItem{
				{work: targetWork, lane: targetLane, tracker: targetTracker},
				{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"},
			})
			stale := provisionPhase1StaleAuthority(t, fixture, "review-"+boundary)
			staleProbe := stale.probe(t, fixture, "review-"+boundary)
			targetInput := workspaceStart{operation: "operation-start-phase1-" + boundary, work: targetWork.String(), lane: targetLane.String(), actor: "actor-phase1-" + boundary, process: "process-phase1-" + boundary, workspace: "workspace-phase1-" + boundary, branch: "runs/work-phase1-" + boundary, trackerWork: targetTracker, editFile: "candidate.txt", editContent: "candidate\n"}
			targetResponse := waitWorkspaceProcess(t, fixture.start(context.Background(), t, targetInput))
			if !targetResponse.OK || targetResponse.Capsule == nil || targetResponse.Work == nil {
				t.Fatalf("scenario=phase1-review fault=%s target_start=%+v", boundary, targetResponse)
			}
			capsule, candidate := targetResponse.Capsule, targetResponse.Work.Commit
			expected := stale.expected.clone()
			expected.addCapsule(capsule, true)
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("crash\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if boundary <= "HR-02" {
				crashPhase1Helper(t, fixture.barrier, boundary, append(handoffArgs(fixture, capsule, candidate, "handoff-phase1-fault", "operation-handoff-phase1-fault"), "--barrier", fixture.barrier))
			} else {
				handoff := runHandoffHelper(t, fixture, capsule, candidate, "handoff-phase1-fault", "operation-handoff-phase1-fault", "")
				if !handoff.OK {
					t.Fatalf("scenario=phase1-review fault=%s handoff=%+v", boundary, handoff)
				}
				crashPhase1Helper(t, fixture.barrier, boundary, append(reviewArgs(fixture, capsule, candidate, "handoff-phase1-fault", "review-phase1-fault", "operation-review-phase1-fault", "actor-phase1-reviewer", "process-phase1-reviewer", review.Approved), "--barrier", fixture.barrier))
			}
			assertPhase1Universal(t, context.Background(), fixture, boundary, expected, []string{candidate, stale.candidate}, staleProbe, true)
			assertHandoffReviewBoundaryFact(t, fixture.database, boundary, 1)
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("continue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if boundary <= "HR-02" {
				replay := runHandoffHelper(t, fixture, capsule, candidate, "handoff-phase1-fault", "operation-handoff-phase1-fault", fixture.barrier)
				assertPhase1Universal(t, context.Background(), fixture, boundary+"-RECOVERED", expected, []string{candidate, stale.candidate}, staleProbe, true)
				if !replay.OK || replay.Judgment == nil || replay.Judgment.Handoff == nil {
					t.Fatalf("scenario=phase1-review fault=%s handoff_replay=%+v", boundary, replay)
				}
			} else {
				replay := runReviewHelper(t, fixture, capsule, candidate, "handoff-phase1-fault", "review-phase1-fault", "operation-review-phase1-fault", "actor-phase1-reviewer", "process-phase1-reviewer", review.Approved, fixture.barrier)
				assertPhase1Universal(t, context.Background(), fixture, boundary+"-RECOVERED", expected, []string{candidate, stale.candidate}, staleProbe, true)
				if !replay.OK || replay.Judgment == nil || replay.Judgment.Review == nil {
					t.Fatalf("scenario=phase1-review fault=%s review_replay=%+v", boundary, replay)
				}
			}
		})
	}
}

func testPhase1IntegrationFaults(t *testing.T) {
	for _, boundary := range phase1IntegrationFaults {
		boundary := boundary
		t.Run(boundary, func(t *testing.T) {
			targetWork, targetLane, targetTracker := ids.WorkID("work-phase1-"+boundary), ids.LaneID("lane-phase1-"+boundary), "TK-phase1-"+boundary
			fixture := newWorkspaceScenario(t, []workspaceItem{
				{work: targetWork, lane: targetLane, tracker: targetTracker},
				{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"},
			})
			stale := provisionPhase1StaleAuthority(t, fixture, "integration-"+boundary)
			staleProbe := stale.probe(t, fixture, "integration-"+boundary)
			targetInput := workspaceStart{operation: "operation-start-phase1-" + boundary, work: targetWork.String(), lane: targetLane.String(), actor: "actor-phase1-" + boundary, process: "process-phase1-" + boundary, workspace: "workspace-phase1-" + boundary, branch: "runs/work-phase1-" + boundary, trackerWork: targetTracker, editFile: "candidate.txt", editContent: "candidate\n"}
			targetResponse := waitWorkspaceProcess(t, fixture.start(context.Background(), t, targetInput))
			if !targetResponse.OK || targetResponse.Capsule == nil || targetResponse.Work == nil {
				t.Fatalf("scenario=phase1-integration fault=%s target_start=%+v", boundary, targetResponse)
			}
			capsule, candidate := targetResponse.Capsule, targetResponse.Work.Commit
			handoffID := "handoff-phase1-" + boundary
			if response := runHandoffHelper(t, fixture, capsule, candidate, handoffID, "operation-handoff-phase1-"+boundary, ""); !response.OK {
				t.Fatalf("scenario=phase1-integration fault=%s handoff=%+v", boundary, response)
			}
			if response := runReviewHelper(t, fixture, capsule, candidate, handoffID, "review-phase1-"+boundary, "operation-review-phase1-"+boundary, "actor-reviewer-phase1-"+boundary, "process-reviewer-phase1-"+boundary, review.Approved, ""); !response.OK {
				t.Fatalf("scenario=phase1-integration fault=%s review=%+v", boundary, response)
			}
			expected := stale.expected.clone()
			expected.addCapsule(capsule, true)
			scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("crash\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			operation := "operation-phase1-integrate-" + boundary
			args := integrationArgs(fixture, fixture.database, operation, "review-phase1-"+boundary, candidate, "actor-phase1-integrator", "process-phase1-integrator-"+boundary, time.Now().UTC())
			crashPhase1Helper(t, fixture.barrier, boundary, args)
			assertPhase1Universal(t, context.Background(), fixture, boundary, expected, []string{candidate, stale.candidate}, staleProbe, true)
			assertIntegrationBoundaryState(t, fixture.database, fixture.repository, operation, boundary, fixture.base)
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("continue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			response := runExternalIntegration(t, args)
			assertPhase1Universal(t, context.Background(), fixture, boundary+"-RECOVERED", expected, []string{candidate, stale.candidate}, staleProbe, true)
			if !response.OK || response.Integration == nil || response.Integration.Record.Status != integration.StatusDone {
				t.Fatalf("scenario=phase1-integration fault=%s recovery=%+v", boundary, response)
			}
		})
	}
}

func assertHandoffReviewBoundaryFact(t *testing.T, database, boundary string, baselineHandoffs int) {
	t.Helper()
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var handoffs, reviews int
	if err := db.QueryRow(`SELECT COUNT(*) FROM handoffs`).Scan(&handoffs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM reviews`).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	wantHandoffs, wantReviews := baselineHandoffs, 0
	if boundary == "HR-02" || boundary >= "HR-03" {
		wantHandoffs++
	}
	if boundary == "HR-06" || boundary == "HR-07" {
		wantReviews = 1
	}
	if handoffs != wantHandoffs || reviews != wantReviews {
		t.Fatalf("scenario=phase1-review fault=%s handoffs=%d want_handoffs=%d reviews=%d want_reviews=%d recovery=retry the identical immutable command", boundary, handoffs, wantHandoffs, reviews, wantReviews)
	}
}

func crashPhase1Helper(t *testing.T, barrier, boundary string, args []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, helperBinary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := waitForBoundaryReached(ctx, barrier, boundary, 1); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 || stdout.Len() != 0 {
		t.Fatalf("fault=%s exit=%v stdout=%q stderr=%q recovery=retry the same operation", boundary, err, stdout.String(), stderr.String())
	}
}

func runPhase1Compensation(t *testing.T, fixture workspaceScenario, input workspaceStart, expectedRevision int64) helpercmd.TrackerResponse {
	t.Helper()
	command := exec.Command(helperBinary, "tracker", "compensate", "--db", fixture.database, "--tracker", fixture.tracker,
		"--operation", input.operation, "--correlation", "compensate-"+input.operation, "--actor", input.actor, "--process", input.process,
		"--provider", "fake", "--work-key", input.trackerWork, "--expected-revision", fmt.Sprint(expectedRevision))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("scenario=phase1-two-agent fault=ATTRIBUTION-CHANGE helper=%v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	var response helpercmd.TrackerResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("scenario=phase1-two-agent fault=ATTRIBUTION-CHANGE decode=%v output=%q", err, stdout.String())
	}
	return response
}

type phase1ExpectedState struct {
	workspaces map[ids.WorkspaceID]workspaceBindingFact
	authority  map[string]activeLeaseFact
}

type phase1StaleAuthority struct {
	capsule   execution.RunCapsule
	candidate string
	handoffID string
	expected  phase1ExpectedState
}

func newPhase1ExpectedState() phase1ExpectedState {
	return phase1ExpectedState{workspaces: map[ids.WorkspaceID]workspaceBindingFact{}, authority: map[string]activeLeaseFact{}}
}

func (state phase1ExpectedState) clone() phase1ExpectedState {
	cloned := newPhase1ExpectedState()
	for key, value := range state.workspaces {
		cloned.workspaces[key] = value
	}
	for key, value := range state.authority {
		cloned.authority[key] = value
	}
	return cloned
}

func (state *phase1ExpectedState) addCapsule(capsule *execution.RunCapsule, active bool) {
	state.workspaces[capsule.WorkspaceID] = workspaceBindingFact{
		RepositoryID: capsule.RepositoryID, OperationID: capsule.OperationID, RunID: capsule.RunID,
		LeaseID: capsule.LeaseID, Generation: capsule.Generation, LaneID: capsule.LaneID,
		Path: capsule.WorkspacePath, Branch: capsule.OwnedBranch, GitDir: capsule.GitDir,
		IndexPath: capsule.IndexPath, PinnedBase: capsule.VerifiedBase,
	}
	key := leaseFactKey(capsule.WorkRevisionID, capsule.WorkID)
	delete(state.authority, key)
	if active {
		state.authority[key] = activeLeaseFact{
			WorkRevisionID: capsule.WorkRevisionID, WorkID: capsule.WorkID, LeaseID: capsule.LeaseID,
			Generation: capsule.Generation, TokenDigest: phase1TokenDigest(capsule.Token), Deadline: capsule.Deadline.UTC().Format(time.RFC3339Nano),
		}
	}
}

func (state *phase1ExpectedState) addRecoveryWorkspace(fixture workspaceScenario, record execution.StartRecoveryRecord) {
	request := record.Request
	path := filepath.Join(fixture.managedRoot, request.Workspace.WorkspaceID.String())
	gitDir, indexPath := "", ""
	if record.WorkspaceStage != "" && record.WorkspaceStage != workspace.StageReserved {
		gitDir = filepath.Join(fixture.repository, ".git", "worktrees", filepath.Base(path))
		indexPath = filepath.Join(gitDir, "index")
	}
	state.workspaces[request.Workspace.WorkspaceID] = workspaceBindingFact{
		RepositoryID: request.Workspace.RepositoryID, OperationID: request.Claim.OperationID, RunID: record.RunID,
		LeaseID: record.LeaseID, Generation: record.Generation, LaneID: request.Workspace.LaneID,
		Path: path, Branch: request.Workspace.OwnedBranch, GitDir: gitDir, IndexPath: indexPath, PinnedBase: request.Workspace.PinnedBase,
	}
}

func (state *phase1ExpectedState) addRecoveryAuthority(record execution.StartRecoveryRecord) {
	claim := record.Request.Claim
	key := leaseFactKey(claim.WorkRevisionID, claim.WorkID)
	state.authority[key] = activeLeaseFact{
		WorkRevisionID: claim.WorkRevisionID, WorkID: claim.WorkID, LeaseID: record.LeaseID,
		Generation: record.Generation, TokenDigest: phase1TokenDigest(record.Token), Deadline: record.Deadline.UTC().Format(time.RFC3339Nano),
	}
}

func phase1TokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func loadPhase1RecoveryRecord(t *testing.T, database string, operation ids.OperationID) execution.StartRecoveryRecord {
	t.Helper()
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.LoadStartRun(context.Background(), operation)
	if err != nil {
		t.Fatalf("load exact operation=%s recovery=%v", operation, err)
	}
	if record.Request.Claim.OperationID != operation {
		t.Fatalf("operation lookup drift requested=%s returned=%s", operation, record.Request.Claim.OperationID)
	}
	return record
}

func provisionPhase1StaleAuthority(t *testing.T, fixture workspaceScenario, suffix string) phase1StaleAuthority {
	t.Helper()
	input := workspaceStart{
		operation: "operation-phase1-matrix-stale-" + suffix, work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale",
		actor: "actor-phase1-matrix-stale", process: "process-phase1-matrix-stale-" + suffix,
		workspace: "workspace-phase1-matrix-stale-" + suffix, branch: "runs/phase1-matrix-stale-" + suffix,
		trackerWork: "TK-PHASE1-MATRIX-STALE", editFile: "stale-" + suffix + ".txt", editContent: "stale\n",
	}
	response := waitWorkspaceProcess(t, fixture.start(context.Background(), t, input))
	if !response.OK || response.Capsule == nil || response.Work == nil {
		t.Fatalf("prepare same-fixture stale authority=%+v", response)
	}
	handoffID := "handoff-phase1-matrix-stale-" + suffix
	if handoffResponse := runHandoffHelper(t, fixture, response.Capsule, response.Work.Commit, handoffID, "operation-handoff-phase1-matrix-stale-"+suffix, ""); !handoffResponse.OK {
		t.Fatalf("prepare same-fixture stale handoff=%+v", handoffResponse)
	}
	expireAuthorityProcess(t, context.Background(), fixture.database, *response.Capsule, input, response.Capsule.Deadline)
	expected := newPhase1ExpectedState()
	expected.addCapsule(response.Capsule, false)
	return phase1StaleAuthority{capsule: *response.Capsule, candidate: response.Work.Commit, handoffID: handoffID, expected: expected}
}

func (stale phase1StaleAuthority) probe(t *testing.T, fixture workspaceScenario, suffix string) func(context.Context) error {
	t.Helper()
	return actualStalePublicationProbe(t, fixture, stale.capsule, stale.candidate, stale.handoffID, suffix)
}

func assertPhase1Universal(t *testing.T, ctx context.Context, fixture workspaceScenario, fault string, expected phase1ExpectedState, candidates []string, staleProbe func(context.Context) error, requireStale bool) {
	t.Helper()
	state := universalAssertionState{
		ScenarioID: "phase1-two-agent-authority", FaultID: fault, Database: fixture.database, TrackerPath: fixture.tracker,
		RepositoryPath: fixture.repository, Repository: fixture.primary, ExpectedPrimary: fixture.primary.Binding().Primary,
		ExpectedWorkspaces: expected.workspaces, ExpectedAuthority: expected.authority, CandidateCommits: candidates,
		StaleProbe: staleProbe, RequireStaleProbe: requireStale,
	}
	if err := assertUniversalInvariants(ctx, state); err != nil {
		t.Fatal(err)
	}
}

func actualStalePublicationProbe(t *testing.T, fixture workspaceScenario, capsule execution.RunCapsule, candidate, handoffID, suffix string) func(context.Context) error {
	t.Helper()
	var sequence atomic.Int64
	return func(ctx context.Context) error {
		attempt := sequence.Add(1)
		label := fmt.Sprintf("%s-%d", suffix, attempt)
		handoff := runHandoffHelper(t, fixture, &capsule, candidate, "handoff-stale-attempt-"+label, "operation-handoff-stale-attempt-"+label, "")
		if handoff.OK || handoff.Error == nil || handoff.Error.Kind != execution.KindStaleAuthority || handoff.Error.RetrySafety != execution.RetryDecisionRequired {
			return fmt.Errorf("stale_handoff=%+v", handoff)
		}
		reviewResponse := runReviewHelper(t, fixture, &capsule, candidate, handoffID, "review-stale-attempt-"+label, "operation-review-stale-attempt-"+label, "actor-stale-reviewer-"+label, "process-stale-reviewer-"+label, review.Approved, "")
		if reviewResponse.OK || reviewResponse.Error == nil || reviewResponse.Error.Kind != execution.KindStaleAuthority || reviewResponse.Error.RetrySafety != execution.RetryDecisionRequired {
			return fmt.Errorf("stale_review=%+v", reviewResponse)
		}
		integrationArguments := integrationArgs(fixture, fixture.database, "operation-integration-stale-attempt-"+label, "review-stale-attempt-"+label, candidate, "actor-stale-integrator-"+label, "process-stale-integrator-"+label, time.Now().UTC())
		setArgument(integrationArguments, "--barrier", "")
		integrationResponse := runExternalIntegration(t, integrationArguments)
		if integrationResponse.OK || integrationResponse.Error == nil || integrationResponse.Error.Kind != execution.ErrorKind(integration.KindNotApproved) || len(integrationResponse.Error.RecoveryActions) == 0 {
			return fmt.Errorf("stale_integration=%+v", integrationResponse)
		}
		db, err := sql.Open("sqlite", fixture.database)
		if err != nil {
			return err
		}
		defer db.Close()
		var integrations int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM integrations WHERE review_id=?`, "review-stale-attempt-"+label).Scan(&integrations); err != nil {
			return err
		}
		if integrations != 0 {
			return fmt.Errorf("stale_review=%s integrations=%d", "review-stale-attempt-"+label, integrations)
		}
		return handoff.Error
	}
}

func phase1UniversalState(t *testing.T, fixture workspaceScenario, fault string, primary workspace.PrimarySnapshot, expected phase1ExpectedState, candidates []string) universalAssertionState {
	t.Helper()
	return universalAssertionState{
		ScenarioID: "phase1-two-agent-torture", FaultID: fault, Database: fixture.database, TrackerPath: fixture.tracker,
		RepositoryPath: fixture.repository, Repository: fixture.primary, ExpectedPrimary: primary,
		ExpectedWorkspaces: expected.workspaces, ExpectedAuthority: expected.authority, CandidateCommits: candidates,
	}
}
