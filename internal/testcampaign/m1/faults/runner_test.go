package faults

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
)

type cleanPort struct{}

func (cleanPort) ObserveM1(context.Context) (m1.Observation, error) { return validObservation(), nil }
func (cleanPort) ProbeStaleAuthority(context.Context) (m1.StaleProbe, error) {
	return m1.StaleProbe{Attempted: true, Refused: true, LeaseStale: true, EpochStale: true}, nil
}

func TestLiteralFaultFactsAndHooksCannotMintPassingRecord(t *testing.T) {
	for _, definition := range Definitions() {
		runner := Runner{RunID: "run-m1", Seed: 1, Executor: ExecutorFunc(func(ctx context.Context, attempt Attempt) (Execution, error) {
			_ = attempt.Hook.Reach(ctx, definition.ID)
			return Execution{Facts: validFacts(definition.ID), Observation: cleanPort{}, Artifacts: validArtifacts()}, nil
		})}
		if record := runner.Run(t.Context(), definition.ID); record.Outcome == OutcomePass || !record.Terminal || len(record.Invariants.Assertions) != len(m1.InvariantIDs) {
			t.Fatalf("literal fault fixture minted pass for %s: %+v", definition.ID, record)
		}
	}
}

func TestProductionFaultRecordsAreMintedByExactExecutionReceipts(t *testing.T) {
	lifecycle := startFaultLifecycle(t)
	root := faultModuleRoot(t)
	executor := &ProductionExecutor{Root: root, Lifecycle: lifecycle}
	t.Cleanup(func() { _ = executor.Close() })
	seenAttempts := make(map[string]struct{}, len(Definitions()))
	for index, definition := range Definitions() {
		record := (Runner{RunID: "run-m1-production", Seed: uint64(index + 1), Executor: executor}).Run(t.Context(), definition.ID)
		if !record.Started || !record.Terminal || record.Outcome != OutcomePass || record.Cut != definition.Cut || !record.Invariants.Passed() || !record.Source.Valid() || record.Receipt.seal == [32]byte{} {
			t.Fatalf("production fault did not mint an admissible record: %+v", record)
		}
		if record.Receipt.Source != record.Source || record.Receipt.FactSHA256 != factsDigest(record.Facts) || record.Receipt.InvariantSHA256 != invariantDigest(record.Invariants) {
			t.Fatalf("record/receipt binding drifted: %+v", record)
		}
		if _, duplicate := seenAttempts[record.Receipt.AttemptID]; duplicate {
			t.Fatalf("production receipt was reused: %s", record.Receipt.AttemptID)
		}
		seenAttempts[record.Receipt.AttemptID] = struct{}{}
	}
}

func faultModuleRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("module root unavailable")
		}
		directory = parent
	}
}

func TestFaultRunnerRejectsUnboundSourceEvidence(t *testing.T) {
	lifecycle := startFaultLifecycle(t)
	base := &ProductionExecutor{Root: faultModuleRoot(t), Lifecycle: lifecycle}
	t.Cleanup(func() { _ = base.Close() })
	mutants := []func(*SourceBinding){
		func(s *SourceBinding) { s.Commit = "not-a-commit" },
		func(s *SourceBinding) { s.Tree = "" },
		func(s *SourceBinding) { s.AdmittedTupleSHA256 = strings.Repeat("0", 64) },
		func(s *SourceBinding) { s.SidecarProvenanceSHA256 = "bad" },
	}
	for index, mutate := range mutants {
		runner := Runner{RunID: "run-source-mutant", Seed: uint64(index + 100), Executor: ExecutorFunc(func(ctx context.Context, attempt Attempt) (Execution, error) {
			execution, err := base.ExecuteFault(ctx, attempt)
			mutate(&execution.Source)
			return execution, err
		})}
		if result := runner.Run(t.Context(), "GJ-01"); result.Outcome != OutcomeFail || result.Diagnostic == "" {
			t.Fatalf("source mutant %d passed: %+v", index, result)
		}
	}
}

