//go:build linux && amd64

package gapdb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"golang.org/x/sys/unix"
)

// Simulated self-peer only, not a genuine Host/provider or admission proof.
// The required fixture owns listener, lifetime and FIRST full-close outcomes.
// Each handler independently checks actual request EOF and peer credentials.
func retainedEOFFixture(t *testing.T, handler func(*net.UnixConn, retainedRequest)) *retainedTransport {
	t.Helper()
	transport, _, _ := retainedRequiredFixture(t, func(conn *net.UnixConn, request retainedRequest) []byte {
		var extra [1]byte
		if n, err := conn.Read(extra[:]); n != 0 || err != io.EOF {
			t.Errorf("request stream not ended: n=%d err=%v", n, err)
			return nil
		}
		raw, err := conn.SyscallConn()
		if err != nil {
			t.Error(err)
			return nil
		}
		var peer *unix.Ucred
		var peerErr error
		if err := raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || peerErr != nil || peer == nil || int(peer.Pid) != os.Getpid() {
			t.Errorf("actual fixture peer differs: control=%v peer=%v credential=%v", err, peer, peerErr)
			return nil
		}
		handler(conn, request)
		return nil
	})
	return transport
}

func retainedEOFWait(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture synchronization missing (watchdog, not an EOF witness)")
	}
}

func retainedEOFEndResponse(t *testing.T, conn *net.UnixConn) {
	t.Helper()
	first := &retainedRequiredClose{close: conn.CloseWrite}
	retainedRequiredCheckClose(t, "server write half", first)
	retainedRequiredCheckClose(t, "cached server write half", first)
	if first.calls.Load() != 1 {
		t.Error("write half closed more than once")
	}
}

func retainedEOFPut(t *testing.T, conn *net.UnixConn, payload []byte) {
	t.Helper()
	if err := retainedWriteFrame(conn, payload); err != nil {
		t.Error("fixture frame write", err)
	}
}

func TestRetainedUnaryEOFPositiveFreshRequestAndActualEOF(t *testing.T) {
	transport := retainedEOFFixture(t, func(conn *net.UnixConn, request retainedRequest) {
		retainedEOFPut(t, conn, retainedSuccess(t, request, retainedTestStatus()))
		retainedEOFEndResponse(t, conn)
	})
	for i := 0; i < 2; i++ {
		got, err := transport.status(t.Context())
		if err != nil || !reflect.DeepEqual(got, retainedTestStatus()) {
			t.Fatal("single response plus actual EOF refused", got, err)
		}
	}
}

func TestRetainedUnaryEOFRejectsExtraAndIncompleteStreams(t *testing.T) {
	for _, mode := range []string{"two-frames", "partial-extra", "EOF-only", "malformed-length", "partial-frame", "malformed-JSON"} {
		t.Run(mode, func(t *testing.T) {
			transport := retainedEOFFixture(t, func(conn *net.UnixConn, request retainedRequest) {
				first := retainedSuccess(t, request, retainedTestStatus())
				var wire bytes.Buffer
				switch mode {
				case "two-frames":
					_ = retainedWriteFrame(&wire, first)
					_ = retainedWriteFrame(&wire, first)
				case "partial-extra":
					_ = retainedWriteFrame(&wire, first)
					wire.WriteByte(0)
				case "malformed-length":
					wire.Write([]byte{0, 0, 0, 0})
				case "partial-frame":
					wire.Write([]byte{0, 0, 0, 8, '{'})
				case "malformed-JSON":
					_ = retainedWriteFrame(&wire, []byte(`{`))
				}
				if wire.Len() != 0 {
					if n, err := conn.Write(wire.Bytes()); err != nil || n != wire.Len() {
						t.Errorf("complete fixture bytes not queued: n=%d want=%d err=%v", n, wire.Len(), err)
					}
				}
				// Full close belongs to the required fixture. For extra bytes it
				// need not race the client's refusal with another shutdown call.
			})
			got, err := transport.exchange(t.Context(), retainedRequest{AdmittedWireVersion, transport.requestID(), "status", struct{}{}}, "")
			var typed *gapdbapi.TransportError
			if !errors.As(err, &typed) || !typed.Ambiguous || !reflect.DeepEqual(got, retainedResult{}) {
				t.Fatal("stream failure exposed a result or non-transport error", got, err)
			}
		})
	}
}

func TestRetainedUnaryEOFDelayedExtraUsesChannelNotSilence(t *testing.T) {
	firstWritten, release, secondWritten := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseWriter()
	transport := retainedEOFFixture(t, func(conn *net.UnixConn, request retainedRequest) {
		retainedEOFPut(t, conn, retainedSuccess(t, request, retainedTestStatus()))
		close(firstWritten)
		<-release
		n, err := conn.Write([]byte{0})
		if err == nil && n != 1 { err = io.ErrShortWrite }
		secondWritten <- err
	})
	type outcome struct { result retainedResult; err error }
	done := make(chan outcome, 1)
	go func() {
		result, err := transport.exchange(t.Context(), retainedRequest{AdmittedWireVersion, transport.requestID(), "status", struct{}{}}, "")
		done <- outcome{result, err}
	}()
	retainedEOFWait(t, firstWritten)
	// This check is not an absence proof. The synchronized later byte is the
	// refusal witness; no quiet interval is used to justify a positive result.
	select {
	case got := <-done:
		releaseWriter()
		t.Fatal("admitted before stream end", got)
	default:
	}
	releaseWriter()
	if err := <-secondWritten; err != nil {
		t.Fatal("delayed byte was not actually written", err)
	}
	got := <-done
	if got.err == nil || !reflect.DeepEqual(got.result, retainedResult{}) {
		t.Fatal("delayed extra byte admitted", got)
	}
}

