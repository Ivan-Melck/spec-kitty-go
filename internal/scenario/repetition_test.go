package scenario

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/architecture"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestPhase1FaultManifestIsComplete(t *testing.T) {
	assertFaultFamily(t, "start-run", phase1StartFaults, "SR", 10)
	assertFaultFamily(t, "handoff-review", phase1HandoffReviewFaults, "HR", 8)
	assertFaultFamily(t, "integration", phase1IntegrationFaults, "IG", 10)
	assertPhase1Catalog(t)
}

type phase1CatalogEntry struct {
	ID       string   `json:"id"`
	Evidence string   `json:"evidence"`
	Status   string   `json:"status"`
	Blocking []string `json:"blocking,omitempty"`
}

type phase1EvidenceCatalog struct {
	SchemaVersion       int                  `json:"schema_version"`
	Mission             string               `json:"mission"`
	WorkPackage         string               `json:"work_package"`
	EnvironmentBoundary string               `json:"environment_boundary"`
	FaultFamilies       phase1FaultFamilies  `json:"fault_families"`
	NarrativeFaults     []string             `json:"narrative_faults"`
	UniversalInvariants []string             `json:"universal_invariants"`
	Catalog             []phase1CatalogEntry `json:"catalog"`
	MutationSeams       map[string]string    `json:"mutation_seams"`
}

type phase1FaultFamilies struct {
	StartRun      []string `json:"start_run"`
	HandoffReview []string `json:"handoff_review"`
	Integration   []string `json:"integration"`
}

var phase1NarrativeFaults = []string{
	"CONCURRENT-CLAIM", "LEASE-EXPIRED", "TRACKER-OUTAGE", "ZOMBIE-RESUME", "ATTRIBUTION-CHANGE",
	"SELF-REVIEW", "MANUAL-TARGET-MOVEMENT", "SIMULTANEOUS-INTEGRATION", "MERGE-CONFLICT",
}

var phase1UniversalInvariants = []string{
	"primary_isolation", "one_valid_lease", "immutable_workspace_binding", "stale_token_refusal",
	"target_ref_serialization", "candidate_reachability", "attribution_safe_compensation",
}

var phase1MutationSeams = map[string]string{
	"sqlite":       "mutations use executable helpers and public services; direct SQL is read-only assertion evidence",
	"git":          "real git argv execution, including deliberate external branch movement; no shell-built commands",
	"tracker":      "portable fake tracker provider through its public store/provider contracts",
	"faults":       "named file barriers; helper exit 86; SIGSTOP/SIGCONT only for the zombie interval",
	"expectations": "exact rows derive from scenario inputs, pre-fault records, and returned capsules; stale publication probes share each fault fixture",
}

var phase1Catalog = []phase1CatalogEntry{
	{ID: "P1-01", Evidence: "TestPhase1TwoAgentTortureChamber#CONCURRENT-CLAIM"},
	{ID: "P1-02", Evidence: "TestPhase1ExactCatalogScenarios/P1-02"},
	{ID: "P1-03", Evidence: "TestPhase1ExactCatalogScenarios/P1-03"},
	{ID: "P1-04", Evidence: "TestPhase1ExactCatalogScenarios/P1-04"},
	{ID: "P1-05", Evidence: "TestPhase1TwoAgentTortureChamber#TRACKER-OUTAGE"},
	{ID: "P1-06", Evidence: "TestPhase1CatalogRegressionScenarios/P1-06"},
	{ID: "P1-07", Evidence: "TestPhase1ExactCatalogScenarios/P1-07"},
	{ID: "P1-08", Evidence: "TestPhase1ExactCatalogScenarios/P1-08"},
	{ID: "P1-09", Evidence: "TestPhase1CatalogRegressionScenarios/P1-09"},
	{ID: "P1-10", Evidence: "TestPhase1CatalogRegressionScenarios/P1-10"},
	{ID: "P1-11", Evidence: "TestPhase1CatalogRegressionScenarios/P1-11"},
	{ID: "P1-12", Evidence: "TestPhase1OwnedBranchMovementRefusesHandoff"},
	{ID: "P1-13", Evidence: "TestPhase1TwoAgentTortureChamber#LEASE-EXPIRED"},
	{ID: "P1-14", Evidence: "TestPhase1ApprovedBeforeExpiryRemainsIntegrable"},
	{ID: "P1-15", Evidence: "TestPhase1ExactCatalogScenarios/P1-15", Status: "partial", Blocking: []string{"read-only inspection, exact commit preservation, stale-publication refusal, and cleanup refusal ship; human-authorized import choices are deferred"}},
	{ID: "P1-16", Evidence: "TestPhase1TwoAgentTortureChamber#ATTRIBUTION-CHANGE"},
	{ID: "P1-17", Evidence: "TestPhase1CatalogRegressionScenarios/P1-17"},
	{ID: "P1-18", Evidence: "TestPhase1NewHandoffSupersedesPendingReview", Status: "partial", Blocking: []string{"a newer handoff rejects a pending older judgment, but superseding an already-recorded prior judgment is deferred to the WP06 review policy owner"}},
	{ID: "P1-19", Evidence: "TestPhase1TwoAgentTortureChamber#STALE-PUBLICATION"},
	{ID: "P1-20", Evidence: "TestPhase1TwoAgentTortureChamber#SELF-REVIEW"},
	{ID: "P1-21", Evidence: "TestPhase1ReviewRejectionIsDurable", Status: "partial", Blocking: []string{"independent approval and durable rejection ship; explicit rejection-to-rework-run creation is deferred to the WP06 review workflow owner"}},
	{ID: "P1-22", Evidence: "TestPhase1CatalogRegressionScenarios/P1-22"},
	{ID: "P1-23", Evidence: "TestPhase1MovedCandidateCannotUsePriorApproval", Status: "partial", Blocking: []string{"integration refuses moved candidate C2 under C's approval; explicit acceptance and queue surfaces are outside the current Phase 1 product"}},
	{ID: "P1-24", Evidence: "TestPhase1TwoAgentTortureChamber#SIMULTANEOUS-INTEGRATION"},
	{ID: "P1-25", Evidence: "TestPhase1NamedFaultBoundaryMatrix/integration"},
	{ID: "P1-26", Evidence: "TestPhase1MergeConflictPreservesEvidence"},
	{ID: "P1-27", Evidence: "TestPhase1CatalogRegressionScenarios/P1-27"},
	{ID: "P1-28", Evidence: "TestPhase1NamedFaultBoundaryMatrix"},
	{ID: "WR-01", Evidence: "TestPhase1DurableWorkRevisionConsumerScenarios/WR-01"},
	{ID: "WR-02", Evidence: "TestPhase1DurableWorkRevisionConsumerScenarios/WR-02"},
	{ID: "WR-03", Evidence: "TestPhase1DurableWorkRevisionConsumerScenarios/WR-03"},
	{ID: "WR-04", Evidence: "TestPhase1DurableWorkRevisionConsumerScenarios/WR-04"},
	{ID: "WR-05", Evidence: "TestPhase1DurableWorkRevisionConsumerScenarios/WR-05"},
	{ID: "WR-06", Evidence: "TestPhase1DurableWorkRevisionConsumerScenarios/WR-06"},
	{ID: "WR-07", Evidence: "TestPhase1WorkRevisionDispositionCatalog/WR-07"},
}

