package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// The whole public census is retained here. Only the derived business view may
// omit an execution recovery root, after persisted and typed-result validation.
type terminalPublicationHistory struct {
	Publications  []terminalPublicationReceipt
	Progress      productionruntime.MissionProgress
	Inspections   map[string]productionruntime.ProgressItem
	Recovery      map[journalv2.OperationID]scenarioRecoveryCommand
	HistorySHA256 string
}
type terminalPublicationReceipt struct {
	Ack    journalv2.AcknowledgedPublicationV2
	Proof  productionruntime.AcknowledgedPublicationProof
	Source string
}

func provenTerminalProgress(t *testing.T, h *portableHarness, progress productionruntime.MissionProgress, tracker []terminalTrackerEvidence, repository repositoryTerminalEvidence, legacy []terminalOperationEvidence) terminalSemanticProgress {
	t.Helper()
	c := h.publications
	c.mu.Lock()
	acks := append([]journalv2.AcknowledgedPublicationV2(nil), c.acks...)
	initial := make(map[journalv2.OperationID]journalv2.AcknowledgedPublicationV2, len(c.initial))
	for k, v := range c.initial {
		initial[k] = v
	}
	sources := make(map[journalv2.PublicationID]string, len(c.accepted))
	for k, v := range c.accepted {
		sources[k] = v
	}
	recovery := make(map[journalv2.OperationID]scenarioRecoveryCommand, len(c.recovery))
	for k, v := range c.recovery {
		recovery[k] = v
	}
	c.mu.Unlock()
	history := &terminalPublicationHistory{Progress: progress, Inspections: make(map[string]productionruntime.ProgressItem), Recovery: recovery}
	for _, ack := range acks {
		var origin []journalv2.AcknowledgedPublicationV2
		if ack.Plan().BindingCreate() == nil {
			root, ok := initial[ack.Plan().OperationState().OperationID()]
			if !ok {
				t.Fatal("continuation omitted collected initial ACK")
			}
			origin = append(origin, root)
		}
		proof, err := h.host.VerifyAcknowledgedPublication(t.Context(), ack, origin...)
		if err != nil {
			t.Fatalf("verify actual ACK %s: %v", ack.PublicationID(), err)
		}
		history.Publications = append(history.Publications, terminalPublicationReceipt{Ack: ack, Proof: proof, Source: sources[ack.PublicationID()]})
	}
	for _, item := range progress.Operations {
		inspected, err := h.host.InspectOperation(t.Context(), progress.ProjectID, progress.MissionID, item.AggregateID)
		if err != nil {
			t.Fatalf("inspect proved mission root %s: %v", item.AggregateID, err)
		}
		history.Inspections[item.AggregateID] = inspected
	}
	history.HistorySHA256 = terminalHistoryDigest(history)
	excluded, err := validateTerminalHistory(history)
	if err != nil {
		t.Fatal(err)
	}
	// Retain the previous five common result checks, now as an exact subset of
	// the independently proved complete history, not as its replacement.
	for _, old := range legacy {
		found := false
		for _, r := range history.Publications {
			s := r.Proof.HistoricalOperationState
			if string(s.OperationID()) == old.OperationID && s.ResultKind() == old.ResultKind && string(s.Result()) == old.ResultBody && s.ResultDigest().String() == old.ResultSHA256 {
				found = true
			}
		}
		if !found {
			t.Fatalf("legacy result missing from complete ACK history: %s", old.OperationID)
		}
	}
	result, err := terminalBusinessOutcome(history, tracker, repository, excluded)
	if err != nil {
		t.Fatal(err)
	}
	result.History = history
	return result
}

// Unconditional stable entry: a business comparison is never evidence that
// either history is valid. In particular unequal whole-trace hashes are legal.
func validateTerminalRestartEquivalence(want, got terminalSemanticProgress) error {
	for _, value := range []terminalSemanticProgress{want, got} {
		excluded, err := validateTerminalHistory(value.History)
		if err != nil {
			return err
		}
		derived, err := terminalBusinessOutcome(value.History, value.Tracker, value.Repository, excluded)
		if err != nil {
			return err
		}
		if err := validateTerminalSemanticEquivalence(derived, value); err != nil {
			return fmt.Errorf("derived business outcome: %w", err)
		}
	}
	return validateTerminalSemanticEquivalence(want, got)
}

