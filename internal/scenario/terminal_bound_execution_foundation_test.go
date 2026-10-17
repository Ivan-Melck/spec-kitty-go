package scenario

// Private intra-history closure only. No oracle, pairing, physical ownership
// correspondence, durable replacement tokens, or outcome comparison.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func terminalWorkspaceMatchesExecution(b workspace.BindingV2, s execution.ExecutionSlotStateV2) bool {
	return b.ProjectID == s.ProjectID && b.MissionID == s.MissionID && b.WorkRevisionID == s.WorkRevisionID && b.WorkID == s.WorkID && b.RunID == s.RunID && b.LeaseID == s.LeaseID && b.WorkspaceID == s.WorkspaceID && b.Generation == s.Generation && b.Fence == s.Fence && b.ActorID == s.ActorID && b.ProcessID == s.ProcessID && b.RuntimeEpochID == s.RuntimeEpochID && b.PathClaimDigest == s.WorkspacePathClaimDigest
}

func (p *terminalCoordinateProof) boundWorkspaceAt(s terminalHistoricalState) (terminalHistoricalState, error) {
	fail := func() (terminalHistoricalState, error) {
		return terminalHistoricalState{}, fmt.Errorf("coordinate proof: UNSUPPORTED workspace-bound execution needs historical permanent-claim reference proof")
	}
	x, err := execution.DecodeExecutionSlotStateV2(s.canonical)
	if err != nil || x.WorkspaceID == "" {
		return fail()
	}
	c, err := workspace.WorkspaceCoordinateV2(workspace.BindingV2{WorkspaceID: x.WorkspaceID})
	inventory, exists := p.workspaces[c]
	if err != nil || !exists || inventory.claimRevision == 0 || inventory.claimRevision > s.key.revision || inventory.claim.ClaimDigest() != x.WorkspacePathClaimDigest {
		return fail()
	}
	var latest terminalHistoricalState
	matches := 0
	for _, key := range inventory.versions {
		if key.revision > s.key.revision {
			continue
		}
		if key.revision > latest.key.revision {
			latest = p.states[key]
			matches = 1
		} else if key.revision == latest.key.revision {
			matches++
		}
	}
	w, err := workspace.DecodeStateV2(latest.canonical)
	if matches != 1 || err != nil || !terminalWorkspaceMatchesExecution(w.Binding, x) {
		return fail()
	}
	return latest, nil
}

func (p *terminalCoordinateProof) closeBoundExecutionReferences(h *terminalPublicationHistory) error {
	return p.closeBoundExecutionReferencesWithVisitation(h, false)
}

// The only alternate visitation is a deterministic reversal of a COPY of the
// actual retained ACK event array. Envelopes, EventIndex and publication facts
// are untouched; no caller-supplied authority or arbitrary order is admitted.
func terminalBoundPublicationVisits(r terminalPublicationReceipt, reverse bool) []journalv2.EventEnvelopeV2 {
	events := append([]journalv2.EventEnvelopeV2(nil), r.Ack.Plan().Events()...)
	if reverse {
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
	}
	return events
}

