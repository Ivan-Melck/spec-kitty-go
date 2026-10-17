//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// W4 CODE ONLY, UNTESTED. Opaque ACKs come from the ORIGINAL coordinator and a
// fixture commit port. Owned Unix/self-pidfd is NOT a genuine provider or Host.
// The six/nine explicitly visited private fixture rows are NOT the owner-private
// visitor: that visitor's real source-format controls live in productionruntime.
// No copied finalizer, canonical token fabrication, state-bit repair, transport
// selector, production hook or provider admission is introduced here.
func w4ComposedACKPair(t *testing.T, label string, firstRevision, lastRevision uint64) []journalv2.AcknowledgedPublicationV2 {
	t.Helper()
	port := &retainedFixturePortV2{revision: firstRevision}
	coordinator := retainedCoordinatorV2(t, port)
	op := journalv2.OperationID("operation-w4-"+label)
	coordinate, err := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(op), journalv2.Scope{Kind: journalv2.ScopeHost})
	if err != nil { t.Fatal(err) }
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: op, CommandKind: "w4.inspect", CommandSchemaVersion: 1,
		CanonicalIntentDigest: v2TestDigest(label), Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, ActorID: "w4-actor", ProcessID: "w4-process", OriginatingRuntimeEpochID: "w4-epoch", ObservedCreatedAt: "2026-09-07T00:00:00Z"})
	if err != nil { t.Fatal(err) }
	claim, err := journalv2.NewWorkspacePathClaimV2(v2TestDigest("claim-"+label), []byte(fmt.Sprintf(`{"workspace":%q}`, label)))
	if err != nil { t.Fatal(err) }
	var prior *journalv2.OperationStateV2
	var acknowledgements []journalv2.AcknowledgedPublicationV2
	for version := 1; version <= 2; version++ {
		event := journalv2.EventID(fmt.Sprintf("event-w4-%s-%d", label, version))
		events := []journalv2.EventID{event}
		if prior != nil { events = append(prior.EventIDs(), event) }
		state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: op, LogicalVersion: journalv2.LogicalVersion(version), Status: journalv2.StatusAccepted, EventIDs: events})
		if err != nil { t.Fatal(err) }
		eventType := "operation.accepted"
		if version == 2 {
			eventType = "operation.progressed"
			state, err = journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: op, LogicalVersion: 2, Status: journalv2.StatusInProgress, EventIDs: events})
			if err != nil { t.Fatal(err) }
			port.revision = lastRevision
		}
		payload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
		if err != nil { t.Fatal(err) }
		digest, err := journalv2.ReducerStateDigestV2(payload)
		if err != nil { t.Fatal(err) }
		head := journalv2.HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: journalv2.LogicalVersion(version-1), NextLogicalVersion: journalv2.LogicalVersion(version), EventID: event,
			CommittedRuntimeEpochID: "w4-epoch", CanonicalStateDigest: digest, AuthoritySummary: payload}
		if version == 2 { head.ExpectedRecordRevision = firstRevision }
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil { t.Fatal(err) }
		input := journalv2.CommitPlanInput{OperationState: state, PriorOperationState: prior,
			Events: []journalv2.EventDraftV2{{EventID: event, EventType: eventType, Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2,
				Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(version), CommittedRuntimeEpochID: "w4-epoch", ActorID: "w4-actor", ProcessID: "w4-process", Payload: payload}},
			Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}}
		if version == 1 { input.OperationBinding, input.WorkspacePathClaim = &binding, &claim }
		ack := retainedPublishV2(t, coordinator, input)
		acknowledgements = append(acknowledgements, ack)
		persisted := ack.Plan().OperationState()
		prior = &persisted
	}
	return acknowledgements
}

type w4ComposedFixture struct {
	a *Adapter
	s RetainedObservationV2 // canonical address; fixture pointer must not be copied
	acks []journalv2.AcknowledgedPublicationV2
	rows map[string]gapdbapi.Record
	private []string
	mu sync.Mutex
	trace []string
	statuses int
	accepted *atomic.Int32
	cleanup *retainedRequiredFixtureCleanup
	reply func(*net.UnixConn, retainedRequest, int, string, []byte) []byte
}