func validateTerminalHistory(h *terminalPublicationHistory) (map[string]bool, error) {
	fail := func(why string) (map[string]bool, error) { return nil, fmt.Errorf("terminal ACK history: %s", why) }
	if h == nil || len(h.Publications) == 0 || h.HistorySHA256 == "" {
		return fail("missing history")
	}
	if terminalHistoryDigest(h) != h.HistorySHA256 {
		return fail("history digest")
	}
	roots := make(map[journalv2.OperationID][]terminalPublicationReceipt)
	publications := make(map[journalv2.PublicationID]bool)
	revisions := make(map[uint64]bool)
	var capture journalv2.StoreRevisionV2
	for _, r := range h.Publications {
		a, p := r.Ack, r.Proof
		s := a.Plan().OperationState()
		if r.Source != "typed-command-success" && r.Source != "admitted-startup-success" {
			return fail("ACK without admitted typed success")
		}
		if a.ProviderRevision() == 0 || a.DurableThroughRevision() < a.ProviderRevision() || publications[a.PublicationID()] || revisions[a.ProviderRevision()] || a.PublicationID() != a.Plan().PublicationID() || a.PlanDigest() != a.Plan().Digest() || a.RecordSetDigest() != a.Plan().RecordSetDigest() || a.MutationCount() != a.Plan().MutationCount() || a.AssertionCount() != a.Plan().AssertionCount() {
			return fail("duplicate or fabricated ACK")
		}
		publications[a.PublicationID()] = true
		revisions[a.ProviderRevision()] = true
		claim := a.Plan().WorkspacePathClaim()
		if claim == nil {
			if p.WorkspacePathClaim != nil || p.ClaimRecordRevision != 0 {
				return fail("extraneous permanent claim evidence")
			}
		} else if p.WorkspacePathClaim == nil || p.ClaimRecordRevision != a.ProviderRevision() || p.WorkspacePathClaim.ClaimDigest() != claim.ClaimDigest() || !bytes.Equal(p.WorkspacePathClaim.CanonicalValue(), claim.CanonicalValue()) {
			return fail("permanent claim evidence")
		}
		if capture.CurrentRevision() == 0 {
			capture = p.CapturedCoordinate
		}
		if capture != p.CapturedCoordinate || capture.DatabaseID() != h.Progress.DatabaseID || capture.CurrentRevision() != h.Progress.ProjectionRevision || a.ProviderRevision() > capture.CurrentRevision() || a.DurableThroughRevision() > capture.DurableThroughRevision() {
			return fail("unstable capture")
		}
		if !journalv2.EqualOperationStatesV2(s, p.HistoricalOperationState) || p.Binding.OperationID() != s.OperationID() {
			return fail("historical state")
		}
		binding, err := journalv2.EncodeOperationBindingV2(p.Binding)
		if err != nil || !bytes.Equal(binding, p.BindingRecord.CanonicalValue()) {
			return fail("binding bytes")
		}
		ba, _ := journalv2.NewOperationBindingAddressV2(s.OperationID())
		if p.BindingRecord.Address() != ba || p.BindingRecord.RecordRevision() == 0 || p.BindingRecord.RecordRevision() > a.ProviderRevision() || p.BindingRecord.ExpiresAt() != "" {
			return fail("binding revision")
		}
		witness, err := journalv2.EncodePublicationWitnessV2(a.Plan().PublicationWitness())
		wa, _ := a.PublicationID().WitnessAddressV2()
		if err != nil || p.Witness != a.Plan().PublicationWitness() || p.WitnessRecord.Address() != wa || p.WitnessRecord.RecordRevision() != a.ProviderRevision() || p.WitnessRecord.ExpiresAt() != "" || !bytes.Equal(witness, p.WitnessRecord.CanonicalValue()) {
			return fail("witness")
		}
		events := a.Plan().Events()
		if len(events) == 0 || len(events) != len(p.JournalRecords) {
			return fail("event census")
		}
		operationEvents := 0
		for i, event := range events {
			record := p.JournalRecords[i]
			address, _ := journalv2.NewJournalEventAddressV2(event.Coordinate(), event.LogicalVersion())
			encoded, err := journalv2.EncodeEventEnvelopeV2(event)
			decoded, decodeErr := journalv2.DecodeEventEnvelopeRecordV2(record, journalv2.DefaultEventRegistryV2())
			if err != nil || decodeErr != nil || record.Address() != address || record.RecordRevision() != a.ProviderRevision() || record.ExpiresAt() != "" || !bytes.Equal(encoded, record.CanonicalValue()) || decoded.OperationID() != s.OperationID() || decoded.OperationVersion() != s.LogicalVersion() || decoded.PublicationID() != a.PublicationID() {
				return fail("historical event bytes/coordinate")
			}
			if event.Family() == "operation" {
				operationEvents++
				reconstructed, err := journalv2.ReconstructOperationStateFromEventV2(decoded)
				if err != nil || !journalv2.EqualOperationStatesV2(reconstructed, s) || !sameRecoveryRecord(record, p.HistoricalOperationRecord) {
					return fail("operation event")
				}
			}
		}
		if operationEvents != 1 {
			return fail("operation event census")
		}
		stateBytes, err := journalv2.EncodeOperationStateV2(p.CurrentState)
		if err != nil || !bytes.Equal(stateBytes, p.CurrentProgressItem.CanonicalState) || p.CurrentProgressItem.Family != "operation" || p.CurrentProgressItem.AggregateID != string(s.OperationID()) || p.CurrentProgressItem.LogicalVersion != uint64(p.CurrentState.LogicalVersion()) || p.CurrentProgressItem.LastPublicationID != string(p.CurrentState.LastPublicationID()) {
			return fail("current head bytes/version/publication")
		}
		// Decode rather than treating a nonempty digest as evidence.
		encodedState, encodeErr := journalv2.EncodeOperationStateV2(s)
		if encodeErr != nil {
			return fail("operation encoding")
		}
		decoded, err := journalv2.DecodeOperationStateV2(encodedState)
		if err != nil || !journalv2.EqualOperationStatesV2(decoded, s) {
			return fail("operation result digest")
		}
		roots[s.OperationID()] = append(roots[s.OperationID()], r)
	}
	expected := make(map[string]productionruntime.ProgressItem)
	excluded := make(map[string]bool)
	for id, receipts := range roots {
		sort.Slice(receipts, func(i, j int) bool {
			return receipts[i].Ack.Plan().OperationState().LogicalVersion() < receipts[j].Ack.Plan().OperationState().LogicalVersion()
		})
		first := receipts[0]
		binding := first.Ack.Plan().BindingCreate()
		if binding == nil || first.Proof.Binding != *binding || first.Proof.BindingRecord.RecordRevision() != first.Ack.ProviderRevision() {
			return fail("missing exact initial binding")
		}
		for i, r := range receipts {
			if r.Ack.Plan().OperationState().LogicalVersion() != journalv2.LogicalVersion(i+1) || r.Proof.Binding != *binding || !sameRecoveryRecord(r.Proof.BindingRecord, first.Proof.BindingRecord) || !journalv2.EqualOperationStatesV2(r.Proof.CurrentState, first.Proof.CurrentState) || !equalProgressItem(r.Proof.CurrentProgressItem, first.Proof.CurrentProgressItem) {
				return fail("noncontiguous versions/binding/head")
			}
			if i > 0 && (r.Ack.Plan().BindingCreate() != nil || r.Ack.ProviderRevision() <= receipts[i-1].Ack.ProviderRevision()) {
				return fail("continuation origin")
			}
		}
		last := receipts[len(receipts)-1]
		if !journalv2.EqualOperationStatesV2(last.Ack.Plan().OperationState(), last.Proof.CurrentState) {
			return fail("unrecorded current head")
		}
		scope := binding.Scope()
		if scope.Kind == journalv2.ScopeHost {
			if scope != (journalv2.Scope{Kind: journalv2.ScopeHost}) {
				return fail("host binding scope")
			}
			if _, exists := h.Inspections[string(id)]; exists {
				return fail("host root in mission inspection")
			}
		} else {
			if scope.ProjectID != h.Progress.ProjectID || scope.MissionID != h.Progress.MissionID {
				return fail("cross-scope binding")
			}
			expected[string(id)] = last.Proof.CurrentProgressItem
		}
		if command, ok := h.Recovery[id]; ok {
			if len(receipts) != 1 {
				return fail("recovery continuation")
			}
			if err := validateScenarioRecovery(id, command, last); err != nil {
				return fail(err.Error())
			}
			if err := validateScenarioRecoveryEvents(command, last); err != nil {
				return fail(err.Error())
			}
			if command.ExecutionRequest != nil {
				excluded[string(id)] = true
			}
		}
	}
	for id := range h.Recovery {
		if _, ok := roots[id]; !ok {
			return fail("typed recovery without ACK")
		}
	}
	check := func(items []productionruntime.ProgressItem) error {
		seen := make(map[string]bool)
		for _, item := range items {
			want, ok := expected[item.AggregateID]
			if !ok || seen[item.AggregateID] || !equalProgressItem(want, item) {
				return fmt.Errorf("mission census mismatch %s", item.AggregateID)
			}
			seen[item.AggregateID] = true
		}
		if len(seen) != len(expected) {
			return fmt.Errorf("mission census omitted root")
		}
		return nil
	}
	if err := check(h.Progress.Operations); err != nil {
		return fail(err.Error())
	}
	var operationItems []productionruntime.ProgressItem
	for _, item := range h.Progress.Items {
		if item.Family == "operation" {
			operationItems = append(operationItems, item)
		}
	}
	if err := check(operationItems); err != nil {
		return fail(err.Error())
	}
	if len(h.Inspections) != len(expected) {
		return fail("inspection census")
	}
	for id, item := range expected {
		if !equalProgressItem(item, h.Inspections[id]) {
			return fail("scoped inspection mismatch")
		}
	}
	return excluded, nil
}

