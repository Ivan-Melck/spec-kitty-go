//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"golang.org/x/sys/unix"
)

// Family 13 supplement: SIMULATED Unix endpoint + self-pidfd only. No native
// Host, admission, provider expiry, outer Host closure or public Host proof is
// exercised. The peer case makes the actual connected descriptor uninspectable;
// it does NOT claim a foreign-PID peer. The image case poisons a test-owned
// expected digest, not the running executable. Both use the real postcheck.
// Duplicate members and any byte after the first response frame are refused.
// Historical frame-reader isolation is narrower than owned stream admission.
type retainedExpiryRequiredEvent struct {
	request retainedRequest
	key     string
	node    *retainedConnectionNode
	conn    *net.UnixConn
}

type retainedExpiryRequiredFixture struct {
	a         *Adapter
	s         RetainedObservationV2 // canonical address: never return/copy a handle
	accepted  *atomic.Int32
	proxy     *retainedRequiredSocket
	cleanup   *retainedRequiredFixtureCleanup
	peerClose *retainedRequiredClose
	mu        sync.Mutex
	events    []retainedExpiryRequiredEvent
	statuses  int
	injected  int
	phase     int // 0=S0, 1=S1, 2=S2; -1=no fault
	fault     string
	mismatch  bool
	faultEnd  chan struct{}
	pairWrites chan retainedUnaryWrite // actual one-Write outcome; armed frame supplement only
	timeoutRelease chan struct{}
	releaseTimeout sync.Once
}

