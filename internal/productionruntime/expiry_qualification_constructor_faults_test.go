//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// H06 constructor/root component tests. These reach original filesystem,
// actual os.File.Close, productionOwner.Close and rollbackControl bodies.
// None constructs an authenticated Host, adapter, gate, ACK or provider workload.
// Native fault-placement obligations remain independent and OPEN.
func h06ConstructorDirectory(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil { t.Fatal(err) }
	parent, err := os.MkdirTemp(cache, "h06-root-")
	if err != nil { t.Fatal(err) }
	if err := os.Chmod(parent, 0700); err != nil { t.Fatal(err) }
	t.Logf("H06 NEW private residual, preserve for manual retirement: %s", parent)
	return parent
}

func h06ConstructorFile(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil { t.Fatal(err) }
	return file
}

func h06ConstructorCounts(t *testing.T, l *expiryHostLease, kind expiryRootDescriptorKind, acquired, attempted, completed uint64) {
	t.Helper()
	c := &l.cleanup.categories[kind]
	got := [3]uint64{c.acquired.Load(), c.attempted.Load(), c.completed.Load()}
	if got != [3]uint64{acquired, attempted, completed} { t.Fatalf("kind=%d acquired/attempted/completed=%v want=%v", kind, got, [3]uint64{acquired, attempted, completed}) }
	t.Logf("kind=%d acquired/attempted/completed=%v", kind, got)
}

func h06ConstructorSnapshot(t *testing.T, l *expiryHostLease) {
	t.Helper()
	for i := range l.cleanup.categories {
		c := &l.cleanup.categories[i]
		t.Logf("root kind=%d acquired=%d attempted=%d completed=%d", i, c.acquired.Load(), c.attempted.Load(), c.completed.Load())
	}
	c := l.control
	if c != nil { t.Logf("controller stage=%d first=%v opaqueStartup=%t residualPinned=%t", c.stage, c.firstErr, c.opaqueStartup, l.residualPinned) }
	t.Logf("root FIRST=%v complete=%v failed=%t; no process inferred from root counters", l.cleanup.outcome(false), l.cleanup.outcome(true), l.failed)
}

// Legacy fixture disposal helper retains its identity, but an unsafe/residual
// graph now categorically bars automatic retirement. No failed FIRST resource,
// metadata refusal, signal, unknown owner or retained root is retried/disposed.
func h06ConstructorRetireOwnRoots(t *testing.T, l *expiryHostLease) {
	t.Helper()
	// A metadata/unknown-custody residual is not automatic cleanup authority.
	// Preserve strong product custody through process exit, including Fatal.
	if l.residualPinned || l.control != nil && l.control.stage == expiryControlTerminalResidual { return }
	for _, pair := range []struct { kind expiryRootDescriptorKind; file *os.File }{
		{expiryRootLock, l.lock}, {expiryRootData, l.directory}, {expiryRootParent, l.parentDirectory},
	} {
		if pair.file != nil && l.cleanup.categories[pair.kind].attempted.Load() == 0 {
			l.cleanup.closeFile(pair.file, pair.kind)
			t.Logf("AFTER residual snapshot manual FIRST root retirement kind=%d", pair.kind)
		}
	}
}

func TestExpiryH06ReserveRootRealFailureSites(t *testing.T) {
	for _, placement := range []string{"missing-parent", "parent-mode", "parent-symlink", "data-existing-empty", "data-existing-nonempty", "lease-existing", "lease-name-bound"} {
		t.Run(placement, func(t *testing.T) {
			parent := h06ConstructorDirectory(t)
			root := filepath.Join(parent, "authority")
			field := "expiry_root_exclusive"
			switch placement {
			case "missing-parent": root = filepath.Join(parent, "missing", "authority"); field = "expiry_root_parent"
			case "parent-mode":
				if err := os.Chmod(parent, 0750); err != nil { t.Fatal(err) }; field = "expiry_root_parent"
			case "parent-symlink":
				link := filepath.Join(parent, "alias")
				actual := filepath.Join(parent, "actual")
				if err := os.Mkdir(actual, 0700); err != nil { t.Fatal(err) }
				if err := os.Symlink(actual, link); err != nil { t.Fatal(err) }
				root = filepath.Join(link, "authority"); field = "expiry_root_parent"
			case "data-existing-empty", "data-existing-nonempty":
				if err := os.Mkdir(root, 0700); err != nil { t.Fatal(err) }
				if placement == "data-existing-nonempty" {
					file := h06ConstructorFile(t, filepath.Join(root, "fixture-owned-preserved"))
					if first := file.Close(); first != nil { t.Fatal(first) }
				}
			case "lease-existing":
				file := h06ConstructorFile(t, filepath.Join(parent, ".authority"+expiryLeaseSuffix))
				if first := file.Close(); first != nil { t.Fatal(first) }; field = "expiry_lease_exclusive"
			case "lease-name-bound": root = filepath.Join(parent, strings.Repeat("x", 255)); field = "expiry_lease_name"
			}
			l, primary := reserveExpiryHostLease(context.Background(), Config{ControlRoot: root})
			var typed *Error
			if l != nil || !errors.As(primary, &typed) || typed.Field != field { t.Fatalf("real constructor placement=%s lease=%v primary=%v", placement, l, primary) }
			// reserve returns no partial lease/report. Do not infer per-FD
			// closure from nil handle or a whole-process FD count.
			t.Logf("primary field=%s; partial descriptor ledger NOT EXPOSED; source-bound rollback only, Native per-resource custody remains required; process=never-started", field)
			if placement == "lease-existing" {
				if info, err := os.Lstat(root); err != nil || !info.IsDir() { t.Fatal("failed created isolation root not preserved", err) }
			}
		})
	}
}