func sameRecoveryRecord(a, b journalv2.RecoveryRecordV2) bool {
	return a.Address() == b.Address() && a.RecordRevision() == b.RecordRevision() && a.ExpiresAt() == b.ExpiresAt() && bytes.Equal(a.CanonicalValue(), b.CanonicalValue())
}
func equalProgressItem(a, b productionruntime.ProgressItem) bool {
	return a.Family == b.Family && a.AggregateID == b.AggregateID && a.LogicalVersion == b.LogicalVersion && a.LastPublicationID == b.LastPublicationID && a.AuthorityState == b.AuthorityState && a.ObservedState == b.ObservedState && bytes.Equal(a.CanonicalState, b.CanonicalState)
}

// Only declaration-specific volatile fields are changed in the expected copy.
// Comparing the entire remaining typed state protects all authority fields,
// including actor/process, governance, path claim, stage and result.
func validateScenarioRecovery(id journalv2.OperationID, c scenarioRecoveryCommand, r terminalPublicationReceipt) error {
	bad := func() error { return fmt.Errorf("typed recovery request/result mismatch %s", id) }
	if (c.ExecutionRequest == nil) == (c.WorkspaceRequest == nil) || c.Before.Validate() != nil || c.After.Validate() != nil || c.Before.RuntimeEpochID == c.After.RuntimeEpochID || c.After.LogicalVersion != c.Before.LogicalVersion+1 {
		return bad()
	}
	expected := c.Before
	expected.LogicalVersion++
	expected.RuntimeEpochID = c.After.RuntimeEpochID
	binding := r.Proof.Binding
	s := r.Proof.HistoricalOperationState
	declaredBinding, err := scenarioRecoveryBinding(id, c)
	if err != nil || declaredBinding != binding {
		return bad()
	}
	if c.ExecutionRequest != nil {
		q := c.ExecutionRequest
		if q.Kind != execution.ExecutionRebindV2 || q.OperationID != id || q.ProjectID != c.Before.ProjectID || q.MissionID != c.Before.MissionID || q.WorkRevisionID != c.Before.WorkRevisionID || q.WorkID != c.Before.WorkID || q.RunID != c.Before.RunID || q.ActorID != c.Before.ActorID || q.ProcessID != c.Before.ProcessID || q.LeaseID != c.Before.LeaseID || q.Generation != c.Before.Generation || q.Fence != c.Before.Fence || q.RuntimeEpochID != c.After.RuntimeEpochID || q.Deadline.UTC().Format(time.RFC3339Nano) != c.Before.Deadline || c.BeforeWorkspace != nil || c.AfterWorkspace != nil {
			return bad()
		}
		governance, err := execution.ExecutionGovernanceFromContextV2(q.Governance)
		if err != nil || !reflect.DeepEqual(governance, c.Before.Governance) {
			return bad()
		}
		// The real rebind reducer preserves heartbeat/deadline.
		expected.HeartbeatAt = c.Before.HeartbeatAt
		if c.Before.WorkspaceID != "" || binding.CommandKind() != "execution_slot."+string(execution.ExecutionRebindV2) || binding.ActorID() != journalv2.ActorID(q.ActorID) || binding.ProcessID() != journalv2.ProcessID(q.ProcessID) || binding.Scope().ProjectID != journalv2.ProjectID(q.ProjectID) || binding.Scope().MissionID != journalv2.MissionID(q.MissionID) || s.ResultKind() != "execution_slot.result" {
			return bad()
		}
		decoded, err := execution.DecodeExecutionSlotStateV2(s.Result())
		if err != nil || !reflect.DeepEqual(decoded, c.After) {
			return bad()
		}
	} else {
		q := c.WorkspaceRequest
		if q.OperationID != id || q.ProjectID != c.Before.ProjectID || q.MissionID != c.Before.MissionID || q.WorkRevisionID != c.Before.WorkRevisionID || q.WorkID != c.Before.WorkID || q.ActorID != c.Before.ActorID || q.ProcessID != c.Before.ProcessID || q.OriginatingRuntimeEpochID != c.After.RuntimeEpochID || !reflect.DeepEqual(q.Governance, c.Before.Governance) || c.BeforeWorkspace == nil || c.AfterWorkspace == nil || c.Before.WorkspaceID != c.BeforeWorkspace.Binding.WorkspaceID || c.Before.WorkspacePathClaimDigest != c.BeforeWorkspace.Binding.PathClaimDigest {
			return bad()
		}
		contribution, err := workspace.NewEpochRebindContributionV2(workspace.EpochRebindContributionInputV2{Prior: *c.BeforeWorkspace, RecordRevision: c.WorkspaceRecordRevision, EventID: q.WorkspaceEventID, RuntimeEpochID: q.OriginatingRuntimeEpochID, Observation: c.BeforeWorkspace.Observation, OccurredAt: q.ObservedAt})
		if err != nil {
			return bad()
		}
		next, err := contribution.State()
		if err != nil || !bytes.Equal(next.CanonicalBytes(), c.AfterWorkspace.CanonicalBytes()) {
			return bad()
		}
		if binding.Scope() != (journalv2.Scope{Kind: journalv2.ScopeHost}) || s.ResultKind() != "workspace.result" {
			return bad()
		}
		var result execution.WorkspaceAuthorityResultV2
		if json.Unmarshal(s.Result(), &result) != nil || !reflect.DeepEqual(result.Execution, c.After) || !bytes.Equal(result.Workspace.CanonicalBytes(), c.AfterWorkspace.CanonicalBytes()) {
			return bad()
		}
		result.RecordRevision = 0
		result.Replayed = false
		canonical, err := journalv2.CanonicalObject(mustTerminalJSON(result))
		if err != nil || !bytes.Equal(canonical, s.Result()) {
			return bad()
		}
	}
	if !reflect.DeepEqual(expected, c.After) {
		return bad()
	}
	return nil
}

