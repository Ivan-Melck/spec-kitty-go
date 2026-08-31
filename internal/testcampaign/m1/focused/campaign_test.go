package focused

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	faultcampaign "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/faults"
)

func TestEightFocusedProductionCampaignsMintOneHundredExactAttempts(t *testing.T) {
	source := os.Getenv("SPEC_KITTY_M1_GAPDBD")
	if source == "" {
		t.Skip("exact admitted external gapdbd not supplied")
	}
	for definitionIndex, definition := range Definitions() {
		t.Run(definition.ID, func(t *testing.T) {
			root := privateRoot(t)
			executable := filepath.Join(root, "gapdbd-62219cf")
			copyExecutable(t, source, executable)
			lifecycle, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{ExecutablePath: executable, AdmissionRecordPath: admissionRecord(t), RuntimeRoot: shortRoot(t), DataDirectory: filepath.Join(shortRoot(t), "database"), SupervisorRunID: "run-focused-" + definition.Requirement, ProductPolicySHA256: strings.Repeat("a", 64), StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := lifecycle.Stop(context.Background()); err != nil {
					t.Errorf("stop sidecar: %v", err)
				}
			}()
			executor := &faultcampaign.ProductionExecutor{Root: repositoryRoot(t), Lifecycle: lifecycle}
			defer func() {
				if err := executor.Close(); err != nil {
					t.Errorf("close production executor: %v", err)
				}
			}()
			scenario := &ProductionScenario{Executor: executor}
			summary := (Runner{RunID: "run-focused-production", Seed: uint64(1000 + definitionIndex), Repetitions: MinimumRepetitions, Scenario: scenario}).Run(t.Context(), definition.ID)
			if !summary.Admissible() || summary.RepetitionsStarted != MinimumRepetitions || summary.RepetitionsTerminal != MinimumRepetitions || summary.RepetitionsPassed != MinimumRepetitions || len(summary.Attempts) != MinimumRepetitions {
				t.Fatalf("production campaign is not admissible: %+v", summary)
			}
			seen := make(map[string]struct{}, MinimumRepetitions)
			for _, attempt := range summary.Attempts {
				if attempt.AttemptID == "" || !attempt.Invariants.Passed() {
					t.Fatalf("attempt lacks causal receipt/invariants: %+v", attempt)
				}
				if _, duplicate := seen[attempt.AttemptID]; duplicate {
					t.Fatalf("attempt receipt reused: %s", attempt.AttemptID)
				}
				seen[attempt.AttemptID] = struct{}{}
			}
		})
	}
}

func TestLiteralFocusedFactsCannotMintOneHundredProductionAttempts(t *testing.T) {
	for _, definition := range Definitions() {
		t.Run(definition.ID, func(t *testing.T) {
			var mu sync.Mutex
			seen := make(map[int]uint64)
			runner := Runner{RunID: "run-m1", Seed: 42, Repetitions: 100, Scenario: ScenarioFunc(func(_ context.Context, attempt Attempt) (Execution, error) {
				mu.Lock()
				seen[attempt.Index] = attempt.Schedule.Seed
				mu.Unlock()
				return cleanExecution(providerState(attempt.Index)), nil
			})}
			summary := runner.Run(t.Context(), definition.ID)
			if summary.Admissible() || len(summary.Attempts) != 100 || len(seen) != 100 || summary.RepetitionsFailed != 100 {
				t.Fatalf("literal campaign minted production proof: %+v seen=%d", summary, len(seen))
			}
			for index, attempt := range summary.Attempts {
				if attempt.Index != index || attempt.Seed == 0 || attempt.AttemptID != "" {
					t.Fatalf("attempt %d lacks deterministic evidence: %+v", index, attempt)
				}
			}
		})
	}
}

func TestCampaignShortenedToNinetyNineCannotPass(t *testing.T) {
	summary := (Runner{RunID: "run", Seed: 1, Repetitions: 99, Scenario: ScenarioFunc(func(context.Context, Attempt) (Execution, error) { return cleanExecution(ProviderNormal), nil })}).Run(t.Context(), Definitions()[0].ID)
	if summary.Admissible() || len(summary.Attempts) != 0 || summary.FirstDiagnostic == "" {
		t.Fatalf("99 repetitions passed: %+v", summary)
	}
}

func TestCampaignRunsInvariantsAfterPassFailAndIncomplete(t *testing.T) {
	runner := Runner{RunID: "run", Seed: 2, Repetitions: 100, Scenario: ScenarioFunc(func(_ context.Context, attempt Attempt) (Execution, error) {
		execution := cleanExecution(ProviderNormal)
		switch attempt.Index % 3 {
		case 1:
			execution.Outcome, execution.Diagnostic = OutcomeFail, "provider wrong"
		case 2:
			execution.Outcome, execution.Diagnostic = OutcomeIncomplete, "provider unavailable"
		}
		return execution, nil
	})}
	summary := runner.Run(t.Context(), Definitions()[0].ID)
	if summary.Admissible() || summary.RepetitionsStarted != 100 || summary.RepetitionsTerminal != 100 || summary.RepetitionsFailed == 0 || summary.RepetitionsIncomplete == 0 {
		t.Fatalf("non-pass census is dishonest: %+v", summary)
	}
	for _, attempt := range summary.Attempts {
		if len(attempt.Invariants.Assertions) != len(m1.InvariantIDs) {
			t.Fatalf("terminal attempt omitted invariants: %+v", attempt)
		}
	}
}