func assertPhase1Catalog(t *testing.T) {
	t.Helper()
	want := make([]string, 0, 35)
	for index := 1; index <= 28; index++ {
		want = append(want, fmt.Sprintf("P1-%02d", index))
	}
	for index := 1; index <= 7; index++ {
		want = append(want, fmt.Sprintf("WR-%02d", index))
	}
	observed := make([]string, 0, len(phase1Catalog))
	seen := map[string]bool{}
	for _, entry := range phase1Catalog {
		if entry.Status == "" {
			entry.Status = "complete"
		}
		if entry.Status != "complete" && entry.Status != "partial" && entry.Status != "blocked" {
			t.Fatalf("scenario=phase1-manifest invalid_status=%+v", entry)
		}
		if entry.Status != "complete" && len(entry.Blocking) == 0 {
			t.Fatalf("scenario=phase1-manifest undocumented_gap=%+v", entry)
		}
		if seen[entry.ID] || !strings.HasPrefix(entry.Evidence, "TestPhase1") {
			t.Fatalf("scenario=phase1-manifest duplicate_or_non_phase1=%+v", entry)
		}
		seen[entry.ID] = true
		observed = append(observed, entry.ID)
	}
	if strings.Join(observed, ",") != strings.Join(want, ",") {
		t.Fatalf("scenario=phase1-manifest observed=%v expected=%v recovery=restore exact P1/WR catalog order", observed, want)
	}
	root, err := phase1RepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := assertPhase1EvidenceResolvable(root, phase1Catalog); err != nil {
		t.Fatalf("scenario=phase1-manifest evidence=%v recovery=map every catalog entry to a real test and literal boundary marker", err)
	}
	document, err := loadPhase1EvidenceCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePhase1EvidenceCatalog(document); err != nil {
		t.Fatalf("scenario=phase1-manifest machine_readable=%v recovery=restore the canonical in-code/JSON catalog", err)
	}
}

func loadPhase1EvidenceCatalog(root string) (phase1EvidenceCatalog, error) {
	contents, err := os.ReadFile(filepath.Join(root, "docs", "phase1-evidence", "scenario-catalog.json"))
	if err != nil {
		return phase1EvidenceCatalog{}, err
	}
	var document phase1EvidenceCatalog
	if err := json.Unmarshal(contents, &document); err != nil {
		return phase1EvidenceCatalog{}, err
	}
	return document, nil
}

func validatePhase1EvidenceCatalog(document phase1EvidenceCatalog) error {
	if document.SchemaVersion != 2 || document.Mission != "phase1-two-agent-authority-proving-ground-01M0Q4A8" || document.WorkPackage != "WP08" || document.EnvironmentBoundary != "single-machine-local-filesystem-authority" {
		return fmt.Errorf("document identity/schema mismatch: schema=%d mission=%q wp=%q boundary=%q", document.SchemaVersion, document.Mission, document.WorkPackage, document.EnvironmentBoundary)
	}
	wantFaults := phase1FaultFamilies{StartRun: phase1StartFaults, HandoffReview: phase1HandoffReviewFaults, Integration: phase1IntegrationFaults}
	if !reflect.DeepEqual(document.FaultFamilies, wantFaults) {
		return fmt.Errorf("fault families mismatch: got=%+v want=%+v", document.FaultFamilies, wantFaults)
	}
	if !reflect.DeepEqual(document.NarrativeFaults, phase1NarrativeFaults) {
		return fmt.Errorf("narrative faults mismatch: got=%v want=%v", document.NarrativeFaults, phase1NarrativeFaults)
	}
	if !reflect.DeepEqual(document.UniversalInvariants, phase1UniversalInvariants) {
		return fmt.Errorf("universal invariants mismatch: got=%v want=%v", document.UniversalInvariants, phase1UniversalInvariants)
	}
	wantCatalog := make([]phase1CatalogEntry, len(phase1Catalog))
	copy(wantCatalog, phase1Catalog)
	for index := range wantCatalog {
		if wantCatalog[index].Status == "" {
			wantCatalog[index].Status = "complete"
		}
	}
	if !reflect.DeepEqual(document.Catalog, wantCatalog) {
		return fmt.Errorf("scenario catalog mismatch: got=%+v want=%+v", document.Catalog, wantCatalog)
	}
	if !reflect.DeepEqual(document.MutationSeams, phase1MutationSeams) {
		return fmt.Errorf("mutation seams mismatch: got=%+v want=%+v", document.MutationSeams, phase1MutationSeams)
	}
	return nil
}

func TestPhase1EvidenceCatalogMutationControls(t *testing.T) {
	root, err := phase1RepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	document, err := loadPhase1EvidenceCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePhase1EvidenceCatalog(document); err != nil {
		t.Fatalf("baseline catalog: %v", err)
	}
	mutate := func(t *testing.T, change func(*phase1EvidenceCatalog)) {
		t.Helper()
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		var mutant phase1EvidenceCatalog
		if err := json.Unmarshal(encoded, &mutant); err != nil {
			t.Fatal(err)
		}
		change(&mutant)
		if err := validatePhase1EvidenceCatalog(mutant); err == nil {
			t.Fatal("catalog mutant survived canonical comparison")
		}
	}
	t.Run("evidence-pointer", func(t *testing.T) {
		mutate(t, func(c *phase1EvidenceCatalog) { c.Catalog[0].Evidence = "TestPhase1Missing" })
	})
	t.Run("status", func(t *testing.T) { mutate(t, func(c *phase1EvidenceCatalog) { c.Catalog[0].Status = "partial" }) })
	t.Run("blocker", func(t *testing.T) { mutate(t, func(c *phase1EvidenceCatalog) { c.Catalog[14].Blocking = nil }) })
	t.Run("fault-family", func(t *testing.T) {
		mutate(t, func(c *phase1EvidenceCatalog) {
			c.FaultFamilies.StartRun = c.FaultFamilies.StartRun[:len(c.FaultFamilies.StartRun)-1]
		})
	})
	t.Run("mutation-seam", func(t *testing.T) {
		mutate(t, func(c *phase1EvidenceCatalog) { c.MutationSeams["sqlite"] = "direct writes" })
	})
}