func validateScenarioRecoveryEvents(c scenarioRecoveryCommand, r terminalPublicationReceipt) error {
	var operationID, slotID, workspaceID journalv2.EventID
	if c.ExecutionRequest != nil {
		operationID = c.ExecutionRequest.OperationEventID
		slotID = c.ExecutionRequest.SlotEventID
	} else {
		operationID = c.WorkspaceRequest.OperationEventID
		slotID = c.WorkspaceRequest.ExecutionEventID
		workspaceID = c.WorkspaceRequest.WorkspaceEventID
	}
	expected := 2
	if c.WorkspaceRequest != nil {
		expected = 3
	}
	if len(r.Ack.Plan().Events()) != expected {
		return fmt.Errorf("typed recovery event census")
	}
	seen := make(map[string]bool)
	for _, event := range r.Ack.Plan().Events() {
		if seen[event.Family()] {
			return fmt.Errorf("typed recovery duplicated family")
		}
		seen[event.Family()] = true
		switch event.Family() {
		case "operation":
			if event.EventID() != operationID {
				return fmt.Errorf("typed recovery operation event identity")
			}
		case "execution_slot":
			reduced, err := execution.ReduceExecutionSlotEventV2(c.Before.CanonicalBytes(), event)
			if err != nil || event.EventID() != slotID || event.EventType() != "execution_slot.runtime_epoch_rebound" || !bytes.Equal(reduced, c.After.CanonicalBytes()) {
				return fmt.Errorf("typed execution recovery event/result")
			}
		case "workspace":
			if c.BeforeWorkspace == nil || c.AfterWorkspace == nil {
				return fmt.Errorf("unexpected recovery workspace")
			}
			reduced, err := workspace.ReduceEventV2(c.BeforeWorkspace.CanonicalBytes(), event)
			if err != nil || event.EventID() != workspaceID || event.EventType() != "workspace.runtime_epoch_rebound" || !bytes.Equal(reduced, c.AfterWorkspace.CanonicalBytes()) {
				return fmt.Errorf("typed workspace recovery event/result")
			}
		default:
			return fmt.Errorf("unexpected recovery family")
		}
	}
	if !seen["operation"] || !seen["execution_slot"] || (c.WorkspaceRequest != nil && !seen["workspace"]) {
		return fmt.Errorf("recovery event family census")
	}
	return nil
}

