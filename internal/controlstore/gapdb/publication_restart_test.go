package gapdb_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestProductionPublicationRecoveryAcrossOwnerRestartCuts(t *testing.T) {
	for _, cut := range []string{"restart_before_reconciliation", "restart_after_reconciliation"} {
		t.Run(cut, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "spk-cut-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			runtimeRoot := filepath.Join(root, "runtime")
			if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			repository := gapdbRepositoryRoot(t)
			admissionPath := filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-admission.json")
			var admission sidecargapdb.ExecutableAdmission
			if os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE") != "" {
				admissionPath = filepath.Join(repository, "docs", "programs", "evidence", "m2-gapdb-qualification-candidate.json")
				admission, err = sidecargapdb.VerifyM2QualificationCandidateAdmission(admissionPath)
			} else {
				admission, err = sidecargapdb.VerifyAssertionExtensionAdoption(
					filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-adoption.json"), admissionPath,
				)
			}
			if err != nil {
				t.Fatal(err)
			}
			owner, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
				ExecutablePath:      publicationCutExecutable(t),
				AdmissionRecordPath: admissionPath,
				Admission:           admission,
				RuntimeRoot:         runtimeRoot, DataDirectory: filepath.Join(root, "authority"),
				SupervisorRunID: "publication-cut-owner-1", ProductPolicySHA256: strings.Repeat("e", 64),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = owner.Stop(t.Context()) })

			plan := publicationCutWorkgraphPlan(t)
			productSocket := filepath.Join(root, "publication-cut.sock")
			proxy, err := sidecargapdb.StartResponseDropProxy(sidecargapdb.ProxyConfig{
				ProductSocket: productSocket, DaemonSocket: owner.Provenance().ConnectedSocket,
				ExpectedDaemon: owner.Provenance().Process, ExpectedDatabaseID: owner.Provenance().DatabaseID,
				SelectedRequestID: string(plan.PublicationID()), SourceCommit: strings.Repeat("a", 40), ReadTimeout: 2 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			adapter, closer, err := storegapdb.Dial(productSocket, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			coordinator := publicationCutCoordinator(t, adapter)
			if _, err := coordinator.Publish(t.Context(), plan); !storegapdb.IsKind(err, storegapdb.OutcomeAmbiguous) || coordinator.UnresolvedCount() != 1 {
				t.Fatalf("durable response cut was not retained as ambiguous: unresolved=%d err=%v", coordinator.UnresolvedCount(), err)
			}
			// The response-drop cut deliberately destroys the selected transport.
			// Reconciliation is a fresh read attempt, never a continuation on the
			// connection whose delivery outcome is unknown.
			if err := closer.Close(); err != nil {
				t.Fatal(err)
			}
			adapter, closer, err = storegapdb.Dial(owner.Provenance().ConnectedSocket, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}

			var committedRevision uint64
			if cut == "restart_after_reconciliation" {
				observation, err := adapter.ReconcileCommitV2(t.Context(), plan)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := coordinator.ReconcileComplete(plan, observation); err != nil {
					t.Fatal(err)
				}
				committedRevision = observation.Revision()
			}
			if err := closer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := proxy.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, err := owner.RestartOwned(t.Context(), sidecargapdb.RestartClean, "publication-cut-owner-2")
			if err != nil {
				t.Fatal(err)
			}
			owner = restarted
			freshAdapter, freshCloser, err := storegapdb.Dial(restarted.Provenance().ConnectedSocket, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer freshCloser.Close()
			before, err := freshAdapter.CaptureRevisionV2(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if committedRevision == 0 {
				freshCoordinator := publicationCutCoordinator(t, freshAdapter)
				ack, err := freshCoordinator.Publish(t.Context(), plan)
				if err != nil || ack.ProviderRevision() != before.CurrentRevision() || freshCoordinator.UnresolvedCount() != 0 {
					t.Fatalf("restart-before reconciliation did not recover exact publication: before=%d ack=%d unresolved=%d err=%v", before.CurrentRevision(), ack.ProviderRevision(), freshCoordinator.UnresolvedCount(), err)
				}
			} else {
				observation, err := freshAdapter.ReconcileCommitV2(t.Context(), plan)
				if err != nil || observation.Revision() != committedRevision || observation.Revision() != before.CurrentRevision() {
					t.Fatalf("restart-after reconciliation changed publication: before=%d committed=%d observed=%d err=%v", before.CurrentRevision(), committedRevision, observation.Revision(), err)
				}
			}
			after, err := freshAdapter.CaptureRevisionV2(t.Context())
			if err != nil || after.CurrentRevision() != before.CurrentRevision() {
				t.Fatalf("restart recovery duplicated durable mutation: before=%d after=%d err=%v", before.CurrentRevision(), after.CurrentRevision(), err)
			}
		})
	}
}

func publicationCutWorkgraphPlan(t *testing.T) journalv2.SealedCommitPlanV2 {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{
		ProjectID: "project-publication-cut", WorkID: "work-publication-cut", Title: "Qualify publication restart",
		Description: "Publish one bounded workgraph across a real owner restart.", DesignContext: "Exercise the shared coordinator and GapDB committer.",
		Criteria:    []workgraph.CriterionObservation{{ID: "criterion-publication-cut", Text: "Recover the exact publication.", Outcome: "No duplicate mutation exists.", Verifier: "test", Requirement: workgraph.CriterionRequired}},
		WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/controlstore/gapdb", Domain: "controlstore", Access: workgraph.ScopeWriteExclusive}},
		Provenance:  []workgraph.ScopeReference{{ID: "plan-publication-cut", Kind: workgraph.ScopePlanConcern}},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-publication-cut", "mission-publication-cut", "artifact-publication-cut", strings.Repeat("b", 64), []workgraph.ApprovedScope{{ID: "plan-publication-cut", Kind: workgraph.ScopePlanConcern, MissionID: "mission-publication-cut"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	definition := sha256.Sum256([]byte("work-publication-cut"))
	observation, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-publication-cut", Provider: "durable_fake", ExternalID: "external-publication-cut", Revision: "provider-publication-cut", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", definition), CriterionEvidenceHints: map[string]string{"criterion-publication-cut": "real sidecar output"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: observation, PlanningSourceCommit: strings.Repeat("c", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("d", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-publication-cut", FinalizedAt: time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC), GovernanceContextID: ids.GovernanceContextID("governance-publication-cut")})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := workgraph.BuildFinalizationAuthorityV2(workgraph.FinalizationPlanInputV2{Context: workgraph.AuthorityContextV2{OperationID: "operation-publication-cut", RuntimeEpochID: "epoch-publication-cut", ActorID: "actor-publication-cut", ProcessID: "process-publication-cut", DoctrineRevision: "doctrine-publication-cut", ProfileRevision: "profile-publication-cut", PolicyRevision: "policy-publication-cut", Governance: []string{"governance-publication-cut"}, ObservedCreatedAt: "2026-09-05T01:00:00Z", OperationEventID: "event-operation-publication-cut", DomainEventID: "event-workgraph-publication-cut"}, Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func publicationCutCoordinator(t *testing.T, adapter *storegapdb.Adapter) *journalv2.PublicationCoordinatorV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := productionruntime.BuildReducerSetV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func gapdbRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate GapDB publication-cut source")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func publicationCutExecutable(t *testing.T) string {
	t.Helper()
	path := os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE")
	if path == "" {
		path = os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	}
	if path == "" {
		t.Skip("set SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE to run real publication restart cuts")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
