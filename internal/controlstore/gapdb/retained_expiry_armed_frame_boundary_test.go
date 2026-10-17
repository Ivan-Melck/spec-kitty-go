//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"io"
	"net"
	"reflect"
	"testing"
)

// SOURCE ONLY / UNRUN. Component framing with a real Unix socket and self-pidfd,
// NOT provider expiry, Native Host, admission, or outer/run/gate finality.
// The existing 52 required-failure cases and their fault table are unchanged.
func TestRetainedExpiryArmedSecondCompleteFrameS0IsProtocolRefusal(t *testing.T) {
	retainedExpiryArmedSecondCompleteFrame(t, 0)
}

func TestRetainedExpiryArmedSecondCompleteFrameS1IsProtocolRefusal(t *testing.T) {
	retainedExpiryArmedSecondCompleteFrame(t, 1)
}

func TestRetainedExpiryArmedSecondCompleteFrameS2IsProtocolRefusal(t *testing.T) {
	retainedExpiryArmedSecondCompleteFrame(t, 2)
}

// Validate the fault's prerequisites independently of stream admission. Both
// payloads are the SAME valid, phase-authentic status, not malformed decoys.
// This isolated decode does not charge/alter the observation's budget or mark
// any observation outcome. The actual exchange must still refuse before decode.
func (f *retainedExpiryRequiredFixture) writeArmedCompletePair(t *testing.T, conn *net.UnixConn, request retainedRequest, phase int, reply []byte) {
	t.Helper()
	tr := f.s.state.transport
	want := retainedTestStatus()
	want.CurrentRevision, want.DurableThroughRevision = 100, 100
	want.RecordCount = len(f.s.state.expected) + 6
	if phase == 1 && f.mismatch {
		want.RecordCount++
	}
	decoded, err := tr.decode(reply, request, "")
	if request.Operation != "status" || phase != f.phase || phase < 0 || phase > 2 || !f.mismatch || err != nil || !reflect.DeepEqual(decoded, retainedResult{status: want}) {
		t.Error("pair FIRST payload is not the exact valid target-phase status", phase, err)
	}
	if want.DatabaseID != string(f.s.state.captured.DatabaseID()) || want.DatabaseID != tr.database || want.OwnerPID != tr.ownerPID || want.OwnerStartedAt != tr.ownerStarted || uint64(want.CurrentRevision) != f.s.state.captured.CurrentRevision() || uint64(want.DurableThroughRevision) != f.s.state.captured.DurableThroughRevision() {
		t.Error("pair status has invalid immutable owner/database/captured coordinates")
	}
	// Intentional request EOF, not a cancellation witness. No sleep, polling,
	// quiet-absence inference, replacement context or transport is involved.
	var extra [1]byte
	if n, err := conn.Read(extra[:]); n != 0 || err != io.EOF {
		t.Error("pair request did not actually end", n, err)
	}
	// Both complete length-prefixed frames are queued in ONE actual Write.
	// Its exact n/want/error are sent even on failure; no second write/repair.
	// Returning nil leaves write-half shutdown UNATTEMPTED in the base fixture;
	// its separately checked FIRST full close remains owned by that fixture.
	retainedUnaryWritePair(conn, reply, reply, f.pairWrites)
}

