package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
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
			repository := repositoryRoot(t)
			admissionPath := filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-admission.json")
			admission, err := sidecargapdb.VerifyAssertionExtensionAdoption(
				filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-adoption.json"), admissionPath,
			)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := sidecargapdb.Start(t.Context(), sidecargapdb.LifecycleConfig{
				ExecutablePath:      productionExecutable(t),
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
	revision, _ := twoItemRevision(t, "project-publication-cut", "mission-publication-cut")
	plan, err := workgraph.BuildFinalizationPlanV2(workgraph.FinalizationPlanInputV2{
		Context: workgraph.AuthorityContextV2{
			OperationID: "operation-publication-cut", RuntimeEpochID: "epoch-publication-cut",
			ActorID: "actor-publication-cut", ProcessID: "process-publication-cut",
			DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
			Governance: []string{"governance-two-item"}, ObservedCreatedAt: "2026-09-05T12:01:00Z",
			OperationEventID: "event-publication-cut-operation", DomainEventID: "event-publication-cut-workgraph",
		},
		Revision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
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