func TestRetainedUnaryEOFMissingEndUsesOriginalBudgetAndCancellation(t *testing.T) {
	for _, mode := range []string{"transport-deadline", "caller-deadline", "caller-cancel"} {
		t.Run(mode, func(t *testing.T) {
			firstWritten, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			transport := retainedEOFFixture(t, func(conn *net.UnixConn, request retainedRequest) {
				retainedEOFPut(t, conn, retainedSuccess(t, request, retainedTestStatus()))
				close(firstWritten)
				<-release // never treat request EOF as client cancellation
			})
			transport.timeout = 200 * time.Millisecond
			ctx, cancel := context.WithCancel(t.Context())
			if mode == "caller-deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				got, err := transport.exchange(ctx, retainedRequest{AdmittedWireVersion, transport.requestID(), "status", struct{}{}}, "")
				if !reflect.DeepEqual(got, retainedResult{}) { t.Error("deadline/cancel leaked result", got) }
				done <- err
			}()
			retainedEOFWait(t, firstWritten)
			if mode == "caller-cancel" { cancel() }
			select {
			case err := <-done:
				var timeout net.Error
				if mode == "caller-cancel" {
					if !errors.Is(err, context.Canceled) { t.Fatal(err) }
				} else if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &timeout) && timeout.Timeout()) {
					t.Fatal("missing EOF not refused by existing deadline", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("existing timeout/cancel failed to bound EOF read")
			}
		})
	}
}

type retainedEOFReader struct { n int; err error }
func (r retainedEOFReader) Read(p []byte) (int, error) { if r.n > 0 { p[0] = 0 }; return r.n, r.err }

func TestRetainedUnaryEOFReaderErrorsAreNotEOF(t *testing.T) {
	sentinel := errors.New("read sentinel")
	for _, input := range []retainedEOFReader{{0, nil}, {0, sentinel}, {1, nil}, {1, io.EOF}, {1, sentinel}} {
		err := retainedRequireUnaryEOF(input)
		if err == nil { t.Fatal("non-EOF admitted", input) }
		if input.err == sentinel && !errors.Is(err, sentinel) { t.Fatal("first read error lost", err) }
	}
}

func TestRetainedUnaryEOFActualClosedUnixReadWriteHalfCloseDeadlineErrors(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil { t.Fatal(err) }
	files := [2]*os.File{os.NewFile(uintptr(fds[0]), "eof-client"), os.NewFile(uintptr(fds[1]), "eof-peer")}
	for _, file := range files {
		first := &retainedRequiredClose{close: file.Close}
		t.Cleanup(func() { retainedRequiredCheckClose(t, "socketpair original file", first) })
	}
	var conns [2]*net.UnixConn
	var closes [2]*retainedRequiredClose
	for i, file := range files {
		raw, err := net.FileConn(file)
		if err != nil { t.Fatal(err) }
		first := &retainedRequiredClose{close: raw.Close}
		t.Cleanup(func() { retainedRequiredCheckClose(t, "socketpair connection", first) })
		var ok bool
		conns[i], ok = raw.(*net.UnixConn)
		if !ok { t.Fatal("not an actual UnixConn") }
		closes[i] = first
	}
	retainedRequiredCheckClose(t, "client FIRST full close", closes[0])
	for name, err := range map[string]error{
		"half-close": conns[0].CloseWrite(),
		"deadline": conns[0].SetDeadline(time.Now()),
		"write": retainedWriteFrame(conns[0], []byte(`{}`)),
		"EOF read": retainedRequireUnaryEOF(conns[0]),
	} {
		if !errors.Is(err, net.ErrClosed) { t.Fatal("actual closed descriptor error lost", name, err) }
	}
	retainedRequiredCheckClose(t, "cached client close", closes[0])
	if closes[0].calls.Load() != 1 { t.Fatal("FIRST client close retried") }
}

func TestRetainedUnaryEOFSourceOrderingAndNoSecondBudget(t *testing.T) {
	raw, err := os.ReadFile("retained_transport.go")
	if err != nil { t.Fatal(err) }
	source := string(raw)
	start, end := strings.Index(source, "func (t *retainedTransport) exchange("), strings.Index(source, "func retainedRequireUnaryEOF(")
	if start < 0 || end <= start { t.Fatal("exchange source missing") }
	body := source[start:end]
	last := -1
	for _, token := range []string{"context.WithTimeout(ctx, t.timeout)", "raw, err := (&net.Dialer{}).DialContext", "conn := raw.(*net.UnixConn)", "conn.SetDeadline(deadline)", "retainedWriteFrame(conn, payload)", "conn.CloseWrite()", "retainedReadFrameContext(ctx, conn)", "retainedRequireUnaryEOF(conn)", "t.decodeContext(ctx, payload, request, key)"} {
		at := strings.Index(body, token)
		if at <= last { t.Fatal("missing/reordered actual unary boundary", token) }
		last = at
	}
	if strings.Count(body, "SetDeadline(") != 1 || strings.Count(body, "DialContext(") != 1 || strings.Count(body, "t.lifetime.VerifyConnection(ctx, conn)") != 2 || !strings.Contains(body, "context.AfterFunc(ctx,") {
		t.Fatal("fresh connection/cancellation/attribution/deadline contract changed")
	}
}
