package gapdb

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func expiryLaunchSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExpiryLaunchSourceConditionalSurfaceAndFirstFields(t *testing.T) {
	for _, name := range []string{"VerifiedExecutable", "Lifecycle"} {
		file := "provenance.go"
		if name == "Lifecycle" {
			file = "lifecycle.go"
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		ast.Inspect(parsed, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if !ok || spec.Name.Name != name {
				return true
			}
			found = true
			st, ok := spec.Type.(*ast.StructType)
			if !ok || len(st.Fields.List) == 0 || len(st.Fields.List[0].Names) != 1 || st.Fields.List[0].Names[0].Name != "expiry" {
				t.Error("conditional state not first", name)
			}
			return false
		})
		if !found {
			t.Fatal("shared owner absent", name)
		}
	}
	for _, file := range []string{"expiry_qualification_launch.go", "expiry_qualification_input.go"} {
		source := expiryLaunchSource(t, file)
		if !strings.HasPrefix(source, "//go:build spec_kitty_expiry_host_qualification && linux && amd64\n") {
			t.Fatal("wrong exact E build constraint", file)
		}
	}
	disabled := expiryLaunchSource(t, "expiry_qualification_disabled.go")
	if !strings.HasPrefix(disabled, "//go:build !spec_kitty_expiry_host_qualification || !linux || !amd64\n") {
		t.Fatal("disabled is not exact complement")
	}
	for _, forbidden := range []string{"SPEC_KITTY_EXPIRY_GATE_FD", "53fa2099", "9d65f8bd", "verifyExpiryQualificationExecutable", "type ExpiryQualification", "StartExpiryQualification", "CrashExpiryQualification"} {
		if strings.Contains(disabled, forbidden) {
			t.Fatal("disabled surface gained fixed tagged behavior", forbidden)
		}
	}
}

func TestExpiryLaunchSourceC2NotificationSurface(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "expiry_qualification_launch.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range file.Decls {
		method, ok := decl.(*ast.FuncDecl)
		if !ok || method.Name.Name != "Done" {
			continue
		}
		found = true
		if method.Recv == nil || len(method.Recv.List) != 1 || method.Type.Results == nil || len(method.Type.Results.List) != 1 {
			t.Fatal("C2 notification signature differs")
		}
		pointer, ok := method.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			t.Fatal("C2 receiver is not pointer")
		}
		receiver, ok := pointer.X.(*ast.Ident)
		if !ok || receiver.Name != "ExpiryQualification" {
			t.Fatal("C2 receiver is not fixed gate")
		}
		channel, ok := method.Type.Results.List[0].Type.(*ast.ChanType)
		if !ok || channel.Dir != ast.RECV {
			t.Fatal("C2 channel is not receive-only")
		}
		value, ok := channel.Value.(*ast.StructType)
		if !ok || len(value.Fields.List) != 0 {
			t.Fatal("C2 notification exposes data")
		}
		calls := 0
		ast.Inspect(method.Body, func(node ast.Node) bool {
			switch node.(type) {
			case *ast.GoStmt, *ast.DeferStmt, *ast.SendStmt, *ast.ForStmt, *ast.RangeStmt:
				t.Error("C2 accessor starts work or sends")
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			calls++
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Done" || len(call.Args) != 0 {
				t.Error("C2 accessor invokes new work/lock/closure")
				return true
			}
			ctx, ok := selector.X.(*ast.SelectorExpr)
			if !ok || ctx.Sel.Name != "ctx" {
				t.Error("C2 accessor does not use existing gate context")
				return true
			}
			state, ok := ctx.X.(*ast.SelectorExpr)
			if !ok || state.Sel.Name != "state" {
				t.Error("C2 accessor selects foreign state")
				return true
			}
			q, ok := state.X.(*ast.Ident)
			if !ok || q.Name != "q" {
				t.Error("C2 accessor selects foreign context")
			}
			return true
		})
		if calls != 1 {
			t.Fatal("C2 accessor has additional calls or no existing context notification", calls)
		}
	}
	if !found {
		t.Fatal("approved C2 method absent")
	}
	for _, name := range []string{"expiry_qualification_disabled.go", "lifecycle.go", "owned.go", "provenance.go"} {
		source := expiryLaunchSource(t, name)
		if strings.Contains(source, "expiryCanceledNotification") {
			t.Fatal("C2 singleton escaped E source", name)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Done" {
				t.Fatal("C2 method escaped E source", name)
			}
		}
	}
}