func w4ComposedNew(t *testing.T, parent context.Context, acks []journalv2.AcknowledgedPublicationV2, admissions int) *w4ComposedFixture {
	t.Helper()
	f := &w4ComposedFixture{acks: acks, rows: make(map[string]gapdbapi.Record)}
	for key, row := range retainedCopiedOracleV2(t, acks) {
		f.rows[key] = gapdbapi.Record{Key: key, Value: bytes.Clone(row.canonical), Revision: gapdbapi.Revision(row.revision)}
	}
	// Structural private records only, explicitly enumerated and independently
	// read. No guessed private count is ever supplied to Finish.
	for i := 0; i < 3+3*admissions; i++ {
		key := fmt.Sprintf("w4-component-private-%02d", i)
		f.private = append(f.private, key)
		f.rows[key] = gapdbapi.Record{Key: key, Value: []byte(fmt.Sprintf(`{"fixture_row":%d}`, i)), Revision: 1}
	}
	transport, lifetime, accepted, cleanup := retainedRequiredFixtureOwned(t, func(conn *net.UnixConn, request retainedRequest) []byte {
		f.mu.Lock()
		phase := f.statuses
		key := ""
		if request.Operation == "status" {
			f.statuses++
			f.trace = append(f.trace, fmt.Sprintf("S%d", phase))
		} else {
			var args struct { Key string `json:"key"` }
			raw, err := json.Marshal(request.Arguments)
			if err != nil || json.Unmarshal(raw, &args) != nil { t.Error("invalid fixture request arguments", err) }
			key = args.Key
			f.trace = append(f.trace, key)
		}
		f.mu.Unlock()
		var response []byte
		if request.Operation == "status" {
			status := retainedTestStatus()
			status.CurrentRevision, status.DurableThroughRevision = 100, 100
			status.RecordCount = len(f.rows)
			if phase == 1 { status.RecordCount++ } // structural S1 inequality, NOT TTL
			response = retainedSuccess(t, request, status)
		} else if record, exists := f.rows[key]; exists {
			response = retainedSuccess(t, request, map[string]any{"record": record})
		} else {
			response = w4ComposedMissing(t, request, key)
		}
		if f.reply != nil { return f.reply(conn, request, phase, key, response) }
		return response
	})
	f.accepted, f.cleanup = accepted, cleanup
	var err error
	f.a, err = newAdapter(&ownedProvider{provider: &fakeProvider{}, transport: transport}, transport.endpoint, time.Second)
	if err != nil { t.Fatal(err) }
	f.a.lifetime = lifetime
	capture, err := journalv2.NewStoreRevisionV2("owned-test-db", 100, 100)
	if err != nil { t.Fatal(err) }
	f.s, err = f.a.PrepareRetainedObservationV2(parent, capture, acks)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { f.s.Close() })
	return f
}

func w4ComposedMissing(t *testing.T, request retainedRequest, key string) []byte {
	t.Helper()
	for _, definition := range gapdbapi.ErrorDefinitions() {
		if definition.Code == gapdbapi.CodeNotFound {
			return retainedJSON(t, map[string]any{"schema_version": AdmittedWireVersion, "ok": false, "database_id": "owned-test-db", "operation": request.Operation, "request_id": request.RequestID,
				"error": map[string]any{"code": definition.Code, "message": "fixture missing row", "retry": definition.Retry, "safe_actions": definition.SafeActions, "key": key, "current_revision": 100}})
		}
	}
	t.Fatal("missing existing not-found definition")
	return nil
}

func (f *w4ComposedFixture) arm(t *testing.T) {
	t.Helper()
	if err := f.s.ArmExpiryQualificationRefusal(); err != nil { t.Fatal(err) }
	if f.s.state.expiry.handle != &f.s || f.s.state.phase != 0 { t.Fatal("not canonical phase-zero arm") }
}

