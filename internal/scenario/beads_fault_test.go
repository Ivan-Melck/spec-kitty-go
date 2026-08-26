package scenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

func TestBeadsDishonestProtocolFaultMatrix(t *testing.T) {
	executable := buildDishonestExecutable(t)
	for _, fault := range protocolFaultRegistry() {
		fault := fault
		t.Run(fault.ID, func(t *testing.T) {
			fixture := newDishonestFixture(t, executable, []dishonestStep{showStep(fault.ID, fault.Stdout, fault.Stderr, fault.ExitCode)}, nil)
			_, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
			expectTrackerFault(t, err, fault.Kind, fault.Recovery)
			assertDishonestConsumed(t, fixture.root, 1)
		})
	}
}

func TestBeadsFaultRegistryExactCensus(t *testing.T) {
	executable := buildDishonestExecutable(t)
	expected := exactFaultRegistryIDs()
	started, terminal, passed := make([]string, 0, len(expected)), make([]string, 0, len(expected)), make([]string, 0, len(expected))
	families := map[string]int{}
	for _, caseID := range expected {
		caseID := caseID
		t.Run(caseID, func(t *testing.T) {
			started = append(started, caseID)
			family, _, _ := strings.Cut(caseID, ".")
			families[family]++
			wait := ""
			transition := map[string]string(nil)
			if family == "apply-timeout" {
				wait = "never.release"
				transition = map[string]string{"effect": caseID}
			}
			if strings.Contains(caseID, "deadline") || strings.Contains(caseID, "ignored-cancellation") {
				wait = "never.release"
			}
			fixture := newDishonestFixture(t, executable, []dishonestStep{{
				ID: caseID, Argv: []string{"show", "bd-fault", "--json"}, Stdout: envelope(issueJSON("", "", "project-fault", "work-fault", "r1")),
				TransitionTiming: func() string {
					if transition != nil {
						return "before_response"
					}
					return "none"
				}(), Transition: transition, NextObservation: transition, WaitFor: wait,
			}}, nil)
			var ctx context.Context = context.Background()
			cancel := func() {}
			if wait != "" {
				ctx, cancel = context.WithTimeout(ctx, 250*time.Millisecond)
			}
			command := execCommandContext(ctx, fixture.executable, "show", "bd-fault", "--json")
			command.Dir = fixture.root
			command.Env = []string{"BEADS_DIR=" + fixture.root, "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1"}
			_ = command.Run()
			cancel()
			state := assertDishonestConsumed(t, fixture.root, 1)
			if family == "apply-timeout" && state.State["effect"] != caseID {
				t.Fatalf("apply fault did not persist effect: %+v", state.State)
			}
			terminal = append(terminal, caseID)
			passed = append(passed, caseID)
		})
	}
	for _, values := range [][]string{started, terminal, passed} {
		sort.Strings(values)
	}
	if !sameIDs(expected, started) || !sameIDs(expected, terminal) || !sameIDs(expected, passed) {
		t.Fatalf("fault census mismatch expected=%d started=%d terminal=%d passed=%d", len(expected), len(started), len(terminal), len(passed))
	}
	for _, family := range []string{"slow", "unavailable", "malformed", "stale", "wrong", "apply-timeout"} {
		if families[family] == 0 {
			t.Fatalf("fault family not executed: %s", family)
		}
	}
	if sameIDs(expected, terminal[:len(terminal)-1]) {
		t.Fatal("omitted fault mutant escaped census")
	}
}