func scenarioRecoveryBinding(id journalv2.OperationID, c scenarioRecoveryCommand) (journalv2.OperationBindingV2, error) {
	var input journalv2.OperationBindingInput
	var command []byte
	if q := c.ExecutionRequest; q != nil {
		governance, err := execution.ExecutionGovernanceFromContextV2(q.Governance)
		if err != nil {
			return journalv2.OperationBindingV2{}, err
		}
		coordinate, err := execution.ExecutionSlotCoordinateV2(q.ProjectID, q.MissionID, q.WorkRevisionID, q.WorkID)
		if err != nil {
			return journalv2.OperationBindingV2{}, err
		}
		command = mustTerminalJSON(map[string]any{"kind": q.Kind, "project_id": q.ProjectID, "mission_id": q.MissionID, "work_revision_id": q.WorkRevisionID, "work_id": q.WorkID, "run_id": q.RunID, "actor_id": q.ActorID, "process_id": q.ProcessID, "lease_id": q.LeaseID, "generation": q.Generation, "presented_fence": q.Fence, "deadline": q.Deadline.UTC().Format(time.RFC3339Nano), "governance": governance})
		if q.Outcome != "" {
			var obj map[string]any
			_ = json.Unmarshal(command, &obj)
			obj["outcome"] = q.Outcome
			command = mustTerminalJSON(obj)
		}
		input = journalv2.OperationBindingInput{OperationID: id, CommandKind: "execution_slot." + string(q.Kind), CommandSchemaVersion: 1, Scope: coordinate.Scope, ActorID: journalv2.ActorID(q.ActorID), ProcessID: journalv2.ProcessID(q.ProcessID), DoctrineRevision: governance.DoctrineRevision, ProfileRevision: governance.ProfileRevision, PolicyRevision: governance.PolicyRevision, OriginatingRuntimeEpochID: q.RuntimeEpochID, ObservedCreatedAt: q.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)}
	} else if q := c.WorkspaceRequest; q != nil {
		command = mustTerminalJSON(map[string]any{"kind": execution.WorkspaceRebindV2, "project_id": q.ProjectID, "mission_id": q.MissionID, "work_revision_id": q.WorkRevisionID, "work_id": q.WorkID, "target_runtime_epoch_id": q.OriginatingRuntimeEpochID})
		input = journalv2.OperationBindingInput{OperationID: id, CommandKind: "workspace." + string(execution.WorkspaceRebindV2), CommandSchemaVersion: 1, Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, ActorID: journalv2.ActorID(q.ActorID), ProcessID: journalv2.ProcessID(q.ProcessID), DoctrineRevision: q.Governance.DoctrineRevision, ProfileRevision: q.Governance.ProfileRevision, PolicyRevision: q.Governance.PolicyRevision, OriginatingRuntimeEpochID: q.OriginatingRuntimeEpochID, ObservedCreatedAt: q.ObservedAt.UTC().Truncate(time.Second).Format(time.RFC3339)}
	} else {
		return journalv2.OperationBindingV2{}, fmt.Errorf("missing typed recovery request")
	}
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: input.CommandKind, CommandSchemaVersion: 1, Scope: input.Scope, Command: command})
	if err != nil {
		return journalv2.OperationBindingV2{}, err
	}
	input.CanonicalIntentDigest = intent
	return journalv2.NewOperationBindingV2(input)
}

