//go:build linux && amd64

package gapdb

// AUTHORED UNRUN AST contracts, not execution/physical containment evidence.
import (
	"go/ast"
	"testing"
)

func TestRevision4AbortRefusalUsesOnlyPublishedValueCustody(t *testing.T) {
	start := strictEOFFunction(t, "startup_custody.go", "startChildWaitWithCall")
	store := strictEOFCalls(start, "l.startupPublished.Store")
	if len(store) != 1 { t.Fatal("sole startup-installed owner publication absent") }
	publish := strictEOFFunction(t, "startup_custody.go", "publishStartupPendingCustody")
	strictEOFCall(t, publish, "l.startupPublished.Load")
	if len(strictEOFCalls(publish, "l.startupPublished.Store")) != 1 { t.Fatal("pending immutable snapshot not published") }
	establish := strictEOFFunction(t, "lifecycle.go", "establishStartedProcess")
	deferred := false
	for _, stmt := range establish.Body.List {
		if d, ok := stmt.(*ast.DeferStmt); ok && len(strictEOFCalls(d, "l.publishStartupPendingCustody")) == 1 { deferred = true }
	}
	if !deferred { t.Fatal("actual rollback return does not publish pending custody") }
	collector := strictEOFFunction(t, "startup_custody.go", "collectPublishedStartupCustody")
	strictEOFCall(t, collector, "l.startupPublished.Load")
	strictEOFCall(t, collector, "owner.wait.published")
	ast.Inspect(collector.Body, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && strictEOFExpr(s.X) == "l" && s.Sel.Name != "startupPublished" { t.Error("refusal reads mutable lifecycle field", s.Sel.Name) }
		if _, ok := n.(*ast.GoStmt); ok { t.Error("collector starts new work") }
		return true
	})
	for _, forbidden := range []string{"command.Wait", "l.lockCleanup", "l.signalLocked", "owner.wait.Wait", "context.WithDeadline", "time.NewTimer"} {
		if len(strictEOFCalls(collector, forbidden)) != 0 { t.Error("collector grants authority/grace", forbidden) }
	}
	abort := strictEOFFunction(t, "startup_custody.go", "abortStartFirst")
	var refusal *ast.IfStmt
	for _, stmt := range abort.Body.List {
		if i, ok := stmt.(*ast.IfStmt); ok && i.Init != nil && len(strictEOFCalls(i.Init, "l.lockCleanup")) == 1 { refusal = i; break }
	}
	if refusal == nil { t.Fatal("initial lock refusal branch absent") }
	strictEOFCall(t, refusal.Body, "l.collectPublishedStartupCustody", "f")
	if _, ok := refusal.Body.List[len(refusal.Body.List)-1].(*ast.ReturnStmt); !ok { t.Fatal("refusal does not publish frozen outcome before returning") }
	ast.Inspect(refusal.Body, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && strictEOFExpr(s.X) == "l" && s.Sel.Name != "collectPublishedStartupCustody" { t.Error("early branch directly reads mutable lifecycle fields", s.Sel.Name) }
		return true
	})
	bridge := strictEOFFunction(t, "startup_custody.go", "published")
	for _, forbidden := range []string{"command.ProcessState", "l.mu", "w.command", "w.process"} {
		ast.Inspect(bridge.Body, func(n ast.Node) bool { if s, ok := n.(*ast.SelectorExpr); ok && strictEOFExpr(s) == forbidden { t.Error("value bridge gained capability", forbidden) }; return true })
	}
}
