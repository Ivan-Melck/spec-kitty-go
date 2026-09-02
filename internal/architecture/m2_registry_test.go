package architecture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/constant"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	m2EligibilityPersistentKeyMarker = "spk/v2/workgraphs/"
	m2EligibilityContractJSON        = `{"contention":{"max_parallel_writers":32,"required_trials":100},"hash_algorithm":"sha256-length-prefixed-mission-work-v1","max_entries_per_shard":256,"policy_version":"spk.workgraph.eligibility-shards.v1","shard_count":64}`
	m2EligibilityContractSHA256      = "896a5e6e77fb779a06831cbb29246e7a09e1d33371f6fd95016aa488cda99127"
)

type m2EligibilityContract struct {
	Contention struct {
		MaxParallelWriters int `json:"max_parallel_writers"`
		RequiredTrials     int `json:"required_trials"`
	} `json:"contention"`
	HashAlgorithm      string `json:"hash_algorithm"`
	MaxEntriesPerShard int    `json:"max_entries_per_shard"`
	PolicyVersion      string `json:"policy_version"`
	ShardCount         int    `json:"shard_count"`
}

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
	contract, err := loadM2EligibilityContract()
	if err != nil {
		t.Fatal(err)
	}
	if err := inspectM2EligibilityModule(t.Context(), m2RepositoryRoot(t), m2ModulePath, contract); err != nil {
		t.Fatal(err)
	}
}

