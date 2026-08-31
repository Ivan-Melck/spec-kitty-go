package faults

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	productfaults "github.com/spec-kitty/go-kitty3/internal/faults"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/mutants"
)

type cleanPort struct{}

func (cleanPort) ObserveM1(context.Context) (m1.Observation, error) { return validObservation(), nil }
func (cleanPort) ProbeStaleAuthority(context.Context) (m1.StaleProbe, error) {
	return m1.StaleProbe{Attempted: true, Refused: true, EpochStale: true}, nil
}

func TestFaultRecordSemanticsRequireExactFiveCutsOnceAndTerminal(t *testing.T) {
	for index, definition := range Definitions() {
		t.Run(definition.ID, func(t *testing.T) {
			runner := cleanRunner(uint64(index+1), func(ctx context.Context, attempt Attempt) (Execution, error) {
				if err := attempt.Hook.Reach(ctx, definition.ID); !errors.Is(err, productfaults.ErrCrashRequested) {
					t.Fatalf("production hook acknowledgement=%v", err)
				}
				return Execution{Facts: validFacts(definition.ID), Observation: cleanPort{}, Artifacts: validArtifacts()}, nil
			})
			record := runner.Run(t.Context(), definition.ID)
			if !record.Started || !record.Terminal || record.Outcome != OutcomePass || record.Cut != definition.Cut || !record.Invariants.Passed() {
				t.Fatalf("fault did not pass honestly: %+v", record)
			}
		})
	}
}

func TestFiveFaultCutsHaveExecutableProductionGuards(t *testing.T) {
	root := faultModuleRoot(t)
	guards := []mutants.Entry{
		{ID: "GJ-01", Package: "./internal/controljournal/application", Test: "TestGJ01PreservesNonemptyCensusExactly", Guard: "application pre-publication"},
		{ID: "GJ-02", Package: "./internal/controljournal/application", Test: "TestReconcileRealGJ02ResponseDrop", Guard: "external response-drop proxy"},
		{ID: "GJ-03", Package: "./internal/provingground", Test: "TestGJ03OrphanHasNoAuthorityAndCannotBePromoted", Guard: "workspace physical-before-binding"},
		{ID: "GJ-04", Package: "./internal/provingground", Test: "TestGJ04RetryReturnsSameBindingWithoutDuplicateWorkspace", Guard: "binding-before-capsule"},
		{ID: "GJ-05", Package: "./internal/provingground", Test: "TestEffectGJ05LeavesDiscoverableIntentAndRecoveryObservesWithoutRepeat", Guard: "effect intent-before-observation"},
	}
	executor := mutants.GoTestExecutor{Root: root}
	for _, guard := range guards {
		if guard.ID == "GJ-02" && os.Getenv("SPEC_KITTY_M1_GAPDBD") == "" {
			continue // exact external gate runs when the admitted artifact is supplied
		}
		observation, err := executor.ExecuteGuard(t.Context(), guard)
		if err != nil || !observation.NamedTestStarted || !observation.NamedTestPassed {
			t.Fatalf("production fault guard %s did not execute: observation=%+v err=%v", guard.ID, observation, err)
		}
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
	base := cleanRunner(1, func(ctx context.Context, attempt Attempt) (Execution, error) {
		_ = attempt.Hook.Reach(ctx, attempt.Definition.ID)
		return cleanExecution(attempt.Definition.ID), nil
	})
	mutants := []func(*SourceBinding){
		func(s *SourceBinding) { s.Commit = "not-a-commit" },
		func(s *SourceBinding) { s.Tree = "" },
		func(s *SourceBinding) { s.AdmittedTupleSHA256 = strings.Repeat("0", 64) },
		func(s *SourceBinding) { s.SidecarProvenanceSHA256 = "bad" },
	}
	for index, mutate := range mutants {
		runner := base
		mutate(&runner.Source)
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
			if test.name != "killed runner" && len(record.Invariants.Assertions) != len(m1.InvariantIDs) {
				t.Fatalf("terminal path omitted universal invariants: %+v", record.Invariants)
			}
		})
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

type badPort struct{}

func (badPort) ObserveM1(context.Context) (m1.Observation, error) {
	return m1.Observation{}, errors.New("inspection violated")
}
func (badPort) ProbeStaleAuthority(context.Context) (m1.StaleProbe, error) {
	return m1.StaleProbe{}, errors.New("probe unavailable")
}

func cleanRunner(seed uint64, executor ExecutorFunc) Runner {
	return Runner{RunID: "run-m1", Seed: seed, Source: SourceBinding{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), AdmittedTupleSHA256: "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d", SidecarProvenanceSHA256: strings.Repeat("d", 64)}, Executor: executor}
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
	}
}

func sha256hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
