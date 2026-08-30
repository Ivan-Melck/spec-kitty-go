package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const m1GapDBModule = "github.com/spec-kitty/gapdb"

var m1GapDBImportAllowlist = map[string]bool{
	"internal/controlstore/gapdb": true,
	"internal/sidecar/gapdb":      true,
}

var m1ReducerImportAllowlist = map[string]bool{
	"bytes":           true,
	"crypto/sha256":   true,
	"encoding/binary": true,
	"encoding/hex":    true,
	"encoding/json":   true,
	"errors":          true,
	"fmt":             true,
	"maps":            true,
	"slices":          true,
	"sort":            true,
	"strconv":         true,
	"strings":         true,
	"unicode":         true,
	"unicode/utf8":    true,
	"github.com/spec-kitty/go-kitty3/internal/controljournal": true,
}

var m1SourceRoots = []string{
	"internal/controljournal",
	"internal/controlstore/gapdb",
	"internal/sidecar/gapdb",
	"internal/provingground",
	"internal/testcampaign/m1",
	"cmd/spk-m1-proving-ground",
}

// These exact identifiers are the frozen source-level constructions forbidden
// by plan.md sections 2.3, 4, and 8.3. The rule is intentionally structural:
// comments and string literals do not match, and historical packages outside
// the M1 roots remain comparison material rather than being reclassified.
var m1ForbiddenIdentifiers = map[string]string{
	"SQLiteFallback":              "sqlite-fallback",
	"UseSQLiteFallback":           "sqlite-fallback",
	"FallbackToSQLite":            "sqlite-fallback",
	"CurrentBatchRevision":        "current-batch-revision",
	"PredictedBatchRevision":      "current-batch-revision",
	"CurrentCommitRevision":       "current-batch-revision",
	"GlobalEpochCAS":              "global-authority-cas",
	"CompareAndSwapGlobalEpoch":   "global-authority-cas",
	"SharedProjectionHead":        "shared-projection-head",
	"GlobalProjectionHead":        "shared-projection-head",
	"LeaseGenerationRuntimeEpoch": "runtime-coordinate-conflation",
	"FenceTokenRuntimeEpoch":      "runtime-coordinate-conflation",
}

type m1SourceFile struct {
	Path   string
	Source []byte
}

type m1ArchitectureViolation struct {
	Path   string
	Kind   string
	Detail string
}

func (v m1ArchitectureViolation) String() string {
	return fmt.Sprintf("%s: %s: %s", v.Path, v.Kind, v.Detail)
}

func TestM1DependencyBoundaries(t *testing.T) {
	files := collectM1RepositorySources(t)
	violations := inspectM1Sources(files)
	if len(violations) != 0 {
		messages := make([]string, len(violations))
		for index, violation := range violations {
			messages[index] = violation.String()
		}
		t.Fatalf("M1 architecture boundary violations:\n%s", strings.Join(messages, "\n"))
	}
}

func TestM1DependencyMutantControls(t *testing.T) {
	clean := []m1SourceFile{
		{Path: "internal/controlstore/gapdb/client.go", Source: []byte("package gapdb\nimport _ \"github.com/spec-kitty/gapdb\"\n")},
		{Path: "internal/controljournal/model.go", Source: []byte("package controljournal\n// CurrentBatchRevision and internal/server are documentation only.\nvar note = \"sqlite fallback\"\n")},
		{Path: "internal/controlstore/sqlite/store.go", Source: []byte("package sqlite\nimport _ \"modernc.org/sqlite\"\n")},
	}
	if violations := inspectM1Sources(clean); len(violations) != 0 {
		t.Fatalf("clean controls rejected: %v", violations)
	}

	tests := []struct {
		id   string
		file m1SourceFile
		kind string
	}{
		{
			id:   "gapdb-import-allowlist-widening",
			file: m1SourceFile{Path: "internal/controljournal/client.go", Source: []byte("package controljournal\nimport _ \"github.com/spec-kitty/gapdb\"\n")},
			kind: "gapdb-import-allowlist",
		},
		{
			id:   "gapdb-import-neutral-package",
			file: m1SourceFile{Path: "internal/neutral/client_test.go", Source: []byte("package neutral\nimport _ \"github.com/spec-kitty/gapdb/client\"\n")},
			kind: "gapdb-import-allowlist",
		},
		{
			id:   "gapdb-internal-server",
			file: m1SourceFile{Path: "internal/sidecar/gapdb/server.go", Source: []byte("package gapdb\nimport _ \"github.com/spec-kitty/gapdb/internal/server\"\n")},
			kind: "internal-server-import",
		},
		{
			id:   "sqlite-fallback",
			file: m1SourceFile{Path: "internal/provingground/runtime.go", Source: []byte("package provingground\nimport _ \"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite\"\nvar UseSQLiteFallback bool\n")},
			kind: "sqlite-import",
		},
		{
			id:   "current-batch-revision-fill-in",
			file: m1SourceFile{Path: "internal/controljournal/application/apply.go", Source: []byte("package application\nvar CurrentBatchRevision uint64\n")},
			kind: "current-batch-revision",
		},
		{
			id:   "global-authority-cas",
			file: m1SourceFile{Path: "internal/controljournal/application/apply.go", Source: []byte("package application\nfunc CompareAndSwapGlobalEpoch() {}\n")},
			kind: "global-authority-cas",
		},
		{
			id:   "shared-projection-head",
			file: m1SourceFile{Path: "internal/controljournal/reducer/reduce.go", Source: []byte("package reducer\ntype SharedProjectionHead struct{}\n")},
			kind: "shared-projection-head",
		},
		{
			id:   "runtime-coordinate-conflation",
			file: m1SourceFile{Path: "internal/controljournal/model.go", Source: []byte("package controljournal\nvar FenceTokenRuntimeEpoch string\n")},
			kind: "runtime-coordinate-conflation",
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			violations := inspectM1Sources([]m1SourceFile{test.file})
			if !containsM1Violation(violations, test.kind) {
				t.Fatalf("mutant survived %s guard: %v", test.kind, violations)
			}
		})
	}
}

