package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	restore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// This test is deliberately the only GapDB/projection join in WP11. The
// production packages remain directionally independent; WP13's composition
// root will own the corresponding runtime wiring.
func TestOutboxGapDBHistoryRestoresDisposableProjectionAfterDroppedAckV2(t *testing.T) {
	registry := journalv2.DefaultEventRegistryV2()
	reducers := outboxProjectionReducersV2(t, registry)
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	memory := newV2AtomicMemory()
	// The execution fixture is conditioned on a real finalized-work eligibility
	// observation at revision 17. Seed that authoritative prerequisite exactly;
	// the source and outbox publications themselves still travel through the
	// production GapDB adapter and coordinator below.
	executionSource, source, coordinates := outboxGapDBSourceFixtureV2(t, registry)
	memory.revision = 17
	for _, guard := range executionSource.EligibilityGuards() {
		key, keyErr := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	}
	provider := memory.provider()
	commit := provider.batchFn
	calls := 0
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls++
		result, commitErr := commit(ctx, batch)
		if commitErr == nil && calls == 4 {
			return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
		}
		return result, commitErr
	}
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "db-outbox-restore", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	provider.scanFn = func(_ context.Context, prefix string, limit int, cursor string) (gapdbapi.ScanPage, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		keys := make([]string, 0, len(memory.records))
		for key := range memory.records {
			if strings.HasPrefix(key, prefix) && key > cursor {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		truncated := len(keys) > limit
		if truncated {
			keys = keys[:limit]
		}
		records := make([]gapdbapi.Record, len(keys))
		for index, key := range keys {
			records[index] = memory.records[key].Clone()
		}
		next := ""
		if truncated {
			next = keys[len(keys)-1]
		}
		return gapdbapi.ScanPage{ObservedRevision: memory.revision, AsOf: time.Date(2026, 9, 5, 12, 30, 0, 0, time.UTC), Records: records, Cursor: next, Truncated: truncated}, nil
	}
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := coordinator.Publish(t.Context(), executionSource); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	wrongIntent, err := controlstore.BindDurableOutboxIntentV2(source, 99, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	wrongCommand := bindGapDBOutboxCommandV2(t, controlstore.OutboxCommandV2{Action: controlstore.OutboxStartAttemptV2, Intent: wrongIntent, AttemptID: "attempt-wrong-proof", RuntimeEpochID: "epoch-outbox", OperationEventID: "event-wrong-proof-operation", OutboxEventID: "event-wrong-proof-outbox"}, "wrong-proof")
	wrongCapability, err := controlstore.BuildOutboxAuthorityPlanV2(wrongCommand, registry)
	if err != nil {
		t.Fatal(err)
	}
	wrongPlan, _ := wrongCapability.Publication()
	if _, err := adapter.CommitV2(t.Context(), wrongPlan); !IsKind(err, OutcomeConflict) || memory.revision != 19 {
		t.Fatalf("unproven source intent committed: revision=%d err=%v", memory.revision, err)
	}
	durable, err := controlstore.BindDurableOutboxIntentV2(source, 19, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	command := controlstore.OutboxCommandV2{
		Action: controlstore.OutboxStartAttemptV2, Intent: durable,
		AttemptID: "attempt-restorable", RuntimeEpochID: "epoch-outbox",
		OperationEventID: "event-outbox-operation-1", OutboxEventID: "event-outbox-1",
	}
	command = bindGapDBOutboxCommandV2(t, command, "restorable")
	capability, err := controlstore.BuildOutboxAuthorityPlanV2(command, registry)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newOutboxAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := authority.PublishOutbox(t.Context(), capability)
	if err != nil || ack.AuthorityRevision != 20 || coordinator.UnresolvedCount() != 0 || memory.batchCalls != 4 {
		t.Fatalf("ack=%+v unresolved=%d batches=%d err=%v", ack, coordinator.UnresolvedCount(), memory.batchCalls, err)
	}
	exact, err := authority.ResolveOutboxOperation(t.Context(), command.OperationBinding())
	if err != nil || exact.Kind != controlstore.OutboxOperationExactV2 || exact.State.Status() != journalv2.StatusResolved {
		t.Fatalf("exact replay=%+v err=%v", exact, err)
	}
	changed := command
	changed.AttemptID = "attempt-substituted"
	changed = bindGapDBOutboxCommandV2(t, changed, "restorable")
	conflict, err := authority.ResolveOutboxOperation(t.Context(), changed.OperationBinding())
	if err != nil || conflict.Kind != controlstore.OutboxOperationConflictV2 {
		t.Fatalf("changed binding=%+v err=%v", conflict, err)
	}

	recovered, err := restore.RestoreV2(t.Context(), restore.InputV2{
		Reader: adapter, EventRegistry: registry, Reducers: reducers,
		HostAuthorityID: "host-outbox", RecoveryAttemptID: "restore-outbox-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	readModel, err := projection.NewAuthorityV2("db-outbox-restore", registry, reducers)
	if err != nil {
		t.Fatal(err)
	}
	if err := readModel.Restore("epoch-restored", recovered); err != nil {
		t.Fatal(err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("outbox", journalv2.AggregateID(coordinates.EffectID), journalv2.Scope{Kind: journalv2.ScopeHost})
	view, err := readModel.Get(projection.GetQueryV2{Coordinate: coordinate, AtRevision: 20})
	if err != nil {
		t.Fatal(err)
	}
	state, err := controlstore.DecodeOutboxDeliveryStateV2(view.CanonicalState)
	if err != nil || state.Stage != controlstore.OutboxAttemptStartedV2 || state.AttemptID != "attempt-restorable" || state.SourcePublicationID != source.PublicationID() {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	durableObservation, err := authority.ObserveOutboxState(t.Context(), coordinate)
	if err != nil || durableObservation.State == nil || durableObservation.RecordRevision != 20 {
		t.Fatalf("durable observation=%+v err=%v", durableObservation, err)
	}
	recoveredIntent, err := controlstore.RecoverDurableOutboxIntentV2(*durableObservation.State)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := controlstore.NewOutboxProviderObservationV2(0, "", "", "unavailable")
	if err != nil {
		t.Fatal(err)
	}
	continuation := controlstore.OutboxCommandV2{Action: controlstore.OutboxRecordObservationV2, Intent: recoveredIntent, Prior: durableObservation.State, PriorRecordRevision: durableObservation.RecordRevision, AttemptID: state.AttemptID, RuntimeEpochID: "epoch-restored", Observation: &unavailable, OperationEventID: "event-restored-observation-operation", OutboxEventID: "event-restored-observation-outbox"}
	continuation = bindGapDBOutboxCommandV2(t, continuation, "restored-observation")
	continuationCapability, err := controlstore.BuildOutboxAuthorityPlanV2(continuation, registry)
	if err != nil {
		t.Fatal(err)
	}
	continuationPlan, _ := continuationCapability.Publication()
	if len(continuationPlan.Assertions()) != 0 {
		t.Fatal("continuation incorrectly required the source head to remain current")
	}
	continuationAck, err := authority.PublishOutbox(t.Context(), continuationCapability)
	if err != nil || continuationAck.AuthorityRevision != 21 || memory.batchCalls != 5 {
		t.Fatalf("continuation=%+v batches=%d err=%v", continuationAck, memory.batchCalls, err)
	}
}

func outboxProjectionReducersV2(t *testing.T, registry journalv2.EventRegistryV2) journalv2.ReducerSetV2 {
	t.Helper()
	owned := []journalv2.ReducerRegistration{{Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Owner: "internal/controljournal/application", ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2}}
	owned = append(owned, journalv2.ReducerRegistration{Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/mission", ImplementationDigest: mission.MissionReducerImplementationDigestV2(), Reduce: mission.ReduceMissionEventV2})
	owned = append(owned, artifact.ReducerRegistrationsV2()...)
	owned = append(owned, workgraph.ReducerRegistrationsV2()...)
	owned = append(owned, execution.ExecutionReducerRegistrationsV2()...)
	owned = append(owned, workspace.ReducerRegistrationsV2()...)
	owned = append(owned, handoff.ReducerRegistrationsV2()...)
	owned = append(owned, review.ReducerRegistrationsV2()...)
	owned = append(owned, integration.AcceptanceReducerRegistrationsV2()...)
	owned = append(owned, integration.IntegrationReducerRegistrationsV2()...)
	owned = append(owned, execution.EffectReducerRegistrationsV2()...)
	owned = append(owned, controlstore.OutboxReducerRegistrationsV2()...)
	byFamily := make(map[string]journalv2.ReducerRegistration, len(owned))
	for _, registration := range owned {
		if prior := byFamily[registration.Family]; prior.Reduce != nil {
			t.Fatalf("duplicate production reducer registration for %s", registration.Family)
		}
		byFamily[registration.Family] = registration
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		registration := byFamily[tuple.Family]
		if tuple.Family == "runtime_host" { // WP13 owns the only not-yet-migrated family.
			sum := sha256.Sum256([]byte("wp11-test-reducer/" + tuple.Family))
			digest, _ := journalv2.ParseDigest("test_reducer", hex.EncodeToString(sum[:]))
			registration = journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, _ journalv2.EventEnvelopeV2) ([]byte, error) {
				return nil, fmt.Errorf("runtime_host is owned by WP13")
			}}
		}
		if registration.Family != tuple.Family || registration.EventSchemaVersion != tuple.EventSchemaVersion || registration.ReducerVersion != tuple.ReducerVersion || registration.Owner != tuple.Owner || registration.Reduce == nil || registration.ImplementationDigest.IsZero() {
			t.Fatalf("missing exact production reducer for %s", tuple.Family)
		}
		registrations = append(registrations, registration)
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return reducers
}

// TestWP11ExecutableCapabilityConformanceV2 is intentionally an executable
// matrix, not a list of test names. Each row runs an accepted path and a
// refusal/fault path through the capability's consumer-owned production port.
// This makes a deleted, renamed, or weakened capability test observable here
// and keeps the final composition proof tied to behavior rather than prose.
func TestWP11ExecutableCapabilityConformanceV2(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded capability campaign is excluded from the short PR tier")
	}
	type evidence struct {
		name               string
		families           []string
		acceptedControl    string
		refusedControl     string
		acceptedOutcome    string
		refusedOutcome     string
		atomicBatchCount   int
		logicalIncrement   int
		effectIntent       bool
		refusalPreMutation bool
	}
	matrix := []evidence{
		{"global_operation", []string{"operation"}, "mission", "binding_conflict", "stable OperationID resolves or replays", "changed canonical binding conflicts before domain read", 1, 1, false, true},
		{"mission", []string{"mission"}, "mission", "binding_conflict", "mission create commits and replays", "changed binding refuses before mission read", 1, 1, false, true},
		{"decision", []string{"mission"}, "decision", "binding_conflict", "decision fact and mission head commit together", "changed binding refuses before mission read", 1, 1, false, true},
		{"blocker", []string{"mission"}, "blocker", "binding_conflict", "blocker fact and mission head commit together", "changed binding refuses before mission read", 1, 1, false, true},
		{"readiness", []string{"mission"}, "readiness", "legacy_phase", "activity census survives restart", "legacy phase authority is refused", 1, 1, false, true},
		{"artifact", []string{"artifact_catalog", "artifact_revision"}, "artifact", "artifact_nil", "catalog and immutable revision commit atomically", "unminted capability is refused", 1, 1, false, true},
		{"workrevision", []string{"workgraph"}, "workgraph", "workgraph_nil", "finalized graph commits atomically", "unminted capability is refused", 1, 1, false, true},
		{"reconciliation", []string{"workgraph"}, "workgraph_contention", "workgraph_nil", "independent mission advances while one CAS wins", "stale or unminted graph refuses", 1, 1, false, true},
		{"run", []string{"execution_slot"}, "execution", "pre_registration", "run admission commits and replays", "pre-registration refusal performs no delivery", 1, 1, false, true},
		{"lease", []string{"execution_slot"}, "execution", "pre_registration", "lease identity and fence commit with run", "stale authority refuses before delivery", 1, 1, false, true},
		{"workspace", []string{"workspace"}, "workspace", "workspace_contention", "lease-scoped path claim commits atomically", "same path permits one winner", 1, 1, false, true},
		{"governance", []string{"acceptance"}, "governance", "governance", "current policy decision permits publication", "stale policy decision refuses before publication", 1, 1, false, true},
		{"handoff", []string{"handoff_slot"}, "handoff", "handoff_partial", "candidate evidence commits atomically", "partial publication remains unresolved", 1, 1, false, true},
		{"review", []string{"review_slot"}, "review", "review_partial", "independent verdict commits atomically", "partial publication remains unresolved", 1, 1, false, true},
		{"acceptance", []string{"acceptance"}, "acceptance_integration", "acceptance_integration_partial", "criteria verdict commits before integration", "partial or mixed publication refuses", 1, 1, false, true},
		{"integration", []string{"integration_target", "integration_attempt"}, "acceptance_integration", "acceptance_integration_partial", "target serialization records exact attempt", "partial or mixed publication refuses", 1, 1, false, true},
		{"effect", []string{"external_effect"}, "effect", "effect_one_shot", "durable intent precedes provider dispatch", "duplicate dispatch permission is refused", 1, 1, true, true},
		{"outbox", []string{"outbox"}, "outbox_projection", "outbox_membership", "delivery evidence restores without authority transfer", "unminted or incomplete outbox plan refuses", 1, 1, false, true},
	}
	type expectedEvidence struct {
		families        string
		acceptedControl string
		refusedControl  string
		effectIntent    bool
	}
	expected := map[string]expectedEvidence{
		"global_operation": {"operation", "mission", "binding_conflict", false},
		"mission":          {"mission", "mission", "binding_conflict", false},
		"decision":         {"mission", "decision", "binding_conflict", false},
		"blocker":          {"mission", "blocker", "binding_conflict", false},
		"readiness":        {"mission", "readiness", "legacy_phase", false},
		"artifact":         {"artifact_catalog,artifact_revision", "artifact", "artifact_nil", false},
		"workrevision":     {"workgraph", "workgraph", "workgraph_nil", false},
		"reconciliation":   {"workgraph", "workgraph_contention", "workgraph_nil", false},
		"run":              {"execution_slot", "execution", "pre_registration", false},
		"lease":            {"execution_slot", "execution", "pre_registration", false},
		"workspace":        {"workspace", "workspace", "workspace_contention", false},
		"governance":       {"acceptance", "governance", "governance", false},
		"handoff":          {"handoff_slot", "handoff", "handoff_partial", false},
		"review":           {"review_slot", "review", "review_partial", false},
		"acceptance":       {"acceptance", "acceptance_integration", "acceptance_integration_partial", false},
		"integration":      {"integration_target,integration_attempt", "acceptance_integration", "acceptance_integration_partial", false},
		"effect":           {"external_effect", "effect", "effect_one_shot", true},
		"outbox":           {"outbox", "outbox_projection", "outbox_membership", false},
	}
	controls := map[string]func(*testing.T){
		"mission": TestMissionAuthorityAdapterOneBatchRestartReplayV2, "binding_conflict": TestMissionAuthorityAdapterBindingConflictPrecedesMissionReadV2,
		"decision": testWP11DecisionFactThroughGapDBV2, "blocker": TestMissionAuthorityAdapterPublishesAndObservesFactInSameBatchV2,
		"readiness": TestReadinessAuthorityAdapterCreateActivityPauseRestartReplayV2, "legacy_phase": TestReadinessAuthorityAdapterRejectsLegacyPhaseStateV2,
		"artifact": TestArtifactAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB, "artifact_nil": TestArtifactAuthorityRefusesNil,
		"workgraph": TestWorkgraphAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB, "workgraph_nil": TestWorkgraphAuthorityRefusesNil, "workgraph_contention": TestWorkgraphAuthorityHeadCASAllowsOneWinnerAndIndependentMissions,
		"execution": TestExecutionAndEffectAuthoritiesProductionConstructorsCommitAndRecoverAmbiguityV2, "pre_registration": TestAuthorityAdapterNeverDeliversAfterPreRegistrationRefusalV2,
		"workspace": TestWorkspaceAuthorityCommitsBindingAndPermanentPathClaimThroughGapDBV2, "workspace_contention": TestWorkspacePathClaimContentionAndIndependentProgressV2,
		"governance": TestAcceptanceGapDBRequiresExactCurrentPolicyDecisionBeforePublicationV2,
		"handoff":    TestHandoffGapDBPublisherCommitsOneCompleteBatchAndContendsV2, "handoff_partial": TestHandoffGapDBPartialPublicationRemainsUnresolvedV2,
		"review": TestReviewGapDBPublisherCommitsOneCompleteBatchAndContendsV2, "review_partial": TestReviewGapDBPartialPublicationRemainsUnresolvedV2,
		"acceptance_integration": TestAcceptanceAndIntegrationPublishThroughRealGapDBAndReconcileLostCASV2, "acceptance_integration_partial": TestAcceptanceAndIntegrationGapDBRejectPartialAndMixedPublicationsV2,
		"effect": TestExternalProviderRunsThroughRealGapDBEffectAuthorityV2, "effect_one_shot": TestEffectDispatchAuthorizationIsDurableOneShotThroughGapDBV2,
		"outbox_projection": TestOutboxGapDBHistoryRestoresDisposableProjectionAfterDroppedAckV2, "outbox_membership": TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2,
	}
	coveredFamilies := map[string]bool{}
	coveredCapabilities := map[string]bool{}
	usedControls := map[string]bool{}
	for _, row := range matrix {
		want, required := expected[row.name]
		if !required || coveredCapabilities[row.name] || strings.Join(row.families, ",") != want.families || row.acceptedControl != want.acceptedControl || row.refusedControl != want.refusedControl || row.effectIntent != want.effectIntent || controls[row.acceptedControl] == nil || controls[row.refusedControl] == nil || row.acceptedOutcome == "" || row.refusedOutcome == "" || row.atomicBatchCount != 1 || row.logicalIncrement != 1 || !row.refusalPreMutation {
			t.Fatalf("incomplete executable conformance row %s", row.name)
		}
		coveredCapabilities[row.name] = true
		for _, family := range row.families {
			coveredFamilies[family] = true
		}
		usedControls[row.acceptedControl] = true
		usedControls[row.refusedControl] = true
	}
	if len(coveredCapabilities) != len(expected) {
		t.Fatalf("capability census width=%d want=%d", len(coveredCapabilities), len(expected))
	}
	for name := range expected {
		if !coveredCapabilities[name] {
			t.Fatalf("required capability %s has no explicit accepted/refused mapping", name)
		}
	}
	for name := range usedControls {
		t.Run("control/"+name, controls[name])
	}
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "runtime_host" { // WP13 owns runtime-host composition.
			continue
		}
		if !coveredFamilies[tuple.Family] {
			t.Fatalf("registered migrated family %s lacks executable accepted/refused evidence", tuple.Family)
		}
	}
	assertWP11ProductionPlansReduceThroughComposedProjectionV2(t)
}

// assertWP11ProductionPlansReduceThroughComposedProjectionV2 closes the
// composition chain behind the capability controls above. Production services
// and plan builders emit the events; the exact WP11 reducer set consumes every
// migrated version-one aggregate event. Capability states must match the
// sealed head digest; operation state must match the durable full operation
// record because operation heads intentionally expose a compact summary. A deleted capability row,
// substituted family, pass-through reducer, or missing production reducer
// therefore makes the campaign fail for a specific family.
func assertWP11ProductionPlansReduceThroughComposedProjectionV2(t *testing.T) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	reducers := outboxProjectionReducersV2(t, registry)
	plans := make([]journalv2.SealedCommitPlanV2, 0, 12)
	appendPlan := func(plan journalv2.SealedCommitPlanV2, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}

	appendPlan(gapdbArtifactPlanV2(t).Publication())
	appendPlan(gapdbWorkgraphPlanV2(t).Publication())
	executionPlan, slot, _, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "wp11-composed", "work-wp11-composed")
	appendPlan(executionPlan.Publication())
	appendPlan(gapdbEffectPlanFixtureForSlotV2(t, "wp11-composed", slot, 18).Publication())

	workspaceMemory := newV2AtomicMemory()
	workspaceMemory.revision = 17
	workspaceProvider := workspaceMemory.provider()
	enableWorkspaceRecoveryScansV2(workspaceMemory, workspaceProvider)
	workspaceProvider.statusFn = gapdbMemoryStatusV2(workspaceMemory, "db-wp11-composed-workspace")
	workspaceAdapter, err := newAdapter(workspaceProvider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	workspacePlan, _ := gapdbWorkspaceBindPlanV2(t, workspaceAdapter, workspaceMemory, "wp11-composed", "work-wp11-composed-workspace", "/srv/spec-kitty/workspaces/wp11-composed")
	plans = append(plans, workspacePlan)
	appendPlan(captureFirstHandoffPlanV2(t, "wp11-composed", "work-wp11-composed-handoff").Publication())
	appendPlan(captureFirstReviewPlanV2(t, "wp11-composed", "review-wp11-composed").Publication())
	appendPlan(captureAcceptancePlanThroughProductionV2(t, newWP10GapDBScenarioV2(t, "wp11-composed-acceptance", false)).Publication())
	appendPlan(captureIntegrationPlanThroughProductionV2(t, newWP10GapDBScenarioV2(t, "wp11-composed-integration", true)).Publication())
	plans = append(plans, captureWP11MissionCreatePlanV2(t, registry))

	_, source, coordinates := outboxGapDBSourceFixtureV2(t, registry)
	durable, err := controlstore.BindDurableOutboxIntentV2(source, 19, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	outboxCommand := bindGapDBOutboxCommandV2(t, controlstore.OutboxCommandV2{Action: controlstore.OutboxStartAttemptV2, Intent: durable, AttemptID: "attempt-wp11-composed", RuntimeEpochID: "epoch-outbox", OperationEventID: "event-operation-outbox-wp11-composed", OutboxEventID: "event-outbox-wp11-composed"}, "wp11-composed")
	outboxPlan, err := controlstore.BuildOutboxAuthorityPlanV2(outboxCommand, registry)
	if err != nil {
		t.Fatal(err)
	}
	appendPlan(outboxPlan.Publication())

	covered := map[string]bool{}
	for _, plan := range plans {
		heads := plan.Heads()
		for _, event := range plan.Events() {
			if event.LogicalVersion() != 1 {
				continue
			}
			var head *journalv2.HeadTransitionV2
			for index := range heads {
				candidate := &heads[index]
				if candidate.Coordinate == event.Coordinate() && candidate.EventID == event.EventID() && candidate.NextLogicalVersion == event.LogicalVersion() {
					head = candidate
					break
				}
			}
			if head == nil {
				t.Fatalf("production plan %s event %s has no exact sealed head", plan.PublicationID(), event.EventID())
			}
			state, digest, reduceErr := reducers.Apply([]byte(`{}`), event)
			if reduceErr != nil {
				t.Fatalf("composed reducer rejected production %s event %s: %v", event.Family(), event.EventID(), reduceErr)
			}
			if event.Family() == "operation" {
				planned, encodeErr := journalv2.EncodeOperationStateV2(plan.OperationState())
				if encodeErr != nil || string(state) != string(planned) {
					t.Fatalf("composed reducer disagrees with durable operation state for event %s", event.EventID())
				}
			} else if digest != head.CanonicalStateDigest {
				t.Fatalf("composed reducer disagrees with sealed %s digest for event %s", event.Family(), event.EventID())
			}
			covered[event.Family()] = true
		}
	}
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "runtime_host" {
			continue
		}
		if !covered[tuple.Family] {
			t.Fatalf("migrated family %s has no production event through composed projection", tuple.Family)
		}
	}
}

type wp11MissionPlanCaptureV2 struct {
	plan journalv2.SealedCommitPlanV2
}

func (p *wp11MissionPlanCaptureV2) ResolveOperation(context.Context, journalv2.OperationBindingV2) (mission.OperationResolutionV2, error) {
	return mission.OperationResolutionV2{Kind: mission.OperationAbsentV2}, nil
}

func (p *wp11MissionPlanCaptureV2) ObserveMission(context.Context, journalv2.ProjectID, journalv2.MissionID) (mission.MissionObservationV2, error) {
	return mission.MissionObservationV2{}, fmt.Errorf("unexpected mission observation")
}

func (p *wp11MissionPlanCaptureV2) ObserveMissionFact(context.Context, journalv2.ProjectID, journalv2.MissionID, mission.MissionFactKindV2, string) (mission.MissionFactObservationV2, error) {
	return mission.MissionFactObservationV2{}, nil
}

func (p *wp11MissionPlanCaptureV2) PublishMission(_ context.Context, plan journalv2.SealedCommitPlanV2) (mission.MissionPublicationV2, error) {
	p.plan = plan
	return mission.MissionPublicationV2{}, fmt.Errorf("captured production mission plan")
}

func captureWP11MissionCreatePlanV2(t *testing.T, registry journalv2.EventRegistryV2) journalv2.SealedCommitPlanV2 {
	t.Helper()
	port := &wp11MissionPlanCaptureV2{}
	service, err := mission.NewMissionAuthorityServiceV2(port, missionEpochFixtureV2{"epoch-1"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	created := missionStateFixtureV2(t)
	command := mission.MissionAuthorityCommandV2{SchemaVersion: 1, OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1", OriginatingRuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: "2026-09-04T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: created}
	if _, err = service.Execute(t.Context(), command); err == nil {
		t.Fatal("mission capture port unexpectedly acknowledged publication")
	}
	if len(port.plan.Events()) == 0 {
		t.Fatal("mission service did not emit a production plan")
	}
	return port.plan
}

func testWP11DecisionFactThroughGapDBV2(t *testing.T) {
	t.Helper()
	memory := newV2AtomicMemory()
	memory.revision = 1
	provider := memory.provider()
	provider.statusFn = gapdbMemoryStatusV2(memory, "database-wp11-decision")
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	registry := journalv2.DefaultEventRegistryV2()
	reducers := outboxProjectionReducersV2(t, registry)
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewMissionAuthorityPortV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	service, err := mission.NewMissionAuthorityServiceV2(authority, missionEpochFixtureV2{"epoch-1"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	created := missionStateFixtureV2(t)
	create := mission.MissionAuthorityCommandV2{SchemaVersion: 1, OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1", OriginatingRuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: "2026-09-04T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: created}
	if _, err := service.Execute(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	deciding, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: created.DefinitionRevision(), DefinitionDigest: created.DefinitionDigest(), Version: 2, Status: mission.AuthorityMissionActiveV2, Step: created.Step(), OpenDecisionIDs: []string{"decision-1"}, Decomposition: mission.DecompositionNoneV2, LastOperationID: "operation-wp11-decision-open", ObservedAt: "2026-09-04T12:01:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{ProjectID: "project-1", MissionID: "mission-1", FactID: "decision-1", Kind: mission.MissionFactDecisionV2, Status: mission.MissionFactOpenV2, LogicalVersion: 1, DetailDigest: created.DefinitionDigest(), LastOperationID: "operation-wp11-decision-open", ObservedAt: "2026-09-04T12:01:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	command := mission.MissionAuthorityCommandV2{SchemaVersion: 1, OperationID: "operation-wp11-decision-open", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1", OriginatingRuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: "2026-09-04T12:01:00Z", EventType: mission.MissionEventDecisionOpenedV2, Expected: created, Successor: deciding, RelatedFact: &mission.MissionFactTransitionV2{Successor: fact}}
	before := memory.batchCalls
	if _, err := service.Execute(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if memory.batchCalls != before+1 {
		t.Fatalf("decision fact publication split across batches: %d -> %d", before, memory.batchCalls)
	}
	observed, err := authority.ObserveMissionFact(t.Context(), "project-1", "mission-1", mission.MissionFactDecisionV2, "decision-1")
	if err != nil || !observed.Found || observed.State.Digest() != fact.Digest() || observed.RecordRevision != 3 {
		t.Fatalf("decision fact observation=%+v err=%v", observed, err)
	}
}

func outboxGapDBSourceFixtureV2(t *testing.T, registry journalv2.EventRegistryV2) (journalv2.SealedCommitPlanV2, journalv2.SealedCommitPlanV2, controlstore.OutboxIntentCoordinatesV2) {
	t.Helper()
	executionCapability, slot, _, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "outbox-source", "work-outbox-source")
	executionPlan, err := executionCapability.Publication()
	if err != nil {
		t.Fatal(err)
	}
	request := gapdbEffectRequestForSlotV2(t, "outbox-source", slot)
	capability := gapdbEffectPlanFixtureForSlotV2(t, "outbox-source", slot, 18)
	plan, err := capability.Publication()
	if err != nil {
		t.Fatal(err)
	}
	binding := plan.BindingCreate()
	if binding == nil || binding.OperationID() != request.Intent.OperationID {
		t.Fatal("effect source fixture omitted its operation binding")
	}
	return executionPlan, plan, controlstore.OutboxIntentCoordinatesV2{
		EffectID: request.Intent.EffectID, SourceOperationID: request.Intent.OperationID,
		CanonicalIntent: request.Intent.CanonicalBytesV2(), IntendedMutationDigest: request.Intent.CanonicalRequestDigest,
		IntendedValue: request.Intent.IntendedValue, ExpectedProviderRevision: uint64(request.Intent.ExpectedProviderRevision),
		ExpectedValue: request.Intent.ExpectedValue, ExpectedOperationMarker: request.Intent.ExpectedOperationMarker,
		AttributionMarker: string(request.Intent.OperationID), ActorID: journalv2.ActorID(request.Intent.ActorID), Capability: binding.CommandKind(),
	}
}
