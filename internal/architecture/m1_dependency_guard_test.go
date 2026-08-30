package architecture

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
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
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)
func CompareCommittedEventOrder(left, right CommittedEvent) int {
	if left.RecordRevision < right.RecordRevision { return -1 }
	if left.RecordRevision > right.RecordRevision { return 1 }
	if left.EventIndex < right.EventIndex { return -1 }
	if left.EventIndex > right.EventIndex { return 1 }
	if left.EventID < right.EventID { return -1 }
	if left.EventID > right.EventID { return 1 }
	return 0
}
func CanonicalEventDigest(event CommittedEvent) string {
	payload := fmt.Sprintf("%d|%d|%s|%s|%s|%d|%d|%s|%d", event.RecordRevision, event.EventIndex, event.EventID, event.OperationID, event.EventType, event.EnvelopeVersion, event.EventSchemaVersion, event.ReducerVersion, event.EventCount)
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}
func SupportedEventVersion(eventType string, eventVersion uint32, reducerVersion string) bool {
	return eventType == "work-claimed" && eventVersion == 1 && reducerVersion == "work-v1"
}
func ValidateCommittedEventGroup(events []CommittedEvent) error {
	if len(events) == 0 { return fmt.Errorf("empty group") }
	seenIDs, seenTuples := map[string]bool{}, map[string]bool{}
	for index, event := range events {
		if event.EnvelopeVersion != 1 || !SupportedEventVersion(event.EventType, event.EventSchemaVersion, event.ReducerVersion) { return fmt.Errorf("unsupported version") }
		if event.RecordRevision == 0 || event.OperationID == "" || event.EventIndex >= event.EventCount || event.EventID == "" { return fmt.Errorf("malformed group") }
		if event.CanonicalEventDigest != CanonicalEventDigest(event) { return fmt.Errorf("digest mismatch") }
		tuple := fmt.Sprintf("%d/%d/%s", event.RecordRevision, event.EventIndex, event.EventID)
		if seenIDs[event.EventID] || seenTuples[tuple] { return fmt.Errorf("duplicate event") }
		seenIDs[event.EventID], seenTuples[tuple] = true, true
		if index == 0 {
			if event.EventIndex != 0 { return fmt.Errorf("missing first index") }
			continue
		}
		previous := events[index-1]
		if CompareCommittedEventOrder(previous, event) >= 0 { return fmt.Errorf("nonmonotonic group") }
		if previous.RecordRevision == event.RecordRevision && (previous.OperationID != event.OperationID || previous.EventCount != event.EventCount || event.EventIndex != previous.EventIndex+1) { return fmt.Errorf("noncontiguous group") }
		if previous.RecordRevision != event.RecordRevision && event.EventIndex != 0 { return fmt.Errorf("missing record start") }
	}
	return nil
}
`),
		},
	}
	if violations := inspectM1Sources(clean); len(violations) != 0 {
		t.Fatalf("clean production journal seams rejected: %v", violations)
	}
	renamed := make([]m1SourceFile, len(clean))
	for index, file := range clean {
		source := string(file.Source)
		for original, replacement := range map[string]string{
			"CompareCommittedEventOrder(":  "threeWay(",
			"CanonicalEventDigest(":        "foldBytes(",
			"SupportedEventVersion(":       "registryContains(",
			"ValidateCommittedEventGroup(": "refuseBadBatch(",
		} {
			source = strings.ReplaceAll(source, original, replacement)
		}
		renamed[index] = m1SourceFile{Path: file.Path, Source: []byte(source)}
	}
	if violations := inspectM1Sources(renamed); len(violations) != 0 {
		t.Fatalf("behaviorally valid seams with arbitrary function names rejected: %v", violations)
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

func TestM1DependencyJournalBehavioralBypassMutants(t *testing.T) {
	base := `package controljournal
type CommittedEvent struct { RecordRevision uint64; EventIndex uint32; EventID string; OperationID string; EventType string; EnvelopeVersion uint32; EventSchemaVersion uint32; ReducerVersion string; EventCount uint32; CanonicalEventDigest string }
`
	correctComparatorSyntax := `