func TestPhase1ExactCatalogScenarios(t *testing.T) {
	t.Run("P1-02", func(t *testing.T) {
		responses, snapshot := runRace(t, 8)
		assertRace(t, responses, snapshot, 8)
		t.Run("same-actor-distinct-process", func(t *testing.T) {
			testPhase1SameActorRace(t, 8)
		})
	})
	t.Run("P1-03", testPhase1ConcurrentSameOperation)
	t.Run("P1-04", func(t *testing.T) { testPhase1CrashExpiryReplacement(t, "SR-02") })
	t.Run("P1-07", func(t *testing.T) { testPhase1CrashExpiryReplacement(t, "SR-05") })
	t.Run("P1-08", testPhase1WorktreeMismatchDecision)
	t.Run("P1-15", testPhase1QuarantinedOutputInspection)
}

func testPhase1SameActorRace(t *testing.T, contenders int) {
	t.Helper()
	database := seedDatabase(t)
	barrier := t.TempDir()
	if err := os.WriteFile(filepath.Join(barrier, "SR-00.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	processes := make([]*claimProcess, contenders)
	for index := range contenders {
		process, err := startClaim(ctx, helperBinary, claimInput{Database: database, Barrier: barrier, Operation: fmt.Sprintf("operation-same-actor-%02d", index), Actor: "actor-shared", Process: fmt.Sprintf("process-shared-%02d", index)})
		if err != nil {
			t.Fatal(err)
		}
		processes[index] = process
	}
	if err := waitForReached(ctx, barrier, contenders); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(barrier, "SR-00.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	responses := make([]helpercmd.Response, contenders)
	for index, process := range processes {
		response, err := process.wait()
		if err != nil {
			t.Fatalf("claimant %d: %v", index, err)
		}
		responses[index] = response
	}
	snapshot, err := inspectAuthority(ctx, helperBinary, database)
	if err != nil {
		t.Fatal(err)
	}
	assertRace(t, responses, snapshot, contenders)
}

func testPhase1ConcurrentSameOperation(t *testing.T) {
	t.Helper()
	database := seedDatabase(t)
	barrier := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := claimInput{Database: database, Barrier: barrier, Operation: "operation-concurrent-replay", Actor: "actor-concurrent-replay", Process: "process-concurrent-replay"}
	type started struct {
		process *claimProcess
		err     error
	}
	ready := make(chan struct{})
	results := make(chan started, 2)
	for range 2 {
		go func() {
			<-ready
			process, err := startClaim(ctx, helperBinary, input)
			results <- started{process: process, err: err}
		}()
	}
	close(ready)
	leftStarted, rightStarted := <-results, <-results
	if leftStarted.err != nil || rightStarted.err != nil {
		t.Fatalf("concurrent starts: left=%v right=%v", leftStarted.err, rightStarted.err)
	}
	left, right := leftStarted.process, rightStarted.process
	first, err := left.wait()
	if err != nil {
		t.Fatal(err)
	}
	second, err := right.wait()
	if err != nil {
		t.Fatal(err)
	}
	if !first.OK || !second.OK || first.Capsule == nil || second.Capsule == nil || first.Capsule.RunID != second.Capsule.RunID || first.Capsule.LeaseID != second.Capsule.LeaseID || first.Capsule.Token != second.Capsule.Token || first.Capsule.Generation != second.Capsule.Generation {
		t.Fatalf("concurrent same-operation results first=%+v second=%+v", first, second)
	}
	snapshot, err := inspectAuthority(ctx, helperBinary, database)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveLeases != 1 || snapshot.Runs != 1 || snapshot.Operations != 1 || snapshot.Events != 1 {
		t.Fatalf("concurrent replay duplicated authority: %+v", snapshot)
	}
}

func assertPhase1EvidenceResolvable(root string, catalog []phase1CatalogEntry) error {
	functions, literals := map[string]bool{}, map[string]bool{}
	for _, name := range []string{"phase1_test.go", "repetition_test.go"} {
		path := filepath.Join(root, "internal", "scenario", name)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.FuncDecl:
				functions[value.Name.Name] = true
			case *ast.BasicLit:
				if value.Kind == token.STRING {
					decoded, err := strconv.Unquote(value.Value)
					if err == nil {
						literals[decoded] = true
					}
				}
			}
			return true
		})
	}
	for _, entry := range catalog {
		evidence := strings.Replace(entry.Evidence, "#", "/", 1)
		parts := strings.Split(evidence, "/")
		if len(parts) == 0 || !functions[parts[0]] {
			return fmt.Errorf("id=%s test=%q exists=false", entry.ID, parts[0])
		}
		for _, marker := range parts[1:] {
			if marker == "" || !literals[marker] {
				return fmt.Errorf("id=%s test=%s marker=%q exists=false", entry.ID, parts[0], marker)
			}
		}
	}
	return nil
}

func TestPhase1CatalogRegressionScenarios(t *testing.T) {
	for _, scenario := range []struct {
		id  string
		run func(*testing.T)
	}{
		{"P1-02", func(t *testing.T) {
			t.Run("claim-race", TestWP01ClaimRace)
			t.Run("contention-ceiling", TestWP01ContentionCeiling)
		}},
		{"P1-03", TestWP01LostResponseReplay},
		{"P1-06", func(t *testing.T) {
			t.Run("post-apply", TestCycle1PostProviderApplyPreObservationRecoveryPreservesCompensationEvidence)
			t.Run("lost-response", TestTrackerSagaLostResponseReplayPersistsObservationWithoutAuthority)
		}},
		{"P1-09", TestStartRunRecoveryRefusesPartialDependencyHeadAndConflictNeverReady},
		{"P1-10", TestStartRunRecoveryRefusesPartialDependencyHeadAndConflictNeverReady},
		{"P1-11", TestStartRunRejectsInventedLaneAndStaleBaseBeforeAuthorityOrWorkspace},
		{"P1-15", TestWorkspaceMaintenanceHelperReportsLiveRefusalsWithoutMutation},
		{"P1-17", TestTrackerSagaAttributionSafeCompensation},
		{"P1-18", TestReviewImmutableCandidateAndActorIndependence},
		{"P1-22", TestReviewCompetingVerdictWritersAndDistinctRejection},
		{"P1-23", TestReviewImmutableCandidateAndActorIndependence},
		{"P1-27", TestIntegrationSerializesTwoLanesFromTwoMissions},
	} {
		t.Run(scenario.id, scenario.run)
	}
}

