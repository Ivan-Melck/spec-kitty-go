//go:build linux && amd64 && spec_kitty_expiry_host_qualification

package productionruntime

import (
	"bytes"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// Source models only: no observer execution, watcher scheduling, or live gate.
const expiryFinalModelGuard = "if !expiryOriginalDoneOpen(r) {\n\t\treturn retainedRecordsError(parent, ctx.Err())\n\t}"

func expiryFinalModelRender(n ast.Node) string {
	var b bytes.Buffer
	if err := format.Node(&b, token.NewFileSet(), n); err != nil { return "<format-error>" }
	return b.String()
}

func expiryFinalModelFunction(src, name string) (*token.FileSet, *ast.FuncDecl, error) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "actual.go", src, 0)
	if err != nil { return nil, nil, err }
	var found *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
			if found != nil { return nil, nil, errors.New("duplicate function: " + name) }
			found = fn
		}
	}
	if found == nil || found.Body == nil { return nil, nil, errors.New("missing function: " + name) }
	return fs, found, nil
}

func expiryFinalModelIf(s ast.Stmt, condition, result string) bool {
	n, ok := s.(*ast.IfStmt)
	if !ok || n.Init != nil || n.Else != nil || len(n.Body.List) != 1 { return false }
	return expiryFinalModelRender(n.Cond) == condition && expiryFinalModelRender(n.Body.List[0]) == result
}

func expiryFinalModelValidate(src string) error {
	_, fn, err := expiryFinalModelFunction(src, "observeS1Refusal")
	if err != nil { return err }
	if fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 ||
		fn.Recv.List[0].Names[0].Name != "r" || expiryFinalModelRender(fn.Recv.List[0].Type) != "*expiryHostRun" {
		return errors.New("wrong observer receiver")
	}
	publication, count := -1, 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range a.Lhs {
				if expiryFinalModelRender(lhs) == "r.negativeClosed" { count++ }
			}
		}
		return true
	})
	for i, s := range fn.Body.List {
		if a, ok := s.(*ast.AssignStmt); ok && a.Tok == token.ASSIGN && len(a.Lhs) == 1 && len(a.Rhs) == 1 &&
			expiryFinalModelRender(a.Lhs[0]) == "r.negativeClosed" && expiryFinalModelRender(a.Rhs[0]) == "true" { publication = i }
	}
	body := fn.Body.List
	if count != 1 || publication != len(body)-2 || publication < 5 || expiryFinalModelRender(body[publication+1]) != "return nil" {
		return errors.New("wrong publication suffix")
	}
	// Baseline d3 must fail here for the missing guard, not a digest mismatch.
	if !expiryFinalModelIf(body[publication-1], "!expiryOriginalDoneOpen(r)", "return retainedRecordsError(parent, ctx.Err())") {
		return errors.New("missing_final_original_gate_cutoff")
	}
	cleanup, ok := body[publication-2].(*ast.IfStmt)
	if !ok || cleanup.Else != nil || cleanup.Init == nil || expiryFinalModelRender(cleanup.Init) != "err := r.gate.RecordedCleanupError()" ||
		expiryFinalModelRender(cleanup.Cond) != "err != nil" || len(cleanup.Body.List) != 1 || expiryFinalModelRender(cleanup.Body.List[0]) != "return err" {
		return errors.New("cleanup must precede final gate cutoff")
	}
	final, ok := body[publication-3].(*ast.IfStmt)
	if !ok || final.Init != nil || final.Else != nil || len(final.Body.List) != 1 ||
		expiryFinalModelRender(final.Body.List[0]) != "return retainedRecordsError(parent, ctx.Err())" {
		return errors.New("missing final lease/context refusal")
	}
	calls := map[string]bool{}
	ast.Inspect(final.Cond, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok { calls[expiryFinalModelRender(c)] = true }
		return true
	})
	if !calls["parent.Err()"] || !calls["ctx.Err()"] || !calls["r.ctx.Err()"] ||
		expiryFinalModelRender(body[publication-5]) != "r.lease.mu.Lock()" ||
		expiryFinalModelRender(body[publication-4]) != "defer r.lease.mu.Unlock()" {
		return errors.New("final context/lease lock order changed")
	}
	return nil
}

func expiryFinalModelRead(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil { t.Fatal(err) }
	return string(b)
}

func expiryFinalModelUniqueReplace(t *testing.T, part, old, replacement string) string {
	t.Helper()
	if old == "" || strings.Count(part, old) != 1 { t.Fatalf("mutation target not unique: %q", old) }
	return strings.Replace(part, old, replacement, 1)
}

// Limit replacement to the unique actual function; unrelated cutoffs are untouched.
func expiryFinalModelReplace(t *testing.T, src, name, old, replacement string) string {
	t.Helper()
	fs, fn, err := expiryFinalModelFunction(src, name)
	if err != nil { t.Fatal(err) }
	start, end := fs.Position(fn.Pos()).Offset, fs.Position(fn.End()).Offset
	return src[:start] + expiryFinalModelUniqueReplace(t, src[start:end], old, replacement) + src[end:]
}

func TestExpiryHostFinalGateCutoffActualSourceModel(t *testing.T) {
	if err := expiryFinalModelValidate(expiryFinalModelRead(t, "expiry_qualification_observation.go")); err != nil { t.Fatal(err) }
}

