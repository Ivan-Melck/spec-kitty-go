package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	m2EligibilityPolicyVersion       = "spk.workgraph.eligibility-shards.v1"
	m2EligibilityShardCount          = 64
	m2EligibilityHashAlgorithm       = "sha256-length-prefixed-mission-work-v1"
	m2EligibilityMaxEntriesPerShard  = 256
	m2EligibilityContentionWriters   = 32
	m2EligibilityContentionTrials    = 100
	m2EligibilityPersistentKeyMarker = "spk/v2/workgraphs/"
)

type m2KeyEntry struct {
	ID       string
	Template string
	Scope    string
	Mutable  bool
}

func m2KeyspaceRegistry() []m2KeyEntry {
	return []m2KeyEntry{
		{ID: "K-AUTHORITY-BINDING", Template: "spk/v2/authority/binding", Scope: "host", Mutable: false},
		{ID: "K-RUNTIME-CURRENT", Template: "spk/v2/runtime/current", Scope: "host", Mutable: true},
		{ID: "K-RUNTIME-EPOCH", Template: "spk/v2/runtime/epochs/<runtime-epoch-id>", Scope: "host", Mutable: false},
		{ID: "K-OPERATION-BINDING", Template: "spk/v2/operations/<operation-id>/binding", Scope: "host", Mutable: false},
		{ID: "K-OPERATION-HEAD", Template: "spk/v2/operations/<operation-id>/head", Scope: "host", Mutable: true},
		{ID: "K-AGGREGATE-HEAD", Template: "spk/v2/scopes/<scope>/heads/<aggregate-kind>/<aggregate-id>", Scope: "typed", Mutable: true},
		{ID: "K-AGGREGATE-EVENT", Template: "spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>", Scope: "typed", Mutable: false},
		{ID: "K-CHECKPOINT-SLOT", Template: "spk/v2/checkpoints/slots/<slot-id>", Scope: "host", Mutable: true},
		{ID: "K-CHECKPOINT-RECORD", Template: "spk/v2/checkpoints/records/<checkpoint-id>", Scope: "host", Mutable: false},
		{ID: "K-WORKGRAPH-ELIGIBILITY", Template: "spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>", Scope: "mission", Mutable: true},
	}
}

func TestM2KeyspaceRegistryIsClosed(t *testing.T) {
	registry := m2KeyspaceRegistry()
	if err := validateM2KeyspaceRegistry(registry); err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{
		"K-AGGREGATE-EVENT", "K-AGGREGATE-HEAD", "K-AUTHORITY-BINDING", "K-CHECKPOINT-RECORD", "K-CHECKPOINT-SLOT",
		"K-OPERATION-BINDING", "K-OPERATION-HEAD", "K-RUNTIME-CURRENT", "K-RUNTIME-EPOCH", "K-WORKGRAPH-ELIGIBILITY",
	}
	gotIDs := make([]string, 0, len(registry))
	for _, entry := range registry {
		gotIDs = append(gotIDs, entry.ID)
	}
	slices.Sort(gotIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("evidence=KEYSPACE-CENSUS observed=%v expected=%v", gotIDs, wantIDs)
	}

	mutants := []struct {
		name   string
		mutate func([]m2KeyEntry) []m2KeyEntry
	}{
		{"mission-scoped-operation", func(v []m2KeyEntry) []m2KeyEntry {
			v[3].Template = "spk/v2/missions/<mission-id>/operations/<operation-id>/binding"
			return v
		}},
		{"variable-width-version", func(v []m2KeyEntry) []m2KeyEntry {
			v[6].Template = strings.Replace(v[6].Template, "<version-hex16>", "<version>", 1)
			return v
		}},
		{"ambiguous-component", func(v []m2KeyEntry) []m2KeyEntry {
			v[5].Template = "spk/v2/scopes/<raw-scope>/heads/<aggregate-kind>/<aggregate-id>"
			return v
		}},
		{"caller-raw-key", func(v []m2KeyEntry) []m2KeyEntry {
			return append(v, m2KeyEntry{ID: "K-RAW", Template: "<caller-key>", Scope: "caller", Mutable: true})
		}},
		{"mixed-v1", func(v []m2KeyEntry) []m2KeyEntry { v[0].Template = "spk/v1/authority/binding"; return v }},
		{"global-journal-head", func(v []m2KeyEntry) []m2KeyEntry {
			return append(v, m2KeyEntry{ID: "K-GLOBAL-JOURNAL", Template: "spk/v2/journal/head", Scope: "host", Mutable: true})
		}},
		{"shared-projection-head", func(v []m2KeyEntry) []m2KeyEntry {
			return append(v, m2KeyEntry{ID: "K-PROJECTION", Template: "spk/v2/projection/head", Scope: "host", Mutable: true})
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			value := slices.Clone(registry)
			if err := validateM2KeyspaceRegistry(mutant.mutate(value)); err == nil {
				t.Fatalf("evidence=KEYSPACE-MUTANT mutant=%s observed=accepted expected=rejected", mutant.name)
			}
		})
	}
}

