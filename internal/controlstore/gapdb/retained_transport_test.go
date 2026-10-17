package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

func retainedTestStatus() gapdbapi.StatusResult {
	return gapdbapi.StatusResult{Lifecycle: gapdbapi.LifecycleReady, DatabaseID: "owned-test-db", OwnerPID: os.Getpid(), OwnerStartedAt: "2026-01-01T00:00:00Z", Limits: gapdbapi.DefaultOptions().Limits}
}
func retainedJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func retainedSuccess(t *testing.T, request retainedRequest, result any) []byte {
	return retainedJSON(t, map[string]any{"schema_version": AdmittedWireVersion, "ok": true, "database_id": "owned-test-db", "operation": request.Operation, "request_id": request.RequestID, "result": result})
}

// Each field retains the FIRST result of an actual source operation. A later
// successful full close never repairs a failed response write/write-half close.
// Peer-disconnect diagnostics are not positive source/cleanup evidence; other
// source failures fail the fixture, including its own deadline or close failure.
type retainedFixtureSourceOutcome struct {
	writeAttempted, shutdownAttempted, closeAttempted bool
	writeErr, shutdownErr, closeErr error
}

func (s retainedFixtureSourceOutcome) report(t *testing.T) {
	t.Helper()
	if first := errors.Join(s.writeErr, s.shutdownErr, s.closeErr); first != nil {
		t.Logf("FIRST actual fixture response outcomes retained: attempted(write/shutdown/close)=%t/%t/%t write=%v shutdown=%v close=%v union=%v; peer-disconnect diagnostics are not positive source evidence or a cancellation attribution", s.writeAttempted, s.shutdownAttempted, s.closeAttempted, s.writeErr, s.shutdownErr, s.closeErr, first)
	}
	for _, err := range []error{s.writeErr, s.shutdownErr} {
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, syscall.ENOTCONN) {
			t.Errorf("FIRST fixture response source failure: %v", err)
		}
	}
	if s.closeErr != nil { t.Errorf("FIRST fixture connection close failed; residual/unqualified: %v", s.closeErr) }
}

