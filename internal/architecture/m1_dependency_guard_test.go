package architecture

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
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

// Pure journal code has a closed import surface. New capabilities must be
// admitted deliberately rather than inheriting ambient I/O through a helper.
var m1JournalPureImportAllowlist = map[string]bool{
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
	clean := append(m1ExactJournalFixture(), []m1SourceFile{
		{Path: "internal/controlstore/gapdb/client.go", Source: []byte("package gapdb\nimport _ \"github.com/spec-kitty/gapdb\"\n")},
		{Path: "internal/controljournal/notes.go", Source: []byte("package controljournal\n// CurrentBatchRevision and internal/server are documentation only.\nvar note = \"sqlite fallback\"\n")},
		{Path: "internal/controlstore/sqlite/store.go", Source: []byte("package sqlite\nimport _ \"modernc.org/sqlite\"\n")},
		{Path: "internal/controlstore/sqlite/cgo.go", Source: []byte("package sqlite\nimport _ \"github.com/mattn/go-sqlite3\"\n")},
	}...)
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
			file: m1SourceFile{Path: "internal/provingground/client.go", Source: []byte("package provingground\nimport _ \"github.com/spec-kitty/gapdb\"\n")},
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
			id:   "sqlite-alternate-driver",
			file: m1SourceFile{Path: "internal/provingground/runtime.go", Source: []byte("package provingground\nimport _ \"github.com/mattn/go-sqlite3\"\n")},
			kind: "sqlite-import",
		},
		{
			id:   "current-batch-revision-fill-in",
			file: m1SourceFile{Path: "internal/provingground/apply.go", Source: []byte("package provingground\nvar CurrentBatchRevision uint64\n")},
			kind: "current-batch-revision",
		},
		{
			id:   "global-authority-cas",
			file: m1SourceFile{Path: "internal/provingground/apply.go", Source: []byte("package provingground\nfunc CompareAndSwapGlobalEpoch() {}\n")},
			kind: "global-authority-cas",
		},
		{
			id:   "shared-projection-head",
			file: m1SourceFile{Path: "internal/provingground/reduce.go", Source: []byte("package provingground\ntype SharedProjectionHead struct{}\n")},
			kind: "shared-projection-head",
		},
		{
			id:   "runtime-coordinate-conflation",
			file: m1SourceFile{Path: "internal/provingground/model.go", Source: []byte("package provingground\nvar FenceTokenRuntimeEpoch string\n")},
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

func TestM1DependencyJournalPurityMutants(t *testing.T) {
	cleanRoot := append(m1ExactJournalFixture(), m1SourceFile{
		Path:   "internal/controljournal/codec.go",
		Source: []byte("package controljournal\nimport (\"crypto/sha256\"; \"encoding/json\")\nvar _ = sha256.Sum256\nvar _ = json.Valid\n"),
	})
	if violations := inspectM1Sources(cleanRoot); len(violations) != 0 {
		t.Fatalf("clean pure journal kernel rejected: %v", violations)
	}

	cleanReducer := append(m1ExactJournalFixture(), m1SourceFile{
		Path:   "internal/controljournal/reducer/reducer.go",
		Source: []byte("package reducer\nimport (\"crypto/sha256\"; \"encoding/json\"; \"github.com/spec-kitty/go-kitty3/internal/controljournal\")\nvar _ = sha256.Sum256\nvar _ = json.Valid\nvar _ controljournal.StoredEventV1\n"),
	})
	if violations := inspectM1Sources(cleanReducer); len(violations) != 0 {
		t.Fatalf("clean pure reducer rejected: %v", violations)
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
		for _, boundary := range []struct {
			name        string
			path        string
			packageName string
			kind        string
		}{
			{name: "kernel", path: "internal/controljournal/io.go", packageName: "controljournal", kind: "journal-kernel-impure-import"},
			{name: "reducer", path: "internal/controljournal/reducer/io.go", packageName: "reducer", kind: "reducer-impure-import"},
		} {
			t.Run(boundary.name+"/"+imported, func(t *testing.T) {
				files := append(m1ExactJournalFixture(), m1SourceFile{
					Path:   boundary.path,
					Source: []byte("package " + boundary.packageName + "\nimport _ \"" + imported + "\"\n"),
				})
				if violations := inspectM1Sources(files); !containsM1Violation(violations, boundary.kind) {
					t.Fatalf("pure journal code admitted forbidden import %q: %v", imported, violations)
				}
			})
		}
	}
}

func TestM1DependencyJournalCapabilityControls(t *testing.T) {
	if violations := inspectM1Sources(nil); len(violations) != 0 {
		t.Fatalf("absent controljournal tree must not activate the guard: %v", violations)
	}
	if violations := inspectM1Sources(m1ExactJournalFixture()); len(violations) != 0 {
		t.Fatalf("exact frozen capability rejected: %v", violations)
	}

	activation := []struct {
		name string
		file m1SourceFile
	}{
		{
			name: "events-go-first",
			file: m1SourceFile{Path: "internal/controljournal/events.go", Source: []byte("package controljournal\ntype EventEnvelopeV1 struct{}\n")},
		},
		{
			name: "reducer-first",
			file: m1SourceFile{Path: "internal/controljournal/reducer/reduce.go", Source: []byte("package reducer\nfunc Reduce() {}\n")},
		},
	}
	for _, mutant := range activation {
		t.Run(mutant.name, func(t *testing.T) {
			violations := inspectM1Sources([]m1SourceFile{mutant.file})
			if !containsM1Violation(violations, "journal-contract-missing") {
				t.Fatalf("first production file did not activate the whole contract: %v", violations)
			}
		})
	}
}

func TestM1DependencyJournalCapabilityMutants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]m1SourceFile) []m1SourceFile
		kind   string
	}{
		{
			name: "embedded-record-revision-does-not-satisfy-stored-event",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "type StoredEventV1 struct {\n\tRecordRevision uint64\n\tEnvelope EventEnvelopeV1\n}", "type revisionCarrier struct { RecordRevision uint64 }\ntype StoredEventV1 struct {\n\trevisionCarrier\n\tEnvelope EventEnvelopeV1\n}")
			},
			kind: "journal-stored-event",
		},
		{
			name: "stored-record-revision-wrong-type",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "RecordRevision uint64", "RecordRevision int64")
			},
			kind: "journal-stored-event",
		},
		{
			name: "stored-envelope-wrong-type",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "Envelope EventEnvelopeV1", "Envelope *EventEnvelopeV1")
			},
			kind: "journal-stored-event",
		},
		{
			name: "event-version-wrong-schema-type",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "EventSchemaVersion uint32", "EventSchemaVersion uint64")
			},
			kind: "journal-event-version",
		},
		{
			name: "exported-registry-state",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "versions map[string]EventVersionV1", "Versions map[string]EventVersionV1")
			},
			kind: "journal-registry",
		},
		{
			name: "second-record-revision-owner",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type RevisionDecoy struct { RecordRevision uint64 }\n")
			},
			kind: "journal-record-revision-owner",
		},
		{
			name: "capability-alias",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type ValidatedAlias = ValidatedJournalV1\n")
			},
			kind: "journal-capability-recreation",
		},
		{
			name: "capability-embedding",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type ValidatedWrapper struct { ValidatedJournalV1 }\n")
			},
			kind: "journal-capability-recreation",
		},
		{
			name: "second-capability-producer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func ManufactureJournal() ValidatedJournalV1 { return ValidatedJournalV1{} }\n")
			},
			kind: "journal-capability-producer",
		},
		{
			name: "raw-slice-ordering-competitor",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortUnvalidated(events []StoredEventV1) { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "raw-slice-alias-competitor",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type RawEvents = []StoredEventV1\nfunc SortAliased(events RawEvents) { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "raw-slice-variadic-competitor",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func ValidateVariadic(events ...StoredEventV1) { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "raw-slice-method-receiver",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type UnvalidatedEvents []StoredEventV1\nfunc (events UnvalidatedEvents) Sort() { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "raw-slice-pointer-method-receiver",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type UnvalidatedEventPointer []StoredEventV1\nfunc (events *UnvalidatedEventPointer) Validate() { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "qualified-raw-slice-alias-competitor",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return append(cloneM1Fixture(files), m1SourceFile{
					Path: "internal/controljournal/reducer/raw.go",
					Source: []byte(`package reducer
import journal "github.com/spec-kitty/go-kitty3/internal/controljournal"
type RawEvents = []journal.StoredEventV1
func ReduceRaw(events RawEvents) { _ = events }
`),
				})
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "anonymous-record-revision-owner",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "var revisionDecoy struct { RecordRevision uint64 }\n")
			},
			kind: "journal-record-revision-owner",
		},
		{
			name: "wrong-registry-constructor-signature",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func NewEventRegistryV1(versions []EventVersionV1) (EventRegistryV1, error)", "func NewEventRegistryV1(versions ...EventVersionV1) (EventRegistryV1, error)")
			},
			kind: "journal-constructor",
		},
		{
			name: "wrong-validation-signature",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func ValidateJournalV1(registry EventRegistryV1, events []StoredEventV1) (ValidatedJournalV1, error)", "func ValidateJournalV1(events []StoredEventV1, registry EventRegistryV1) (ValidatedJournalV1, error)")
			},
			kind: "journal-validator",
		},
		{
			name: "exported-capability-state",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "events []StoredEventV1", "Events []StoredEventV1")
			},
			kind: "journal-capability",
		},
		{
			name: "len-pointer-receiver",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func (journal ValidatedJournalV1) Len() int", "func (journal *ValidatedJournalV1) Len() int")
			},
			kind: "journal-len-method",
		},
		{
			name: "at-wrong-result",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func (journal ValidatedJournalV1) At(index int) (StoredEventV1, bool)", "func (journal ValidatedJournalV1) At(index int) (*StoredEventV1, bool)")
			},
			kind: "journal-at-method",
		},
	}

	for _, mutant := range tests {
		t.Run(mutant.name, func(t *testing.T) {
			violations := inspectM1Sources(mutant.mutate(m1ExactJournalFixture()))
			if !containsM1Violation(violations, mutant.kind) {
				t.Fatalf("capability mutant survived %s: %v", mutant.kind, violations)
			}
		})
	}
}

