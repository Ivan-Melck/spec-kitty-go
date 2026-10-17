package gapdb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
)

// ownedProvider redirects EVERY provider.Get/Status consumer, including private
// v2 readers, ambiguity resolution and claims. Other operations stay on the SDK;
// ReadMany/snapshots/health/config are not attributed observation evidence.
type ownedProvider struct {
	provider
	transport *retainedTransport
}

func (p *ownedProvider) Get(ctx context.Context, key string) (gapdbapi.Record, error) {
	return p.transport.get(ctx, key)
}
func (p *ownedProvider) Status(ctx context.Context) (gapdbapi.StatusResult, error) {
	return p.transport.status(ctx)
}
func (p *ownedProvider) ReadRecoverySnapshot(ctx context.Context, request gapdbapi.RecoverySnapshotRequest) (gapdbapi.RecoverySnapshotResult, error) {
	return p.provider.(recoverySnapshotProvider).ReadRecoverySnapshot(ctx, request)
}

// There is deliberately no exported unary client, key selector, request factory,
// callback or connection port. One actual fresh Unix connection serves each RPC.
type retainedTransport struct {
	lifetime                         *retainedkernel.Lifetime
	endpoint, database, ownerStarted string
	ownerPID                         int
	timeout                          time.Duration
	requests                         atomic.Uint64
	mu                               sync.Mutex
	closed                           bool
	active                           *retainedConnectionNode
	dials                            sync.WaitGroup // Add only under mu while !closed
	dialStop                         chan struct{} // mu; Close cancels existing actual dials
	closeOnce                        sync.Once
	closeErr                         error // immutable entire FIRST Close outcome
	connectionCloseErr               error // mu; every actual owned close result
	connectionCloseAttempts          uint64 // mu; absent != actual successful close
}

// Stable fixed-size intrusive nodes, never copied once registered. All links
// and conn fields are owned by t.mu. owner.conn is immutable after actual dial;
// clearing node.conn during unlink cannot race cancellation/global/deferred close.
type retainedConnectionNode struct {
	owner      *retainedConnectionClose
	conn       *net.UnixConn
	prev, next *retainedConnectionNode
	linked     bool
}

// registerConnection is called only AFTER actual dial and BEFORE precheck.
func (t *retainedTransport) registerConnection(conn *net.UnixConn) *retainedConnectionNode {
	node, _ := t.registerConnectionResult(conn) // legacy private registry-test entry
	return node
}

func (t *retainedTransport) registerConnectionResult(conn *net.UnixConn) (*retainedConnectionNode, error) {
	owner := &retainedConnectionClose{conn: conn} // AFTER actual dial only
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, t.closeConnection(owner)
	}
	node := &retainedConnectionNode{conn: conn, owner: owner, next: t.active, linked: true}
	if t.active != nil {
		t.active.prev = node
	}
	t.active = node
	t.mu.Unlock()
	return node, nil
}

// Caller holds t.mu, after the existing lifetime/context/closed postchecks.
// Close detaches every node; subsequent deferred cleanup is harmless.
func (t *retainedTransport) unlinkConnection(node *retainedConnectionNode) {
	if !node.linked {
		return
	}
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		t.active = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	}
	node.linked, node.prev, node.next, node.conn = false, nil, nil, nil
}

type retainedRequest struct {
	SchemaVersion uint64 `json:"schema_version"`
	RequestID     string `json:"request_id"`
	Operation     string `json:"operation"`
	Arguments     any    `json:"arguments"`
}
type retainedEnvelope struct {
	SchemaVersion        *uint64                 `json:"schema_version"`
	OK                   *bool                   `json:"ok"`
	RequestID            string                  `json:"request_id,omitempty"`
	DatabaseID           string                  `json:"database_id"`
	Operation            string                  `json:"operation"`
	Result               json.RawMessage         `json:"result,omitempty"`
	Stream               string                  `json:"stream,omitempty"`
	RegistrationRevision *gapdbapi.Revision      `json:"registration_revision,omitempty"`
	Event                json.RawMessage         `json:"event,omitempty"`
	Reason               gapdbapi.WatchEndReason `json:"reason,omitempty"`
	Error                json.RawMessage         `json:"error,omitempty"`
}
type retainedResult struct {
	record gapdbapi.Record
	status gapdbapi.StatusResult
}