// Actual Unix fixture. These tests constrain identity, not provider admission.
func retainedFixture(t *testing.T, handler func(*net.UnixConn, retainedRequest) []byte) (*retainedTransport, *retainedkernel.Lifetime, *atomic.Int32) {
	t.Helper()
	root, err := os.MkdirTemp("", "rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			accepted.Add(1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				var source retainedFixtureSourceOutcome
				defer func() { source.closeAttempted = true; source.closeErr = conn.Close(); source.report(t) }()
				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil { t.Error("fixture server deadline", err); return }
				raw, err := retainedReadFrame(conn)
				if err != nil {
					return
				}
				var request retainedRequest
				if err := json.Unmarshal(raw, &request); err != nil {
					return
				}
				response := handler(conn, request)
				if response != nil {
					source.writeAttempted = true
					source.writeErr = retainedWriteFrame(conn, response)
					if source.writeErr == nil { source.shutdownAttempted = true; source.shutdownErr = conn.CloseWrite() }
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done; workers.Wait() })
	proc, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(proc[bytes.LastIndexByte(proc, ')')+2:]))
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	var exe, socket unix.Stat_t
	if err := unix.Fstat(int(image.Fd()), &exe); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lstat(path, &socket); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, image); err != nil {
		t.Fatal(err)
	}
	var directory unix.Stat_t
	if err := unix.Lstat(root, &directory); err != nil {
		t.Fatal(err)
	}
	anchor := retainedkernel.Anchor{DirectoryPath: root, DirectoryDevice: uint64(directory.Dev), DirectoryInode: directory.Ino, PID: os.Getpid(), StartTicks: ticks, ImageDevice: uint64(exe.Dev), ImageInode: exe.Ino, ImageSize: exe.Size, ImageSHA256: hex.EncodeToString(hash.Sum(nil)), SocketPath: path, SocketDevice: uint64(socket.Dev), SocketInode: socket.Ino}
	lifetime, err := retainedkernel.New(t.Context(), anchor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lifetime.Close() })
	status := retainedTestStatus()
	transport := &retainedTransport{lifetime: lifetime, endpoint: path, database: status.DatabaseID, ownerPID: status.OwnerPID, ownerStarted: status.OwnerStartedAt, timeout: time.Second}
	t.Cleanup(func() { _ = transport.Close() })
	return transport, lifetime, &accepted
}
func TestRetainedTransportActualFreshSuccessTTLAndOwnedWrapper(t *testing.T) {
	expiry := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) // generic transport does NOT reject past expiry
	transport, lifetime, accepted := retainedFixture(t, func(_ *net.UnixConn, request retainedRequest) []byte {
		if request.Operation == "status" {
			return retainedSuccess(t, request, retainedTestStatus())
		}
		var args struct {
			Key string `json:"key"`
		}
		encoded, _ := json.Marshal(request.Arguments)
		_ = json.Unmarshal(encoded, &args)
		return retainedSuccess(t, request, map[string]any{"record": gapdbapi.Record{Key: args.Key, Value: []byte{0, 1, 255}, Revision: ^gapdbapi.Revision(0), ExpiresAt: &expiry}})
	})
	for i := 0; i < 2; i++ {
		record, err := transport.get(t.Context(), "private-key")
		if err != nil || record.Revision != ^gapdbapi.Revision(0) || !bytes.Equal(record.Value, []byte{0, 1, 255}) || record.ExpiresAt == nil || !record.ExpiresAt.Equal(expiry) {
			t.Fatalf("record=%+v err=%v", record, err)
		}
	}
	if _, err := transport.status(t.Context()); err != nil {
		t.Fatal(err)
	}
	if accepted.Load() != 3 {
		t.Fatalf("not one fresh connection per RPC: %d", accepted.Load())
	}
	var writes atomic.Int32
	base := &fakeProvider{getFn: func(context.Context, string) (gapdbapi.Record, error) {
		t.Error("SDK Get fallback")
		return gapdbapi.Record{}, errors.New("fallback")
	}, statusFn: func(context.Context) (gapdbapi.StatusResult, error) {
		t.Error("SDK Status fallback")
		return gapdbapi.StatusResult{}, errors.New("fallback")
	}, batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		writes.Add(1)
		return gapdbapi.MutationResult{}, nil
	}}
	adapter, err := newAdapter(&ownedProvider{provider: base, transport: transport}, transport.endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.client.Get(t.Context(), "private-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.client.Status(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.client.AtomicBatch(t.Context(), gapdbapi.Batch{}); err != nil || writes.Load() != 1 {
		t.Fatal("SDK writes changed", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.get(t.Context(), "private-key"); err == nil {
		t.Fatal("closed transport accepted")
	}
	if err := lifetime.Check(t.Context()); err != nil {
		t.Fatal("adapter closer revoked lifecycle descriptor", err)
	}
}
func TestRetainedTransportOwnedConstructorAndProxyRefusal(t *testing.T) {
	transport, lifetime, _ := retainedFixture(t, func(_ *net.UnixConn, request retainedRequest) []byte {
		return retainedSuccess(t, request, retainedTestStatus())
	})
	adapter, closer, err := DialOwned(t.Context(), transport.endpoint, time.Second, lifetime, transport.database, transport.ownerPID, transport.ownerStarted)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.lifetime != lifetime {
		t.Fatal("owned lifetime lost")
	}
	if _, ok := adapter.client.(*ownedProvider); !ok {
		t.Fatal("owned wrapper missing")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Check(t.Context()); err != nil {
		t.Fatal("closer owns lifetime", err)
	}
	proxy := transport.endpoint + "-proxy"
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: proxy, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, _, err := DialOwned(t.Context(), proxy, time.Second, lifetime, transport.database, transport.ownerPID, transport.ownerStarted); err == nil {
		t.Fatal("proxy construction accepted")
	}
	lifetime.Revoke()
	if _, _, err := DialOwned(t.Context(), transport.endpoint, time.Second, lifetime, transport.database, transport.ownerPID, transport.ownerStarted); err == nil {
		t.Fatal("revoked constructor accepted")
	}
}
func TestRetainedTransportPostcheckEvenDecodeErrorAndCancellation(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(strconv.FormatBool(valid), func(t *testing.T) {
			var transport *retainedTransport
			transport, _, _ = retainedFixture(t, func(_ *net.UnixConn, request retainedRequest) []byte {
				if err := os.Rename(transport.endpoint, transport.endpoint+"-old"); err != nil {
					t.Error(err)
				}
				replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: transport.endpoint, Net: "unix"})
				if err != nil {
					t.Error(err)
				} else {
					t.Cleanup(func() { _ = replacement.Close() })
				}
				if valid {
					return retainedSuccess(t, request, map[string]any{"record": gapdbapi.Record{Key: "k", Value: []byte("v"), Revision: 1}})
				}
				return []byte(`{"invalid":true}`)
			})
			record, err := transport.get(t.Context(), "k")
			if err == nil || record.Key != "" || !errors.Is(err, retainedkernel.ErrIdentity) {
				t.Fatalf("postcheck result escaped %+v %v", record, err)
			}
		})
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	transport, _, _ := retainedFixture(t, func(_ *net.UnixConn, _ retainedRequest) []byte {
		close(blocked)
		<-release // request EOF is intentional, not client cancellation
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := transport.get(ctx, "k"); done <- err }()
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not arrive")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not close actual connection")
	}
	transport.mu.Lock()
	active := retainedRegistryCountLocked(t, transport)
	transport.mu.Unlock()
	if active != 0 {
		t.Fatal("connection registration leaked")
	}
}