func retainedExpiryRequiredNew(t *testing.T, phase int, fault string, mismatch bool) *retainedExpiryRequiredFixture {
	t.Helper()
	f := &retainedExpiryRequiredFixture{phase: phase, fault: fault, mismatch: mismatch, faultEnd: make(chan struct{}), pairWrites: make(chan retainedUnaryWrite, 1), timeoutRelease: make(chan struct{})}
	acks := []journalv2.AcknowledgedPublicationV2{retainedACKV2(t, "required-armed-phase-simulation", 7)}
	rows := retainedCopiedOracleV2(t, acks)
	transport, lifetime, accepted, cleanup := retainedRequiredFixtureOwned(t, func(conn *net.UnixConn, request retainedRequest) []byte {
		f.mu.Lock()
		var key string
		if request.Operation == "get" {
			raw, _ := json.Marshal(request.Arguments)
			var args struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				t.Error(err)
			}
			key = args.Key
		}
		tr := f.s.state.transport
		tr.mu.Lock()
		node := tr.active
		if node == nil || node.next != nil || node.conn == nil {
			t.Error("fixture must see exactly one active fresh RPC")
			tr.mu.Unlock()
			f.mu.Unlock()
			return nil
		}
		f.events = append(f.events, retainedExpiryRequiredEvent{request, key, node, node.conn})
		tr.mu.Unlock()
		if request.Operation != "status" {
			f.mu.Unlock()
			r := gapdbapi.Record{Key: key, Value: []byte(`{"private":"simulation"}`), Revision: 1}
			if row, ok := rows[key]; ok {
				r.Value, r.Revision = row.canonical, gapdbapi.Revision(row.revision)
			}
			return retainedSuccess(t, request, map[string]any{"record": r})
		}
		phase := f.statuses
		f.statuses++
		inject := phase == f.phase && f.fault != ""
		// A proxy-path failure is injected BEFORE the target exchange. At S2
		// its boundary is the preceding valid S1 reply, inside Finish.
		proxyBoundary := f.fault == "proxy_path" && f.phase == 2 && phase == 1
		if inject && f.fault != "proxy_path" || proxyBoundary {
			f.injected++
		}
		f.mu.Unlock()
		s := retainedTestStatus()
		s.CurrentRevision, s.DurableThroughRevision = 100, 100
		s.RecordCount = len(rows) + 6
		if phase == 1 && f.mismatch {
			s.RecordCount++ // even a bad S1 must NOT classify this inequality
		}
		reply := retainedSuccess(t, request, s)
		if proxyBoundary {
			// registerConnection and deferred unlink both hold this mutex;
			// mutation is ordered between them, before S2's endpoint read.
			tr.mu.Lock()
			tr.endpoint += "-proxy"
			tr.mu.Unlock()
			close(f.faultEnd)
			return reply
		}
		if !inject || f.fault == "proxy_path" {
			return reply
		}
		defer close(f.faultEnd)
		switch f.fault {
		case "second_complete_frame":
			f.writeArmedCompletePair(t, conn, request, phase, reply)
			return nil // pair write acknowledged separately; fixture owns FIRST full close
		case "remote_error":
			var remote map[string]any
			for _, d := range gapdbapi.ErrorDefinitions() {
				if d.Code == gapdbapi.CodeNotFound {
					remote = map[string]any{"code": d.Code, "message": "SECRET remote diagnostic", "retry": d.Retry, "safe_actions": d.SafeActions, "key": "SECRET-key", "current_revision": 100}
				}
			}
			reply = retainedJSON(t, map[string]any{"schema_version": AdmittedWireVersion, "ok": false, "database_id": s.DatabaseID, "operation": request.Operation, "request_id": request.RequestID, "error": remote})
			// Prerequisite: this is a VALID remote error, not a malformed
			// decoder input accidentally advertised as remote-error coverage.
			var got *gapdbapi.Error
			if _, err := tr.decode(reply, request, ""); !errors.As(err, &got) || got.Code != gapdbapi.CodeNotFound {
				t.Error("invalid remote-error fixture", err)
			}
			return reply
		case "malformed_frame_length":
			if n, err := conn.Write([]byte{0, 0, 0, 0}); n != 4 || err != nil {
				t.Error("malformed header was not actually written", n, err)
			}
			return nil
		case "partial_frame_EOF":
			if n, err := conn.Write([]byte{0, 0, 0, 8, '{'}); n != 5 || err != nil {
				t.Error("partial frame was not actually written", n, err)
			}
			return nil
		case "missing_frame_timeout":
			// Request EOF is intentional, not a timeout witness. Keep the
			// response open until the caller observes its actual deadline.
			<-f.timeoutRelease
			return nil
		case "EOF":
			return nil // existing fixture closes without any response bytes
		case "duplicate_member":
			return append(append([]byte{}, reply[:len(reply)-1]...), []byte(`,"ok":true}`)...)
		case "wrong_request_echo":
			return mutateRetainedJSON(t, reply, "request_id", json.RawMessage(`"SECRET-other-request"`))
		case "wrong_operation_echo":
			return mutateRetainedJSON(t, reply, "operation", json.RawMessage(`"get"`))
		case "wrong_database_envelope":
			return mutateRetainedJSON(t, reply, "database_id", json.RawMessage(`"SECRET-other-db"`))
		case "malformed_JSON":
			return []byte(`{"schema_version":`)
		case "wrong_JSON_type":
			return mutateRetainedJSON(t, reply, "result", json.RawMessage(`[]`))
		case "peer_descriptor_closed":
			// Real VerifyConnection postcheck cannot inspect this connection.
			// Do not substitute a wrong status OwnerPID for peer inspection.
			tr.mu.Lock()
			owner := node.owner
			tr.mu.Unlock()
			if owner == nil { t.Error("actual connection lacks its FIRST close owner"); return nil }
			// Publish the intentional physical close through the SAME owner
			// used by cancellation and deferred cleanup, outside transport.mu.
			f.peerClose = &retainedRequiredClose{close: func() error { return tr.closeConnection(owner) }}
			retainedRequiredCheckClose(t, "intentionally closed client peer descriptor", f.peerClose)
			// Later cleanup joins this actual cached FIRST outcome; it must
			// neither reclose the connection nor suppress a genuine close error.
			return reply
		case "socket_replaced":
			if err := f.cleanup.socket.replace(t); err != nil {
				t.Error("identity-safe socket replacement", err)
				return nil
			}
			return reply
		case "executed_image_digest":
			retainedExpiryRequiredPoisonImage(t, tr)
			return reply
		default:
			t.Error("unknown fixture fault", f.fault)
			return nil
		}
	})
	f.accepted, f.cleanup = accepted, cleanup
	if fault == "proxy_path" {
		var err error
		f.proxy, err = retainedRequiredListen(transport.endpoint+"-proxy", cleanup.socket.dir)
		t.Cleanup(func() { f.proxy.remove(t) })
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := newAdapter(&ownedProvider{provider: &fakeProvider{}, transport: transport}, transport.endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.lifetime = lifetime
	f.a = a
	capture, err := journalv2.NewStoreRevisionV2("owned-test-db", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	f.s, err = a.PrepareRetainedObservationV2(t.Context(), capture, acks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.s.Close)
	t.Cleanup(func() { f.releaseTimeout.Do(func() { close(f.timeoutRelease) }) })
	return f
}

// Deliberate TEST-ONLY corruption of this fixture's immutable expected digest.
// No production hook/provider/callback/selector is added. Use the lifetime's
// existing mutex: the real postcheck must acquire it and hash the executed
// /proc/self/exe bytes before discovering the mismatch. This is NOT an actual
// exec/image-inode substitution or foreign-process image qualification.
func retainedExpiryRequiredPoisonImage(t *testing.T, tr *retainedTransport) {
	t.Helper()
	v := reflect.ValueOf(tr.lifetime).Elem()
	mu := (*sync.Mutex)(unsafe.Pointer(v.FieldByName("mu").Addr().UnsafePointer()))
	mu.Lock()
	defer mu.Unlock()
	digest := v.FieldByName("anchor").FieldByName("ImageSHA256")
	p := (*string)(unsafe.Pointer(digest.Addr().UnsafePointer()))
	*p = strings.Repeat("0", 64)
}

func (f *retainedExpiryRequiredFixture) arm(t *testing.T) {
	t.Helper()
	if f.s.state.phase != 0 || f.s.state.budget.attempts != 0 || f.accepted.Load() != 0 {
		t.Fatal("not a fresh phase-zero session")
	}
	if err := f.s.ArmExpiryQualificationRefusal(); err != nil {
		t.Fatal(err)
	}
	if f.s.state.expiry.handle != &f.s || !f.s.state.expiry.armed || f.s.state.phase != 0 {
		t.Fatal("canonical Arm(0) prerequisite missing")
	}
}

func (f *retainedExpiryRequiredFixture) private(t *testing.T) context.Context {
	t.Helper()
	if err := f.s.ReadPublications(); err != nil {
		t.Fatal("valid S0/exact-publication prerequisite", err)
	}
	ctx, err := f.s.PrivateContext()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		key := "required-private-simulation-" + strconv.Itoa(i)
		r, err := f.a.Get(ctx, key)
		if err != nil || r.Key != key || !bytes.Equal(r.Value, []byte(`{"private":"simulation"}`)) || r.Revision != 1 || r.ExpiresAt != nil {
			t.Fatal("valid exact private-read prerequisite", err)
		}
	}
	return ctx
}

// Assert the ENTIRE ordered request trace, not merely a decoder error or a
// parent test name. All exact publications and all six private reads must
// precede S1, and a valid authenticated inequality must precede attempted S2.
func (f *retainedExpiryRequiredFixture) trace(t *testing.T, statuses int, attempts uint64) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var operations, keys []string
	if statuses > 0 {
		operations, keys = append(operations, "status"), append(keys, "")
	}
	if f.phase != 0 || f.fault == "" {
		for _, r := range f.s.state.expected {
			operations, keys = append(operations, "get"), append(keys, r.key)
		}
		for i := 0; i < 6; i++ {
			operations, keys = append(operations, "get"), append(keys, "required-private-simulation-"+strconv.Itoa(i))
		}
		for i := 1; i < statuses; i++ {
			operations, keys = append(operations, "status"), append(keys, "")
		}
	}
	if len(f.events) != len(operations) || f.statuses != statuses || f.accepted.Load() != int32(len(operations)) || f.s.state.budget.attempts != attempts {
		t.Fatalf("phase correlation: events=%d expected=%d statuses=%d expected=%d accepted=%d attempts=%d expected=%d", len(f.events), len(operations), f.statuses, statuses, f.accepted.Load(), f.s.state.budget.attempts, attempts)
	}
	seenNodes, seenConns := map[*retainedConnectionNode]bool{}, map[*net.UnixConn]bool{}
	tr := f.s.state.transport
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for i, e := range f.events {
		if e.request.Operation != operations[i] || e.key != keys[i] || e.request.RequestID != "owned-read-"+strconv.Itoa(i+1) || seenNodes[e.node] || seenConns[e.conn] {
			t.Fatal("not the exact ordered, fresh single-request connection trace", i)
		}
		seenNodes[e.node], seenConns[e.conn] = true, true
		retainedRegistryDetached(t, e.node)
		if _, err := e.conn.Write([]byte{0}); !errors.Is(err, net.ErrClosed) {
			t.Fatal("fresh client socket not closed", i, err)
		}
	}
	if retainedRegistryCountLocked(t, tr) != 0 {
		t.Fatal("connection registration retained after failed/successful RPC")
	}
	if f.peerClose != nil && (f.peerClose.Close() != nil || f.peerClose.calls.Load() != 1) {
		t.Fatal("intentional peer close lost/retried its actual first outcome")
	}
	if f.proxy != nil {
		// The forbidden alternate endpoint really EXISTS. Refusing a missing
		// pathname at dial would not establish an endpoint-authentication cut.
		raw, err := f.proxy.listener.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var acceptErr, acceptedCloseErr error
		acceptedFD := false
		if err := raw.Control(func(fd uintptr) {
			for i := 0; i < 64; i++ {
				accepted, _, err := unix.Accept4(int(fd), unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
				if err == unix.EINTR {
					continue
				}
				acceptErr = err
				if accepted >= 0 {
					acceptedFD = true
					acceptedCloseErr = unix.Close(accepted) // actual FIRST close; no retry
				}
				break
			}
		}); err != nil || acceptErr != unix.EAGAIN || acceptedFD || acceptedCloseErr != nil {
			t.Fatalf("proxy queue: control=%v accept=%v acceptedFD=%v FIRST accepted-FD close=%v (residual if nonnil)", err, acceptErr, acceptedFD, acceptedCloseErr)
		}
	}
}

func (f *retainedExpiryRequiredFixture) refusesWithoutRenewal(t *testing.T, first error) {
	t.Helper()
	if first != nil && strings.Contains(first.Error(), "SECRET") {
		t.Fatal("first refusal diagnostic leaked", first)
	}
	b := f.s.state.budget
	attempts, hashed, frames, accepted, requests := b.attempts, b.hashed, b.frames, f.accepted.Load(), f.s.state.transport.requests.Load()
	for i := 0; i < 2; i++ {
		err := f.s.ConsumeExpiryQualificationRefusal()
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("failure consumed/renewed a completed negative", err)
		}
		if first == nil {
			first = err // omitted-terminal control first fails here
		}
	}
	if !f.s.state.invalid.Load() || f.s.state.ctx.Err() == nil || f.s.state.expiry.consumed {
		t.Fatal("refusal not irreversibly canceled; completed negative escaped")
	}
	if pubs, err := f.s.Finish(6); pubs != 0 || err == nil {
		t.Fatal("failed session returned positive publication bookkeeping", pubs, err)
	}
	if err := f.s.CheckLifetime(); err == nil {
		t.Fatal("failed session renewed terminal work")
	}
	f.s.Close()
	f.s.Close()
	if b.attempts != attempts || b.hashed != hashed || b.frames != frames || f.accepted.Load() != accepted || f.s.state.transport.requests.Load() != requests {
		t.Fatal("Consume/Finish/CheckLifetime/Close renewed failed work")
	}
	// Preserve the operation refusal SEPARATELY from the actual first owned
	// transport close. This real transport returns nil; this is NOT a claim
	// of production close-failure retention from an unchanged local variable.
	firstClose := f.cleanup.transportClose.Close()
	if firstClose != nil {
		t.Errorf("FIRST transport close failed; residual/unqualified cleanup: %v", firstClose)
	}
	laterClose := f.cleanup.transportClose.Close()
	if laterClose != firstClose || f.cleanup.transportClose.calls.Load() != 1 {
		t.Fatal("cached transport close lost/retried first outcome", firstClose, laterClose)
	}
	if first != errRetainedObservation && !errors.Is(first, context.DeadlineExceeded) {
		t.Fatal("operation refusal changed independently of cleanup", first)
	}
}