func TestExpiryH06RootFirstCloseRealFailureSites(t *testing.T) {
	for _, placement := range []string{"parent", "data", "lease", "metadata"} {
		t.Run(placement, func(t *testing.T) {
			parent := h06ConstructorDirectory(t)
			l, err := reserveExpiryHostLease(context.Background(), Config{ControlRoot: filepath.Join(parent, "authority")})
			if err != nil { t.Fatal(err) }
			defer h06ConstructorRetireOwnRoots(t, l)
			c := l.control
			file, kind := l.parentDirectory, expiryRootParent
			switch placement {
			case "data": file, kind = l.directory, expiryRootData
			case "lease": file, kind = l.lock, expiryRootLock
			case "metadata":
				file, err = l.openDirectory(parent, expiryRootMetadata); kind = expiryRootMetadata
				if err != nil { t.Fatal(err) }
			}
			// Closed-object component fault: File.Close retires the raw fd and
			// prevents any later product call from closing a reused descriptor.
			// This is not evidence of a failed FIRST physical kernel close.
			if err := file.Close(); err != nil { t.Fatal(err) }
			if placement == "metadata" { l.cleanup.closeFile(file, kind) }
			primary := errors.New("H06 constructor primary before rollback")
			outcome := l.rollbackControl(c, primary)
			h06ConstructorSnapshot(t, l)
			if !errors.Is(outcome, primary) || c.firstErr != primary || c.stage != expiryControlTerminalResidual || !l.residualPinned || l.control != c { t.Fatal("primary/canonical metadata residual lost", outcome) }
			for _, rootKind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} { h06ConstructorCounts(t, l, rootKind, 1, 0, 0) }
			if placement == "metadata" {
				// Actual metadata-close-only error remains independently observable.
				if !errors.Is(outcome, os.ErrClosed) || l.cleanup.first.Load() == nil || !errors.Is(l.cleanup.first.Load().err, os.ErrClosed) { t.Fatal("actual metadata File.Close error lost", outcome) }
				h06ConstructorCounts(t, l, expiryRootMetadata, 2, 2, 2)
			} else {
				// Closed File BEFORE final check: Fstat refuses metadata; NOT a
				// failed root Close. checkPathSnapshot classifies, drops OS cause.
				var typed *Error
				if !errors.As(l.cleanup.outcome(false), &typed) || typed.Field != "expiry_root_identity" || errors.Is(outcome, os.ErrClosed) { t.Fatal("closed-root METADATA refusal misclassified", outcome) }
				h06ConstructorCounts(t, l, expiryRootMetadata, 1, 1, 1)
			}
			sticky := l.cleanup.first.Load()
			if sticky == nil || l.cleanup.outcome(true) == nil { t.Fatal("metadata FIRST absent") }
			if !errors.Is(l.rollbackControl(c, nil), primary) || sticky != l.cleanup.first.Load() { t.Fatal("repeat repaired FIRST metadata residual") }
			t.Logf("primary=%v cleanup union=%v transferred=root lease only; kill/wait/reap=not-applicable; no Host success", primary, outcome)
		})
	}
}

