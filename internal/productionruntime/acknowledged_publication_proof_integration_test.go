package productionruntime

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// Test-only sink follows the actual Host across restart. Publication fixtures
// still pass through the admitted adapter and kernel coordinator, never ACK
// parsers or proof-specific authority grants.
type liveProofSink struct{ host *Host }

func (s *liveProofSink) AdvanceAcknowledgedPublicationV2(ctx context.Context, ack journalv2.AcknowledgedPublicationV2) error {
	return s.host.projection.AdvanceAcknowledgedPublicationV2(ctx, ack)
}

func TestAcknowledgedPublicationProofProductionOwner(t *testing.T) {
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
	root, err := os.MkdirTemp("", "spk-proof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	admission := "m1-assertion-extension-admission.json"
	observer := &publicationProofFixture{}
	config := Config{
		ControlRoot: filepath.Join(root, "authority"), HostAuthorityID: "host-proof-live", AuthorityBindingOperationID: "operation-proof-live-binding",
		M1AdoptionRecordPath:                 filepath.Join(repository, "docs/programs/evidence/m1-adoption.json"),
		AssertionExtensionAdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-assertion-extension-adoption.json"),
		Sidecar:                              sidecargapdb.LifecycleConfig{ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repository, "docs/programs/evidence", admission), RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(root, "authority"), SupervisorRunID: "supervisor-proof-live", ProductPolicySHA256: repeatByte('e', 64)},
		CapabilityReadiness:                  absentReadinessSource{}, DecompositionProvider: refusingDecompositionProvider{}, CandidateVerifier: refusingCandidateVerifier{}, IntegrationGit: refusingIntegrationGit{},
		Reconciler: &missionCreatingReconciler{}, PublicationBoundaryObserver: observer,
	}
	h, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(t.Context()) })
	if len(observer.observerACKs) == 0 {
		t.Fatal("production observer captured no actual ACK")
	}
	// Exercise a normal typed mission continuation too. Mission progression has
	// distinct operation roots; it must not be mislabeled operation version two.
	missionProgress, err := h.Progress().Mission("project-runtime", "mission-reconciled")
	if err != nil {
		t.Fatal(err)
	}
	var expected mission.ReadinessAuthorityStateV2
	for _, item := range missionProgress.Items {
		if item.Family == "mission" {
			expected, err = mission.DecodeReadinessAuthorityStateV2(item.CanonicalState)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	definitionDigest, _ := journalv2.ReducerStateDigestV2([]byte(`{"definition":"reconciled"}`))
	updated, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{ProjectID: "project-runtime", MissionID: "mission-reconciled", DefinitionRevision: "definition-runtime", DefinitionDigest: definitionDigest, Version: 2, Lifecycle: mission.MissionPaused, LastOperationID: "operation-proof-pause", ObservedAt: "2026-09-05T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.ExecuteReadiness(t.Context(), h.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: "operation-proof-pause", ProjectID: "project-runtime", MissionID: "mission-reconciled", ActorID: "actor-runtime", ProcessID: "process-proof-live", OriginatingRuntimeEpochID: h.RuntimeEpochID(), DoctrineRevision: "doctrine-runtime", ProfileRevision: "profile-runtime", PolicyRevision: "policy-runtime", ObservedAt: "2026-09-05T12:00:00Z", EventType: mission.ReadinessEventPausedV2, Expected: expected, Updated: updated})
	if err != nil {
		t.Fatal(err)
	}
	// A foundation operation fixture supplies a true same-root continuation.
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := BuildReducerSetV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	sink := &liveProofSink{host: h}
	owner := h.owner.(*productionOwner) // fixture composition only; proof dispatch has no concrete fallback
	coordinator, err := journalv2.NewPublicationCoordinatorWithObserverAndSinkV2(owner.adapter, tracker, observer, sink)
	if err != nil {
		t.Fatal(err)
	}
	first := publishLiveProofOperation(t, h, coordinator, nil)
	before := h.Status()
	if _, err := h.VerifyAcknowledgedPublication(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if h.Status() != before {
		t.Fatal("proof changed admission/projection")
	}
	oldEpoch := h.RuntimeEpochID()
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	config.Reconciler = nil
	config.Sidecar.SupervisorRunID = "supervisor-proof-restart"
	h, err = Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	sink.host = h
	// A new owner requires a new durable port but the same validated tracker.
	coordinator, err = journalv2.NewPublicationCoordinatorWithObserverAndSinkV2(h.owner.(*productionOwner).adapter, tracker, observer, sink)
	if err != nil {
		t.Fatal(err)
	}
	continued := publishLiveProofOperation(t, h, coordinator, &first)
	if h.RuntimeEpochID() == oldEpoch {
		t.Fatal("restart did not rotate epoch")
	}
	if continued.Plan().Events()[0].CommittedRuntimeEpochID() == first.Plan().BindingCreate().OriginatingEpoch() {
		t.Fatal("fixture did not exercise recovered continuation epoch")
	}
	before = h.Status()
	origins := map[journalv2.OperationID]journalv2.AcknowledgedPublicationV2{}
	for _, ack := range observer.observerACKs {
		state := ack.Plan().OperationState()
		var origin []journalv2.AcknowledgedPublicationV2
		if ack.Plan().BindingCreate() != nil {
			origins[state.OperationID()] = ack
		} else {
			origin = append(origin, origins[state.OperationID()])
		}
		proof, err := h.VerifyAcknowledgedPublication(t.Context(), ack, origin...)
		if err != nil {
			t.Fatalf("actual persisted publication %s: %v", ack.PublicationID(), err)
		}
		if claim := ack.Plan().WorkspacePathClaim(); claim != nil {
			if proof.WorkspacePathClaim == nil || proof.WorkspacePathClaim.ClaimDigest() != claim.ClaimDigest() || string(proof.WorkspacePathClaim.CanonicalValue()) != string(claim.CanonicalValue()) || proof.ClaimRecordRevision != ack.ProviderRevision() {
				t.Fatal("original permanent claim not verified after restart")
			}
		} else if proof.WorkspacePathClaim != nil || proof.ClaimRecordRevision != 0 {
			t.Fatal("invented claim evidence")
		}
		if proof.WitnessRecord.RecordRevision() != ack.ProviderRevision() || proof.Binding.OperationID() != state.OperationID() {
			t.Fatal("actual adapter proof misattributed")
		}
	}
	if h.Status() != before {
		t.Fatal("proof changed restarted host")
	}
	if _, err := h.InspectOperation(t.Context(), "project-runtime", "mission-reconciled", "operation-proof-live-continuation"); !IsProgressKind(err, ProgressNotFound) {
		t.Fatalf("host-only root leaked into mission: %v", err)
	}
}

func publishLiveProofOperation(t *testing.T, h *Host, coordinator *journalv2.PublicationCoordinatorV2, root *journalv2.AcknowledgedPublicationV2) journalv2.AcknowledgedPublicationV2 {
	t.Helper()
	version := journalv2.LogicalVersion(1)
	status := journalv2.StatusAccepted
	eventID := journalv2.EventID("event-proof-live-initial")
	ids := []journalv2.EventID{eventID}
	var prior *journalv2.OperationStateV2
	if root != nil {
		version = 2
		status = journalv2.StatusResolved
		eventID = "event-proof-live-continuation"
		state := root.Plan().OperationState()
		prior = &state
		ids = append(state.EventIDs(), eventID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	stateInput := journalv2.OperationStateInput{OperationID: "operation-proof-live-continuation", LogicalVersion: version, Status: status, EventIDs: ids}
	if root != nil {
		stateInput.ResultKind = "proof.result"
		stateInput.ResultSchemaVersion = 1
		stateInput.CanonicalResult = []byte(`{"verified":true}`)
	}
	state, err := journalv2.NewOperationStateV2(stateInput)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(state.OperationID()), journalv2.Scope{Kind: journalv2.ScopeHost})
	digest, _ := journalv2.ReducerStateDigestV2(payload)
	head := journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: version - 1, NextLogicalVersion: version, EventID: eventID, CommittedRuntimeEpochID: h.RuntimeEpochID(), CanonicalStateDigest: digest, AuthoritySummary: payload}
	if root != nil {
		head.ExpectedRecordRevision = root.ProviderRevision()
	}
	condition, err := journalv2.ConditionForHeadTransitionV2(head)
	if err != nil {
		t.Fatal(err)
	}
	eventType := "operation.accepted"
	if root != nil {
		eventType = "operation.resolved"
	}
	input := journalv2.CommitPlanInput{OperationState: state, PriorOperationState: prior, Events: []journalv2.EventDraftV2{{EventID: eventID, EventType: eventType, Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: h.RuntimeEpochID(), ActorID: "actor-proof-live", ProcessID: "process-proof-live", Payload: payload}}, Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}}
	if root == nil {
		intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "proof.live", CommandSchemaVersion: 1, Scope: coordinate.Scope, Command: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: state.OperationID(), CommandKind: "proof.live", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: coordinate.Scope, ActorID: "actor-proof-live", ProcessID: "process-proof-live", OriginatingRuntimeEpochID: h.RuntimeEpochID(), ObservedCreatedAt: "2026-09-05T12:01:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		input.OperationBinding = &binding
		// A generic canonical sealed claim fixture, not workspace admission.
		claimDigest, err := journalv2.ReducerStateDigestV2([]byte(`{"path":"/original/physical/workspace"}`))
		if err != nil {
			t.Fatal(err)
		}
		claim, err := journalv2.NewWorkspacePathClaimV2(claimDigest, []byte(`{"workspace_id":"workspace-proof-live"}`))
		if err != nil {
			t.Fatal(err)
		}
		input.WorkspacePathClaim = &claim
	}
	plan, err := journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	ack, err := coordinator.Publish(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return ack
}
