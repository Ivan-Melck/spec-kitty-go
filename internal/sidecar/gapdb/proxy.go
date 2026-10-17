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
	"syscall"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

// ResponseDropCut is emitted only after a complete, strict durable response
// has been consumed and before any byte is written to the product connection.
type ResponseDropCut struct {
	FaultID            string
	Cut                string
	RequestID          string
	Operation          string
	DatabaseID         string
	Revision           uint64
	MutationCount      int
	ExpectedDatabaseID string
	DaemonSocket       string
	ProductSocket      string
	DaemonPID          int
	ProxyPID           int
	SourceCommit       string
	ProxySHA256        string
	ObservedAt         time.Time
}

// ProxyConfig selects one request by stable protocol identity. The first exact
// matching durable response is dropped; later traffic is ordinary pass-through.
type ProxyConfig struct {
	ProductSocket      string
	DaemonSocket       string
	ExpectedDaemon     ProcessIdentity
	ExpectedDatabaseID string
	SelectedRequestID  string
	SourceCommit       string
	ReadTimeout        time.Duration
}

// ProxyEvidence binds the qualification proxy to the lifecycle database as
// well as to its frozen artifact projection.
type ProxyEvidence struct {
	QualificationProxyEvidenceV1
	ExpectedDatabaseID string `json:"expected_database_id"`
}

// ResponseDropProxy is a qualification-only, protocol-aware Unix proxy.
type ResponseDropProxy struct {
	mu            sync.Mutex
	config        ProxyConfig
	listener      *net.UnixListener
	connections   map[net.Conn]struct{}
	cut           *ResponseDropCut
	reserved      bool
	errors        []error
	closed        bool
	wait          sync.WaitGroup
	socketID      fileIdentity
	proxyIdentity ProcessIdentity
}

// StartResponseDropProxy binds the explicit product endpoint. It does not
// alter, replace, or remove the daemon endpoint.
func StartResponseDropProxy(config ProxyConfig) (*ResponseDropProxy, error) {
	if err := normalizeProxyConfig(&config); err != nil {
		return nil, err
	}
	if err := validatePrivateRoot(filepath.Dir(config.ProductSocket)); err != nil {
		return nil, failure(FailureUnsafePath, "proxy_start", "product socket parent is not private", err)
	}
	if err := validateDaemonEndpoint(config.DaemonSocket, config.ExpectedDaemon, config.ReadTimeout); err != nil {
		return nil, err
	}
	proxyIdentity, err := currentExecutableIdentity()
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(config.ProductSocket); err == nil {
		return nil, failure(FailureCleanup, "proxy_start", "product endpoint already exists", nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, failure(FailureCleanup, "proxy_start", "product endpoint cannot be inspected", err)
	}
	address := &net.UnixAddr{Name: config.ProductSocket, Net: "unix"}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		return nil, failure(FailureStart, "proxy_start", "product endpoint cannot be bound", err)
	}
	// net.UnixListener otherwise unlinks the pathname unconditionally on Close,
	// which could delete a foreign replacement after a same-UID rename race.
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(config.ProductSocket, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(config.ProductSocket)
		return nil, failure(FailureStart, "proxy_start", "product endpoint mode cannot be set", err)
	}
	info, err := os.Lstat(config.ProductSocket)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(config.ProductSocket)
		return nil, failure(FailureStart, "proxy_start", "product endpoint identity cannot be read", err)
	}
	identity, err := identityFromInfo(info)
	if err != nil || identity.mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFSOCK) {
		_ = listener.Close()
		_ = os.Remove(config.ProductSocket)
		return nil, failure(FailureStart, "proxy_start", "product endpoint is not a socket", err)
	}
	proxy := &ResponseDropProxy{
		config: config, listener: listener, connections: make(map[net.Conn]struct{}),
		socketID: identity, proxyIdentity: proxyIdentity,
	}
	proxy.wait.Add(1)
	go proxy.serve()
	return proxy, nil
}