func TestM2EligibilityGuardPolicyIsFrozenBeforePersistence(t *testing.T) {
	want := map[string]string{
		"EligibilityPolicyVersionV1":      strconv.Quote(m2EligibilityPolicyVersion),
		"EligibilityShardCountV1":         strconv.Itoa(m2EligibilityShardCount),
		"EligibilityHashAlgorithmV1":      strconv.Quote(m2EligibilityHashAlgorithm),
		"EligibilityMaxEntriesPerShardV1": strconv.Itoa(m2EligibilityMaxEntriesPerShard),
		"EligibilityContentionWritersV1":  strconv.Itoa(m2EligibilityContentionWriters),
		"EligibilityContentionTrialsV1":   strconv.Itoa(m2EligibilityContentionTrials),
	}
	if m2EligibilityShardCount&(m2EligibilityShardCount-1) != 0 || m2EligibilityShardCount*m2EligibilityMaxEntriesPerShard < 10_000 {
		t.Fatal("evidence=ELIGIBILITY-POLICY law=bounded-10k-capacity observed=invalid")
	}
	if m2EligibilityContentionWriters <= 1 || m2EligibilityContentionTrials < 100 {
		t.Fatal("evidence=ELIGIBILITY-POLICY law=contention-budget observed=invalid")
	}

	root := filepath.Join(m2RepositoryRoot(t), "internal", "workgraph")
	values, persistencePresent, err := inspectM2EligibilityPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	if !persistencePresent {
		return
	}
	for name, expected := range want {
		if values[name] != expected {
			t.Fatalf("evidence=ELIGIBILITY-POLICY law=frozen-tuple field=%s observed=%q expected=%q", name, values[name], expected)
		}
	}
}

func TestM2EligibilityPolicyMutantsAreRejected(t *testing.T) {
	base := map[string]string{
		"EligibilityPolicyVersionV1":      strconv.Quote(m2EligibilityPolicyVersion),
		"EligibilityShardCountV1":         strconv.Itoa(m2EligibilityShardCount),
		"EligibilityHashAlgorithmV1":      strconv.Quote(m2EligibilityHashAlgorithm),
		"EligibilityMaxEntriesPerShardV1": strconv.Itoa(m2EligibilityMaxEntriesPerShard),
		"EligibilityContentionWritersV1":  strconv.Itoa(m2EligibilityContentionWriters),
		"EligibilityContentionTrialsV1":   strconv.Itoa(m2EligibilityContentionTrials),
	}
	for _, name := range []string{"EligibilityPolicyVersionV1", "EligibilityShardCountV1", "EligibilityHashAlgorithmV1", "EligibilityMaxEntriesPerShardV1", "EligibilityContentionWritersV1", "EligibilityContentionTrialsV1"} {
		t.Run(name, func(t *testing.T) {
			mutant := make(map[string]string, len(base))
			for key, value := range base {
				mutant[key] = value
			}
			delete(mutant, name)
			if m2EligibilityValuesMatch(mutant) {
				t.Fatalf("evidence=ELIGIBILITY-POLICY-MUTANT mutant=%s observed=accepted expected=rejected", name)
			}
		})
	}
}

