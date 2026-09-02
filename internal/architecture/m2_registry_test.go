package architecture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	m2EligibilityPersistentKeyMarker = "spk/v2/workgraphs/"
	m2EligibilityContractJSON        = `{"admitted_m1_tuple_sha256":"4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d","contention":{"max_parallel_writers":32,"required_trials":100},"hash_algorithm":"sha256-length-prefixed-mission-work-v1","max_entries_per_shard":256,"policy_version":"spk.workgraph.eligibility-shards.v1","shard_count":64}`
	m2EligibilityContractSHA256      = "8ea2387a39847c307e40d55cecc21bf9ef2dad9ad149f352b5317eaa1f3abd85"
	m2EligibilityTemplate            = "spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>"
	m2KeyspaceContractJSON           = `[{"id":"K-AUTHORITY-BINDING","template":"spk/v2/authority/binding","scope":"host","mutable":false,"suffix_rule":"none","max_key_bytes":4096},{"id":"K-RUNTIME-CURRENT","template":"spk/v2/runtime/current","scope":"host","mutable":true,"suffix_rule":"none","max_key_bytes":4096},{"id":"K-RUNTIME-EPOCH","template":"spk/v2/runtime/epochs/<runtime-epoch-id>","scope":"host","mutable":false,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-OPERATION-BINDING","template":"spk/v2/operations/<operation-id>/binding","scope":"host","mutable":false,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-OPERATION-HEAD","template":"spk/v2/operations/<operation-id>/head","scope":"host","mutable":true,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-AGGREGATE-HEAD","template":"spk/v2/scopes/<scope>/heads/<aggregate-kind>/<aggregate-id>","scope":"typed","mutable":true,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-AGGREGATE-EVENT","template":"spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>","scope":"typed","mutable":false,"suffix_rule":"lower-hex16","max_key_bytes":4096},{"id":"K-CHECKPOINT-SLOT","template":"spk/v2/checkpoints/slots/<slot-id>","scope":"host","mutable":true,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-CHECKPOINT-RECORD","template":"spk/v2/checkpoints/records/<checkpoint-id>","scope":"host","mutable":false,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-WORKGRAPH-ELIGIBILITY","template":"spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>","scope":"mission","mutable":true,"suffix_rule":"shard-hex2","max_key_bytes":4096}]`
	m2KeyspaceContractSHA256         = "4ed5585ee6a59e0a54a58c14ca3fa7d1a60567d104902cf7aa8362fba21ebe1f"
)

type m2EligibilityContract struct {
	AdmittedM1TupleSHA string `json:"admitted_m1_tuple_sha256"`
	Contention         struct {
		MaxParallelWriters int `json:"max_parallel_writers"`
		RequiredTrials     int `json:"required_trials"`
	} `json:"contention"`
	HashAlgorithm      string `json:"hash_algorithm"`
	MaxEntriesPerShard int    `json:"max_entries_per_shard"`
	PolicyVersion      string `json:"policy_version"`
	ShardCount         int    `json:"shard_count"`
}

type m2KeyEntry struct {
	ID          string `json:"id"`
	Template    string `json:"template"`
	Scope       string `json:"scope"`
	Mutable     bool   `json:"mutable"`
	SuffixRule  string `json:"suffix_rule"`
	MaxKeyBytes int    `json:"max_key_bytes"`
}

