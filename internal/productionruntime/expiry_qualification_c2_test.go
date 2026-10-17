//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// An already-closed gate must refuse before even consulting the run context,
// not merely before a later RPC. This is a negative no-work control, not a Host.
type expiryForbiddenContextWork struct{ context.Context }

func (expiryForbiddenContextWork) Err() error { panic("closed gate performed observer context work") }

func TestExpiryHostC2ClosedGateEntryDoesNoObserverWork(t *testing.T) {
	for _, gate := range []*sidecargapdb.ExpiryQualification{nil, {}} {
		run := &expiryHostRun{lease: &expiryHostLease{}, ctx: expiryForbiddenContextWork{context.Background()}, gate: gate}
		err := run.observeS1Refusal(context.Background(), nil)
		refusal, ok := err.(*ProgressError)
		if !ok || refusal.Kind != ProgressIncomplete || refusal.Field != "retained_records" || run.negativeClosed || !run.invalid.Load() || !run.lease.failed {
			t.Fatal("closed entry did not permanently refuse sanitized/no-work")
		}
	}
}

func expirySourceFunction(t *testing.T, path, name string) (*ast.FuncDecl, string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn, string(raw)
		}
	}
	t.Fatalf("missing %s", name)
	return nil, ""
}

func TestExpiryHostC2ExactlyOneFixedWatcherAndOutsideLockJoin(t *testing.T) {
	fn, source := expirySourceFunction(t, "expiry_qualification_observation.go", "observeS1Refusal")
	watchers := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		goStmt, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		watchers++
		closure, ok := goStmt.Call.Fun.(*ast.FuncLit)
		if !ok {
			t.Fatal("watcher is not a fixed local closure")
		}
		calls := map[string]int{}
		ast.Inspect(closure.Body, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					calls[fun.Name]++
				case *ast.SelectorExpr:
					calls[fun.Sel.Name]++
				}
			}
			return true
		})
		if len(calls) != 3 || calls["close"] != 1 || calls["cancel"] != 1 || calls["Done"] != 1 {
			t.Fatal("watcher gained work beyond outer cancel/done signal/select")
		}
		return true
	})
	if watchers != 1 {
		t.Fatalf("fixed local watcher count=%d", watchers)
	}
	entry := strings.Index(source, "gateDone := r.gate.Done()")
	closed := strings.Index(source[entry:], "case <-gateDone:") + entry
	preflight := strings.Index(source[entry:], "r.checkCurrent(parent)") + entry
	prepare := strings.Index(source, ".PrepareRetainedObservationV2(")
	if entry < 0 || closed < entry || preflight < closed || prepare < preflight {
		t.Fatal("closed-at-entry check no longer precedes observer work")
	}
	unlock := strings.Index(source, "r.operationMu.Unlock()")
	cancel := strings.Index(source, "if cancel != nil")
	join := strings.Index(source, "<-watcherDone")
	if unlock < 0 || cancel < unlock || join < cancel {
		t.Fatal("watcher cancel/join not outside operation lock")
	}
	consume := strings.Index(source, "session.ConsumeExpiryQualificationRefusal()")
	end := strings.Index(source, "func (r *expiryHostRun) release(")
	post := source[consume:end]
	for _, forbidden := range []string{"privateContext.Err(", "session.CheckLifetime(", "PrepareRetainedObservationV2(", ".ReadPublications(", ".Finish("} {
		if strings.Contains(post, forbidden) {
			t.Fatal("post-Consume inner-context check or renewed observer work")
		}
	}
	if !strings.Contains(post, "r.checkHeld(ctx, attribution)") || !strings.Contains(post, "r.lease.current != r") {
		t.Fatal("C2 replaced final direct closure guards")
	}
}

func TestExpiryHostC2AndRuntimeSurfaceBuildComplements(t *testing.T) {
	for _, profile := range []struct {
		os, arch string
		tag      bool
		enabled  bool
	}{{"linux", "amd64", true, true}, {"linux", "amd64", false, false}, {"linux", "arm64", true, false}, {"darwin", "amd64", true, false}} {
		cfg := build.Default
		cfg.GOOS, cfg.GOARCH = profile.os, profile.arch
		cfg.BuildTags = nil
		if profile.tag {
			cfg.BuildTags = []string{"spec_kitty_expiry_host_qualification"}
		}
		for _, path := range []string{"expiry_qualification.go", "expiry_qualification_observation.go", "expiry_qualification_rundown.go"} {
			match, err := cfg.MatchFile(".", path)
			if err != nil || match != profile.enabled {
				t.Fatalf("%+v %s match=%v err=%v", profile, path, match, err)
			}
		}
		match, err := cfg.MatchFile(".", "expiry_qualification_disabled.go")
		if err != nil || match == profile.enabled {
			t.Fatal("disabled complement differs")
		}
		match, err = cfg.MatchFile("../sidecar/gapdb", "expiry_qualification_launch.go")
		if err != nil || match != profile.enabled {
			t.Fatal("gate C2 surface selected in wrong build")
		}
	}
	for _, path := range []string{"expiry_qualification.go", "expiry_qualification_observation.go", "expiry_qualification_rundown.go", "expiry_qualification_disabled.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.IsExported() {
				t.Fatal("public runtime qualification root/method added", fn.Name.Name)
			}
		}
	}
}

func TestExpiryHostFirstGateReportAndTeardownSourceOrder(t *testing.T) {
	_, source := expirySourceFunction(t, "expiry_qualification.go", "invalidate")
	for _, required := range []string{"r.gateCloseOnce.Do(", "r.firstGateCloseErr.Store(", "r.gateCloseRecorded.Store(true)", "first := r.firstGateCloseErr.Load()", "r.gate.CleanupError()"} {
		if !strings.Contains(source, required) {
			t.Fatal("first gate outcome/completion no longer joined", required)
		}
	}
	fn, _ := expirySourceFunction(t, "expiry_qualification.go", "restart")
	// Source ordering is supplemental only; genuine restart remains native-pending.
	calls := []string{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "CrashExpiryQualification" || sel.Sel.Name == "Close") {
				calls = append(calls, sel.Sel.Name)
			}
		}
		return true
	})
	if len(calls) != 2 || calls[0] != "CrashExpiryQualification" || calls[1] != "Close" {
		t.Fatal("unclean teardown no longer crash BEFORE Host.Close")
	}
}
