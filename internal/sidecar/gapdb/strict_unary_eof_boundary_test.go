//go:build linux && amd64

package gapdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// These are simulated self-peer connections, not genuine Host/provider proof.
// FIRST actual closes are cached; roots are removed only when known and empty.
type strictEOFFirstClose struct {
	once sync.Once
	close func() error
	err error
	calls int
}
func (c *strictEOFFirstClose) Close() error {
	c.once.Do(func() { c.calls++; c.err = c.close() })
	return c.err
}
func strictEOFCheckClose(t *testing.T, c *strictEOFFirstClose) {
	t.Helper()
	if c != nil {
		if err := c.Close(); err != nil { t.Errorf("FIRST fixture close failed (cached): %v", err) }
	}
}

type strictEOFFixture struct {
	path string
	socket fileIdentity
	process ProcessIdentity
}

func strictEOFListen(t *testing.T, handler func(*net.UnixConn, wireRequest) error) strictEOFFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "strict-eof-")
	if err != nil { t.Fatal(err) }
	var directory fileIdentity
	var listener *net.UnixListener
	var listenerClose *strictEOFFirstClose
	var socket fileIdentity
	knownDirectory, knownSocket, started := false, false, false
	done := make(chan error, 1)
	path := filepath.Join(root, "s")
	t.Cleanup(func() {
		strictEOFCheckClose(t, listenerClose)
		if started {
			if err := <-done; err != nil { t.Error("fixture server outcome", err) }
		}
		info, err := os.Lstat(root)
		if err != nil || !knownDirectory { t.Errorf("preserving unknown root %s: %v", root, err); return }
		actual, err := identityFromInfo(info)
		if err != nil || actual.device != directory.device || actual.inode != directory.inode || !info.IsDir() {
			t.Errorf("preserving changed root %s: %v", root, err); return
		}
		if listener != nil {
			if !knownSocket { t.Errorf("preserving unknown socket %s", path); return }
			if err := verifySocketPathIdentity(path, socket); err != nil { t.Errorf("preserving foreign socket %s: %v", path, err); return }
			if err := os.Remove(path); err != nil { t.Error(err); return }
		}
		if err := os.Remove(root); err != nil { t.Errorf("preserving residual/nonempty root: %v", err) }
	})
	info, err := os.Lstat(root)
	if err != nil { t.Fatal(err) }
	directory, err = identityFromInfo(info)
	if err != nil || !info.IsDir() { t.Fatal("directory identity", err) }
	knownDirectory = true
	listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil { t.Fatal(err) }
	listener.SetUnlinkOnClose(false)
	listenerClose = &strictEOFFirstClose{close: listener.Close}
	socket, err = socketPathIdentity(path)
	if err != nil { t.Fatal(err) }
	knownSocket = true
	process := currentProcessIdentity(t)
	if err := listener.SetDeadline(time.Now().Add(3*time.Second)); err != nil { t.Fatal(err) }
	started = true
	go func() {
		var outcome error
		defer func() { done <- outcome }()
		conn, err := listener.AcceptUnix()
		if err != nil { outcome = err; return }
		first := &strictEOFFirstClose{close: conn.Close}
		defer func() { outcome = errors.Join(outcome, first.Close()) }()
		if err := conn.SetDeadline(time.Now().Add(2*time.Second)); err != nil { outcome = err; return }
		peer, err := peerProcessOfConnection(conn)
		if err != nil || peer != os.Getpid() { outcome = errors.Join(err, errors.New("actual fixture peer differs")); return }
		raw, err := readWireFrame(conn, 4096)
		if err != nil { outcome = err; return }
		var request wireRequest
		if err := json.Unmarshal(raw, &request); err != nil { outcome = err; return }
		if request.SchemaVersion != 1 || request.RequestID != "sidecar-observe-health" || request.Operation != "health" || string(request.Arguments) != "{}" {
			outcome = errors.New("actual strict request differs"); return
		}
		var extra [1]byte
		if n, err := conn.Read(extra[:]); n != 0 || err != io.EOF {
			outcome = errors.Join(err, errors.New("request did not end after one frame")); return
		}
		outcome = handler(conn, request)
	}()
	return strictEOFFixture{path, socket, process}
}

func strictEOFWait(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(3*time.Second):
		t.Fatal("fixture synchronization missing (watchdog, not an EOF witness)")
	}
}