func TestExpiryHostFinalGateCutoffPlacementMutantsModel(t *testing.T) {
	const name = "observeS1Refusal"
	src := expiryFinalModelRead(t, "expiry_qualification_observation.go")
	if err := expiryFinalModelValidate(src); err != nil {
		if err.Error() != "missing_final_original_gate_cutoff" { t.Fatal(err) }
		src = expiryFinalModelReplace(t, src, name, "r.negativeClosed = true", expiryFinalModelGuard + "\n\tr.negativeClosed = true")
	}
	if err := expiryFinalModelValidate(src); err != nil { t.Fatalf("source-emittable proposed candidate: %v", err) }
	fs, fn, err := expiryFinalModelFunction(src, name)
	if err != nil { t.Fatal(err) }
	list := fn.Body.List
	n := len(list)-2
	text := func(s ast.Stmt) string { return src[fs.Position(s.Pos()).Offset:fs.Position(s.End()).Offset] }
	guard, cleanup, final := text(list[n-1]), text(list[n-2]), text(list[n-3])
	replace := func(s, old, new string) string { return expiryFinalModelReplace(t, s, name, old, new) }
	alterGuard := func(old, new string) string { return expiryFinalModelUniqueReplace(t, guard, old, new) }
	without := replace(src, guard, "")
	mutants := []struct { name, source string }{
		{"removal", without},
		{"inversion", replace(src, guard, alterGuard("!expiryOriginalDoneOpen", "expiryOriginalDoneOpen"))},
		{"before_final_context_guard", replace(without, final, guard + "\n\t" + final)},
		{"after_publication", replace(without, "r.negativeClosed = true", "r.negativeClosed = true\n\t" + guard)},
		{"cleanup_wrong_priority", replace(without, cleanup, guard + "\n\t" + cleanup)},
		{"observer_context_substitution", replace(src, guard, alterGuard("!expiryOriginalDoneOpen(r)", "ctx.Err() != nil"))},
		{"ctx_return_cause_substitution", replace(src, guard, alterGuard("ctx.Err()", "r.ctx.Err()"))},
		{"wrong_receiver", replace(src, guard, alterGuard("expiryOriginalDoneOpen(r)", "expiryOriginalDoneOpen(other)"))},
		{"wrong_return_nil", replace(src, guard, alterGuard("return retainedRecordsError(parent, ctx.Err())", "return nil"))},
	}
	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			// Parse separately: syntax failure is not an accepted mutant kill.
			if _, _, err := expiryFinalModelFunction(m.source, name); err != nil { t.Fatal(err) }
			if expiryFinalModelValidate(m.source) == nil { t.Fatal("placement mutant accepted") }
		})
	}
}

const expiryFinalModelHelperBody = `{
	if r == nil || r.gate == nil {
		return false
	}
	done := r.gate.Done()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}`

func expiryFinalModelHelperValidate(src string) error {
	_, fn, err := expiryFinalModelFunction(src, "expiryOriginalDoneOpen")
	if err != nil { return err }
	_, want, err := expiryFinalModelFunction("package productionruntime\nfunc expiryOriginalDoneOpen(r *expiryHostRun) bool " + expiryFinalModelHelperBody, "expiryOriginalDoneOpen")
	if err != nil { return err }
	// Exact small AST body excludes extra locks, I/O, callbacks, and other channels.
	if fn.Recv != nil || expiryFinalModelRender(fn.Type) != expiryFinalModelRender(want.Type) ||
		expiryFinalModelRender(fn.Body) != expiryFinalModelRender(want.Body) { return errors.New("original_done_helper_contract") }
	return nil
}

func TestExpiryHostFinalGateCutoffOriginalDoneHelperModel(t *testing.T) {
	const name = "expiryOriginalDoneOpen"
	src := expiryFinalModelRead(t, "expiry_qualification_rundown.go")
	if err := expiryFinalModelHelperValidate(src); err != nil { t.Fatal(err) }
	mutants := []struct { name, old, replacement string }{
		{"nil_run", "r == nil || r.gate == nil", "r.gate == nil"},
		{"nil_gate", "r == nil || r.gate == nil", "r == nil"},
		{"nil_done", "if done == nil {\n\t\treturn false", "if done == nil {\n\t\treturn true"},
		{"wrong_channel", "r.gate.Done()", "r.ctx.Done()"},
		{"closed_select", "case <-done:\n\t\treturn false", "case <-done:\n\t\treturn true"},
		{"open_select", "default:\n\t\treturn true", "default:\n\t\treturn false"},
		{"blocking_select", "default:\n\t\treturn true", ""},
	}
	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			changed := expiryFinalModelReplace(t, src, name, m.old, m.replacement)
			if _, _, err := expiryFinalModelFunction(changed, name); err != nil { t.Fatal(err) }
			if expiryFinalModelHelperValidate(changed) == nil { t.Fatal("helper mutant accepted") }
		})
	}
	// Existing actual helper only; zero gate's Done is the closed fallback.
	for i, r := range []*expiryHostRun{nil, {}, {gate: &sidecargapdb.ExpiryQualification{}}} {
		if expiryOriginalDoneOpen(r) { t.Fatalf("nil/zero gate %d reported open", i) }
	}
}