func CompareCommittedEventOrder(left, right CommittedEvent) int {
	if left.RecordRevision < right.RecordRevision { return -1 }; if left.RecordRevision > right.RecordRevision { return 1 }
	if left.EventIndex < right.EventIndex { return -1 }; if left.EventIndex > right.EventIndex { return 1 }
	if left.EventID < right.EventID { return -1 }; if left.EventID > right.EventID { return 1 }; return 0
}
`
	decoratedValidator := `
func ValidateCommittedEventGroup(event CommittedEvent) error {
	if event.EnvelopeVersion != 1 || event.EventType == "" || event.EventSchemaVersion != 1 || event.ReducerVersion != "v1" { return errInvalid }
	if !SupportedEventVersion(event.EventType, event.EventSchemaVersion, event.ReducerVersion) { return errInvalid }
	if event.RecordRevision == 0 || event.OperationID == "" || event.EventIndex >= event.EventCount || event.EventID == "" { return errInvalid }
	if !CanonicalEventDigestMatches(event.CanonicalEventDigest) { return errInvalid }; return nil
}
func SupportedEventVersion(string, uint32, string) bool { return true }
func CanonicalEventDigestMatches(digest string) bool { return digest != "" }
var errInvalid error
`
	wholeGroupValidator := func(versionGuard, digestGuard string) string {
		source := `
type invalidJournalError struct{}
func (invalidJournalError) Error() string { return "invalid" }
var errInvalid error = invalidJournalError{}
func CanonicalEventDigest(event CommittedEvent) string { return event.EventID + "|" + event.OperationID + "|" + event.EventType + "|" + event.ReducerVersion }
func SupportedEventVersion(eventType string, eventVersion uint32, reducerVersion string) bool { return eventType == "work-claimed" && eventVersion == 1 && reducerVersion == "work-v1" }
func ValidateCommittedEventGroup(events []CommittedEvent) error {
	if len(events) == 0 { return errInvalid }
	seen := map[string]bool{}
	for index, event := range events {
		VERSION_GUARD
		if event.RecordRevision == 0 || event.OperationID == "" || event.EventIndex >= event.EventCount || event.EventID == "" { return errInvalid }
		DIGEST_GUARD
		if seen[event.EventID] { return errInvalid }; seen[event.EventID] = true
		if index == 0 { if event.EventIndex != 0 { return errInvalid }; continue }
		previous := events[index-1]
		if CompareCommittedEventOrder(previous, event) >= 0 { return errInvalid }
		if previous.RecordRevision == event.RecordRevision && (previous.OperationID != event.OperationID || previous.EventCount != event.EventCount || event.EventIndex != previous.EventIndex+1) { return errInvalid }
		if previous.RecordRevision != event.RecordRevision && event.EventIndex != 0 { return errInvalid }
	}
	return nil
}
`
		source = strings.Replace(source, "VERSION_GUARD", versionGuard, 1)
		return strings.Replace(source, "DIGEST_GUARD", digestGuard, 1)
	}
	validVersionGuard := `if event.EnvelopeVersion != 1 || !SupportedEventVersion(event.EventType, event.EventSchemaVersion, event.ReducerVersion) { return errInvalid }`
	validDigestGuard := `if event.CanonicalEventDigest != CanonicalEventDigest(event) { return errInvalid }`
	tests := []struct {
		id, source, kind, detail string
	}{
		{
			id: "invalid-event-order/decorated-wrong-comparator",
			source: base + `