func terminalHistoryDigest(h *terminalPublicationHistory) string {
	type recordFact struct {
		Address   string
		Revision  uint64
		Expires   string
		Canonical []byte
	}
	record := func(r journalv2.RecoveryRecordV2) recordFact {
		return recordFact{fmt.Sprintf("%#v", r.Address()), r.RecordRevision(), r.ExpiresAt(), r.CanonicalValue()}
	}
	type receiptFact struct {
		Publication                  string
		Plan, RecordSet              string
		Revision, Durable            uint64
		Mutations, Assertions        int
		Source                       string
		Binding, Witness, Historical recordFact
		Events                       []recordFact
		Current                      productionruntime.ProgressItem
		Capture                      string
		ClaimPresent                 bool
		ClaimDigest                  string
		ClaimCanonical               []byte
		ClaimRecordRevision          uint64
	}
	facts := make([]receiptFact, 0, len(h.Publications))
	for _, r := range h.Publications {
		a, p := r.Ack, r.Proof
		f := receiptFact{Publication: string(a.PublicationID()), Plan: a.PlanDigest().String(), RecordSet: a.RecordSetDigest().String(), Revision: a.ProviderRevision(), Durable: a.DurableThroughRevision(), Mutations: a.MutationCount(), Assertions: a.AssertionCount(), Source: r.Source, Binding: record(p.BindingRecord), Witness: record(p.WitnessRecord), Historical: record(p.HistoricalOperationRecord), Current: p.CurrentProgressItem, Capture: fmt.Sprintf("%#v", p.CapturedCoordinate)}
		f.ClaimRecordRevision = p.ClaimRecordRevision
		if p.WorkspacePathClaim != nil {
			f.ClaimPresent = true
			f.ClaimDigest = p.WorkspacePathClaim.ClaimDigest().String()
			f.ClaimCanonical = p.WorkspacePathClaim.CanonicalValue()
		}
		for _, e := range p.JournalRecords {
			f.Events = append(f.Events, record(e))
		}
		facts = append(facts, f)
	}
	return digestBytes(mustTerminalJSON(struct {
		Receipts    []receiptFact
		Progress    productionruntime.MissionProgress
		Inspections map[string]productionruntime.ProgressItem
		Recovery    map[journalv2.OperationID]scenarioRecoveryCommand
	}{facts, h.Progress, h.Inspections, h.Recovery}))
}
func mustTerminalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Decode a single complete JSON value without float64 conversion. Unknown keys,
// including unknown numeric business values, survive normalization exactly.
func terminalSemanticObject(b []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("terminal semantic JSON: trailing value or invalid suffix: %v", err)
	}
	if value == nil {
		return nil, fmt.Errorf("terminal semantic JSON: expected object")
	}
	return value, nil
}