func TestRetainedExpiryRequiredFailuresSimulatedUnixSelfPIDFD(t *testing.T) {
	faults := []string{"remote_error", "malformed_frame_length", "partial_frame_EOF", "missing_frame_timeout", "EOF", "duplicate_member", "wrong_request_echo", "wrong_operation_echo", "wrong_database_envelope", "malformed_JSON", "wrong_JSON_type", "peer_descriptor_closed", "socket_replaced", "proxy_path", "executed_image_digest"}
	for phase := 0; phase < 3; phase++ {
		for _, fault := range faults {
			t.Run("S"+strconv.Itoa(phase)+"/"+fault, func(t *testing.T) {
				f := retainedExpiryRequiredNew(t, phase, fault, true)
				f.arm(t)
				if fault == "missing_frame_timeout" {
					f.s.state.transport.timeout = 200 * time.Millisecond
				}
				if phase > 0 {
					f.private(t)
				}
				if fault == "proxy_path" && phase < 2 {
					f.s.state.transport.endpoint += "-proxy"
					f.injected++
					close(f.faultEnd)
				}
				var first error
				if phase == 0 {
					first = f.s.ReadPublications()
				} else {
					var pubs uint64
					pubs, first = f.s.Finish(6)
					if pubs != 0 {
						t.Fatal("failed armed Finish returned positive bookkeeping", pubs)
					}
				}
				if fault == "missing_frame_timeout" {
					if !errors.Is(first, context.DeadlineExceeded) { t.Fatal("missing-frame release requires the actual caller-observed deadline", first) }
					f.releaseTimeout.Do(func() { close(f.timeoutRelease) })
				}
				select {
				case <-f.faultEnd:
				case <-time.After(3 * time.Second):
					t.Fatal("target phase's fault was never reached")
				}
				statuses := phase + 1
				attempts := uint64(statuses)
				if phase > 0 {
					attempts += uint64(len(f.s.state.expected)) + 6
				}
				if fault == "proxy_path" {
					statuses-- // rejected endpoint: reserved RPC, no wire request
				}
				f.trace(t, statuses, attempts)
				f.mu.Lock()
				injected := f.injected
				f.mu.Unlock()
				wantPhase := uint8(3)
				if phase == 0 {
					wantPhase = 1
				}
				validError := first == errRetainedObservation
				if fault == "missing_frame_timeout" {
					validError = errors.Is(first, context.DeadlineExceeded)
				}
				if !validError || injected != 1 || f.s.state.phase != wantPhase || !f.s.state.invalid.Load() || f.s.state.expiry.s2Closed || f.s.state.budget.terminal {
					t.Fatal("failed exact phase became completed negative", first, injected)
				}
				wantMismatch := phase == 2
				if f.s.state.countMismatch != wantMismatch || f.s.state.expiry.s1Mismatch != wantMismatch {
					t.Fatal("unauthenticated S1 classified, or valid S1 prerequisite missing")
				}
				if f.s.state.budget.hashed != f.s.state.budget.image*3*attempts {
					t.Fatal("failed RPC's reserved work was refunded/renewed")
				}
				f.refusesWithoutRenewal(t, first)
			})
		}
	}
}

