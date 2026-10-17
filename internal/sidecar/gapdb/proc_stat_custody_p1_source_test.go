//go:build linux && amd64

package gapdb

// AUTHORED UNRUN strict contracts on ACTUAL selected package inputs. No cache
// fallback, comment/token proxy, emitter or independently asserted donor body.
import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"testing"
)

func procStatP1Selected(t *testing.T, name string) ([]byte, *token.FileSet, *ast.File) {
	t.Helper()
	switch name {
	case "lifecycle.go", "startup_custody.go", "expiry_qualification_work.go", "expiry_qualification_disabled.go", "proxy.go", "expiry_qualification_launch.go":
	default: t.Fatal("not an actual selected package source", name)
	}
	raw, err := os.ReadFile(name) // selected directory only, never cached *.txt
	if err != nil { t.Fatal(err) }
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, name, raw, parser.SkipObjectResolution)
	if err != nil { t.Fatal(err) }
	if file.Name.Name != "gapdb" { t.Fatal("wrong selected package", name) }
	return raw, set, file
}

func procStatP1SelectedPin(t *testing.T, name, want string) {
	t.Helper()
	raw, _, _ := procStatP1Selected(t, name)
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want { t.Fatal("selected dependency differs", name, got) }
}

func procStatP1Calls(node ast.Node, receiver, method string) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr); if !ok { return true }
		if receiver == "" {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == method { count++ }
		} else if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == receiver { count++ }
		}
		return true
	})
	return count
}

func procStatP1Function(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	var found *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			if found != nil { t.Fatal("duplicate selected function", name) }
			found = fn
		}
	}
	if found == nil { t.Fatal("selected function missing", name) }
	return found
}

func procStatP1AST(t *testing.T, node ast.Node) string {
	t.Helper()
	// Erase source positions before canonical printing: same AST on different
	// lines (or with one-line blocks) must compare identically. Comments are
	// not parsed. No identifiers/operators/literals/structure are erased.
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil { return true }
		value := reflect.ValueOf(n).Elem()
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			if field.CanSet() && field.Type() == reflect.TypeOf(token.Pos(0)) { field.SetInt(0) }
		}
		return true
	})
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), node); err != nil { t.Fatal(err) }
	return out.String()
}

func procStatP1Expected(t *testing.T, source, name string) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "contract.go", "package gapdb\n"+source, parser.SkipObjectResolution)
	if err != nil { t.Fatal(err) }
	return procStatP1Function(t, file, name)
}

func procStatP1FunctionEquals(t *testing.T, path, name, expected string) {
	t.Helper()
	_, _, selected := procStatP1Selected(t, path)
	got := procStatP1AST(t, procStatP1Function(t, selected, name))
	want := procStatP1AST(t, procStatP1Expected(t, expected, name))
	if got != want { t.Fatalf("actual selected AST differs: %s.%s\ngot:\n%s\nwant:\n%s", path, name, got, want) }
}