func TestPhase1NewHandoffSupersedesPendingReview(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "phase1-new-handoff")
	if response := runHandoffHelper(t, fixture, capsule, candidate, "handoff-phase1-old", "operation-handoff-phase1-old", ""); !response.OK {
		t.Fatalf("old handoff=%+v", response)
	}
	if err := os.WriteFile(filepath.Join(capsule.WorkspacePath, "new-candidate.txt"), []byte("new candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, capsule.WorkspacePath, "add", "new-candidate.txt")
	scenarioGit(t, capsule.WorkspacePath, "commit", "-m", "new phase1 candidate")
	newCandidate := scenarioGit(t, capsule.WorkspacePath, "rev-parse", "HEAD")
	if newCandidate == candidate {
		t.Fatal("new handoff candidate did not move")
	}
	if response := runHandoffHelper(t, fixture, capsule, newCandidate, "handoff-phase1-new", "operation-handoff-phase1-new", ""); !response.OK || response.Judgment == nil || response.Judgment.Handoff == nil || response.Judgment.Handoff.HandoffGeneration != 2 {
		t.Fatalf("new handoff=%+v", response)
	}
	stale := runReviewHelper(t, fixture, capsule, candidate, "handoff-phase1-old", "review-phase1-old", "operation-review-phase1-old", "actor-phase1-old-reviewer", "process-phase1-old-reviewer", review.Approved, "")
	if stale.OK || stale.Error == nil || stale.Error.Kind != execution.ErrorKind(review.KindRevisionIneligible) || stale.Error.ObservedState["handoff_generation"] != "1" || stale.Error.ExpectedState["handoff_generation"] != "2" {
		t.Fatalf("older handoff judgment=%+v", stale)
	}
}

func TestPhase1ReviewRejectionIsDurable(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "phase1-rejection-durable")
	const handoffID = "handoff-phase1-rejection-durable"
	if response := runHandoffHelper(t, fixture, capsule, candidate, handoffID, "operation-handoff-phase1-rejection-durable", ""); !response.OK {
		t.Fatalf("handoff=%+v", response)
	}
	rejection := runReviewHelper(t, fixture, capsule, candidate, handoffID, "review-phase1-rejection-durable", "operation-review-phase1-rejection-durable", "actor-phase1-rejection-reviewer", "process-phase1-rejection-reviewer", review.ChangesRequested, "")
	if !rejection.OK || rejection.Judgment == nil || rejection.Judgment.Review == nil || rejection.Judgment.Review.Verdict != review.ChangesRequested {
		t.Fatalf("rejection=%+v", rejection)
	}
	db, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var verdict, criterionStatus string
	if err := db.QueryRow(`SELECT verdict FROM reviews WHERE review_id='review-phase1-rejection-durable'`).Scan(&verdict); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM review_criteria WHERE review_id='review-phase1-rejection-durable'`).Scan(&criterionStatus); err != nil {
		t.Fatal(err)
	}
	if verdict != string(review.ChangesRequested) || criterionStatus != string(review.Failed) {
		t.Fatalf("durable rejection verdict=%q criterion=%q", verdict, criterionStatus)
	}
}

func TestPhase1MovedCandidateCannotUsePriorApproval(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "phase1-moved-approved")
	approveMissionCandidate(t, fixture, capsule, "mission-1", candidate, "phase1-moved-approved")
	if err := os.WriteFile(filepath.Join(capsule.WorkspacePath, "moved-after-approval.txt"), []byte("moved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, capsule.WorkspacePath, "add", "moved-after-approval.txt")
	scenarioGit(t, capsule.WorkspacePath, "commit", "-m", "move after approval")
	moved := scenarioGit(t, capsule.WorkspacePath, "rev-parse", "HEAD")
	if moved == candidate {
		t.Fatal("approved branch did not move")
	}
	response := runExternalIntegration(t, integrationArgs(fixture, fixture.database, "operation-integrate-phase1-moved-approved", "review-integration-phase1-moved-approved", moved, "actor-integrator-phase1-moved-approved", "process-integrator-phase1-moved-approved", time.Now().UTC()))
	if response.OK || response.Error == nil || response.Error.Kind != execution.ErrorKind(integration.KindNotApproved) {
		t.Fatalf("moved candidate reused old approval=%+v", response)
	}
	if got := scenarioGit(t, fixture.repository, "rev-parse", "refs/heads/main"); got != fixture.base {
		t.Fatalf("ref moved on refused C2 integration: got=%s base=%s", got, fixture.base)
	}
}

func TestPhase1WorkRevisionDispositionCatalog(t *testing.T) {
	oldRevision := scenarioRevision(t, "revision-phase1-old", 1, []ids.WorkID{"work-old"})
	tests := []struct {
		id          string
		disposition workgraph.Disposition
		status      workgraph.WorkStatus
		newWork     []ids.WorkID
		forks       []workgraph.ForkEdge
		assert      func(*testing.T, workgraph.Consequence)
	}{
		{"WR-01", workgraph.Continue, workgraph.StatusActive, []ids.WorkID{"work-old"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if !c.RunMayContinue || !c.HandoffEligible || !c.IntegrationEligible {
				t.Fatalf("continue=%+v", c)
			}
		}},
		{"WR-02", workgraph.Supersede, workgraph.StatusActive, []ids.WorkID{"work-replacement"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if !c.RunMayContinue || c.HandoffEligible || c.IntegrationEligible {
				t.Fatalf("supersede=%+v", c)
			}
		}},
		{"WR-03", workgraph.Invalidate, workgraph.StatusActive, []ids.WorkID{"work-old"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if !c.ExpireAuthority || !c.QuarantineWorkspace || c.RunMayContinue {
				t.Fatalf("invalidate=%+v", c)
			}
		}},
		{"WR-04", workgraph.Fork, workgraph.StatusActive, []ids.WorkID{"work-left", "work-right"}, []workgraph.ForkEdge{{FromOldWorkID: "work-old", ToNewWorkID: "work-left"}, {FromOldWorkID: "work-old", ToNewWorkID: "work-right"}}, func(t *testing.T, c workgraph.Consequence) {
			if !c.RunMayContinue || !c.HandoffEligible || !c.IntegrationEligible {
				t.Fatalf("fork=%+v", c)
			}
		}},
		{"WR-05", workgraph.Supersede, workgraph.StatusApproved, []ids.WorkID{"work-new-dependency"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if c.IntegrationEligible || c.HandoffEligible {
				t.Fatalf("approved supersede=%+v", c)
			}
		}},
		{"WR-06", workgraph.Invalidate, workgraph.StatusIntegrated, []ids.WorkID{"work-follow-up"}, nil, func(t *testing.T, c workgraph.Consequence) {
			if !c.PreservesIntegratedHistory || c.ExpireAuthority || c.QuarantineWorkspace {
				t.Fatalf("integrated history=%+v", c)
			}
		}},
	}
	for index, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			newRevision := scenarioRevision(t, ids.WorkRevisionID("revision-phase1-"+test.id), int64(index+2), test.newWork)
			reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
				ID: "reconciliation-phase1-" + test.id, Old: oldRevision, New: newRevision, Disposition: test.disposition,
				ActorID: "actor-owner", Rationale: "phase1 " + test.id, OldWorkIDs: []ids.WorkID{"work-old"}, NewWorkIDs: test.newWork,
				ForkEdges: test.forks, OccurredAt: time.Unix(int64(index+1), 0),
			})
			if err != nil {
				t.Fatal(err)
			}
			consequence, ok := reconciliation.Consequence("work-old", test.status)
			if !ok || consequence.Reason == "" {
				t.Fatalf("consequence=%+v present=%t", consequence, ok)
			}
			test.assert(t, consequence)
		})
	}
	t.Run("WR-07", func(t *testing.T) {
		draft := workgraph.NewDraft("project-1", "mission-1")
		for _, lane := range []workgraph.Lane{{ID: "lane-a"}, {ID: "lane-b"}} {
			if err := draft.AddLane(lane); err != nil {
				t.Fatal(err)
			}
		}
		for _, item := range []workgraph.WorkItem{
			{ID: "a1", LaneID: "lane-a", Criteria: []workgraph.Criterion{{ID: "a1", Text: "done"}}},
			{ID: "b1", LaneID: "lane-b", Criteria: []workgraph.Criterion{{ID: "b1", Text: "done"}}, Dependencies: []ids.WorkID{"a1"}},
			{ID: "b2", LaneID: "lane-b", Criteria: []workgraph.Criterion{{ID: "b2", Text: "done"}}},
			{ID: "a2", LaneID: "lane-a", Criteria: []workgraph.Criterion{{ID: "a2", Text: "done"}}, Dependencies: []ids.WorkID{"b2"}},
		} {
			if err := draft.AddWork(item); err != nil {
				t.Fatal(err)
			}
		}
		_, err := draft.Finalize(workgraph.FinalizeMetadata{ID: "revision-phase1-cycle", Number: 1, ActorID: "actor-owner", FinalizedAt: time.Unix(1, 0)})
		if !workgraph.IsKind(err, workgraph.KindLaneCycle) {
			t.Fatalf("cycle=%v", err)
		}
	})
}

func TestPhase1DurableWorkRevisionConsumerScenarios(t *testing.T) {
	t.Run("WR-01", func(t *testing.T) {
		fixture, capsule, candidate := startPhase1RevisionCandidate(t, "wr01")
		newRevision := scenarioRevision(t, "revision-phase1-wr01-2", 2, []ids.WorkID{capsule.WorkID})
		reconciliationID := reconcilePhase1RevisionThroughHelper(t, fixture.database, "wr01", capsule.WorkRevisionID, newRevision, workgraph.Continue, []ids.WorkID{capsule.WorkID}, []ids.WorkID{capsule.WorkID}, nil)
		assertDurablePhase1Consequence(t, fixture.database, reconciliationID, capsule.WorkID, workgraph.StatusActive, func(consequence workgraph.Consequence) bool {
			return consequence.RunMayContinue && consequence.HandoffEligible && consequence.IntegrationEligible
		})
		response := runHandoffHelper(t, fixture, capsule, candidate, "handoff-phase1-wr01", "operation-handoff-phase1-wr01", "")
		if !response.OK {
			t.Fatalf("continue consumer handoff=%+v", response)
		}
	})

	t.Run("WR-02", func(t *testing.T) {
		fixture, capsule, candidate := startPhase1RevisionCandidate(t, "wr02")
		newRevision := scenarioRevision(t, "revision-phase1-wr02-2", 2, []ids.WorkID{"work-phase1-wr02-replacement"})
		reconciliationID := reconcilePhase1RevisionThroughHelper(t, fixture.database, "wr02", capsule.WorkRevisionID, newRevision, workgraph.Supersede, []ids.WorkID{capsule.WorkID}, []ids.WorkID{"work-phase1-wr02-replacement"}, nil)
		assertDurablePhase1Consequence(t, fixture.database, reconciliationID, capsule.WorkID, workgraph.StatusActive, func(consequence workgraph.Consequence) bool {
			return consequence.RunMayContinue && !consequence.HandoffEligible && !consequence.IntegrationEligible
		})
		response := runHandoffHelper(t, fixture, capsule, candidate, "handoff-phase1-wr02", "operation-handoff-phase1-wr02", "")
		if response.OK || response.Error == nil || response.Error.Kind != execution.ErrorKind(handoff.KindRevisionIneligible) {
			t.Fatalf("supersede consumer handoff=%+v", response)
		}
	})

	t.Run("WR-03", func(t *testing.T) {
		fixture, capsule, candidate := startPhase1RevisionCandidate(t, "wr03")
		priorHandoffID := "handoff-phase1-wr03-prior"
		if response := runHandoffHelper(t, fixture, capsule, candidate, priorHandoffID, "operation-handoff-phase1-wr03-prior", ""); !response.OK {
			t.Fatalf("pre-invalidation handoff=%+v", response)
		}
		newRevision := scenarioRevision(t, "revision-phase1-wr03-2", 2, []ids.WorkID{capsule.WorkID})
		reconciliationID := reconcilePhase1RevisionThroughHelper(t, fixture.database, "wr03", capsule.WorkRevisionID, newRevision, workgraph.Invalidate, []ids.WorkID{capsule.WorkID}, []ids.WorkID{capsule.WorkID}, nil)
		expected := newPhase1ExpectedState()
		expected.addCapsule(capsule, false)
		assertPhase1Universal(t, context.Background(), fixture, "WR-03-INVALIDATED", expected, []string{candidate}, nil, false)
		assertDurablePhase1Consequence(t, fixture.database, reconciliationID, capsule.WorkID, workgraph.StatusActive, func(consequence workgraph.Consequence) bool {
			return consequence.ExpireAuthority && consequence.QuarantineWorkspace && !consequence.RunMayContinue
		})
		store, err := sqlite.Open(context.Background(), fixture.database)
		if err != nil {
			t.Fatal(err)
		}
		record, err := store.Workspace(context.Background(), capsule.WorkspaceID)
		_ = store.Close()
		if err != nil || !workspace.IsQuarantined(record) {
			t.Fatalf("invalidated workspace=%+v err=%v", record, err)
		}
		stale := runHandoffHelper(t, fixture, capsule, candidate, "handoff-phase1-wr03-stale", "operation-handoff-phase1-wr03-stale", "")
		if stale.OK || stale.Error == nil || (stale.Error.Kind != execution.KindStaleAuthority && stale.Error.Kind != execution.ErrorKind(handoff.KindRevisionIneligible)) {
			t.Fatalf("invalidated handoff=%+v", stale)
		}
		replacementInput := workspaceStart{operation: "operation-start-phase1-wr03-new", work: capsule.WorkID.String(), lane: "lane-a", actor: "actor-phase1-wr03-new", process: "process-phase1-wr03-new", workspace: "workspace-phase1-wr03-new", branch: "runs/phase1-wr03-new", trackerWork: "TK-PHASE1-WR03", trackerRevision: 2, revision: newRevision.ID().String(), at: "2026-08-24T12:01:00Z"}
		replacement := waitWorkspaceProcess(t, fixture.start(context.Background(), t, replacementInput))
		if replacement.Capsule == nil {
			t.Fatalf("invalidated replacement omitted capsule=%+v", replacement)
		}
		expected.addCapsule(replacement.Capsule, true)
		assertPhase1Universal(t, context.Background(), fixture, "WR-03-REPLACED", expected, []string{candidate}, actualStalePublicationProbe(t, fixture, *capsule, candidate, priorHandoffID, "wr03"), true)
		if !replacement.OK || replacement.Capsule == nil || replacement.Capsule.WorkspaceID == capsule.WorkspaceID || replacement.Capsule.WorkspacePath == capsule.WorkspacePath || replacement.Capsule.OwnedBranch == capsule.OwnedBranch {
			t.Fatalf("invalidated replacement=%+v old=%+v", replacement, capsule)
		}
	})

	t.Run("WR-04", func(t *testing.T) {
		database := filepath.Join(t.TempDir(), "control.db")
		oldRevision := scenarioRevision(t, "revision-phase1-wr04-1", 1, []ids.WorkID{"work-phase1-wr04-old"})
		finalizePhase1RevisionThroughHelper(t, database, oldRevision, 0)
		newRevision := scenarioRevision(t, "revision-phase1-wr04-2", 2, []ids.WorkID{"work-phase1-wr04-left", "work-phase1-wr04-right"})
		edges := []workgraph.ForkEdge{{FromOldWorkID: "work-phase1-wr04-old", ToNewWorkID: "work-phase1-wr04-left"}, {FromOldWorkID: "work-phase1-wr04-old", ToNewWorkID: "work-phase1-wr04-right"}}
		reconciliationID := reconcilePhase1RevisionThroughHelper(t, database, "wr04", oldRevision.ID(), newRevision, workgraph.Fork, []ids.WorkID{"work-phase1-wr04-old"}, []ids.WorkID{"work-phase1-wr04-left", "work-phase1-wr04-right"}, edges)
		store, err := sqlite.Open(context.Background(), database)
		if err != nil {
			t.Fatal(err)
		}
		evidence, err := store.Reconciliation(context.Background(), reconciliationID)
		if err != nil || len(evidence.ForkEdges) != 2 {
			_ = store.Close()
			t.Fatalf("fork evidence=%+v err=%v", evidence, err)
		}
		service := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{})
		left, leftErr := service.Start(context.Background(), execution.StartRequest{OperationID: "operation-phase1-wr04-left", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: newRevision.ID(), WorkID: "work-phase1-wr04-left", ActorID: "actor-phase1-wr04", ProcessID: "process-phase1-wr04-left", Now: time.Now().UTC(), LeaseTTL: time.Minute})
		right, rightErr := service.Start(context.Background(), execution.StartRequest{OperationID: "operation-phase1-wr04-right", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: newRevision.ID(), WorkID: "work-phase1-wr04-right", ActorID: "actor-phase1-wr04", ProcessID: "process-phase1-wr04-right", Now: time.Now().UTC(), LeaseTTL: time.Minute})
		_ = store.Close()
		if leftErr != nil || rightErr != nil || left.RunID == right.RunID || left.WorkID == right.WorkID {
			t.Fatalf("fork consumers left=%+v/%v right=%+v/%v", left, leftErr, right, rightErr)
		}
	})

	t.Run("WR-05", func(t *testing.T) {
		fixture, capsule, candidate := startPhase1RevisionCandidate(t, "wr05")
		approveMissionCandidate(t, fixture, capsule, "mission-1", candidate, "phase1-wr05")
		newRevision := phase1RevisionWithDependency(t, "revision-phase1-wr05-2", 2, capsule.WorkID, "work-phase1-wr05-dependency")
		reconciliationID := reconcilePhase1RevisionThroughHelper(t, fixture.database, "wr05", capsule.WorkRevisionID, newRevision, workgraph.Supersede, []ids.WorkID{capsule.WorkID}, []ids.WorkID{capsule.WorkID}, nil)
		assertDurablePhase1Consequence(t, fixture.database, reconciliationID, capsule.WorkID, workgraph.StatusApproved, func(consequence workgraph.Consequence) bool {
			return !consequence.HandoffEligible && !consequence.IntegrationEligible
		})
		response := runExternalIntegration(t, integrationArgs(fixture, fixture.database, "operation-integrate-phase1-wr05", "review-integration-phase1-wr05", candidate, "actor-phase1-wr05-integrator", "process-phase1-wr05-integrator", time.Now().UTC()))
		expected := newPhase1ExpectedState()
		expected.addCapsule(capsule, true)
		assertPhase1Universal(t, context.Background(), fixture, "WR-05-INTEGRATION-REFUSED", expected, []string{candidate}, nil, false)
		if response.OK || response.Error == nil || response.Error.Kind != execution.ErrorKind(integration.KindRevisionIneligible) {
			t.Fatalf("approved supersede integration=%+v", response)
		}
	})

	t.Run("WR-06", func(t *testing.T) {
		const workID ids.WorkID = "work-phase1-wr06-integrated"
		fixture := newWorkspaceScenario(t, []workspaceItem{{work: workID, lane: "lane-a", tracker: "TK-PHASE1-WR06"}})
		start := waitWorkspaceProcess(t, fixture.start(context.Background(), t, workspaceStart{
			operation: "operation-start-phase1-wr06", work: workID.String(), lane: "lane-a", actor: "actor-phase1-wr06", process: "process-phase1-wr06",
			workspace: "workspace-phase1-wr06", branch: "runs/phase1-wr06", trackerWork: "TK-PHASE1-WR06", editFile: "wr06.txt", editContent: "integrated history\n",
		}))
		if !start.OK || start.Capsule == nil || start.Work == nil {
			t.Fatalf("WR-06 start=%+v", start)
		}
		candidate := start.Work.Commit
		approveMissionCandidate(t, fixture, start.Capsule, "mission-1", candidate, "phase1-wr06")
		scenarioGit(t, fixture.repository, "checkout", "--detach", "main")
		primaryBefore, err := fixture.primary.SnapshotPrimary(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		integrationResponse := runExternalIntegration(t, integrationArgs(fixture, fixture.database, "operation-integrate-phase1-wr06", "review-integration-phase1-wr06", candidate, "actor-phase1-wr06-integrator", "process-phase1-wr06-integrator", time.Now().UTC()))
		if !integrationResponse.OK || integrationResponse.Integration == nil || integrationResponse.Integration.Record.Status != integration.StatusDone {
			t.Fatalf("WR-06 integration=%+v", integrationResponse)
		}
		integrated := integrationResponse.Integration.Record
		targetBefore := scenarioGit(t, fixture.repository, "rev-parse", "refs/heads/main")
		if targetBefore != integrated.ResultCommit || scenarioGit(t, fixture.repository, "merge-base", candidate, targetBefore) != candidate {
			t.Fatalf("WR-06 target=%s integration=%+v candidate=%s", targetBefore, integrated, candidate)
		}
		expected := newPhase1ExpectedState()
		expected.addCapsule(start.Capsule, true)
		assertPhase1Universal(t, context.Background(), fixture, "WR-06-INTEGRATED", expected, []string{candidate, integrated.ResultCommit}, nil, false)

		newRevision := scenarioRevision(t, "revision-phase1-wr06-2", 2, []ids.WorkID{"work-phase1-wr06-follow-up"})
		reconciliationID := reconcilePhase1RevisionThroughHelper(t, fixture.database, "wr06", "revision-1", newRevision, workgraph.Invalidate, []ids.WorkID{workID}, []ids.WorkID{"work-phase1-wr06-follow-up"}, nil)
		expected.addCapsule(start.Capsule, false)
		assertPhase1Universal(t, context.Background(), fixture, "WR-06-HISTORY", expected, []string{candidate, integrated.ResultCommit}, nil, false)
		assertDurablePhase1Consequence(t, fixture.database, reconciliationID, workID, workgraph.StatusIntegrated, func(consequence workgraph.Consequence) bool {
			return consequence.PreservesIntegratedHistory && !consequence.ExpireAuthority && !consequence.QuarantineWorkspace
		})
		store, err := sqlite.Open(context.Background(), fixture.database)
		if err != nil {
			t.Fatal(err)
		}
		evidence, err := store.Reconciliation(context.Background(), reconciliationID)
		if err != nil || len(evidence.OldWorkIDs) != 1 || evidence.OldWorkIDs[0] != workID || len(evidence.NewWorkIDs) != 1 || evidence.NewWorkIDs[0] != "work-phase1-wr06-follow-up" {
			_ = store.Close()
			t.Fatalf("integrated lineage=%+v err=%v", evidence, err)
		}
		followUp, followUpErr := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{}).Start(context.Background(), execution.StartRequest{
			CorrelationID: "correlation-start-phase1-wr06-follow-up", OperationID: "operation-start-phase1-wr06-follow-up", ProjectID: "project-1", MissionID: "mission-1",
			WorkRevisionID: newRevision.ID(), WorkID: "work-phase1-wr06-follow-up", ActorID: "actor-phase1-wr06-follow-up", ProcessID: "process-phase1-wr06-follow-up", Now: time.Now().UTC(), LeaseTTL: time.Minute,
		})
		_ = store.Close()
		if followUpErr != nil || followUp.RunID == "" || followUp.LeaseID == "" || followUp.WorkRevisionID != newRevision.ID() || followUp.WorkID != "work-phase1-wr06-follow-up" {
			t.Fatalf("follow-up executable=%+v err=%v", followUp, followUpErr)
		}
		db, err := sql.Open("sqlite", fixture.database)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var doneCount int
		var doneCandidate, doneResult, doneTarget string
		if err := db.QueryRow(`SELECT candidate,result_commit,target_ref FROM integration_done WHERE operation_id=?`, integrated.OperationID).Scan(&doneCandidate, &doneResult, &doneTarget); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM integration_done WHERE operation_id=?`, integrated.OperationID).Scan(&doneCount); err != nil {
			t.Fatal(err)
		}
		primaryAfter, err := fixture.primary.SnapshotPrimary(context.Background())
		if err != nil || primaryAfter != primaryBefore || scenarioGit(t, fixture.repository, "rev-parse", "refs/heads/main") != targetBefore || doneCount != 1 || doneCandidate != candidate || doneResult != integrated.ResultCommit || doneTarget != "refs/heads/main" {
			t.Fatalf("integrated history changed primary_before=%+v primary_after=%+v target=%s done=%d/%s/%s/%s err=%v", primaryBefore, primaryAfter, targetBefore, doneCount, doneCandidate, doneResult, doneTarget, err)
		}
	})
}