func (t *retainedTransport) get(ctx context.Context, key string) (gapdbapi.Record, error) {
	if err := (gapdbapi.Mutation{Kind: gapdbapi.MutationPut, Key: key, Condition: gapdbapi.Condition{Kind: gapdbapi.ConditionAny}}).Validate(gapdbapi.DefaultOptions().Limits); err != nil {
		return gapdbapi.Record{}, err
	}
	request := retainedRequest{AdmittedWireVersion, t.requestID(), "get", struct {
		Key string `json:"key"`
	}{key}}
	result, err := t.exchange(ctx, request, key)
	return result.record, err
}
func (t *retainedTransport) status(ctx context.Context) (gapdbapi.StatusResult, error) {
	result, err := t.exchange(ctx, retainedRequest{AdmittedWireVersion, t.requestID(), "status", struct{}{}}, "")
	return result.status, err
}
func (t *retainedTransport) requestID() string {
	return fmt.Sprintf("owned-read-%d", t.requests.Add(1))
}
func (t *retainedTransport) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true // bars new dial reservations; no Add after Wait begins
		if t.dialStop != nil { close(t.dialStop) }
		t.mu.Unlock()
		// A dial already reserved by exchange must either register before closed
		// or close its actual rejected connection before Done. No mutex held.
		t.dials.Wait()
		for {
			t.mu.Lock()
			node := t.active
			if node == nil { t.mu.Unlock(); break }
			t.unlinkConnection(node) // preserve existing nil/link semantics
			t.mu.Unlock()
			_ = t.closeConnection(node.owner) // Once callback never needs t.mu
		}
		t.mu.Lock()
		t.closeErr = t.connectionCloseErr
		t.mu.Unlock()
	})
	return t.closeErr // FIRST errors sticky; lifetime remains lifecycle-owned
}
func (t *retainedTransport) exchange(ctx context.Context, request retainedRequest, key string) (result retainedResult, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	failed := func(err error) error {
		return &gapdbapi.TransportError{Operation: request.Operation, SocketPath: t.endpoint, Ambiguous: true, Cause: err}
	}
	// Observation attempts are reserved BEFORE endpoint/hash checks, even when
	// the endpoint is refused. Children of private Get inherit this exact key.
	if budget := retainedBudget(ctx); budget != nil {
		if err := budget.attempt(t); err != nil {
			return retainedResult{}, failed(err)
		}
	}
	if err := t.lifetime.CheckEndpoint(ctx, t.endpoint); err != nil {
		return retainedResult{}, failed(err)
	}
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return retainedResult{}, withOwnedCustody(failed(errors.New("owned transport closed")), t.Close())
	}
	// Reserve under the SAME mu as Close's terminal publication.
	t.mu.Lock()
	if t.closed { t.mu.Unlock(); return retainedResult{}, withOwnedCustody(failed(errors.New("owned transport closed")), t.Close()) }
	if t.dialStop == nil { t.dialStop = make(chan struct{}) }
	dialStop := t.dialStop
	t.dials.Add(1)
	t.mu.Unlock()
	// Close cancels the EXISTING exchange context, not a new cleanup budget.
	// Both channels describe actual dial-cancel work, NEVER process/reap facts.
	// The local cancellation goroutine is OWNED/UNQUALIFIED and joined before
	// this reservation completes; no callback/canceller is stored on transport.
	dialDone, dialCancelDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(dialCancelDone)
		select { case <-dialStop: cancel(); case <-dialDone: }
	}()
	raw, err := (&net.Dialer{}).DialContext(ctx, "unix", t.endpoint)
	close(dialDone)
	<-dialCancelDone
	if err != nil {
		t.dials.Done()
		return retainedResult{}, failed(err)
	}
	conn := raw.(*net.UnixConn)
	node, registrationCloseErr := t.registerConnectionResult(conn)
	t.dials.Done() // rejected actual close/result publication already complete
	if node == nil {
		return retainedResult{}, withOwnedCustody(failed(errors.New("owned transport closed")), withOwnedCustody(registrationCloseErr, t.Close()))
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = t.closeConnection(node.owner) })
	defer func() {
		// This check occurs even on framing, decode, remote error or cancellation.
		// On failed attribution no decoded data or provider diagnostics can escape.
		if err := t.lifetime.VerifyConnection(ctx, conn); err != nil {
			result = retainedResult{}
			resultErr = failed(err)
		}
		if err := ctx.Err(); err != nil {
			result = retainedResult{}
			resultErr = failed(err)
		}
		stopCancel()
		// Join the SAME actual close, including a callback already running.
		closeErr := t.closeConnection(node.owner)
		t.mu.Lock()
		closed := t.closed
		if closed {
			result = retainedResult{}
			resultErr = failed(errors.New("owned transport closed"))
		}
		t.unlinkConnection(node) // owner immutable even though node.conn becomes nil
		t.mu.Unlock()
		if closed { resultErr = withOwnedCustody(resultErr, t.Close()) }
		if closeErr != nil { result, resultErr = ownedCloseResult(result, resultErr, failed(closeErr)) }
	}()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return retainedResult{}, failed(err)
		}
	}
	if err := t.lifetime.VerifyConnection(ctx, conn); err != nil {
		return retainedResult{}, failed(err)
	}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) > MaxWireFrameBytes {
		return retainedResult{}, failed(errors.Join(err, errors.New("owned request exceeds frame bound")))
	}
	if err := retainedWriteFrame(conn, payload); err != nil {
		return retainedResult{}, failed(err)
	}
	if err := conn.CloseWrite(); err != nil {
		return retainedResult{}, failed(err)
	}
	payload, err = retainedReadFrameContext(ctx, conn)
	if err != nil {
		return retainedResult{}, failed(err)
	}
	if err := retainedRequireUnaryEOF(conn); err != nil {
		return retainedResult{}, failed(err)
	}
	result, err = t.decodeContext(ctx, payload, request, key)
	if err != nil {
		var remote *gapdbapi.Error
		if errors.As(err, &remote) {
			return retainedResult{}, err
		}
		return retainedResult{}, failed(err)
	}
	return result, nil
}