func TestProcStatP1ActualSelectedLiteralCallChain(t *testing.T) {
	procStatP1FunctionEquals(t, "lifecycle.go", "processStartTicks", `func processStartTicks(pid int) (uint64, error) {
		return processStartTicksWithReport(pid, nil)
	}`)
	procStatP1FunctionEquals(t, "lifecycle.go", "processStartTicksWithReport", `func processStartTicksWithReport(pid int, report *expiryCleanupReport) (uint64, error) {
		file, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil { return 0, err }
		return processStartTicksFileWithReport(file, report)
	}`)
	procStatP1FunctionEquals(t, "lifecycle.go", "processStartTicksFileWithReport", `func processStartTicksFileWithReport(file *os.File, report *expiryCleanupReport) (uint64, error) {
		return processStartTicksFileWithClose(file, report, (*expiryCleanupReport).closeLocalFile)
	}`)
	procStatP1FunctionEquals(t, "lifecycle.go", "processStartTicksFileWithClose", `func processStartTicksFileWithClose(file *os.File, report *expiryCleanupReport, closeCall func(*expiryCleanupReport, *os.File) error) (ticks uint64, resultErr error) {
		report.localOpened()
		defer func() {
			resultErr = withStartupCustody(resultErr, closeCall(report, file))
			if resultErr != nil { ticks = 0 }
		}()
		encoded, err := io.ReadAll(file)
		if err != nil { return 0, err }
		closeIndex := bytes.LastIndexByte(encoded, ')')
		if closeIndex < 0 || closeIndex+2 >= len(encoded) { return 0, errors.New("process stat is malformed") }
		fields := strings.Fields(string(encoded[closeIndex+2:]))
		if len(fields) <= 19 { return 0, errors.New("process stat lacks start time") }
		value, err := strconv.ParseUint(fields[19], 10, 64)
		if err != nil || value == 0 { return 0, errors.New("process start time is invalid") }
		return value, nil
	}`)
	procStatP1FunctionEquals(t, "startup_custody.go", "withStartupCustody", `func withStartupCustody(primary, custody error) error {
		if custody == nil { return primary }
		if primary == nil { return custody }
		return &startupCustodyError{primary: primary, custody: custody}
	}`)
}

func TestProcStatP1ActualSelectedDefaultBindingAST(t *testing.T) {
	_, _, selected := procStatP1Selected(t, "lifecycle.go")
	normalize := procStatP1Function(t, selected, "normalizeLifecycleConfigWithReport")
	want := procStatP1Expected(t, `func binding() {
		if config.processes.startTicks == nil {
			config.processes = systemProcessAPI
			if report != nil {
				config.processes.startTicks = func(pid int) (uint64, error) { return processStartTicksWithReport(pid, report) }
				config.processes.executable = func(pid int) (fileIdentity, string, error) { return processExecutableWithReport(pid, report) }
				config.processes.executableIdentity = func(pid int) (fileIdentity, error) { return processExecutableIdentityWithReport(pid, report) }
			}
		} else if config.processes.executableIdentity == nil {
			config.processes.executableIdentity = systemProcessAPI.executableIdentity
			if report != nil {
				config.processes.executableIdentity = func(pid int) (fileIdentity, error) { return processExecutableIdentityWithReport(pid, report) }
			}
		}
	}`, "binding").Body.List[0]
	matches := 0
	for _, stmt := range normalize.Body.List {
		if procStatP1AST(t, stmt) == procStatP1AST(t, want) { matches++ }
	}
	if matches != 1 { t.Fatal("actual default/custom/report binding is not exact", matches) }
	defaults := 0
	for _, decl := range selected.Decls {
		g, ok := decl.(*ast.GenDecl); if !ok { continue }
		for _, spec := range g.Specs {
			v, ok := spec.(*ast.ValueSpec); if !ok { continue }
			for _, name := range v.Names {
				if name.Name != "systemProcessAPI" { continue }
				defaults++
				if len(v.Values) != 1 { t.Fatal("system defaults not literal") }
				literal, ok := v.Values[0].(*ast.CompositeLit)
				if !ok { t.Fatal("system defaults not composite literal") }
				fields := 0
				for _, element := range literal.Elts {
					kv, ok := element.(*ast.KeyValueExpr); if !ok { t.Fatal("unkeyed system default") }
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "startTicks" {
						fields++
						id, ok := kv.Value.(*ast.Ident)
						if !ok || id.Name != "processStartTicks" { t.Fatal("ordinary system default rerouted") }
					}
				}
				if fields != 1 { t.Fatal("system startTicks cardinality", fields) }
			}
		}
	}
	if defaults != 1 { t.Fatal("system defaults declaration cardinality", defaults) }
}