func TestM1DependencyReducerPurityMutants(t *testing.T) {
	clean := m1SourceFile{
		Path:   "internal/controljournal/reducer/reducer.go",
		Source: []byte("package reducer\nimport (\"crypto/sha256\"; \"encoding/json\"; \"github.com/spec-kitty/go-kitty3/internal/controljournal\")\nvar _ = sha256.Sum256\nvar _ = json.Valid\nvar _ controljournal.EventID\n"),
	}
	if violations := inspectM1Sources([]m1SourceFile{clean}); len(violations) != 0 {
		t.Fatalf("clean pure reducer control rejected: %v", violations)
	}
	for _, imported := range []string{
		"os",
		"time",
		"math/rand",
		"net/http",
		"github.com/spec-kitty/go-kitty3/internal/gitrepo",
		"github.com/spec-kitty/go-kitty3/internal/tracker",
		"github.com/spec-kitty/go-kitty3/internal/artifact",
		"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb",
	} {
		t.Run(imported, func(t *testing.T) {
			mutant := m1SourceFile{
				Path:   "internal/controljournal/reducer/reducer.go",
				Source: []byte("package reducer\nimport _ \"" + imported + "\"\n"),
			}
			if violations := inspectM1Sources([]m1SourceFile{mutant}); !containsM1Violation(violations, "reducer-impure-import") {
				t.Fatalf("pure reducer admitted forbidden import %q: %v", imported, violations)
			}
		})
	}
}