func startPhase1RevisionCandidate(t *testing.T, suffix string) (workspaceScenario, *execution.RunCapsule, string) {
	t.Helper()
	workID := ids.WorkID("work-phase1-" + suffix)
	trackerKey := "TK-PHASE1-" + strings.ToUpper(suffix)
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: workID, lane: "lane-a", tracker: trackerKey}})
	response := waitWorkspaceProcess(t, fixture.start(context.Background(), t, workspaceStart{operation: "operation-start-phase1-" + suffix, work: workID.String(), lane: "lane-a", actor: "actor-phase1-" + suffix, process: "process-phase1-" + suffix, workspace: "workspace-phase1-" + suffix, branch: "runs/phase1-" + suffix, trackerWork: trackerKey, editFile: suffix + ".txt", editContent: suffix + "\n"}))
	if !response.OK || response.Capsule == nil || response.Work == nil {
		t.Fatalf("start %s=%+v", suffix, response)
	}
	return fixture, response.Capsule, response.Work.Commit
}

func finalizePhase1RevisionThroughHelper(t *testing.T, database string, revision workgraph.Revision, expectedPrevious int64) {
	t.Helper()
	encoded, err := revision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	result := runWorkGraphHelper(t, "finalize", "--db", database, "--operation", "operation-finalize-"+revision.ID().String(), "--correlation", "correlation-finalize-"+revision.ID().String(), "--schema-version", "1", "--actor", revision.ActorID().String(), "--process", "process-finalize-"+revision.ID().String(), "--at", revision.FinalizedAt().Format(time.RFC3339Nano), "--expected-previous", strconv.FormatInt(expectedPrevious, 10), "--revision-json", string(encoded))
	assertTypedResult(t, result, true, "work_revision_finalized")
}