func TestFaultRunnerWrongOrderMissingMultipleAndFakeTimeoutFail(t *testing.T) {
	tests := []struct {
		name string
		id   string
		exec ExecutorFunc
	}{
		{"wrong cut", "GJ-03", func(ctx context.Context, a Attempt) (Execution, error) {
			_ = a.Hook.Reach(ctx, "GJ-04")
			return cleanExecution("GJ-03"), nil
		}},
		{"missing cut", "GJ-01", func(context.Context, Attempt) (Execution, error) { return cleanExecution("GJ-01"), nil }},
		{"multiple cut", "GJ-05", func(ctx context.Context, a Attempt) (Execution, error) {
			_ = a.Hook.Reach(ctx, "GJ-05")
			_ = a.Hook.Reach(ctx, "GJ-05")
			return cleanExecution("GJ-05"), nil
		}},
		{"fake timeout", "GJ-02", func(ctx context.Context, a Attempt) (Execution, error) {
			_ = a.Hook.Reach(ctx, "GJ-02")
			execution := cleanExecution("GJ-02")
			execution.Facts.ProxyConsumedCompleteResponse = false
			return execution, nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := cleanRunner(1, test.exec).Run(t.Context(), test.id)
			if record.Outcome != OutcomeFail || !record.Terminal || record.Diagnostic == "" {
				t.Fatalf("invalid cut passed or lacked evidence: %+v", record)
			}
		})
	}
}

func TestFaultRunnerFailureIncompleteAndPanicRemainTerminalAndSchemaShaped(t *testing.T) {
	tests := []struct {
		name string
		exec ExecutorFunc
		want Outcome
	}{
		{"failure", func(ctx context.Context, a Attempt) (Execution, error) {
			_ = a.Hook.Reach(ctx, a.Definition.ID)
			return cleanExecution(a.Definition.ID), errors.New("provider wrong")
		}, OutcomeFail},
		{"unavailable", func(ctx context.Context, a Attempt) (Execution, error) {
			_ = a.Hook.Reach(ctx, a.Definition.ID)
			return cleanExecution(a.Definition.ID), &IncompleteError{Reason: "daemon unavailable"}
		}, OutcomeIncomplete},
		{"killed runner", func(context.Context, Attempt) (Execution, error) { panic("killed") }, OutcomeFail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := cleanRunner(7, test.exec).Run(t.Context(), "GJ-05")
			if !record.Started || !record.Terminal || record.Outcome != test.want || record.FaultID == "" || record.RunID == "" || record.Cut == "" || record.Diagnostic == "" {
				t.Fatalf("non-pass evidence malformed: %+v", record)
			}
			if len(record.Invariants.Assertions) != len(m1.InvariantIDs) {
				t.Fatalf("terminal path omitted universal invariants: %+v", record.Invariants)
			}
		})
	}
}

func TestFaultRunnerEarlyTerminalPathsCarryEightFailedInvariants(t *testing.T) {
	for _, runnerAndID := range []struct {
		runner Runner
		id     string
	}{
		{Runner{RunID: "run", Executor: ExecutorFunc(func(context.Context, Attempt) (Execution, error) { return Execution{}, nil })}, "unknown"},
		{Runner{RunID: "run"}, "GJ-01"},
		{Runner{Executor: ExecutorFunc(func(context.Context, Attempt) (Execution, error) { return Execution{}, nil })}, "GJ-01"},
	} {
		record := runnerAndID.runner.Run(t.Context(), runnerAndID.id)
		if !record.Started || !record.Terminal || record.Outcome == OutcomePass || len(record.Invariants.Assertions) != 8 {
			t.Fatalf("early terminal evidence malformed: %+v", record)
		}
		for _, assertion := range record.Invariants.Assertions {
			if assertion.Passed {
				t.Fatalf("uninspectable early invariant passed: %+v", record.Invariants)
			}
		}
	}
}

func TestFaultRunnerInvariantViolationCannotPass(t *testing.T) {
	runner := cleanRunner(1, func(ctx context.Context, a Attempt) (Execution, error) {
		_ = a.Hook.Reach(ctx, a.Definition.ID)
		execution := cleanExecution(a.Definition.ID)
		execution.Observation = badPort{}
		return execution, nil
	})
	record := runner.Run(t.Context(), "GJ-01")
	if record.Outcome != OutcomeFail || record.Invariants.Passed() {
		t.Fatalf("invariant violation passed: %+v", record)
	}
}