func TestExpiryH06RollbackActualOwnerCloseAndIndependentUnion(t *testing.T) {
	for _, placement := range []string{"owner-close", "owner-plus-root-close", "opaque-constructor"} {
		t.Run(placement, func(t *testing.T) {
			parent := h06ConstructorDirectory(t)
			l, err := reserveExpiryHostLease(context.Background(), Config{ControlRoot: filepath.Join(parent, "authority")})
			if err != nil { t.Fatal(err) }
			defer h06ConstructorRetireOwnRoots(t, l)
			c := l.control
			var r *expiryHostRun
			var metadataFirst error
			var ownedCloser *os.File
			if placement == "opaque-constructor" {
				// Explicit constructor-state component placement, no simulated
				// close report: production must preserve unknown startup custody.
				c.opaqueStartup = true
			} else {
				file := h06ConstructorFile(t, filepath.Join(parent, "owned-closer"))
				ownedCloser = file
				r = &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
				r.owner = &productionOwner{expiry: ownerExpiryState{run: r}, closer: file}
				c.candidate = r
				if placement == "owner-plus-root-close" {
					metadata, err := l.openDirectory(parent, expiryRootMetadata)
					if err != nil { t.Fatal(err) }
					if err := metadata.Close(); err != nil { t.Fatal(err) }
					l.cleanup.closeFile(metadata, expiryRootMetadata)
					metadataFirst = l.cleanup.first.Load().err
				}
			}
			// Safe closed-os.File component model, not a failed kernel close.
			// No unowned numeric descriptor is retained for product re-close.
			if ownedCloser != nil { if err := ownedCloser.Close(); err != nil { t.Fatal(err) } }
			primary := errors.New("H06 independent original constructor failure")
			outcome := l.rollbackControl(c, primary)
			h06ConstructorSnapshot(t, l)
			if !errors.Is(outcome, primary) || c.firstErr != primary || c.stage != expiryControlTerminalResidual || !l.residualPinned || l.control != c { t.Fatal("rollback lost primary or canonical residual", outcome) }
			for _, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} { h06ConstructorCounts(t, l, kind, 1, 0, 0) }
			if r != nil {
				if !r.ownerCloseRecorded || !errors.Is(r.firstOwnerCloseErr, os.ErrClosed) || !errors.Is(outcome, r.firstOwnerCloseErr) || !r.window.completed { t.Fatal("actual FIRST owner close absent from union", outcome, r.firstOwnerCloseErr) }
				if r.hostCloseRecorded || r.gateCloseRecorded.Load() || r.host != nil || r.gate != nil { t.Fatal("absence fabricated a Host/gate close") }
				first := r.firstOwnerCloseErr
				if err := r.owner.Close(context.Background()); err != nil || r.firstOwnerCloseErr != first { t.Fatal("repeat-nil repaired FIRST actual owner outcome", err) }
			}
			if metadataFirst != nil && !errors.Is(outcome, metadataFirst) { t.Fatal("independent root error omitted", outcome) }
			t.Logf("primary=%v union=%v; roots retained BEFORE manual FIRST retirement; kill/wait/reap=UNKNOWN for opaque, not-applicable for component owner with nil lifecycle; NOT genuine Host", primary, outcome)
		})
	}
}

// Every placement below resolves an ACTUAL production function and call, not a
// fake error report or a replacement constructor. Presence is source evidence
// ONLY: it cannot discharge syscall faults, successful transfers, or Native.
func TestExpiryH06ConstructorUnreachableFaultSourcePlacements(t *testing.T) {
	type placement struct { file, function, object, method string; minimum int }
	for _, p := range []placement{
		{"expiry_qualification.go", "reserveExpiryHostLease", "l", "openDirectory", 1},
		{"expiry_qualification.go", "reserveExpiryHostLease", "unix", "Mkdirat", 1},
		{"expiry_qualification.go", "reserveExpiryHostLease", "unix", "Openat", 2},
		{"expiry_qualification.go", "reserveExpiryHostLease", "l.directory", "ReadDir", 1},
		{"expiry_qualification.go", "reserveExpiryHostLease", "unix", "Flock", 1},
		{"expiry_qualification.go", "checkPathSnapshot", "unix", "Fstatat", 1},
		{"expiry_qualification.go", "checkPathSnapshot", "unix", "Flock", 1},
		{"expiry_qualification.go", "startGeneration", "sidecargapdb", "StartExpiryQualification", 1},
		{"expiry_qualification.go", "startGeneration", "lifecycle", "DialOwned", 1},
		{"expiry_qualification.go", "startGeneration", "adapter", "Get", 1},
		{"composition.go", "completeHostStartup", "journalrestore", "RestoreV2", 4},
		{"composition.go", "completeHostStartup", "config.Reconciler", "Reconcile", 1},
		{"composition.go", "completeHostStartup", "host", "Close", 1},
		{"composition.go", "Close", "o.closer", "Close", 1},
		{"composition.go", "Close", "o.lifecycle", "Stop", 1},
	} {
		t.Run(p.function+"/"+p.object+"."+p.method, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), p.file, nil, 0)
			if err != nil { t.Fatal(err) }
			count := 0
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != p.function { continue }
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok { return true }
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == p.method && h06ConstructorSelector(sel.X) == p.object { count++ }
					return true
				})
			}
			if count < p.minimum { t.Fatalf("blocked fault placement drift: %s/%s %s.%s count=%d minimum=%d", p.file, p.function, p.object, p.method, count, p.minimum) }
			t.Log("SOURCE ONLY: no syscall fault executed, no cleanup/reap/Native credit")
		})
	}
	// Non-selector helper calls are separate constructor refusal boundaries.
	for _, helper := range []string{"ensureAuthorityBinding", "completeHostStartup", "readCurrentEpoch", "admitFreshEpoch", "restoredAuthority", "composeRuntimeCapabilities"} {
		t.Run(helper, func(t *testing.T) {
			found := false
			for _, name := range []string{"composition.go", "expiry_qualification.go"} {
				parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
				if err != nil { t.Fatal(err) }
				for _, decl := range parsed.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Name.Name != "completeHostStartup" && fn.Name.Name != "startGeneration" { continue }
					ast.Inspect(fn.Body, func(n ast.Node) bool { if call, ok := n.(*ast.CallExpr); ok { if id, yes := call.Fun.(*ast.Ident); yes && id.Name == helper { found = true } }; return true })
				}
			}
			if !found { t.Fatal("constructor helper placement disappeared", helper) }
		})
	}
}

