//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"testing"
)

// W5 code-only controls. Source and state-guard evidence, NOT genuine Host
// execution. Fresh genuine-Host cases live only in host-restart-code-* cache.
func expiryW5Function(t *testing.T, path, name string) *ast.FuncDecl {
	t.Helper()
	// Go overlays do not redirect parser/os file reads. Only the qualification
	// product uses the explicit test-only selected-source contract; unrelated
	// W5 source controls retain their original file reads.
	var source any
	if path == "expiry_qualification.go" { source = expiryCompatReadSelected(t) }
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil { t.Fatal(err) }
	var result *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			if result != nil { t.Fatalf("ambiguous source function %s", name) }
			result = fn
		}
	}
	if result == nil { t.Fatalf("missing source function %s", name) }
	return result
}

func expiryW5Text(t *testing.T, node ast.Node) string {
	t.Helper()
	var b bytes.Buffer
	if err := printer.Fprint(&b, token.NewFileSet(), node); err != nil { t.Fatal(err) }
	return b.String()
}

func expiryW5Atoms(t *testing.T, expr ast.Expr, op token.Token) map[string]bool {
	t.Helper()
	result := make(map[string]bool)
	var visit func(ast.Expr)
	visit = func(e ast.Expr) {
		if b, ok := e.(*ast.BinaryExpr); ok && b.Op == op { visit(b.X); visit(b.Y); return }
		result[expiryW5Text(t, e)] = true
	}
	visit(expr)
	return result
}

func expiryW5Error(t *testing.T, err error, field string) {
	t.Helper()
	var actual *Error
	if !errors.As(err, &actual) || actual.Kind != ErrorStartup || actual.State != HostRecovering || actual.Field != field {
		t.Fatalf("wrong refusal boundary: %v; want %s", err, field)
	}
}

func TestExpiryW5RestartMissingAuthorityStateGuards(t *testing.T) {
	for _, mode := range []string{"clean", "unclean"} {
		for _, shape := range []string{"nil_run", "missing_lease"} {
			t.Run(mode+"/"+shape, func(t *testing.T) {
				var run *expiryHostRun
				if shape == "missing_lease" { run = &expiryHostRun{} }
				var host *Host
				var next *expiryHostRun
				var err error
				if mode == "clean" { host, next, err = run.restartClean(context.Background(), "supervisor-w5-new") } else { host, next, err = run.restartUnclean(context.Background(), "supervisor-w5-new") }
				if host != nil || next != nil { t.Fatal("missing authority escaped a successor") }
				expiryW5Error(t, err, "expiry_generation")
			})
		}
	}
}

func TestExpiryW5RestartStaleReservationStateGuards(t *testing.T) {
	for _, mode := range []expiryControlKind{expiryControlRestartClean, expiryControlRestartUnclean} {
		for _, condition := range []string{"missing_run", "wrong_lease", "stale_run", "missing_window", "released_lease"} {
			t.Run(strconv.Itoa(int(mode))+"/"+condition, func(t *testing.T) {
				// Deliberately no fabricated healthy gate, process or provider.
				l := &expiryHostLease{phase: expiryLeaseRunning}
				r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
				l.current = r
				switch condition {
				case "missing_run": r = nil
				case "wrong_lease": r.lease = new(expiryHostLease)
				case "stale_run": l.current = &expiryHostRun{lease: l}
				case "missing_window": r.window = nil
				case "released_lease": l.phase = expiryLeaseClosed
				}
				before := l.current
				control, err := l.reserveControl(r, mode, context.Background())
				if control != nil || l.control != nil || l.current != before || !l.failed { t.Fatal("loser acquired or replaced transition authority") }
				expiryW5Error(t, err, "expiry_stale_controller")
			})
		}
	}
}

func TestExpiryW5RestartRepeatedTransitionStateGuard(t *testing.T) {
	l := &expiryHostLease{phase: expiryLeaseFresh}
	c, err := l.reserveControl(nil, expiryControlFresh, context.Background())
	if err != nil { t.Fatal(err) }
	if err := l.moveControl(c, expiryControlAcquiringRoot, expiryControlReserved); err != nil { t.Fatal(err) }
	// Repeating the original transition must not advance, replace, or repair.
	err = l.moveControl(c, expiryControlChecking, expiryControlReserved)
	expiryW5Error(t, err, "expiry_controller_stage")
	if l.control != c || c.stage != expiryControlAcquiringRoot || !l.failed { t.Fatal("repeated transition changed canonical controller") }
}

func TestExpiryW5RestartFirstOwnerAndHostOutcomeStateGuards(t *testing.T) {
	for _, target := range []string{"owner", "host"} {
		for _, firstFails := range []bool{false, true} {
			t.Run(target+"/first_fails_"+strconv.FormatBool(firstFails), func(t *testing.T) {
				l := new(expiryHostLease)
				r := &expiryHostRun{lease: l}
				o := &productionOwner{expiry: ownerExpiryState{run: r}}
				h := &Host{expiry: hostExpiryState{run: r}}
				first, later := error(nil), error(errors.New("later outcome must not replace FIRST"))
				if firstFails { first, later = errors.New("FIRST cleanup failure"), nil }
				if target == "owner" { o.noteExpiryOwnerClose(first); o.noteExpiryOwnerClose(later) } else { h.expiryClosed(first); h.expiryClosed(later) }
				recorded, got := r.ownerCloseRecorded, r.firstOwnerCloseErr
				if target == "host" { recorded, got = r.hostCloseRecorded, r.firstHostCloseErr }
				if !recorded || got != first || !l.failed { t.Fatal("nullable FIRST outcome lost or later failure erased") }
				// No actual Stop/Wait/Close is claimed by this recording-hook test.
			})
		}
	}
}

// One strict helper-aware contract on the actual selected product bytes. No
// legacy-inline alternative or permanently cached candidate proxy is accepted.
func TestExpiryW5RestartFreshnessSourceGuards(t *testing.T) {
	source := expiryCompatReadSelected(t)
	if err := expiryCompatFreshness(source); err != nil { t.Fatal(err) }
	if err := expiryCompatOnlyExtraction(source); err != nil { t.Fatal(err) }
}

func TestExpiryW5RestartCleanupSourceGuards(t *testing.T) {
	source := expiryCompatReadSelected(t)
	if err := expiryCompatCleanup(source); err != nil { t.Fatal(err) }
	if err := expiryCompatOnlyExtraction(source); err != nil { t.Fatal(err) }
}

func TestExpiryW5RestartNoReplacementConfigurationSource(t *testing.T) {
	for _, name := range []string{"restartClean", "restartUnclean", "restart"} {
		t.Run(name, func(t *testing.T) {
			fn := expiryW5Function(t, "expiry_qualification.go", name)
			for _, field := range fn.Type.Params.List {
				kind := expiryW5Text(t, field.Type)
				if kind != "context.Context" && kind != "string" && kind != "bool" { t.Fatal("restart acquired replacement selector", kind) }
			}
		})
	}
}