func (f *w4ComposedFixture) visit(t *testing.T) uint64 {
	t.Helper()
	ctx, err := f.s.PrivateContext()
	if err != nil { t.Fatal(err) }
	visited := make(map[string]bool)
	for _, key := range f.private {
		record, err := f.a.Get(ctx, key)
		want := f.rows[key]
		if err != nil || visited[key] || record.Key != key || record.Revision != uint64(want.Revision) || record.ExpiresAt != nil || !bytes.Equal(record.Value, want.Value) {
			t.Fatal("private fixture exact read failed", err)
		}
		visited[key] = true
	}
	return uint64(len(visited))
}

func (f *w4ComposedFixture) snapshotTrace() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.trace...)
}

func (f *w4ComposedFixture) wantTrace(statuses int) []string {
	out := []string{"S0"}
	for _, row := range f.s.state.expected { out = append(out, row.key) }
	out = append(out, f.private...)
	for phase := 1; phase < statuses; phase++ { out = append(out, fmt.Sprintf("S%d", phase)) }
	return out
}

func (f *w4ComposedFixture) cannotConsume(t *testing.T, first error) {
	t.Helper()
	if first == nil || strings.Contains(first.Error(), "SECRET") { t.Fatal("missing/unsanitized FIRST refusal", first) }
	budget := f.s.state.budget
	attempts, hashes, frames, accepted := budget.attempts, budget.hashed, budget.frames, f.accepted.Load()
	if err := f.s.ConsumeExpiryQualificationRefusal(); err == nil || f.s.state.expiry.consumed { t.Fatal("failed composed chain consumed") }
	if err := f.s.CheckLifetime(); err == nil { t.Fatal("failed chain renewed terminal work") }
	f.s.Close()
	if attempts != budget.attempts || hashes != budget.hashed || frames != budget.frames || accepted != f.accepted.Load() { t.Fatal("refusal retried/renewed work") }
	// Independent cached FIRST teardown, never overwrite the operation error.
	cleanupFirst := f.cleanup.transportClose.Close()
	if cleanupFirst != nil { t.Errorf("FIRST cleanup error/residual: %v", cleanupFirst) }
	if f.cleanup.transportClose.Close() != cleanupFirst || f.cleanup.transportClose.calls.Load() != 1 { t.Fatal("cleanup repaired/retried FIRST") }
}

func TestRetainedExpiryComposedACKControls(t *testing.T) {
	for _, fault := range []string{"omitted_origin", "zero_ACK", "duplicate_publication", "duplicate_revision", "future_coordinate", "foreign_fixture_ACK", "omitted_witness", "omitted_claim", "omitted_expected_event", "stale_overwritten_head"} {
		t.Run(fault, func(t *testing.T) {
			acks := w4ComposedACKPair(t, "ack-controls", 7, 29)
			f := w4ComposedNew(t, t.Context(), acks, 1)
			input := append([]journalv2.AcknowledgedPublicationV2(nil), acks...)
			preflight := false
			switch fault {
			case "omitted_origin": input = input[1:]; preflight = true
			case "zero_ACK": input[0] = journalv2.AcknowledgedPublicationV2{}; preflight = true
			case "duplicate_publication": input = append(input, input[0]); preflight = true
			case "duplicate_revision": input = append(input, w4ComposedACKPair(t, "other-revision", 7, 41)[0]); preflight = true
			case "future_coordinate": input = append(input, w4ComposedACKPair(t, "future", 101, 102)[0]); preflight = true
			case "foreign_fixture_ACK":
				// Actual distinguishable opaque ACKs from ANOTHER coordinator,
				// operation, intent, claim and publication, not an edited ACK.
				input = w4ComposedACKPair(t, "foreign", 11, 37)
			default:
				var role journalv2.SealedRecordRole
				switch fault {
				case "omitted_witness": role = journalv2.RecordPublicationWitness
				case "omitted_claim": role = journalv2.RecordWorkspacePathClaim
				case "omitted_expected_event": role = journalv2.RecordJournalEvent
				case "stale_overwritten_head": role = journalv2.RecordOperationHead
				}
				for _, record := range acks[0].Plan().Records() {
					if record.Role() != role { continue }
					key, err := v2KeyForSealedRecord(record)
					if err != nil { t.Fatal(err) }
					if fault == "stale_overwritten_head" {
						old := f.rows[key.String()]
						old.Value, old.Revision = record.CanonicalValue(), gapdbapi.Revision(acks[0].ProviderRevision())
						f.rows[key.String()] = old
					} else { delete(f.rows, key.String()) }
					break
				}
			}
			// Discard the unused original PREPARED handle, not a failed chain.
			// Each actual attempt has a fresh canonical pointer and one budget.
			f.s.Close()
			capture, err := journalv2.NewStoreRevisionV2("owned-test-db", 100, 100)
			if err != nil { t.Fatal(err) }
			f.s, err = f.a.PrepareRetainedObservationV2(t.Context(), capture, input)
			if preflight {
				if err == nil || !reflect.ValueOf(f.s).IsZero() || f.accepted.Load() != 0 { t.Fatal("invalid ACK input reached exchange", err) }
				return
			}
			if err != nil { t.Fatal("valid opaque ACK input unexpectedly failed preflight", err) }
			f.arm(t)
			first := f.s.ReadPublications()
			if first == nil || f.s.state.countMismatch || f.s.state.expiry.s1Mismatch || f.s.state.expiry.s2Closed || f.s.state.budget.terminal {
				t.Fatal("structural final-map refusal became a count witness", first)
			}
			if len(f.snapshotTrace()) < 2 || f.snapshotTrace()[0] != "S0" { t.Fatal("did not reach exact publication read") }
			f.cannotConsume(t, first)
		})
	}
}