func retainedExpiryArmedSecondCompleteFrame(t *testing.T, phase int) {
	t.Helper()
	f := retainedExpiryRequiredNew(t, phase, "second_complete_frame", true)
	f.arm(t)
	if f.s.state.expiry.consumed || f.s.state.expiry.s1Mismatch || f.s.state.expiry.s2Closed || f.s.state.countMismatch || f.s.state.budget.terminal {
		t.Fatal("fresh canonical armed prerequisite already classified/completed")
	}
	var private context.Context
	if phase > 0 {
		private = f.private(t) // actual valid S0, all exact publications, six private Get RPCs
		if f.s.state.phase != 2 || f.s.state.invalid.Load() || private.Err() != nil || f.s.state.countMismatch || f.s.state.expiry.s1Mismatch {
			t.Fatal("valid complete pre-S1 prefix missing")
		}
	}
	var publications uint64
	var first error
	if phase == 0 {
		first = f.s.ReadPublications()
	} else {
		publications, first = f.s.Finish(6)
	}
	// A caller refusal alone is insufficient: the server must acknowledge a
	// successful complete pair Write. A short/failed write cannot turn green.
	retainedUnaryReceiveWrite(t, f.pairWrites)
	retainedEOFWait(t, f.faultEnd)
	statuses := phase + 1
	attempts := uint64(statuses)
	if phase > 0 {
		attempts += uint64(len(f.s.state.expected)) + 6
	}
	f.trace(t, statuses, attempts) // full ordered fresh-RPC prefix, exact accepted count
	f.mu.Lock()
	injected := f.injected
	last := f.events[len(f.events)-1]
	f.mu.Unlock()
	wantPhase := uint8(3)
	if phase == 0 {
		wantPhase = 1
	}
	if publications != 0 || first != errRetainedObservation || injected != 1 || last.request.Operation != "status" || f.s.state.phase != wantPhase || !f.s.state.invalid.Load() || f.s.state.ctx.Err() != context.Canceled {
		t.Fatal("target complete pair did not refuse with zero publication result at its exact phase", phase, publications, first, injected)
	}
	if private != nil && private.Err() != context.Canceled {
		t.Fatal("failed pair did not cancel its original private context")
	}
	wantMismatch := phase == 2
	if f.s.state.countMismatch != wantMismatch || f.s.state.expiry.s1Mismatch != wantMismatch {
		t.Fatal("S0/S1 stream classified a count, or S2 lacks prior legitimate S1 inequality")
	}
	if !f.s.state.expiry.armed || f.s.state.expiry.handle != &f.s || f.s.state.expiry.s2Closed || f.s.state.expiry.consumed || f.s.state.budget.terminal {
		t.Fatal("bad stream closed/completed/consumed the armed negative")
	}
	b := f.s.state.budget
	if b.hashed != b.image*3*attempts || f.s.state.transport.requests.Load() != attempts {
		t.Fatal("target failure refunded work or attempted another request")
	}
	beforeAttempts, beforeHash, beforeFrames := b.attempts, b.hashed, b.frames
	beforeAccepted, beforeRequests := f.accepted.Load(), f.s.state.transport.requests.Load()
	// Includes two Consume refusals and checked cached FIRST transport Close.
	// It never substitutes a later successful Close for an earlier failure.
	f.refusesWithoutRenewal(t, first)
	for i := 0; i < 2; i++ {
		if pubs, err := f.s.Finish(6); pubs != 0 || err != errRetainedObservation {
			t.Fatal("repeated Finish exposed partial bookkeeping or renewed work", pubs, err)
		}
		if err := f.s.CheckLifetime(); err != errRetainedObservation {
			t.Fatal("repeated CheckLifetime renewed terminal work", err)
		}
		if err := f.s.ConsumeExpiryQualificationRefusal(); err != errRetainedObservation {
			t.Fatal("repeated Consume accepted a failed stream", err)
		}
		f.s.Close()
	}
	if b.attempts != beforeAttempts || b.hashed != beforeHash || b.frames != beforeFrames || f.accepted.Load() != beforeAccepted || f.s.state.transport.requests.Load() != beforeRequests || b.terminal || f.s.state.expiry.s2Closed || f.s.state.expiry.consumed || f.s.state.countMismatch != wantMismatch || f.s.state.expiry.s1Mismatch != wantMismatch {
		t.Fatal("Finish/CheckLifetime/Consume/Close renewed or completed the failed negative")
	}
	f.trace(t, statuses, attempts) // still ends at named S0/S1/S2, no retry or next phase
}