func CompareCommittedEventOrder(left, right CommittedEvent) int {
	if left.RecordRevision < right.RecordRevision { return -1 }
	if left.EventIndex < right.EventIndex { return -1 }
	if left.EventID < right.EventID { return -1 }; return 0
}
` + wholeGroupValidator(validVersionGuard, validDigestGuard),
			kind:   "event-order-seam",
			detail: "executable comparator violates",
		},
		{
			id:     "invalid-event-order/single-event-group-validator",
			source: base + correctComparatorSyntax + decoratedValidator,
			kind:   "event-group-refusal-seam",
			detail: "no func([]T) error validator",
		},
		{
			id: "version-refusal/nonempty-only-digest-verifier",
			source: base + correctComparatorSyntax + wholeGroupValidator(validVersionGuard,
				`if event.CanonicalEventDigest == "" { return errInvalid }`),
			kind:   "event-digest-refusal-seam",
			detail: "executable group validator admits a canonical digest mismatch",
		},
		{
			id: "version-refusal/ignored-always-true-verifier",
			source: base + correctComparatorSyntax + wholeGroupValidator(
				`_ = SupportedEventVersion(event.EventType, event.EventSchemaVersion, event.ReducerVersion); if event.EnvelopeVersion != event.EnvelopeVersion || event.EventType != event.EventType || event.EventSchemaVersion != event.EventSchemaVersion || event.ReducerVersion != event.ReducerVersion { return errInvalid }`,
				validDigestGuard),
			kind:   "event-version-refusal-seam",
			detail: "executable group validator admits an unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			violations := inspectM1Sources([]m1SourceFile{{Path: "internal/controljournal/model.go", Source: []byte(test.source)}})
			detail, found := findM1Violation(violations, test.kind)
			if !found {
				t.Fatalf("behaviorally invalid production seam survived %s guard: %v", test.kind, violations)
			}
			if !strings.Contains(detail, test.detail) {
				t.Fatalf("%s was rejected without exercising the intended semantic boundary: %q", test.kind, detail)
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
	var functions []*ast.FuncDecl
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
				functions = append(functions, function)
			}
		}
	}

	seam := discoverM1ExecutableJournalSeam(functions)
	var violations []m1ArchitectureViolation
	appendViolation := func(kind, detail string) {
		violations = append(violations, m1ArchitectureViolation{Path: model.Path, Kind: kind, Detail: detail})
	}
	if len(seam.Comparators) == 0 {
		appendViolation("event-order-seam", "no func(T, T) int comparator is executable for the committed event type")
	}
	if len(seam.Validators) == 0 {
		appendViolation("event-group-refusal-seam", "no func([]T) error validator observes a whole committed event group")
		appendViolation("event-version-refusal-seam", "no whole-group validator can refuse unknown event and reducer versions")
	}
	if len(seam.Digests) == 0 {
		appendViolation("event-digest-refusal-seam", "no func(T) string canonical digest calculator is executable for mismatch probes")
	}
	if seam.EventType == "" || (len(seam.Comparators) == 0 && (len(seam.Validators) == 0 || len(seam.Digests) == 0)) {
		return violations
	}

	probeFailures, probeDetail := executeM1JournalSemanticProbe(files, seam)
	for _, kind := range []string{"event-order-seam", "event-group-refusal-seam", "event-version-refusal-seam", "event-digest-refusal-seam"} {
		if probeFailures[kind] {
			appendViolation(kind, probeDetail[kind])
		}
	}
	return violations
}

type m1DigestSeam struct {
	Name       string
	PointerArg bool
	ReturnsErr bool
}

type m1ExecutableJournalSeam struct {
	EventType   string
	Comparators []string
	Validators  []string
	Digests     []m1DigestSeam
}

func discoverM1ExecutableJournalSeam(functions []*ast.FuncDecl) m1ExecutableJournalSeam {
	byType := make(map[string]*m1ExecutableJournalSeam)
	for _, function := range functions {
		parameters := m1FieldTypeNames(function.Type.Params)
		results := m1FieldTypeNames(function.Type.Results)
		if len(parameters) == 2 && parameters[0] == parameters[1] && len(results) == 1 && results[0] == "int" && isM1NamedType(parameters[0]) {
			seam := m1SeamForType(byType, parameters[0])
			seam.Comparators = append(seam.Comparators, function.Name.Name)
		}
		if len(parameters) == 1 && strings.HasPrefix(parameters[0], "[]") && len(results) == 1 && results[0] == "error" && isM1NamedType(strings.TrimPrefix(parameters[0], "[]")) {
			seam := m1SeamForType(byType, strings.TrimPrefix(parameters[0], "[]"))
			seam.Validators = append(seam.Validators, function.Name.Name)
		}
		if len(parameters) == 1 && (len(results) == 1 || len(results) == 2) && results[0] == "string" && (len(results) == 1 || results[1] == "error") {
			eventType := strings.TrimPrefix(parameters[0], "*")
			if isM1NamedType(eventType) {
				seam := m1SeamForType(byType, eventType)
				seam.Digests = append(seam.Digests, m1DigestSeam{Name: function.Name.Name, PointerArg: strings.HasPrefix(parameters[0], "*"), ReturnsErr: len(results) == 2})
			}
		}
	}
	var best m1ExecutableJournalSeam
	bestScore := -1
	for _, seam := range byType {
		score := 0
		if len(seam.Comparators) > 0 {
			score += 4
		}
		if len(seam.Validators) > 0 {
			score += 2
		}
		if len(seam.Digests) > 0 {
			score++
		}
		if score > bestScore || (score == bestScore && seam.EventType < best.EventType) {
			best, bestScore = *seam, score
		}
	}
	return best
}

func m1SeamForType(byType map[string]*m1ExecutableJournalSeam, eventType string) *m1ExecutableJournalSeam {
	if byType[eventType] == nil {
		byType[eventType] = &m1ExecutableJournalSeam{EventType: eventType}
	}
	return byType[eventType]
}

func isM1NamedType(name string) bool {
	if name == "" || strings.ContainsAny(name, "[]{}().") {
		return false
	}
	switch name {
	case "bool", "byte", "complex64", "complex128", "error", "float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return false
	}
	for index, character := range name {
		if character != '_' && !('a' <= character && character <= 'z') && !('A' <= character && character <= 'Z') && !(index > 0 && '0' <= character && character <= '9') {
			return false
		}
	}
	return true
}

func m1FieldTypeNames(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var names []string
	for _, field := range fields.List {
		name := m1SimpleTypeName(field.Type)
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			names = append(names, name)
		}
	}
	return names
}

func m1SimpleTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return "*" + m1SimpleTypeName(value.X)
	case *ast.ArrayType:
		if value.Len == nil {
			return "[]" + m1SimpleTypeName(value.Elt)
		}
	}
	return ""
}

func executeM1JournalSemanticProbe(files []m1SourceFile, seam m1ExecutableJournalSeam) (map[string]bool, map[string]string) {
	enabled := map[string]bool{
		"event-order-seam":           len(seam.Comparators) > 0,
		"event-group-refusal-seam":   len(seam.Validators) > 0 && len(seam.Digests) > 0,
		"event-version-refusal-seam": len(seam.Validators) > 0 && len(seam.Digests) > 0,
		"event-digest-refusal-seam":  len(seam.Validators) > 0 && len(seam.Digests) > 0,
	}
	failures := make(map[string]bool)
	details := make(map[string]string)
	tempDir, err := os.MkdirTemp("", "m1-journal-contract-")
	if err != nil {
		return m1ProbeInfrastructureFailure(enabled, "create semantic probe: "+err.Error())
	}
	defer os.RemoveAll(tempDir)
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module github.com/spec-kitty/go-kitty3\n\ngo 1.26\n"), 0o600); err != nil {
		return m1ProbeInfrastructureFailure(enabled, "write semantic probe module: "+err.Error())
	}
	for _, file := range files {
		path := filepath.ToSlash(file.Path)
		if filepath.ToSlash(filepath.Dir(path)) != "internal/controljournal" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		if err := os.WriteFile(filepath.Join(tempDir, filepath.Base(path)), file.Source, 0o600); err != nil {
			return m1ProbeInfrastructureFailure(enabled, "write semantic probe source: "+err.Error())
		}
	}
	probe := renderM1JournalSemanticProbe(seam, enabled)
	if err := os.WriteFile(filepath.Join(tempDir, "m1_contract_probe_test.go"), []byte(probe), 0o600); err != nil {
		return m1ProbeInfrastructureFailure(enabled, "write semantic probe test: "+err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", ".", "-run", "^TestM1ArchitectureJournalSemantics$", "-count=1")
	command.Dir = tempDir
	command.Env = append(os.Environ(), "GOWORK=off")
	output, commandErr := command.CombinedOutput()
	markers := map[string]string{
		"event-order-seam":           "M1_SEMANTIC_ORDER",
		"event-group-refusal-seam":   "M1_SEMANTIC_GROUP",
		"event-version-refusal-seam": "M1_SEMANTIC_VERSION",
		"event-digest-refusal-seam":  "M1_SEMANTIC_DIGEST",
	}
	for kind, marker := range markers {
		if strings.Contains(string(output), marker) {
			failures[kind] = true
			details[kind] = m1SemanticFailureDetail(kind)
		}
	}
	if commandErr != nil && len(failures) == 0 {
		detail := strings.TrimSpace(string(output))
		if len(detail) > 600 {
			detail = detail[:600]
		}
		if ctx.Err() != nil {
			detail = "semantic probe timed out"
		}
		return m1ProbeInfrastructureFailure(enabled, "semantic probe could not execute: "+detail)
	}
	return failures, details
}

func m1ProbeInfrastructureFailure(enabled map[string]bool, detail string) (map[string]bool, map[string]string) {
	failures := make(map[string]bool)
	details := make(map[string]string)
	for kind, active := range enabled {
		if active {
			failures[kind] = true
			details[kind] = detail
		}
	}
	return failures, details
}

func m1SemanticFailureDetail(kind string) string {
	switch kind {
	case "event-order-seam":
		return "executable comparator violates three-way (RecordRevision, EventIndex, EventID) ordering"
	case "event-group-refusal-seam":
		return "executable group validator admits missing, duplicate, mixed-operation, or nonmonotonic events"
	case "event-version-refusal-seam":
		return "executable group validator admits an unknown envelope, event, or reducer version"
	default:
		return "executable group validator admits a canonical digest mismatch"
	}
}

func renderM1JournalSemanticProbe(seam m1ExecutableJournalSeam, enabled map[string]bool) string {
	comparators := make([]string, len(seam.Comparators))
	copy(comparators, seam.Comparators)
	validators := make([]string, len(seam.Validators))
	copy(validators, seam.Validators)
	digests := make([]string, 0, len(seam.Digests))
	for _, digest := range seam.Digests {
		argument := "event"
		if digest.PointerArg {
			argument = "&event"
		}
		body := "return " + digest.Name + "(" + argument + ")"
		if digest.ReturnsErr {
			body = "value, err := " + digest.Name + "(" + argument + "); if err != nil { return \"\" }; return value"
		}
		digests = append(digests, "func(event "+seam.EventType+") string { "+body+" }")
	}
	return fmt.Sprintf(`package controljournal