// This blocking one-byte read uses the exchange's existing deadline and cancel
// owner. EOF establishes only this response stream's end, never peer finality.
func retainedRequireUnaryEOF(reader io.Reader) error {
	var extra [1]byte
	n, err := reader.Read(extra[:])
	if n != 0 {
		return errors.Join(errors.New("extra byte after owned unary response"), err)
	}
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return io.ErrNoProgress
}

func retainedWriteFrame(w io.Writer, payload []byte) error {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(payload)))
	for _, part := range [][]byte{prefix[:], payload} {
		for len(part) != 0 {
			n, err := w.Write(part)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}
func retainedReadFrame(r io.Reader) ([]byte, error) {
	return retainedReadFrameContext(context.Background(), r)
}

func retainedReadFrameContext(ctx context.Context, r io.Reader) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	// Reserve untrusted payload length immediately after the header, BEFORE
	// allocation, including malformed oversized replies. Never refund errors.
	if budget := retainedBudget(ctx); budget != nil {
		if err := budget.frame(uint64(n)); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == 0 || n > MaxWireFrameBytes {
		return nil, errors.New("owned response exceeds frame bound")
	}
	payload := make([]byte, int(n))
	_, err := io.ReadFull(r, payload)
	return payload, err
}

// These private decoders mirror the pinned SDK client_codec.go/client.go rules
// for status/get/errors, with additional immutable owner/database/key checks.
// uint64 revisions never pass through float64. Generic Get preserves optional
// TTL, including null; no-expiry is a later proof MATCHER requirement.
func (t *retainedTransport) decode(payload []byte, request retainedRequest, key string) (retainedResult, error) {
	return t.decodeContext(context.Background(), payload, request, key)
}

func (t *retainedTransport) decodeContext(ctx context.Context, payload []byte, request retainedRequest, key string) (retainedResult, error) {
	node, err := retainedParse(ctx, payload)
	if err != nil {
		return retainedResult{}, err
	}
	var envelope retainedEnvelope
	// Validate exact names and non-null types BEFORE encoding/json's permissive
	// case-insensitive scalar binding. Large result bytes stay borrowed, not copied.
	if err := retainedEnvelopeFields(ctx, node, &envelope); err != nil {
		return retainedResult{}, err
	}
	if envelope.SchemaVersion == nil || *envelope.SchemaVersion != AdmittedWireVersion || envelope.OK == nil || envelope.DatabaseID == "" || envelope.DatabaseID != t.database || !retainedOperation(envelope.Operation) || len(envelope.RequestID) > 256 {
		return retainedResult{}, errors.New("owned envelope differs")
	}
	has := func(name string) bool { _, ok := node.fields[name]; return ok }
	if *envelope.OK == has("error") {
		return retainedResult{}, errors.New("owned ok/error presence differs")
	}
	for _, name := range []string{"stream", "registration_revision", "event", "reason"} {
		if has(name) {
			return retainedResult{}, errors.New("watch field in unary response")
		}
	}
	if !*envelope.OK {
		if has("result") {
			return retainedResult{}, errors.New("failed result must be omitted")
		}
		var remote gapdbapi.Error
		if err := retainedTyped(ctx, node.fields["error"], &remote); err != nil {
			return retainedResult{}, err
		}
		if err := retainedRemoteError(&remote, envelope.Error); err != nil {
			return retainedResult{}, err
		}
		// Exact SDK pre-admission server_busy exception: operation/request ID may
		// differ, but its known operation, strict evidence and bound db still apply.
		if remote.Code != gapdbapi.CodeServerBusy && (envelope.Operation != request.Operation || envelope.RequestID != request.RequestID) {
			return retainedResult{}, errors.New("owned error echo differs")
		}
		if err := ctx.Err(); err != nil {
			return retainedResult{}, err
		}
		return retainedResult{}, remote.Clone()
	}
	if !has("result") || envelope.Operation != request.Operation || envelope.RequestID != request.RequestID {
		return retainedResult{}, errors.New("owned result echo differs")
	}
	switch request.Operation {
	case "get":
		record, err := retainedGetRecord(ctx, node.fields["result"])
		if err != nil {
			return retainedResult{}, err
		}
		limits := gapdbapi.DefaultOptions().Limits
		limits.MaxKeyBytes, limits.MaxValueBytes = gapdbapi.HardMaxKeyBytes, gapdbapi.HardMaxValueBytes
		if err := gapdbapi.NewPutMutation(record.Key, record.Value, gapdbapi.Condition{Kind: gapdbapi.ConditionAny}, nil).Validate(limits); err != nil {
			return retainedResult{}, err
		}
		if record.Key != key || record.Revision == 0 {
			return retainedResult{}, errors.New("owned record key/revision differs")
		}
		if err := ctx.Err(); err != nil {
			return retainedResult{}, err
		}
		return retainedResult{record: record}, nil
	case "status":
		var status gapdbapi.StatusResult
		if err := retainedTyped(ctx, node.fields["result"], &status); err != nil {
			return retainedResult{}, err
		}
		if !retainedLifecycle(status.Lifecycle) || status.DatabaseID != t.database || status.OwnerPID != t.ownerPID || status.OwnerStartedAt != t.ownerStarted || (gapdbapi.Options{Limits: status.Limits}).Validate() != nil {
			return retainedResult{}, errors.New("owned status differs")
		}
		var instant retainedUTC
		encoded, _ := json.Marshal(status.OwnerStartedAt)
		if err := instant.UnmarshalJSON(encoded); err != nil {
			return retainedResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return retainedResult{}, err
		}
		return retainedResult{status: status}, nil
	default:
		return retainedResult{}, errors.New("not a fixed owned read")
	}
}
func retainedFields(raw []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	err := json.Unmarshal(raw, &fields)
	return fields, err
}
func retainedMeaningfulPresence(raw []byte) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

type retainedUTC time.Time

func (t *retainedUTC) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || !strings.HasSuffix(text, "Z") || parsed.UTC().Format(time.RFC3339Nano) != text {
		return errors.New("noncanonical UTC timestamp")
	}
	*t = retainedUTC(parsed.UTC())
	return nil
}