func TestRetainedExpiryRequiredPrerequisitesSimulatedUnixSelfPIDFD(t *testing.T) {
	for _, armed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unarmed_exact_positive", true: "canonical_armed_closed_negative"}[armed], func(t *testing.T) {
			f := retainedExpiryRequiredNew(t, -1, "", armed)
			if armed {
				f.arm(t)
			}
			private := f.private(t)
			pubs, err := f.s.Finish(6)
			if armed {
				if pubs != 0 || err != errRetainedObservation || !f.s.state.expiry.s2Closed || !f.s.state.countMismatch || !f.s.state.expiry.s1Mismatch {
					t.Fatal("valid closed-negative prerequisite absent", pubs, err)
				}
			} else if err != nil || pubs != uint64(len(f.s.state.expected)) {
				t.Fatal("valid exact-positive prerequisite absent", pubs, err)
			}
			attempts := uint64(len(f.s.state.expected)) + 6 + 3
			f.trace(t, 3, attempts)
			if err := f.s.CheckLifetime(); err != nil || !f.s.state.budget.terminal || f.s.state.phase != 4 || f.s.state.invalid.Load() || private.Err() != nil {
				t.Fatal("successful reserved terminal hash prerequisite absent", err)
			}
			if f.s.state.budget.hashed != f.s.state.budget.image*(3*attempts+1) {
				t.Fatal("terminal work accounting")
			}
			if armed {
				if err := f.s.ConsumeExpiryQualificationRefusal(); err != nil || !f.s.state.expiry.consumed || private.Err() != context.Canceled || t.Context().Err() != nil {
					t.Fatal("valid canonical negative did not consume independently", err)
				}
			}
		})
	}
}