// These are exact owner-declared paths, not recursively matched field names.
// Epochs identify the serving host, and logical versions count durable stages
// (including typed recovery), not business outcomes. Complete ACK histories
// retain their original values and every common-root continuation version.
// No timestamps, fences, paths, authority digests, or caller-snapshot evidence
// are excluded without an additional owner contract proving their volatility.
func terminalSemanticBytes(b []byte, owner string) ([]byte, error) {
	value, err := terminalSemanticObject(b)
	if err != nil {
		return nil, err
	}
	remove := func(path ...string) {
		object := value
		for _, key := range path[:len(path)-1] {
			next, ok := object[key].(map[string]any)
			if !ok {
				return
			}
			object = next
		}
		delete(object, path[len(path)-1])
	}
	executionPaths := func(prefix ...string) {
		remove(append(append([]string(nil), prefix...), "logical_version")...)
		remove(append(append([]string(nil), prefix...), "runtime_epoch_id")...)
	}
	workspacePaths := func(prefix ...string) {
		remove(append(append([]string(nil), prefix...), "logical_version")...)
		remove(append(append([]string(nil), prefix...), "binding", "runtime_epoch_id")...)
	}
	switch owner {
	case "family:operation":
		// OperationStateV2's wrapper owns these publication coordinates and derived
		// encodings. Identity is compared as AggregateID/OperationID; kind, schema,
		// status and effects remain here; the FULL decoded result is compared below.
		for _, key := range []string{"operation_id", "event_ids", "last_publication_id", "logical_version", "canonical_result", "result_sha256"} {
			remove(key)
		}
	case "family:execution_slot", "result:execution_slot.result":
		executionPaths()
	case "family:workspace", "result:workspace.stage.result":
		workspacePaths()
	case "result:workspace.result":
		// WorkspaceAuthorityResultV2 uses exported (capitalized) JSON members.
		executionPaths("Execution")
		workspacePaths("Workspace")
	case "family:handoff_slot", "result:handoff.result",
		"family:review_slot", "result:review.result",
		"family:acceptance", "result:acceptance.result",
		"family:integration_target", "family:integration_attempt", "result:integration.result":
		remove("logical_version")
		remove("runtime_epoch_id")
	case "family:external_effect", "result:external_effect.result":
		remove("logical_version")
		remove("runtime_epoch_id")
		// Effect ownership/adoption snapshots contain their own epoch coordinates.
		remove("execution", "runtime_epoch_id")
		remove("intent", "origin_runtime_epoch_id")
	}
	return journalv2.CanonicalObject(mustTerminalJSON(value))
}