// Literal per-member residual controls, not the old 52 cases. S1 always has
// inequality PLUS this independently invalid envelope/status coordinate.
// S0/S2 count-only decoys and wrong coordinate VALUES already have old coverage.
func w4ComposedChange(t *testing.T, raw []byte, path []string, mode string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil { t.Fatal(err) }
	original, exists := object[path[0]]
	if !exists { t.Fatal("missing literal fixture member", path) }
	if len(path) > 1 {
		changed := w4ComposedChange(t, original, path[1:], mode)
		needle := append([]byte(fmt.Sprintf("%q:", path[0])), original...)
		return bytes.Replace(raw, needle, append([]byte(fmt.Sprintf("%q:", path[0])), changed...), 1)
	}
	switch mode {
	case "missing": delete(object, path[0])
	case "null": object[path[0]] = json.RawMessage("null")
	case "wrong_type": object[path[0]] = json.RawMessage("[]")
	case "duplicate":
		return append(append(bytes.Clone(raw[:len(raw)-1]), append([]byte(fmt.Sprintf(",%q:", path[0])), original...)...), '}')
	default: t.Fatal("unknown literal mutation", mode)
	}
	return retainedJSON(t, object)
}

func TestRetainedExpiryComposedWireResidualMembers(t *testing.T) {
	paths := []string{"schema_version", "ok", "database_id", "operation", "request_id", "result",
		"result.lifecycle", "result.database_id", "result.owner_pid", "result.owner_started_at", "result.current_revision", "result.durable_through_revision"}
	for phase := 0; phase < 3; phase++ {
		for _, member := range paths {
			modes := []string{"missing", "null", "wrong_type"}
			if strings.Contains(member, ".") { modes = append(modes, "duplicate") }
			for _, mode := range modes {
				if member == "result" && mode == "wrong_type" { continue } // old wrong_JSON_type, not a new case
				t.Run(fmt.Sprintf("S%d/%s/%s", phase, member, mode), func(t *testing.T) {
					f := w4ComposedNew(t, t.Context(), w4ComposedACKPair(t, "wire", 7, 29), 2)
					var injections atomic.Int32
					f.reply = func(_ *net.UnixConn, request retainedRequest, actual int, _ string, raw []byte) []byte {
						if request.Operation == "status" && actual == phase {
							injections.Add(1)
							return w4ComposedChange(t, raw, strings.Split(member, "."), mode)
						}
						return raw
					}
					f.arm(t)
					first := f.s.ReadPublications()
					if phase > 0 {
						if first != nil { t.Fatal("S0/exact-read prerequisite", first) }
						visited := f.visit(t)
						var publications uint64
						publications, first = f.s.Finish(visited)
						if publications != 0 { t.Fatal("partial bookkeeping returned") }
					}
					if first == nil || injections.Load() != 1 || f.s.state.expiry.s2Closed || f.s.state.budget.terminal || f.s.state.expiry.s1Mismatch != (phase == 2) {
						t.Fatal("invalid target phase classified or later phase completed", first)
					}
					want := []string{"S0"}
					if phase > 0 { want = f.wantTrace(phase+1) }
					if !reflect.DeepEqual(f.snapshotTrace(), want) || int(f.accepted.Load()) != len(want) { t.Fatal("wrong exact request prefix", f.snapshotTrace(), want) }
					f.cannotConsume(t, first)
				})
			}
		}
	}
}