func TestFocusedSemanticCensusRejectsOmissionExtraAndRenameForEveryCampaign(t *testing.T) {
	expected := map[string][]string{
		"atomic-publication-nfr001":        {"gj01_cut_once", "gj01_no_partial_batch", "gj01_preexisting_preserved", "gj02_cut_once", "gj02_complete_response", "gj02_durable_acceptance", "gj02_one_operation_result", "schedule_consumed"},
		"lost-response-idempotency-nfr002": {"changed_digest_no_publication", "changed_digest_refused", "equal_retry_durable_acceptance", "equal_retry_one_operation", "equal_retry_same_revision", "exact_proxy_source", "schedule_consumed", "zero_blind_effect_repeats"},
		"concurrent-claims-nfr003":         {"both_items_progress", "distinct_authorities", "participant_one", "participant_zero"},
		"competing-integrations-nfr004":    {"both_participants", "git_target_moved_once", "one_candidate_publication", "one_target_authority"},
		"restore-playback-nfr005":          {"digest_equal", "history_materialized", "playback_zero_io", "restore_ready", "schedule_consumed"},
		"restart-epoch-nfr007":             {"current_epoch_accepted", "fresh_owner_process", "fresh_private_endpoint", "fresh_runtime_instance", "new_epoch_unique", "predecessor_refused", "ready_new_lifetime", "restart_mode_observed", "same_durable_database", "schedule_consumed"},
		"zombie-isolation-nfr008":          {"fresh_content_unchanged", "fresh_head_unchanged", "fresh_index_unchanged", "fresh_tree_unchanged", "old_workspace_posix_mutated", "replacement_lease_differs", "replacement_workspace_differs", "schedule_consumed", "stale_authority_refused"},
		"attribution-reassignment-nfr015":  {"effect_intent_durable", "one_typed_attempt", "provider_state_observed", "schedule_consumed", "zero_blind_repeats"},
	}
	if len(focusedFactCensus) != len(expected) {
		t.Fatalf("campaign semantic census size=%d want=%d", len(focusedFactCensus), len(expected))
	}
	for campaignID, required := range expected {
		actual, exists := focusedFactCensus[campaignID]
		if !exists || strings.Join(actual, "\x00") != strings.Join(required, "\x00") {
			t.Fatalf("campaign %s semantic census drifted: got=%v want=%v", campaignID, actual, required)
		}
		t.Run(campaignID, func(t *testing.T) {
			clean := make(map[string]bool, len(required))
			for _, name := range required {
				clean[name] = true
			}
			if err := validateFocusedFactCensus(campaignID, clean); err != nil {
				t.Fatalf("exact census rejected: %v", err)
			}
			for _, mutation := range []func(map[string]bool){
				func(facts map[string]bool) { delete(facts, required[0]) },
				func(facts map[string]bool) { facts["unexpected"] = true },
				func(facts map[string]bool) { delete(facts, required[0]); facts[required[0]+"-renamed"] = true },
				func(facts map[string]bool) { facts[required[0]] = false },
			} {
				defect := make(map[string]bool, len(clean)+1)
				for name, passed := range clean {
					defect[name] = passed
				}
				mutation(defect)
				if err := validateFocusedFactCensus(campaignID, defect); err == nil {
					t.Fatalf("semantic census mutant survived: %v", defect)
				}
			}
		})
	}
	if err := validateFocusedFactCensus("renamed-campaign", map[string]bool{}); err == nil {
		t.Fatal("unknown campaign minted a semantic census")
	}
}