func TestM1DependencyJournalResolvedTypeCleanControls(t *testing.T) {
	files := appendM1Fixture(m1ExactJournalFixture(), `
type EventVersions []EventVersionV1
func InspectVersions(versions EventVersions) { _ = versions }
func (versions EventVersions) Len() int { return len(versions) }
var revisionMetadata struct { Revision uint64 }
`)
	files = append(files, m1SourceFile{
		Path: "internal/controljournal/reducer/versions.go",
		Source: []byte(`package reducer
import journal "github.com/spec-kitty/go-kitty3/internal/controljournal"
type Versions = []journal.EventVersionV1
func Inspect(versions Versions) { _ = versions }
`),
	})
	if violations := inspectM1Sources(files); len(violations) != 0 {
		t.Fatalf("non-authority slices and anonymous metadata rejected: %v", violations)
	}
}

func TestM1DependencyJournalDecorativeDecoysCannotMaskExactSeam(t *testing.T) {
	decoy := `
type DecorativeStoredEvent struct { Record uint64; Envelope EventEnvelopeV1 }
type DecorativeValidated struct { events []DecorativeStoredEvent }
func DiscoverAndValidate([]DecorativeStoredEvent) (DecorativeValidated, error) {
	return DecorativeValidated{}, nil
}
`
	for _, order := range []string{"before", "after"} {
		t.Run(order, func(t *testing.T) {
			files := m1ExactJournalFixture()
			files = replaceM1Fixture(files,
				"func ValidateJournalV1(registry EventRegistryV1, events []StoredEventV1) (ValidatedJournalV1, error)",
				"func ValidateJournalV1(events []StoredEventV1, registry EventRegistryV1) (ValidatedJournalV1, error)")
			if order == "before" {
				files[0].Source = []byte(strings.Replace(string(files[0].Source), "type EventEnvelopeV1", decoy+"\ntype EventEnvelopeV1", 1))
			} else {
				files = appendM1Fixture(files, decoy)
			}
			if violations := inspectM1Sources(files); !containsM1Violation(violations, "journal-validator") {
				t.Fatalf("decorative %s decoy masked the broken exact validator: %v", order, violations)
			}
		})
	}
}

