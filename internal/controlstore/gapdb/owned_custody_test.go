//go:build linux

package gapdb

// UNRUN provider-free component tests. No provider admission or kernel-fault
// credit. Returned-error injection closes the ACTUAL connection once safely.
import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"golang.org/x/sys/unix"
)

func ownedCustodyConnection(t *testing.T) *net.UnixConn {
	t.Helper()
	root, err := os.MkdirTemp("", "fc-")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = os.RemoveAll(root) }) // no durable residual claim
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "s"), Net: "unix"})
	if err != nil { t.Fatal(err) }
	type accepted struct { conn *net.UnixConn; err error }
	arrival := make(chan accepted, 1)
	go func() { conn, err := listener.AcceptUnix(); arrival <- accepted{conn, err} }()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil { _ = listener.Close(); <-arrival; t.Fatal(err) }
	peer := <-arrival
	if peer.err != nil { _ = client.Close(); _ = listener.Close(); t.Fatal(peer.err) }
	if err := listener.Close(); err != nil { t.Fatal(err) }
	t.Cleanup(func() { if err := peer.conn.Close(); err != nil { t.Error("test-owned peer close", err) } })
	return client // caller's close owner exclusively owns this actual dial result
}

func TestOwnedCloseResultPreservesPrimaryAndZerosOnFailure(t *testing.T) {
	primary := &gapdbapi.TransportError{Operation: "get", SocketPath: "/component", Cause: context.Canceled}
	custody := &gapdbapi.TransportError{Operation: "get", SocketPath: "/component", Cause: unix.EIO}
	value := retainedResult{record: gapdbapi.Record{Key: "component", Value: []byte("not admitted"), Revision: 1}}
	if got, err := ownedCloseResult(value, primary, nil); !reflect.DeepEqual(got, value) || err != primary { t.Fatal("nil close changed identity/result") }
	got, err := ownedCloseResult(value, primary, custody)
	var typed *gapdbapi.TransportError
	if !reflect.DeepEqual(got, retainedResult{}) || err.Error() != primary.Error() || !errors.Is(err, context.Canceled) || !errors.Is(err, unix.EIO) || !errors.As(err, &typed) || typed != primary { t.Fatal("dual primary/result/As changed", got, err) }
	got, err = ownedCloseResult(value, nil, custody)
	if !reflect.DeepEqual(got, retainedResult{}) || err != custody { t.Fatal("close-only success escaped") }
	if withOwnedCustody(nil, nil) != nil || withOwnedCustody(primary, nil) != primary { t.Fatal("nil controls") }
}

func TestConnectionSameFIRSTOwnerCancellationGlobalCloseAndDefer(t *testing.T) {
	transport := &retainedTransport{}
	connection := ownedCustodyConnection(t)
	node, err := transport.registerConnectionResult(connection)
	if err != nil || node == nil { t.Fatal(err) }
	owner := node.owner
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	injected := make(chan error, 1)
	go func() {
		injected <- owner.closeWith(func(actual *net.UnixConn) error {
			calls.Add(1)
			if actual != connection { t.Error("owner retargeted") }
			close(entered)
			<-release
			if err := actual.Close(); err != nil { return err }
			return unix.EIO // returned COMPONENT error; actual close succeeded
		})
	}()
	<-entered
	global := make(chan error, 4)
	for i := 0; i < 4; i++ { go func() { global <- transport.Close() }() }
	ctx, cancel := context.WithCancel(t.Context())
	callbackDone := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() { callbackDone <- transport.closeConnection(owner) })
	deferred := make(chan error, 1)
	go func() { deferred <- transport.closeConnection(owner) }()
	cancel()
	// A close callback waiting on its own Once must NOT own transport.mu.
	deadline := time.Now().Add(time.Second)
	for {
		transport.mu.Lock()
		detached := node.conn == nil
		if owner.conn != connection { transport.mu.Unlock(); t.Fatal("mutable node retargeted owner") }
		transport.mu.Unlock()
		if detached { break } // GLOBAL Close detached it before joining owner.Once
		if time.Now().After(deadline) { t.Fatal("global Close stuck behind transport mutex") }
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-injected; !errors.Is(err, unix.EIO) { t.Fatal(err) }
	if err := <-callbackDone; !errors.Is(err, unix.EIO) { t.Fatal("cancel lost actual result", err) }
	stop()
	if err := <-deferred; !errors.Is(err, unix.EIO) { t.Fatal("defer lost actual result", err) }
	first := <-global
	for i := 0; i < 3; i++ { if got := <-global; got != first { t.Fatal("global FIRST object changed", got) } }
	if !errors.Is(first, unix.EIO) || transport.Close() != first || calls.Load() != 1 || !owner.attempted || transport.connectionCloseAttempts != 1 { t.Fatal("transport FIRST repaired/retried", first, calls.Load(), transport.connectionCloseAttempts) }
}