func m2KeyspaceRegistry() []m2KeyEntry {
	return []m2KeyEntry{
		{ID: "K-AUTHORITY-BINDING", Template: "spk/v2/authority/binding", Scope: "host", Mutable: false, SuffixRule: "none", MaxKeyBytes: 4096},
		{ID: "K-RUNTIME-CURRENT", Template: "spk/v2/runtime/current", Scope: "host", Mutable: true, SuffixRule: "none", MaxKeyBytes: 4096},
		{ID: "K-RUNTIME-EPOCH", Template: "spk/v2/runtime/epochs/<runtime-epoch-id>", Scope: "host", Mutable: false, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-OPERATION-BINDING", Template: "spk/v2/operations/<operation-id>/binding", Scope: "host", Mutable: false, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-OPERATION-HEAD", Template: "spk/v2/operations/<operation-id>/head", Scope: "host", Mutable: true, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-AGGREGATE-HEAD", Template: "spk/v2/scopes/<scope>/heads/<aggregate-kind>/<aggregate-id>", Scope: "typed", Mutable: true, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-AGGREGATE-EVENT", Template: "spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>", Scope: "typed", Mutable: false, SuffixRule: "lower-hex16", MaxKeyBytes: 4096},
		{ID: "K-CHECKPOINT-SLOT", Template: "spk/v2/checkpoints/slots/<slot-id>", Scope: "host", Mutable: true, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-CHECKPOINT-RECORD", Template: "spk/v2/checkpoints/records/<checkpoint-id>", Scope: "host", Mutable: false, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-WORKGRAPH-ELIGIBILITY", Template: "spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>", Scope: "mission", Mutable: true, SuffixRule: "shard-hex2", MaxKeyBytes: 4096},
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
	for index, entry := range registry {
		t.Run("canonical-path-"+entry.ID, func(t *testing.T) {
			mutant := slices.Clone(registry)
			mutant[index].Template = strings.Replace(mutant[index].Template, "spk/v2/", "spk/v2/alternate/", 1)
			if err := validateM2KeyspaceRegistry(mutant); err == nil {
				t.Fatalf("evidence=KEYSPACE-MUTANT id=%s observed=substituted-path-accepted", entry.ID)
			}
		})
	}
	for _, mutation := range []struct {
		name   string
		mutate func([]m2KeyEntry)
	}{
		{"scope", func(v []m2KeyEntry) { v[0].Scope = "mission" }},
		{"mutability", func(v []m2KeyEntry) { v[0].Mutable = true }},
		{"suffix", func(v []m2KeyEntry) { v[6].SuffixRule = "decimal" }},
		{"budget", func(v []m2KeyEntry) { v[0].MaxKeyBytes++ }},
	} {
		t.Run("canonical-"+mutation.name, func(t *testing.T) {
			mutant := slices.Clone(registry)
			mutation.mutate(mutant)
			if err := validateM2KeyspaceRegistry(mutant); err == nil {
				t.Fatalf("evidence=KEYSPACE-MUTANT coordinate=%s observed=substitution-accepted", mutation.name)
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
		{"exact-workgraph", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("workgraph", "exact")}, false},
		{"renamed-function-and-type", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("workgraph", "renamed")}, false},
		{"wrong-value", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("workgraph", "wrong")}, true},
		{"runtime-key", map[string]string{"internal/workgraph/eligibility.go": m2EligibilitySource("workgraph", "runtime")}, true},
		{"adapter-owned-wrong", map[string]string{"internal/controlstore/gapdb/eligibility.go": m2EligibilitySource("gapdb", "wrong")}, true},
		{
			"broken-gapdb-valid-workgraph-decoy-first",
			map[string]string{
				"internal/workgraph/a_decoy.go":          m2EligibilityDecoy("workgraph"),
				"internal/controlstore/gapdb/z_codec.go": m2EligibilitySource("gapdb", "wrong"),
			},
			true,
		},
		{
			"broken-gapdb-valid-workgraph-decoy-last",
			map[string]string{
				"internal/workgraph/z_decoy.go":          m2EligibilityDecoy("workgraph"),
				"internal/controlstore/gapdb/a_codec.go": m2EligibilitySource("gapdb", "wrong"),
			},
			true,
		},
		{
			"multiple-clean-seams",
			map[string]string{
				"internal/workgraph/codec.go":          m2EligibilitySource("workgraph", "exact"),
				"internal/controlstore/gapdb/codec.go": m2EligibilitySource("gapdb", "renamed"),
			},
			false,
		},
		{
			"reachable-wrapper-wrong",
			map[string]string{
				"internal/workgraph/eligibility.go": "package workgraph\nimport wrapper \"example.test/eligibility/internal/keybuilder\"\nvar Build = wrapper.EligibilityCodec{}.Compose\n",
				"internal/keybuilder/key.go":        m2EligibilitySource("keybuilder", "wrong"),
			},
			true,
		},
		{
			"reachable-wrapper-clean",
			map[string]string{
				"internal/workgraph/eligibility.go": "package workgraph\nimport wrapper \"example.test/eligibility/internal/keybuilder\"\nvar Build = wrapper.EligibilityCodec{}.Compose\n",
				"internal/keybuilder/key.go":        m2EligibilitySource("keybuilder", "exact"),
			},
			false,
		},
		{
			"split-file-evidence",
			map[string]string{
				"internal/workgraph/evidence.go": m2EligibilityDecoy("workgraph"),
				"internal/workgraph/codec.go":    "package workgraph\nfunc Compose(missionID, shardID string) string { return decoyTemplate }\n",
			},
			true,
		},
		{"unclassified-owner", map[string]string{"internal/unowned/eligibility.go": m2EligibilitySource("unowned", "exact")}, true},
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

func TestM2RepositoryHasNoGenericRawKeyAuthority(t *testing.T) {
	if err := inspectM2RawStorageModule(t.Context(), m2RepositoryRoot(t), m2ModulePath); err != nil {
		t.Fatal(err)
	}
}

func TestM2RawKeyAndGodStoreMutantsAreRejected(t *testing.T) {
	fixtures := []struct {
		name  string
		files map[string]string
	}{
		{"interface", map[string]string{"internal/mission/store.go": "package mission\ntype Store interface { Read(string) ([]byte,error); Write(string,[]byte) error }\n"}},
		{"functions", map[string]string{"internal/mission/store.go": "package mission\nfunc Access(string,[]byte)([]byte,error){return nil,nil}\n"}},
		{"aliases", map[string]string{"internal/mission/store.go": "package mission\ntype Key = string\ntype Bytes = []byte\ntype Store interface { Read(Key)(Bytes,error); Write(Key,Bytes)error }\n"}},
		{"embedding", map[string]string{"internal/mission/store.go": "package mission\ntype Raw interface { Read(string)([]byte,error); Write(string,[]byte)error }\ntype Store interface { Raw }\n"}},
		{"generics", map[string]string{"internal/mission/store.go": "package mission\ntype Store[K ~string, V ~[]byte] interface { Read(K)(V,error); Write(K,V)error }\n"}},
		{"wrapper", map[string]string{"internal/mission/store.go": "package mission\ntype Client struct{}\nfunc (Client) Read(string)([]byte,error){return nil,nil}\nfunc (Client) Write(string,[]byte)error{return nil}\ntype Wrapper struct{ Client }\n"}},
		{"generated", map[string]string{"internal/mission/generated.go": "// Code generated by guard-test. DO NOT EDIT.\npackage mission\ntype Store interface { Read(string)([]byte,error); Write(string,[]byte)error }\n"}},
		{"build-variant", map[string]string{
			"internal/mission/normal.go":  "package mission\nconst Present = true\n",
			"internal/mission/variant.go": "//go:build never_guard_variant\n\npackage mission\ntype Store interface { Read(string)([]byte,error); Write(string,[]byte)error }\n",
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoFixture(t, root, "go.mod", "module example.test/raw\n\ngo 1.26\n")
			for path, source := range fixture.files {
				writeGoFixture(t, root, path, source)
			}
			setIsolatedGoPaths(t)
			t.Setenv("GOPROXY", "off")
			if err := inspectM2RawStorageModule(t.Context(), root, "example.test/raw"); err == nil {
				t.Fatalf("evidence=RAW-KEY-DENIAL fixture=%s observed=accepted", fixture.name)
			}
		})
	}
}

func TestM2AggregateOwnedPortIsLegal(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module example.test/raw\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/mission/store.go", `package mission
type MissionID string
type MissionState struct{ Revision uint64 }
type Store interface {
    Load(MissionID) (MissionState, error)
    Save(MissionID, MissionState) error
}`)
	setIsolatedGoPaths(t)
	t.Setenv("GOPROXY", "off")
	if err := inspectM2RawStorageModule(t.Context(), root, "example.test/raw"); err != nil {
		t.Fatalf("evidence=RAW-KEY-DENIAL legal aggregate port rejected: %v", err)
	}
}

func TestM2FocusedEvidenceCensus(t *testing.T) {
	if observed := verifyM2ExecutableMutants(t); observed != m2MutantCensusCount {
		t.Fatalf("evidence=M2-MUTANT-CENSUS observed-executions=%d expected=%d", observed, m2MutantCensusCount)
	}
	want := []string{
		"CONTRACT-NEGATIVE-BOUNDARIES", "CONTRACT-SCHEMA-COMPILATION", "ELIGIBILITY-SEAM-EVIDENCE",
		"EVENT-FAMILY-REGISTRY", "GAPDB-PRIVATE-DENIAL", "GLOBAL-OPERATION-KEYSPACE", "GUARD-DELETION-MUTANTS",
		"KEYSPACE-CONTRACT-ANCHOR", "KEYSPACE-V2", "M1-ENTRY-TUPLE", "M2-MUTANT-CENSUS",
		"PACKAGE-DIRECTIONS", "PROVIDER-REACHABILITY", "PURE-KERNEL-IMPORTS", "RAW-KEY-DENIAL", "RELEASE-ARCHIVE-COORDINATES",
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
		{ID: "ELIGIBILITY-SEAM-EVIDENCE", Terminal: true}, {ID: "GAPDB-PRIVATE-DENIAL", Terminal: true},
		{ID: "GUARD-DELETION-MUTANTS", Terminal: true}, {ID: "KEYSPACE-V2", Terminal: true},
		{ID: "KEYSPACE-CONTRACT-ANCHOR", Terminal: true}, {ID: "M2-MUTANT-CENSUS", Terminal: true},
		{ID: "M1-ENTRY-TUPLE", Terminal: true}, {ID: "PACKAGE-DIRECTIONS", Terminal: true},
		{ID: "PROVIDER-REACHABILITY", Terminal: true}, {ID: "PURE-KERNEL-IMPORTS", Terminal: true},
		{ID: "RELEASE-ARCHIVE-COORDINATES", Terminal: true}, {ID: "SEMANTIC-DECOYS", Terminal: true},
		{ID: "SEMANTIC-EMBEDDING", Terminal: true}, {ID: "SEMANTIC-RENAMES", Terminal: true},
		{ID: "SQLITE-SQL-DENIAL", Terminal: true}, {ID: "TYPE-AST-REACHABILITY", Terminal: true},
		{ID: "RAW-KEY-DENIAL", Terminal: true}, {ID: "WORKGRAPH-ELIGIBILITY-POLICY", Terminal: true},
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
		"CONTRACT-NEGATIVE-BOUNDARIES", "CONTRACT-SCHEMA-COMPILATION", "ELIGIBILITY-SEAM-EVIDENCE",
		"EVENT-FAMILY-REGISTRY", "GAPDB-PRIVATE-DENIAL", "GLOBAL-OPERATION-KEYSPACE", "GUARD-DELETION-MUTANTS",
		"KEYSPACE-CONTRACT-ANCHOR", "KEYSPACE-V2", "M1-ENTRY-TUPLE", "M2-MUTANT-CENSUS",
		"PACKAGE-DIRECTIONS", "PROVIDER-REACHABILITY", "PURE-KERNEL-IMPORTS", "RAW-KEY-DENIAL", "RELEASE-ARCHIVE-COORDINATES",
		"SEMANTIC-DECOYS", "SEMANTIC-EMBEDDING", "SEMANTIC-RENAMES", "SQLITE-SQL-DENIAL", "TYPE-AST-REACHABILITY",
		"WORKGRAPH-ELIGIBILITY-POLICY",
	}
}

func validateM2KeyspaceRegistry(entries []m2KeyEntry) error {
	want, err := loadM2KeyspaceContract()
	if err != nil {
		return err
	}
	if len(entries) != len(want) {
		return fmt.Errorf("evidence=KEYSPACE-CENSUS observed-count=%d expected=%d", len(entries), len(want))
	}
	seen := make(map[string]bool, len(entries))
	for index, entry := range entries {
		if entry.ID == "" || seen[entry.ID] || entry != want[index] {
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

func loadM2KeyspaceContract() ([]m2KeyEntry, error) {
	digest := sha256.Sum256([]byte(m2KeyspaceContractJSON))
	got := hex.EncodeToString(digest[:])
	if got != m2KeyspaceContractSHA256 {
		return nil, fmt.Errorf("evidence=KEYSPACE-CENSUS law=independent-contract-digest observed=%s expected=%s", got, m2KeyspaceContractSHA256)
	}
	var entries []m2KeyEntry
	if err := decodeM2Strict([]byte(m2KeyspaceContractJSON), &entries); err != nil {
		return nil, fmt.Errorf("evidence=KEYSPACE-CENSUS law=typed-contract: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("evidence=KEYSPACE-CENSUS law=typed-contract observed=empty")
	}
	return entries, nil
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
	if contract.AdmittedM1TupleSHA != m2TupleSHA || contract.PolicyVersion == "" || contract.HashAlgorithm == "" || contract.ShardCount <= 0 || contract.ShardCount&(contract.ShardCount-1) != 0 ||
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
	seamCount := 0
	for _, source := range sources {
		for _, file := range source.files {
			callables := m2EligibilityCallables(source, file)
			if len(callables) == 0 {
				continue
			}
			seamCount += len(callables)
			filename := source.fileSet.Position(file.Pos()).Filename
			if !reachable[source.importPath] {
				return fmt.Errorf("evidence=ELIGIBILITY-POLICY law=typed-owner observed=%s:%s expected=workgraph-or-gapdb-reachable", source.importPath, filepath.Base(filename))
			}
			values := m2FileTypedConstantValues(source, file)
			for _, value := range m2RequiredEligibilityValues(contract) {
				if !values[value] {
					return fmt.Errorf("evidence=ELIGIBILITY-POLICY law=seam-local-evidence owner=%s file=%s callable=%v observed=missing expected=%q", source.importPath, filepath.Base(filename), callables, value)
				}
			}
		}
	}
	_ = seamCount // Absence is valid before WP06 creates the first persistent seam.
	return nil
}

func m2RequiredEligibilityValues(contract m2EligibilityContract) []string {
	return []string{
		m2EligibilityTemplate,
		m2EligibilityContractSHA256,
		contract.AdmittedM1TupleSHA,
		contract.PolicyVersion,
		contract.HashAlgorithm,
		strconv.Itoa(contract.ShardCount),
		strconv.Itoa(contract.MaxEntriesPerShard),
		strconv.Itoa(contract.Contention.MaxParallelWriters),
		strconv.Itoa(contract.Contention.RequiredTrials),
	}
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

func m2EligibilityCallables(source *productionSourcePackage, file *ast.File) []string {
	if !m2SourceHasEligibilityKeyClues(source) {
		return nil
	}
	clueObjects := m2EligibilityClueObjects(source)
	var result []string
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			object, _ := source.info.Defs[value.Name].(*types.Func)
			if object != nil && m2SignatureReturnsString(object.Type()) && m2EligibilityNodeHasClue(source, value.Body, clueObjects) {
				result = append(result, value.Name.Name)
			}
		case *ast.GenDecl:
			for _, specification := range value.Specs {
				values, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range values.Names {
					object := source.info.Defs[name]
					if object != nil && m2SignatureReturnsString(object.Type()) && index < len(values.Values) && m2EligibilityNodeHasClue(source, values.Values[index], clueObjects) {
						result = append(result, name.Name)
					}
				}
			}
		}
	}
	slices.Sort(result)
	return result
}

func m2SourceHasEligibilityKeyClues(source *productionSourcePackage) bool {
	var corpus strings.Builder
	for _, file := range source.files {
		ast.Inspect(file, func(node ast.Node) bool {
			expression, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			typed, ok := source.info.Types[expression]
			if ok && typed.Value != nil && typed.Value.Kind() == constant.String {
				corpus.WriteString(strings.ToLower(constant.StringVal(typed.Value)))
				corpus.WriteByte('\n')
			}
			return true
		})
	}
	value := strings.ReplaceAll(corpus.String(), "\n", "")
	return strings.Contains(value, "spk/v2") && strings.Contains(value, "workgraph") && strings.Contains(value, "eligibility")
}

func m2EligibilityClueObjects(source *productionSourcePackage) map[types.Object]bool {
	result := make(map[types.Object]bool)
	for _, file := range source.files {
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range generic.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, expression := range value.Values {
					if !m2EligibilityNodeHasClue(source, expression, nil) || index >= len(value.Names) {
						continue
					}
					if object := source.info.Defs[value.Names[index]]; object != nil {
						result[object] = true
					}
				}
			}
		}
	}
	return result
}

func m2SignatureReturnsString(candidate types.Type) bool {
	signature, ok := types.Unalias(candidate).Underlying().(*types.Signature)
	if !ok || signature.Results().Len() == 0 {
		return false
	}
	basic, ok := types.Unalias(signature.Results().At(0).Type()).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
}

func m2EligibilityNodeHasClue(source *productionSourcePackage, node ast.Node, clueObjects map[types.Object]bool) bool {
	found := false
	ast.Inspect(node, func(candidate ast.Node) bool {
		if found || candidate == nil {
			return !found
		}
		if identifier, ok := candidate.(*ast.Ident); ok && clueObjects[source.info.ObjectOf(identifier)] {
			found = true
			return false
		}
		expression, isExpression := candidate.(ast.Expr)
		if !isExpression {
			return true
		}
		typed, ok := source.info.Types[expression]
		if !ok || typed.Value == nil || typed.Value.Kind() != constant.String {
			return true
		}
		if strings.Contains(strings.ToLower(constant.StringVal(typed.Value)), "eligibility") {
			found = true
			return false
		}
		return true
	})
	return found
}

func m2FileTypedConstantValues(source *productionSourcePackage, file *ast.File) map[string]bool {
	values := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		expression, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		typed, ok := source.info.Types[expression]
		if !ok || typed.Value == nil {
			return true
		}
		switch typed.Value.Kind() {
		case constant.String:
			values[constant.StringVal(typed.Value)] = true
		case constant.Int:
			values[typed.Value.ExactString()] = true
		}
		return true
	})
	return values
}

func m2EligibilitySource(packageName, variant string) string {
	policyVersion := "spk.workgraph.eligibility-shards.v1"
	shardCount := 64
	typeName := "EligibilityCodec"
	functionName := "Compose"
	keyDeclaration := `const keyTemplate = "spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>"`
	keyExpression := "keyTemplate"
	if variant == "wrong" {
		policyVersion = "spk.workgraph.eligibility-shards.v2"
		shardCount = 128
	}
	if variant == "renamed" {
		typeName = "Arbitrary"
		functionName = "Unrelated"
	}
	if variant == "runtime" {
		keyDeclaration = `var keyRoot = "spk/v2/workgraphs/"
var keyTail = "/eligibility/<shard-id>"`
		keyExpression = `keyRoot + missionID + keyTail`
	}
	return fmt.Sprintf(`package %s
const (
    evidenceVersion = %q
    evidenceHash = "sha256-length-prefixed-mission-work-v1"
    evidenceShards = %d
    evidenceEntries = 256
    evidenceWriters = 32
    evidenceTrials = 100
    evidenceContract = %q
    evidenceAdmission = %q
)
%s
type %s struct{}
func (%s) %s(missionID, shardID string) string { return %s }
`, packageName, policyVersion, shardCount, m2EligibilityContractSHA256, m2TupleSHA, keyDeclaration, typeName, typeName, functionName, keyExpression)
}

func m2EligibilityDecoy(packageName string) string {
	return fmt.Sprintf(`package %s
const (
    decoyTemplate = %q
    decoyVersion = "spk.workgraph.eligibility-shards.v1"
    decoyHash = "sha256-length-prefixed-mission-work-v1"
    decoyShards = 64
    decoyEntries = 256
    decoyWriters = 32
    decoyTrials = 100
    decoyContract = %q
    decoyAdmission = %q
)
`, packageName, m2EligibilityTemplate, m2EligibilityContractSHA256, m2TupleSHA)
}

type m2RawStorageShape uint8

const (
	m2RawRead m2RawStorageShape = 1 << iota
	m2RawWrite
)

func inspectM2RawStorageModule(ctx context.Context, root, module string) error {
	sources, err := loadTypedProductionSources(ctx, root, module, runGraphCommand)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if !m2RawGuardedPackage(source.importPath, module) {
			continue
		}
		owners := make(map[string]m2RawStorageShape)
		scope := source.typed.Scope()
		for _, name := range scope.Names() {
			object := scope.Lookup(name)
			switch value := object.(type) {
			case *types.Func:
				owners["function:"+name] |= m2TypedRawStorageShape(value.Type())
			case *types.TypeName:
				methodSet := types.NewMethodSet(value.Type())
				pointerSet := types.NewMethodSet(types.NewPointer(value.Type()))
				for index := 0; index < methodSet.Len(); index++ {
					owners[name] |= m2TypedRawStorageShape(methodSet.At(index).Obj().Type())
				}
				for index := 0; index < pointerSet.Len(); index++ {
					owners[name] |= m2TypedRawStorageShape(pointerSet.At(index).Obj().Type())
				}
			}
		}
		if owner, found := m2FirstGodStoreOwner(owners); found {
			return fmt.Errorf("evidence=RAW-KEY-DENIAL law=typed-god-store package=%s owner=%s", source.importPath, owner)
		}
	}
	return inspectM2AllSourceRawStorage(root, module)
}

func m2RawGuardedPackage(importPath, module string) bool {
	return matchesPrefix(importPath, []string{
		module + "/internal/mission",
		module + "/internal/artifact",
		module + "/internal/workgraph",
		module + "/internal/execution",
		module + "/internal/workspace",
		module + "/internal/handoff",
		module + "/internal/review",
		module + "/internal/integration",
		module + "/internal/projection",
		module + "/internal/controljournal/v2",
		module + "/internal/controlstore/gapdb",
	})
}

func m2TypedRawStorageShape(candidate types.Type) m2RawStorageShape {
	signature, ok := types.Unalias(candidate).Underlying().(*types.Signature)
	if !ok {
		return 0
	}
	hasStringKey := false
	hasBytesParameter := false
	for index := 0; index < signature.Params().Len(); index++ {
		parameter := signature.Params().At(index).Type()
		hasStringKey = hasStringKey || m2TypeIsStringLike(parameter)
		hasBytesParameter = hasBytesParameter || m2TypeIsBytesLike(parameter)
	}
	hasBytesResult := false
	for index := 0; index < signature.Results().Len(); index++ {
		hasBytesResult = hasBytesResult || m2TypeIsBytesLike(signature.Results().At(index).Type())
	}
	var shape m2RawStorageShape
	if hasStringKey && hasBytesResult {
		shape |= m2RawRead
	}
	if hasStringKey && hasBytesParameter {
		shape |= m2RawWrite
	}
	return shape
}

func m2TypeIsStringLike(candidate types.Type) bool {
	if parameter, ok := types.Unalias(candidate).(*types.TypeParam); ok {
		normalized := strings.ReplaceAll(types.TypeString(parameter.Constraint(), nil), " ", "")
		return strings.Contains(normalized, "~string")
	}
	underlying := types.Unalias(candidate).Underlying()
	if basic, ok := underlying.(*types.Basic); ok {
		return basic.Kind() == types.String
	}
	normalized := strings.ReplaceAll(types.TypeString(candidate, nil), " ", "")
	return strings.Contains(normalized, "~string")
}

func m2TypeIsBytesLike(candidate types.Type) bool {
	if parameter, ok := types.Unalias(candidate).(*types.TypeParam); ok {
		normalized := strings.ReplaceAll(types.TypeString(parameter.Constraint(), nil), " ", "")
		return strings.Contains(normalized, "~[]byte")
	}
	underlying := types.Unalias(candidate).Underlying()
	if slice, ok := underlying.(*types.Slice); ok {
		basic, ok := types.Unalias(slice.Elem()).Underlying().(*types.Basic)
		return ok && basic.Kind() == types.Uint8
	}
	normalized := strings.ReplaceAll(types.TypeString(candidate, nil), " ", "")
	return strings.Contains(normalized, "~[]byte")
}

func m2FirstGodStoreOwner(owners map[string]m2RawStorageShape) (string, bool) {
	names := make([]string, 0, len(owners))
	for name := range owners {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if owners[name]&(m2RawRead|m2RawWrite) == m2RawRead|m2RawWrite {
			return name, true
		}
	}
	return "", false
}

func inspectM2AllSourceRawStorage(root, module string) error {
	type packageShape struct {
		aliases map[string]ast.Expr
		owners  map[string]m2RawStorageShape
	}
	packages := make(map[string]*packageShape)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == "vendor" || entry.Name() == "kitty-specs" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		importPath := module
		if relative != "." {
			importPath += "/" + filepath.ToSlash(relative)
		}
		if !m2RawGuardedPackage(importPath, module) {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("evidence=RAW-KEY-DENIAL law=parse-all-source file=%s: %w", path, err)
		}
		key := importPath + "#" + file.Name.Name
		model := packages[key]
		if model == nil {
			model = &packageShape{aliases: make(map[string]ast.Expr), owners: make(map[string]m2RawStorageShape)}
			packages[key] = model
		}
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				if typeSpec.Assign.IsValid() {
					model.aliases[typeSpec.Name.Name] = typeSpec.Type
				}
			}
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				owner := "function:" + value.Name.Name
				if value.Recv != nil && len(value.Recv.List) > 0 {
					owner = m2ASTBaseTypeName(value.Recv.List[0].Type)
				}
				model.owners[owner] |= m2ASTRawStorageShape(value.Type, model.aliases)
			case *ast.GenDecl:
				if value.Tok != token.TYPE {
					continue
				}
				for _, specification := range value.Specs {
					typeSpec := specification.(*ast.TypeSpec)
					interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					for _, field := range interfaceType.Methods.List {
						if functionType, ok := field.Type.(*ast.FuncType); ok {
							model.owners[typeSpec.Name.Name] |= m2ASTRawStorageShape(functionType, model.aliases)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if owner, found := m2FirstGodStoreOwner(packages[key].owners); found {
			return fmt.Errorf("evidence=RAW-KEY-DENIAL law=all-source-god-store package=%s owner=%s", key, owner)
		}
	}
	return nil
}

func m2ASTRawStorageShape(signature *ast.FuncType, aliases map[string]ast.Expr) m2RawStorageShape {
	parameters := m2ASTFieldKinds(signature.Params, aliases)
	results := m2ASTFieldKinds(signature.Results, aliases)
	var shape m2RawStorageShape
	if slices.Contains(parameters, "string") && slices.Contains(results, "bytes") {
		shape |= m2RawRead
	}
	if slices.Contains(parameters, "string") && slices.Contains(parameters, "bytes") {
		shape |= m2RawWrite
	}
	return shape
}

func m2ASTFieldKinds(fields *ast.FieldList, aliases map[string]ast.Expr) []string {
	if fields == nil {
		return nil
	}
	var result []string
	for _, field := range fields.List {
		kind := m2ASTTypeKind(field.Type, aliases, nil)
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			result = append(result, kind)
		}
	}
	return result
}

func m2ASTTypeKind(expression ast.Expr, aliases map[string]ast.Expr, seen map[string]bool) string {
	if seen == nil {
		seen = make(map[string]bool)
	}
	switch value := expression.(type) {
	case *ast.Ident:
		if value.Name == "string" {
			return "string"
		}
		if alias := aliases[value.Name]; alias != nil && !seen[value.Name] {
			seen[value.Name] = true
			return m2ASTTypeKind(alias, aliases, seen)
		}
	case *ast.ArrayType:
		if value.Len == nil {
			if identifier, ok := value.Elt.(*ast.Ident); ok && identifier.Name == "byte" {
				return "bytes"
			}
		}
	case *ast.UnaryExpr:
		if value.Op == token.TILDE {
			return m2ASTTypeKind(value.X, aliases, seen)
		}
	case *ast.InterfaceType:
		for _, field := range value.Methods.List {
			if kind := m2ASTTypeKind(field.Type, aliases, seen); kind != "" {
				return kind
			}
		}
	}
	return ""
}

func m2ASTBaseTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return m2ASTBaseTypeName(value.X)
	case *ast.IndexExpr:
		return m2ASTBaseTypeName(value.X)
	case *ast.IndexListExpr:
		return m2ASTBaseTypeName(value.X)
	default:
		return "unknown"
	}
}