func (p *terminalCoordinateProof) closeBoundExecutionReferencesWithVisitation(h *terminalPublicationHistory, reverse bool) error {
	p.boundExecutions = make(map[terminalHistoricalKey]terminalHistoricalKey)
	groups := append([]terminalPublicationReceipt(nil), h.Publications...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Ack.ProviderRevision() < groups[j].Ack.ProviderRevision() })
	current := make(map[journalv2.AggregateCoordinate]terminalHistoricalState)
	for _, r := range groups {
		visits := terminalBoundPublicationVisits(r, reverse)
		// Apply every owner first. References within a publication see its complete
		// next view, never whichever workspace happened to have a lower EventIndex.
		prior := make(map[journalv2.AggregateCoordinate]terminalHistoricalState, len(current))
		for c, s := range current {
			prior[c] = s
		}
		changed := false
		for _, e := range visits {
			s := p.states[p.events[e.EventID()]]
			current[e.Coordinate()] = s
			if e.Family() == "execution_slot" || e.Family() == "workspace" {
				changed = true
			}
		}
		for _, e := range visits {
			if e.Family() != "execution_slot" {
				continue
			}
			s := current[e.Coordinate()]
			x, err := execution.DecodeExecutionSlotStateV2(s.canonical)
			if err != nil {
				return err
			}
			if x.WorkspaceID == "" {
				continue
			}
			w, err := p.boundWorkspaceAt(s)
			if err != nil {
				return err
			}
			p.boundExecutions[s.key] = w.key
			if e.EventType() == "execution_slot.workspace_bound" || e.EventType() == "execution_slot.runtime_epoch_rebound" || e.EventType() == "execution_slot.lease_expired" {
				if err := p.closeWorkspaceCoupling(r, prior, s, w); err != nil {
					return err
				}
			}
		}
		if !changed {
			continue
		}
		// Revalidate all current bound owners, not historical slots superseded by
		// later generations. Workspace-only stage changes are legal if authority
		// stays equal; workspace-only epoch changes cannot strand a bound head,
		// and quarantine of a current bound workspace requires its execution's
		// same-publication expiry and the same source-owned composition proof.
		for _, s := range current {
			if s.envelope.Family() != "execution_slot" {
				continue
			}
			x, err := execution.DecodeExecutionSlotStateV2(s.canonical)
			if err != nil {
				return err
			}
			if x.WorkspaceID == "" {
				continue
			}
			c, err := workspace.WorkspaceCoordinateV2(workspace.BindingV2{WorkspaceID: x.WorkspaceID})
			workspaceState := current[c]
			w, decodeErr := workspace.DecodeStateV2(workspaceState.canonical)
			if err != nil || decodeErr != nil || !terminalWorkspaceMatchesExecution(w.Binding, x) {
				return fmt.Errorf("coordinate proof: bound execution current owner mismatch at revision %d", r.Ack.ProviderRevision())
			}
			// Workspace-led check is deliberately limited to CURRENT bound
			// owners. An obsolete slot superseded by an unbound generation is
			// not a consumer of a later workspace transition.
			if workspaceState.key.publication == r.Ack.PublicationID() && workspaceState.envelope.EventType() == "workspace.quarantined" {
				if s.key.publication != r.Ack.PublicationID() || s.key.revision != r.Ack.ProviderRevision() || s.envelope.EventType() != "execution_slot.lease_expired" {
					return fmt.Errorf("coordinate proof: workspace quarantine needs same-publication bound lease expiry")
				}
				if err := p.closeWorkspaceCoupling(r, prior, s, workspaceState); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Copy-safe exact linked resolution, with history/capture/digest proof at the
// execution endpoint and a fresh historical lookup defending against a stale
// or substituted link. No global digest alias and no latest-at-capture lookup.
func (p *terminalCoordinateProof) resolveBoundExecutionWorkspace(ref terminalStateReference) (terminalHistoricalState, error) {
	s, err := p.resolveAcceptanceReference(ref)
	if err != nil {
		return terminalHistoricalState{}, err
	}
	expected, err := p.boundWorkspaceAt(s)
	link, ok := p.boundExecutions[s.key]
	if err != nil || !ok || link != expected.key {
		return terminalHistoricalState{}, fmt.Errorf("coordinate reference: missing/nonexact bound execution historical link")
	}
	expected.canonical = append([]byte(nil), expected.canonical...)
	return expected, nil
}

// Reconstruct through the source-owned capability, without publishing. This
// closes compose laws, command shape/stable intent, exact result schema/bytes,
// governance, origin, effect membership and old/new CAS from retained states.
// The capture port only accepts the historical coordinates supplied here.
type terminalCouplingPort struct {
	executionCoordinate journalv2.AggregateCoordinate
	workspaceCoordinate journalv2.AggregateCoordinate
	execution           execution.ExecutionSlotObservationV2
	workspace           execution.WorkspaceAuthorityObservationV2
	epoch               journalv2.RuntimeEpochID
	plan                journalv2.SealedCommitPlanV2
}

var terminalCouplingCaptured = fmt.Errorf("private coupling reconstruction captured")

func (p *terminalCouplingPort) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
}
func (p *terminalCouplingPort) ObserveExecutionSlot(_ context.Context, c journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	if c != p.executionCoordinate {
		return execution.ExecutionSlotObservationV2{}, fmt.Errorf("foreign execution observation")
	}
	return p.execution, nil
}
func (p *terminalCouplingPort) ObserveWorkspace(_ context.Context, c journalv2.AggregateCoordinate) (execution.WorkspaceAuthorityObservationV2, error) {
	if c != p.workspaceCoordinate {
		return execution.WorkspaceAuthorityObservationV2{}, fmt.Errorf("foreign workspace observation")
	}
	return p.workspace, nil
}
func (p *terminalCouplingPort) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return p.epoch, nil
}
func (p *terminalCouplingPort) PublishWorkspace(_ context.Context, plan execution.WorkspaceAuthorityPlanV2) (execution.WorkspaceAuthorityAckV2, error) {
	p.plan, _ = plan.Publication()
	return execution.WorkspaceAuthorityAckV2{}, terminalCouplingCaptured
}

func (p *terminalCoordinateProof) closeWorkspaceCoupling(r terminalPublicationReceipt, prior map[journalv2.AggregateCoordinate]terminalHistoricalState, s, w terminalHistoricalState) error {
	fail := func(why string) error {
		return fmt.Errorf("coordinate proof: workspace-bound execution coupling: %s", why)
	}
	actual := r.Ack.Plan()
	if len(actual.Events()) != 3 || len(actual.Heads()) != 3 || len(actual.Conditions()) != 3 || len(actual.Assertions()) != 0 {
		return fail("atomic membership")
	}
	members := make(map[string]journalv2.EventEnvelopeV2)
	for _, e := range actual.Events() {
		if _, ok := members[e.Family()]; ok {
			return fail("duplicate family")
		}
		members[e.Family()] = e
	}
	if len(members) != 3 || members["operation"].EventType() != "operation.resolved" || members["workspace"].EventID() != w.key.event || w.key.revision != s.key.revision || w.key.publication != s.key.publication {
		return fail("nonexact atomic workspace")
	}
	next, err := execution.DecodeExecutionSlotEventV2(s.envelope.Payload())
	if err != nil {
		return err
	}
	before, err := execution.DecodeExecutionSlotStateV2(prior[s.key.coordinate].canonical)
	if err != nil {
		return fail("missing old execution")
	}
	ws, err := workspace.DecodeStateV2(w.canonical)
	if err != nil {
		return err
	}
	port := &terminalCouplingPort{executionCoordinate: s.key.coordinate, workspaceCoordinate: w.key.coordinate, execution: execution.ExecutionSlotObservationV2{State: &before, RecordRevision: prior[s.key.coordinate].key.revision}, epoch: next.State.RuntimeEpochID}
	if old, ok := prior[w.key.coordinate]; ok {
		state, err := workspace.DecodeStateV2(old.canonical)
		if err != nil {
			return err
		}
		port.workspace = execution.WorkspaceAuthorityObservationV2{State: &state, RecordRevision: old.key.revision}
	}
	service, err := execution.NewWorkspaceAuthorityServiceV2(port, port, journalv2.DefaultEventRegistryV2())
	if err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, next.ObservedAt)
	if err != nil {
		return err
	}
	request := execution.WorkspaceAuthorityRequestV2{OperationID: actual.OperationState().OperationID(), OperationEventID: members["operation"].EventID(), ExecutionEventID: s.key.event, Governance: next.State.Governance, ObservedAt: at}
	switch s.envelope.EventType() {
	case "execution_slot.workspace_bound":
		request.Kind = execution.WorkspaceBindV2
		request.Contribution, err = workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: ws.Binding, EffectID: ws.EffectID, EventID: w.key.event, OccurredAt: at})
	case "execution_slot.lease_expired":
		request.Kind = execution.WorkspaceQuarantineV2
		if port.workspace.State == nil {
			return fail("missing old workspace")
		}
		request.Contribution, err = workspace.NewQuarantineContributionV2(workspace.QuarantineContributionInputV2{Prior: *port.workspace.State, RecordRevision: port.workspace.RecordRevision, EventID: w.key.event, Reason: ws.QuarantineReason, OccurredAt: at})
	case "execution_slot.runtime_epoch_rebound":
		_, err = service.RebindRuntimeEpoch(context.Background(), execution.WorkspaceEpochRebindRequestV2{ProjectID: next.State.ProjectID, MissionID: next.State.MissionID, WorkRevisionID: next.State.WorkRevisionID, WorkID: next.State.WorkID, ActorID: next.State.ActorID, ProcessID: next.State.ProcessID, OriginatingRuntimeEpochID: next.State.RuntimeEpochID, OperationID: request.OperationID, OperationEventID: request.OperationEventID, ExecutionEventID: s.key.event, WorkspaceEventID: w.key.event, Governance: request.Governance, ObservedAt: at})
	default:
		return fail("unsupported coupled transition")
	}
	if request.Kind != "" && err == nil {
		_, err = service.Execute(context.Background(), request)
	}
	if !errors.Is(err, terminalCouplingCaptured) || port.plan.Digest().IsZero() {
		return fail(fmt.Sprintf("source-owned contract refused: %v", err))
	}
	expected := port.plan
	if actual.BindingCreate() == nil || *actual.BindingCreate() != *expected.BindingCreate() || r.Proof.Binding != *expected.BindingCreate() {
		return fail("command/intent/authority binding")
	}
	op, want := actual.OperationState(), expected.OperationState()
	if op.Status() != want.Status() || op.LogicalVersion() != want.LogicalVersion() || op.ResultKind() != want.ResultKind() || op.ResultSchemaVersion() != want.ResultSchemaVersion() || !bytes.Equal(op.Result(), want.Result()) || !reflect.DeepEqual(op.EventIDs(), want.EventIDs()) || !reflect.DeepEqual(op.EffectIDs(), want.EffectIDs()) {
		return fail("exact typed result/census")
	}
	for _, e := range expected.Events() {
		a, ok := members[e.Family()]
		if !ok || !reflect.DeepEqual(terminalCouplingDraft(a), terminalCouplingDraft(e)) {
			return fail("typed event/old-new snapshot")
		}
	}
	// Head and CAS condition order is not a semantic authority choice.
	for _, head := range expected.Heads() {
		found := false
		for _, a := range actual.Heads() {
			if reflect.DeepEqual(a, head) {
				found = true
			}
		}
		if !found {
			return fail("historical CAS/head")
		}
	}
	for _, condition := range expected.Conditions() {
		found := false
		for _, a := range actual.Conditions() {
			if a == condition {
				found = true
			}
		}
		if !found {
			return fail("historical CAS condition")
		}
	}
	if !reflect.DeepEqual(actual.EffectIntents(), expected.EffectIntents()) {
		return fail("effect intent")
	}
	a, b := actual.WorkspacePathClaim(), expected.WorkspacePathClaim()
	if (a == nil) != (b == nil) || a != nil && (a.ClaimDigest() != b.ClaimDigest() || !bytes.Equal(a.CanonicalValue(), b.CanonicalValue())) {
		return fail("original claim")
	}
	return nil
}

func terminalCouplingDraft(e journalv2.EventEnvelopeV2) journalv2.EventDraftV2 {
	return journalv2.EventDraftV2{EventID: e.EventID(), EventType: e.EventType(), Family: e.Family(), EventSchemaVersion: e.EventSchemaVersion(), ReducerVersion: e.ReducerVersion(), Coordinate: e.Coordinate(), AggregateLogicalVersion: e.LogicalVersion(), CommittedRuntimeEpochID: e.CommittedRuntimeEpochID(), ActorID: e.ActorID(), ProcessID: e.ProcessID(), WorkRevisionID: e.WorkRevisionID(), PolicyRevision: e.PolicyRevision(), GovernanceRevisions: e.GovernanceRevisions(), Observations: e.Observations(), Payload: e.Payload()}
}
