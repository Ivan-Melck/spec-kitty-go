package architecture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CallerClass describes the production caller required for a selected symbol.
type CallerClass string

const (
	CallerExternalPackage       CallerClass = "external_package"
	CallerSamePackageProduction CallerClass = "same_package_production"
	CallerEitherProduction      CallerClass = "either_production"
)

// ReachabilityTarget is an intentionally small registry entry for an
// authority-critical guard or concrete port constructor.
type ReachabilityTarget struct {
	Package             string
	Symbol              string
	RequiredCallerClass CallerClass
}

// DefaultReachabilityTargets returns the curated production registry. It
// contains one authority guard and one concrete adapter composition seam.
func DefaultReachabilityTargets(modulePath string) []ReachabilityTarget {
	return []ReachabilityTarget{
		{
			Package: modulePath + "/internal/handoff", Symbol: "VerifyCandidateAuthority",
			RequiredCallerClass: CallerExternalPackage,
		},
		{
			Package: modulePath + "/internal/gitrepo", Symbol: "NewHandoffVerifier",
			RequiredCallerClass: CallerExternalPackage,
		},
	}
}

// ReachabilityViolation is a stable symbol-oriented diagnostic.
type ReachabilityViolation struct {
	Rule                RuleID
	Package             string
	Symbol              string
	RequiredCallerClass CallerClass
}

func (v ReachabilityViolation) Error() string {
	return fmt.Sprintf("rule=%s package=%s symbol=%s required_caller=%s", v.Rule, v.Package, v.Symbol, v.RequiredCallerClass)
}

type reachabilityViolations []ReachabilityViolation

func (v reachabilityViolations) Error() string {
	parts := make([]string, len(v))
	for index := range v {
		parts[index] = v[index].Error()
	}
	return strings.Join(parts, "\n")
}

type reachabilityObservation struct {
	declared bool
	same     bool
	external bool
}

type productionSourcePackage struct {
	importPath  string
	packageName string
	fileSet     *token.FileSet
	files       []*ast.File
	info        *types.Info
	typed       *types.Package
	checking    bool
}

type productionSourceImporter struct {
	packages map[string]*productionSourcePackage
	fallback types.Importer
}

type productionDependencyImporter struct {
	exports  map[string]string
	exported types.Importer
	source   types.Importer
}

func (i *productionDependencyImporter) Import(importPath string) (*types.Package, error) {
	if i.exports[importPath] != "" {
		return i.exported.Import(importPath)
	}
	return i.source.Import(importPath)
}

func (i *productionSourceImporter) Import(importPath string) (*types.Package, error) {
	if source := i.packages[importPath]; source != nil {
		return i.check(source)
	}
	return i.fallback.Import(importPath)
}

func (i *productionSourceImporter) check(source *productionSourcePackage) (*types.Package, error) {
	if source.typed != nil {
		return source.typed, nil
	}
	if source.checking {
		return nil, fmt.Errorf("production import cycle includes %s", source.importPath)
	}
	source.checking = true
	defer func() { source.checking = false }()
	source.info = &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	config := types.Config{Importer: i, Error: func(error) {}}
	checked, err := config.Check(source.importPath, source.fileSet, source.files, source.info)
	if err != nil {
		return nil, err
	}
	if checked == nil {
		return nil, fmt.Errorf("type-check production package %s", source.importPath)
	}
	source.typed = checked
	return checked, nil
}