func TestRetainedExpiryComposedTerminalBoundaries(t *testing.T) {
	for _, boundary := range []string{"after_S0", "after_private", "after_S2", "after_terminal"} {
		for _, action := range []string{"caller_cancel", "session_close", "lifetime_revoke"} {
			t.Run(boundary+"/"+action, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				f := w4ComposedNew(t, ctx, w4ComposedACKPair(t, "terminal", 7, 29), 2)
				f.arm(t)
				if first := f.s.ReadPublications(); first != nil { t.Fatal(first) }
				var visited uint64
				if boundary != "after_S0" { visited = f.visit(t) }
				if boundary == "after_S2" || boundary == "after_terminal" {
					pubs, first := f.s.Finish(visited)
					if pubs != 0 || first != errRetainedObservation || !f.s.state.expiry.s2Closed || f.s.state.invalid.Load() { t.Fatal("S1/S2 prerequisite", first) }
					if !reflect.DeepEqual(f.snapshotTrace(), f.wantTrace(3)) { t.Fatal("not exact composed request sequence") }
				}
				if boundary == "after_terminal" {
					if first := f.s.CheckLifetime(); first != nil || f.s.state.phase != 4 { t.Fatal("terminal hash prerequisite", first) }
				}
				// Explicit synchronous API boundary, NOT a paused worker, watcher
				// completion, original gate.Close, run cancel or Native phase seam.
				switch action {
				case "caller_cancel": cancel()
				case "session_close": f.s.Close()
				case "lifetime_revoke": f.a.lifetime.Revoke()
				}
				var first error
				switch boundary {
				case "after_S0": _, first = f.s.PrivateContext()
				case "after_private": _, first = f.s.Finish(visited)
				case "after_S2": first = f.s.CheckLifetime()
				case "after_terminal": first = f.s.ConsumeExpiryQualificationRefusal()
				}
				// PrivateContext is a scalar phase/binding operation and does NOT
				// poll lifetime. Completed Revoke is detected at the next real RPC.
				if boundary == "after_S0" && action == "lifetime_revoke" {
					if first != nil { t.Fatal("unexpected lifetime work at PrivateContext", first) }
					_, first = f.a.Get(f.s.state.ctx, f.private[0])
				}
				f.cannotConsume(t, first)
			})
		}
	}
}