// Caller holds mu. Unlike head==nil, this checks exact membership, both links,
// uniqueness, and live connection ownership; detached nodes are checked below.
func retainedRegistryCountLocked(t *testing.T, transport *retainedTransport) int {
	t.Helper()
	seen := make(map[*retainedConnectionNode]bool)
	conns := make(map[*net.UnixConn]bool)
	var previous *retainedConnectionNode
	for node := transport.active; node != nil; node = node.next {
		if seen[node] || !node.linked || node.prev != previous || node.conn == nil || conns[node.conn] {
			t.Fatal("registry integrity")
		}
		seen[node], conns[node.conn] = true, true
		previous = node
	}
	return len(seen)
}

func retainedRegistryDetached(t *testing.T, node *retainedConnectionNode) {
	t.Helper()
	if node.linked || node.prev != nil || node.next != nil || node.conn != nil {
		t.Fatal("detached node retained links/descriptor")
	}
}

func TestRetainedTransportRegistryHeadMiddleTailCloseAndLateRegistration(t *testing.T) {
	transport, _, _ := retainedFixture(t, func(*net.UnixConn, retainedRequest) []byte { return nil })
	var nodes [3]*retainedConnectionNode
	var conns [3]*net.UnixConn
	for i := range nodes {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: transport.endpoint, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		conns[i], nodes[i] = conn, transport.registerConnection(conn)
	}
	// Middle, tail, head; exact stable nodes, repeated unlink must be harmless.
	for i, index := range []int{1, 0, 2} {
		transport.mu.Lock()
		if retainedRegistryCountLocked(t, transport) != 3-i {
			t.Fatal("registration count")
		}
		transport.unlinkConnection(nodes[index])
		transport.unlinkConnection(nodes[index])
		retainedRegistryDetached(t, nodes[index])
		if retainedRegistryCountLocked(t, transport) != 2-i {
			t.Fatal("unlink count")
		}
		transport.mu.Unlock()
		_ = conns[index].Close()
	}
	// Refill and Close detaches ALL nodes, including each deferred-cleanup target.
	for i := range nodes {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: transport.endpoint, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		conns[i], nodes[i] = conn, transport.registerConnection(conn)
	}
	_ = transport.Close()
	_ = transport.Close()
	transport.mu.Lock()
	for i, node := range nodes {
		retainedRegistryDetached(t, node)
		transport.unlinkConnection(node)
		if _, err := conns[i].Write([]byte{0}); !errors.Is(err, net.ErrClosed) {
			t.Fatal("actual socket not closed", err)
		}
	}
	if retainedRegistryCountLocked(t, transport) != 0 {
		t.Fatal("Close registry leak")
	}
	transport.mu.Unlock()
	late, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: transport.endpoint, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if transport.registerConnection(late) != nil {
		t.Fatal("late registration resurrected list")
	}
	if _, err := late.Write([]byte{0}); !errors.Is(err, net.ErrClosed) {
		t.Fatal("late socket not closed", err)
	}
	t.Logf("stable node bytes=%d; at most A nodes/invocation, separate <=A pending AfterFunc callbacks; no global cap", unsafe.Sizeof(retainedConnectionNode{}))
}