import "testing"

func TestM1ArchitectureJournalSemantics(t *testing.T) {
	if %t {
		ok := false
		for _, compare := range []func(%s, %s) int{%s} { if m1ContractOrderOK(compare) { ok = true } }
		if !ok { t.Error("M1_SEMANTIC_ORDER") }
	}
	if %t {
		groupOK, versionOK, digestOK := false, false, false
		for _, validate := range []func([]%s) error{%s} {
			for _, digest := range []func(%s) string{%s} {
				if m1ContractGroupOK(validate, digest) { groupOK = true }
				if m1ContractVersionOK(validate, digest) { versionOK = true }
				if m1ContractDigestOK(validate, digest) { digestOK = true }
			}
		}
		if !groupOK { t.Error("M1_SEMANTIC_GROUP") }
		if !versionOK { t.Error("M1_SEMANTIC_VERSION") }
		if !digestOK { t.Error("M1_SEMANTIC_DIGEST") }
	}
}

func m1ContractOrderOK(compare func(%s, %s) int) bool {
	var left, right %s
	left.RecordRevision, right.RecordRevision = 6, 7; left.EventIndex, right.EventIndex = 9, 0; left.EventID, right.EventID = "z", "a"
	if compare(left, right) >= 0 || compare(right, left) <= 0 { return false }
	left.RecordRevision, right.RecordRevision = 7, 7; left.EventIndex, right.EventIndex = 0, 1; left.EventID, right.EventID = "z", "a"
	if compare(left, right) >= 0 || compare(right, left) <= 0 { return false }
	left.EventIndex, right.EventIndex = 1, 1; left.EventID, right.EventID = "a", "b"
	if compare(left, right) >= 0 || compare(right, left) <= 0 { return false }
	return compare(left, left) == 0
}

