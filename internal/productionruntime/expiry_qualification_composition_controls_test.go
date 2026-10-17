//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"go/ast"
	"go/token"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/projection"
)

// Source guards enumerate frozen-composition obligations separately. They do
// not stand in for external faults in a genuine Host/native observation.
func TestExpiryW5CompositionCaptureSourceGuards(t *testing.T) {
	fn := expiryW5Function(t, "retained_records_proof.go", "retainedCapture")
	var shape, attribution map[string]bool
	for _, stmt := range fn.Body.List {
		guard, ok := stmt.(*ast.IfStmt)
		if !ok { continue }
		atoms := expiryW5Atoms(t, guard.Cond, token.LOR)
		if atoms["h.retained != c"] { shape = atoms }
		if atoms["a.Revision == 0"] { attribution = atoms }
	}
	for _, condition := range []string{
		"h.retained != c", "c.host != h", "c.owner == nil", "c.lifecycle == nil", "c.adapter == nil", "c.projection == nil",
		"h.state != HostReady", "h.owner != c.owner", "h.projection != c.projection", "h.databaseID != c.database", "h.epoch != c.epoch",
		"c.owner.lifecycle != c.lifecycle", "c.owner.adapter != c.adapter", "c.owner.database != c.database",
		"c.owner.binding != c.binding", "c.binding.DatabaseID != c.database",
	} {
		t.Run(condition, func(t *testing.T) { if !shape[condition] { t.Fatal("original composition refusal missing", condition) } })
	}
	for _, condition := range []string{"a.DatabaseID != c.database", "a.RuntimeEpochID != c.epoch", "a.Revision == 0", "a.DurableThrough != a.Revision", "a.CursorToken.IsZero()"} {
		t.Run(condition, func(t *testing.T) { if !attribution[condition] { t.Fatal("attribution refusal missing", condition) } })
	}
}

func TestExpiryW5CompositionCurrentGenerationSourceGuards(t *testing.T) {
	fn := expiryW5Function(t, "expiry_qualification_rundown.go", "currentLocked")
	var atoms map[string]bool
	for _, stmt := range fn.Body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if ok && len(ret.Results) == 1 { atoms = expiryW5Atoms(t, ret.Results[0], token.LAND) }
	}
	for _, condition := range []string{
		"expiryOriginalDoneOpen(r)", "ctx != nil", "ctx.Err() == nil", "r.ctx != nil", "r.ctx.Err() == nil", "!r.invalid.Load()",
		"l.current == r", "l.phase == expiryLeaseRunning", "l.control == nil", "!l.failed", "r.window != nil", "!r.window.retired",
		"l.parent != nil", "l.parent.Err() == nil", "l.ctx != nil", "l.ctx.Err() == nil",
		"r.host != nil", "r.owner != nil", "r.composition != nil", "expiryIdentityValid(r.identity)", "r.gate.Identity() == r.identity",
		"l.config.ControlRoot == l.controlRoot", "l.config.HostAuthorityID == l.hostAuthority",
		"r.owner == r.composition.owner", "r.owner.expiry.run == r", "r.host.expiry.run == r",
		"r.composition.binding == l.binding", "r.composition.database == l.database",
	} {
		t.Run(condition, func(t *testing.T) { if !atoms[condition] { t.Fatal("current-generation conjunct missing", condition) } })
	}
}

func TestExpiryW5CompositionNoAuthorityCaptureStateGuards(t *testing.T) {
	for _, condition := range []string{"nil_host", "nil_composition", "copied_uninstalled_composition", "degraded", "stopped"} {
		t.Run(condition, func(t *testing.T) {
			// These are EARLY state guards only. No model is called an original
			// lifecycle or successful projection. Native cases require real start.
			h := new(Host)
			c := &retainedHostComposition{host: h}
			h.retained = c
			switch condition {
			case "nil_host": h = nil
			case "nil_composition": c = nil
			case "copied_uninstalled_composition": copied := *c; c = &copied
			case "degraded": h.state = HostDegraded
			case "stopped": h.state = HostClosed
			}
			got, ok := h.retainedCapture(c)
			if ok || got != (projection.AttributionV2{}) { t.Fatal("unminted composition returned attribution") }
		})
	}
}

func TestExpiryW5CompositionNoGateCurrentStateGuard(t *testing.T) {
	l := &expiryHostLease{phase: expiryLeaseRunning}
	r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
	l.current = r
	l.mu.Lock()
	valid := r.currentLocked(context.Background())
	l.mu.Unlock()
	if valid { t.Fatal("missing original gate minted a current generation") }
	// No metadata/operation was admitted and no context was constructed.
	if r.window.operation != nil || r.window.metadataCount != 0 || l.control != nil { t.Fatal("scalar guard acquired work") }
}

func TestExpiryW5CompositionPostTerminalLocalMetadataSource(t *testing.T) {
	fn := expiryW5Function(t, "../sidecar/gapdb/expiry_qualification_launch.go", "checkOriginalLifecycle")
	calls := make(map[string]int)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok { calls[expiryW5Text(t, call.Fun)]++ }
		return true
	})
	for _, required := range []string{"openDirectoryNoFollow", "identityOf", "unix.Fstatat", "verifySocketPathIdentity", "lifetime.CheckExpiryQualificationLive"} {
		t.Run(required, func(t *testing.T) { if calls[required] != 1 { t.Fatal("missing/duplicated original local closure", required, calls[required]) } })
	}
	for _, forbidden := range []string{"lifetime.Check", "l.DialOwned", "digestFile", "processExecutable", "l.processes.executable", "l.processes.executableIdentity"} {
		t.Run("no_"+forbidden, func(t *testing.T) { if calls[forbidden] != 0 { t.Fatal("local guard renewed RPC/full image work", forbidden) } })
	}
}