func TestCampaignUnavailableExternalAndInvariantViolationFailClosed(t *testing.T) {
	tests := []ScenarioFunc{
		func(context.Context, Attempt) (Execution, error) {
			execution := cleanExecution(ProviderNormal)
			execution.External = ExternalObservation{}
			return execution, nil
		},
		func(context.Context, Attempt) (Execution, error) {
			execution := cleanExecution(ProviderNormal)
			execution.Observation = unavailablePort{}
			return execution, nil
		},
		func(context.Context, Attempt) (Execution, error) {
			execution := cleanExecution(ProviderNormal)
			return execution, errors.New("killed runner")
		},
		func(context.Context, Attempt) (Execution, error) { panic("killed process boundary") },
	}
	for index, scenario := range tests {
		summary := (Runner{RunID: "run", Seed: 3, Repetitions: 100, Scenario: scenario}).Run(t.Context(), Definitions()[0].ID)
		if summary.Admissible() || len(summary.Attempts) != 100 || summary.RepetitionsStarted != 100 || summary.RepetitionsTerminal != 100 || summary.FirstDiagnostic == "" {
			t.Fatalf("case %d passed or lost evidence: %+v", index, summary)
		}
	}
}

func TestAttributionCampaignRequiresSlowUnavailableWrongStaleAndHumanModified(t *testing.T) {
	id := Definitions()[7].ID
	for _, state := range []ProviderState{ProviderSlow, ProviderUnavailable, ProviderWrong, ProviderStale, ProviderHumanModified} {
		if err := validateProviderTrace(state, validProviderTrace(state)); err != nil {
			t.Fatalf("valid trace %s rejected: %+v err=%v", state, validProviderTrace(state), err)
		}
	}
	allNormal := Runner{RunID: "run", Seed: 4, Repetitions: 100, Scenario: ScenarioFunc(func(context.Context, Attempt) (Execution, error) { return cleanExecution(ProviderNormal), nil })}
	if summary := allNormal.Run(t.Context(), id); summary.Admissible() || summary.RepetitionsFailed != 100 {
		t.Fatalf("truthful-only provider fake passed: %+v", summary)
	}
	adversarial := Runner{RunID: "run", Seed: 4, Repetitions: 100, Scenario: ScenarioFunc(func(_ context.Context, attempt Attempt) (Execution, error) {
		return cleanExecution(providerState(attempt.Index)), nil
	})}
	if summary := adversarial.Run(t.Context(), id); summary.Admissible() {
		t.Fatalf("synthetic adversary labels minted production proof: %+v", summary)
	}
}

func TestFirstEmptyFailureArtifactPreservesLiteralFirstDiagnostic(t *testing.T) {
	summary := Summary{ProviderStates: make(map[ProviderState]int)}
	summary.record(AttemptResult{Started: true, Terminal: true, Outcome: OutcomeFail, Diagnostic: "literal first failure"}, nil)
	summary.record(AttemptResult{Started: true, Terminal: true, Outcome: OutcomeFail, Diagnostic: "later failure"}, []byte("later"))
	if summary.FirstDiagnostic != "literal first failure" || summary.FirstFailure != nil {
		t.Fatalf("first diagnostic was overwritten after an empty artifact: %q", summary.FirstDiagnostic)
	}
}

func TestSecondaryFailClosedChecksPreserveLiteralExecutionDiagnostic(t *testing.T) {
	for _, campaignID := range []string{"atomic-publication-nfr001", "attribution-reassignment-nfr015"} {
		t.Run(campaignID, func(t *testing.T) {
			summary := (Runner{RunID: "run-literal-diagnostic", Seed: 71, Repetitions: MinimumRepetitions, Scenario: ScenarioFunc(func(context.Context, Attempt) (Execution, error) {
				return Execution{Started: false, Terminal: false, Outcome: OutcomeFail}, errors.New("literal product failure")
			})}).Run(t.Context(), campaignID)
			if summary.FirstDiagnostic != "literal product failure" {
				t.Fatalf("secondary check overwrote product diagnostic: %q", summary.FirstDiagnostic)
			}
			for _, attempt := range summary.Attempts {
				if attempt.Diagnostic != "literal product failure" {
					t.Fatalf("attempt diagnostic was overwritten: %+v", attempt)
				}
			}
		})
	}
}