// Raw slices borrow the bounded frame. No JSON map/string materialization of
// the multi-MiB value occurs. Budgets are charged before every map/node growth.
const retainedMaxNodes = 1024
const retainedMaxFields = 64
const retainedMaxDepth = 16
const retainedSmallJSON = 64 << 10
const retainedDecodeBatch = 4096

var errRetainedJSON = errors.New("invalid or excessive owned JSON")

type retainedNode struct {
	raw    []byte
	kind   byte
	fields map[string]*retainedNode
	items  []*retainedNode
}
type retainedScanner struct {
	ctx                 context.Context
	raw                 []byte
	pos, nodes, checked int
}

func (s *retainedScanner) checkpoint() error {
	if s.pos >= s.checked {
		s.checked = s.pos + retainedDecodeBatch
		return s.ctx.Err()
	}
	return nil
}
func (s *retainedScanner) space() error {
	for s.pos < len(s.raw) && (s.raw[s.pos] == ' ' || s.raw[s.pos] == '\n' || s.raw[s.pos] == '\r' || s.raw[s.pos] == '\t') {
		if err := s.checkpoint(); err != nil {
			return err
		}
		s.pos++
	}
	return s.ctx.Err()
}
func (s *retainedScanner) text(maximum int) error {
	start := s.pos
	s.pos++ // opening quote
	for s.pos < len(s.raw) {
		if err := s.checkpoint(); err != nil {
			return err
		}
		if s.pos-start > maximum {
			return errRetainedJSON
		}
		c := s.raw[s.pos]
		s.pos++
		if c == '"' {
			return nil
		}
		if c < 0x20 {
			return errRetainedJSON
		}
		if c == '\\' {
			if s.pos == len(s.raw) {
				return errRetainedJSON
			}
			c = s.raw[s.pos]
			s.pos++
			if c == 'u' {
				if len(s.raw)-s.pos < 4 {
					return errRetainedJSON
				}
				for _, h := range s.raw[s.pos : s.pos+4] {
					if !((h >= '0' && h <= '9') || (h >= 'a' && h <= 'f') || (h >= 'A' && h <= 'F')) {
						return errRetainedJSON
					}
				}
				s.pos += 4
			} else if !strings.ContainsRune("\"\\/bfnrt", rune(c)) {
				return errRetainedJSON
			}
		} else if c >= utf8.RuneSelf {
			r, n := utf8.DecodeRune(s.raw[s.pos-1:])
			if r == utf8.RuneError && n == 1 {
				return errRetainedJSON
			}
			s.pos += n - 1
		}
	}
	return errRetainedJSON
}
func (s *retainedScanner) value(depth int, largeString bool) (*retainedNode, error) {
	if err := s.space(); err != nil {
		return nil, err
	}
	if s.pos == len(s.raw) || depth > retainedMaxDepth || s.nodes >= retainedMaxNodes {
		return nil, errRetainedJSON
	}
	s.nodes++
	start := s.pos
	n := &retainedNode{kind: s.raw[s.pos]}
	switch n.kind {
	case '{', '[':
		s.pos++
		if err := s.space(); err != nil {
			return nil, err
		}
		end := byte('}')
		if n.kind == '[' {
			end = ']'
		}
		if s.pos == len(s.raw) {
			return nil, errRetainedJSON
		}
		if s.raw[s.pos] != end {
			// Allocation is after depth/node checks; collections have fixed caps.
			if n.kind == '{' {
				n.fields = make(map[string]*retainedNode)
			}
			for {
				if err := s.space(); err != nil {
					return nil, err
				}
				name := ""
				if n.kind == '{' {
					if len(n.fields) >= retainedMaxFields || s.pos == len(s.raw) || s.raw[s.pos] != '"' {
						return nil, errRetainedJSON
					}
					nameStart := s.pos
					if err := s.text(128); err != nil {
						return nil, err
					}
					if err := json.Unmarshal(s.raw[nameStart:s.pos], &name); err != nil {
						return nil, errRetainedJSON
					}
					if _, duplicate := n.fields[name]; duplicate {
						return nil, errRetainedJSON
					}
					if err := s.space(); err != nil {
						return nil, err
					}
					if s.pos == len(s.raw) || s.raw[s.pos] != ':' {
						return nil, errRetainedJSON
					}
					s.pos++
				} else if len(n.items) >= 128 {
					return nil, errRetainedJSON
				}
				child, err := s.value(depth+1, name == "value_base64")
				if err != nil {
					return nil, err
				}
				if n.kind == '{' {
					n.fields[name] = child
				} else {
					n.items = append(n.items, child)
				}
				if err := s.space(); err != nil {
					return nil, err
				}
				if s.pos == len(s.raw) {
					return nil, errRetainedJSON
				}
				if s.raw[s.pos] == end {
					break
				}
				if s.raw[s.pos] != ',' {
					return nil, errRetainedJSON
				}
				s.pos++
			}
		}
		s.pos++
	case '"':
		maximum := retainedSmallJSON
		if largeString {
			maximum = MaxWireFrameBytes
		}
		if err := s.text(maximum); err != nil {
			return nil, err
		}
	default:
		for s.pos < len(s.raw) && !strings.ContainsRune(" \n\r\t,}]", rune(s.raw[s.pos])) {
			s.pos++
			if s.pos-start > 32 {
				return nil, errRetainedJSON
			}
		}
		if !json.Valid(s.raw[start:s.pos]) {
			return nil, errRetainedJSON
		}
	}
	n.raw = s.raw[start:s.pos]
	return n, s.ctx.Err()
}
func retainedParse(ctx context.Context, raw []byte) (*retainedNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > MaxWireFrameBytes {
		return nil, errRetainedJSON
	}
	s := retainedScanner{ctx: ctx, raw: raw}
	n, err := s.value(0, false)
	if err != nil {
		return nil, err
	}
	if err := s.space(); err != nil {
		return nil, err
	}
	if s.pos != len(raw) {
		return nil, errRetainedJSON
	}
	return n, nil
}