// Honest in-flight component callbacks on existing fresh Unix request frames.
// Actions finish in the server callback before its first reply. There is no
// paused lease-owning worker, no watcher wait, sleep/poll or automatic resume.
// These actions are NOT native runCancel/gateClose proxies.
func TestRetainedExpiryComposedEarlyFrameCutoffs(t *testing.T) {
	for _, site := range []string{"S0", "publication_read", "private_read", "S1", "S2"} {
		for _, action := range []string{"caller_cancel", "session_close", "lifetime_revoke"} {
			t.Run(site+"/"+action, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				f := w4ComposedNew(t, ctx, w4ComposedACKPair(t, "early-frame", 7, 29), 1)
				var injections atomic.Int32
				f.reply = func(_ *net.UnixConn, request retainedRequest, phase int, key string, raw []byte) []byte {
					target := request.Operation == "status" && (site == "S0" && phase == 0 || site == "S1" && phase == 1 || site == "S2" && phase == 2) ||
						request.Operation == "get" && (site == "publication_read" && key == f.s.state.expected[0].key || site == "private_read" && key == f.private[0])
					if target {
						injections.Add(1)
						switch action {
						case "caller_cancel": cancel()
						case "session_close": f.s.Close()
						case "lifetime_revoke": f.a.lifetime.Revoke()
						}
					}
					return raw
				}
				f.arm(t)
				first := f.s.ReadPublications()
				if site != "S0" && site != "publication_read" {
					if first != nil { t.Fatal("valid exact-publication prefix", first) }
					private, err := f.s.PrivateContext()
					if err != nil { t.Fatal(err) }
					visited := make(map[string]bool)
					for _, key := range f.private {
						record, err := f.a.Get(private, key)
						if err != nil { first = err; break }
						if record.Key != key || visited[key] || !bytes.Equal(record.Value, f.rows[key].Value) { t.Fatal("inexact private fixture read") }
						visited[key] = true
					}
					if site != "private_read" {
						if first != nil { t.Fatal("valid private-read prefix", first) }
						publications, err := f.s.Finish(uint64(len(visited)))
						first = err
						if publications != 0 { t.Fatal("partial positive bookkeeping") }
					}
				}
				if first == nil || injections.Load() != 1 || f.s.state.expiry.s2Closed || f.s.state.budget.terminal || f.s.state.expiry.s1Mismatch != (site == "S2") {
					t.Fatal("early frame cutoff did not refuse at required component stage", first)
				}
				// FIRST wrapped transport diagnostic is retained locally; public
				// normalization belongs to the Host, not this component helper.
				f.cannotConsume(t, first)
			})
		}
	}
}

func TestRetainedExpiryComposedSecondCompleteFrameIsProtocolRefusal(t *testing.T) {
	f := w4ComposedNew(t, t.Context(), w4ComposedACKPair(t, "second-frame", 7, 29), 1)
	writes := make(chan retainedUnaryWrite, 1)
	f.reply = func(conn *net.UnixConn, request retainedRequest, phase int, _ string, raw []byte) []byte {
		if request.Operation == "status" && phase == 1 {
			// Neither frame is admitted: the first simulated inequality plus a
			// second complete equality fails the stream boundary before decode.
			status := retainedTestStatus()
			status.CurrentRevision, status.DurableThroughRevision = 100, 100
			status.RecordCount = len(f.rows)
			retainedUnaryWritePair(conn, raw, retainedSuccess(t, request, status), writes)
			return nil
		}
		return raw
	}
	f.arm(t)
	if err := f.s.ReadPublications(); err != nil { t.Fatal(err) }
	visited := f.visit(t)
	pubs, first := f.s.Finish(visited)
	retainedUnaryReceiveWrite(t, writes)
	if pubs != 0 || first != errRetainedObservation || f.s.state.expiry.s1Mismatch || f.s.state.expiry.s2Closed || f.s.state.countMismatch || f.s.state.budget.terminal || !f.s.state.invalid.Load() { t.Fatal("extra frame classified an unauthenticated S1", first) }
	if !reflect.DeepEqual(f.snapshotTrace(), f.wantTrace(2)) || f.accepted.Load() != int32(len(f.wantTrace(2))) { t.Fatal("protocol refusal attempted S2/retry") }
	requests := f.s.state.transport.requests.Load()
	f.cannotConsume(t, first)
	if later, err := f.s.Finish(visited); later != 0 || err == nil { t.Fatal("failed stream renewed Finish", later, err) }
	if f.s.state.transport.requests.Load() != requests || !reflect.DeepEqual(f.snapshotTrace(), f.wantTrace(2)) || f.accepted.Load() != int32(len(f.wantTrace(2))) { t.Fatal("Consume/CheckLifetime/Close/Finish renewed S2 or an RPC") }
	// Simulated framing refusal only: no provider/Host or lifetime-finality claim.
}