func terminalBusinessOutcome(h *terminalPublicationHistory, tracker []terminalTrackerEvidence, repository repositoryTerminalEvidence, excluded map[string]bool) (terminalSemanticProgress, error) {
	p := h.Progress
	result := terminalSemanticProgress{Lifecycle: p.Lifecycle, WorkRevisionID: string(p.WorkRevisionID), Generation: p.Generation, Complete: p.Complete, TotalWork: p.TotalWork, WorkCounts: append([]productionruntime.StateCount(nil), p.WorkCounts...), Tracker: append([]terminalTrackerEvidence(nil), tracker...), Repository: repository}
	families := make(map[string]bool)
	for _, item := range p.Items {
		families[item.Family] = true
		if item.Family == "operation" && excluded[item.AggregateID] {
			continue
		}
		semantic, err := terminalSemanticBytes(item.CanonicalState, "family:"+item.Family)
		if err != nil {
			return result, err
		}
		entry := terminalSemanticItem{Family: item.Family, AggregateID: item.AggregateID, AuthorityState: item.AuthorityState, ObservedState: item.ObservedState, SemanticSHA256: digestBytes(semantic)}
		decoded, err := terminalSemanticObject(item.CanonicalState)
		if err != nil {
			return result, err
		}
		fence, _ := nestedTextField(decoded, "fence")
		entry.Fenced = fence != ""
		if item.Family == "operation" {
			op, err := journalv2.DecodeOperationStateV2(item.CanonicalState)
			if err != nil {
				return result, err
			}
			body, err := terminalSemanticBytes(op.Result(), "result:"+op.ResultKind())
			if err != nil {
				return result, err
			}
			entry.ResultKind = op.ResultKind()
			entry.ResultBody = string(body)
			entry.ResultSHA256 = digestBytes(body)
			result.Operations = append(result.Operations, terminalOperationEvidence{OperationID: item.AggregateID, ResultKind: entry.ResultKind, ResultBody: entry.ResultBody, ResultSHA256: entry.ResultSHA256})
		}
		result.Items = append(result.Items, entry)
	}
	for family := range families {
		result.Families = append(result.Families, family)
	}
	sort.Strings(result.Families)
	sort.Slice(result.WorkCounts, func(i, j int) bool { return result.WorkCounts[i].State < result.WorkCounts[j].State })
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].Family != result.Items[j].Family {
			return result.Items[i].Family < result.Items[j].Family
		}
		return result.Items[i].AggregateID < result.Items[j].AggregateID
	})
	sort.Slice(result.Operations, func(i, j int) bool { return result.Operations[i].OperationID < result.Operations[j].OperationID })
	canonical, err := journalv2.CanonicalObject(mustTerminalJSON(result))
	if err != nil {
		return result, err
	}
	result.SemanticSHA256 = digestBytes(canonical)
	return result, nil
}
