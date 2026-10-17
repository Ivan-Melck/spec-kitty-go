package gapdb

import (
	"bytes"
	"context"
	"errors"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

// The pure frame-reader test preserves the historical narrower parser contract:
// it consumes one frame and leaves the next unread. It is NOT the public owned
// transport contract. Fresh owned exchanges now require actual response EOF;
// the simulated Unix cases below refuse extra frames, without Host admission.
func TestRetainedUnaryFrameReaderLeavesSecondFrameUntouched(t *testing.T) {
	first, second := []byte(`{"first":true}`), []byte(`{"second":true}`)
	var wire, tail bytes.Buffer
	if err := retainedWriteFrame(&wire, first); err != nil {
		t.Fatal(err)
	}
	if err := retainedWriteFrame(&tail, second); err != nil {
		t.Fatal(err)
	}
	wire.Write(tail.Bytes())
	got, err := retainedReadFrameContext(context.Background(), &wire)
	if err != nil || !bytes.Equal(got, first) || !bytes.Equal(wire.Bytes(), tail.Bytes()) {
		t.Fatal("one frame must consume exactly its header/payload, not the next frame")
	}
}

type retainedUnaryWrite struct {
	n, want int
	err     error
}

func retainedUnaryReceiveWrite(t *testing.T, writes <-chan retainedUnaryWrite) {
	t.Helper()
	select {
	case w := <-writes:
		if w.err != nil || w.n != w.want {
			t.Fatalf("fixture did not queue BOTH complete frames: n=%d want=%d err=%v", w.n, w.want, w.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fixture writer outcome missing")
	}
}

func retainedUnaryWritePair(conn *net.UnixConn, first, second []byte, writes chan<- retainedUnaryWrite) {
	var wire bytes.Buffer
	// bytes.Buffer writes cannot fail; each bounded pair is queued in one Write
	// so a subsequent peer close cannot silently manufacture a partial witness.
	_ = retainedWriteFrame(&wire, first)
	_ = retainedWriteFrame(&wire, second)
	n, err := conn.Write(wire.Bytes())
	writes <- retainedUnaryWrite{n, wire.Len(), err}
}

func TestRetainedUnaryFrameSecondReplyCannotBecomeNextRPC(t *testing.T) {
	writes := make(chan retainedUnaryWrite, 2)
	var served atomic.Uint64
	transport, _, accepted := retainedFixture(t, func(conn *net.UnixConn, request retainedRequest) []byte {
		status := retainedTestStatus()
		status.CurrentRevision = gapdbapi.Revision(10 + served.Add(1))
		first := retainedSuccess(t, request, status)
		status.CurrentRevision = 999
		second := retainedSuccess(t, request, status)
		retainedUnaryWritePair(conn, first, second, writes)
		return nil
	})
	for i := 0; i < 2; i++ {
		got, err := transport.status(t.Context())
		var typed *gapdbapi.TransportError
		if !errors.As(err, &typed) || !typed.Ambiguous || !reflect.DeepEqual(got, gapdbapi.StatusResult{}) {
			t.Fatalf("fresh RPC admitted an extra response frame: status=%+v error=%v", got, err)
		}
		retainedUnaryReceiveWrite(t, writes)
	}
	if accepted.Load() != 2 || served.Load() != 2 {
		t.Fatal("second RPC must use its own freshly accepted connection")
	}
}

func TestRetainedUnaryFrameSecondReplyCannotRepairFirst(t *testing.T) {
	writes := make(chan retainedUnaryWrite, 1)
	transport, _, accepted := retainedFixture(t, func(conn *net.UnixConn, request retainedRequest) []byte {
		retainedUnaryWritePair(conn, []byte(`{`), retainedSuccess(t, request, retainedTestStatus()), writes)
		return nil
	})
	got, err := transport.status(t.Context())
	var transportErr *gapdbapi.TransportError
	if !errors.As(err, &transportErr) || !transportErr.Ambiguous || !reflect.DeepEqual(got, gapdbapi.StatusResult{}) {
		t.Fatal("malformed FIRST payload must retain existing transport refusal and zero result")
	}
	retainedUnaryReceiveWrite(t, writes)
	if accepted.Load() != 1 {
		t.Fatal("a valid second frame must not trigger fallback/retry")
	}
}