func TestRetainedTransportRegistryConcurrentRegisterUnlinkAndClose(t *testing.T) {
	transport, _, _ := retainedFixture(t, func(*net.UnixConn, retainedRequest) []byte { return nil })
	const count = 32
	var conns [count]*net.UnixConn
	var nodes [count]*retainedConnectionNode
	for i := range conns {
		var err error
		conns[i], err = net.DialUnix("unix", nil, &net.UnixAddr{Name: transport.endpoint, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			nodes[i] = transport.registerConnection(conns[i])
			if nodes[i] != nil {
				transport.mu.Lock()
				retainedRegistryCountLocked(t, transport)
				if i%2 == 0 {
					transport.unlinkConnection(nodes[i])
					_ = conns[i].Close()
				}
				transport.mu.Unlock()
			}
		}(i)
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-start; _ = transport.Close(); _ = transport.Close() }()
	close(start)
	wg.Wait()
	transport.mu.Lock()
	for i, node := range nodes {
		if node != nil {
			retainedRegistryDetached(t, node)
			transport.unlinkConnection(node)
		}
		if _, err := conns[i].Write([]byte{0}); !errors.Is(err, net.ErrClosed) {
			t.Fatal("raced socket remained open", err)
		}
	}
	if retainedRegistryCountLocked(t, transport) != 0 {
		t.Fatal("raced registry resurrected")
	}
	transport.mu.Unlock()
}

func TestRetainedTransportRegistryConcurrentRPCFailuresAndClose(t *testing.T) {
	for _, fault := range []string{"decode", "cancel", "attribution", "close"} {
		t.Run(fault, func(t *testing.T) {
			const count = 6
			arrived, release := make(chan struct{}, count), make(chan struct{})
			transport, lifetime, _ := retainedFixture(t, func(_ *net.UnixConn, request retainedRequest) []byte {
				arrived <- struct{}{}
				<-release
				if fault == "decode" {
					return []byte(`{"invalid":true}`)
				}
				return retainedSuccess(t, request, map[string]any{"record": gapdbapi.Record{Key: "k", Value: []byte("v"), Revision: 1}})
			})
			transport.timeout = 5 * time.Second
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, count)
			for i := 0; i < count; i++ {
				go func() {
					r, err := transport.get(ctx, "k")
					if r.Key != "" || r.Value != nil {
						t.Error("failed concurrent result escaped")
					}
					done <- err
				}()
			}
			for i := 0; i < count; i++ {
				select {
				case <-arrived:
				case <-time.After(5 * time.Second):
					close(release)
					t.Fatal("actual RPC did not arrive")
				}
			}
			transport.mu.Lock()
			if retainedRegistryCountLocked(t, transport) != count {
				t.Fatal("actual RPC registration count")
			}
			nodes := make([]*retainedConnectionNode, 0, count)
			conns := make([]*net.UnixConn, 0, count)
			for n := transport.active; n != nil; n = n.next {
				nodes = append(nodes, n)
				conns = append(conns, n.conn)
			}
			transport.mu.Unlock()
			switch fault {
			case "cancel":
				cancel()
			case "attribution":
				lifetime.Revoke()
			case "close":
				_ = transport.Close()
				_ = transport.Close()
			}
			close(release)
			for i := 0; i < count; i++ {
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("failed RPC accepted")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("RPC cleanup blocked")
				}
			}
			transport.mu.Lock()
			if retainedRegistryCountLocked(t, transport) != 0 {
				t.Fatal("failed RPC registry leaked")
			}
			for i, node := range nodes {
				retainedRegistryDetached(t, node)
				if _, err := conns[i].Write([]byte{0}); !errors.Is(err, net.ErrClosed) {
					t.Fatal("failed RPC socket open", err)
				}
			}
			transport.mu.Unlock()
		})
	}
}