func reconcilePhase1RevisionThroughHelper(t *testing.T, database, suffix string, oldRevisionID ids.WorkRevisionID, newRevision workgraph.Revision, disposition workgraph.Disposition, oldWorkIDs, newWorkIDs []ids.WorkID, edges []workgraph.ForkEdge) string {
	t.Helper()
	finalizePhase1RevisionThroughHelper(t, database, newRevision, newRevision.Number()-1)
	reconciliationID := "operation-reconcile-phase1-" + suffix
	args := []string{"reconcile", "--db", database, "--operation", reconciliationID, "--correlation", "correlation-reconcile-phase1-" + suffix, "--schema-version", "1", "--old", oldRevisionID.String(), "--new", newRevision.ID().String(), "--disposition", string(disposition), "--actor", "actor-owner", "--process", "process-reconcile-phase1-" + suffix, "--rationale", "phase1 durable consumer " + suffix, "--old-work", joinPhase1WorkIDs(oldWorkIDs), "--new-work", joinPhase1WorkIDs(newWorkIDs), "--at", time.Unix(100+newRevision.Number(), 0).UTC().Format(time.RFC3339Nano)}
	if len(edges) != 0 {
		encodedEdges := make([]string, len(edges))
		for index, edge := range edges {
			encodedEdges[index] = edge.FromOldWorkID.String() + ":" + edge.ToNewWorkID.String()
		}
		args = append(args, "--fork-edges", strings.Join(encodedEdges, ","))
	}
	result := runWorkGraphHelper(t, args...)
	assertTypedResult(t, result, true, "work_revision_reconciled")
	return reconciliationID
}