func TestM1DependencyJournalContractMutants(t *testing.T) {
	clean := []m1SourceFile{
		{
			Path: "internal/controljournal/model.go",
			Source: []byte(`package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string; OperationID string; EventType string; EnvelopeVersion uint32; EventSchemaVersion uint32; ReducerVersion string; EventCount uint32; CanonicalEventDigest string }
`),
		},
		{
			Path: "internal/controljournal/validation.go",
			Source: []byte(`package controljournal
func CompareCommittedEventOrder(left, right CommittedEvent) int {
	if left.RecordRevision < right.RecordRevision { return -1 }
	if left.EventIndex < right.EventIndex { return -1 }
	if left.EventID < right.EventID { return -1 }
	return 0
}
func ValidateCommittedEventGroup(event CommittedEvent) error {
	if event.EnvelopeVersion != 1 || event.EventType == "" || event.EventSchemaVersion != 1 || event.ReducerVersion != "v1" { return errInvalid }
	if !SupportedEventVersion(event.EventType, event.EventSchemaVersion, event.ReducerVersion) { return errInvalid }
	if event.RecordRevision == 0 || event.OperationID == "" || event.EventIndex >= event.EventCount || event.EventID == "" { return errInvalid }
	if !CanonicalEventDigestMatches(event.CanonicalEventDigest) { return errInvalid }
	return nil
}
func SupportedEventVersion(eventType string, eventVersion uint32, reducerVersion string) bool { return eventType != "" && eventVersion == 1 && reducerVersion == "v1" }
func CanonicalEventDigestMatches(digest string) bool { return digest != "" }
var errInvalid error
`),
		},
	}
	if violations := inspectM1Sources(clean); len(violations) != 0 {
		t.Fatalf("clean production journal seams rejected: %v", violations)
	}
	tests := []struct {
		id     string
		source string
		kind   string
	}{
		{
			id: "invalid-event-order/event-id-only",
			source: `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string }
func CompareCommittedEventOrder(left, right CommittedEvent) int { if left.EventID < right.EventID { return -1 }; return 0 }
`,
			kind: "event-order-seam",
		},
		{
			id: "invalid-event-order/coordinates-read-but-not-compared",
			source: `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string }
func CompareCommittedEventOrder(left, right CommittedEvent) int { _, _, _ = left.RecordRevision, left.EventIndex, left.EventID; if left.EventID < right.EventID { return -1 }; return 0 }
`,
			kind: "event-order-seam",
		},
		{
			id: "invalid-event-order/wrong-coordinate-precedence",
			source: `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string }
func CompareCommittedEventOrder(left, right CommittedEvent) int { if left.EventID < right.EventID { return -1 }; if left.RecordRevision < right.RecordRevision { return -1 }; if left.EventIndex < right.EventIndex { return -1 }; return 0 }
`,
			kind: "event-order-seam",
		},
		{
			id: "version-refusal/missing-version-checks",
			source: `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string; OperationID string; EventCount uint32; CanonicalEventDigest string }
func CompareCommittedEventOrder(left, right CommittedEvent) int { if left.RecordRevision < right.RecordRevision { return -1 }; if left.EventIndex < right.EventIndex { return -1 }; if left.EventID < right.EventID { return -1 }; return 0 }
func ValidateCommittedEventGroup(event CommittedEvent) error { if event.RecordRevision == 0 || event.OperationID == "" || event.EventIndex >= event.EventCount || event.EventID == "" || event.CanonicalEventDigest == "" { return errInvalid }; return nil }
var errInvalid error
`,
			kind: "event-version-refusal-seam",
		},
		{
			id: "version-refusal/unknown-versions-admitted",
			source: `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string; OperationID string; EventType string; EnvelopeVersion uint32; EventSchemaVersion uint32; ReducerVersion string; EventCount uint32; CanonicalEventDigest string }
func CompareCommittedEventOrder(left, right CommittedEvent) int { if left.RecordRevision < right.RecordRevision { return -1 }; if left.EventIndex < right.EventIndex { return -1 }; if left.EventID < right.EventID { return -1 }; return 0 }
func ValidateCommittedEventGroup(event CommittedEvent) error { if event.EnvelopeVersion == 0 || event.EventType == "" || event.EventSchemaVersion == 0 || event.ReducerVersion == "" || event.RecordRevision == 0 || event.OperationID == "" || event.EventIndex >= event.EventCount || event.EventID == "" { return errInvalid }; if !CanonicalEventDigestMatches(event.CanonicalEventDigest) { return errInvalid }; return nil }
func CanonicalEventDigestMatches(digest string) bool { return digest != "" }
var errInvalid error
`,
			kind: "event-version-refusal-seam",
		},
		{
			id: "invalid-event-order/malformed-group-unchecked",
			source: `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string; OperationID string; EventType string; EnvelopeVersion uint32; EventSchemaVersion uint32; ReducerVersion string; CanonicalEventDigest string }
func CompareCommittedEventOrder(left, right CommittedEvent) int { if left.RecordRevision < right.RecordRevision { return -1 }; if left.EventIndex < right.EventIndex { return -1 }; if left.EventID < right.EventID { return -1 }; return 0 }
func ValidateCommittedEventGroup(event CommittedEvent) error { if event.EnvelopeVersion != 1 || event.EventType == "" || event.EventSchemaVersion != 1 || event.ReducerVersion != "v1" || event.CanonicalEventDigest == "" { return errInvalid }; return nil }
var errInvalid error
`,
			kind: "event-group-refusal-seam",
		},
		{
			id: "version-refusal/digest-unchecked",
			source: `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string; OperationID string; EventType string; EnvelopeVersion uint32; EventSchemaVersion uint32; ReducerVersion string; EventCount uint32 }
func CompareCommittedEventOrder(left, right CommittedEvent) int { if left.RecordRevision < right.RecordRevision { return -1 }; if left.EventIndex < right.EventIndex { return -1 }; if left.EventID < right.EventID { return -1 }; return 0 }
func ValidateCommittedEventGroup(event CommittedEvent) error { if event.EnvelopeVersion != 1 || event.EventType == "" || event.EventSchemaVersion != 1 || event.ReducerVersion != "v1" || event.RecordRevision == 0 || event.OperationID == "" || event.EventIndex >= event.EventCount || event.EventID == "" { return errInvalid }; return nil }
var errInvalid error
`,
			kind: "event-digest-refusal-seam",
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			mutant := m1SourceFile{Path: "internal/controljournal/model.go", Source: []byte(test.source)}
			if violations := inspectM1Sources([]m1SourceFile{mutant}); !containsM1Violation(violations, test.kind) {
				t.Fatalf("production journal mutant survived %s: %v", test.kind, violations)
			}
		})
	}
}

