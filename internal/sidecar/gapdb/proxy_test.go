package gapdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

const testProxySourceCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestResponseDropConsumesCompleteAcceptedResponseThenDrops(t *testing.T) {
	requestID := "gj02-selected"
	daemon, requests := startWireDaemon(t, func(request wireRequest) []byte {
		if request.RequestID == requestID {
			return acceptedResponse(t, request, 7, 1)
		}
		return ordinaryResponse(t, request)
	})
	productRoot := privateShortRoot(t)
	proxy, err := StartResponseDropProxy(ProxyConfig{
		ProductSocket: filepath.Join(productRoot, "product.sock"), DaemonSocket: daemon,
		ExpectedDaemon:     currentProcessIdentity(t),
		ExpectedDatabaseID: "database",
		SelectedRequestID:  requestID, SourceCommit: testProxySourceCommit, ReadTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxyEvidence := proxy.Evidence()
	if proxyEvidence.ProductSocketPath != proxy.config.ProductSocket || proxyEvidence.DaemonSocketPath != daemon ||
		proxyEvidence.SourceCommit != testProxySourceCommit || len(proxyEvidence.BinarySHA256) != 64 || proxyEvidence.ExpectedDatabaseID != "database" {
		t.Fatalf("proxy evidence=%+v", proxyEvidence)
	}
	selected := atomicBatchRequest(t, requestID)
	connection := dialUnix(t, proxy.config.ProductSocket)
	if err := writeWireFrame(connection, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := readWireFrame(connection, 1024); !errors.Is(err, io.EOF) {
		t.Fatalf("selected response bytes escaped: %v", err)
	}
	_ = connection.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	cut, err := proxy.WaitForCut(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cut.FaultID != "GJ-02" || cut.Cut != "after_durable_acceptance_before_client_response" ||
		cut.RequestID != requestID || cut.Revision != 7 || cut.MutationCount != 1 || cut.DaemonPID == 0 || cut.ProxyPID == 0 ||
		cut.SourceCommit != testProxySourceCommit || cut.ProxySHA256 == "" {
		t.Fatalf("cut=%+v", cut)
	}
	if requests.Load() != 1 {
		t.Fatalf("selected request forwarded %d times", requests.Load())
	}

	ordinary := ordinaryRequest(t, "later", "status")
	connection = dialUnix(t, proxy.config.ProductSocket)
	if err := writeWireFrame(connection, ordinary); err != nil {
		t.Fatal(err)
	}
	response, err := readWireFrame(connection, 1024)
	_ = connection.Close()
	if err != nil || string(response) != string(ordinaryResponse(t, wireRequest{SchemaVersion: 1, RequestID: "later", Operation: "status", Arguments: json.RawMessage(`{}`)})) {
		t.Fatalf("ordinary response=%s err=%v", response, err)
	}
	if requests.Load() != 2 {
		t.Fatalf("ordinary request census=%d", requests.Load())
	}
}

func TestResponseDropSupportsSeveralUnaryExchangesBeforeSelectedCut(t *testing.T) {
	daemon, requests := startWireDaemon(t, func(request wireRequest) []byte {
		if request.RequestID == "selected" {
			return acceptedResponse(t, request, 1, 1)
		}
		return ordinaryResponse(t, request)
	})
	root := privateShortRoot(t)
	proxy, err := StartResponseDropProxy(ProxyConfig{ProductSocket: filepath.Join(root, "p.sock"), DaemonSocket: daemon, ExpectedDaemon: currentProcessIdentity(t), ExpectedDatabaseID: "database", SelectedRequestID: "selected", SourceCommit: testProxySourceCommit, ReadTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	connection := dialUnix(t, proxy.config.ProductSocket)
	defer connection.Close()
	for _, id := range []string{"preflight-operation", "preflight-head"} {
		if err := writeWireFrame(connection, atomicBatchRequest(t, id)); err != nil {
			t.Fatal(err)
		}
		response, err := readWireFrame(connection, storegapdb.MaxWireFrameBytes)
		if err != nil {
			t.Fatalf("ordinary exchange %s closed reusable product connection: %v", id, err)
		}
		if !bytes.Equal(response, ordinaryResponse(t, wireRequest{RequestID: id, Operation: "atomic_batch"})) {
			t.Fatalf("ordinary response changed for %s", id)
		}
		if _, ok := proxy.Cut(); ok {
			t.Fatal("preflight traffic minted selected cut")
		}
	}
	if err := writeWireFrame(connection, atomicBatchRequest(t, "selected")); err != nil {
		t.Fatal(err)
	}
	var firstByte [1]byte
	if count, err := connection.Read(firstByte[:]); count != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("selected response leaked bytes or did not close: bytes=%d err=%v", count, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	cut, err := proxy.WaitForCut(ctx)
	if err != nil || cut.RequestID != "selected" || cut.Revision != 1 || requests.Load() != 3 {
		t.Fatalf("selected exchange proof: cut=%+v requests=%d err=%v", cut, requests.Load(), err)
	}
	if failures := proxy.Errors(); len(failures) != 0 {
		t.Fatalf("serial unary relay errors: %v", failures)
	}
	// Selection is global and one-shot, not reset by a new connection.
	next := dialUnix(t, proxy.config.ProductSocket)
	defer next.Close()
	if err := writeWireFrame(next, atomicBatchRequest(t, "selected")); err != nil {
		t.Fatal(err)
	}
	if _, err := readWireFrame(next, storegapdb.MaxWireFrameBytes); err != nil {
		t.Fatalf("later matching request was dropped again: %v", err)
	}
	if requests.Load() != 4 {
		t.Fatalf("proxy replayed/dropped request count=%d", requests.Load())
	}
}

func TestResponseDropCloseInterruptsBlockedDaemonWithoutWaitingForReadTimeout(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	daemon, requests := startWireDaemon(t, func(request wireRequest) []byte {
		close(entered)
		<-release
		return acceptedResponse(t, request, 1, 1)
	})
	root := privateShortRoot(t)
	proxy, err := StartResponseDropProxy(ProxyConfig{ProductSocket: filepath.Join(root, "p.sock"), DaemonSocket: daemon, ExpectedDaemon: currentProcessIdentity(t), ExpectedDatabaseID: "database", SelectedRequestID: "selected", SourceCommit: testProxySourceCommit, ReadTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	connection := dialUnix(t, proxy.config.ProductSocket)
	defer connection.Close()
	if err := writeWireFrame(connection, atomicBatchRequest(t, "selected")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request never reached daemon")
	}
	closed := make(chan error, 1)
	go func() { closed <- proxy.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close left daemon relay blocked until its read timeout")
	}
	if _, ok := proxy.Cut(); ok {
		t.Fatal("shutdown minted an unconsumed response cut")
	}
	if requests.Load() != 1 {
		t.Fatalf("shutdown replayed selected request: %d", requests.Load())
	}
}

func TestResponseDropRequiresExactMutationCountAndDatabase(t *testing.T) {
	tests := []struct {
		name              string
		requestMutations  int
		responseMutations int
		responseDatabase  string
	}{
		{"two-to-one-cardinality", 2, 1, "database"},
		{"wrong-database", 1, 1, "lookalike-database"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			daemon, _ := startWireDaemon(t, func(request wireRequest) []byte {
				return acceptedResponseDatabase(t, request, 7, test.responseMutations, test.responseDatabase)
			})
			root := privateShortRoot(t)
			proxy, err := StartResponseDropProxy(ProxyConfig{
				ProductSocket: filepath.Join(root, "p.sock"), DaemonSocket: daemon,
				ExpectedDaemon: currentProcessIdentity(t), ExpectedDatabaseID: "database",
				SelectedRequestID: "selected", SourceCommit: testProxySourceCommit, ReadTimeout: 200 * time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			connection := dialUnix(t, proxy.config.ProductSocket)
			if err := writeWireFrame(connection, atomicBatchRequestWithCount(t, "selected", test.requestMutations)); err != nil {
				t.Fatal(err)
			}
			_, _ = readWireFrame(connection, 1024)
			_ = connection.Close()
			deadline := time.Now().Add(time.Second)
			for len(proxy.Errors()) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if cut, ok := proxy.Cut(); ok {
				t.Fatalf("mismatched response produced cut: %+v", cut)
			}
			if len(proxy.Errors()) == 0 {
				t.Fatal("mismatched response produced no diagnostic")
			}
			if err := proxy.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponseDropPartialMalformedAndImpossibleResponsesAreNotGJ02(t *testing.T) {
	tests := []struct {
		name     string
		response func(net.Conn, wireRequest)
	}{
		{"partial", func(connection net.Conn, _ wireRequest) {
			var prefix [4]byte
			binary.BigEndian.PutUint32(prefix[:], 100)
			_, _ = connection.Write(prefix[:])
			_, _ = connection.Write([]byte(`{"schema_version":1}`))
		}},
		{"unknown-field", func(connection net.Conn, request wireRequest) {
			encoded := acceptedResponse(t, request, 7, 1)
			encoded = append(encoded[:len(encoded)-1], []byte(`,"unknown":true}`)...)
			_ = writeWireFrame(connection, encoded)
		}},
		{"duplicate-field", func(connection net.Conn, request wireRequest) {
			encoded := []byte(`{"schema_version":1,"schema_version":1,"ok":true,"request_id":"` + request.RequestID + `","database_id":"db","operation":"atomic_batch","result":{"revision":7,"ack":"durable","durable_through_revision":7,"mutation_count":1}}`)
			_ = writeWireFrame(connection, encoded)
		}},
		{"durable-ahead", func(connection net.Conn, request wireRequest) {
			encoded := []byte(`{"schema_version":1,"ok":true,"request_id":"` + request.RequestID + `","database_id":"db","operation":"atomic_batch","result":{"revision":7,"ack":"durable","durable_through_revision":8,"mutation_count":1}}`)
			_ = writeWireFrame(connection, encoded)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			daemon := startRawWireDaemon(t, test.response)
			root := privateShortRoot(t)
			proxy, err := StartResponseDropProxy(ProxyConfig{ProductSocket: filepath.Join(root, "p.sock"), DaemonSocket: daemon, ExpectedDaemon: currentProcessIdentity(t), ExpectedDatabaseID: "database", SelectedRequestID: "selected", SourceCommit: testProxySourceCommit, ReadTimeout: 200 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			connection := dialUnix(t, proxy.config.ProductSocket)
			if err := writeWireFrame(connection, atomicBatchRequest(t, "selected")); err != nil {
				t.Fatal(err)
			}
			_, _ = readWireFrame(connection, 1024)
			_ = connection.Close()
			deadline := time.Now().Add(time.Second)
			for len(proxy.Errors()) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if cut, ok := proxy.Cut(); ok {
				t.Fatalf("malformed response labeled GJ-02: %+v", cut)
			}
			if len(proxy.Errors()) == 0 {
				t.Fatal("malformed response produced no typed diagnostic")
			}
			if err := proxy.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponseDropSelectedRequestMustBeStrictDurableBatch(t *testing.T) {
	request := []byte(`{"schema_version":1,"request_id":"selected","operation":"atomic_batch","arguments":{"ack":"memory","mutations":[]}}`)
	if _, err := decodeWireRequest(request); err != nil {
		t.Fatal(err)
	}
	decoded, _ := decodeWireRequest(request)
	if err := validateSelectedRequest(decoded); !IsFailure(err, FailureProtocol) {
		t.Fatalf("non-durable selected request err=%v", err)
	}
	duplicate := []byte(`{"schema_version":1,"request_id":"selected","request_id":"other","operation":"atomic_batch","arguments":{}}`)
	if _, err := decodeWireRequest(duplicate); !IsFailure(err, FailureProtocol) {
		t.Fatalf("duplicate request field err=%v", err)
	}
}

func TestResponseDropLookalikeDaemonIdentityIsRefused(t *testing.T) {
	daemon, _ := startWireDaemon(t, func(request wireRequest) []byte { return ordinaryResponse(t, request) })
	identity := currentProcessIdentity(t)
	identity.StartTicks++
	root := privateShortRoot(t)
	_, err := StartResponseDropProxy(ProxyConfig{
		ProductSocket: filepath.Join(root, "p.sock"), DaemonSocket: daemon,
		ExpectedDaemon: identity, SelectedRequestID: "selected", SourceCommit: testProxySourceCommit, ReadTimeout: time.Second,
		ExpectedDatabaseID: "database",
	})
	if !IsFailure(err, FailureOwnership) {
		t.Fatalf("lookalike daemon err=%v", err)
	}
}

func TestResponseDropShutdownNeverDuplicatesMutatingRequest(t *testing.T) {
	blocked := make(chan struct{})
	var releaseOnce sync.Once
	releaseBlocked := func() { releaseOnce.Do(func() { close(blocked) }) }
	defer releaseBlocked()
	daemon, requests := startWireDaemon(t, func(request wireRequest) []byte {
		<-blocked
		return acceptedResponse(t, request, 1, 1)
	})
	root := privateShortRoot(t)
	proxy, err := StartResponseDropProxy(ProxyConfig{ProductSocket: filepath.Join(root, "p.sock"), DaemonSocket: daemon, ExpectedDaemon: currentProcessIdentity(t), ExpectedDatabaseID: "database", SelectedRequestID: "selected", SourceCommit: testProxySourceCommit, ReadTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	connection := dialUnix(t, proxy.config.ProductSocket)
	if err := writeWireFrame(connection, atomicBatchRequest(t, "selected")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for requests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- proxy.Close() }()
	releaseBlocked()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy shutdown blocked")
	}
	_ = connection.Close()
	if requests.Load() != 1 {
		t.Fatalf("request was forwarded %d times", requests.Load())
	}
}

func TestResponseDropCleanupRefusesReplacedProductSocket(t *testing.T) {
	daemon, _ := startWireDaemon(t, func(request wireRequest) []byte { return ordinaryResponse(t, request) })
	root := privateShortRoot(t)
	product := filepath.Join(root, "p.sock")
	proxy, err := StartResponseDropProxy(ProxyConfig{
		ProductSocket: product, DaemonSocket: daemon, ExpectedDaemon: currentProcessIdentity(t),
		ExpectedDatabaseID: "database",
		SelectedRequestID:  "selected", SourceCommit: testProxySourceCommit, ReadTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(product); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(product, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Close(); !IsFailure(err, FailureCleanup) {
		t.Fatalf("replaced socket cleanup err=%v", err)
	}
	if encoded, err := os.ReadFile(product); err != nil || string(encoded) != "foreign" {
		t.Fatalf("foreign replacement changed: %q %v", encoded, err)
	}
}

func TestResponseDropRealAdmittedLifecycleAndReconcile(t *testing.T) {
	lifecycle := startTestLifecycle(t, "proxy-real")
	defer stopTestLifecycle(t, lifecycle)
	root := privateShortRoot(t)
	productSocket := filepath.Join(root, "p.sock")
	proxy, err := StartResponseDropProxy(ProxyConfig{
		ProductSocket: productSocket, DaemonSocket: lifecycle.Provenance().ConfiguredSocket,
		ExpectedDaemon:     lifecycle.Provenance().Process,
		ExpectedDatabaseID: lifecycle.Provenance().DatabaseID,
		SelectedRequestID:  "real-gj02", SourceCommit: testProxySourceCommit, ReadTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	client, err := gapdbapi.Dial(productSocket, gapdbapi.ClientOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := gapdbapi.WithRequestID(t.Context(), "real-gj02")
	batch := gapdbapi.Batch{Ack: gapdbapi.AckDurable, Mutations: []gapdbapi.Mutation{
		gapdbapi.NewPutMutation("proxy/operation", []byte("accepted"), gapdbapi.Condition{Kind: gapdbapi.ConditionAbsent}, nil),
	}}
	_, err = client.AtomicBatch(ctx, batch)
	var transport *gapdbapi.TransportError
	if !errors.As(err, &transport) || !transport.Ambiguous {
		t.Fatalf("lost response was not public ambiguity: %v", err)
	}
	wait, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	cut, err := proxy.WaitForCut(wait)
	if err != nil || cut.RequestID != "real-gj02" || cut.Revision == 0 {
		t.Fatalf("cut=%+v err=%v", cut, err)
	}
	if cut.DaemonPID != lifecycle.Provenance().Process.PID || cut.ProxyPID != os.Getpid() || cut.DaemonPID == cut.ProxyPID {
		t.Fatalf("proxy and daemon identities were conflated: %+v", cut)
	}
	record, err := client.Get(t.Context(), "proxy/operation")
	if err != nil || string(record.Value) != "accepted" || record.Revision != gapdbapi.Revision(cut.Revision) {
		t.Fatalf("reconcile record=%+v err=%v", record, err)
	}
}

func atomicBatchRequest(t *testing.T, requestID string) []byte {
	return atomicBatchRequestWithCount(t, requestID, 1)
}

func atomicBatchRequestWithCount(t *testing.T, requestID string, count int) []byte {
	t.Helper()
	mutations := make([]gapdbapi.Mutation, 0, count)
	for index := 0; index < count; index++ {
		mutations = append(mutations, gapdbapi.NewPutMutation(fmt.Sprintf("key-%d", index), []byte("value"), gapdbapi.Condition{Kind: gapdbapi.ConditionAbsent}, nil))
	}
	batch := gapdbapi.Batch{Ack: gapdbapi.AckDurable, Mutations: mutations}
	encoded, err := json.Marshal(wireRequest{SchemaVersion: 1, RequestID: requestID, Operation: "atomic_batch", Arguments: mustJSON(t, batch)})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func ordinaryRequest(t *testing.T, requestID, operation string) []byte {
	t.Helper()
	encoded, err := json.Marshal(wireRequest{SchemaVersion: 1, RequestID: requestID, Operation: operation, Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func acceptedResponse(t *testing.T, request wireRequest, revision uint64, count int) []byte {
	return acceptedResponseDatabase(t, request, revision, count, "database")
}

func acceptedResponseDatabase(t *testing.T, request wireRequest, revision uint64, count int, database string) []byte {
	t.Helper()
	result := gapdbapi.MutationResult{Revision: gapdbapi.Revision(revision), Ack: gapdbapi.AckDurable, DurableThroughRevision: gapdbapi.Revision(revision), MutationCount: count}
	encoded, err := json.Marshal(wireResponse{SchemaVersion: 1, OK: true, RequestID: request.RequestID, DatabaseID: database, Operation: request.Operation, Result: mustJSON(t, result)})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func ordinaryResponse(t *testing.T, request wireRequest) []byte {
	t.Helper()
	encoded, err := json.Marshal(wireResponse{SchemaVersion: 1, OK: true, RequestID: request.RequestID, DatabaseID: "database", Operation: request.Operation, Result: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func startWireDaemon(t *testing.T, response func(wireRequest) []byte) (string, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	socket := startRawWireDaemon(t, func(connection net.Conn, request wireRequest) {
		requests.Add(1)
		unixConnection, ok := connection.(*net.UnixConn)
		if !ok { t.Error("fixture connection is not an actual UnixConn"); return }
		var source strictFixtureSourceOutcome
		defer func() { source.report(t) }()
		payload := response(request)
		source.writeAttempted = true
		source.writeErr = writeWireFrame(unixConnection, payload)
		if source.writeErr == nil { source.shutdownAttempted = true; source.shutdownErr = unixConnection.CloseWrite() }
	})
	return socket, &requests
}

func startRawWireDaemon(t *testing.T, respond func(net.Conn, wireRequest)) string {
	t.Helper()
	root := privateShortRoot(t)
	socket := filepath.Join(root, "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		if first := listener.Close(); first != nil { t.Errorf("FIRST fixture listener close after chmod failure: %v", first) }
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	done := make(chan struct{})
	t.Cleanup(func() {
		if first := listener.Close(); first != nil { t.Errorf("FIRST fixture listener close failed: %v", first) }
		<-done
		workers.Wait() // source FIRST outcomes cannot outlive the fixture
	})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { source := strictFixtureSourceOutcome{closeAttempted: true, closeErr: connection.Close()}; source.report(t) }()
				if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil { t.Error("fixture server read deadline", err); return }
				encoded, err := readWireFrame(connection, 1<<20)
				if err != nil {
					return
				}
				request, err := decodeWireRequest(encoded)
				if err != nil {
					return
				}
				respond(connection, request)
			}()
		}
	}()
	return socket
}

func currentProcessIdentity(t *testing.T) ProcessIdentity {
	t.Helper()
	start, err := processStartTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity, digest, err := processExecutable(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	return ProcessIdentity{PID: os.Getpid(), StartTicks: start, Device: identity.device, Inode: identity.inode, Size: identity.size, SHA256: digest}
}

func FuzzResponseDropStrictWireDecoders(f *testing.F) {
	f.Add(atomicBatchRequestForFuzz("selected"))
	f.Add([]byte(`{"schema_version":1,"schema_version":1}`))
	f.Add([]byte{0, 1, 2, 3})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > 1<<20 {
			t.Skip()
		}
		_ = validateNoDuplicateJSON(encoded)
		request, err := decodeWireRequest(encoded)
		if err == nil && request.Operation == "atomic_batch" {
			_ = validateSelectedRequest(request)
			_, _, _ = decodeAcceptedMutationResponse(encoded, request, 1, "database")
		}
	})
}

func atomicBatchRequestForFuzz(requestID string) []byte {
	batch := gapdbapi.Batch{Ack: gapdbapi.AckDurable, Mutations: []gapdbapi.Mutation{
		gapdbapi.NewPutMutation("key", []byte("value"), gapdbapi.Condition{Kind: gapdbapi.ConditionAbsent}, nil),
	}}
	arguments, _ := json.Marshal(batch)
	encoded, _ := json.Marshal(wireRequest{SchemaVersion: 1, RequestID: requestID, Operation: "atomic_batch", Arguments: arguments})
	return encoded
}

func dialUnix(t *testing.T, socket string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	return connection
}