func TestRetainedExpiryRequiredTerminalFlagSimulatedUnixSelfPIDFD(t *testing.T) {
	for _, mode := range []string{"omitted_CheckLifetime", "failed_CheckLifetime_image", "failed_CheckLifetime_preexisting_flag"} {
		t.Run(mode, func(t *testing.T) {
			f := retainedExpiryRequiredNew(t, -1, "", true)
			f.arm(t)
			f.private(t)
			if pubs, err := f.s.Finish(6); pubs != 0 || err != errRetainedObservation || !f.s.state.expiry.s2Closed || f.s.state.invalid.Load() || f.s.state.phase != 3 {
				t.Fatal("valid S1 inequality and real S2 prerequisite absent", pubs, err)
			}
			f.trace(t, 3, uint64(len(f.s.state.expected))+9)
			b := f.s.state.budget
			beforeHash := b.hashed
			if mode == "failed_CheckLifetime_image" {
				retainedExpiryRequiredPoisonImage(t, f.s.state.transport)
			} else {
				b.mu.Lock()
				b.terminal = true // no successful terminal hash/check occurred
				b.mu.Unlock()
			}
			var first error
			if mode != "omitted_CheckLifetime" {
				first = f.s.CheckLifetime()
				if first != errRetainedObservation || f.s.state.phase != 4 || !f.s.state.invalid.Load() {
					t.Fatal("terminal failure was not actually reached", first)
				}
			}
			wantHash := beforeHash
			if mode == "failed_CheckLifetime_image" {
				wantHash += b.image // reserved flag/hash can precede a FAILED check
			}
			if !b.terminal || b.hashed != wantHash {
				t.Fatal("fixture did not retain terminal=true without successful check")
			}
			f.refusesWithoutRenewal(t, first)
		})
	}
}