func m1ContractSignedEvent(digest func(%s) string, revision uint64, index, count uint32, id, operation string) %s {
	var event %s
	event.RecordRevision = revision; event.EventIndex = index; event.EventCount = count
	event.EventID = id; event.OperationID = operation; event.EnvelopeVersion = 1
	event.EventType = "work-claimed"; event.EventSchemaVersion = 1; event.ReducerVersion = "work-v1"
	event.CanonicalEventDigest = digest(event)
	return event
}

func m1ContractResign(event %s, digest func(%s) string) %s {
	event.CanonicalEventDigest = ""; event.CanonicalEventDigest = digest(event); return event
}

func m1ContractGroupOK(validate func([]%s) error, digest func(%s) string) bool {
	first := m1ContractSignedEvent(digest, 7, 0, 2, "event-a", "operation-1")
	second := m1ContractSignedEvent(digest, 7, 1, 2, "event-b", "operation-1")
	if validate([]%s{first, second}) != nil { return false }
	missing := m1ContractSignedEvent(digest, 7, 2, 3, "event-c", "operation-1")
	if validate([]%s{first, missing}) == nil || validate([]%s{first, first}) == nil { return false }
	mixed := m1ContractSignedEvent(digest, 7, 1, 2, "event-b", "operation-2")
	if validate([]%s{first, mixed}) == nil { return false }
	newer := m1ContractSignedEvent(digest, 8, 0, 1, "event-c", "operation-2")
	older := m1ContractSignedEvent(digest, 7, 0, 1, "event-d", "operation-3")
	return validate([]%s{newer, older}) != nil
}