func m1ExactJournalFixture() []m1SourceFile {
	return []m1SourceFile{
		{
			Path: "internal/controljournal/events.go",
			Source: []byte(`package controljournal

type EventEnvelopeV1 struct{}

type StoredEventV1 struct {
	RecordRevision uint64
	Envelope EventEnvelopeV1
}

type EventVersionV1 struct {
	EventType string
	EventSchemaVersion uint32
	ReducerVersion string
}

type EventRegistryV1 struct { versions map[string]EventVersionV1 }
type ValidatedJournalV1 struct { events []StoredEventV1 }
`),
		},
		{
			Path: "internal/controljournal/validate.go",
			Source: []byte(`package controljournal

func NewEventRegistryV1(versions []EventVersionV1) (EventRegistryV1, error) {
	registry := EventRegistryV1{versions: make(map[string]EventVersionV1, len(versions))}
	for _, version := range versions { registry.versions[version.EventType] = version }
	return registry, nil
}

func ValidateJournalV1(registry EventRegistryV1, events []StoredEventV1) (ValidatedJournalV1, error) {
	_ = registry
	return ValidatedJournalV1{events: append([]StoredEventV1(nil), events...)}, nil
}

func (journal ValidatedJournalV1) Len() int { return len(journal.events) }
func (journal ValidatedJournalV1) At(index int) (StoredEventV1, bool) {
	if index < 0 || index >= len(journal.events) { return StoredEventV1{}, false }
	return journal.events[index], true
}
`),
		},
	}
}