func joinPhase1WorkIDs(values []ids.WorkID) string {
	encoded := make([]string, len(values))
	for index, value := range values {
		encoded[index] = value.String()
	}
	return strings.Join(encoded, ",")
}

func assertDurablePhase1Consequence(t *testing.T, database, reconciliationID string, workID ids.WorkID, status workgraph.WorkStatus, valid func(workgraph.Consequence) bool) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	consequence, err := store.ReconciliationConsequence(context.Background(), reconciliationID, workID, status)
	_ = store.Close()
	if err != nil || consequence.Reason == "" || !valid(consequence) {
		t.Fatalf("reconciliation=%s work=%s status=%s consequence=%+v err=%v", reconciliationID, workID, status, consequence, err)
	}
	result := runWorkGraphHelper(t, "consequence", "--db", database, "--operation", "operation-inspect-"+reconciliationID+"-"+string(status), "--correlation", "correlation-inspect-"+reconciliationID+"-"+string(status), "--reconciliation", reconciliationID, "--work", workID.String(), "--status", string(status))
	assertTypedResult(t, result, true, "reconciliation_consequence")
}

func phase1RevisionWithDependency(t *testing.T, revisionID ids.WorkRevisionID, number int64, workID, dependencyID ids.WorkID) workgraph.Revision {
	t.Helper()
	draft := workgraph.NewDraft("project-1", "mission-1")
	if err := draft.AddLane(workgraph.Lane{ID: "lane-a"}); err != nil {
		t.Fatal(err)
	}
	if err := draft.AddWork(workgraph.WorkItem{ID: dependencyID, LaneID: "lane-a", Criteria: []workgraph.Criterion{{ID: "criterion-" + dependencyID.String(), Text: "dependency"}}}); err != nil {
		t.Fatal(err)
	}
	if err := draft.AddWork(workgraph.WorkItem{ID: workID, LaneID: "lane-a", Dependencies: []ids.WorkID{dependencyID}, Criteria: []workgraph.Criterion{{ID: "criterion-" + workID.String(), Text: "done after dependency"}}}); err != nil {
		t.Fatal(err)
	}
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: revisionID, Number: number, ActorID: "actor-owner", FinalizedAt: time.Unix(number, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func assertFaultFamily(t *testing.T, family string, faults []string, prefix string, count int) {
	t.Helper()
	want := make([]string, count)
	for index := range want {
		want[index] = fmt.Sprintf("%s-%02d", prefix, index)
	}
	if strings.Join(faults, ",") != strings.Join(want, ",") {
		t.Fatalf("scenario=phase1-manifest family=%s observed=%v expected=%v recovery=restore the named fault-boundary contract", family, faults, want)
	}
}

func TestPhase1ProductionDependencyAndArchitectureScope(t *testing.T) {
	root, err := phase1RepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := architecture.LoadGraph(context.Background(), root)
	if err != nil {
		t.Fatalf("scenario=phase1-architecture load=%v", err)
	}
	policy, err := architecture.DefaultPolicy(graph.ModulePath)
	if err != nil {
		t.Fatalf("scenario=phase1-architecture policy=%v", err)
	}
	if err := policy.Check(graph); err != nil {
		t.Fatalf("scenario=phase1-architecture invariant=directional-capability-policy state=%v recovery=classify the package or remove the forbidden production edge", err)
	}
	if err := architecture.CheckReachability(root, graph.ModulePath, architecture.DefaultReachabilityTargets(graph.ModulePath)); err != nil {
		t.Fatalf("scenario=phase1-architecture invariant=production-reachability state=%v recovery=wire the registered target from production or remove the unshipped registration", err)
	}
	if err := architecture.CheckDeferredRuntimeSurfaces(root, graph.ModulePath); err != nil {
		t.Fatalf("scenario=phase1-architecture invariant=no-python-runtime state=%v recovery=remove the deferred runtime integration", err)
	}
}

func phase1RepositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")), nil
}

func TestPhase1SourceScopeMutantControl(t *testing.T) {
	const module = "github.com/spec-kitty/go-kitty3"
	policy, err := architecture.DefaultPolicy(module)
	if err != nil {
		t.Fatal(err)
	}
	beadsAdapter := module + "/internal/tracker/beads"
	for index := range policy.Tiers {
		if policy.Tiers[index].Name == architecture.TierAdapter {
			policy.Tiers[index].Packages = append(policy.Tiers[index].Packages, architecture.ExactPackage(beadsAdapter))
			policy.Tiers[index].AllowedExternalPrefixes = append(policy.Tiers[index].AllowedExternalPrefixes, "example.invalid/beads")
		}
	}
	adapterGraph := architecture.Graph{
		ModulePath: module,
		Packages:   []architecture.Package{{ImportPath: module + "/internal/tracker"}, {ImportPath: beadsAdapter}},
		Edges: []architecture.ImportEdge{
			{SourcePackage: beadsAdapter, TargetPackage: module + "/internal/tracker", Kind: architecture.ImportModuleLocal},
			{SourcePackage: beadsAdapter, TargetPackage: "example.invalid/beads", Kind: architecture.ImportExternal},
		},
	}
	if err := policy.Check(adapterGraph); err != nil {
		t.Fatalf("scenario=phase1-adapter-shape rejected=%v", err)
	}

	kernelGraph := adapterGraph
	kernelGraph.Packages = append(kernelGraph.Packages, architecture.Package{ImportPath: module + "/internal/execution"})
	kernelGraph.Edges = append(kernelGraph.Edges, architecture.ImportEdge{SourcePackage: module + "/internal/execution", TargetPackage: beadsAdapter, Kind: architecture.ImportModuleLocal})
	if err := policy.Check(kernelGraph); err == nil || !strings.Contains(err.Error(), string(architecture.RuleLocalDirection)) || !strings.Contains(err.Error(), beadsAdapter) {
		t.Fatalf("scenario=phase1-kernel-adapter-mutant observed=%v", err)
	}

	deferred := []architecture.Graph{
		{ModulePath: module, Packages: []architecture.Package{{ImportPath: module + "/internal/api"}}},
		{ModulePath: module, Packages: []architecture.Package{{ImportPath: module + "/internal/python"}}},
		{ModulePath: module, Packages: []architecture.Package{{ImportPath: module + "/internal/markdown"}}},
		{ModulePath: module, Packages: []architecture.Package{{ImportPath: module + "/internal/mcp"}}},
		{ModulePath: module, Packages: []architecture.Package{{ImportPath: module + "/internal/team-kitty"}}},
		{ModulePath: module, Packages: []architecture.Package{{ImportPath: module + "/internal/execution"}}, Edges: []architecture.ImportEdge{{SourcePackage: module + "/internal/execution", TargetPackage: "net/http", Kind: architecture.ImportStandardLibrary}}},
	}
	for _, mutant := range deferred {
		if violations := policy.Evaluate(mutant); len(violations) != 1 || violations[0].Rule != architecture.RuleDeferredSurface {
			t.Fatalf("scenario=phase1-deferred-surface-mutant graph=%+v violations=%+v", mutant, violations)
		}
	}
}