// Historical test identity retained; its old missing-close characterization is
// superseded by REQUIRED positive FIRST-custody source contracts. No syscall,
// successful Host transfer, durable retention or Native credit follows.
func TestExpiryH06FrozenKernelCloseLossSource(t *testing.T) {
	root := filepath.Join("..", "controlstore", "gapdb", "retainedkernel")
	for _, p := range []struct { file, function string; required []string }{
		{"lifetime.go", "newLifetimeWithClose", []string{"joinRetainedCustody(err, l.closeWith(closeFD))"}},
		{"lifetime.go", "closeWith", []string{"l.Revoke()", "l.mu.Lock()", "return l.closeErr", "l.closeAttempted = true", "l.pidfd = -1", "l.closeErr = retainedCloseResult(nil, \"pidfd\", closeFD(fd))"}},
		{"lifetime.go", "checkLockedWithCalls", []string{"defer func()", "resultErr = retainedCloseResult(resultErr, \"proc-exe\", closeFile(file))"}},
		{"lifetime.go", "startTicksWithCalls", []string{"defer func()", "retainedCloseResult(resultErr, \"proc-stat\", closeFile(file))", "ticks = 0"}},
		{"lifetime.go", "configuredSocketIdentityWithCalls", []string{"defer func()", "retainedCloseResult(resultErr, \"socket-directory\", closeFD(fd))"}},
		{"file_open.go", "openRetainedFileWithCalls", []string{"retainedCloseResult(err, \"file-postcheck\", closeFile(file))"}},
	} {
		t.Run(p.function, func(t *testing.T) {
			path := filepath.Join(root, p.file)
			raw, err := os.ReadFile(path)
			if err != nil { t.Fatal(err) }
			set := token.NewFileSet()
			parsed, err := parser.ParseFile(set, path, raw, 0)
			if err != nil { t.Fatal(err) }
			found := false
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != p.function { continue }
				found = true
				body := string(raw[set.Position(fn.Body.Pos()).Offset:set.Position(fn.Body.End()).Offset])
				for _, required := range p.required { if !strings.Contains(body, required) { t.Error("FIRST custody source lost", p.function, required) } }
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if a, ok := n.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && len(a.Rhs) == 1 {
						if blank, yes := a.Lhs[0].(*ast.Ident); yes && blank.Name == "_" {
							if c, yes := a.Rhs[0].(*ast.CallExpr); yes {
								if sel, yes := c.Fun.(*ast.SelectorExpr); yes && sel.Sel.Name == "Close" { t.Error("discarded actual close reintroduced") }
							}
						}
					}
					return true
				})
			}
			if !found { t.Fatal("FIRST custody function absent", p.function) }
			t.Log("SOURCE ONLY: positive FIRST result retention; actual-fault/Host/Native obligations still OPEN")
		})
	}
	wrapper, err := os.ReadFile(filepath.Join(root, "lifetime_custody.go"))
	if err != nil { t.Fatal(err) }
	for _, required := range []string{"return e.primary.Error()", "[]error{e.primary, e.custody}", "return e.cause.Error()", "if custody == nil", "return primary"} {
		if !strings.Contains(string(wrapper), required) { t.Error("primary diagnostic/traversal contract absent", required) }
	}
}

func h06ConstructorSelector(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident: return value.Name
	case *ast.SelectorExpr: return h06ConstructorSelector(value.X)+"."+value.Sel.Name
	default: return ""
	}
}