// Only private fixed schema types reach this validator. Reflection reads their
// JSON tags, NOT case-folded Go names; every present value must be non-null.
// Optional evidence may be absent, but null cannot fabricate zero/false evidence.
func retainedShape(n *retainedNode, typ reflect.Type) error {
	if n == nil || n.kind == 'n' {
		return errRetainedJSON
	}
	if typ.Kind() == reflect.Pointer {
		return retainedShape(n, typ.Elem())
	}
	switch typ.Kind() {
	case reflect.Struct:
		if n.kind != '{' {
			return errRetainedJSON
		}
		allowed := make(map[string]reflect.Type, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			allowed[tag[0]] = field.Type
			if len(tag) == 1 && n.fields[tag[0]] == nil {
				return errRetainedJSON
			}
		}
		for name, child := range n.fields {
			field, ok := allowed[name]
			if !ok {
				return errRetainedJSON
			}
			if err := retainedShape(child, field); err != nil {
				return err
			}
		}
	case reflect.String:
		if n.kind != '"' || len(n.raw) > retainedSmallJSON {
			return errRetainedJSON
		}
	case reflect.Bool:
		if string(n.raw) != "true" && string(n.raw) != "false" {
			return errRetainedJSON
		}
	case reflect.Uint64, reflect.Uint32, reflect.Uint16, reflect.Uint8, reflect.Uint:
		if _, err := strconv.ParseUint(string(n.raw), 10, typ.Bits()); err != nil {
			return errRetainedJSON
		}
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8:
		if _, err := strconv.ParseInt(string(n.raw), 10, typ.Bits()); err != nil {
			return errRetainedJSON
		}
	case reflect.Slice:
		if n.kind != '[' {
			return errRetainedJSON
		}
		for _, child := range n.items {
			if err := retainedShape(child, typ.Elem()); err != nil {
				return err
			}
		}
	default:
		return errRetainedJSON
	}
	return nil
}
func retainedTyped(ctx context.Context, n *retainedNode, destination any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := retainedShape(n, reflect.TypeOf(destination).Elem()); err != nil {
		return err
	}
	// Small status/error objects only. Standard materialization remains finite,
	// not instantly interruptible; bracket it and never use it for large values.
	if len(n.raw) > retainedSmallJSON {
		return errRetainedJSON
	}
	if err := json.Unmarshal(n.raw, destination); err != nil {
		return errRetainedJSON
	}
	return ctx.Err()
}
func retainedEnvelopeFields(ctx context.Context, n *retainedNode, e *retainedEnvelope) error {
	if n.kind != '{' {
		return errRetainedJSON
	}
	for _, name := range []string{"schema_version", "ok", "database_id", "operation"} {
		if n.fields[name] == nil {
			return errRetainedJSON
		}
	}
	for name, child := range n.fields {
		var destination any
		switch name {
		case "schema_version":
			destination = &e.SchemaVersion
		case "ok":
			destination = &e.OK
		case "database_id":
			destination = &e.DatabaseID
		case "operation":
			destination = &e.Operation
		case "request_id":
			destination = &e.RequestID
		case "result", "error":
			if child.kind != '{' {
				return errRetainedJSON
			}
			if name == "result" {
				e.Result = child.raw
			} else {
				e.Error = child.raw
			}
			continue
		default:
			return errRetainedJSON // aliases, unknown or watch-only fields
		}
		if err := retainedTyped(ctx, child, destination); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func retainedGetRecord(ctx context.Context, result *retainedNode) (gapdbapi.Record, error) {
	if result == nil || result.kind != '{' || len(result.fields) != 1 {
		return gapdbapi.Record{}, errRetainedJSON
	}
	n := result.fields["record"]
	if n == nil || n.kind != '{' {
		return gapdbapi.Record{}, errRetainedJSON
	}
	for _, name := range []string{"key", "value_base64", "revision"} {
		if n.fields[name] == nil {
			return gapdbapi.Record{}, errRetainedJSON
		}
	}
	// All known names/types are checked before decoding any credited field.
	for name, child := range n.fields {
		var typ reflect.Type
		switch name {
		case "key":
			typ = reflect.TypeOf("")
		case "revision":
			typ = reflect.TypeOf(gapdbapi.Revision(0))
		case "value_base64":
			if child.kind != '"' {
				return gapdbapi.Record{}, errRetainedJSON
			}
			continue
		case "expires_at":
			if string(child.raw) == "null" {
				continue
			}
			typ = reflect.TypeOf("")
		default:
			return gapdbapi.Record{}, errRetainedJSON
		}
		if err := retainedShape(child, typ); err != nil {
			return gapdbapi.Record{}, err
		}
	}
	var record gapdbapi.Record
	for name, child := range n.fields {
		if err := ctx.Err(); err != nil {
			return gapdbapi.Record{}, err
		}
		var err error
		switch name {
		case "key":
			err = retainedTyped(ctx, child, &record.Key)
		case "revision":
			err = retainedTyped(ctx, child, &record.Revision)
		case "value_base64":
			record.Value, err = retainedDecodeBase64(ctx, child)
		case "expires_at":
			if string(child.raw) == "null" {
				continue
			} // only nullable proof field
			if err = retainedShape(child, reflect.TypeOf("")); err == nil {
				var instant retainedUTC
				err = instant.UnmarshalJSON(child.raw)
				if err == nil {
					value := time.Time(instant)
					record.ExpiresAt = &value
				}
			}
		default:
			err = errRetainedJSON
		}
		if err != nil {
			return gapdbapi.Record{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return gapdbapi.Record{}, err
	}
	return record, nil
}

// Consume a JSON base64 string in bounded, canonical 4-character groups. JSON
// escapes remain compatible, but only decoded ASCII base64 characters can pass.
// There is no unchecked giant DecodeString, EncodeToString or text allocation.
func retainedDecodeBase64(ctx context.Context, n *retainedNode) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n == nil || n.kind != '"' {
		return nil, errRetainedJSON
	}
	raw := n.raw[1 : len(n.raw)-1]
	count := 0
	consume := func(emit func(byte) error) error {
		nextCheck := 0
		for i := 0; i < len(raw); {
			if i >= nextCheck {
				if err := ctx.Err(); err != nil {
					return err
				}
				nextCheck = i + retainedDecodeBatch
			}
			c := raw[i]
			i++
			if c == '\\' {
				if i == len(raw) {
					return errRetainedJSON
				}
				c = raw[i]
				i++
				if c == 'u' {
					if len(raw)-i < 4 {
						return errRetainedJSON
					}
					value, err := strconv.ParseUint(string(raw[i:i+4]), 16, 16)
					if err != nil || value > 127 {
						return errRetainedJSON
					}
					c = byte(value)
					i += 4
				} else if c != '/' {
					return errRetainedJSON
				}
			}
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
				return errRetainedJSON
			}
			if err := emit(c); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	if err := consume(func(byte) error { count++; return nil }); err != nil {
		return nil, err
	}
	if count%4 != 0 || base64.StdEncoding.DecodedLen(count) > gapdbapi.HardMaxValueBytes {
		return nil, errRetainedJSON
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(count))
	var chunk [retainedDecodeBatch]byte
	used, written, processed := 0, 0, 0
	flush := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		part := chunk[:used]
		if processed+used != count && bytes.IndexByte(part, '=') >= 0 {
			return errRetainedJSON
		}
		n, err := base64.StdEncoding.Strict().Decode(decoded[written:], part)
		if err != nil || base64.StdEncoding.EncodeToString(decoded[written:written+n]) != string(part) {
			return errRetainedJSON
		}
		written += n
		processed += used
		used = 0
		return ctx.Err()
	}
	if err := consume(func(c byte) error {
		chunk[used] = c
		used++
		if used == len(chunk) {
			return flush()
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if used != 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return decoded[:written], ctx.Err()
}
func retainedLifecycle(value gapdbapi.LifecycleState) bool {
	switch value {
	case gapdbapi.LifecycleReady, gapdbapi.LifecycleDegradedReadOnly, gapdbapi.LifecycleDraining, gapdbapi.LifecycleInspectionOnly:
		return true
	}
	return false
}
func retainedOperation(value string) bool {
	// Pinned SDK recognizes these operation names even on pre-admission busy.
	switch value {
	case "get", "get_many", "read_recovery_snapshot", "put", "put_if_absent", "compare_and_swap", "delete_if_revision", "atomic_batch", "scan_prefix", "watch", "status", "health", "stats", "describe_config", "verify", "create_snapshot", "compact", "backup", "offline_inspect", "offline_verify", "offline_recover_propose", "offline_recover_apply":
		return true
	}
	return false
}

// Evidence grammar mirrors the pinned SDK: space-separated required fields;
// comma-separated alternatives inside a group; optional fields follow '|'.
// A false/zero evidence scalar is meaningful (only null/empty text/list isn't).
var retainedErrorSchemas = map[gapdbapi.ErrorCode]string{
	gapdbapi.CodeInvalidRequest:          "reason field,path",
	gapdbapi.CodeUnsupportedVersion:      "received_version supported_versions",
	gapdbapi.CodeFrameTooLarge:           "received_bytes maximum_bytes",
	gapdbapi.CodeKeyTooLarge:             "received_bytes maximum_bytes | field",
	gapdbapi.CodeValueTooLarge:           "received_bytes maximum_bytes | field",
	gapdbapi.CodeBatchTooLarge:           "| received_operations maximum_operations received_bytes maximum_bytes",
	gapdbapi.CodeDuplicateKey:            "key mutation_indexes,assertion_index | mutation_index",
	gapdbapi.CodeExpiryNotFuture:         "supplied_expiry effective_time",
	gapdbapi.CodeInvalidCursor:           "reason",
	gapdbapi.CodeNotFound:                "key current_revision",
	gapdbapi.CodeAlreadyExists:           "key actual_revision",
	gapdbapi.CodeRevisionMismatch:        "key expected_revision actual_revision",
	gapdbapi.CodeConditionFailed:         "key condition mutation_index,assertion_index actual_state,actual_revision | expected_revision",
	gapdbapi.CodeScanStale:               "cursor_revision current_revision",
	gapdbapi.CodeRevisionAhead:           "requested_revision current_revision",
	gapdbapi.CodeRevisionCompacted:       "requested_revision earliest_available_revision",
	gapdbapi.CodeWatchLagged:             "last_delivered_revision current_revision",
	gapdbapi.CodeOwnerExists:             "path",
	gapdbapi.CodeServerBusy:              "active_clients maximum_clients queue_depth",
	gapdbapi.CodeServerShuttingDown:      "lifecycle",
	gapdbapi.CodeDeadlineExceeded:        "operation deadline",
	gapdbapi.CodeServerUnavailable:       "socket_path connection_reason",
	gapdbapi.CodePermissionDenied:        "path operation",
	gapdbapi.CodeStorageDegraded:         "failed_stage current_revision durable_through_revision",
	gapdbapi.CodeCorruptIdentity:         "file offset,reason | expected_database_id",
	gapdbapi.CodeCorruptManifest:         "file reason",
	gapdbapi.CodeCorruptSnapshot:         "file offset reason",
	gapdbapi.CodeCorruptWAL:              "file offset reason | actual_revision",
	gapdbapi.CodeUnknownFormat:           "file received_version supported_versions",
	gapdbapi.CodeDatabaseIDMismatch:      "expected_database_id actual_database_id file,operation",
	gapdbapi.CodeRevisionRangeExhausted:  "reserved_revision_end reason",
	gapdbapi.CodeIOError:                 "operation path os_error_category",
	gapdbapi.CodeAdminPreconditionFailed: "| expected_database_id actual_database_id expected_revision actual_revision expected_manifest_generation actual_manifest_generation",
	gapdbapi.CodeSnapshotInProgress:      "operation_id started_at",
	gapdbapi.CodeCompactionNotSafe:       "through_revision active_snapshot_revision",
	gapdbapi.CodeBackupDestinationExists: "destination",
	gapdbapi.CodeBackupInvalid:           "backup_path file,reason",
	gapdbapi.CodeRecoveryRequired:        "detected_corruption proposal_available",
	gapdbapi.CodeRecoveryActionMismatch:  "expected_database_id actual_database_id expected_manifest_generation actual_manifest_generation proposal_id",
	gapdbapi.CodeAuditFailedAfterApply:   "operation reason current_revision operation_applied",
	gapdbapi.CodeInternal:                "correlation_id | lifecycle reason",
}

func retainedRemoteError(remote *gapdbapi.Error, raw []byte) error {
	if remote.Message == "" || remote.AssertionIndex != nil {
		return errors.New("remote error incomplete or assertion outside fixed read")
	}
	classified := false
	for _, definition := range gapdbapi.ErrorDefinitions() {
		if definition.Code != remote.Code {
			continue
		}
		if remote.Retry != definition.Retry || len(remote.SafeActions) != len(definition.SafeActions) {
			return errors.New("remote classification differs")
		}
		for i := range definition.SafeActions {
			if remote.SafeActions[i] != definition.SafeActions[i] {
				return errors.New("remote actions differ")
			}
		}
		classified = true
		break
	}
	if !classified {
		return errors.New("unknown remote error")
	}
	schema, ok := retainedErrorSchemas[remote.Code]
	if !ok {
		return errors.New("remote schema absent")
	}
	fields, err := retainedFields(raw)
	if err != nil {
		return err
	}
	allowed := map[string]bool{"code": true, "message": true, "retry": true, "safe_actions": true, "operation_applied": true}
	parts := strings.SplitN(schema, "|", 2)
	for _, group := range strings.Fields(parts[0]) {
		found := false
		for _, name := range strings.Split(group, ",") {
			allowed[name] = true
			found = found || retainedEvidence(fields[name])
		}
		if !found {
			return errors.New("remote evidence missing")
		}
	}
	if len(parts) == 2 {
		for _, name := range strings.Fields(parts[1]) {
			allowed[name] = true
		}
	}
	for name, encoded := range fields {
		if !allowed[name] {
			return errors.New("remote evidence forbidden")
		}
		if name != "code" && name != "message" && name != "retry" && name != "safe_actions" && !retainedEvidence(encoded) {
			return errors.New("remote evidence empty")
		}
	}
	if encoded, present := fields["operation_applied"]; present {
		var applied bool
		if json.Unmarshal(encoded, &applied) != nil || !applied {
			return errors.New("operation_applied must be true")
		}
	}
	pair := func(left, right string) bool {
		return retainedEvidence(fields[left]) && retainedEvidence(fields[right])
	}
	if remote.Code == gapdbapi.CodeBatchTooLarge && !pair("received_operations", "maximum_operations") && !pair("received_bytes", "maximum_bytes") {
		return errors.New("batch evidence pair missing")
	}
	if remote.Code == gapdbapi.CodeAdminPreconditionFailed && !pair("expected_database_id", "actual_database_id") && !pair("expected_revision", "actual_revision") && !pair("expected_manifest_generation", "actual_manifest_generation") {
		return errors.New("admin evidence pair missing")
	}
	if remote.Code == gapdbapi.CodeConditionFailed {
		_, m := fields["mutation_index"]
		_, a := fields["assertion_index"]
		if m == a {
			return errors.New("condition evidence index invalid")
		}
	}
	return nil
}
func retainedEvidence(raw []byte) bool {
	if !retainedMeaningfulPresence(raw) {
		return false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch value := value.(type) {
	case string:
		return value != ""
	case []any:
		return len(value) != 0
	default:
		return true
	}
}