func mutateRetainedJSON(t *testing.T, raw []byte, field string, value json.RawMessage) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if value == nil {
		delete(object, field)
	} else {
		object[field] = value
	}
	return retainedJSON(t, object)
}
func TestRetainedTransportStrictGetMatrix(t *testing.T) {
	request := retainedRequest{AdmittedWireVersion, "request", "get", nil}
	transport := &retainedTransport{database: "owned-test-db"}
	base := []byte(`{"record":{"key":"k","value_base64":"AAH/","revision":18446744073709551615}}`)
	wrap := func(result []byte) []byte { return retainedSuccess(t, request, json.RawMessage(result)) }
	tests := map[string][]byte{
		"missing record": wrap([]byte(`{}`)), "null record": wrap([]byte(`{"record":null}`)), "unknown result": wrap([]byte(`{"record":{"key":"k","value_base64":"","revision":1},"other":true}`)),
		"key alias":     wrap([]byte(`{"record":{"key":"k","KEY":"foreign","value_base64":"","revision":1}}`)),
		"revision null": wrap([]byte(`{"record":{"key":"k","value_base64":"","revision":null}}`)),
		"value alias":   wrap([]byte(`{"record":{"key":"k","VALUE_BASE64":"","revision":1}}`)),
		"missing value": wrap([]byte(`{"record":{"key":"k","revision":1}}`)), "missing key": wrap([]byte(`{"record":{"value_base64":"","revision":1}}`)), "missing revision": wrap([]byte(`{"record":{"key":"k","value_base64":""}}`)),
		"foreign key":       wrap(bytes.ReplaceAll(base, []byte(`"key":"k"`), []byte(`"key":"other"`))),
		"duplicate key":     wrap(bytes.ReplaceAll(base, []byte(`"key":"k"`), []byte(`"key":"k","key":"k"`))),
		"escaped duplicate": wrap(bytes.ReplaceAll(base, []byte(`"key":"k"`), []byte(`"key":"k","\u006bey":"k"`))),
		"invalid UTF8":      append([]byte(`{"schema_version":1,"ok":true,"database_id":"owned-test-db","operation":"get","request_id":"request","result":{"record":{"key":"k","value_base64":"`), append([]byte{0xff}, []byte(`","revision":1}}}`)...)...),
		"base64 null":       wrap(bytes.ReplaceAll(base, []byte(`"AAH/"`), []byte(`null`))), "base64 newline": wrap(bytes.ReplaceAll(base, []byte(`"AAH/"`), []byte(`"AAH/\n"`))), "base64 padbits": wrap(bytes.ReplaceAll(base, []byte(`"AAH/"`), []byte(`"Zh=="`))), "base64 unpadded": wrap(bytes.ReplaceAll(base, []byte(`"AAH/"`), []byte(`"Zg"`))),
		"fraction revision": wrap(bytes.ReplaceAll(base, []byte(`18446744073709551615`), []byte(`1.0`))), "overflow revision": wrap(bytes.ReplaceAll(base, []byte(`18446744073709551615`), []byte(`18446744073709551616`))), "zero revision": wrap(bytes.ReplaceAll(base, []byte(`18446744073709551615`), []byte(`0`))), "negative revision": wrap(bytes.ReplaceAll(base, []byte(`18446744073709551615`), []byte(`-1`))),
		"nonUTC TTL": wrap([]byte(`{"record":{"key":"k","value_base64":"","revision":1,"expires_at":"2026-01-01T01:00:00+01:00"}}`)), "noncanonical TTL": wrap([]byte(`{"record":{"key":"k","value_base64":"","revision":1,"expires_at":"2026-01-01T00:00:00.000Z"}}`)),
		"trailing": append(wrap(base), []byte(` {}`)...),
	}
	success := wrap(base)
	for _, field := range []string{"schema_version", "ok", "database_id", "operation", "request_id", "result"} {
		tests["missing envelope "+field] = mutateRetainedJSON(t, success, field, nil)
	}
	for field, value := range map[string]string{"schema_version": "2", "ok": "null", "database_id": `"foreign"`, "operation": `"status"`, "request_id": `"foreign"`, "event": "{}", "registration_revision": "0", "extra": "true"} {
		tests["wrong envelope "+field] = mutateRetainedJSON(t, success, field, json.RawMessage(value))
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := transport.decode(raw, request, "k"); err == nil {
				t.Fatal("malformed accepted", string(raw))
			}
		})
	}
	for _, result := range [][]byte{base, []byte(`{"record":{"key":"k","value_base64":"","revision":1,"expires_at":null}}`), []byte(`{"record":{"key":"k","value_base64":"Zg==","revision":1,"expires_at":"2020-01-01T00:00:00Z"}}`)} {
		if _, err := transport.decode(wrap(result), request, "k"); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRetainedTransportStrictStatusAndErrors(t *testing.T) {
	request := retainedRequest{AdmittedWireVersion, "request", "status", nil}
	status := retainedTestStatus()
	transport := &retainedTransport{database: status.DatabaseID, ownerPID: status.OwnerPID, ownerStarted: status.OwnerStartedAt}
	raw := retainedJSON(t, status)
	fields, _ := retainedFields(raw)
	for name := range fields {
		for _, replacement := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`[]`)} {
			t.Run("type "+name+" "+string(replacement), func(t *testing.T) {
				payload := retainedSuccess(t, request, json.RawMessage(mutateRetainedJSON(t, raw, name, replacement)))
				if _, err := transport.decode(payload, request, ""); err == nil {
					t.Fatal("required status null/wrong type accepted")
				}
			})
		}
		t.Run("semantic alias "+name, func(t *testing.T) {
			alias := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"`+strings.ToUpper(name)+`":`+string(fields[name])+`}`)...)
			if _, err := transport.decode(retainedSuccess(t, request, json.RawMessage(alias)), request, ""); err == nil {
				t.Fatal("semantic duplicate/case alias accepted")
			}
		})
		t.Run("missing "+name, func(t *testing.T) {
			payload := retainedSuccess(t, request, json.RawMessage(mutateRetainedJSON(t, raw, name, nil)))
			if _, err := transport.decode(payload, request, ""); err == nil {
				t.Fatal("missing status field accepted")
			}
		})
	}
	for field, value := range map[string]string{"owner_pid": "1", "owner_started_at": `"2026-01-02T00:00:00Z"`, "database_id": `"foreign"`, "limits": "{}", "lifecycle": `"foreign"`, "current_revision": "18446744073709551616", "record_count": "1.5", "extra": "false"} {
		t.Run("wrong "+field, func(t *testing.T) {
			payload := retainedSuccess(t, request, json.RawMessage(mutateRetainedJSON(t, raw, field, json.RawMessage(value))))
			if _, err := transport.decode(payload, request, ""); err == nil {
				t.Fatal("wrong status field accepted")
			}
		})
	}
	limitsRaw := retainedJSON(t, status.Limits)
	limitFields, _ := retainedFields(limitsRaw)
	for name := range limitFields {
		for _, replacement := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`false`)} {
			t.Run("limits "+name+" "+string(replacement), func(t *testing.T) {
				changed := mutateRetainedJSON(t, limitsRaw, name, replacement)
				payload := retainedSuccess(t, request, json.RawMessage(mutateRetainedJSON(t, raw, "limits", changed)))
				if _, err := transport.decode(payload, request, ""); err == nil {
					t.Fatal("missing/null/wrong-type limit accepted")
				}
			})
		}
		t.Run("limits alias "+name, func(t *testing.T) {
			alias := append(append([]byte{}, limitsRaw[:len(limitsRaw)-1]...), []byte(`,"`+strings.ToUpper(name)+`":1}`)...)
			payload := retainedSuccess(t, request, json.RawMessage(mutateRetainedJSON(t, raw, "limits", alias)))
			if _, err := transport.decode(payload, request, ""); err == nil {
				t.Fatal("limit alias accepted")
			}
		})
	}
	// Genuine source zero counters and uint64 extremes remain exact and valid.
	status.CurrentRevision = ^gapdbapi.Revision(0)
	if _, err := transport.decode(retainedSuccess(t, request, status), request, ""); err != nil {
		t.Fatal(err)
	}
	errorFrame := func(remote map[string]any, op, id string) []byte {
		return retainedJSON(t, map[string]any{"schema_version": AdmittedWireVersion, "ok": false, "database_id": status.DatabaseID, "operation": op, "request_id": id, "error": remote})
	}
	definition := func(code gapdbapi.ErrorCode) map[string]any {
		for _, d := range gapdbapi.ErrorDefinitions() {
			if d.Code == code {
				return map[string]any{"code": code, "message": "error", "retry": d.Retry, "safe_actions": d.SafeActions}
			}
		}
		t.Fatal("definition absent")
		return nil
	}
	missing := definition(gapdbapi.CodeNotFound)
	missing["key"] = "k"
	missing["current_revision"] = uint64(0)
	_, err := transport.decode(errorFrame(missing, "status", "request"), request, "")
	var remote *gapdbapi.Error
	if !errors.As(err, &remote) || remote.Code != gapdbapi.CodeNotFound {
		t.Fatal("notfound classification lost", err)
	}
	busy := definition(gapdbapi.CodeServerBusy)
	busy["active_clients"], busy["maximum_clients"], busy["queue_depth"] = 0, 1, 0
	_, err = transport.decode(errorFrame(busy, "health", ""), request, "")
	if !errors.As(err, &remote) || remote.Code != gapdbapi.CodeServerBusy {
		t.Fatal("pre-admission busy exception lost", err)
	}
	for name, mutate := range map[string]func(map[string]any){"missing evidence": func(v map[string]any) { delete(v, "key") }, "foreign evidence": func(v map[string]any) { v["path"] = "x" }, "wrong retry": func(v map[string]any) { v["retry"] = "never" }, "wrong actions": func(v map[string]any) { v["safe_actions"] = []string{} }, "overflow evidence": func(v map[string]any) { v["current_revision"] = json.RawMessage(`18446744073709551616`) }, "null evidence": func(v map[string]any) { v["current_revision"] = nil }, "false applied": func(v map[string]any) { v["operation_applied"] = false }} {
		t.Run(name, func(t *testing.T) {
			v := definition(gapdbapi.CodeNotFound)
			v["key"] = "k"
			v["current_revision"] = 1
			mutate(v)
			_, err := transport.decode(errorFrame(v, "status", "request"), request, "")
			if err == nil || errors.As(err, &remote) {
				t.Fatal("invalid remote error accepted", err)
			}
		})
	}
	if _, err := transport.decode(errorFrame(missing, "get", "wrong"), request, ""); err == nil || errors.As(err, &remote) {
		t.Fatal("foreign error echo accepted", err)
	}
}

// A deterministic cancellation context exercises staged work without scheduling
// or an assertion that cancellation is instant. It is private test machinery.
type retainedCancelContext struct {
	context.Context
	calls atomic.Int32
	after int32
}

func (c *retainedCancelContext) Err() error {
	if c.calls.Add(1) >= c.after {
		return context.Canceled
	}
	return nil
}
func TestRetainedDecodeBudgetsAndCancellation(t *testing.T) {
	request := retainedRequest{AdmittedWireVersion, "request", "get", nil}
	transport := &retainedTransport{database: "owned-test-db"}
	value := bytes.Repeat([]byte{0, 1, 255, 3}, 1<<20)
	payload := retainedSuccess(t, request, map[string]any{"record": gapdbapi.Record{Key: "k", Value: value, Revision: 1}})
	result, err := transport.decodeContext(t.Context(), payload, request, "k")
	if err != nil || !bytes.Equal(result.record.Value, value) {
		t.Fatal("4 MiB checkpoint changed", err)
	}
	ctx := &retainedCancelContext{Context: context.Background(), after: 10}
	result, err = transport.decodeContext(ctx, payload, request, "k")
	if !errors.Is(err, context.Canceled) || result.record.Key != "" || result.record.Value != nil {
		t.Fatal("scanner cancellation leaked result", err)
	}
	node, err := retainedParse(t.Context(), []byte(`"`+strings.Repeat("AAAA", 1<<20)+`"`))
	// A standalone large string isn't a fixed record value; reject before allocation.
	if err == nil || node != nil {
		t.Fatal("unscoped giant string accepted")
	}
	for _, raw := range []string{
		strings.Repeat("[", retainedMaxDepth+2) + "0" + strings.Repeat("]", retainedMaxDepth+2),
		"[" + strings.Repeat("0,", 129) + "0]",
		`{"a":1,"\u0061":2}`, `{"a":1,}`, `[true,]`, `"\x00"`,
	} {
		if _, err := retainedParse(t.Context(), []byte(raw)); err == nil {
			t.Fatal("scanner grammar/budget accepted", raw)
		}
	}
	var many strings.Builder
	many.WriteByte('{')
	for i := 0; i < retainedMaxFields+1; i++ {
		if i != 0 {
			many.WriteByte(',')
		}
		many.WriteString(`"f` + strconv.Itoa(i) + `":0`)
	}
	many.WriteByte('}')
	if _, err := retainedParse(t.Context(), []byte(many.String())); err == nil {
		t.Fatal("field budget ignored")
	}
	base := &retainedNode{kind: '"', raw: []byte(`"` + strings.Repeat("AAAA", 1<<20) + `"`)}
	ctx = &retainedCancelContext{Context: context.Background(), after: 10}
	if decoded, err := retainedDecodeBase64(ctx, base); !errors.Is(err, context.Canceled) || decoded != nil {
		t.Fatal("base64 count cancellation ignored", err)
	}
	ctx = &retainedCancelContext{Context: context.Background(), after: 1100}
	if decoded, err := retainedDecodeBase64(ctx, base); !errors.Is(err, context.Canceled) || decoded != nil {
		t.Fatal("base64 decode cancellation ignored", err)
	}
	for _, raw := range []string{`"Zg=="`, `"\u005ag=="`, `"AAH\/"`} {
		n, err := retainedParse(t.Context(), []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := retainedDecodeBase64(t.Context(), n); err != nil {
			t.Fatal("JSON base64 escaping changed", err)
		}
	}
}
func TestRetainedCancelTransferredCheckpoint(t *testing.T) {
	sent := make(chan struct{})
	transport, _, _ := retainedFixture(t, func(conn *net.UnixConn, request retainedRequest) []byte {
		raw := retainedSuccess(t, request, map[string]any{"record": gapdbapi.Record{Key: "k", Value: make([]byte, 4<<20), Revision: 1}})
		if err := retainedWriteFrame(conn, raw); err != nil {
			t.Error(err)
		}
		close(sent)
		return nil
	})
	transport.timeout = 5 * time.Second
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type answer struct {
		record gapdbapi.Record
		err    error
	}
	done := make(chan answer, 1)
	go func() { record, err := transport.get(ctx, "k"); done <- answer{record, err} }()
	<-sent
	start := time.Now()
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) || got.record.Key != "" || got.record.Value != nil {
		t.Fatal("cancelled transferred result escaped", got.err)
	}
	t.Logf("transferred 4 MiB cancellation return lag=%s; bounded stages, not instantaneous cancellation guarantee", time.Since(start))
}

func TestRetainedTransportFrameBounds(t *testing.T) {
	for _, length := range []uint32{0, MaxWireFrameBytes + 1} {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], length)
		if _, err := retainedReadFrame(bytes.NewReader(prefix[:])); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	var buffer bytes.Buffer
	if err := retainedWriteFrame(&buffer, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := retainedReadFrame(&buffer)
	if err != nil || string(raw) != `{}` {
		t.Fatal(err)
	}
}