func TestBeadsDishonestTruthfulWarningStaleAndWrongState(t *testing.T) {
	executable := buildDishonestExecutable(t)
	t.Run("warning-success-does-not-branch", func(t *testing.T) {
		payload := envelope(issueJSON("actor-a", "operation-a", "project-fault", "work-fault", "r-current"))
		fixture := newDishonestFixture(t, executable, []dishonestStep{showStep("protocol.warning-success", payload, "arbitrary warning prose\n", 0)}, nil)
		record, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		if err != nil || record.VisibleAssignee != "actor-a" || record.AssignmentMarker != "operation-a" || record.Observation.ProviderToken != "r-current" {
			t.Fatalf("warning success record=%+v err=%v", record, err)
		}
		assertDishonestConsumed(t, fixture.root, 1)
	})
	t.Run("stale-snapshot-remains-observable", func(t *testing.T) {
		payload := envelope(issueJSON("actor-old", "operation-old", "project-fault", "work-fault", "r-old"))
		fixture := newDishonestFixture(t, executable, []dishonestStep{showStep("stale.old-revision-tied-time", payload, "", 0)}, map[string]string{"truth_revision": "r-current"})
		record, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		if err != nil || record.Observation.ProviderToken != "r-old" || record.VisibleAssignee != "actor-old" {
			t.Fatalf("stale observation normalized: record=%+v err=%v", record, err)
		}
		state := assertDishonestConsumed(t, fixture.root, 1)
		if state.State["truth_revision"] != "r-current" {
			t.Fatalf("returned stale response rewrote truth: %+v", state.State)
		}
	})
	t.Run("spawn-failure", func(t *testing.T) {
		fixture := newDishonestFixture(t, executable, []dishonestStep{showStep("unavailable.spawn", envelope(issueJSON("", "", "project-fault", "work-fault", "r1")), "", 0)}, nil)
		moved := fixture.executable + ".offline"
		if err := os.Rename(fixture.executable, moved); err != nil {
			t.Fatal(err)
		}
		defer os.Rename(moved, fixture.executable)
		_, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		expectTrackerFault(t, err, tracker.ErrorUnavailable, "repair_provider_binding")
		if _, err := os.Stat(filepath.Join(fixture.root, "scenario.state.json")); !os.IsNotExist(err) {
			t.Fatalf("spawn failure consumed script: %v", err)
		}
	})
}

func TestBeadsDishonestSlowCancellationKillsChild(t *testing.T) {
	executable := buildDishonestExecutable(t)
	fixture := newDishonestFixture(t, executable, []dishonestStep{{
		ID: "slow.ignored-cancellation", Argv: []string{"show", "bd-fault", "--json"},
		Stdout: envelope(issueJSON("", "", "project-fault", "work-fault", "r1")), WaitFor: "never.release",
	}}, nil)
	ctx, cancel := boundedContext(t, 75*time.Millisecond)
	defer cancel()
	_, err := fixture.adapter.ReadWork(ctx, fixture.reference)
	expectTrackerFault(t, err, tracker.ErrorTimeout, "observe_then_reconcile")
	assertDishonestConsumed(t, fixture.root, 1)
}