func TestExpiryLaunchSourceRevokeAndCompleteSuffixCustody(t *testing.T) {
	source := expiryLaunchSource(t, "lifecycle.go")
	for _, pair := range [][2]string{
		{"func (l *Lifecycle) abortStart() error {", "ctx := context.Background()"},
		{"func (l *Lifecycle) Stop(ctx context.Context) (resultErr error) {", "result, _ := l.firstCustodyBefore("},
		{"func (l *Lifecycle) stopOwnedUnclean(ctx context.Context) (resultErr error) {", "result, won := l.firstCustodyBefore("},
		{"func (l *Lifecycle) CleanupStale() error {", "ctx := context.Background()"},
	} {
		_, body, ok := strings.Cut(source, pair[0])
		if !ok || !strings.HasPrefix(strings.TrimSpace(body), pair[1]) {
			t.Fatal("revocation not first", pair[0])
		}
	}
	owned := expiryLaunchSource(t, "owned.go")
	_, body, ok := strings.Cut(owned, "func (l *Lifecycle) revokeOwned() {")
	if !ok || !strings.HasPrefix(strings.TrimSpace(body), "l.ownedRevoked.Store(true)") {
		t.Fatal("gate not revoked before original owned state")
	}
	start := strings.Index(source, "func Start(")
	suffix := strings.Index(source, "func startVerified(")
	attach := strings.Index(source, "func Attach(")
	if start < 0 || suffix <= start || attach <= suffix {
		t.Fatal("shared launch extraction absent")
	}
	prefix := source[start:suffix]
	if !strings.Contains(prefix, "VerifyExecutable(VerifyConfig{") || !strings.Contains(prefix, "closeErr := verified.Close()") || !strings.Contains(prefix, "return startVerified(ctx, config, verified)") {
		t.Fatal("ordinary verified prefix/order changed")
	}
	body = source[suffix:attach]
	for _, required := range []string{"validatePrivateRootWithReport(config.RuntimeRoot, verified.expiry.cleanupReport())", "preparePrivateDirectoryWithReport(config.DataDirectory, verified.expiry.cleanupReport())", "os.MkdirTemp(config.RuntimeRoot", "os.Chmod(runtimeDirectory", "runtimeID, err := identityOf(runtimeFile)", "runtimeIdentityResult(socket, err, runtimeCloseErr)", "lifecycle.initExpiry(verified)", "verified.command(arguments", "command.Stdout, command.Stderr", "startedBefore := time.Now().UTC()", "command.Start()", "startedAfter := time.Now().UTC()", "config.commandStarted(command.Process)", "closeErr := verified.Close()", "lifecycle.waitForComposite(", "lifecycle.startChildWait(command)", "lifecycle.abortStartBefore(ctx, lifecycle.originalNonEscalatingCleanupCutoff(ctx))", "lifecycle.ownedEndpoint, lifecycle.ownedBootstrap", "lifecycle.provenance = SidecarProvenance{", "lifecycle.expiryStarted(verified)"} {
		if !strings.Contains(body, required) {
			t.Error("complete original launch suffix lost stage", required)
		}
	}
	// Retain every suffix stage above, and require the new SINGLE actual Wait
	// owner plus first-custody cancellation/claim/report ordering explicitly.
	custody := expiryLaunchSource(t, "startup_custody.go")
	for _, required := range []string{"l.revokeOwned()", "l.teardownCustody.CompareAndSwap(nil, claim)", "l.beginExpiryTeardownBefore(cleanup)", "l.drainExpiryTeardownBefore(cleanup, teardown)", "l.teardownCustodyErr = result", "l.finishExpiryTeardown(teardown, result)", "if len(socket) > maxSocketPathBytes", "configured socket exceeds Unix bound", "l.startChildWaitWithCall(command, (*exec.Cmd).Wait)", "w.err = waitCall(command)", "w.state = command.ProcessState", "close(w.done)"} {
		if !strings.Contains(custody, required) { t.Error("single-owner custody stage absent", required) }
	}
	if strings.Count(custody, "w.err = waitCall(command)") != 1 || strings.Contains(source, "terminateUnobservedChild(") || strings.Contains(source, "l.command.Wait()") {
		t.Fatal("duplicate or old unbounded wait owner")
	}
	if strings.Index(custody, "l.teardownCustodyErr = result") > strings.Index(custody, "l.finishExpiryTeardown(teardown, result)") { t.Fatal("E completion precedes FIRST result") }
	if strings.Count(body, "verified.expiryCommandStarted()") != 2 {
		t.Fatal("child endpoint not closed after both Start outcomes")
	}
	launch := expiryLaunchSource(t, "expiry_qualification_launch.go")
	_, guard, ok := strings.Cut(launch, "func (g *expiryGateState) checkOriginalLifecycle(")
	if !ok {
		t.Fatal("local guard absent")
	}
	guard, _, _ = strings.Cut(guard, "func (q *ExpiryQualification) CheckHeld(")
	for _, forbidden := range []string{".Check(ctx)", "processExecutable(", ".executable(", "digestFile(", "DialOwned(", ".Get(", ".Status("} {
		if strings.Contains(guard, forbidden) {
			t.Fatal("post-terminal local guard renewed hash/RPC", forbidden)
		}
	}
	if !strings.Contains(guard, "CheckExpiryQualificationLive(ctx)") || !strings.Contains(guard, "openDirectoryNoFollow(") || !strings.Contains(guard, "Fstatat(") {
		t.Fatal("local metadata/pidfd guard incomplete")
	}
	if strings.Count(launch, "l.checkExpiryStartedProcess(v.executed)") != 2 ||
		!strings.Contains(launch, "unix.Open(fmt.Sprintf(\"/proc/%d/fd/4\", l.process.PID)") ||
		!strings.Contains(launch, "unix.Stat(fmt.Sprintf(\"/proc/%d/fd/3\", l.process.PID)") {
		t.Fatal("exact original child process/fd4/fd3 bracket absent")
	}
	_, bracket, ok := strings.Cut(launch, "func (l *Lifecycle) checkExpiryStartedProcess(")
	if !ok {
		t.Fatal("reported original process startup bracket absent")
	}
	bracket, _, _ = strings.Cut(bracket, "func (g *expiryGateState) closeParent()")
	if strings.Count(bracket, ".metadataOpened()") != 2 || strings.Count(bracket, ".metadataClosed(closeErr)") != 2 ||
		strings.Contains(bracket, "processExecutable(") || strings.Contains(bracket, "processStartTicks(") ||
		!strings.Contains(bracket, "verifySealedExecution(image, expected)") {
		t.Fatal("additional startup liveness descriptor outcomes unobserved")
	}
}