func loadTypedProductionSources(ctx context.Context, root, modulePath string, run graphCommandRunner) ([]*productionSourcePackage, error) {
	ctx, cancel := context.WithTimeout(ctx, goListTimeout)
	defer cancel()

	fileSet := token.NewFileSet()
	byPath := make(map[string]*productionSourcePackage)
	const sourceListCommand = "go list -e -deps -json ./..."
	result, err := run(ctx, root, "list", "-e", "-deps", "-json", "./...")
	if err != nil {
		return nil, commandLoadError(sourceListCommand, result, err)
	}
	if result.overflow {
		return nil, commandLoadError(sourceListCommand, result, errors.New("bounded output exceeded"))
	}
	type listedPackage struct {
		Dir        string
		ImportPath string
		Imports    []string
		Name       string
		GoFiles    []string
		CgoFiles   []string
		Standard   bool
		Module     *struct {
			Path string
			Main bool
		}
	}
	listedByPath := make(map[string]listedPackage)
	mainImports := make(map[string]struct{})
	decoder := json.NewDecoder(bytes.NewReader(result.stdout))
	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, &LoadError{Kind: LoadInvalidOutput, Command: sourceListCommand, Cause: err}
		}
		listedByPath[listed.ImportPath] = listed
		if listed.Module == nil || !listed.Module.Main || listed.Module.Path != modulePath || !withinModule(modulePath, listed.ImportPath) {
			continue
		}
		for _, imported := range listed.Imports {
			mainImports[imported] = struct{}{}
		}
		files := append(append([]string(nil), listed.GoFiles...), listed.CgoFiles...)
		if len(files) == 0 {
			continue
		}
		source := &productionSourcePackage{
			importPath: listed.ImportPath, packageName: listed.Name, fileSet: fileSet,
		}
		for _, name := range files {
			filePath := filepath.Join(listed.Dir, name)
			parsed, err := parser.ParseFile(fileSet, filePath, nil, 0)
			if err != nil {
				return nil, &LoadError{Kind: LoadInvalidSource, Command: "parse production source " + filePath, Cause: err}
			}
			if parsed.Name.Name != listed.Name {
				return nil, &LoadError{
					Kind:    LoadInvalidSource,
					Command: "validate production package " + listed.ImportPath + " file " + filePath,
					Cause:   fmt.Errorf("package names %q and %q do not match", listed.Name, parsed.Name.Name),
				}
			}
			source.files = append(source.files, parsed)
		}
		byPath[listed.ImportPath] = source
	}
	packages := make([]*productionSourcePackage, 0, len(byPath))
	for _, source := range byPath {
		packages = append(packages, source)
	}
	sort.Slice(packages, func(left, right int) bool { return packages[left].importPath < packages[right].importPath })
	var exportImports []string
	hasExternalImport := false
	for imported := range mainImports {
		metadata, found := listedByPath[imported]
		if found && metadata.Module != nil && metadata.Module.Main {
			continue
		}
		exportImports = append(exportImports, imported)
		if !found || !metadata.Standard {
			hasExternalImport = true
		}
	}
	sort.Strings(exportImports)
	var fallback types.Importer = importer.ForCompiler(fileSet, "source", nil)
	if hasExternalImport {
		arguments := append([]string{"list", "-deps", "-export", "-json"}, exportImports...)
		exportCommand := "go " + strings.Join(arguments, " ")
		exportResult, err := run(ctx, root, arguments...)
		if err != nil {
			return nil, commandLoadError(exportCommand, exportResult, err)
		}
		if exportResult.overflow {
			return nil, commandLoadError(exportCommand, exportResult, errors.New("bounded output exceeded"))
		}
		exports := make(map[string]string)
		exportDecoder := json.NewDecoder(bytes.NewReader(exportResult.stdout))
		for {
			var listed struct {
				ImportPath string
				Export     string
			}
			if err := exportDecoder.Decode(&listed); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return nil, &LoadError{Kind: LoadInvalidOutput, Command: exportCommand, Cause: err}
			}
			if listed.Export != "" {
				exports[listed.ImportPath] = listed.Export
			}
		}
		exported := importer.ForCompiler(fileSet, "gc", importer.Lookup(func(importPath string) (io.ReadCloser, error) {
			exportPath := exports[importPath]
			if exportPath == "" {
				return nil, fmt.Errorf("Go tooling returned no export data for %s", importPath)
			}
			return os.Open(exportPath)
		}))
		fallback = &productionDependencyImporter{
			exports: exports, exported: exported, source: importer.ForCompiler(fileSet, "source", nil),
		}
	}
	checker := &productionSourceImporter{packages: byPath, fallback: fallback}
	for _, source := range packages {
		if _, err := checker.check(source); err != nil {
			return nil, &LoadError{Kind: LoadInvalidSource, Command: "type-check production package " + source.importPath, Cause: err}
		}
	}
	return packages, nil
}

func calledObject(expression ast.Expr, info *types.Info) types.Object {
	switch function := expression.(type) {
	case *ast.Ident:
		return info.Uses[function]
	case *ast.SelectorExpr:
		return info.Uses[function.Sel]
	default:
		return nil
	}
}

type packageCallGraph struct {
	edges map[types.Object]map[types.Object]struct{}
	roots map[types.Object]struct{}
}