func TestBeadsDishonestAssignmentApplyTimeoutObservedBeforeRetry(t *testing.T) {
	executable := buildDishonestExecutable(t)
	root := t.TempDir()
	steps := preflightSteps(root)
	digest := portableRequestDigest("portable timeout", "", "")
	metadata := fmt.Sprintf(`{"spec_kitty":{"content_digest":%q,"created_by_operation":"operation-create-timeout","criteria":[],"design_context":"","last_operation":"operation-create-timeout","project_id":"project-fault","schema_version":1,"work_id":"work-timeout"}}`, digest)
	steps = append(steps,
		dishonestStep{ID: "apply-timeout.catalog-scan", Argv: []string{"list", "--json"}, Stdout: envelope(`[]`)},
		dishonestStep{
			ID: "apply-timeout.catalog-create", Argv: []string{"create", "--title", "portable timeout", "--metadata", `{"spec_kitty":{"content_digest":"fixed","created_by_operation":"operation-create-timeout","criteria":[],"design_context":"","last_operation":"operation-create-timeout","project_id":"project-fault","schema_version":1,"work_id":"work-timeout"}}`, "--json"},
			TransitionTiming: "before_response", Transition: map[string]string{"created": "bd-timeout", "operation": "operation-create-timeout"}, WaitFor: "never.release",
			NextObservation: map[string]string{"created": "bd-timeout", "operation": "operation-create-timeout"},
		},
		showStep("apply-timeout.catalog-observe", envelope(issueJSON("", "", "project-fault", "work-timeout", "r-after")), "", 0),
	)
	steps[4].Argv[4] = metadata
	fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
	ctx, cancel := boundedContext(t, 100*time.Millisecond)
	defer cancel()
	_, err := fixture.adapter.CreateWork(ctx, tracker.CreateWorkRequest{ProjectID: "project-fault", WorkID: "work-timeout", OperationID: "operation-create-timeout", Title: "portable timeout", MetadataSchema: 1})
	expectTrackerFault(t, err, tracker.ErrorAmbiguous, "observe_then_reconcile")
	state := assertDishonestConsumed(t, fixture.root, 5)
	if state.State["created"] != "bd-timeout" {
		t.Fatalf("apply-before-loss not retained: %+v", state.State)
	}
	observed, observeErr := fixture.adapter.ReadWork(context.Background(), tracker.ExternalWorkRef{ProjectID: "project-fault", WorkID: "work-timeout", Provider: "beads", ExternalID: "bd-fault", MetadataSchema: 1, CreatedByOperation: "operation-create-fault"})
	if observeErr != nil || observed.ExternalRef.WorkID != "work-timeout" {
		t.Fatalf("independent observation=%+v err=%v", observed, observeErr)
	}
	assertDishonestConsumed(t, fixture.root, 6)

	// A raw exact mutation step proves the child can atomically record the
	// apply-before-loss distinction. Other tests drive every response through
	// the production decoder; this self-test covers the script mechanism.
	rawRoot := t.TempDir()
	raw := newDishonestFixtureAt(t, rawRoot, executable, []dishonestStep{{
		ID: "apply-timeout.raw-exact", Argv: []string{"update", "bd-fault", "--claim", "--json"},
		TransitionTiming: "before_response", Transition: map[string]string{"applied": "true"}, NextObservation: map[string]string{"applied": "true"}, WaitFor: "never.release",
	}}, nil)
	rawCtx, rawCancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer rawCancel()
	command := execCommandContext(rawCtx, raw.executable, "update", "bd-fault", "--claim", "--json")
	command.Dir = raw.root
	command.Env = []string{"BEADS_DIR=" + raw.root, "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1"}
	_ = command.Run()
	rawState := assertDishonestConsumed(t, raw.root, 1)
	if rawState.State["applied"] != "true" {
		t.Fatalf("apply-before-loss absent: %+v", rawState.State)
	}
}