func collectM1RepositorySources(t *testing.T) []m1SourceFile {
	t.Helper()
	root := filepath.Clean(filepath.Join(m1ContractsDir(t), "..", "..", ".."))
	var files []m1SourceFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, m1SourceFile{Path: filepath.ToSlash(relative), Source: source})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func inspectM1Sources(files []m1SourceFile) []m1ArchitectureViolation {
	var violations []m1ArchitectureViolation
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Source, parser.ImportsOnly)
		if err != nil {
			violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "parse-error", Detail: err.Error()})
			continue
		}
		packagePath := filepath.ToSlash(filepath.Dir(file.Path))
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "parse-error", Detail: "invalid import path"})
				continue
			}
			if strings.Contains(path, "/internal/server") || strings.HasSuffix(path, "internal/server") {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "internal-server-import", Detail: path})
				continue
			}
			if path == m1GapDBModule || strings.HasPrefix(path, m1GapDBModule+"/") {
				if !m1GapDBImportAllowlist[packagePath] {
					violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "gapdb-import-allowlist", Detail: path})
				}
			}
			if isM1SourcePath(file.Path) && isSQLiteImport(path) {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "sqlite-import", Detail: path})
			}
			if isM1ProductionReducerPath(file.Path) && !m1ReducerImportAllowlist[path] {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "reducer-impure-import", Detail: path})
			}
		}

		if isM1SourcePath(file.Path) && !strings.HasSuffix(file.Path, "_test.go") {
			whole, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Source, 0)
			if err != nil {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "parse-error", Detail: err.Error()})
				continue
			}
			seen := make(map[string]bool)
			ast.Inspect(whole, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				kind, forbidden := m1ForbiddenIdentifiers[identifier.Name]
				if forbidden && !seen[kind] {
					seen[kind] = true
					violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: kind, Detail: identifier.Name})
				}
				return true
			})
		}
	}
	violations = append(violations, inspectM1JournalContractSeams(files)...)
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Path != violations[j].Path {
			return violations[i].Path < violations[j].Path
		}
		if violations[i].Kind != violations[j].Kind {
			return violations[i].Kind < violations[j].Kind
		}
		return violations[i].Detail < violations[j].Detail
	})
	return violations
}

func isM1ProductionReducerPath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasPrefix(path, "internal/controljournal/reducer/") && !strings.HasSuffix(path, "_test.go")
}

func inspectM1JournalContractSeams(files []m1SourceFile) []m1ArchitectureViolation {
	var model *m1SourceFile
	for index := range files {
		if filepath.ToSlash(files[index].Path) == "internal/controljournal/model.go" {
			model = &files[index]
			break
		}
	}
	if model == nil {
		return nil
	}
	modelAST, err := parser.ParseFile(token.NewFileSet(), model.Path, model.Source, 0)
	if err != nil || !declaresM1EventModel(modelAST) {
		return nil // The repository-wide parser reports syntax errors once.
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, file := range files {
		path := filepath.ToSlash(file.Path)
		if filepath.ToSlash(filepath.Dir(path)) != "internal/controljournal" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Source, 0)
		if err != nil {
			continue // The repository-wide parser reports this error once.
		}
		for _, declaration := range parsed.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil {
				functions[function.Name.Name] = function
			}
		}
	}

	var violations []m1ArchitectureViolation
	comparator := functions["CompareCommittedEventOrder"]
	if comparator == nil {
		comparator = functions["CompareEventOrder"]
	}
	if comparator == nil || !usesM1OrderCoordinates(comparator) {
		violations = append(violations, m1ArchitectureViolation{
			Path: model.Path, Kind: "event-order-seam",
			Detail: "production comparator must use RecordRevision, EventIndex, then EventID",
		})
	}

	validator := functions["ValidateCommittedEventGroup"]
	if validator == nil {
		validator = functions["ValidateEventGroup"]
	}
	selectors := comparedSelectorNames(validator)
	if validator == nil || !containsAllNames(selectors, "EnvelopeVersion", "EventType", "EventSchemaVersion", "ReducerVersion") || !callsM1VersionVerifier(validator) {
		violations = append(violations, m1ArchitectureViolation{
			Path: model.Path, Kind: "event-version-refusal-seam",
			Detail: "production group validator must consult envelope, event-type/schema, and reducer versions",
		})
	}
	if validator == nil || !containsAllNames(selectors, "RecordRevision", "OperationID", "EventIndex", "EventCount", "EventID") {
		violations = append(violations, m1ArchitectureViolation{
			Path: model.Path, Kind: "event-group-refusal-seam",
			Detail: "production group validator must consult revision, operation, index/count, and EventID",
		})
	}
	if validator == nil || !containsAllNames(selectorNames(validator), "CanonicalEventDigest") || !callsAnyName(validator, "CanonicalEventDigestMatches", "VerifyCanonicalEventDigest", "ValidateCanonicalEventDigest") {
		violations = append(violations, m1ArchitectureViolation{
			Path: model.Path, Kind: "event-digest-refusal-seam",
			Detail: "production group validator must consult CanonicalEventDigest",
		})
	}
	return violations
}