func replaceM1Fixture(files []m1SourceFile, old, replacement string) []m1SourceFile {
	result := cloneM1Fixture(files)
	for index := range result {
		source := string(result[index].Source)
		if strings.Contains(source, old) {
			result[index].Source = []byte(strings.Replace(source, old, replacement, 1))
			return result
		}
	}
	panic("fixture replacement target not found: " + old)
}

func appendM1Fixture(files []m1SourceFile, declaration string) []m1SourceFile {
	result := cloneM1Fixture(files)
	result[len(result)-1].Source = append(result[len(result)-1].Source, []byte("\n"+declaration)...)
	return result
}

func cloneM1Fixture(files []m1SourceFile) []m1SourceFile {
	result := make([]m1SourceFile, len(files))
	for index, file := range files {
		result[index] = m1SourceFile{Path: file.Path, Source: append([]byte(nil), file.Source...)}
	}
	return result
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
			if isM1ProductionReducerPath(file.Path) && !m1JournalPureImportAllowlist[path] {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "reducer-impure-import", Detail: path})
			}
			if isM1ProductionJournalKernelPath(file.Path) && !m1JournalPureImportAllowlist[path] {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "journal-kernel-impure-import", Detail: path})
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
	violations = append(violations, inspectM1JournalCapability(files)...)
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

type m1ParsedJournalFile struct {
	source m1SourceFile
	file   *ast.File
	info   *types.Info
}