func TestProcStatP1ActualSelectedCallerReachability(t *testing.T) {
	procStatP1SelectedPin(t, "startup_custody.go", "c3b7414078c7a3bcc19bfb891178906cb8d6fb707418d013f766bc951e4f8bef")
	procStatP1SelectedPin(t, "proxy.go", "c7f573882fe840931871263bc132886320ab788b22617269fab2ad2c07ce345c")
	_, _, lifecycle := procStatP1Selected(t, "lifecycle.go")
	for name, want := range map[string]int{
		"observeStartedProcessWithCustody": 2,
		"InspectOwnership": 1,
		"inspectOwnershipLocked": 1,
		"monitorRecordedProcess": 1,
	} {
		if got := procStatP1Calls(procStatP1Function(t, lifecycle, name), "processes", "startTicks"); got != want { t.Fatal("actual processAPI caller detached", name, got) }
	}
	stale := procStatP1Function(t, lifecycle, "cleanupStaleBefore")
	if procStatP1Calls(stale, "l", "inspectOwnershipLocked") != 1 || procStatP1Calls(stale, "l", "finishExitedShutdown") != 1 { t.Fatal("original stale ownership refusal route detached") }
	_, _, proxy := procStatP1Selected(t, "proxy.go")
	if procStatP1Calls(procStatP1Function(t, proxy, "validateProcessIdentityWith"), "processes", "startTicks") != 1 { t.Fatal("strict unary/proxy processAPI validation detached") }
	// Disclose and freeze inherited direct nil-report probe route. It now
	// returns actual close errors but is NOT newly report-bound by this delta.
	probe := procStatP1Function(t, proxy, "currentExecutableIdentityWithReport")
	if procStatP1Calls(probe, "", "processStartTicks") != 1 || procStatP1Calls(probe, "", "processStartTicksWithReport") != 0 { t.Fatal("proxy probe changed outside bounded lifecycle delta") }
}

func TestProcStatP1ActualSelectedFrozenBytes(t *testing.T) {
	raw, set, selected := procStatP1Selected(t, "lifecycle.go")
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != "1557bdf57df09e2dc534cb5a50086cec18739130640dc3b426b7d4c065670e45" { t.Fatal("not exact prospective P1 selected lifecycle", got) }
	// Full A4 FUNCTION bytes, not the entire suffix. Proc-stat is in that
	// suffix, so an unchanged-suffix claim would be false.
	pins := map[string]string{
		"observeStrictUnary": "028dc7a67de377a294f87002fbe79501ba172b1c5d64d2b576b1c3af632c0c4e",
		"observeStrictUnaryWithReport": "25ec250e3792fd04bdca65ff0f28466973eebd94f7c9269caba70cd6e061aed1",
		"observeStrictUnaryWithClose": "2b0065cefb72985f101752ce165d262060af4b4f7cefcdffadaf47b854d54cb0",
		"strictRequireUnaryEOF": "88c63b76d40fd4a448fcf43e143ab983a3f89b6aaa9ea8f9683814d5f3b74173",
		"processExecutable": "29a24f2bc699a7d99188fd174084bfc9d42b936ec08f8fbe6bc52770db7e6f44",
		"processExecutableWithReport": "7a143e42b7164883221c5627d0ae4afd470a6a6d7d5c7728518410c285a86a74",
		"processExecutableFileWithReport": "78eee675555d3a1af22db9009bb9c1c8faf3eda708a4342eb0f2e29f719a302b",
		"processExecutableIdentity": "fe55e6713282f346e0d189fa2cdbabac55bfd624b6db2450dd22fccb3241bdde",
		"processExecutableIdentityWithReport": "e86f4f07e67e438a8431ac8e3b98f3fd768f981147f1132880bb21a1253b6859",
	}
	for name, want := range pins {
		fn := procStatP1Function(t, selected, name)
		body := raw[set.Position(fn.Pos()).Offset:set.Position(fn.End()).Offset]
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != want { t.Fatal("protected actual function bytes changed", name, got) }
	}
}