func TestRestartTwice(t *testing.T) {
	lifecycle := startFaultLifecycle(t)
	executor := &ProductionExecutor{Root: faultModuleRoot(t), Lifecycle: lifecycle}
	t.Cleanup(func() {
		_ = executor.Close()
		if executor.Lifecycle != nil {
			_ = executor.Lifecycle.Stop(context.Background())
		}
	})
	for index := 0; index < 2; index++ {
		barrier := newTestFocusedBarrier(2)
		attemptID := fmt.Sprintf("restart-regression-%d", index)
		execution, err := executor.ExecuteFocused(t.Context(), FocusedInput{RunID: "run-restart-regression", CampaignID: "restart-epoch-nfr007", AttemptID: attemptID, Index: index, Seed: uint64(index + 1), Order: [2]int{index % 2, 1 - index%2}, Barrier: barrier})
		if err != nil {
			t.Fatalf("restart %d: %v", index, err)
		}
		if err := validateFocusedFactCensus("restart-epoch-nfr007", execution.Facts); err != nil {
			t.Fatalf("restart %d fact census: %v facts=%v", index, err, execution.Facts)
		}
		if !m1.AssertUniversal(t.Context(), execution.Observation).Passed() {
			t.Fatalf("restart %d universal observation failed", index)
		}
	}
}

type testFocusedBarrier struct {
	mu        sync.Mutex
	remaining int
	release   chan struct{}
}

func newTestFocusedBarrier(participants int) *testFocusedBarrier {
	return &testFocusedBarrier{remaining: participants, release: make(chan struct{})}
}

func (b *testFocusedBarrier) Arrive(ctx context.Context) error {
	b.mu.Lock()
	b.remaining--
	if b.remaining == 0 {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
		return nil
	}
}

type badPort struct{}

func (badPort) ObserveM1(context.Context) (m1.Observation, error) {
	return m1.Observation{}, errors.New("inspection violated")
}
func (badPort) ProbeStaleAuthority(context.Context) (m1.StaleProbe, error) {
	return m1.StaleProbe{}, errors.New("probe unavailable")
}

func cleanRunner(seed uint64, executor ExecutorFunc) Runner {
	return Runner{RunID: "run-m1", Seed: seed, Executor: executor}
}

func startFaultLifecycle(t *testing.T) *sidecargapdb.Lifecycle {
	t.Helper()
	source := os.Getenv("SPEC_KITTY_M1_GAPDBD")
	if source == "" {
		t.Skip("exact admitted external gapdbd not supplied")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "gapdbd-62219cf")
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(executable, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	runtimeRoot, dataRoot := t.TempDir(), t.TempDir()
	if err := os.Chmod(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
		ExecutablePath: executable, AdmissionRecordPath: faultAdmissionRecord(t), RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(dataRoot, "database"),
		SupervisorRunID: "run-fault-production", ProductPolicySHA256: strings.Repeat("a", 64), StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Errorf("stop sidecar: %v", err)
		}
	})
	return lifecycle
}

func faultAdmissionRecord(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "docs", "programs", "evidence", "m1-admission.json"))
}

func cleanExecution(id string) Execution {
	return Execution{Facts: validFacts(id), Observation: cleanPort{}, Artifacts: validArtifacts()}
}

func validArtifacts() []Artifact {
	return []Artifact{{Kind: "fault-log", SHA256: strings.Repeat("a", 64), SizeBytes: 128}}
}

func validFacts(id string) Facts {
	switch id {
	case "GJ-01":
		return Facts{PreexistingBefore: map[string]ProductFact{"existing": {Revision: 7, Digest: strings.Repeat("a", 64)}}, PreexistingAfter: map[string]ProductFact{"existing": {Revision: 7, Digest: strings.Repeat("a", 64)}}}
	case "GJ-02":
		return Facts{OperationRecords: 1, OperationResults: 1, DurableAccepted: true, DaemonResponseComplete: true, ProxyConsumedCompleteResponse: true, ProxyCut: &sidecargapdb.ResponseDropCut{FaultID: "GJ-02", Cut: "after_durable_acceptance_before_client_response", RequestID: "operation", Operation: "batch", DatabaseID: "database", Revision: 7, MutationCount: 3, DaemonSocket: "/private/daemon.sock", ProductSocket: "/private/product.sock", DaemonPID: 11, ProxyPID: 12, SourceCommit: strings.Repeat("a", 40), ProxySHA256: strings.Repeat("b", 64), ObservedAt: time.Unix(1, 0).UTC()}}
	case "GJ-03":
		return Facts{PhysicalWorkspaceCreated: true, PhysicalWorkspaceID: "W1-orphan", RecoveredWorkspaceID: "W3-fresh", DurableWorkspaceCount: 1}
	case "GJ-04":
		return Facts{PhysicalWorkspaceCreated: true, PhysicalWorkspaceID: "W1", DurableBinding: true, CapsuleReconstructed: true, CapsuleBindingMatches: true, RecoveredWorkspaceID: "W1", DurableWorkspaceCount: 1}
	case "GJ-05":
		return Facts{EffectIntentDurable: true, ProviderObservationDurable: true, ProviderDispatches: 1, AttributableOutcomes: 1}
	default:
		return Facts{}
	}
}

