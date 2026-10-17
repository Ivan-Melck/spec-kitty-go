package productionruntime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// Opt-in ORIGINAL WP13 image only. Do not interpret this fixture as an expired
// retained-extra witness or performance qualification. No new image/writer gate.
func TestRetainedRecordsProofProductionOwnerLive(t *testing.T) {
	binary := os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	if binary == "" {
		t.Skip("set the exact admitted SPEC_KITTY_WP13_GAPDBD")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, publications := range []bool{false, true} {
		name := "private_only"
		if publications {
			name = "actual_ACK_final_overwrite"
		}
		t.Run(name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "spk-retained-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			runtimeRoot := filepath.Join(root, "runtime")
			if err := os.Mkdir(runtimeRoot, 0700); err != nil {
				t.Fatal(err)
			}
			observer := &publicationProofFixture{}
			config := Config{
				ControlRoot: filepath.Join(root, "authority"), HostAuthorityID: "host-retained-live", AuthorityBindingOperationID: "operation-retained-binding",
				M1AdoptionRecordPath:                 filepath.Join(repository, "docs/programs/evidence/m1-adoption.json"),
				AssertionExtensionAdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-adoption.json"),
				Sidecar:                              sidecargapdb.LifecycleConfig{ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-admission.json"), RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(root, "authority"), SupervisorRunID: "supervisor-retained-live", ProductPolicySHA256: repeatByte('e', 64)},
				CapabilityReadiness:                  absentReadinessSource{}, DecompositionProvider: refusingDecompositionProvider{}, CandidateVerifier: refusingCandidateVerifier{}, IntegrationGit: refusingIntegrationGit{},
				PublicationBoundaryObserver: observer, MonitorInterval: time.Hour,
			}
			if publications {
				config.Reconciler = &missionCreatingReconciler{}
			}
			h, err := Start(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.Close(t.Context()) })
			if publications {
				registry := journalv2.DefaultEventRegistryV2()
				reducers, err := BuildReducerSetV2(registry)
				if err != nil {
					t.Fatal(err)
				}
				tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
				if err != nil {
					t.Fatal(err)
				}
				coordinator, err := journalv2.NewPublicationCoordinatorWithObserverAndSinkV2(h.owner.(*productionOwner).adapter, tracker, observer, &liveProofSink{host: h})
				if err != nil {
					t.Fatal(err)
				}
				first := publishLiveProofOperation(t, h, coordinator, nil)
				_ = publishLiveProofOperation(t, h, coordinator, &first)
				if len(observer.observerACKs) < 3 {
					t.Fatal("actual publication observer did not collect all ACKs")
				}
			} else if len(observer.observerACKs) != 0 {
				t.Fatal("empty fixture unexpectedly published")
			}
			before := h.projection.Attribution()
			proof, err := h.VerifyRetainedRecords(t.Context(), observer.observerACKs)
			if err != nil {
				t.Fatal(err)
			}
			if proof.Attribution() != before || proof.MatchedPrivateRecords() != 6 || proof.MatchedRecords() != proof.MatchedPublicationRecords()+6 || h.projection.Attribution() != before || h.State() != HostReady {
				t.Fatal("inexact or mutating high proof")
			}
			if publications {
				if proof.MatchedPublicationRecords() == 0 {
					t.Fatal("actual publication rows not included")
				}
				if p, err := h.VerifyRetainedRecords(t.Context(), observer.observerACKs[1:]); err == nil || !reflect.ValueOf(p).IsZero() {
					t.Fatal("missing actual ACK footprint accepted")
				}
			} else if proof.MatchedPublicationRecords() != 0 {
				t.Fatal("invented publication row")
			}
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if p, err := h.VerifyRetainedRecords(t.Context(), observer.observerACKs); err == nil || !reflect.ValueOf(p).IsZero() {
				t.Fatal("closed original owner earned proof")
			}
		})
	}
}