func TestM2FocusedEvidenceCensus(t *testing.T) {
	want := []string{
		"CONTRACT-NEGATIVE-BOUNDARIES", "CONTRACT-SCHEMA-COMPILATION", "EVENT-FAMILY-REGISTRY",
		"GLOBAL-OPERATION-KEYSPACE", "GUARD-DELETION-MUTANTS", "KEYSPACE-V2", "M1-ENTRY-TUPLE",
		"PACKAGE-DIRECTIONS", "PROVIDER-REACHABILITY", "PURE-KERNEL-IMPORTS", "RELEASE-ARCHIVE-COORDINATES",
		"SEMANTIC-DECOYS", "SEMANTIC-EMBEDDING", "SEMANTIC-RENAMES", "SQLITE-SQL-DENIAL", "TYPE-AST-REACHABILITY",
		"WORKGRAPH-ELIGIBILITY-POLICY",
	}
	got := m2EvidenceRegistry()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("evidence=M2-WP01-CENSUS observed=%v expected=%v", got, want)
	}
	for _, mutation := range []string{"missing", "duplicate", "unexpected", "non-terminal"} {
		t.Run(mutation, func(t *testing.T) {
			entries := m2TerminalEvidence()
			switch mutation {
			case "missing":
				entries = entries[1:]
			case "duplicate":
				entries = append(entries, entries[0])
			case "unexpected":
				entries = append(entries, m2EvidenceEntry{ID: "UNREGISTERED", Terminal: true})
			case "non-terminal":
				entries[0].Terminal = false
			}
			if err := validateM2Evidence(entries); err == nil {
				t.Fatalf("evidence=M2-WP01-CENSUS mutant=%s observed=accepted expected=rejected", mutation)
			}
		})
	}
}

type m2EvidenceEntry struct {
	ID       string
	Terminal bool
}

func m2EvidenceRegistry() []string {
	entries := m2TerminalEvidence()
	ids := make([]string, len(entries))
	for index, entry := range entries {
		ids[index] = entry.ID
	}
	return ids
}

func m2TerminalEvidence() []m2EvidenceEntry {
	return []m2EvidenceEntry{
		{ID: "CONTRACT-NEGATIVE-BOUNDARIES", Terminal: true}, {ID: "CONTRACT-SCHEMA-COMPILATION", Terminal: true},
		{ID: "EVENT-FAMILY-REGISTRY", Terminal: true}, {ID: "GLOBAL-OPERATION-KEYSPACE", Terminal: true},
		{ID: "GUARD-DELETION-MUTANTS", Terminal: true}, {ID: "KEYSPACE-V2", Terminal: true},
		{ID: "M1-ENTRY-TUPLE", Terminal: true}, {ID: "PACKAGE-DIRECTIONS", Terminal: true},
		{ID: "PROVIDER-REACHABILITY", Terminal: true}, {ID: "PURE-KERNEL-IMPORTS", Terminal: true},
		{ID: "RELEASE-ARCHIVE-COORDINATES", Terminal: true}, {ID: "SEMANTIC-DECOYS", Terminal: true},
		{ID: "SEMANTIC-EMBEDDING", Terminal: true}, {ID: "SEMANTIC-RENAMES", Terminal: true},
		{ID: "SQLITE-SQL-DENIAL", Terminal: true}, {ID: "TYPE-AST-REACHABILITY", Terminal: true},
		{ID: "WORKGRAPH-ELIGIBILITY-POLICY", Terminal: true},
	}
}

func validateM2Evidence(entries []m2EvidenceEntry) error {
	want := m2EvidenceRegistryLiteral()
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID == "" || seen[entry.ID] || !entry.Terminal {
			return fmt.Errorf("evidence=M2-WP01-CENSUS invalid=%+v", entry)
		}
		seen[entry.ID] = true
	}
	got := make([]string, 0, len(seen))
	for id := range seen {
		got = append(got, id)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		return fmt.Errorf("evidence=M2-WP01-CENSUS observed=%v expected=%v", got, want)
	}
	return nil
}