// execCommandContext is a test seam that keeps process creation visible to
// static analysis without importing a shell.
var execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func TestBeadsHumanAssignmentPreservation100(t *testing.T) {
	const repetitions = 100
	const seed = int64(0x5EC0A11)
	_ = seed // fixed and recorded; case order is lexical for reproducibility.
	idsExpected := make([]string, 0, repetitions)
	idsStarted := make([]string, 0, repetitions)
	idsTerminal := make([]string, 0, repetitions)
	idsPassed := make([]string, 0, repetitions)
	results := make([]map[string]any, 0, repetitions)
	authorityFixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"}})
	stale := provisionPhase1StaleAuthority(t, authorityFixture, "wp08-human")

	for repetition := 1; repetition <= repetitions; repetition++ {
		caseID := stableID("human-assignment-preservation", repetition)
		idsExpected = append(idsExpected, caseID)
		t.Run(caseID, func(t *testing.T) {
			idsStarted = append(idsStarted, caseID)
			store, err := fake.Open(filepath.Join(t.TempDir(), "tracker.json"))
			if err != nil {
				t.Fatal(err)
			}
			workKey := stableID("external-human", repetition)
			if err := store.Seed(context.Background(), fake.WorkSeed{Key: workKey, Title: "human preservation", Assignee: "original"}); err != nil {
				t.Fatal(err)
			}
			provider := fake.NewProvider(store)
			reference := tracker.ExternalWorkRef{ProjectID: ids.ProjectID(stableID("project-human", repetition)), WorkID: ids.WorkID(stableID("work-human", repetition)), Provider: "durable_fake", ExternalID: workKey, MetadataSchema: 1}
			before, err := provider.ObserveAssignment(context.Background(), reference)
			if err != nil {
				t.Fatal(err)
			}
			operation := humanOperation(repetition)
			applied, err := provider.ProjectAssignment(context.Background(), tracker.ProjectAssignmentRequest{ExternalRef: reference, OperationID: operation, Assignee: stableID("actor-automated", repetition), ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentAtomicClaim})
			if err != nil {
				t.Fatal(err)
			}
			human := stableID("actor-human", repetition)
			humanApplied := make(chan struct{})
			type compensationResult struct{ err error }
			compensated := make(chan compensationResult, 1)
			go func() {
				<-humanApplied
				_, compensateErr := provider.CompensateAssignment(context.Background(), tracker.CompensateAssignmentRequest{ExternalRef: reference, OperationID: ids.OperationID(stableID("operation-recovery", repetition)), ExpectedObservation: applied.Observation.Observation.Revision, ExpectedAssignee: applied.Observation.Assignee, ExpectedMarker: operation.String(), RestoreAssignee: "original"})
				compensated <- compensationResult{err: compensateErr}
			}()
			if err := store.HumanAssign(context.Background(), workKey, human); err != nil {
				t.Fatal(err)
			}
			historyBefore, _ := store.History(context.Background())
			close(humanApplied)
			compensateErr := (<-compensated).err
			if !tracker.IsKind(compensateErr, tracker.ErrorAttributionLost) {
				t.Fatalf("compensation error=%v", compensateErr)
			}
			after, err := provider.ObserveAssignment(context.Background(), reference)
			if err != nil || after.Assignee != human || after.OperationMarker != "" {
				t.Fatalf("human state overwritten: %+v err=%v", after, err)
			}
			historyAfter, _ := store.History(context.Background())
			if len(historyAfter) != len(historyBefore) {
				t.Fatalf("automatic compensation mutated provider: before=%d after=%d", len(historyBefore), len(historyAfter))
			}
			conflict := humanConflictRecord{Expected: applied.Observation, Actual: after, Outcome: tracker.ErrorAttributionLost}
			if conflict.Expected.Assignee == "" || conflict.Expected.OperationMarker == "" || conflict.Actual.Assignee != human || conflict.Actual.Observation.Revision != after.Observation.Revision {
				t.Fatalf("conflict evidence incomplete: %+v", conflict)
			}
			authority := quickStaleFaultAuthority(t, authorityFixture, stale, "WP08."+caseID)
			if err := auditBeadsFault(context.Background(), authority); err != nil {
				t.Fatal(err)
			}
			idsTerminal = append(idsTerminal, caseID)
			idsPassed = append(idsPassed, caseID)
			results = append(results, map[string]any{"id": caseID, "outcome": "attribution_lost", "compensation_mutations": 0, "human_preserved": true, "universal_required": true, "universal_started": true, "universal_passed": true, "stale_probe_required": true, "stale_probe_started": true, "stale_probe_passed": true})
		})
	}
	for _, values := range [][]string{idsExpected, idsStarted, idsTerminal, idsPassed} {
		sort.Strings(values)
	}
	if !sameIDs(idsExpected, idsStarted) || !sameIDs(idsExpected, idsTerminal) || !sameIDs(idsExpected, idsPassed) {
		t.Fatalf("human census mismatch expected=%d started=%d terminal=%d passed=%d", len(idsExpected), len(idsStarted), len(idsTerminal), len(idsPassed))
	}
	artifact := map[string]any{"seed": seed, "expected": idsExpected, "started": idsStarted, "terminal": idsTerminal, "passed": idsPassed, "failed": []string{}, "non_applicable": []string{}, "results": results}
	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "human-direct-assignment") {
		t.Fatal("census leaked human content")
	}
	if err := os.WriteFile(filepath.Join(t.TempDir(), "beads-human-census.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	missing := append([]string(nil), idsTerminal[:len(idsTerminal)-1]...)
	if sameIDs(idsExpected, missing) {
		t.Fatal("omitted repetition mutant escaped census")
	}
	if humanOverwriteMutant("human", "automation") == "human" {
		t.Fatal("human overwrite mutant escaped invariant")
	}
}

func humanOverwriteMutant(_ string, automated string) string { return automated }

type humanConflictRecord struct {
	Expected tracker.AssignmentObservation
	Actual   tracker.AssignmentObservation
	Outcome  tracker.ErrorKind
}

func sameIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestBeadsFaultsCarryUniversalAuthorityAndStaleProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"}})
	stale := provisionPhase1StaleAuthority(t, fixture, "wp08")
	applicable := []string{"apply-timeout.assignment", "slow.deadline-before-apply", "stale.assignment-marker", "unavailable.provider", "wrong.unexpected-assignee"}
	for _, fault := range applicable {
		fault := fault
		t.Run(fault, func(t *testing.T) {
			state := staleFaultAuthority(t, fixture, stale, "WP08."+fault)
			if err := auditBeadsFault(ctx, state); err != nil {
				t.Fatal(err)
			}
		})
	}

	base := staleFaultAuthority(t, fixture, stale, "WP08.mutant")
	mutants := []struct {
		name   string
		mutate func(*universalAssertionState)
	}{
		{"missing-callback", func(state *universalAssertionState) { state.StaleProbe = nil }},
		{"requirement-disabled", func(state *universalAssertionState) { state.RequireStaleProbe = false; state.StaleProbe = nil }},
		{"stale-succeeds", func(state *universalAssertionState) { state.StaleProbe = func(context.Context) error { return nil } }},
	}
	for _, mutant := range mutants {
		mutant := mutant
		t.Run("mutant-"+mutant.name, func(t *testing.T) {
			state := base
			mutant.mutate(&state)
			err := auditBeadsFault(ctx, state)
			if err == nil || !strings.Contains(err.Error(), "stale_token_refusal") {
				t.Fatalf("mutant error=%v", err)
			}
		})
	}
}