func inspectM1JournalCapability(files []m1SourceFile) []m1ArchitectureViolation {
	var production []m1SourceFile
	for _, file := range files {
		path := filepath.ToSlash(file.Path)
		if strings.HasPrefix(path, "internal/controljournal/") && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			production = append(production, file)
		}
	}
	if len(production) == 0 {
		return nil
	}

	fset := token.NewFileSet()
	var parsed []m1ParsedJournalFile
	var rootFiles []*ast.File
	var violations []m1ArchitectureViolation
	for _, source := range production {
		file, err := parser.ParseFile(fset, source.Path, source.Source, 0)
		if err != nil {
			continue // inspectM1Sources reports syntax failures once.
		}
		parsed = append(parsed, m1ParsedJournalFile{source: source, file: file})
		if filepath.ToSlash(filepath.Dir(source.Path)) == "internal/controljournal" {
			if file.Name.Name != "controljournal" {
				violations = append(violations, m1ArchitectureViolation{Path: source.Path, Kind: "journal-root-package", Detail: "root production files must declare package controljournal"})
				continue
			}
			rootFiles = append(rootFiles, file)
		}
	}
	if len(rootFiles) == 0 {
		return append(violations, m1ArchitectureViolation{
			Path:   "internal/controljournal",
			Kind:   "journal-contract-missing",
			Detail: "production tree is active but the exact root capability is absent",
		})
	}

	rootInfo := m1NewTypesInfo()
	config := types.Config{Importer: importer.Default(), Error: func(error) {}}
	pkg, _ := config.Check("github.com/spec-kitty/go-kitty3/internal/controljournal", fset, rootFiles, rootInfo)
	if pkg == nil {
		violations = append(violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: "journal-contract-missing", Detail: "root package cannot be type checked"})
		return append(violations, inspectM1JournalCompetitors(parsed, nil)...)
	}
	m1ResolveJournalPackageTypes(fset, parsed, pkg, rootInfo)

	stored := m1RequireNamedStruct(pkg, "StoredEventV1", "journal-stored-event", &violations)
	envelope := m1RequireNamed(pkg, "EventEnvelopeV1", "journal-stored-event", &violations)
	if stored != nil && envelope != nil {
		m1RequireExactStructFields(stored, []m1ExpectedField{
			{name: "RecordRevision", typ: types.Typ[types.Uint64]},
			{name: "Envelope", typ: envelope},
		}, "journal-stored-event", &violations)
	}

	version := m1RequireNamedStruct(pkg, "EventVersionV1", "journal-event-version", &violations)
	if version != nil {
		m1RequireExactStructFields(version, []m1ExpectedField{
			{name: "EventType", typ: types.Typ[types.String]},
			{name: "EventSchemaVersion", typ: types.Typ[types.Uint32]},
			{name: "ReducerVersion", typ: types.Typ[types.String]},
		}, "journal-event-version", &violations)
	}

	registry := m1RequireOpaqueStruct(pkg, "EventRegistryV1", "journal-registry", &violations)
	validated := m1RequireOpaqueStruct(pkg, "ValidatedJournalV1", "journal-capability", &violations)
	if version != nil && registry != nil {
		m1RequireFunction(pkg, "NewEventRegistryV1", []types.Type{types.NewSlice(version)}, []types.Type{registry, types.Universe.Lookup("error").Type()}, "journal-constructor", &violations)
	}
	if registry != nil && stored != nil && validated != nil {
		m1RequireFunction(pkg, "ValidateJournalV1", []types.Type{registry, types.NewSlice(stored)}, []types.Type{validated, types.Universe.Lookup("error").Type()}, "journal-validator", &violations)
		m1RequireMethod(validated, "Len", nil, []types.Type{types.Typ[types.Int]}, "journal-len-method", &violations)
		m1RequireMethod(validated, "At", []types.Type{types.Typ[types.Int]}, []types.Type{stored, types.Typ[types.Bool]}, "journal-at-method", &violations)
	}
	violations = append(violations, inspectM1JournalCompetitors(parsed, stored)...)
	return violations
}

func m1NewTypesInfo() *types.Info {
	return &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
}

