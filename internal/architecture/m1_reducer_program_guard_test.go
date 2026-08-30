package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The playback boundary is zero-I/O only if callers cannot smuggle executable
// code into the reducer registry. This guard deliberately freezes the M1
// registry input to a closed data-only instruction. A future reducer behavior
// must therefore add an audited declarative opcode instead of a callback.
func TestM1ReducerProgramsAreClosedData(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "controljournal", "reducer", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	sources := make(map[string][]byte)
	for _, path := range paths {
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[path] = source
	}
	if violations := inspectM1ReducerProgramSurface(sources); len(violations) != 0 {
		t.Fatalf("reducer program surface admits executable caller code: %v", violations)
	}
}

func TestM1ReducerProgramMutantControls(t *testing.T) {
	base := `package reducer
type AggregateKind string
type EventType string
type ReducerVersion string
type ReducerProgramV1 string
type EntryV1 struct {
	AggregateKind AggregateKind
	EventType EventType
	EventSchemaVersion uint32
	ReducerVersion ReducerVersion
	Program ReducerProgramV1
}
type RegistryV1 struct{}
func NewRegistryV1(entries []EntryV1) (RegistryV1, error) { return RegistryV1{}, nil }
`
	if violations := inspectM1ReducerProgramSurface(map[string][]byte{"reducer.go": []byte(base)}); len(violations) != 0 {
		t.Fatalf("clean closed reducer fixture rejected: %v", violations)
	}

	mutants := map[string]string{
		"direct callback field": `type EntryV1 struct {
	AggregateKind AggregateKind; EventType EventType; EventSchemaVersion uint32
	ReducerVersion ReducerVersion; Program ReducerProgramV1; Reduce func()
}`,
		"opaque executable field": `type EntryV1 struct {
	AggregateKind AggregateKind; EventType EventType; EventSchemaVersion uint32
	ReducerVersion ReducerVersion; Program ReducerProgramV1; Reduce any
}`,
		"constructor callback": `func NewRegistryV1(entries []EntryV1, reduce func()) (RegistryV1, error) { return RegistryV1{}, nil }`,
	}
	for name, replacement := range mutants {
		t.Run(name, func(t *testing.T) {
			mutant := base
			switch name {
			case "constructor callback":
				mutant = replaceDeclaration(mutant, "func NewRegistryV1", replacement)
			default:
				mutant = replaceDeclaration(mutant, "type EntryV1", replacement)
			}
			if violations := inspectM1ReducerProgramSurface(map[string][]byte{"reducer.go": []byte(mutant)}); len(violations) == 0 {
				t.Fatal("executable reducer mutant survived the closed-program guard")
			}
		})
	}
}

func replaceDeclaration(source, prefix, replacement string) string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", source, 0)
	if err != nil {
		panic(err)
	}
	for _, declaration := range file.Decls {
		start := fset.Position(declaration.Pos()).Offset
		end := fset.Position(declaration.End()).Offset
		if start >= 0 && end <= len(source) && len(source[start:end]) >= len(prefix) && source[start:start+len(prefix)] == prefix {
			return source[:start] + replacement + source[end:]
		}
	}
	panic("fixture declaration not found: " + prefix)
}

func inspectM1ReducerProgramSurface(sources map[string][]byte) []string {
	var violations []string
	entryFound := false
	programFound := false
	constructorFound := false
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, sources[path], 0)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: parse: %v", path, err))
			continue
		}
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, callback := typeSpec.Type.(*ast.FuncType); callback {
						violations = append(violations, path+": named callback type "+typeSpec.Name.Name)
					}
					if typeSpec.Name.Name == "ReducerProgramV1" {
						programFound = true
						if identifier, ok := typeSpec.Type.(*ast.Ident); !ok || identifier.Name != "string" {
							violations = append(violations, path+": ReducerProgramV1 must remain string data")
						}
					}
					if typeSpec.Name.Name == "EntryV1" {
						entryFound = true
						violations = append(violations, inspectM1ReducerEntry(path, typeSpec.Type)...)
					}
				}
			case *ast.FuncDecl:
				if declaration.Name.Name == "NewRegistryV1" {
					constructorFound = true
					if !isM1ClosedRegistryConstructor(declaration.Type) {
						violations = append(violations, path+": NewRegistryV1 accepts more than closed EntryV1 data")
					}
				}
			}
		}
	}
	if !entryFound {
		violations = append(violations, "EntryV1 declaration missing")
	}
	if !programFound {
		violations = append(violations, "ReducerProgramV1 declaration missing")
	}
	if !constructorFound {
		violations = append(violations, "NewRegistryV1 declaration missing")
	}
	return violations
}

func inspectM1ReducerEntry(path string, expression ast.Expr) []string {
	structure, ok := expression.(*ast.StructType)
	if !ok {
		return []string{path + ": EntryV1 is not a closed struct"}
	}
	expected := []struct{ name, selector string }{
		{"AggregateKind", "AggregateKind"},
		{"EventType", "EventType"},
		{"EventSchemaVersion", "uint32"},
		{"ReducerVersion", "ReducerVersion"},
		{"Program", "ReducerProgramV1"},
	}
	if len(structure.Fields.List) != len(expected) {
		return []string{path + ": EntryV1 field set changed"}
	}
	var violations []string
	for index, field := range structure.Fields.List {
		if len(field.Names) != 1 || field.Names[0].Name != expected[index].name || m1TypeTail(field.Type) != expected[index].selector {
			violations = append(violations, path+": EntryV1 is not the frozen data-only shape")
			break
		}
	}
	return violations
}

func m1TypeTail(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expression.Sel.Name
	default:
		return ""
	}
}

func isM1ClosedRegistryConstructor(function *ast.FuncType) bool {
	if function.Params == nil || len(function.Params.List) != 1 || function.Results == nil || len(function.Results.List) != 2 {
		return false
	}
	parameter := function.Params.List[0]
	list, ok := parameter.Type.(*ast.ArrayType)
	if !ok || list.Len != nil || m1TypeTail(list.Elt) != "EntryV1" {
		return false
	}
	return m1TypeTail(function.Results.List[0].Type) == "RegistryV1" && m1TypeTail(function.Results.List[1].Type) == "error"
}