func TestRetainedExpiryRequiredStaleDiagnosticSimulatedUnixSelfPIDFD(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_canceled_S1", true: "completed_negative_canceled_before_consume"}[completed], func(t *testing.T) {
			f := retainedExpiryRequiredNew(t, -1, "", true)
			if completed {
				f.arm(t)
			}
			f.private(t)
			pubs, first := f.s.Finish(6)
			if pubs != 0 || first != errRetainedObservation || !f.s.state.countMismatch || f.s.state.expiry.s1Mismatch != completed || f.s.state.expiry.s2Closed != completed {
				t.Fatal("stale-diagnostic prerequisite absent", pubs, first)
			}
			statuses := 2
			if completed {
				statuses++
				if err := f.s.CheckLifetime(); err != nil || f.s.state.invalid.Load() || !f.s.state.budget.terminal {
					t.Fatal("completed canonical negative prerequisite absent", err)
				}
				f.s.state.cancel()
			} else if !f.s.state.invalid.Load() || f.s.state.budget.terminal {
				t.Fatal("ordinary S1 must already have canceled without terminal work")
			}
			f.trace(t, statuses, uint64(len(f.s.state.expected)+6+statuses))
			if f.s.state.ctx.Err() == nil || !f.s.state.countMismatch {
				t.Fatal("old diagnostic must survive cancellation for this control")
			}
			before := f.s.state.budget.attempts
			if err := f.s.ArmExpiryQualificationRefusal(); err == nil || f.s.state.expiry.armed != completed || f.s.state.budget.attempts != before {
				t.Fatal("stale canceled diagnostic rearmed/revived a negative", err)
			}
			f.refusesWithoutRenewal(t, first)
		})
	}
}