type m1JournalImporter struct {
	root     *types.Package
	fallback types.Importer
}

func (i m1JournalImporter) Import(path string) (*types.Package, error) {
	if path == i.root.Path() {
		return i.root, nil
	}
	return i.fallback.Import(path)
}

func m1ResolveJournalPackageTypes(fset *token.FileSet, parsed []m1ParsedJournalFile, root *types.Package, rootInfo *types.Info) {
	type group struct {
		indices []int
		files   []*ast.File
	}
	groups := make(map[string]*group)
	for index := range parsed {
		directory := filepath.ToSlash(filepath.Dir(parsed[index].source.Path))
		if directory == "internal/controljournal" && parsed[index].file.Name.Name == "controljournal" {
			parsed[index].info = rootInfo
			continue
		}
		key := directory + "\x00" + parsed[index].file.Name.Name
		current := groups[key]
		if current == nil {
			current = &group{}
			groups[key] = current
		}
		current.indices = append(current.indices, index)
		current.files = append(current.files, parsed[index].file)
	}
	for key, current := range groups {
		directory, _, _ := strings.Cut(key, "\x00")
		info := m1NewTypesInfo()
		config := types.Config{
			Importer: m1JournalImporter{root: root, fallback: importer.Default()},
			Error:    func(error) {},
		}
		_, _ = config.Check("github.com/spec-kitty/go-kitty3/"+directory, fset, current.files, info)
		for _, index := range current.indices {
			parsed[index].info = info
		}
	}
}

type m1ExpectedField struct {
	name string
	typ  types.Type
}

func m1RequireNamed(pkg *types.Package, name, kind string, violations *[]m1ArchitectureViolation) *types.Named {
	object := pkg.Scope().Lookup(name)
	named, ok := objectTypeAsNamed(object)
	if !ok {
		*violations = append(*violations,
			m1ArchitectureViolation{Path: "internal/controljournal", Kind: "journal-contract-missing", Detail: "missing exact type " + name},
			m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: "missing exact type " + name},
		)
		return nil
	}
	return named
}

func m1RequireNamedStruct(pkg *types.Package, name, kind string, violations *[]m1ArchitectureViolation) *types.Named {
	named := m1RequireNamed(pkg, name, kind, violations)
	if named == nil {
		return nil
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: name + " must be a defined struct"})
		return nil
	}
	return named
}

func m1RequireOpaqueStruct(pkg *types.Package, name, kind string, violations *[]m1ArchitectureViolation) *types.Named {
	named := m1RequireNamedStruct(pkg, name, kind, violations)
	if named == nil {
		return nil
	}
	structure := named.Underlying().(*types.Struct)
	if structure.NumFields() == 0 {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: name + " must retain unexported state"})
		return named
	}
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if field.Exported() || field.Embedded() {
			*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: name + " state must be direct and unexported"})
			break
		}
	}
	return named
}

func objectTypeAsNamed(object types.Object) (*types.Named, bool) {
	if object == nil {
		return nil, false
	}
	named, ok := object.Type().(*types.Named)
	return named, ok
}

func m1RequireExactStructFields(named *types.Named, expected []m1ExpectedField, kind string, violations *[]m1ArchitectureViolation) {
	structure := named.Underlying().(*types.Struct)
	if structure.NumFields() != len(expected) {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: named.Obj().Name() + " must declare only the frozen direct fields"})
		return
	}
	for index, field := range expected {
		actual := structure.Field(index)
		if actual.Name() != field.name || actual.Embedded() || !types.Identical(actual.Type(), field.typ) {
			*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: named.Obj().Name() + " field " + field.name + " must be direct and have the exact frozen type"})
		}
	}
}