func m1ContractVersionOK(validate func([]%s) error, digest func(%s) string) bool {
	valid := m1ContractSignedEvent(digest, 7, 0, 1, "event-a", "operation-1")
	if validate([]%s{valid}) != nil { return false }
	unknownEnvelope := valid; unknownEnvelope.EnvelopeVersion = 2; unknownEnvelope = m1ContractResign(unknownEnvelope, digest)
	unknownType := valid; unknownType.EventType = "unknown-event"; unknownType = m1ContractResign(unknownType, digest)
	unknownEvent := valid; unknownEvent.EventSchemaVersion = 2; unknownEvent = m1ContractResign(unknownEvent, digest)
	unknownReducer := valid; unknownReducer.ReducerVersion = "unknown-v2"; unknownReducer = m1ContractResign(unknownReducer, digest)
	return validate([]%s{unknownEnvelope}) != nil && validate([]%s{unknownType}) != nil && validate([]%s{unknownEvent}) != nil && validate([]%s{unknownReducer}) != nil
}

func m1ContractDigestOK(validate func([]%s) error, digest func(%s) string) bool {
	event := m1ContractSignedEvent(digest, 7, 0, 1, "event-a", "operation-1")
	if validate([]%s{event}) != nil { return false }
	event.CanonicalEventDigest = "mismatched-canonical-digest"
	return validate([]%s{event}) != nil
}
`, enabled["event-order-seam"], seam.EventType, seam.EventType, strings.Join(comparators, ", "),
		enabled["event-group-refusal-seam"], seam.EventType, strings.Join(validators, ", "), seam.EventType, strings.Join(digests, ", "),
		seam.EventType, seam.EventType, seam.EventType,
		seam.EventType, seam.EventType, seam.EventType,
		seam.EventType, seam.EventType, seam.EventType,
		seam.EventType, seam.EventType, seam.EventType, seam.EventType, seam.EventType, seam.EventType, seam.EventType,
		seam.EventType, seam.EventType, seam.EventType, seam.EventType, seam.EventType, seam.EventType, seam.EventType,
		seam.EventType, seam.EventType, seam.EventType, seam.EventType)
}

func declaresM1EventModel(file *ast.File) bool {
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.TYPE {
			continue
		}
		for _, specification := range generic.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if ok && m1StructHasFields(structure, "EventID", "EventIndex") {
				return true
			}
		}
	}
	return false
}

func m1StructHasFields(structure *ast.StructType, wanted ...string) bool {
	found := make(map[string]bool)
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			found[name.Name] = true
		}
	}
	for _, name := range wanted {
		if !found[name] {
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