func buildPackageCallGraph(source *productionSourcePackage) packageCallGraph {
	graph := packageCallGraph{
		edges: make(map[types.Object]map[types.Object]struct{}),
		roots: make(map[types.Object]struct{}),
	}
	for _, parsed := range source.files {
		ownedCalls := make(map[*ast.CallExpr]struct{})
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			caller := source.info.Defs[function.Name]
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ownedCalls[call] = struct{}{}
				called := calledObject(call.Fun, source.info)
				if caller == nil || called == nil || called.Pkg() == nil || called.Pkg().Path() != source.importPath {
					return true
				}
				if graph.edges[caller] == nil {
					graph.edges[caller] = make(map[types.Object]struct{})
				}
				graph.edges[caller][called] = struct{}{}
				return true
			})
		}
		// A function literal is a callable body, not package initialization.
		// Calls inside top-level closures are therefore not roots merely because
		// the literal appears in a var declaration. Calls in literals nested in a
		// FuncDecl are already attributed to that declaration above.
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.FuncLit)
			if !ok {
				return true
			}
			ast.Inspect(literal.Body, func(nested ast.Node) bool {
				if call, ok := nested.(*ast.CallExpr); ok {
					ownedCalls[call] = struct{}{}
				}
				return true
			})
			return false
		})
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if _, owned := ownedCalls[call]; owned {
				return true
			}
			called := calledObject(call.Fun, source.info)
			if called != nil && called.Pkg() != nil && called.Pkg().Path() == source.importPath {
				graph.roots[called] = struct{}{}
			}
			return true
		})
	}
	return graph
}

func (g packageCallGraph) reaches(start, target types.Object) bool {
	visited := make(map[types.Object]bool)
	pending := []types.Object{start}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current == target {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		for next := range g.edges[current] {
			pending = append(pending, next)
		}
	}
	return false
}

func (g packageCallGraph) hasCallerRootedOutsideTargetCycle(target types.Object) bool {
	component := make(map[types.Object]bool)
	component[target] = true
	for caller := range g.edges {
		if g.reaches(target, caller) && g.reaches(caller, target) {
			component[caller] = true
		}
	}
	for root := range g.roots {
		if component[root] {
			return true
		}
	}
	for caller, callees := range g.edges {
		if component[caller] {
			continue
		}
		for called := range callees {
			if component[called] {
				return true
			}
		}
	}
	return false
}

// CheckReachability parses active production Go files and verifies the curated
// targets. The caller context and an internal deadline bound Go tooling. Test
// files and files excluded from the active Go build never satisfy a target.
func CheckReachability(ctx context.Context, root, modulePath string, targets []ReachabilityTarget) error {
	if modulePath == "" {
		return fmt.Errorf("reachability module path is required")
	}
	ordered := append([]ReachabilityTarget(nil), targets...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Package != ordered[j].Package {
			return ordered[i].Package < ordered[j].Package
		}
		return ordered[i].Symbol < ordered[j].Symbol
	})
	observations := make(map[ReachabilityTarget]*reachabilityObservation, len(ordered))
	for _, target := range ordered {
		if !withinModule(modulePath, target.Package) || target.Symbol == "" || !token.IsIdentifier(target.Symbol) {
			return fmt.Errorf("invalid reachability target package=%q symbol=%q", target.Package, target.Symbol)
		}
		switch target.RequiredCallerClass {
		case CallerExternalPackage, CallerSamePackageProduction, CallerEitherProduction:
		default:
			return fmt.Errorf("invalid reachability caller class %q", target.RequiredCallerClass)
		}
		observations[target] = &reachabilityObservation{}
	}
	packages, err := loadTypedProductionSources(ctx, root, modulePath, runGraphCommand)
	if err != nil {
		return err
	}
	objects := make(map[types.Object]ReachabilityTarget, len(ordered))
	for _, source := range packages {
		for _, target := range ordered {
			if source.importPath != target.Package {
				continue
			}
			function, ok := source.typed.Scope().Lookup(target.Symbol).(*types.Func)
			if !ok {
				continue
			}
			signature, ok := function.Type().(*types.Signature)
			if !ok || signature.Recv() != nil {
				continue
			}
			observations[target].declared = true
			objects[function] = target
		}
	}
	callGraphs := make(map[string]packageCallGraph, len(packages))
	for _, source := range packages {
		callGraphs[source.importPath] = buildPackageCallGraph(source)
	}
	for object, target := range objects {
		if callGraphs[target.Package].hasCallerRootedOutsideTargetCycle(object) {
			observations[target].same = true
		}
	}
	for _, source := range packages {
		for _, parsed := range source.files {
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				called := calledObject(call.Fun, source.info)
				target, ok := objects[called]
				if !ok {
					return true
				}
				if source.importPath != target.Package {
					observations[target].external = true
				}
				return true
			})
		}
	}

	violations := make(reachabilityViolations, 0)
	for _, target := range ordered {
		observation := observations[target]
		if !observation.declared {
			violations = append(violations, ReachabilityViolation{Rule: RuleReachabilityTarget, Package: target.Package, Symbol: target.Symbol, RequiredCallerClass: target.RequiredCallerClass})
			continue
		}
		reachable := (target.RequiredCallerClass == CallerExternalPackage && observation.external) ||
			(target.RequiredCallerClass == CallerSamePackageProduction && observation.same) ||
			(target.RequiredCallerClass == CallerEitherProduction && (observation.external || observation.same))
		if !reachable {
			violations = append(violations, ReachabilityViolation{Rule: RuleReachability, Package: target.Package, Symbol: target.Symbol, RequiredCallerClass: target.RequiredCallerClass})
		}
	}
	if len(violations) > 0 {
		return violations
	}
	return nil
}