func normalizeProxyConfig(config *ProxyConfig) error {
	if err := validateAbsolutePath(config.ProductSocket); err != nil {
		return failure(FailureInvalidConfiguration, "proxy_configure", "product socket path is invalid", err)
	}
	if err := validateAbsolutePath(config.DaemonSocket); err != nil {
		return failure(FailureInvalidConfiguration, "proxy_configure", "daemon socket path is invalid", err)
	}
	if config.ProductSocket == config.DaemonSocket || len(config.ProductSocket) > maxSocketPathBytes || len(config.DaemonSocket) > maxSocketPathBytes {
		return failure(FailureInvalidConfiguration, "proxy_configure", "proxy endpoints are equal or exceed the Unix bound", nil)
	}
	if config.SelectedRequestID == "" || len(config.SelectedRequestID) > storegapdb.MaxOperationIDBytes {
		return failure(FailureInvalidConfiguration, "proxy_configure", "selected request identity is invalid", nil)
	}
	if config.ExpectedDatabaseID == "" || len(config.ExpectedDatabaseID) > 128 {
		return failure(FailureInvalidConfiguration, "proxy_configure", "expected database identity is invalid", nil)
	}
	if config.ExpectedDaemon.PID <= 0 || config.ExpectedDaemon.StartTicks == 0 || config.ExpectedDaemon.Device == 0 ||
		config.ExpectedDaemon.Inode == 0 || config.ExpectedDaemon.Size <= 0 || len(config.ExpectedDaemon.SHA256) != 64 {
		return failure(FailureInvalidConfiguration, "proxy_configure", "expected daemon identity is incomplete", nil)
	}
	if len(config.SourceCommit) != 40 {
		return failure(FailureInvalidConfiguration, "proxy_configure", "proxy source commit is invalid", nil)
	}
	for _, character := range config.SourceCommit {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return failure(FailureInvalidConfiguration, "proxy_configure", "proxy source commit is invalid", nil)
		}
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = defaultStartTimeout
	}
	if config.ReadTimeout <= 0 || config.ReadTimeout > time.Minute {
		return failure(FailureInvalidConfiguration, "proxy_configure", "proxy timeout is outside bound", nil)
	}
	return nil
}

func validateDaemonEndpoint(socket string, expected ProcessIdentity, timeout time.Duration) error {
	if err := validatePathChain(socket, false); err != nil {
		return failure(FailureUnsafePath, "proxy_start", "daemon endpoint path is unsafe", err)
	}
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return failure(FailureOwnership, "proxy_start", "daemon endpoint is not a socket", err)
	}
	peer, err := peerProcess(socket, timeout)
	if err != nil || peer != expected.PID {
		return failure(FailureOwnership, "proxy_start", "daemon endpoint peer differs", err)
	}
	return validateProcessIdentity(expected)
}

func validateConnectedDaemon(connection net.Conn, expected ProcessIdentity) error {
	peer, err := peerProcessOfConnection(connection)
	if err != nil || peer != expected.PID {
		return failure(FailureOwnership, "proxy_dial", "connected daemon peer differs", err)
	}
	return validateProcessIdentity(expected)
}

func validateProcessIdentity(expected ProcessIdentity) error {
	return validateProcessIdentityWith(systemProcessAPI, expected)
}

func validateProcessIdentityWith(processes processAPI, expected ProcessIdentity) error {
	start, err := processes.startTicks(expected.PID)
	if err != nil || start != expected.StartTicks {
		return failure(FailureOwnership, "proxy_identity", "daemon process start differs", err)
	}
	identity, digest, err := processes.executable(expected.PID)
	if err != nil || identity.device != expected.Device || identity.inode != expected.Inode ||
		identity.size != expected.Size || digest != expected.SHA256 {
		return failure(FailureOwnership, "proxy_identity", "daemon executable identity differs", err)
	}
	return nil
}

func currentExecutableIdentity() (ProcessIdentity, error) {
	return currentExecutableIdentityWithReport(nil)
}

func currentExecutableIdentityWithReport(report *expiryCleanupReport) (ProcessIdentity, error) {
	pid := os.Getpid()
	start, err := processStartTicks(pid)
	if err != nil {
		return ProcessIdentity{}, failure(FailureIdentityUnprovable, "proxy_identity", "proxy process start is unavailable", err)
	}
	identity, digest, err := processExecutableWithReport(pid, report)
	if err != nil {
		return ProcessIdentity{}, failure(FailureIdentityUnprovable, "proxy_identity", "proxy executable is unavailable", err)
	}
	return ProcessIdentity{
		PID: pid, StartTicks: start, Device: identity.device, Inode: identity.inode,
		Size: identity.size, SHA256: digest,
	}, nil
}