func strictEOFReply(request wireRequest) ([]byte, error) {
	return json.Marshal(wireResponse{SchemaVersion: 1, OK: true, RequestID: request.RequestID, DatabaseID: "strict-eof-fixture", Operation: request.Operation, Result: json.RawMessage(`{"lifecycle":"ready","healthy":true,"failing_subsystems":[],"safe_actions":[]}`)})
}

func (f strictEOFFixture) observe(ctx context.Context, destination any, timeout time.Duration, report *expiryCleanupReport) (string, error) {
	return observeStrictUnaryWithReport(ctx, systemProcessAPI, f.path, f.socket, f.process, "health", destination, timeout, report)
}

func TestStrictUnaryEOFPositiveRequestEOFThenSingleResponse(t *testing.T) {
	f := strictEOFListen(t, func(conn *net.UnixConn, request wireRequest) error {
		response, err := strictEOFReply(request)
		if err != nil { return err }
		if err := writeWireFrame(conn, response); err != nil { return err }
		first := &strictEOFFirstClose{close: conn.CloseWrite}
		if err := first.Close(); err != nil { return err }
		if err := first.Close(); err != nil || first.calls != 1 { return errors.New("write half FIRST outcome not cached") }
		return nil
	})
	report := new(expiryCleanupReport)
	var got map[string]any
	database, err := f.observe(t.Context(), &got, time.Second, report)
	if err != nil || database != "strict-eof-fixture" || got["healthy"] != true { t.Fatal("single frame plus actual EOF refused", database, got, err) }
	if err := report.cleanupError(); err != nil { t.Fatal("FIRST local cleanup", err) }
}

func TestStrictUnaryEOFRejectsExtrasBeforeDestinationMutation(t *testing.T) {
	for _, mode := range []string{"two-frames", "partial-extra", "EOF-only", "malformed-length", "partial-frame", "malformed-JSON"} {
		t.Run(mode, func(t *testing.T) {
			f := strictEOFListen(t, func(conn *net.UnixConn, request wireRequest) error {
				response, err := strictEOFReply(request)
				if err != nil { return err }
				var wire bytes.Buffer
				switch mode {
				case "two-frames":
					if err := writeWireFrame(&wire, response); err != nil { return err }
					if err := writeWireFrame(&wire, response); err != nil { return err }
				case "partial-extra":
					if err := writeWireFrame(&wire, response); err != nil { return err }
					wire.WriteByte(0)
				case "malformed-length": wire.Write([]byte{0, 0, 0, 0})
				case "partial-frame": wire.Write([]byte{0, 0, 0, 8, '{'})
				case "malformed-JSON":
					if err := writeWireFrame(&wire, []byte(`{`)); err != nil { return err }
				}
				if wire.Len() != 0 {
					n, err := conn.Write(wire.Bytes())
					if err != nil { return err }
					if n != wire.Len() { return io.ErrShortWrite }
				}
				return nil // FIRST full close sends real EOF
			})
			got := map[string]any{"sentinel": "unchanged"}
			report := new(expiryCleanupReport)
			database, err := f.observe(t.Context(), &got, time.Second, report)
			if database != "" || !IsFailure(err, FailureProtocol) || !reflect.DeepEqual(got, map[string]any{"sentinel": "unchanged"}) {
				t.Fatal("stream failure mutated destination/admitted evidence", database, got, err)
			}
			if err := report.cleanupError(); err != nil { t.Fatal("FIRST cleanup", err) }
		})
	}
}

func TestStrictUnaryEOFDelayedByteRequiresStreamEnd(t *testing.T) {
	firstWritten, release, extraWritten := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseWriter()
	f := strictEOFListen(t, func(conn *net.UnixConn, request wireRequest) error {
		response, err := strictEOFReply(request)
		if err != nil { return err }
		if err := writeWireFrame(conn, response); err != nil { return err }
		close(firstWritten)
		<-release
		n, err := conn.Write([]byte{0})
		if err == nil && n != 1 { err = io.ErrShortWrite }
		extraWritten <- err
		return err
	})
	done := make(chan error, 1)
	go func() {
		got := map[string]any{"sentinel": "unchanged"}
		database, err := f.observe(t.Context(), &got, time.Second, nil)
		if database != "" || !reflect.DeepEqual(got, map[string]any{"sentinel": "unchanged"}) { t.Error("result mutated before stream end", database, got) }
		done <- err
	}()
	strictEOFWait(t, firstWritten)
	select {
	case err := <-done:
		releaseWriter()
		t.Fatal("returned before stream end", err)
	default:
	}
	releaseWriter()
	if err := <-extraWritten; err != nil { t.Fatal("delayed byte not actually written", err) }
	if err := <-done; !IsFailure(err, FailureProtocol) { t.Fatal("delayed byte accepted", err) }
}