// CheckDeferredRuntimeSurfaces rejects production code that launches a Python
// interpreter. The caller context and an internal deadline bound Go tooling.
// Import and package-based deferred surfaces are enforced by Policy; this
// targeted AST check covers the runtime dependency that an import graph cannot
// represent without returning to broad textual name matching.
func CheckDeferredRuntimeSurfaces(ctx context.Context, root, modulePath string) error {
	if modulePath == "" {
		return fmt.Errorf("deferred runtime module path is required")
	}
	seen := make(map[Violation]bool)
	packages, err := loadTypedProductionSources(ctx, root, modulePath, runGraphCommand)
	if err != nil {
		return err
	}
	for _, source := range packages {
		bindings := make(map[types.Object]ast.Expr)
		for _, parsed := range source.files {
			ast.Inspect(parsed, func(node ast.Node) bool {
				switch declaration := node.(type) {
				case *ast.ValueSpec:
					if len(declaration.Names) != len(declaration.Values) {
						return true
					}
					for index, name := range declaration.Names {
						if object := source.info.Defs[name]; object != nil {
							bindings[object] = declaration.Values[index]
						}
					}
				case *ast.AssignStmt:
					if declaration.Tok != token.DEFINE || len(declaration.Lhs) != len(declaration.Rhs) {
						return true
					}
					for index, left := range declaration.Lhs {
						name, ok := left.(*ast.Ident)
						if !ok {
							continue
						}
						if object := source.info.Defs[name]; object != nil {
							bindings[object] = declaration.Rhs[index]
						}
					}
				}
				return true
			})
		}
		for _, parsed := range source.files {
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				function, ok := calledObject(call.Fun, source.info).(*types.Func)
				if !ok || function.Pkg() == nil || function.Pkg().Path() != "os/exec" {
					return true
				}
				commandFunction := function.Name()
				if commandFunction != "Command" && commandFunction != "CommandContext" {
					return true
				}
				argumentIndex := 0
				if commandFunction == "CommandContext" {
					argumentIndex = 1
				}
				if len(call.Args) <= argumentIndex {
					return true
				}
				command, ok := staticStringValue(call.Args[argumentIndex], source.info, bindings, make(map[types.Object]bool))
				if !ok {
					// Runtime-computed command strings are deliberately outside this
					// static proof boundary. The guard proves literals and immutable
					// package/local binding chains without treating arbitrary exec
					// calls as Python merely because their value is dynamic.
					return true
				}
				if !isPythonRuntime(command) {
					return true
				}
				seen[Violation{Rule: RuleDeferredSurface, SourcePackage: source.importPath, TargetPackage: "runtime:" + command}] = true
				return true
			})
		}
	}
	violations := make(Violations, 0, len(seen))
	for violation := range seen {
		violations = append(violations, violation)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].SourcePackage != violations[j].SourcePackage {
			return violations[i].SourcePackage < violations[j].SourcePackage
		}
		return violations[i].TargetPackage < violations[j].TargetPackage
	})
	if len(violations) > 0 {
		return violations
	}
	return nil
}

func staticStringValue(expression ast.Expr, info *types.Info, bindings map[types.Object]ast.Expr, visiting map[types.Object]bool) (string, bool) {
	if value := info.Types[expression].Value; value != nil && value.Kind() == constant.String {
		return constant.StringVal(value), true
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return "", false
	}
	object := info.Uses[identifier]
	if object == nil {
		object = info.Defs[identifier]
	}
	bound, ok := bindings[object]
	if !ok || visiting[object] {
		return "", false
	}
	visiting[object] = true
	defer delete(visiting, object)
	return staticStringValue(bound, info, bindings, visiting)
}

func isPythonRuntime(command string) bool {
	name := strings.ToLower(filepath.Base(command))
	return name == "python" || name == "python2" || name == "python3" ||
		name == "pypy" || name == "pypy2" || name == "pypy3" ||
		strings.HasPrefix(name, "python2.") || strings.HasPrefix(name, "python3.") ||
		strings.HasPrefix(name, "pypy2.") || strings.HasPrefix(name, "pypy3.")
}