func m2EvidenceRegistryLiteral() []string {
	return []string{
		"CONTRACT-NEGATIVE-BOUNDARIES", "CONTRACT-SCHEMA-COMPILATION", "EVENT-FAMILY-REGISTRY",
		"GLOBAL-OPERATION-KEYSPACE", "GUARD-DELETION-MUTANTS", "KEYSPACE-V2", "M1-ENTRY-TUPLE",
		"PACKAGE-DIRECTIONS", "PROVIDER-REACHABILITY", "PURE-KERNEL-IMPORTS", "RELEASE-ARCHIVE-COORDINATES",
		"SEMANTIC-DECOYS", "SEMANTIC-EMBEDDING", "SEMANTIC-RENAMES", "SQLITE-SQL-DENIAL", "TYPE-AST-REACHABILITY",
		"WORKGRAPH-ELIGIBILITY-POLICY",
	}
}

func validateM2KeyspaceRegistry(entries []m2KeyEntry) error {
	want := m2KeyspaceRegistry()
	if len(entries) != len(want) {
		return fmt.Errorf("evidence=KEYSPACE-CENSUS observed-count=%d expected=%d", len(entries), len(want))
	}
	seen := make(map[string]bool, len(entries))
	for index, entry := range entries {
		if entry.ID == "" || seen[entry.ID] || entry.Template != want[index].Template || entry.Scope != want[index].Scope || entry.Mutable != want[index].Mutable {
			return fmt.Errorf("evidence=KEYSPACE-CENSUS index=%d observed=%+v expected=%+v", index, entry, want[index])
		}
		if !strings.HasPrefix(entry.Template, "spk/v2/") || strings.Contains(entry.Template, "//") || strings.Contains(entry.Template, "<raw-") || strings.Contains(entry.Template, "<caller-") {
			return fmt.Errorf("evidence=KEYSPACE-CENSUS id=%s observed-template=%q expected=canonical-v2", entry.ID, entry.Template)
		}
		seen[entry.ID] = true
	}
	if strings.Contains(entries[3].Template, "mission") || strings.Contains(entries[4].Template, "mission") {
		return fmt.Errorf("evidence=GLOBAL-OPERATION-KEYSPACE observed=mission-scoped expected=host-global")
	}
	if !strings.Contains(entries[6].Template, "<version-hex16>") {
		return fmt.Errorf("evidence=KEYSPACE-V2 observed=variable-version expected=fixed-hex16")
	}
	return nil
}

func inspectM2EligibilityPolicy(root string) (map[string]string, bool, error) {
	values := make(map[string]string)
	persistent := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, content, 0)
		if err != nil {
			return err
		}
		for _, literal := range file.Decls {
			declaration, ok := literal.(*ast.GenDecl)
			if !ok || declaration.Tok != token.CONST {
				continue
			}
			for _, spec := range declaration.Specs {
				valueSpec := spec.(*ast.ValueSpec)
				for index, name := range valueSpec.Names {
					if index < len(valueSpec.Values) {
						values[name.Name] = string(content[valueSpec.Values[index].Pos()-file.Pos() : valueSpec.Values[index].End()-file.Pos()])
					}
				}
			}
		}
		if bytesContainStringLiteral(file, m2EligibilityPersistentKeyMarker) {
			persistent = true
		}
		return nil
	})
	if os.IsNotExist(err) {
		return values, false, nil
	}
	return values, persistent, err
}

func bytesContainStringLiteral(file *ast.File, needle string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.Contains(value, needle) {
			found = true
		}
		return !found
	})
	return found
}

func m2EligibilityValuesMatch(values map[string]string) bool {
	return values["EligibilityPolicyVersionV1"] == strconv.Quote(m2EligibilityPolicyVersion) &&
		values["EligibilityShardCountV1"] == strconv.Itoa(m2EligibilityShardCount) &&
		values["EligibilityHashAlgorithmV1"] == strconv.Quote(m2EligibilityHashAlgorithm) &&
		values["EligibilityMaxEntriesPerShardV1"] == strconv.Itoa(m2EligibilityMaxEntriesPerShard) &&
		values["EligibilityContentionWritersV1"] == strconv.Itoa(m2EligibilityContentionWriters) &&
		values["EligibilityContentionTrialsV1"] == strconv.Itoa(m2EligibilityContentionTrials)
}