func TestStrictUnaryEOFMissingEndBoundedByExistingTimeoutAndContext(t *testing.T) {
	for _, mode := range []string{"timeout", "caller-deadline", "caller-cancel"} {
		t.Run(mode, func(t *testing.T) {
			firstWritten, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			f := strictEOFListen(t, func(conn *net.UnixConn, request wireRequest) error {
				response, err := strictEOFReply(request)
				if err != nil { return err }
				if err := writeWireFrame(conn, response); err != nil { return err }
				close(firstWritten)
				<-release
				return nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			if mode == "caller-deadline" { cancel(); ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond) }
			defer cancel()
			done := make(chan error, 1)
			report := new(expiryCleanupReport)
			go func() {
				got := map[string]any{"sentinel": "unchanged"}
				database, err := f.observe(ctx, &got, 200*time.Millisecond, report)
				if database != "" || !reflect.DeepEqual(got, map[string]any{"sentinel": "unchanged"}) { t.Error("deadline/cancel admitted response", database, got) }
				done <- err
			}()
			strictEOFWait(t, firstWritten)
			if mode == "caller-cancel" { cancel() }
			select {
			case err := <-done:
				if !IsFailure(err, FailureProtocol) { t.Fatal("missing EOF did not refuse", err) }
				var timed net.Error
				if mode != "caller-cancel" && !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &timed) && timed.Timeout()) { t.Fatal("original deadline not retained", err) }
			case <-time.After(3*time.Second): t.Fatal("EOF read outlived original timeout/cancellation")
			}
			if err := report.cleanupError(); err != nil { t.Fatal("FIRST cancellation cleanup", err) }
		})
	}
}

type strictEOFReader struct { n int; err error }
func (r strictEOFReader) Read(p []byte) (int, error) { if r.n > 0 { p[0] = 0 }; return r.n, r.err }

func TestStrictUnaryEOFRejectsNoProgressAndPreservesFirstReadError(t *testing.T) {
	sentinel := errors.New("first read sentinel")
	for _, input := range []strictEOFReader{{0, nil}, {0, sentinel}, {1, nil}, {1, io.EOF}, {1, sentinel}} {
		err := strictRequireUnaryEOF(input)
		if err == nil { t.Fatal("non-EOF admitted", input) }
		if input.err == sentinel && !errors.Is(err, sentinel) { t.Fatal("read error lost", err) }
	}
}

func TestStrictUnaryEOFActualClosedUnixFailuresAndFirstClose(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil { t.Fatal(err) }
	files := [2]*os.File{os.NewFile(uintptr(fds[0]), "strict-client"), os.NewFile(uintptr(fds[1]), "strict-peer")}
	for _, file := range files {
		first := &strictEOFFirstClose{close: file.Close}
		t.Cleanup(func() { strictEOFCheckClose(t, first) })
	}
	var conns [2]*net.UnixConn
	var closes [2]*strictEOFFirstClose
	for i, file := range files {
		raw, err := net.FileConn(file)
		if err != nil { t.Fatal(err) }
		first := &strictEOFFirstClose{close: raw.Close}
		t.Cleanup(func() { strictEOFCheckClose(t, first) })
		var ok bool
		conns[i], ok = raw.(*net.UnixConn)
		if !ok { t.Fatal("not an actual UnixConn") }
		closes[i] = first
	}
	strictEOFCheckClose(t, closes[0])
	for name, err := range map[string]error{
		"half-close": conns[0].CloseWrite(),
		"deadline": conns[0].SetDeadline(time.Now()),
		"write": writeWireFrame(conns[0], []byte(`{}`)),
		"EOF read": strictRequireUnaryEOF(conns[0]),
	} {
		if !errors.Is(err, net.ErrClosed) { t.Fatal("actual closed descriptor error lost", name, err) }
	}
	strictEOFCheckClose(t, closes[0])
	if closes[0].calls != 1 { t.Fatal("FIRST actual close retried") }
	firstError := errors.New("FIRST close sentinel")
	cached := &strictEOFFirstClose{close: func() error { return firstError }}
	if cached.Close() != firstError { t.Fatal("FIRST error lost") }
	cached.close = func() error { t.Fatal("later close attempted repair"); return nil }
	if cached.Close() != firstError || cached.calls != 1 { t.Fatal("FIRST close outcome repaired") }
}