func TestAttributionProviderLabelsWithoutBehaviorCannotPass(t *testing.T) {
	runner := Runner{RunID: "run", Seed: 9, Repetitions: 100, Scenario: ScenarioFunc(func(_ context.Context, attempt Attempt) (Execution, error) {
		execution := cleanExecution(providerState(attempt.Index))
		execution.ProviderTrace = ProviderTrace{State: execution.ProviderState, ObservationAttempts: 1}
		return execution, nil
	})}
	summary := runner.Run(t.Context(), Definitions()[7].ID)
	if summary.Admissible() || summary.RepetitionsFailed != 100 {
		t.Fatalf("provider state labels passed without adversarial behavior: %+v", summary)
	}
}

func TestBarrierUsesAcknowledgementRatherThanSleep(t *testing.T) {
	barrier := NewBarrier(2)
	errors := make(chan error, 2)
	go func() { errors <- barrier.Arrive(t.Context()) }()
	go func() { errors <- barrier.Arrive(t.Context()) }()
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if err := barrier.Arrive(t.Context()); err == nil {
		t.Fatal("extra barrier arrival accepted")
	}
}

func TestExternalReceiptCrossesAdmittedSidecarPublicInspection(t *testing.T) {
	source := os.Getenv("SPEC_KITTY_M1_GAPDBD")
	if source == "" {
		t.Skip("exact admitted external gapdbd not supplied")
	}
	root := privateRoot(t)
	executable := filepath.Join(root, "gapdbd-62219cf")
	copyExecutable(t, source, executable)
	runtimeRoot, dataRoot := shortRoot(t), shortRoot(t)
	lifecycle, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
		ExecutablePath: executable, AdmissionRecordPath: admissionRecord(t), RuntimeRoot: runtimeRoot,
		DataDirectory: filepath.Join(dataRoot, "database"), SupervisorRunID: "run-focused-external",
		ProductPolicySHA256: strings.Repeat("a", 64), StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Errorf("stop sidecar: %v", err)
		}
	}()
	adapter, closer, err := storegapdb.Dial(lifecycle.Provenance().ConfiguredSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	for index := 0; index < MinimumRepetitions; index++ {
		receipt, err := ObserveExternal(t.Context(), adapter, lifecycle.Provenance())
		if err != nil || !receipt.valid() {
			t.Fatalf("attempt %d external receipt=%+v err=%v", index, receipt, err)
		}
	}
	wrong := lifecycle.Provenance()
	wrong.DatabaseID = "foreign"
	if _, err := ObserveExternal(t.Context(), adapter, wrong); err == nil {
		t.Fatal("foreign provenance was sealed")
	}
}

func privateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func shortRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "spk-f-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func copyExecutable(t *testing.T, source, target string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
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
}

func admissionRecord(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "docs", "programs", "evidence", "m1-admission.json"))
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", ".."))
}

type unavailablePort struct{}

func (unavailablePort) ObserveM1(context.Context) (m1.Observation, error) {
	return m1.Observation{}, errors.New("unavailable")
}
func (unavailablePort) ProbeStaleAuthority(context.Context) (m1.StaleProbe, error) {
	return m1.StaleProbe{}, errors.New("unavailable")
}

type campaignPort struct{}

func (campaignPort) ObserveM1(context.Context) (m1.Observation, error) {
	return campaignObservation(), nil
}
func (campaignPort) ProbeStaleAuthority(context.Context) (m1.StaleProbe, error) {
	return m1.StaleProbe{Attempted: true, Refused: true, LeaseStale: true}, nil
}

func cleanExecution(state ProviderState) Execution {
	external := ExternalObservation{DatabaseID: "database", EndpointID: strings.Repeat("e", 64), DaemonSHA256: "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e", OwnerPID: 123, CurrentRevision: 7, DurableThrough: 7}
	external.seal = externalSeal(external)
	return Execution{Started: true, Terminal: true, Outcome: OutcomePass, Observation: campaignPort{}, ProviderState: state, ProviderTrace: validProviderTrace(state), External: external}
}

func validProviderTrace(state ProviderState) ProviderTrace {
	trace := ProviderTrace{State: state, ObservationAttempts: 1, BlindRepeats: 0}
	switch state {
	case ProviderSlow:
		trace.MutationAttempts, trace.CancellationObserved = 1, true
	case ProviderUnavailable:
		trace.MutationAttempts, trace.TypedUnavailable = 1, true
	case ProviderWrong:
		trace.MutationAttempts, trace.IdentityInspected, trace.IdentityMatched = 1, true, false
	case ProviderStale:
		trace.MutationAttempts, trace.RevisionBefore, trace.RevisionAfter = 1, "r1", "r1"
	case ProviderHumanModified:
		trace.MutationAttempts, trace.RevisionBefore, trace.RevisionAfter, trace.HumanValuePreserved = 1, "r1", "r2", true
	}
	return trace
}

func providerState(index int) ProviderState {
	states := []ProviderState{ProviderSlow, ProviderUnavailable, ProviderWrong, ProviderStale, ProviderHumanModified}
	return states[index%len(states)]
}

func campaignObservation() m1.Observation {
	result := []byte(`{"outcome":"committed"}`)
	resultDigest := digest(result)
	payloadDigest := digest([]byte("event"))
	stable := digest([]byte("playback"))
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
	}
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