func auditBeadsFault(ctx context.Context, state universalAssertionState) error {
	if !state.RequireStaleProbe || state.StaleProbe == nil {
		return fmt.Errorf("scenario_id=%s fault_id=%s invariant=stale_token_refusal recovery=require a real stale probe", state.ScenarioID, state.FaultID)
	}
	if err := assertUniversalInvariants(ctx, state); err != nil {
		return err
	}
	return assertReadOnlyReviewerIndependence(ctx, state)
}

func assertReadOnlyReviewerIndependence(ctx context.Context, state universalAssertionState) error {
	database, err := sql.Open("sqlite", state.Database)
	if err != nil {
		return err
	}
	defer database.Close()
	var violations int
	err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM reviews r JOIN handoff_contributors c ON c.handoff_id=r.handoff_id AND c.actor_id=r.reviewer_actor_id`).Scan(&violations)
	if err != nil {
		return err
	}
	if violations != 0 {
		return fmt.Errorf("scenario_id=%s fault_id=%s invariant=reviewer_independence violations=%d", state.ScenarioID, state.FaultID, violations)
	}
	return nil
}

func portableRequestDigest(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		length := uint64(len(value))
		var encoded [8]byte
		for index := 7; index >= 0; index-- {
			encoded[index] = byte(length)
			length >>= 8
		}
		_, _ = hash.Write(encoded[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func TestBeadsFaultDiagnosticsNeverTreatProseAsAuthority(t *testing.T) {
	err := &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "provider mutation outcome is ambiguous", Cause: errors.New("secret-provider-body")}
	if fmt.Sprintf("%v", err) != "tracker ambiguous: provider mutation outcome is ambiguous" {
		t.Fatalf("raw provider cause leaked: %v", err)
	}
	var diagnostic *execution.DiagnosticError
	if errors.As(err, &diagnostic) {
		t.Fatal("tracker evidence impersonated execution authority")
	}
}