// Evidence returns the proxy identity projected into the frozen sidecar
// provenance schema. The binary digest is derived from the running proxy.
func (p *ResponseDropProxy) Evidence() ProxyEvidence {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProxyEvidence{
		QualificationProxyEvidenceV1: QualificationProxyEvidenceV1{
			ProductSocketPath: p.config.ProductSocket, DaemonSocketPath: p.config.DaemonSocket,
			SourceCommit: p.config.SourceCommit, BinarySHA256: p.proxyIdentity.SHA256,
		},
		ExpectedDatabaseID: p.config.ExpectedDatabaseID,
	}
}

func (p *ResponseDropProxy) serve() {
	defer p.wait.Done()
	for {
		connection, err := p.listener.AcceptUnix()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			if !closed {
				p.errors = appendBoundedError(p.errors, err)
			}
			p.mu.Unlock()
			return
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = connection.Close()
			return
		}
		p.connections[connection] = struct{}{}
		p.wait.Add(1)
		p.mu.Unlock()
		go p.handle(connection)
	}
}

func (p *ResponseDropProxy) handle(product *net.UnixConn) {
	defer p.wait.Done()
	defer func() {
		_ = product.Close()
		p.mu.Lock()
		delete(p.connections, product)
		p.mu.Unlock()
	}()
	for p.relayUnaryExchange(product) {
		// A new complete frame is a new exchange, never a replay of the prior one.
	}
}

// relayUnaryExchange keeps product connection reuse separate from the one-shot
// selected cut. Each request is independently bounded, attributed and sent once.
// A false return closes the product connection on cut, refusal or EOF.
func (p *ResponseDropProxy) relayUnaryExchange(product *net.UnixConn) (forwarded bool) {
	_ = product.SetDeadline(time.Now().Add(p.config.ReadTimeout))
	requestBytes, err := readWireFrame(product, storegapdb.MaxWireFrameBytes)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			p.recordError(failure(FailureProtocol, "proxy_request", "product request frame is incomplete", err))
		}
		return
	}
	request, err := decodeWireRequest(requestBytes)
	if err != nil {
		p.recordError(err)
		return
	}
	selected := false
	selectedMutationCount := 0
	if request.SchemaVersion == storegapdb.AdmittedWireVersion && request.Operation == "atomic_batch" && request.RequestID == p.config.SelectedRequestID {
		count, err := selectedRequestMutationCount(request)
		if err != nil {
			p.recordError(err)
			return
		}
		selectedMutationCount = count
		selected = p.reserveSelection()
	}

	daemon, err := net.DialTimeout("unix", p.config.DaemonSocket, p.config.ReadTimeout)
	if err != nil {
		p.recordError(failure(FailureProtocol, "proxy_dial", "daemon endpoint is unavailable", err))
		return
	}
	defer daemon.Close()
	// Register the relay before observation/publication so Close interrupts its
	// read as well as the product read. If shutdown won the race, send nothing.
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.connections[daemon] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.connections, daemon)
		p.mu.Unlock()
	}()
	if err := validateConnectedDaemon(daemon, p.config.ExpectedDaemon); err != nil {
		p.recordError(err)
		return
	}
	_ = daemon.SetDeadline(time.Now().Add(p.config.ReadTimeout))
	// The exact complete request is published once. There is no retry loop in
	// the proxy, including when shutdown races the response.
	if err := writeWireFrame(daemon, requestBytes); err != nil {
		p.recordError(failure(FailureProtocol, "proxy_publish", "request publication failed", err))
		return
	}
	responseBytes, err := readWireFrame(daemon, storegapdb.MaxWireFrameBytes)
	if err != nil {
		p.recordError(failure(FailureProtocol, "proxy_response", "daemon response frame is incomplete", err))
		return
	}
	if selected {
		response, result, err := decodeAcceptedMutationResponse(responseBytes, request, selectedMutationCount, p.config.ExpectedDatabaseID)
		if err != nil {
			p.recordError(err)
			return
		}
		cut := ResponseDropCut{
			FaultID: "GJ-02", Cut: "after_durable_acceptance_before_client_response",
			RequestID: request.RequestID, Operation: request.Operation,
			DatabaseID: response.DatabaseID, Revision: uint64(result.Revision),
			MutationCount: result.MutationCount, ExpectedDatabaseID: p.config.ExpectedDatabaseID, DaemonSocket: p.config.DaemonSocket,
			ProductSocket: p.config.ProductSocket, DaemonPID: p.config.ExpectedDaemon.PID,
			ProxyPID: os.Getpid(), SourceCommit: p.config.SourceCommit,
			ProxySHA256: p.proxyIdentity.SHA256, ObservedAt: time.Now().UTC(),
		}
		if p.recordCut(cut) {
			// The complete accepted frame has been consumed. Closing product here,
			// without any Write, is the actual GJ-02 ambiguity cut.
			return
		}
	}
	if err := writeWireFrame(product, responseBytes); err != nil {
		p.recordError(failure(FailureProtocol, "proxy_forward", "ordinary response forwarding failed", err))
		return
	}
	return true
}