func m1RequireFunction(pkg *types.Package, name string, parameters, results []types.Type, kind string, violations *[]m1ArchitectureViolation) {
	object, ok := pkg.Scope().Lookup(name).(*types.Func)
	if !ok {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: "missing exact function " + name})
		return
	}
	signature, ok := object.Type().(*types.Signature)
	if !ok || signature.Recv() != nil || signature.Variadic() || !m1TupleMatches(signature.Params(), parameters) || !m1TupleMatches(signature.Results(), results) {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: name + " does not have the exact frozen signature"})
	}
}

func m1RequireMethod(receiver *types.Named, name string, parameters, results []types.Type, kind string, violations *[]m1ArchitectureViolation) {
	selection := types.NewMethodSet(receiver).Lookup(nil, name)
	if selection == nil {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: "missing exact value method " + name})
		return
	}
	function, ok := selection.Obj().(*types.Func)
	if !ok {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: name + " is not a method"})
		return
	}
	signature := function.Type().(*types.Signature)
	if signature.Variadic() || signature.Recv() == nil || !types.Identical(signature.Recv().Type(), receiver) || !m1TupleMatches(signature.Params(), parameters) || !m1TupleMatches(signature.Results(), results) {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: name + " does not have the exact frozen value-method signature"})
	}
}

func m1TupleMatches(tuple *types.Tuple, expected []types.Type) bool {
	if tuple.Len() != len(expected) {
		return false
	}
	for index, typ := range expected {
		if !types.Identical(tuple.At(index).Type(), typ) {
			return false
		}
	}
	return true
}

func inspectM1JournalCompetitors(files []m1ParsedJournalFile, stored *types.Named) []m1ArchitectureViolation {
	var violations []m1ArchitectureViolation
	for _, parsed := range files {
		root := filepath.ToSlash(filepath.Dir(parsed.source.Path)) == "internal/controljournal" && parsed.file.Name.Name == "controljournal"
		violations = append(violations, m1RecordRevisionOwnerViolations(parsed, root)...)
		for _, declaration := range parsed.file.Decls {
			switch value := declaration.(type) {
			case *ast.GenDecl:
				if value.Tok != token.TYPE {
					continue
				}
				for _, specification := range value.Specs {
					typeSpec := specification.(*ast.TypeSpec)
					if typeSpec.Name.Name != "ValidatedJournalV1" && m1ExprReferencesName(typeSpec.Type, "ValidatedJournalV1") {
						violations = append(violations, m1ArchitectureViolation{Path: parsed.source.Path, Kind: "journal-capability-recreation", Detail: typeSpec.Name.Name + " aliases, derives, or embeds ValidatedJournalV1"})
					}
				}
			case *ast.FuncDecl:
				authoritative := root && value.Recv == nil && value.Name.Name == "ValidateJournalV1"
				if !authoritative && m1FieldListReferencesName(value.Type.Results, "ValidatedJournalV1") {
					violations = append(violations, m1ArchitectureViolation{Path: parsed.source.Path, Kind: "journal-capability-producer", Detail: value.Name.Name + " is a competing ValidatedJournalV1 producer"})
				}
				if !authoritative && m1FunctionConsumesRawStoredEvents(value, parsed.info, stored) {
					violations = append(violations, m1ArchitectureViolation{Path: parsed.source.Path, Kind: "journal-raw-slice-authority", Detail: value.Name.Name + " accepts unvalidated []StoredEventV1"})
				}
			}
		}
	}
	return violations
}

func m1RecordRevisionOwnerViolations(parsed m1ParsedJournalFile, root bool) []m1ArchitectureViolation {
	var authorized *ast.StructType
	if root {
		for _, declaration := range parsed.file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				if typeSpec.Name.Name == "StoredEventV1" {
					authorized, _ = typeSpec.Type.(*ast.StructType)
				}
			}
		}
	}

	var violations []m1ArchitectureViolation
	ast.Inspect(parsed.file, func(node ast.Node) bool {
		structure, ok := node.(*ast.StructType)
		if !ok || structure == authorized {
			return true
		}
		for _, field := range structure.Fields.List {
			for _, name := range field.Names {
				if name.Name == "RecordRevision" {
					violations = append(violations, m1ArchitectureViolation{
						Path:   parsed.source.Path,
						Kind:   "journal-record-revision-owner",
						Detail: "anonymous or non-authorized struct directly owns RecordRevision",
					})
					return false
				}
			}
		}
		return true
	})
	return violations
}