func callsM1VersionVerifier(function *ast.FuncDecl) bool {
	return callsNameMatching(function, func(name string) bool {
		lower := strings.ToLower(name)
		return strings.Contains(lower, "version") && (strings.Contains(lower, "support") || strings.Contains(lower, "valid") || strings.Contains(lower, "registr"))
	})
}

func declaresM1EventModel(file *ast.File) bool {
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, specification := range generic.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if ok && slices.Contains([]string{"EventEnvelope", "CommittedEvent"}, typeSpec.Name.Name) {
				return true
			}
		}
	}
	return false
}

func usesM1OrderCoordinates(function *ast.FuncDecl) bool {
	names := comparedSelectorNames(function)
	revision := firstNameIndex(names, "RecordRevision", "CommittedRevision")
	eventIndex := firstNameIndex(names, "EventIndex")
	eventID := firstNameIndex(names, "EventID")
	return revision >= 0 && eventIndex > revision && eventID > eventIndex
}

func selectorNames(function *ast.FuncDecl) []string {
	if function == nil || function.Body == nil {
		return nil
	}
	var names []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			names = append(names, selector.Sel.Name)
		}
		return true
	})
	return names
}

func comparedSelectorNames(function *ast.FuncDecl) []string {
	if function == nil || function.Body == nil {
		return nil
	}
	var names []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		comparison, ok := node.(*ast.BinaryExpr)
		if !ok || (comparison.Op != token.EQL && comparison.Op != token.NEQ && comparison.Op != token.LSS && comparison.Op != token.LEQ && comparison.Op != token.GTR && comparison.Op != token.GEQ) {
			return true
		}
		ast.Inspect(comparison, func(child ast.Node) bool {
			if selector, ok := child.(*ast.SelectorExpr); ok {
				names = append(names, selector.Sel.Name)
			}
			return true
		})
		return false
	})
	return names
}

func callsAnyName(function *ast.FuncDecl, wanted ...string) bool {
	return callsNameMatching(function, func(name string) bool { return slices.Contains(wanted, name) })
}

func callsNameMatching(function *ast.FuncDecl, matches func(string) bool) bool {
	if function == nil || function.Body == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch callable := call.Fun.(type) {
		case *ast.Ident:
			name = callable.Name
		case *ast.SelectorExpr:
			name = callable.Sel.Name
		}
		if matches(name) {
			found = true
			return false
		}
		return true
	})
	return found
}

func firstNameIndex(names []string, wanted ...string) int {
	for index, name := range names {
		if slices.Contains(wanted, name) {
			return index
		}
	}
	return -1
}

func containsAllNames(names []string, wanted ...string) bool {
	for _, name := range wanted {
		if !slices.Contains(names, name) {
			return false
		}
	}
	return true
}

func isM1SourcePath(path string) bool {
	path = filepath.ToSlash(path)
	for _, root := range m1SourceRoots {
		if path == root+".go" || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func isSQLiteImport(path string) bool {
	return path == "modernc.org/sqlite" || strings.HasSuffix(path, "/controlstore/sqlite")
}

func containsM1Violation(violations []m1ArchitectureViolation, kind string) bool {
	for _, violation := range violations {
		if violation.Kind == kind {
			return true
		}
	}
	return false
}