type strictEOFReadEntry struct { io.Reader; entered chan struct{} }
func (r strictEOFReadEntry) Read(p []byte) (int, error) {
	close(r.entered)
	return r.Reader.Read(p)
}

// Actual Unix read + actual context callback close, without a socket/context
// timer race. This is a component cause/ownership control, not Host evidence.
func TestStrictUnaryEOFContextCloseRetainsActualReadAndContextCauses(t *testing.T) {
	for _, mode := range []string{"caller-cancel", "caller-deadline"} {
		t.Run(mode, func(t *testing.T) {
			fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
			if err != nil { t.Fatal(err) }
			var conns [2]*net.UnixConn
			var owners [2]*strictEOFFirstClose
			var files [2]*os.File
			var originals [2]*strictEOFFirstClose
			// Socketpair acquired BOTH descriptors: register both owners before
			// either fallible FileConn conversion can abandon the fixture.
			for i, fd := range fds {
				files[i] = os.NewFile(uintptr(fd), "context-close-pair")
				original := &strictEOFFirstClose{close: files[i].Close}
				originals[i] = original
				t.Cleanup(func() { strictEOFCheckClose(t, original) })
			}
			report := new(expiryCleanupReport)
			for i, file := range files {
				raw, err := net.FileConn(file)
				if err != nil { t.Fatal(err) }
				owner := &strictEOFFirstClose{close: raw.Close}
				t.Cleanup(func() { strictEOFCheckClose(t, owner) })
				strictEOFCheckClose(t, originals[i]) // FileConn owns its duplicate.
				var ok bool
				conns[i], ok = raw.(*net.UnixConn)
				if !ok { t.Fatal("actual pair did not yield UnixConn") }
				owners[i] = owner
				if i == 0 {
					report.localOpened()
					owner.close = func() error { return report.closeLocalConnection(raw) }
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			if mode == "caller-deadline" {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now())
			}
			defer cancel()
			closed, entered := make(chan struct{}), make(chan struct{})
			stop := context.AfterFunc(ctx, func() { owners[0].Close(); close(closed) })
			defer stop()
			type outcome struct { primary, retained error }
			done := make(chan outcome, 1)
			go func() {
				primary := strictRequireUnaryEOF(strictEOFReadEntry{conns[0], entered})
				done <- outcome{primary, strictUnaryReadCause(ctx, primary)}
			}()
			strictEOFWait(t, entered)
			if mode == "caller-cancel" { cancel() }
			strictEOFWait(t, closed) // Actual FIRST callback close finished.
			var got outcome
			select {
			case got = <-done:
			case <-time.After(3*time.Second): t.Fatal("closed read did not return")
			}
			if ctx.Err() == nil || !errors.Is(got.primary, net.ErrClosed) ||
				got.retained.Error() != got.primary.Error() || !errors.Is(got.retained, got.primary) ||
				!errors.Is(got.retained, net.ErrClosed) || !errors.Is(got.retained, ctx.Err()) {
				t.Fatal("actual read/context cause lost", got.primary, got.retained, ctx.Err())
			}
			var original, retained *net.OpError
			if !errors.As(got.primary, &original) || !errors.As(got.retained, &retained) || retained != original {
				t.Fatal("primary concrete network error identity lost")
			}
			if strictUnaryReadCause(context.Background(), got.primary) != got.primary {
				t.Fatal("live context fabricated a cancellation cause")
			}
			sentinel := errors.New("independent non-closed read failure")
			if strictUnaryReadCause(ctx, sentinel) != sentinel { t.Fatal("unrelated read failure relabeled") }
			strictEOFCheckClose(t, owners[0])
			if owners[0].calls != 1 { t.Fatal("FIRST actual close retried") }
			if err := report.cleanupError(); err != nil { t.Fatal("actual FIRST close report", err) }
		})
	}
}

// AST-only checks below cannot be satisfied by comments or inert token strings.
func strictEOFExpr(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident: return x.Name
	case *ast.SelectorExpr: return strictEOFExpr(x.X)+"."+x.Sel.Name
	case *ast.ParenExpr: return strictEOFExpr(x.X)
	case *ast.StarExpr: return "*"+strictEOFExpr(x.X)
	}
	return "<non-name>"
}
func strictEOFFunction(t *testing.T, path, name string) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil { t.Fatal(err) }
	var result *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			if result != nil { t.Fatal("duplicate source function", path, name) }
			result = fn
		}
	}
	if result == nil || result.Body == nil { t.Fatal("source function absent", path, name) }
	return result
}
func strictEOFCalls(node ast.Node, name string) []*ast.CallExpr {
	var result []*ast.CallExpr
	ast.Inspect(node, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && strictEOFExpr(c.Fun) == name { result = append(result, c) }
		return true
	})
	return result
}
func strictEOFCall(t *testing.T, node ast.Node, name string, args ...string) *ast.CallExpr {
	t.Helper()
	calls := strictEOFCalls(node, name)
	if len(calls) != 1 { t.Fatalf("actual call %s count=%d", name, len(calls)) }
	call := calls[0]
	if len(call.Args) != len(args) { t.Fatal("call arguments differ", name) }
	for i, arg := range args { if strictEOFExpr(call.Args[i]) != arg { t.Fatal("actual argument differs", name, i) } }
	return call
}
func TestStrictUnaryEOFSourceUsesActualConnectionBeforeDecode(t *testing.T) {
	wrapper := strictEOFFunction(t, "lifecycle.go", "observeStrictUnaryWithReport")
	if len(wrapper.Body.List) != 1 { t.Fatal("production wrapper gained alternate path") }
	ret, ok := wrapper.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 { t.Fatal("fixed wrapper is not direct return") }
	strictEOFCall(t, ret, "observeStrictUnaryWithClose", "ctx", "processes", "socket", "socketID", "process", "operation", "destination", "timeout", "report", "*expiryCleanupReport.closeLocalConnection")
	body := strictEOFFunction(t, "lifecycle.go", "observeStrictUnaryWithClose").Body
	var closeBody *ast.FuncLit
	actualOnce := false
	for _, stmt := range body.List {
		if a, ok := stmt.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && strictEOFExpr(a.Lhs[0]) == "closeConnection" && len(a.Rhs) == 1 { closeBody, _ = a.Rhs[0].(*ast.FuncLit) }
		if d, ok := stmt.(*ast.DeclStmt); ok {
			ast.Inspect(d, func(n ast.Node) bool {
				if v, ok := n.(*ast.ValueSpec); ok && len(v.Names) == 1 && v.Names[0].Name == "closeOnce" && strictEOFExpr(v.Type) == "sync.Once" { actualOnce = true }
				return true
			})
		}
	}
	if !actualOnce || closeBody == nil || len(closeBody.Body.List) != 1 { t.Fatal("actual sync.Once close owner absent") }
	once := strictEOFCalls(closeBody, "closeOnce.Do")
	if len(once) != 1 || len(once[0].Args) != 1 { t.Fatal("close not owned by one Once") }
	attempt, ok := once[0].Args[0].(*ast.FuncLit)
	if !ok || len(attempt.Body.List) != 1 { t.Fatal("extra close work in Once") }
	a, ok := attempt.Body.List[0].(*ast.AssignStmt)
	if !ok || len(a.Lhs) != 1 || strictEOFExpr(a.Lhs[0]) != "closeErr" { t.Fatal("close result not retained") }
	strictEOFCall(t, attempt, "closeCall", "report", "connection")
	if len(strictEOFCalls(body, "closeCall")) != 1 { t.Fatal("actual close outside shared owner") }
	strictEOFCall(t, body, "context.AfterFunc", "ctx", "closeConnection")
	deferredClose, deferredStop := false, false
	ast.Inspect(body, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt); if !ok { return true }
		if strictEOFExpr(d.Call.Fun) == "stopCancel" { deferredStop = true }
		if lit, ok := d.Call.Fun.(*ast.FuncLit); ok {
			if len(strictEOFCalls(lit, "closeConnection")) == 1 && len(strictEOFCalls(lit, "withStartupCustody")) == 1 && len(strictEOFCalls(lit, "startupCleanupError")) == 1 { deferredClose = true }
		}
		return true
	})
	if !deferredClose || !deferredStop { t.Fatal("deferred close custody or cancellation unregistration absent") }
	strictEOFCall(t, body, "ctx.Deadline")
	strictEOFCall(t, body, "connection.SetDeadline", "deadline")
	strictEOFCall(t, body, "verifyConnectedEndpoint", "connection", "peer", "socket", "socketID")
	previous := token.NoPos
	for _, name := range []string{"writeWireFrame", "unixConnection.CloseWrite", "readWireFrame", "strictRequireUnaryEOF", "validateNoDuplicateJSON", "decodeStrictJSON"} {
		calls := strictEOFCalls(body, name)
		if len(calls) == 0 || calls[0].Pos() <= previous { t.Fatal("actual EOF/decode order differs", name) }
		previous = calls[0].Pos()
	}
	strictEOFCall(t, body, "writeWireFrame", "connection", "request")
	strictEOFCall(t, body, "unixConnection.CloseWrite")
	strictEOFCall(t, body, "readWireFrame", "connection", "storegapdb.MaxWireFrameBytes")
	strictEOFCall(t, body, "strictRequireUnaryEOF", "connection")
	unixType, boundedDial := false, false
	ast.Inspect(body, func(n ast.Node) bool {
		if a, ok := n.(*ast.TypeAssertExpr); ok && strictEOFExpr(a.X) == "connection" && strictEOFExpr(a.Type) == "*net.UnixConn" { unixType = true }
		if lit, ok := n.(*ast.CompositeLit); ok && strictEOFExpr(lit.Type) == "net.Dialer" {
			for _, element := range lit.Elts {
				kv, ok := element.(*ast.KeyValueExpr); if !ok || strictEOFExpr(kv.Key) != "Timeout" { continue }
				strictEOFCall(t, kv.Value, "min", "timeout", "time.Second")
				boundedDial = true
			}
		}
		return true
	})
	if !unixType || !boundedDial { t.Fatal("actual UnixConn/old dial timeout missing") }
	decodes := strictEOFCalls(body, "decodeStrictJSON")
	if len(decodes) != 2 || len(decodes[1].Args) != 2 || strictEOFExpr(decodes[1].Args[0]) != "response.Result" || strictEOFExpr(decodes[1].Args[1]) != "destination" { t.Fatal("actual destination decode missing") }
	checks := strictEOFCalls(body, "validateProcessIdentityWith")
	eof := strictEOFCalls(body, "strictRequireUnaryEOF")[0]
	if len(checks) != 2 || checks[0].Pos() >= eof.Pos() || checks[1].Pos() <= eof.Pos() { t.Fatal("real process attribution bracket absent") }
	paths := strictEOFCalls(body, "verifySocketPathIdentity")
	if len(paths) != 3 || paths[2].Pos() <= checks[1].Pos() { t.Fatal("real socket postcheck absent") }
	for _, path := range []string{"expiry_qualification_work.go", "expiry_qualification_disabled.go"} {
		method := strictEOFFunction(t, path, "closeLocalConnection")
		strictEOFCall(t, method, "connection.Close")
	}
}

func TestStrictUnaryEOFPinnedServerSourceReturnsOnRequestEOF(t *testing.T) {
	// Read-only source assertion, not provider execution or authorization. This
	// exact pinned module source remains unchanged by the product patch.
	raw, err := os.ReadFile("/home/user/go/pkg/mod/github.com/spec-kitty/gapdb@v0.0.0-20260907114832-006e9d469d40/internal/server/server.go")
	if err != nil { t.Fatal(err) }
	source := string(raw)
	for _, token := range []string{"func (server *Server) serveConnection(conn net.Conn)", "_ = conn.Close()", "payload, err := protocol.ReadFrame(conn, server.limits.MaxFrameBytes)", "if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {\n\t\t\t\treturn", "response := server.dispatch(request)", "server.writeResponse(conn, response)"} {
		if !strings.Contains(source, token) { t.Fatal("pinned server EOF premise changed", token) }
	}
}