func m1FunctionConsumesRawStoredEvents(function *ast.FuncDecl, info *types.Info, stored *types.Named) bool {
	if info != nil && stored != nil {
		object, _ := info.Defs[function.Name].(*types.Func)
		if object != nil {
			signature, _ := object.Type().(*types.Signature)
			if signature != nil {
				for index := 0; index < signature.Params().Len(); index++ {
					if m1TypeIsRawStoredEventSlice(signature.Params().At(index).Type(), stored) {
						return true
					}
				}
				if signature.Recv() != nil && m1TypeIsRawStoredEventSlice(signature.Recv().Type(), stored) {
					return true
				}
			}
		}
	}
	// Fail closed for the literal forms even when an unrelated package error
	// prevents go/types from resolving a declaration.
	return m1FieldListHasRawStoredSliceSyntax(function.Type.Params) || m1FieldListHasRawStoredSliceSyntax(function.Recv)
}

func m1TypeIsRawStoredEventSlice(typ types.Type, stored *types.Named) bool {
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	slice, ok := typ.Underlying().(*types.Slice)
	return ok && types.Identical(slice.Elem(), stored)
}

func m1ExprReferencesName(expression ast.Expr, name string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func m1FieldListReferencesName(fields *ast.FieldList, name string) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if m1ExprReferencesName(field.Type, name) {
			return true
		}
	}
	return false
}

func m1FieldListHasRawStoredSliceSyntax(fields *ast.FieldList) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		switch expression := field.Type.(type) {
		case *ast.ArrayType:
			if expression.Len == nil && m1ExprReferencesName(expression.Elt, "StoredEventV1") {
				return true
			}
		case *ast.Ellipsis:
			if m1ExprReferencesName(expression.Elt, "StoredEventV1") {
				return true
			}
		}
	}
	return false
}

func isM1ProductionReducerPath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasPrefix(path, "internal/controljournal/reducer/") && !strings.HasSuffix(path, "_test.go")
}

func isM1ProductionJournalKernelPath(path string) bool {
	path = filepath.ToSlash(path)
	return filepath.ToSlash(filepath.Dir(path)) == "internal/controljournal" && !strings.HasSuffix(path, "_test.go")
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
	for _, provider := range []string{
		"modernc.org/sqlite",
		"github.com/mattn/go-sqlite3",
		"github.com/glebarez/go-sqlite",
		"github.com/glebarez/sqlite",
		"github.com/ncruces/go-sqlite3",
		"github.com/tursodatabase/go-libsql",
		"github.com/tursodatabase/libsql-client-go",
		"github.com/uptrace/bun/driver/sqlitedialect",
		"github.com/uptrace/bun/driver/sqliteshim",
		"gorm.io/driver/sqlite",
		"zombiezen.com/go/sqlite",
		"crawshaw.io/sqlite",
	} {
		if path == provider || strings.HasPrefix(path, provider+"/") {
			return true
		}
	}
	// Provider names evolve faster than the frozen contract. Classify import
	// path components, not source text, so a new SQLite driver fails closed
	// without making comments or string literals architectural evidence.
	for _, component := range strings.Split(strings.ToLower(path), "/") {
		if strings.Contains(component, "sqlite") {
			return true
		}
	}
	return false
}

func containsM1Violation(violations []m1ArchitectureViolation, kind string) bool {
	_, found := findM1Violation(violations, kind)
	return found
}

func findM1Violation(violations []m1ArchitectureViolation, kind string) (string, bool) {
	for _, violation := range violations {
		if violation.Kind == kind {
			return violation.Detail, true
		}
	}
	return "", false
}