func TestTransportFirstCloseIncludesEarlierUnlinkedFailureAndAllNodes(t *testing.T) {
	transport := &retainedTransport{}
	for i := 0; i < 3; i++ {
		conn := ownedCustodyConnection(t)
		node, err := transport.registerConnectionResult(conn)
		if err != nil { t.Fatal(err) }
		if i == 0 {
			if err := node.owner.closeWith(func(c *net.UnixConn) error { if err := c.Close(); err != nil { return err }; return unix.EIO }); !errors.Is(err, unix.EIO) { t.Fatal(err) }
			if err := transport.closeConnection(node.owner); !errors.Is(err, unix.EIO) { t.Fatal(err) }
			transport.mu.Lock(); transport.unlinkConnection(node); transport.mu.Unlock()
		}
	}
	first := transport.Close()
	if !errors.Is(first, unix.EIO) || transport.connectionCloseAttempts != 3 || transport.active != nil || transport.Close() != first { t.Fatal("prior/global node result lost", first) }
	absent := &retainedTransport{}
	if err := absent.Close(); err != nil || absent.connectionCloseAttempts != 0 { t.Fatal("absence manufactured attempts", err) }
}

func TestTransportCloseJoinsReservedDialAndRejectedActualConnection(t *testing.T) {
	transport := &retainedTransport{}
	// Component reservation at the SAME pre-dial seam as exchange, not a new
	// owner before dial. The owner below is constructed only after actual Dial.
	transport.mu.Lock(); transport.dials.Add(1); transport.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- transport.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		transport.mu.Lock(); closed := transport.closed; transport.mu.Unlock()
		if closed { break }
		if time.Now().After(deadline) { t.Fatal("Close did not bar reservations") }
		time.Sleep(time.Millisecond)
	}
	conn := ownedCustodyConnection(t)
	node, err := transport.registerConnectionResult(conn)
	if node != nil || err != nil { t.Fatal("late dial not safely retired", node, err) }
	select { case <-done: t.Fatal("Close published before reserved dial Done"); default: }
	transport.dials.Done()
	if err := <-done; err != nil || transport.connectionCloseAttempts != 1 { t.Fatal("rejected dial close omitted", err) }
	if _, err := conn.Write([]byte("x")); !errors.Is(err, net.ErrClosed) { t.Fatal("actual rejected connection still open", err) }
}

type custodySDKCloser struct { calls int; err error; order *[]string }
func (c *custodySDKCloser) Close() error { c.calls++; if c.order != nil { *c.order = append(*c.order, "sdk") }; return c.err }

func TestOwnedCloserSingleFIRSTTransportThenSDKOrdering(t *testing.T) {
	transport := &retainedTransport{}
	conn := ownedCustodyConnection(t)
	node, err := transport.registerConnectionResult(conn)
	if err != nil { t.Fatal(err) }
	var order []string
	if err := node.owner.closeWith(func(c *net.UnixConn) error { order = append(order, "transport"); if err := c.Close(); err != nil { return err }; return unix.EIO }); !errors.Is(err, unix.EIO) { t.Fatal(err) }
	second := errors.New("SDK component close")
	sdk := &custodySDKCloser{err: second, order: &order}
	closer := &ownedCloser{transport: transport, sdk: sdk}
	first := closer.Close()
	if first.Error() != unix.EIO.Error() || !errors.Is(first, unix.EIO) || !errors.Is(first, second) || sdk.calls != 1 || !reflect.DeepEqual(order, []string{"transport", "sdk"}) { t.Fatal("outer close chain/order", first, order) }
	if got := closer.Close(); got != first || sdk.calls != 1 { t.Fatal("outer FIRST repaired/retried", got) }
}

func TestOwnedCustodySourcePreservesLiveProtocolAndPostchecks(t *testing.T) {
	raw, err := os.ReadFile("retained_transport.go")
	if err != nil { t.Fatal(err) }
	source := string(raw)
	for _, token := range []string{"retainedWriteFrame(conn, payload)", "conn.CloseWrite()", "retainedReadFrameContext(ctx, conn)", "retainedRequireUnaryEOF(conn)", "t.decodeContext(ctx, payload, request, key)", "t.dials.Add(1)", "t.dials.Done()", "t.dials.Wait()", "owner := &retainedConnectionClose{conn: conn}", "context.AfterFunc(ctx, func() { _ = t.closeConnection(node.owner) })", "closeErr := t.closeConnection(node.owner)", "ownedCloseResult(result, resultErr, failed(closeErr))"} {
		if !strings.Contains(source, token) { t.Error("protocol/FIRST/dial seam absent", token) }
	}
	if strings.Count(source, "t.lifetime.VerifyConnection(ctx, conn)") != 2 || strings.Contains(source, "_ = node.conn.Close()") || strings.Contains(source, "_ = conn.Close()") { t.Fatal("attribution or raw double-close regression") }
	helper, err := os.ReadFile("owned_custody.go")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(helper), "return c.closeWith((*net.UnixConn).Close)") { t.Fatal("production close selector introduced") }
	// Struct-level guard: new owners contain no stored configurable function.
	for _, typ := range []reflect.Type{reflect.TypeOf(retainedConnectionClose{}), reflect.TypeOf(retainedTransport{}), reflect.TypeOf(ownedCloser{})} {
		for i := 0; i < typ.NumField(); i++ { if typ.Field(i).Type.Kind() == reflect.Func { t.Fatal("stored closer/provider", typ, typ.Field(i).Name) } }
	}
}