func TestM2EligibilityPolicyMutantsAreRejected(t *testing.T) {
	contract, err := loadM2EligibilityContract()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name      string
		files     map[string]string
		wantError bool
	}{
		{"exact-workgraph", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("policy", 64, false)}, false},
		{"value-change", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("policy", 128, false)}, true},
		{"arbitrary-rename", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("renamed", 128, false)}, true},
		{"composed-key", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("policy", 128, true)}, true},
		{"adapter-owned", map[string]string{"internal/controlstore/gapdb/eligibility.go": strings.Replace(m2EligibilitySource("policy", 128, true), "package workgraph", "package gapdb", 1)}, true},
		{
			"reachable-wrapper",
			map[string]string{
				"internal/workgraph/eligibility.go": "package workgraph\nimport wrapper \"example.test/eligibility/internal/keybuilder\"\nvar _ = wrapper.EligibilityKey\n",
				"internal/keybuilder/key.go":        strings.Replace(m2EligibilitySource("renamed", 128, true), "package workgraph", "package keybuilder", 1),
			},
			true,
		},
		{"unclassified-owner", map[string]string{"internal/unowned/eligibility.go": strings.Replace(m2EligibilitySource("policy", 64, true), "package workgraph", "package unowned", 1)}, true},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoFixture(t, root, "go.mod", "module example.test/eligibility\n\ngo 1.26\n")
			for path, source := range fixture.files {
				writeGoFixture(t, root, path, source)
			}
			setIsolatedGoPaths(t)
			t.Setenv("GOPROXY", "off")
			err := inspectM2EligibilityModule(t.Context(), root, "example.test/eligibility", contract)
			if (err != nil) != fixture.wantError {
				t.Fatalf("evidence=ELIGIBILITY-POLICY-MUTANT fixture=%s observed-error=%v expected-error=%t", fixture.name, err, fixture.wantError)
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

func loadM2EligibilityContract() (m2EligibilityContract, error) {
	digest := sha256.Sum256([]byte(m2EligibilityContractJSON))
	if hex.EncodeToString(digest[:]) != m2EligibilityContractSHA256 {
		return m2EligibilityContract{}, fmt.Errorf("evidence=ELIGIBILITY-POLICY law=independent-contract-digest observed=%s expected=%s", hex.EncodeToString(digest[:]), m2EligibilityContractSHA256)
	}
	var contract m2EligibilityContract
	if err := decodeM2Strict([]byte(m2EligibilityContractJSON), &contract); err != nil {
		return contract, fmt.Errorf("evidence=ELIGIBILITY-POLICY law=typed-contract: %w", err)
	}
	if contract.PolicyVersion == "" || contract.HashAlgorithm == "" || contract.ShardCount <= 0 || contract.ShardCount&(contract.ShardCount-1) != 0 ||
		contract.MaxEntriesPerShard <= 0 || contract.ShardCount*contract.MaxEntriesPerShard < 10_000 ||
		contract.Contention.MaxParallelWriters <= 1 || contract.Contention.RequiredTrials < 100 {
		return contract, fmt.Errorf("evidence=ELIGIBILITY-POLICY law=typed-bounds observed=%+v", contract)
	}
	return contract, nil
}

func inspectM2EligibilityModule(ctx context.Context, root, module string, contract m2EligibilityContract) error {
	graph, err := LoadGraph(ctx, root)
	if err != nil {
		return err
	}
	sources, err := loadTypedProductionSources(ctx, root, module, runGraphCommand)
	if err != nil {
		return err
	}
	reachable := m2EligibilityOwnerClosure(graph, module)
	seams := make(map[string]bool)
	ownedValues := make(map[string]bool)
	for _, source := range sources {
		values := m2TypedConstantValues(source)
		for _, value := range values {
			if strings.Contains(value, m2EligibilityPersistentKeyMarker) && strings.Contains(value, "/eligibility/") {
				seams[source.importPath] = true
			}
			if reachable[source.importPath] {
				ownedValues[value] = true
			}
		}
	}
	if len(seams) == 0 {
		return nil
	}
	for owner := range seams {
		if !reachable[owner] {
			return fmt.Errorf("evidence=ELIGIBILITY-POLICY law=typed-owner observed=%s expected=workgraph-or-gapdb-reachable", owner)
		}
	}
	required := []string{
		contract.PolicyVersion,
		contract.HashAlgorithm,
		strconv.Itoa(contract.ShardCount),
		strconv.Itoa(contract.MaxEntriesPerShard),
		strconv.Itoa(contract.Contention.MaxParallelWriters),
		strconv.Itoa(contract.Contention.RequiredTrials),
	}
	for _, value := range required {
		if !ownedValues[value] {
			return fmt.Errorf("evidence=ELIGIBILITY-POLICY law=frozen-typed-coordinate observed=missing expected=%q seam=%v", value, sortedM2MapKeys(seams))
		}
	}
	return nil
}

func m2EligibilityOwnerClosure(graph Graph, module string) map[string]bool {
	reachable := make(map[string]bool)
	queue := make([]string, 0)
	for _, pkg := range graph.Packages {
		if matchesPrefix(pkg.ImportPath, []string{module + "/internal/workgraph", module + "/internal/controlstore/gapdb"}) {
			reachable[pkg.ImportPath] = true
			queue = append(queue, pkg.ImportPath)
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range graph.Edges {
			if edge.SourcePackage != current || edge.Kind != ImportModuleLocal || reachable[edge.TargetPackage] {
				continue
			}
			reachable[edge.TargetPackage] = true
			queue = append(queue, edge.TargetPackage)
		}
	}
	return reachable
}

func m2TypedConstantValues(source *productionSourcePackage) []string {
	values := make(map[string]bool)
	for _, typeAndValue := range source.info.Types {
		if typeAndValue.Value == nil {
			continue
		}
		switch typeAndValue.Value.Kind() {
		case constant.String:
			values[constant.StringVal(typeAndValue.Value)] = true
		case constant.Int:
			values[typeAndValue.Value.ExactString()] = true
		}
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func sortedM2MapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func m2EligibilitySource(prefix string, shardCount int, composed bool) string {
	key := `const EligibilityKey = "spk/v2/workgraphs/mission/eligibility/00"`
	if composed {
		key = `const root = "spk/v2/"
const category = "workgraphs/mission/eligibility/00"
const EligibilityKey = root + category`
	}
	return fmt.Sprintf(`package workgraph
const (
    %sVersion = "spk.workgraph.eligibility-shards.v1"
    %sHash = "sha256-length-prefixed-mission-work-v1"
    %sShards = %d
    %sEntries = 256
    %sWriters = 32
    %sTrials = 100
)
%s
`, prefix, prefix, prefix, shardCount, prefix, prefix, prefix, key)
}