type wireRequest struct {
	SchemaVersion uint64          `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Operation     string          `json:"operation"`
	Arguments     json.RawMessage `json:"arguments"`
}

type wireResponse struct {
	SchemaVersion uint64          `json:"schema_version"`
	OK            bool            `json:"ok"`
	RequestID     string          `json:"request_id"`
	DatabaseID    string          `json:"database_id"`
	Operation     string          `json:"operation"`
	Result        json.RawMessage `json:"result"`
}

func decodeWireRequest(encoded []byte) (wireRequest, error) {
	if err := validateNoDuplicateJSON(encoded); err != nil {
		return wireRequest{}, failure(FailureProtocol, "proxy_request", "request JSON is not unique", err)
	}
	var request wireRequest
	if err := decodeStrictJSON(encoded, &request); err != nil {
		return wireRequest{}, failure(FailureProtocol, "proxy_request", "request envelope is malformed", err)
	}
	if request.SchemaVersion != storegapdb.AdmittedWireVersion || request.RequestID == "" ||
		len(request.RequestID) > storegapdb.MaxOperationIDBytes || request.Operation == "" || len(request.Arguments) == 0 {
		return wireRequest{}, failure(FailureProtocol, "proxy_request", "request envelope is incomplete", nil)
	}
	return request, nil
}

func validateSelectedRequest(request wireRequest) error {
	_, err := selectedRequestMutationCount(request)
	return err
}

func selectedRequestMutationCount(request wireRequest) (int, error) {
	var batch gapdbapi.Batch
	if err := decodeStrictJSON(request.Arguments, &batch); err != nil {
		return 0, failure(FailureProtocol, "proxy_request", "selected atomic batch is malformed", err)
	}
	if batch.Ack != gapdbapi.AckDurable || len(batch.Mutations) == 0 || len(batch.Mutations) > storegapdb.MaxMutationsPerBatch {
		return 0, failure(FailureProtocol, "proxy_request", "selected atomic batch is not a bounded durable mutation", nil)
	}
	if err := batch.Validate(gapdbapi.DefaultOptions().Limits); err != nil {
		return 0, failure(FailureProtocol, "proxy_request", "selected atomic batch violates protocol v1", err)
	}
	return len(batch.Mutations), nil
}

func decodeAcceptedMutationResponse(encoded []byte, request wireRequest, expectedMutationCount int, expectedDatabaseID string) (wireResponse, gapdbapi.MutationResult, error) {
	if err := validateNoDuplicateJSON(encoded); err != nil {
		return wireResponse{}, gapdbapi.MutationResult{}, failure(FailureProtocol, "proxy_response", "response JSON is not unique", err)
	}
	var response wireResponse
	if err := decodeStrictJSON(encoded, &response); err != nil {
		return wireResponse{}, gapdbapi.MutationResult{}, failure(FailureProtocol, "proxy_response", "response envelope is malformed", err)
	}
	if response.SchemaVersion != storegapdb.AdmittedWireVersion || !response.OK || response.RequestID != request.RequestID ||
		response.Operation != request.Operation || response.DatabaseID != expectedDatabaseID || len(response.Result) == 0 {
		return wireResponse{}, gapdbapi.MutationResult{}, failure(FailureProtocol, "proxy_response", "response does not match selected request", nil)
	}
	var result gapdbapi.MutationResult
	if err := decodeStrictJSON(response.Result, &result); err != nil {
		return wireResponse{}, gapdbapi.MutationResult{}, failure(FailureProtocol, "proxy_response", "mutation result is malformed", err)
	}
	if result.Revision == 0 || result.Ack != gapdbapi.AckDurable ||
		result.DurableThroughRevision != result.Revision || result.MutationCount != expectedMutationCount || expectedMutationCount <= 0 {
		return wireResponse{}, gapdbapi.MutationResult{}, failure(FailureProtocol, "proxy_response", "response is not exact durable acceptance", nil)
	}
	return response, result, nil
}

func decodeStrictJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func validateNoDuplicateJSON(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing token %v", token)
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object field %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func readWireFrame(reader io.Reader, maximum int) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(prefix[:]))
	if length <= 0 || length > maximum {
		return nil, fmt.Errorf("invalid frame length %d", length)
	}
	payload := make([]byte, length)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func writeWireFrame(writer io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > storegapdb.MaxWireFrameBytes {
		return errors.New("frame payload is outside product bound")
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(payload)))
	for _, value := range [][]byte{prefix[:], payload} {
		for len(value) > 0 {
			written, err := writer.Write(value)
			if written < 0 || written > len(value) {
				return io.ErrShortWrite
			}
			value = value[written:]
			if err != nil {
				return err
			}
			if written == 0 {
				return io.ErrShortWrite
			}
		}
	}
	return nil
}

func (p *ResponseDropProxy) reserveSelection() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reserved || p.cut != nil || p.closed {
		return false
	}
	p.reserved = true
	return true
}

func (p *ResponseDropProxy) recordCut(cut ResponseDropCut) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.reserved || p.cut != nil || p.closed {
		return false
	}
	copy := cut
	p.cut = &copy
	return true
}

func (p *ResponseDropProxy) recordError(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.errors = appendBoundedError(p.errors, err)
	p.mu.Unlock()
}

func appendBoundedError(existing []error, err error) []error {
	if len(existing) >= 16 {
		return existing
	}
	return append(existing, err)
}

// Cut returns the completed GJ-02 record, if and only if the exact response
// consumption invariant was satisfied.
func (p *ResponseDropProxy) Cut() (ResponseDropCut, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cut == nil {
		return ResponseDropCut{}, false
	}
	return *p.cut, true
}

// Errors returns bounded typed proxy failures. A malformed/partial response is
// observable here and is never represented as a GJ-02 cut.
func (p *ResponseDropProxy) Errors() []error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]error(nil), p.errors...)
}

// WaitForCut waits for real durable-response consumption; it never fabricates
// a timeout result or cut record.
func (p *ResponseDropProxy) WaitForCut(ctx context.Context) (ResponseDropCut, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if cut, ok := p.Cut(); ok {
			return cut, nil
		}
		select {
		case <-ctx.Done():
			return ResponseDropCut{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Close stops accepting and interrupts active relays. It never retries a
// request. The product socket is removed only if its identity is unchanged.
func (p *ResponseDropProxy) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	listener := p.listener
	connections := make([]net.Conn, 0, len(p.connections))
	for connection := range p.connections {
		connections = append(connections, connection)
	}
	p.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
	p.wait.Wait()
	info, err := os.Lstat(p.config.ProductSocket)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return failure(FailureCleanup, "proxy_close", "product endpoint cannot be inspected", err)
	}
	identity, err := identityFromInfo(info)
	if err != nil || identity.device != p.socketID.device || identity.inode != p.socketID.inode {
		return failure(FailureCleanup, "proxy_close", "product endpoint identity changed", err)
	}
	if err := os.Remove(p.config.ProductSocket); err != nil {
		return failure(FailureCleanup, "proxy_close", "owned product endpoint cannot be removed", err)
	}
	return nil
}