func validObservation() m1.Observation {
	// Reuse a deliberately small but fully inspectable public-fact observation.
	result := []byte(`{"outcome":"committed"}`)
	resultDigest := sha256hex(result)
	payloadDigest := sha256hex([]byte("event"))
	stable := sha256hex([]byte("playback"))
	return m1.Observation{
		Capabilities: m1.CapabilityObservation{Authority: true, Workspace: true, Review: true, Integration: true, Effects: true},
		Journal:      m1.JournalObservation{Inspectable: true, Operations: []m1.JournalOperation{{OperationID: "op", RequestDigest: payloadDigest, ResultDigest: resultDigest, RecordRevision: 1, EventCount: 1, Terminal: true, CanonicalResult: result, ObservedHeadKeys: []string{"authority/work"}, Events: []m1.JournalEvent{{EventID: "event", Index: 0, Count: 1, RecordRevision: 1, AggregateKey: "authority/work", LogicalVersion: 1, PayloadDigest: payloadDigest}}}}, Heads: []m1.JournalHead{{AggregateKey: "authority/work", LogicalVersion: 1, RecordRevision: 1, LastEventID: "event", PayloadDigest: payloadDigest}}},
		Authorities:  []m1.AuthorityObservation{{WorkRevisionID: "wr", WorkID: "work", ActorID: "actor", ProcessID: "process", LeaseID: "lease", RuntimeEpochID: "epoch", Generation: 1, FenceToken: 1, Current: true}},
		Workspaces:   []m1.WorkspaceObservation{{WorkspaceID: "W1", WorkRevisionID: "wr", WorkID: "work", LeaseID: "lease", RuntimeEpochID: "epoch", Generation: 1, CanonicalPath: "/repo/W1", OwnedBranch: "lane", Status: "active-bound", PrimaryPath: "/repo", Registered: true}},
		Epoch:        m1.EpochObservation{Inspectable: true, Current: "epoch", EveryCurrentUses: true},
		Reviews:      []m1.ReviewObservation{{CandidateID: "candidate", ContributorIDs: []string{"actor"}, ReviewerActorID: "reviewer", CandidateImmutable: true}},
		Integration:  []m1.IntegrationObservation{{RepositoryID: "repo", TargetRef: "main", ActiveAuthorities: 1, CandidateID: "candidate"}},
		Effects:      []m1.EffectObservation{{EffectID: "effect", OperationID: "effect-op", ExpectedRevision: "r1", AppliedRevision: "r2", ExpectedMarker: "m1", AppliedMarker: "m2", Dispatches: 1}},
		Playback:     m1.PlaybackObservation{Inspectable: true, RestoreDigest: stable, PlaybackDigest: stable, StableCanonical: true},
		Primary: m1.PrimaryObservation{Inspectable: true,
			Before: m1.GitSnapshotObservation{Head: strings.Repeat("a", 40), IndexDigest: strings.Repeat("b", 64), TreeDigest: strings.Repeat("c", 64)},
			After:  m1.GitSnapshotObservation{Head: strings.Repeat("a", 40), IndexDigest: strings.Repeat("b", 64), TreeDigest: strings.Repeat("c", 64)}},
	}
}

func sha256hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
