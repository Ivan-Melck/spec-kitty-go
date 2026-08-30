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
	"context":         true,
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
			id:   "sqlite-compatible-sqlcipher-driver",
			file: m1SourceFile{Path: "internal/provingground/runtime.go", Source: []byte("package provingground\nimport _ \"github.com/mutecomm/go-sqlcipher/v4\"\n")},
			kind: "sqlite-import",
		},
		{
			id:   "sqlite-provider-classification-case-insensitive",
			file: m1SourceFile{Path: "internal/provingground/runtime.go", Source: []byte("package provingground\nimport _ \"example.test/storage/Go-SQLCipher\"\n")},
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
	cleanPlayback := append(m1ExactJournalFixture(), m1SourceFile{
		Path:   "internal/controljournal/playback/playback.go",
		Source: []byte("package playback\nimport (\"crypto/sha256\"; \"github.com/spec-kitty/go-kitty3/internal/controljournal\")\nvar _ = sha256.Sum256\nvar _ controljournal.ValidatedJournalV1\n"),
	})
	if violations := inspectM1Sources(cleanPlayback); len(violations) != 0 {
		t.Fatalf("clean recursively pure playback rejected: %v", violations)
	}
	cleanCheckpoint := append(m1ExactJournalFixture(), m1SourceFile{
		Path:   "internal/controljournal/checkpoint/restore.go",
		Source: []byte("package checkpoint\nimport (\"context\"; \"github.com/spec-kitty/go-kitty3/internal/controljournal\")\ntype Reader interface { Read(context.Context) controljournal.StoredEventV1 }\n"),
	})
	if violations := inspectM1Sources(cleanCheckpoint); len(violations) != 0 {
		t.Fatalf("pure cancellation-aware checkpoint seam rejected: %v", violations)
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
			{name: "playback", path: "internal/controljournal/playback/io.go", packageName: "playback", kind: "journal-child-impure-import"},
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

func TestM1DependencyJournalApplicationIsImpureButSQLiteFree(t *testing.T) {
	clean := append(m1ExactJournalFixture(), m1SourceFile{
		Path: "internal/controljournal/application/service.go",
		Source: []byte("package application\nimport (\"github.com/spec-kitty/go-kitty3/internal/controljournal\"; \"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb\")\n" +
			"var _ controljournal.ValidatedJournalV1\nvar _ gapdb.Port\n"),
	})
	if violations := inspectM1Sources(clean); len(violations) != 0 {
		t.Fatalf("application composition rejected: %v", violations)
	}

	mutant := append(m1ExactJournalFixture(), m1SourceFile{
		Path:   "internal/controljournal/application/service.go",
		Source: []byte("package application\nimport _ \"modernc.org/sqlite\"\n"),
	})
	if violations := inspectM1Sources(mutant); !containsM1Violation(violations, "sqlite-import") {
		t.Fatalf("application SQLite mutant survived: %v", violations)
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

func TestM1DependencyJournalTypeCheckMutants(t *testing.T) {
	rootBroken := appendM1Fixture(m1ExactJournalFixture(), "var _ MissingType\n")
	if violations := inspectM1Sources(rootBroken); !containsM1Violation(violations, "journal-type-check") {
		t.Fatalf("root type-check error failed open: %v", violations)
	}

	childBroken := append(m1ExactJournalFixture(), m1SourceFile{
		Path:   "internal/controljournal/playback/broken.go",
		Source: []byte("package playback\nfunc Broken(MissingType) {}\n"),
	})
	if violations := inspectM1Sources(childBroken); !containsM1Violation(violations, "journal-type-check") {
		t.Fatalf("child type-check error failed open: %v", violations)
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
			name: "capability-defined-derivation",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type ValidatedDerivation ValidatedJournalV1\n")
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
			name: "function-literal-raw-consumer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "var SortRaw = func(events []StoredEventV1) { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "interface-method-raw-consumer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type RawSorter interface { Sort([]StoredEventV1) }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "function-literal-capability-producer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "var Manufacture = func() ValidatedJournalV1 { return ValidatedJournalV1{} }\n")
			},
			kind: "journal-capability-producer",
		},
		{
			name: "generic-raw-slice-constraint",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortGeneric[S ~[]StoredEventV1](events S) { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "named-raw-slice-wrapper",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type RawInput struct { Events []StoredEventV1 }\nfunc SortWrapped(input RawInput) { _ = input }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "nested-anonymous-raw-slice-wrapper",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortNested(input struct { Batch struct { Events []StoredEventV1 } }) { _ = input }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "array-raw-container",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortArray(input [1][]StoredEventV1) { _ = input }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "map-raw-container",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortMap(input map[string][]StoredEventV1) { _ = input }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "channel-raw-container",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortChannel(input chan []StoredEventV1) { _ = input }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "function-wrapper-raw-container",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortCallback(input func([]StoredEventV1)) { _ = input }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "pointer-raw-wrapper",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type RawPointerInput struct { Events []StoredEventV1 }\nfunc SortPointer(input *RawPointerInput) { _ = input }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "generic-union-raw-constraint",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func SortUnion[S ~[]StoredEventV1 | ~[1]StoredEventV1](events S) { _ = events }\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "function-variable-raw-consumer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "var SortVariable func([]StoredEventV1)\n")
			},
			kind: "journal-raw-slice-authority",
		},
		{
			name: "wrapped-capability-producer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type CapabilityResult struct { Journal ValidatedJournalV1 }\nfunc ManufactureWrapped() CapabilityResult { return CapabilityResult{} }\n")
			},
			kind: "journal-capability-producer",
		},
		{
			name: "nested-container-capability-producer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func ManufactureNested() map[string][]*ValidatedJournalV1 { return nil }\n")
			},
			kind: "journal-capability-producer",
		},
		{
			name: "interface-capability-producer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type Manufacturer interface { Manufacture() ValidatedJournalV1 }\n")
			},
			kind: "journal-capability-producer",
		},
		{
			name: "function-variable-capability-producer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "var ManufactureVariable func() ValidatedJournalV1\n")
			},
			kind: "journal-capability-producer",
		},
		{
			name: "validator-function-value-rebinding",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "var ValidateAgain = ValidateJournalV1\n")
			},
			kind: "journal-validator-rebinding",
		},
		{
			name: "validation-seal-version-wrong-type",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "version uint8", "version uint16")
			},
			kind: "journal-validation-seal",
		},
		{
			name: "validation-seal-digest-wrong-shape",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "digest [32]byte", "digest []byte")
			},
			kind: "journal-validation-seal",
		},
		{
			name: "validation-seal-tagged-field",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "digest [32]byte", "digest [32]byte `json:\"digest\"`")
			},
			kind: "journal-validation-seal",
		},
		{
			name: "capability-missing-seal",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "seal validationSealV1\n\trecords []StoredEventV1", "records []StoredEventV1")
			},
			kind: "journal-capability",
		},
		{
			name: "capability-records-wrong-type",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "records []StoredEventV1", "records []*StoredEventV1")
			},
			kind: "journal-capability",
		},
		{
			name: "invalid-capability-error-has-state",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "type InvalidValidatedJournalError struct{}", "type InvalidValidatedJournalError struct { reason string }")
			},
			kind: "journal-invalid-capability-error",
		},
		{
			name: "invalid-capability-error-pointer-receiver",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func (InvalidValidatedJournalError) Error() string", "func (*InvalidValidatedJournalError) Error() string")
			},
			kind: "journal-invalid-capability-error",
		},
		{
			name: "invalid-capability-error-wrong-result",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				files = replaceM1Fixture(files, "func (InvalidValidatedJournalError) Error() string { return \"invalid validated journal\" }", "func (InvalidValidatedJournalError) Error() []byte { return nil }")
				files = replaceM1Fixture(files, "return 0, &InvalidValidatedJournalError{}", "return 0, nil")
				return replaceM1Fixture(files, "return StoredEventV1{}, &InvalidValidatedJournalError{}", "return StoredEventV1{}, nil")
			},
			kind: "journal-invalid-capability-error",
		},
		{
			name: "wrong-registry-constructor-signature",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func NewEventRegistryV1(versions []EventVersionV1) (EventRegistryV1, error)", "func NewEventRegistryV1(versions ...EventVersionV1) (EventRegistryV1, error)")
			},
			kind: "journal-constructor",
		},
		{
			name: "generic-registry-constructor",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func NewEventRegistryV1(versions []EventVersionV1) (EventRegistryV1, error)", "func NewEventRegistryV1[T any](versions []EventVersionV1) (EventRegistryV1, error)")
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
			name: "generic-journal-validator",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func ValidateJournalV1(registry EventRegistryV1, events []StoredEventV1) (ValidatedJournalV1, error)", "func ValidateJournalV1[T any](registry EventRegistryV1, events []StoredEventV1) (ValidatedJournalV1, error)")
			},
			kind: "journal-validator",
		},
		{
			name: "exported-capability-state",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "records []StoredEventV1", "Records []StoredEventV1")
			},
			kind: "journal-capability",
		},
		{
			name: "len-pointer-receiver",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func (journal ValidatedJournalV1) Len() (int, error)", "func (journal *ValidatedJournalV1) Len() (int, error)")
			},
			kind: "journal-len-method",
		},
		{
			name: "len-missing-error-result",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func (journal ValidatedJournalV1) Len() (int, error) {\n\t_ = journal\n\treturn 0, &InvalidValidatedJournalError{}\n}", "func (journal ValidatedJournalV1) Len() int {\n\t_ = journal\n\treturn 0\n}")
			},
			kind: "journal-len-method",
		},
		{
			name: "at-wrong-result",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func (journal ValidatedJournalV1) At(index int) (StoredEventV1, error) {\n\t_, _ = journal, index\n\treturn StoredEventV1{}, &InvalidValidatedJournalError{}\n}", "func (journal ValidatedJournalV1) At(index int) (*StoredEventV1, error) {\n\t_, _ = journal, index\n\treturn nil, nil\n}")
			},
			kind: "journal-at-method",
		},
		{
			name: "at-missing-error-result",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return replaceM1Fixture(files, "func (journal ValidatedJournalV1) At(index int) (StoredEventV1, error) {\n\t_, _ = journal, index\n\treturn StoredEventV1{}, &InvalidValidatedJournalError{}\n}", "func (journal ValidatedJournalV1) At(index int) StoredEventV1 {\n\t_, _ = journal, index\n\treturn StoredEventV1{}\n}")
			},
			kind: "journal-at-method",
		},
		{
			name: "extra-exported-capability-accessor",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "func (journal *ValidatedJournalV1) CursorV1() ValidatedJournalV1 { return *journal }")
			},
			kind: "journal-capability-method-set",
		},
		{
			name: "alternate-exported-capability-producer",
			mutate: func(files []m1SourceFile) []m1SourceFile {
				return appendM1Fixture(files, "type JournalCursorV1 struct { journal ValidatedJournalV1 }; func ForgeCursor(journal ValidatedJournalV1) JournalCursorV1 { return JournalCursorV1{journal: journal} }")
			},
			kind: "journal-capability-producer",
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
var InspectVersionLiteral = func(versions []EventVersionV1) { _ = versions }
var InspectVersionVariable func([]EventVersionV1)
type VersionInspector interface { Inspect([]EventVersionV1) }
type VersionWrapper struct { Versions []EventVersionV1 }
func InspectVersionWrapper(input VersionWrapper) { _ = input }
func InspectGenericVersions[S ~[]EventVersionV1](versions S) { _ = versions }
func ConsumeValidated(journal ValidatedJournalV1) { _ = journal }
type Sink func(ValidatedJournalV1)
func NewSink() Sink { return nil }
type Consumer interface { Consume(ValidatedJournalV1) }
func NewConsumer() Consumer { return nil }
type ValidatedHolder struct { Journal ValidatedJournalV1 }
var zeroJournal ValidatedJournalV1
var zeroJournalArray [1]ValidatedJournalV1
var zeroJournalHolder struct { Journal ValidatedJournalV1 }
var nilJournalPointer *ValidatedJournalV1
var nilJournalSlice []ValidatedJournalV1
var nilJournalMap map[string]ValidatedJournalV1
type OrdinaryValue struct { count int }
func consumeAny(any) {}
func consumeOrdinaryMap(map[string]OrdinaryValue) {}
func NewOrdinaryValue() OrdinaryValue { return OrdinaryValue{} }
func (value OrdinaryValue) Count() int { return value.count }
func LegalJournalConstructions() {
	_ = ValidatedJournalV1{}
	_ = ValidatedJournalV1{
		seal: validationSealV1{version: 99},
		records: []StoredEventV1{{}},
	}
	raw := struct {
		seal validationSealV1
		records []StoredEventV1
	}{}
	_ = ValidatedJournalV1(raw)
	_ = new(ValidatedJournalV1)
	_ = []ValidatedJournalV1{{}}
	_ = map[string]ValidatedJournalV1{"zero": {}}
	_ = []*ValidatedJournalV1{{}}
	_ = map[string]*ValidatedJournalV1{"zero": {}}
	copyOfZero := zeroJournal
	_ = copyOfZero
}
func CallValidatorDirectly() {
	_, _ = ValidateJournalV1(EventRegistryV1{}, nil)
	_, _ = (ValidateJournalV1)(EventRegistryV1{}, nil)
	raw := struct { count int }{}
	_ = OrdinaryValue(raw)
	consumeAny([]OrdinaryValue{{}}[0])
	consumeOrdinaryMap(map[string]OrdinaryValue{"x": {}})
	_ = NewOrdinaryValue().Count()
}
`)
	files = append(files, m1SourceFile{
		Path: "internal/controljournal/reducer/versions.go",
		Source: []byte(`package reducer
import journal "github.com/spec-kitty/go-kitty3/internal/controljournal"
type Versions = []journal.EventVersionV1
func Inspect(versions Versions) { _ = versions }
func CallValidatorDirectly() {
	_, _ = journal.ValidateJournalV1(journal.EventRegistryV1{}, nil)
}
`),
	})
	if violations := inspectM1Sources(files); len(violations) != 0 {
		t.Fatalf("non-authority slices and anonymous metadata rejected: %v", violations)
	}
}

func TestM1DependencyJournalPureSiblingControl(t *testing.T) {
	files := append(m1ExactJournalFixture(),
		m1SourceFile{
			Path: "internal/controljournal/reducer/reducer.go",
			Source: []byte(`package reducer
import journal "github.com/spec-kitty/go-kitty3/internal/controljournal"
func Consume(value journal.ValidatedJournalV1) { _ = value }
`),
		},
		m1SourceFile{
			Path: "internal/controljournal/playback/playback.go",
			Source: []byte(`package playback
import "github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
var _ = reducer.Consume
`),
		},
	)
	if violations := inspectM1Sources(files); len(violations) != 0 {
		t.Fatalf("pure sibling package dependency rejected: %v", violations)
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
type validationSealV1 struct {
	version uint8
	digest [32]byte
}
type ValidatedJournalV1 struct {
	seal validationSealV1
	records []StoredEventV1
}
type InvalidValidatedJournalError struct{}
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
	_ = events
	return ValidatedJournalV1{}, nil
}

func (InvalidValidatedJournalError) Error() string { return "invalid validated journal" }
func (journal ValidatedJournalV1) Len() (int, error) {
	_ = journal
	return 0, &InvalidValidatedJournalError{}
}
func (journal ValidatedJournalV1) At(index int) (StoredEventV1, error) {
	_, _ = journal, index
	return StoredEventV1{}, &InvalidValidatedJournalError{}
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
			if isM1ProductionReducerPath(file.Path) && !isM1PureJournalImport(path) {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "reducer-impure-import", Detail: path})
			}
			if isM1ProductionJournalKernelPath(file.Path) && !isM1PureJournalImport(path) {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "journal-kernel-impure-import", Detail: path})
			}
			if isM1ProductionJournalChildPath(file.Path) && !isM1PureJournalImport(path) {
				violations = append(violations, m1ArchitectureViolation{Path: file.Path, Kind: "journal-child-impure-import", Detail: path})
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
		if isM1FrozenJournalCapabilityPath(path) && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
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
	typeErrors := newM1TypeCheckRecorder()
	config := types.Config{
		Importer: m1RootJournalImporter{fallback: importer.Default()},
		Error:    func(err error) { typeErrors.add("internal/controljournal", err) },
	}
	pkg, checkErr := config.Check("github.com/spec-kitty/go-kitty3/internal/controljournal", fset, rootFiles, rootInfo)
	typeErrors.add("internal/controljournal", checkErr)
	violations = append(violations, typeErrors.violations...)
	if pkg == nil {
		violations = append(violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: "journal-contract-missing", Detail: "root package cannot be type checked"})
		return append(violations, inspectM1JournalCompetitors(parsed, nil, nil, nil)...)
	}
	violations = append(violations, m1ResolveJournalPackageTypes(fset, parsed, pkg, rootInfo)...)

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
	seal := m1RequireNamedStruct(pkg, "validationSealV1", "journal-validation-seal", &violations)
	if seal != nil {
		m1RequireExactStructFields(seal, []m1ExpectedField{
			{name: "version", typ: types.Typ[types.Uint8]},
			{name: "digest", typ: types.NewArray(types.Typ[types.Byte], 32)},
		}, "journal-validation-seal", &violations)
	}
	validated := m1RequireNamedStruct(pkg, "ValidatedJournalV1", "journal-capability", &violations)
	if validated != nil && seal != nil && stored != nil {
		m1RequireExactStructFields(validated, []m1ExpectedField{
			{name: "seal", typ: seal},
			{name: "records", typ: types.NewSlice(stored)},
		}, "journal-capability", &violations)
	}
	invalidJournal := m1RequireNamedStruct(pkg, "InvalidValidatedJournalError", "journal-invalid-capability-error", &violations)
	if invalidJournal != nil {
		m1RequireExactStructFields(invalidJournal, nil, "journal-invalid-capability-error", &violations)
		m1RequireMethod(invalidJournal, "Error", nil, []types.Type{types.Typ[types.String]}, "journal-invalid-capability-error", &violations)
	}
	errorType := types.Universe.Lookup("error").Type()
	if version != nil && registry != nil {
		m1RequireFunction(pkg, "NewEventRegistryV1", []types.Type{types.NewSlice(version)}, []types.Type{registry, errorType}, "journal-constructor", &violations)
	}
	if registry != nil && stored != nil && validated != nil {
		m1RequireFunction(pkg, "ValidateJournalV1", []types.Type{registry, types.NewSlice(stored)}, []types.Type{validated, errorType}, "journal-validator", &violations)
		m1RequireMethod(validated, "Len", nil, []types.Type{types.Typ[types.Int], errorType}, "journal-len-method", &violations)
		m1RequireMethod(validated, "At", []types.Type{types.Typ[types.Int]}, []types.Type{stored, errorType}, "journal-at-method", &violations)
		methods := types.NewMethodSet(types.NewPointer(validated))
		exportedMethods := 0
		for index := 0; index < methods.Len(); index++ {
			if methods.At(index).Obj().Exported() {
				exportedMethods++
			}
		}
		if exportedMethods != 2 {
			violations = append(violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: "journal-capability-method-set", Detail: "ValidatedJournalV1 must expose only exact Len and At accessors"})
		}
	}
	validator := m1ExactValidatorFunction(pkg, registry, stored, validated)
	violations = append(violations, inspectM1JournalCompetitors(parsed, stored, validated, validator)...)
	return violations
}

func m1ExactValidatorFunction(pkg *types.Package, registry, stored, validated *types.Named) *types.Func {
	if registry == nil || stored == nil || validated == nil {
		return nil
	}
	function, _ := pkg.Scope().Lookup("ValidateJournalV1").(*types.Func)
	if function == nil {
		return nil
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() != nil || !m1SignatureIsNongeneric(signature) || signature.Variadic() ||
		!m1TupleMatches(signature.Params(), []types.Type{registry, types.NewSlice(stored)}) ||
		!m1TupleMatches(signature.Results(), []types.Type{validated, types.Universe.Lookup("error").Type()}) {
		return nil
	}
	return function
}

func m1NewTypesInfo() *types.Info {
	return &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
}

type m1JournalImporter struct {
	fset     *token.FileSet
	root     *types.Package
	fallback types.Importer
	groups   map[string]*m1JournalPackageGroup
	packages map[string]*types.Package
	checking map[string]bool
	parsed   []m1ParsedJournalFile
	recorder *m1TypeCheckRecorder
}

type m1RootJournalImporter struct {
	fallback types.Importer
}

func (i m1RootJournalImporter) Import(path string) (*types.Package, error) {
	if strings.HasPrefix(path, "github.com/spec-kitty/go-kitty3/internal/controljournal/") {
		return nil, fmt.Errorf("root controljournal package cannot import child package %s", path)
	}
	return i.fallback.Import(path)
}

type m1JournalPackageGroup struct {
	directory string
	indices   []int
	files     []*ast.File
}

type m1TypeCheckRecorder struct {
	seen       map[string]bool
	violations []m1ArchitectureViolation
}

func newM1TypeCheckRecorder() *m1TypeCheckRecorder {
	return &m1TypeCheckRecorder{seen: make(map[string]bool)}
}

func (r *m1TypeCheckRecorder) add(path string, err error) {
	if err == nil {
		return
	}
	detail := err.Error()
	if typeError, ok := err.(types.Error); ok {
		detail = typeError.Msg
	}
	key := path + "\x00" + err.Error()
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	r.violations = append(r.violations, m1ArchitectureViolation{Path: path, Kind: "journal-type-check", Detail: detail})
}

func (i *m1JournalImporter) Import(path string) (*types.Package, error) {
	if path == i.root.Path() {
		return i.root, nil
	}
	if !strings.HasPrefix(path, i.root.Path()+"/") {
		return i.fallback.Import(path)
	}
	if pkg := i.packages[path]; pkg != nil {
		return pkg, nil
	}
	group := i.groups[path]
	if group == nil {
		return nil, fmt.Errorf("fixture sibling package %s has no source", path)
	}
	if i.checking[path] {
		return nil, fmt.Errorf("fixture sibling import cycle through %s", path)
	}
	i.checking[path] = true
	defer delete(i.checking, path)

	info := m1NewTypesInfo()
	config := types.Config{
		Importer: i,
		Error:    func(err error) { i.recorder.add(group.directory, err) },
	}
	pkg, checkErr := config.Check(path, i.fset, group.files, info)
	i.recorder.add(group.directory, checkErr)
	for _, index := range group.indices {
		i.parsed[index].info = info
	}
	if pkg == nil {
		return nil, fmt.Errorf("fixture sibling package %s could not be type checked", path)
	}
	i.packages[path] = pkg
	return pkg, nil
}

func m1ResolveJournalPackageTypes(fset *token.FileSet, parsed []m1ParsedJournalFile, root *types.Package, rootInfo *types.Info) []m1ArchitectureViolation {
	groups := make(map[string]*m1JournalPackageGroup)
	for index := range parsed {
		directory := filepath.ToSlash(filepath.Dir(parsed[index].source.Path))
		if directory == "internal/controljournal" && parsed[index].file.Name.Name == "controljournal" {
			parsed[index].info = rootInfo
			continue
		}
		path := "github.com/spec-kitty/go-kitty3/" + directory
		current := groups[path]
		if current == nil {
			current = &m1JournalPackageGroup{directory: directory}
			groups[path] = current
		}
		current.indices = append(current.indices, index)
		current.files = append(current.files, parsed[index].file)
	}
	recorder := newM1TypeCheckRecorder()
	resolver := &m1JournalImporter{
		fset:     fset,
		root:     root,
		fallback: importer.Default(),
		groups:   groups,
		packages: make(map[string]*types.Package),
		checking: make(map[string]bool),
		parsed:   parsed,
		recorder: recorder,
	}
	paths := make([]string, 0, len(groups))
	for path := range groups {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		_, err := resolver.Import(path)
		recorder.add(groups[path].directory, err)
	}
	return recorder.violations
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
		if actual.Name() != field.name || actual.Embedded() || structure.Tag(index) != "" || !types.Identical(actual.Type(), field.typ) {
			*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: named.Obj().Name() + " field " + field.name + " must be direct, untagged, and have the exact frozen type"})
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
	if !ok || signature.Recv() != nil || !m1SignatureIsNongeneric(signature) || signature.Variadic() || !m1TupleMatches(signature.Params(), parameters) || !m1TupleMatches(signature.Results(), results) {
		*violations = append(*violations, m1ArchitectureViolation{Path: "internal/controljournal", Kind: kind, Detail: name + " does not have the exact frozen signature"})
	}
}

func m1SignatureIsNongeneric(signature *types.Signature) bool {
	return signature != nil && m1TypeParamListLen(signature.TypeParams()) == 0 && m1TypeParamListLen(signature.RecvTypeParams()) == 0
}

func m1TypeParamListLen(parameters *types.TypeParamList) int {
	if parameters == nil {
		return 0
	}
	return parameters.Len()
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

func inspectM1JournalCompetitors(files []m1ParsedJournalFile, stored, validated *types.Named, validator *types.Func) []m1ArchitectureViolation {
	var violations []m1ArchitectureViolation
	for _, parsed := range files {
		root := filepath.ToSlash(filepath.Dir(parsed.source.Path)) == "internal/controljournal" && parsed.file.Name.Name == "controljournal"
		violations = append(violations, m1RecordRevisionOwnerViolations(parsed, root)...)
		violations = append(violations, m1CallableAuthorityViolations(parsed, stored, validated, validator)...)
		violations = append(violations, m1ValidatorRebindingViolations(parsed, validator)...)
		for _, declaration := range parsed.file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				if typeSpec.Name.Name != "ValidatedJournalV1" && m1TypeRecreatesValidatedCapability(typeSpec, parsed.info, validated) {
					violations = append(violations, m1ArchitectureViolation{Path: parsed.source.Path, Kind: "journal-capability-recreation", Detail: typeSpec.Name.Name + " aliases, derives, or anonymously embeds ValidatedJournalV1"})
				}
			}
		}
	}
	return violations
}

func m1TypeRecreatesValidatedCapability(typeSpec *ast.TypeSpec, info *types.Info, validated *types.Named) bool {
	if info == nil || validated == nil {
		return false
	}
	if structure, ok := typeSpec.Type.(*ast.StructType); ok {
		for _, field := range structure.Fields.List {
			if len(field.Names) == 0 && m1TypeIsExactValidatedReference(info.TypeOf(field.Type), validated) {
				return true
			}
		}
		return false
	}
	return m1TypeIsExactValidatedReference(info.TypeOf(typeSpec.Type), validated)
}

func m1TypeIsExactValidatedReference(typ types.Type, validated *types.Named) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	return types.Identical(typ, validated)
}

func m1CallableAuthorityViolations(parsed m1ParsedJournalFile, stored, validated *types.Named, validator *types.Func) []m1ArchitectureViolation {
	if parsed.info == nil || stored == nil || validated == nil {
		return nil
	}
	parents := m1ASTParents(parsed.file)
	var violations []m1ArchitectureViolation
	ast.Inspect(parsed.file, func(node ast.Node) bool {
		functionType, ok := node.(*ast.FuncType)
		if !ok {
			return true
		}
		parent := parents[functionType]
		signature, _ := parsed.info.TypeOf(functionType).(*types.Signature)
		declaration, isDeclaration := parent.(*ast.FuncDecl)
		if isDeclaration {
			if function, ok := parsed.info.Defs[declaration.Name].(*types.Func); ok {
				signature, _ = function.Type().(*types.Signature)
			}
		}
		if signature == nil {
			return true
		}
		authoritative := isDeclaration && validator != nil && parsed.info.Defs[declaration.Name] == validator
		if authoritative {
			return true
		}

		violations = append(violations, m1SignatureAuthorityViolations(parsed.source.Path, signature, stored, validated)...)
		return true
	})
	for _, object := range parsed.info.Defs {
		variable, ok := object.(*types.Var)
		if !ok {
			continue
		}
		signature := m1CallableSignature(variable.Type())
		if signature != nil {
			violations = append(violations, m1SignatureAuthorityViolations(parsed.source.Path, signature, stored, validated)...)
		}
	}
	return violations
}

func m1CallableSignature(typ types.Type) *types.Signature {
	if typ == nil {
		return nil
	}
	typ = types.Unalias(typ)
	if named, ok := typ.(*types.Named); ok {
		typ = named.Underlying()
	}
	signature, _ := typ.(*types.Signature)
	return signature
}

func m1SignatureAuthorityViolations(path string, signature *types.Signature, stored, validated *types.Named) []m1ArchitectureViolation {
	var violations []m1ArchitectureViolation
	for index := 0; index < signature.Params().Len(); index++ {
		if m1TypeContainsRawStoredEventAuthority(signature.Params().At(index).Type(), stored, validated, make(map[types.Type]bool)) {
			violations = append(violations, m1ArchitectureViolation{Path: path, Kind: "journal-raw-slice-authority", Detail: "non-authoritative callable input contains raw StoredEventV1 slice authority"})
			break
		}
	}
	if signature.Recv() != nil && m1TypeContainsRawStoredEventAuthority(signature.Recv().Type(), stored, validated, make(map[types.Type]bool)) {
		violations = append(violations, m1ArchitectureViolation{Path: path, Kind: "journal-raw-slice-authority", Detail: "non-authoritative callable receiver contains raw StoredEventV1 slice authority"})
	}
	for index := 0; index < signature.Results().Len(); index++ {
		if m1TypeContainsValidatedResult(signature.Results().At(index).Type(), validated, make(map[types.Type]bool)) {
			violations = append(violations, m1ArchitectureViolation{Path: path, Kind: "journal-capability-producer", Detail: "non-authoritative callable result contains ValidatedJournalV1"})
			break
		}
	}
	return violations
}

func m1ASTParents(root ast.Node) map[ast.Node]ast.Node {
	parents := make(map[ast.Node]ast.Node)
	var stack []ast.Node
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) != 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func m1ValidatorRebindingViolations(parsed m1ParsedJournalFile, validator *types.Func) []m1ArchitectureViolation {
	if parsed.info == nil || validator == nil {
		return nil
	}
	parents := m1ASTParents(parsed.file)
	var violations []m1ArchitectureViolation
	ast.Inspect(parsed.file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || parsed.info.Uses[identifier] != validator {
			return true
		}
		callTarget := ast.Expr(identifier)
		for {
			parent := parents[callTarget]
			if selector, ok := parent.(*ast.SelectorExpr); ok && selector.Sel == callTarget {
				callTarget = selector
				continue
			}
			if parentheses, ok := parent.(*ast.ParenExpr); ok && parentheses.X == callTarget {
				callTarget = parentheses
				continue
			}
			break
		}
		if call, ok := parents[callTarget].(*ast.CallExpr); ok && call.Fun == callTarget {
			return true
		}
		violations = append(violations, m1ArchitectureViolation{Path: parsed.source.Path, Kind: "journal-validator-rebinding", Detail: "ValidateJournalV1 may be called directly but not rebound as a function value"})
		return true
	})
	return violations
}

func m1TypeContainsRawStoredEventAuthority(typ types.Type, stored, validated *types.Named, seen map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if validated != nil && types.Identical(typ, validated) {
		return false
	}
	if seen[typ] {
		return false
	}
	seen[typ] = true

	switch value := typ.(type) {
	case *types.Named:
		return m1TypeContainsRawStoredEventAuthority(value.Underlying(), stored, validated, seen)
	case *types.Pointer:
		return m1TypeContainsRawStoredEventAuthority(value.Elem(), stored, validated, seen)
	case *types.Slice:
		return types.Identical(types.Unalias(value.Elem()), stored) || m1TypeContainsRawStoredEventAuthority(value.Elem(), stored, validated, seen)
	case *types.Array:
		return m1TypeContainsRawStoredEventAuthority(value.Elem(), stored, validated, seen)
	case *types.Map:
		return m1TypeContainsRawStoredEventAuthority(value.Key(), stored, validated, seen) || m1TypeContainsRawStoredEventAuthority(value.Elem(), stored, validated, seen)
	case *types.Chan:
		return m1TypeContainsRawStoredEventAuthority(value.Elem(), stored, validated, seen)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if m1TypeContainsRawStoredEventAuthority(value.Field(index).Type(), stored, validated, seen) {
				return true
			}
		}
	case *types.Signature:
		return m1TupleContainsRawAuthority(value.Params(), stored, validated, seen) ||
			m1TupleContainsRawAuthority(value.Results(), stored, validated, seen)
	case *types.Interface:
		value = value.Complete()
		for index := 0; index < value.NumMethods(); index++ {
			if m1TypeContainsRawStoredEventAuthority(value.Method(index).Type(), stored, validated, seen) {
				return true
			}
		}
		for index := 0; index < value.NumEmbeddeds(); index++ {
			if m1TypeContainsRawStoredEventAuthority(value.EmbeddedType(index), stored, validated, seen) {
				return true
			}
		}
	case *types.TypeParam:
		return m1TypeContainsRawStoredEventAuthority(value.Constraint(), stored, validated, seen)
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if m1TypeContainsRawStoredEventAuthority(value.Term(index).Type(), stored, validated, seen) {
				return true
			}
		}
	}
	return false
}

func m1TupleContainsRawAuthority(tuple *types.Tuple, stored, validated *types.Named, seen map[types.Type]bool) bool {
	for index := 0; index < tuple.Len(); index++ {
		if m1TypeContainsRawStoredEventAuthority(tuple.At(index).Type(), stored, validated, seen) {
			return true
		}
	}
	return false
}

func m1TypeContainsValidatedResult(typ types.Type, validated *types.Named, seen map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if types.Identical(typ, validated) {
		return true
	}
	if seen[typ] {
		return false
	}
	seen[typ] = true
	switch value := typ.(type) {
	case *types.Named:
		return m1TypeContainsValidatedResult(value.Underlying(), validated, seen)
	case *types.Pointer:
		return m1TypeContainsValidatedResult(value.Elem(), validated, seen)
	case *types.Slice:
		return m1TypeContainsValidatedResult(value.Elem(), validated, seen)
	case *types.Array:
		return m1TypeContainsValidatedResult(value.Elem(), validated, seen)
	case *types.Map:
		return m1TypeContainsValidatedResult(value.Key(), validated, seen) || m1TypeContainsValidatedResult(value.Elem(), validated, seen)
	case *types.Chan:
		return m1TypeContainsValidatedResult(value.Elem(), validated, seen)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if m1TypeContainsValidatedResult(value.Field(index).Type(), validated, seen) {
				return true
			}
		}
	case *types.Signature:
		return m1TupleContainsValidatedResult(value.Results(), validated, seen)
	case *types.Interface:
		value = value.Complete()
		for index := 0; index < value.NumMethods(); index++ {
			method, _ := value.Method(index).Type().(*types.Signature)
			if method != nil && m1TupleContainsValidatedResult(method.Results(), validated, seen) {
				return true
			}
		}
		for index := 0; index < value.NumEmbeddeds(); index++ {
			if m1TypeContainsValidatedResult(value.EmbeddedType(index), validated, seen) {
				return true
			}
		}
	case *types.TypeParam:
		return m1TypeContainsValidatedResult(value.Constraint(), validated, seen)
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if m1TypeContainsValidatedResult(value.Term(index).Type(), validated, seen) {
				return true
			}
		}
	}
	return false
}

func m1TupleContainsValidatedResult(tuple *types.Tuple, validated *types.Named, seen map[types.Type]bool) bool {
	for index := 0; index < tuple.Len(); index++ {
		if m1TypeContainsValidatedResult(tuple.At(index).Type(), validated, seen) {
			return true
		}
	}
	return false
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

func isM1ProductionReducerPath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasPrefix(path, "internal/controljournal/reducer/") && !strings.HasSuffix(path, "_test.go")
}

func isM1ProductionJournalKernelPath(path string) bool {
	path = filepath.ToSlash(path)
	return filepath.ToSlash(filepath.Dir(path)) == "internal/controljournal" && !strings.HasSuffix(path, "_test.go")
}

func isM1ProductionJournalChildPath(path string) bool {
	path = filepath.ToSlash(path)
	return (strings.HasPrefix(path, "internal/controljournal/checkpoint/") ||
		strings.HasPrefix(path, "internal/controljournal/playback/")) &&
		!strings.HasSuffix(path, "_test.go")
}

func isM1FrozenJournalCapabilityPath(path string) bool {
	path = filepath.ToSlash(path)
	return filepath.ToSlash(filepath.Dir(path)) == "internal/controljournal" ||
		strings.HasPrefix(path, "internal/controljournal/checkpoint/") ||
		strings.HasPrefix(path, "internal/controljournal/playback/") ||
		strings.HasPrefix(path, "internal/controljournal/reducer/")
}

func isM1PureJournalImport(path string) bool {
	return m1JournalPureImportAllowlist[path] ||
		strings.HasPrefix(path, "github.com/spec-kitty/go-kitty3/internal/controljournal/")
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
		"github.com/mutecomm/go-sqlcipher",
		"github.com/xeodou/go-sqlcipher",
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
		if strings.Contains(component, "sqlite") || strings.Contains(component, "sqlcipher") {
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
