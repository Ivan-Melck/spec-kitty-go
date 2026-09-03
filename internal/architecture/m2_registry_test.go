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
	m2KeyspaceContractJSON           = `[{"id":"K-AUTHORITY-BINDING","template":"spk/v2/authority/binding","scope":"host","mutable":false,"suffix_rule":"none","max_key_bytes":4096},{"id":"K-RUNTIME-CURRENT","template":"spk/v2/runtime/current","scope":"host","mutable":true,"suffix_rule":"none","max_key_bytes":4096},{"id":"K-RUNTIME-EPOCH","template":"spk/v2/runtime/epochs/<runtime-epoch-id>","scope":"host","mutable":false,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-OPERATION-BINDING","template":"spk/v2/operations/<operation-id>/binding","scope":"host","mutable":false,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-OPERATION-HEAD","template":"spk/v2/operations/<operation-id>/head","scope":"host","mutable":true,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-AGGREGATE-HEAD","template":"spk/v2/heads/<scope>/<aggregate-kind>/<aggregate-id>","scope":"typed","mutable":true,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-AGGREGATE-EVENT","template":"spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>","scope":"typed","mutable":false,"suffix_rule":"lower-hex16","max_key_bytes":4096},{"id":"K-CHECKPOINT-SLOT","template":"spk/v2/checkpoints/slots/<slot-id>","scope":"host","mutable":true,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-CHECKPOINT-RECORD","template":"spk/v2/checkpoints/records/<checkpoint-id>","scope":"host","mutable":false,"suffix_rule":"canonical-bounded","max_key_bytes":4096},{"id":"K-WORKGRAPH-ELIGIBILITY","template":"spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>","scope":"mission","mutable":true,"suffix_rule":"shard-hex2","max_key_bytes":4096},{"id":"K-PUBLICATION-WITNESS","template":"spk/v2/publications/<publication-id>/witness","scope":"host","mutable":false,"suffix_rule":"canonical-bounded","max_key_bytes":4096,"write_condition":"require-absent","expires":false}]`
	m2KeyspaceContractSHA256         = "44dea603826496df094f1f0beae170544f1930e170c91b29f7c9ad7da8c94078"
	m2FoundationContractSHA256       = "5579ab86bb3320ce9a76b8f4593dcbc379195fa771d6ef0033aa3b9270124711"
	m2PublicationRecoverySHA256      = "dfb2914655d9b4e7da011ccc594553e230f212f02b980eb1f3f02a598310e15b"
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
	ID             string `json:"id"`
	Template       string `json:"template"`
	Scope          string `json:"scope"`
	Mutable        bool   `json:"mutable"`
	SuffixRule     string `json:"suffix_rule"`
	MaxKeyBytes    int    `json:"max_key_bytes"`
	WriteCondition string `json:"write_condition,omitempty"`
	Expires        bool   `json:"expires,omitempty"`
}

func m2KeyspaceRegistry() []m2KeyEntry {
	return []m2KeyEntry{
		{ID: "K-AUTHORITY-BINDING", Template: "spk/v2/authority/binding", Scope: "host", Mutable: false, SuffixRule: "none", MaxKeyBytes: 4096},
		{ID: "K-RUNTIME-CURRENT", Template: "spk/v2/runtime/current", Scope: "host", Mutable: true, SuffixRule: "none", MaxKeyBytes: 4096},
		{ID: "K-RUNTIME-EPOCH", Template: "spk/v2/runtime/epochs/<runtime-epoch-id>", Scope: "host", Mutable: false, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-OPERATION-BINDING", Template: "spk/v2/operations/<operation-id>/binding", Scope: "host", Mutable: false, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-OPERATION-HEAD", Template: "spk/v2/operations/<operation-id>/head", Scope: "host", Mutable: true, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-AGGREGATE-HEAD", Template: "spk/v2/heads/<scope>/<aggregate-kind>/<aggregate-id>", Scope: "typed", Mutable: true, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-AGGREGATE-EVENT", Template: "spk/v2/scopes/<scope>/journal/<aggregate-kind>/<aggregate-id>/<version-hex16>", Scope: "typed", Mutable: false, SuffixRule: "lower-hex16", MaxKeyBytes: 4096},
		{ID: "K-CHECKPOINT-SLOT", Template: "spk/v2/checkpoints/slots/<slot-id>", Scope: "host", Mutable: true, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-CHECKPOINT-RECORD", Template: "spk/v2/checkpoints/records/<checkpoint-id>", Scope: "host", Mutable: false, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096},
		{ID: "K-WORKGRAPH-ELIGIBILITY", Template: "spk/v2/workgraphs/<mission-id>/eligibility/<shard-id>", Scope: "mission", Mutable: true, SuffixRule: "shard-hex2", MaxKeyBytes: 4096},
		{ID: "K-PUBLICATION-WITNESS", Template: "spk/v2/publications/<publication-id>/witness", Scope: "host", Mutable: false, SuffixRule: "canonical-bounded", MaxKeyBytes: 4096, WriteCondition: "require-absent", Expires: false},
	}
}

func TestM2KeyspaceRegistryIsClosed(t *testing.T) {
	registry := m2KeyspaceRegistry()
	if err := validateM2KeyspaceRegistry(registry); err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{
		"K-AGGREGATE-EVENT", "K-AGGREGATE-HEAD", "K-AUTHORITY-BINDING", "K-CHECKPOINT-RECORD", "K-CHECKPOINT-SLOT",
		"K-OPERATION-BINDING", "K-OPERATION-HEAD", "K-PUBLICATION-WITNESS", "K-RUNTIME-CURRENT", "K-RUNTIME-EPOCH", "K-WORKGRAPH-ELIGIBILITY",
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
			v[5].Template = "spk/v2/heads/<raw-scope>/<aggregate-kind>/<aggregate-id>"
			return v
		}},
		{"scope-first-aggregate-head", func(v []m2KeyEntry) []m2KeyEntry {
			v[5].Template = "spk/v2/scopes/<scope>/heads/<aggregate-kind>/<aggregate-id>"
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
		{"missing-publication-witness", func(v []m2KeyEntry) []m2KeyEntry { return v[:len(v)-1] }},
		{"plausible-publication-prefix", func(v []m2KeyEntry) []m2KeyEntry {
			v[len(v)-1].Template = "spk/v2/operations/<publication-id>/witness"
			return v
		}},
		{"malformed-publication-witness", func(v []m2KeyEntry) []m2KeyEntry {
			v[len(v)-1].Template = "spk/v2/publications//<publication-id>/witness"
			return v
		}},
		{"mutable-publication-witness", func(v []m2KeyEntry) []m2KeyEntry { v[len(v)-1].Mutable = true; return v }},
		{"expiring-publication-witness", func(v []m2KeyEntry) []m2KeyEntry { v[len(v)-1].Expires = true; return v }},
		{"revision-conditioned-publication-witness", func(v []m2KeyEntry) []m2KeyEntry {
			v[len(v)-1].WriteCondition = "require-revision"
			return v
		}},
		{"unconditioned-publication-witness", func(v []m2KeyEntry) []m2KeyEntry {
			v[len(v)-1].WriteCondition = ""
			return v
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
		{"same-file-broken-first", map[string]string{"internal/workgraph/eligibility.go": m2EligibilityPairedSource("workgraph", true, false)}, true},
		{"same-file-broken-last", map[string]string{"internal/workgraph/eligibility.go": m2EligibilityPairedSource("workgraph", false, true)}, true},
		{"same-file-two-clean", map[string]string{"internal/workgraph/eligibility.go": m2EligibilityPairedSource("workgraph", false, false)}, false},
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

func TestM2RecoveryAndPublicationContractsRemainFoundationOwned(t *testing.T) {
	if err := inspectM2FoundationContractOwnership(t.Context(), m2RepositoryRoot(t), m2ModulePath); err != nil {
		t.Fatal(err)
	}

	for _, fixture := range []struct {
		name      string
		files     map[string]string
		wantError bool
	}{
		{"foundation-contract", m2FoundationOwnershipFixture("COMPLETE"), false},
		{"missing-foundation-member", m2FoundationOwnershipFixture("MISSING"), true},
		{"adapter-owned-v2-record", m2FoundationOwnershipFixture("ADAPTER-OWNED"), true},
		{"renamed-adapter-contract-in-read-signature", m2FoundationOwnershipFixture("RENAMED-ADAPTER"), true},
		{"renamed-callable-beside-valid-read", m2FoundationOwnershipFixture("RENAMED-CALLABLE"), true},
		{"named-function-type", m2FoundationOwnershipFixture("NAMED-FUNCTION-TYPE"), true},
		{"alias-function-type", m2FoundationOwnershipFixture("ALIAS-FUNCTION-TYPE"), true},
		{"used-generic-constraint", m2FoundationOwnershipFixture("USED-GENERIC-CONSTRAINT"), true},
		{"unused-generic-constraint", m2FoundationOwnershipFixture("UNUSED-GENERIC-CONSTRAINT"), true},
		{"union-constraint", m2FoundationOwnershipFixture("UNION-CONSTRAINT"), true},
		{"generic-alias", m2FoundationOwnershipFixture("GENERIC-ALIAS"), true},
		{"defined-request-wrapper", m2FoundationOwnershipFixture("DEFINED-REQUEST-WRAPPER"), true},
		{"transitive-defined-request-wrapper", m2FoundationOwnershipFixture("TRANSITIVE-DEFINED-WRAPPER"), true},
		{"variable-wrapper-pointer", m2FoundationOwnershipFixture("VARIABLE-WRAPPER-POINTER"), true},
		{"field-wrapper-pointer", m2FoundationOwnershipFixture("FIELD-WRAPPER-POINTER"), true},
		{"named-callable-pointer", m2FoundationOwnershipFixture("NAMED-CALLABLE-POINTER"), true},
		{"interface-method-pointer", m2FoundationOwnershipFixture("INTERFACE-METHOD-POINTER"), true},
		{"receiver-generic-constraint", m2FoundationOwnershipFixture("RECEIVER-GENERIC-CONSTRAINT"), true},
		{"anonymous-interface-variable", m2FoundationOwnershipFixture("ANONYMOUS-INTERFACE-VARIABLE"), true},
		{"unexported-receiver-variable", m2FoundationOwnershipFixture("UNEXPORTED-RECEIVER-VARIABLE"), true},
		{"anonymous-struct-function-field", m2FoundationOwnershipFixture("ANONYMOUS-STRUCT-FUNCTION-FIELD"), true},
		{"function-returned-receiver", m2FoundationOwnershipFixture("FUNCTION-RETURNED-RECEIVER"), true},
		{"exported-field-receiver", m2FoundationOwnershipFixture("EXPORTED-FIELD-RECEIVER"), true},
		{"unrelated-public-adapter-api", m2FoundationOwnershipFixture("UNRELATED-PUBLIC"), false},
		{"private-adapter-helper", m2FoundationOwnershipFixture("PRIVATE-HELPER"), false},
		{"unrelated-named-function-type", m2FoundationOwnershipFixture("UNRELATED-NAMED-FUNCTION"), false},
		{"unrelated-alias-function-type", m2FoundationOwnershipFixture("UNRELATED-ALIAS-FUNCTION"), false},
		{"unrelated-generic-constraint", m2FoundationOwnershipFixture("UNRELATED-GENERIC-CONSTRAINT"), false},
		{"foundation-only-generic", m2FoundationOwnershipFixture("FOUNDATION-ONLY-GENERIC"), false},
		{"unrelated-variable", m2FoundationOwnershipFixture("UNRELATED-VARIABLE"), false},
		{"unrelated-field", m2FoundationOwnershipFixture("UNRELATED-FIELD"), false},
		{"foundation-only-receiver", m2FoundationOwnershipFixture("FOUNDATION-ONLY-RECEIVER"), false},
		{"private-generic-constraint", m2FoundationOwnershipFixture("PRIVATE-GENERIC-CONSTRAINT"), false},
		{"private-field-wrapper", m2FoundationOwnershipFixture("PRIVATE-FIELD-WRAPPER"), false},
		{"unrelated-composite", m2FoundationOwnershipFixture("UNRELATED-COMPOSITE"), false},
		{"foundation-only-composite", m2FoundationOwnershipFixture("FOUNDATION-ONLY-COMPOSITE"), false},
		{"private-unreachable-type", m2FoundationOwnershipFixture("PRIVATE-UNREACHABLE-TYPE"), false},
		{"recursive-unrelated-composite", m2FoundationOwnershipFixture("RECURSIVE-UNRELATED-COMPOSITE"), false},
		{"publication-recovery-complete", m2PublicationRecoveryFixture("COMPLETE"), false},
		{"publication-recovery-missing-type", m2PublicationRecoveryFixture("MISSING-TYPE"), true},
		{"publication-recovery-missing-helper", m2PublicationRecoveryFixture("MISSING-HELPER"), true},
		{"publication-recovery-missing-boundaries-method", m2PublicationRecoveryFixture("MISSING-BOUNDARIES"), true},
		{"adapter-owned-publication-witness", m2PublicationRecoveryFixture("ADAPTER-WITNESS"), true},
		{"adapter-owned-operation-boundary-lookalike", m2PublicationRecoveryFixture("ADAPTER-BOUNDARY-LOOKALIKE"), true},
		{"renamed-wrapped-publication-witness", m2PublicationRecoveryFixture("WRAPPED-WITNESS"), true},
		{"renamed-wrapped-operation-boundary", m2PublicationRecoveryFixture("WRAPPED-BOUNDARY"), true},
		{"compact-boundary-full-id-leak", m2PublicationRecoveryFixture("BOUNDARY-FULL-ID"), true},
		{"compact-boundary-publication-id-leak", m2PublicationRecoveryFixture("BOUNDARY-PUBLICATION-ID"), true},
		{"compact-boundary-terminal-result-leak", m2PublicationRecoveryFixture("BOUNDARY-TERMINAL-RESULT"), true},
		{"compact-boundary-status-leak", m2PublicationRecoveryFixture("BOUNDARY-STATUS"), true},
		{"compact-boundary-membership-leak", m2PublicationRecoveryFixture("BOUNDARY-MEMBERSHIP"), true},
		{"compact-boundary-missing-match", m2PublicationRecoveryFixture("BOUNDARY-MISSING-MATCH"), true},
		{"summary-cannot-detect-lookup-collision", m2PublicationRecoveryFixture("SUMMARY-DUPLICATE-COLLISION"), true},
		{"summary-deleted-state-aware-validation", m2PublicationRecoveryFixture("SUMMARY-DELETED-STATE-VALIDATION"), true},
		{"summary-wrong-exact-budget", m2PublicationRecoveryFixture("SUMMARY-WRONG-BUDGET"), true},
		{"compact-boundary-authority-method-leak", m2PublicationRecoveryFixture("BOUNDARY-METHOD-LEAK"), true},
		{"compact-boundary-variable-digest", m2PublicationRecoveryFixture("BOUNDARY-VARIABLE-DIGEST"), true},
		{"compact-boundary-wide-version", m2PublicationRecoveryFixture("BOUNDARY-WIDE-VERSION"), true},
		{"compact-boundary-renamed-field", m2PublicationRecoveryFixture("BOUNDARY-RENAMED-FIELD"), true},
		{"compact-boundary-exported-field", m2PublicationRecoveryFixture("BOUNDARY-EXPORTED-FIELD"), true},
		{"summary-extra-authority-input", m2PublicationRecoveryFixture("SUMMARY-EXTRA-AUTHORITY"), true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoFixture(t, root, "go.mod", "module example.test/recovery-ownership\n\ngo 1.26\n")
			for path, source := range fixture.files {
				writeGoFixture(t, root, path, source)
			}
			setIsolatedGoPaths(t)
			t.Setenv("GOPROXY", "off")
			err := inspectM2FoundationContractOwnership(t.Context(), root, "example.test/recovery-ownership")
			if (err != nil) != fixture.wantError {
				t.Fatalf("evidence=FOUNDATION-CONTRACT-OWNERSHIP fixture=%s observed-error=%v expected-error=%t", fixture.name, err, fixture.wantError)
			}
		})
	}
}

func m2PublicationRecoveryFixture(variant string) map[string]string {
	if !slices.Contains([]string{
		"COMPLETE", "MISSING-TYPE", "MISSING-HELPER", "MISSING-BOUNDARIES",
		"ADAPTER-WITNESS", "ADAPTER-BOUNDARY-LOOKALIKE", "WRAPPED-WITNESS", "WRAPPED-BOUNDARY",
		"BOUNDARY-FULL-ID", "BOUNDARY-PUBLICATION-ID", "BOUNDARY-TERMINAL-RESULT", "BOUNDARY-STATUS",
		"BOUNDARY-MEMBERSHIP", "BOUNDARY-MISSING-MATCH", "SUMMARY-DUPLICATE-COLLISION",
		"SUMMARY-DELETED-STATE-VALIDATION", "SUMMARY-WRONG-BUDGET",
		"BOUNDARY-METHOD-LEAK", "BOUNDARY-VARIABLE-DIGEST", "BOUNDARY-WIDE-VERSION",
		"BOUNDARY-RENAMED-FIELD", "BOUNDARY-EXPORTED-FIELD", "SUMMARY-EXTRA-AUTHORITY",
	}, variant) {
		return nil
	}
	files := m2FoundationOwnershipFixture("COMPLETE")
	helpersToDeclare := m2PublicationRecoveryHelperNames()
	if variant == "MISSING-HELPER" {
		helpersToDeclare = helpersToDeclare[:len(helpersToDeclare)-1]
	}
	boundaryFields := "operationIDLookupDigest Digest; logicalVersion LogicalVersion; leafDigest Digest"
	switch variant {
	case "BOUNDARY-FULL-ID":
		boundaryFields += "; operationID OperationID"
	case "BOUNDARY-PUBLICATION-ID":
		boundaryFields += "; publicationID PublicationID"
	case "BOUNDARY-TERMINAL-RESULT":
		boundaryFields += "; terminalResult []byte"
	case "BOUNDARY-STATUS":
		boundaryFields += "; status OperationStatus"
	case "BOUNDARY-MEMBERSHIP":
		boundaryFields += "; eventMembership []Digest"
	case "BOUNDARY-RENAMED-FIELD":
		boundaryFields = "lookup Digest; logicalVersion LogicalVersion; leafDigest Digest"
	case "BOUNDARY-EXPORTED-FIELD":
		boundaryFields = "OperationIDLookupDigest Digest; logicalVersion LogicalVersion; leafDigest Digest"
	}
	var declarations strings.Builder
	if variant == "BOUNDARY-VARIABLE-DIGEST" {
		declarations.WriteString("type Digest string\n")
	} else {
		declarations.WriteString("type Digest [32]byte\n")
	}
	if variant == "BOUNDARY-WIDE-VERSION" {
		declarations.WriteString("type LogicalVersion string\n")
	} else {
		declarations.WriteString("type LogicalVersion uint64\n")
	}
	declarations.WriteString("type OperationID string\ntype PublicationID string\ntype OperationStatus string\ntype OperationStateV2 struct{}\n")
	if variant != "MISSING-TYPE" {
		declarations.WriteString("type PublicationWitnessV2 struct{}\n")
	}
	for _, name := range helpersToDeclare {
		switch name {
		case "BuildOperationSummaryV2":
			if variant == "SUMMARY-DUPLICATE-COLLISION" {
				declarations.WriteString("func BuildOperationSummaryV2([]OperationBoundaryV2) (OperationSummaryV2, error) { return OperationSummaryV2{}, nil }\n")
			} else {
				declarations.WriteString("func BuildOperationSummaryV2([]OperationStateV2) (OperationSummaryV2, error) { return OperationSummaryV2{}, nil }\n")
			}
		case "ValidateOperationSummaryV2":
			if variant == "SUMMARY-DELETED-STATE-VALIDATION" {
				declarations.WriteString("func ValidateOperationSummaryV2(OperationSummaryV2) error { return nil }\n")
			} else if variant == "SUMMARY-EXTRA-AUTHORITY" {
				declarations.WriteString("func ValidateOperationSummaryV2(OperationSummaryV2, []OperationStateV2, OperationID) error { return nil }\n")
			} else {
				declarations.WriteString("func ValidateOperationSummaryV2(OperationSummaryV2, []OperationStateV2) error { return nil }\n")
			}
		default:
			fmt.Fprintf(&declarations, "func %s() {}\n", name)
		}
	}
	declarations.WriteString("type OperationBoundaryV2 struct{" + boundaryFields + "}\n")
	declarations.WriteString("func (OperationBoundaryV2) OperationIDLookupDigest() Digest { var value Digest; return value }\n")
	declarations.WriteString("func (OperationBoundaryV2) LogicalVersion() LogicalVersion { var value LogicalVersion; return value }\n")
	declarations.WriteString("func (OperationBoundaryV2) LeafDigest() Digest { var value Digest; return value }\n")
	if variant != "BOUNDARY-MISSING-MATCH" {
		declarations.WriteString("func (OperationBoundaryV2) MatchesOperationID(OperationID) bool { return true }\n")
	}
	if variant == "BOUNDARY-METHOD-LEAK" {
		declarations.WriteString("func (OperationBoundaryV2) OperationID() OperationID { return \"operation-01\" }\n")
	}
	if variant != "MISSING-BOUNDARIES" {
		declarations.WriteString("func (OperationSummaryV2) Boundaries() []OperationBoundaryV2 { return nil }\n")
	}
	if variant == "SUMMARY-WRONG-BUDGET" {
		declarations.WriteString("const MaxOperationSummaryBytesV2 = 2112001\n")
	} else {
		declarations.WriteString("const MaxOperationSummaryBytesV2 = 2112000\n")
	}
	files["internal/controljournal/v2/contracts.go"] += declarations.String()
	switch variant {
	case "ADAPTER-WITNESS":
		files["internal/controlstore/gapdb/adapter.go"] += "type PublicationWitnessV2 struct{}\n"
	case "ADAPTER-BOUNDARY-LOOKALIKE":
		files["internal/controlstore/gapdb/adapter.go"] += "type V2OperationBoundary struct{}\n"
	case "WRAPPED-WITNESS":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Proof journalv2.PublicationWitnessV2
func Decode(Proof) (Parcel, error) { return Parcel{}, nil }
`
	case "WRAPPED-BOUNDARY":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Boundary journalv2.OperationBoundaryV2
var Decode func(*Boundary) (*Parcel, error)
`
	}
	return files
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
		{"generic-record", map[string]string{"internal/mission/store.go": "package mission\ntype Store[K ~string, R any] interface { Get(K)(R,error); Put(K,R)error }\n"}},
		{"record-value", map[string]string{"internal/mission/store.go": "package mission\ntype Record struct{ N int }; type Store interface { Get(string)(Record,error); Put(string,Record)error }\n"}},
		{"record-pointer", map[string]string{"internal/mission/store.go": "package mission\ntype Record struct{ N int }; type Store interface { Get(string)(*Record,error); Put(string,*Record)error }\n"}},
		{"struct-function-fields", map[string]string{"internal/mission/store.go": "package mission\ntype Record struct{ N int }; type Store struct { Get func(string)(Record,error); Put func(string,Record)error }\n"}},
		{"split-methods", map[string]string{
			"internal/mission/read.go":  "package mission\ntype Record struct{ N int }; type Store struct{}; func (Store) Get(string)(Record,error){ return Record{},nil }\n",
			"internal/mission/write.go": "package mission\nfunc (*Store) Put(string,Record)error{return nil}\n",
		}},
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
	results := executeM2EvidenceProbes(t, m2EvidenceProbes())
	if err := validateM2Evidence(results); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"missing", "duplicate", "skip", "substitution", "self-declare"} {
		t.Run(mutation, func(t *testing.T) {
			entries := slices.Clone(results)
			switch mutation {
			case "missing":
				entries = entries[1:]
			case "duplicate":
				entries = append(entries, entries[0])
			case "skip":
				entries[0].Result = "skipped"
			case "substitution":
				entries[0].Validator = entries[1].Validator
			case "self-declare":
				entries = append(entries, m2EvidenceResult{ID: "SELF-DECLARED", Validator: "self", Result: "pass", Receipt: "claimed"})
			}
			if err := validateM2Evidence(entries); err == nil {
				t.Fatalf("evidence=M2-WP01-CENSUS mutant=%s observed=accepted expected=rejected", mutation)
			}
		})
	}
	for _, mutation := range []string{"missing-execution", "duplicate-execution", "substituted-execution"} {
		t.Run(mutation, func(t *testing.T) {
			probes := slices.Clone(m2EvidenceProbes())
			switch mutation {
			case "missing-execution":
				probes = probes[1:]
			case "duplicate-execution":
				probes = append(probes, probes[0])
			case "substituted-execution":
				probes[0].Validator = probes[1].Validator
			}
			if err := validateM2Evidence(executeM2EvidenceProbes(t, probes)); err == nil {
				t.Fatalf("evidence=M2-WP01-CENSUS mutant=%s observed=accepted expected=rejected", mutation)
			}
		})
	}
}

type m2EvidenceProbe struct {
	ID        string
	Validator string
}

type m2EvidenceResult struct {
	ID        string
	Validator string
	Result    string
	Receipt   string
}

func m2EvidenceProbes() []m2EvidenceProbe {
	return []m2EvidenceProbe{
		{"CONTRACT-SCHEMA-COMPILATION", "contract-census-v2"},
		{"EVENT-FAMILY-REGISTRY", "event-registry-v2"},
		{"M1-ENTRY-AND-ARCHIVE", "m1-admission-v1"},
		{"PACKAGE-DIRECTIONS", "repository-dependency-v2"},
		{"RAW-KEY-DENIAL", "repository-raw-store-v2"},
		{"WORKGRAPH-ELIGIBILITY-POLICY", "eligibility-contract-v2"},
		{"KEYSPACE-V2", "keyspace-contract-v2"},
		{"FOUNDATION-CONTRACT-OWNERSHIP", "foundation-contract-ownership-v2"},
		{"M2-MUTANT-CENSUS", "mutant-census-v2"},
	}
}

func executeM2EvidenceProbes(t *testing.T, probes []m2EvidenceProbe) []m2EvidenceResult {
	t.Helper()
	results := make([]m2EvidenceResult, 0, len(probes))
	for _, probe := range probes {
		switch probe.Validator {
		case "contract-census-v2":
			TestM2ContractCensusAndSchemas(t)
		case "event-registry-v2":
			TestM2ContractEventFamilyRegistry(t)
		case "m1-admission-v1":
			TestM2EntryAdmissionIsExactAndLocal(t)
		case "repository-dependency-v2":
			TestM2CurrentRepositoryActivatesCompleteGuard(t)
		case "repository-raw-store-v2":
			TestM2RepositoryHasNoGenericRawKeyAuthority(t)
		case "eligibility-contract-v2":
			TestM2EligibilityGuardPolicyIsFrozenBeforePersistence(t)
		case "keyspace-contract-v2":
			TestM2KeyspaceRegistryIsClosed(t)
		case "foundation-contract-ownership-v2":
			TestM2RecoveryAndPublicationContractsRemainFoundationOwned(t)
		case "mutant-census-v2":
			if observed := verifyM2ExecutableMutants(t); observed != m2MutantCensusCount {
				t.Fatalf("evidence=M2-MUTANT-CENSUS observed-executions=%d expected=%d", observed, m2MutantCensusCount)
			}
		default:
			t.Fatalf("evidence=M2-WP01-CENSUS validator=%s observed=unknown", probe.Validator)
		}
		results = append(results, m2EvidenceResult{ID: probe.ID, Validator: probe.Validator, Result: "pass", Receipt: m2EvidenceReceipt(probe.ID, probe.Validator, "pass")})
	}
	return results
}

func m2EvidenceReceipt(id, validator, result string) string {
	digest := sha256.Sum256([]byte(id + "\x00" + validator + "\x00" + result))
	return hex.EncodeToString(digest[:])
}

func validateM2Evidence(entries []m2EvidenceResult) error {
	probes := m2EvidenceProbes()
	want := make(map[string]string, len(probes))
	for _, probe := range probes {
		want[probe.ID] = probe.Validator
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID == "" || seen[entry.ID] || want[entry.ID] != entry.Validator || entry.Result != "pass" || entry.Receipt != m2EvidenceReceipt(entry.ID, entry.Validator, entry.Result) {
			return fmt.Errorf("evidence=M2-WP01-CENSUS invalid=%+v", entry)
		}
		seen[entry.ID] = true
	}
	if len(seen) != len(want) {
		return fmt.Errorf("evidence=M2-WP01-CENSUS observed=%d expected=%d", len(seen), len(want))
	}
	return nil
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

func inspectM2FoundationContractOwnership(ctx context.Context, root, module string) error {
	if err := validateM2FoundationContractTypeNames(m2FoundationContractTypeNames()); err != nil {
		return err
	}
	if err := validateM2PublicationRecoveryNames(m2PublicationRecoveryTypeNames(), m2PublicationRecoveryHelperNames()); err != nil {
		return err
	}
	sources, err := loadTypedProductionSources(ctx, root, module, runGraphCommand)
	if err != nil {
		return err
	}
	foundationPath := module + "/internal/controljournal/v2"
	adapterPath := module + "/internal/controlstore/gapdb"
	var foundation *productionSourcePackage
	for _, source := range sources {
		if source.importPath == foundationPath {
			foundation = source
			break
		}
	}
	if foundation == nil {
		return nil
	}
	for _, name := range m2FoundationContractTypeNames() {
		if _, ok := foundation.typed.Scope().Lookup(name).(*types.TypeName); !ok {
			return fmt.Errorf("evidence=FOUNDATION-CONTRACT-OWNERSHIP law=complete-foundation-contract owner=%s observed=missing expected-type=%s", foundationPath, name)
		}
	}
	if err := validateM2PublicationRecoveryActivation(foundation, foundationPath); err != nil {
		return err
	}
	for _, source := range sources {
		if !matchesPrefix(source.importPath, []string{adapterPath}) {
			continue
		}
		for _, forbiddenName := range m2AdapterOwnedRecoveryTypeNames() {
			if _, ok := source.typed.Scope().Lookup(forbiddenName).(*types.TypeName); ok {
				return fmt.Errorf("evidence=FOUNDATION-CONTRACT-OWNERSHIP law=no-adapter-recovery-contract owner=%s observed-type=%s expected-owner=%s", source.importPath, forbiddenName, foundationPath)
			}
		}
		function, ownedType, err := m2FirstAdapterOwnedRecoverySignature(source, foundationPath, adapterPath)
		if err != nil {
			return err
		}
		if function != "" {
			return fmt.Errorf("evidence=FOUNDATION-CONTRACT-OWNERSHIP law=foundation-signatures owner=%s callable=%s observed-type=%s expected-owner=%s", source.importPath, function, ownedType, foundationPath)
		}
	}
	return nil
}

func m2FoundationContractTypeNames() []string {
	return []string{
		"ErrorKind", "Error", "StoreRevisionV2", "RecordAddressV2", "ScanTargetV2",
		"ReadRequestV2", "ScanRequestV2", "RecoveryRecordV2", "RecoveryPageV2",
		"CommitObservationV2", "AcknowledgedPublicationV2", "DurablePublicationPortV2",
		"PublicationCoordinatorV2", "ProjectionTrackerV2", "OperationSummaryV2",
		"SealedProjectionPrefixV2",
	}
}

func m2PublicationRecoveryTypeNames() []string {
	return []string{"PublicationWitnessV2", "OperationBoundaryV2"}
}

func m2PublicationRecoveryHelperNames() []string {
	return []string{
		"EncodePublicationWitnessV2", "DecodePublicationWitnessV2", "ValidatePublicationWitnessV2",
		"BuildOperationSummaryV2", "ValidateOperationSummaryV2", "ReducerStateDigestV2",
		"ProjectionPrefixGenesisV2", "AdvanceProjectionPrefixV2",
	}
}

func validateM2PublicationRecoveryNames(typeNames, helperNames []string) error {
	canonical := append(slices.Clone(typeNames), helperNames...)
	slices.Sort(canonical)
	if len(typeNames) != 2 || len(helperNames) != 8 || len(canonical) != 10 {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=closed-symbol-census observed-types=%d observed-helpers=%d expected-types=2 expected-helpers=8", len(typeNames), len(helperNames))
	}
	for index, name := range canonical {
		if name == "" || (index > 0 && canonical[index-1] == name) {
			return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=closed-symbol-census observed=%v", canonical)
		}
	}
	if digest := m2DigestJSON(canonical); digest != m2PublicationRecoverySHA256 {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=closed-symbol-census observed-sha256=%s expected=%s", digest, m2PublicationRecoverySHA256)
	}
	return nil
}

// The corrected contract is predeclared before WP02 lands. Its first exported
// symbol activates the complete surface; a partially introduced proof API is
// never admitted.
func validateM2PublicationRecoveryActivation(foundation *productionSourcePackage, foundationPath string) error {
	scope := foundation.typed.Scope()
	active := false
	for _, name := range append(m2PublicationRecoveryTypeNames(), m2PublicationRecoveryHelperNames()...) {
		if scope.Lookup(name) != nil {
			active = true
			break
		}
	}
	if !active {
		return nil
	}
	for _, name := range m2PublicationRecoveryTypeNames() {
		if _, ok := scope.Lookup(name).(*types.TypeName); !ok {
			return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=complete-activated-surface owner=%s observed=missing expected-type=%s", foundationPath, name)
		}
	}
	for _, name := range m2PublicationRecoveryHelperNames() {
		if _, ok := scope.Lookup(name).(*types.Func); !ok {
			return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=complete-activated-surface owner=%s observed=missing expected-helper=%s", foundationPath, name)
		}
	}
	if err := validateM2CompactOperationBoundary(foundation, foundationPath); err != nil {
		return err
	}
	summary, _ := scope.Lookup("OperationSummaryV2").(*types.TypeName)
	boundaryAccessor := m2MethodSignature(summary, "Boundaries")
	if !m2SignatureIsBoundaryAccessor(boundaryAccessor, foundationPath) {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=defensive-boundary-access owner=%s observed=missing expected-method=OperationSummaryV2.Boundaries", foundationPath)
	}
	if err := validateM2OperationSummaryHelperSignatures(foundation, foundationPath); err != nil {
		return err
	}
	budget, ok := scope.Lookup("MaxOperationSummaryBytesV2").(*types.Const)
	if !ok || budget.Val().ExactString() != "2112000" {
		observed := "missing"
		if ok {
			observed = budget.Val().ExactString()
		}
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=exact-operation-summary-budget owner=%s observed=%s expected=2112000", foundationPath, observed)
	}
	return nil
}

func validateM2CompactOperationBoundary(foundation *productionSourcePackage, foundationPath string) error {
	scope := foundation.typed.Scope()
	boundary, _ := scope.Lookup("OperationBoundaryV2").(*types.TypeName)
	if boundary == nil {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=compact-operation-boundary owner=%s observed=missing", foundationPath)
	}
	named, ok := types.Unalias(boundary.Type()).(*types.Named)
	if !ok {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=compact-operation-boundary owner=%s observed=alias", foundationPath)
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok || structure.NumFields() != 3 {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=compact-operation-boundary owner=%s observed-fields=%d expected-fields=3", foundationPath, m2StructFieldCount(structure))
	}
	want := []struct {
		name     string
		typeName string
	}{
		{"operationIDLookupDigest", "Digest"},
		{"logicalVersion", "LogicalVersion"},
		{"leafDigest", "Digest"},
	}
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if field.Exported() || field.Embedded() || field.Name() != want[index].name || !m2IsFixedBoundaryFieldType(field.Type(), foundationPath, want[index].typeName) {
			return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=compact-operation-boundary owner=%s index=%d observed-field=%s observed-type=%s expected-private-field=%s expected-type=%s", foundationPath, index, field.Name(), field.Type(), want[index].name, want[index].typeName)
		}
	}
	match := m2MethodSignature(boundary, "MatchesOperationID")
	if !m2SignatureIsExactIDMatch(match, foundationPath) {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=exact-operation-id-match owner=%s observed=%v expected=MatchesOperationID(OperationID)bool", foundationPath, match)
	}
	for _, accessor := range []struct {
		name     string
		typeName string
	}{
		{"OperationIDLookupDigest", "Digest"},
		{"LogicalVersion", "LogicalVersion"},
		{"LeafDigest", "Digest"},
	} {
		if !m2SignatureIsFixedBoundaryAccessor(m2MethodSignature(boundary, accessor.name), foundationPath, accessor.typeName) {
			return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=compact-operation-boundary-access owner=%s observed=missing expected-method=%s()%s", foundationPath, accessor.name, accessor.typeName)
		}
	}
	if leakedMethod, leakedType := m2BoundaryMethodLeak(boundary, foundationPath); leakedMethod != "" {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=no-boundary-authority-leak owner=%s method=%s observed-type=%s", foundationPath, leakedMethod, leakedType)
	}
	return nil
}

func m2StructFieldCount(structure *types.Struct) int {
	if structure == nil {
		return 0
	}
	return structure.NumFields()
}

func m2IsFixedBoundaryFieldType(candidate types.Type, foundationPath, name string) bool {
	named, ok := types.Unalias(candidate).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != foundationPath || named.Obj().Name() != name {
		return false
	}
	switch name {
	case "Digest":
		array, ok := named.Underlying().(*types.Array)
		if !ok {
			return false
		}
		basic, elementOK := array.Elem().Underlying().(*types.Basic)
		return elementOK && array.Len() == 32 && basic.Kind() == types.Byte
	case "LogicalVersion":
		basic, ok := named.Underlying().(*types.Basic)
		return ok && basic.Kind() == types.Uint64
	default:
		return false
	}
}

func m2MethodSignature(typeName *types.TypeName, method string) *types.Signature {
	if typeName == nil {
		return nil
	}
	for _, methodSet := range []*types.MethodSet{types.NewMethodSet(typeName.Type()), types.NewMethodSet(types.NewPointer(typeName.Type()))} {
		selection := methodSet.Lookup(nil, method)
		if selection != nil {
			signature, _ := selection.Obj().Type().(*types.Signature)
			return signature
		}
	}
	return nil
}

func m2SignatureIsExactIDMatch(signature *types.Signature, foundationPath string) bool {
	return signature != nil && !signature.Variadic() && signature.Params().Len() == 1 && signature.Results().Len() == 1 &&
		m2IsFoundationNamedType(signature.Params().At(0).Type(), foundationPath, "OperationID") &&
		types.Identical(signature.Results().At(0).Type(), types.Typ[types.Bool])
}

func m2SignatureIsFixedBoundaryAccessor(signature *types.Signature, foundationPath, typeName string) bool {
	return signature != nil && !signature.Variadic() && signature.Params().Len() == 0 && signature.Results().Len() == 1 &&
		m2IsFoundationNamedType(signature.Results().At(0).Type(), foundationPath, typeName)
}

func m2BoundaryMethodLeak(boundary *types.TypeName, foundationPath string) (string, string) {
	allowed := map[string]bool{
		"MatchesOperationID":      true,
		"OperationIDLookupDigest": true,
		"LogicalVersion":          true,
		"LeafDigest":              true,
	}
	for _, methodSet := range []*types.MethodSet{types.NewMethodSet(boundary.Type()), types.NewMethodSet(types.NewPointer(boundary.Type()))} {
		for index := 0; index < methodSet.Len(); index++ {
			method := methodSet.At(index).Obj()
			if !method.Exported() || allowed[method.Name()] {
				continue
			}
			references := make(map[m2NamedTypeReference]bool)
			m2CollectNamedTypeReferences(method.Type(), references, make(map[types.Type]bool))
			for _, name := range []string{"OperationID", "PublicationID", "OperationStatus", "OperationStateV2", "EventID", "EffectID"} {
				if references[m2NamedTypeReference{Owner: foundationPath, Name: name}] {
					return method.Name(), name
				}
			}
			return method.Name(), method.Type().String()
		}
	}
	return "", ""
}

func m2SignatureIsBoundaryAccessor(signature *types.Signature, foundationPath string) bool {
	if signature == nil || signature.Variadic() || signature.Params().Len() != 0 || signature.Results().Len() != 1 {
		return false
	}
	slice, ok := types.Unalias(signature.Results().At(0).Type()).(*types.Slice)
	return ok && m2IsFoundationNamedType(slice.Elem(), foundationPath, "OperationBoundaryV2")
}

func m2IsFoundationNamedType(candidate types.Type, foundationPath, name string) bool {
	named, ok := types.Unalias(candidate).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == foundationPath && named.Obj().Name() == name
}

func validateM2OperationSummaryHelperSignatures(foundation *productionSourcePackage, foundationPath string) error {
	build, _ := foundation.typed.Scope().Lookup("BuildOperationSummaryV2").(*types.Func)
	buildSignature := m2FunctionSignature(build)
	if buildSignature == nil || buildSignature.Variadic() || buildSignature.Params().Len() != 1 || buildSignature.Results().Len() != 2 ||
		!m2IsSliceOfFoundationType(buildSignature.Params().At(0).Type(), foundationPath, "OperationStateV2") ||
		!m2IsFoundationNamedType(buildSignature.Results().At(0).Type(), foundationPath, "OperationSummaryV2") ||
		!types.Identical(buildSignature.Results().At(1).Type(), types.Universe.Lookup("error").Type()) {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=collision-aware-operation-summary owner=%s helper=BuildOperationSummaryV2 observed=%v expected=func([]OperationStateV2)(OperationSummaryV2,error)", foundationPath, buildSignature)
	}
	validate, _ := foundation.typed.Scope().Lookup("ValidateOperationSummaryV2").(*types.Func)
	validateSignature := m2FunctionSignature(validate)
	if validateSignature == nil || validateSignature.Variadic() || validateSignature.Params().Len() != 2 || validateSignature.Results().Len() != 1 ||
		!m2IsFoundationNamedType(validateSignature.Params().At(0).Type(), foundationPath, "OperationSummaryV2") ||
		!m2IsSliceOfFoundationType(validateSignature.Params().At(1).Type(), foundationPath, "OperationStateV2") ||
		!types.Identical(validateSignature.Results().At(0).Type(), types.Universe.Lookup("error").Type()) {
		return fmt.Errorf("evidence=PUBLICATION-RECOVERY-CONTRACT law=state-aware-operation-summary owner=%s helper=ValidateOperationSummaryV2 observed=%v expected=func(OperationSummaryV2,[]OperationStateV2)error", foundationPath, validateSignature)
	}
	return nil
}

func m2FunctionSignature(function *types.Func) *types.Signature {
	if function == nil {
		return nil
	}
	signature, _ := function.Type().(*types.Signature)
	return signature
}

func m2IsSliceOfFoundationType(candidate types.Type, foundationPath, name string) bool {
	slice, ok := types.Unalias(candidate).(*types.Slice)
	return ok && m2IsFoundationNamedType(slice.Elem(), foundationPath, name)
}

func validateM2FoundationContractTypeNames(names []string) error {
	canonical := slices.Clone(names)
	slices.Sort(canonical)
	if len(canonical) != 16 {
		return fmt.Errorf("evidence=FOUNDATION-CONTRACT-OWNERSHIP law=closed-type-census observed-count=%d expected=16", len(canonical))
	}
	for index, name := range canonical {
		if name == "" || (index > 0 && canonical[index-1] == name) {
			return fmt.Errorf("evidence=FOUNDATION-CONTRACT-OWNERSHIP law=closed-type-census observed=%v", canonical)
		}
	}
	if digest := m2DigestJSON(canonical); digest != m2FoundationContractSHA256 {
		return fmt.Errorf("evidence=FOUNDATION-CONTRACT-OWNERSHIP law=closed-type-census observed-sha256=%s expected=%s", digest, m2FoundationContractSHA256)
	}
	return nil
}

func m2AdapterOwnedRecoveryTypeNames() []string {
	return []string{
		"V2Key", "V2Prefix", "V2Record", "V2ScanRequest", "V2ScanPage", "V2Page", "V2CommitResult", "V2RecoveryError",
		"PublicationWitnessV2", "OperationBoundaryV2", "V2PublicationWitness", "V2OperationBoundary",
	}
}

type m2NamedTypeReference struct {
	Owner string
	Name  string
}

const m2MaxExternallyReachableTypeNodes = 65_536

func m2FirstAdapterOwnedRecoverySignature(source *productionSourcePackage, foundationPath, adapterPath string) (string, string, error) {
	provenance := m2TypeDeclarationProvenance(source)
	check := func(name string, candidate types.Type) (string, string) {
		signature, _ := types.Unalias(candidate).Underlying().(*types.Signature)
		if signature == nil {
			return "", ""
		}
		references := make(map[m2NamedTypeReference]bool)
		m2CollectNamedTypeReferences(signature, references, make(map[types.Type]bool))
		m2ExpandTypeDeclarationProvenance(references, provenance)
		foundationContract := false
		owned := ""
		foundationTypes := append(m2FoundationContractTypeNames(), m2PublicationRecoveryTypeNames()...)
		for reference := range references {
			if reference.Owner == foundationPath && slices.Contains(foundationTypes, reference.Name) {
				foundationContract = true
			}
			if matchesPrefix(reference.Owner, []string{adapterPath, "github.com/spec-kitty/gapdb"}) {
				candidate := reference.Owner + "." + reference.Name
				if owned == "" || candidate < owned {
					owned = candidate
				}
			}
		}
		if foundationContract && owned != "" {
			return name, owned
		}
		return "", ""
	}
	seen := make(map[types.Type]bool)
	visited := 0
	var walk func(string, types.Type) (string, string, error)
	walk = func(path string, candidate types.Type) (string, string, error) {
		if candidate == nil || seen[candidate] {
			return "", "", nil
		}
		seen[candidate] = true
		visited++
		if visited > m2MaxExternallyReachableTypeNodes {
			return "", "", fmt.Errorf("evidence=FOUNDATION-CONTRACT-OWNERSHIP law=bounded-public-type-graph owner=%s observed-nodes>%d", source.importPath, m2MaxExternallyReachableTypeNodes)
		}
		if function, owner := check(path, candidate); function != "" {
			return function, owner, nil
		}
		walkOne := func(suffix string, nested types.Type) (string, string, error) {
			return walk(path+suffix, nested)
		}
		walkTuple := func(suffix string, tuple *types.Tuple) (string, string, error) {
			for index := 0; tuple != nil && index < tuple.Len(); index++ {
				if function, owner, err := walkOne(fmt.Sprintf(".%s[%d]", suffix, index), tuple.At(index).Type()); function != "" || err != nil {
					return function, owner, err
				}
			}
			return "", "", nil
		}
		walkParameters := func(suffix string, parameters *types.TypeParamList) (string, string, error) {
			for index := 0; parameters != nil && index < parameters.Len(); index++ {
				if function, owner, err := walkOne(fmt.Sprintf(".%s[%d]", suffix, index), parameters.At(index)); function != "" || err != nil {
					return function, owner, err
				}
			}
			return "", "", nil
		}
		walkMethods := func(methodType types.Type) (string, string, error) {
			for _, set := range []*types.MethodSet{types.NewMethodSet(methodType), types.NewMethodSet(types.NewPointer(methodType))} {
				for index := 0; index < set.Len(); index++ {
					method := set.At(index).Obj()
					if !method.Exported() {
						continue
					}
					if function, owner, err := walkOne("."+method.Name(), method.Type()); function != "" || err != nil {
						return function, owner, err
					}
				}
			}
			return "", "", nil
		}
		switch value := candidate.(type) {
		case *types.Alias:
			if function, owner, err := walkParameters("type-parameter", value.TypeParams()); function != "" || err != nil {
				return function, owner, err
			}
			for index := 0; index < value.TypeArgs().Len(); index++ {
				if function, owner, err := walkOne(fmt.Sprintf(".type-argument[%d]", index), value.TypeArgs().At(index)); function != "" || err != nil {
					return function, owner, err
				}
			}
			return walkOne(".alias", types.Unalias(value))
		case *types.Named:
			if function, owner, err := walkMethods(value); function != "" || err != nil {
				return function, owner, err
			}
			if function, owner, err := walkParameters("type-parameter", value.TypeParams()); function != "" || err != nil {
				return function, owner, err
			}
			for index := 0; index < value.TypeArgs().Len(); index++ {
				if function, owner, err := walkOne(fmt.Sprintf(".type-argument[%d]", index), value.TypeArgs().At(index)); function != "" || err != nil {
					return function, owner, err
				}
			}
			return walkOne(".underlying", value.Underlying())
		case *types.Pointer:
			return walkOne(".element", value.Elem())
		case *types.Array:
			return walkOne(".element", value.Elem())
		case *types.Slice:
			return walkOne(".element", value.Elem())
		case *types.Map:
			if function, owner, err := walkOne(".key", value.Key()); function != "" || err != nil {
				return function, owner, err
			}
			return walkOne(".element", value.Elem())
		case *types.Chan:
			return walkOne(".element", value.Elem())
		case *types.Tuple:
			return walkTuple("tuple", value)
		case *types.Signature:
			if function, owner, err := walkTuple("parameter", value.Params()); function != "" || err != nil {
				return function, owner, err
			}
			if function, owner, err := walkTuple("result", value.Results()); function != "" || err != nil {
				return function, owner, err
			}
			if function, owner, err := walkParameters("type-parameter", value.TypeParams()); function != "" || err != nil {
				return function, owner, err
			}
			return walkParameters("receiver-type-parameter", value.RecvTypeParams())
		case *types.TypeParam:
			return walkOne(".constraint", value.Constraint())
		case *types.Struct:
			for index := 0; index < value.NumFields(); index++ {
				field := value.Field(index)
				if !field.Exported() && !field.Embedded() {
					continue
				}
				if function, owner, err := walkOne("."+field.Name(), field.Type()); function != "" || err != nil {
					return function, owner, err
				}
			}
		case *types.Interface:
			value.Complete()
			for index := 0; index < value.NumMethods(); index++ {
				method := value.Method(index)
				if !method.Exported() {
					continue
				}
				if function, owner, err := walkOne("."+method.Name(), method.Type()); function != "" || err != nil {
					return function, owner, err
				}
			}
			for index := 0; index < value.NumEmbeddeds(); index++ {
				if function, owner, err := walkOne(fmt.Sprintf(".embedded[%d]", index), value.EmbeddedType(index)); function != "" || err != nil {
					return function, owner, err
				}
			}
		case *types.Union:
			for index := 0; index < value.Len(); index++ {
				if function, owner, err := walkOne(fmt.Sprintf(".term[%d]", index), value.Term(index).Type()); function != "" || err != nil {
					return function, owner, err
				}
			}
		}
		return "", "", nil
	}
	scope := source.typed.Scope()
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		if object == nil || !object.Exported() {
			continue
		}
		if function, owner, err := walk(object.Name(), object.Type()); function != "" || err != nil {
			return function, owner, err
		}
	}
	return "", "", nil
}

func m2TypeDeclarationProvenance(source *productionSourcePackage) map[m2NamedTypeReference]map[m2NamedTypeReference]bool {
	provenance := make(map[m2NamedTypeReference]map[m2NamedTypeReference]bool)
	for _, file := range source.files {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				owner, ok := source.info.Defs[typeSpec.Name].(*types.TypeName)
				if !ok || owner.Pkg() == nil {
					continue
				}
				origin := m2TypeNameReference(owner)
				references := make(map[m2NamedTypeReference]bool)
				collect := func(node ast.Node) bool {
					identifier, ok := node.(*ast.Ident)
					if !ok {
						return true
					}
					referenced, ok := source.info.Uses[identifier].(*types.TypeName)
					if ok && referenced.Pkg() != nil {
						references[m2TypeNameReference(referenced)] = true
					}
					return true
				}
				if typeSpec.TypeParams != nil {
					ast.Inspect(typeSpec.TypeParams, collect)
				}
				ast.Inspect(typeSpec.Type, collect)
				delete(references, origin)
				if len(references) != 0 {
					provenance[origin] = references
				}
			}
		}
	}
	return provenance
}

func m2TypeNameReference(object *types.TypeName) m2NamedTypeReference {
	return m2NamedTypeReference{Owner: object.Pkg().Path(), Name: object.Name()}
}

func m2ExpandTypeDeclarationProvenance(references map[m2NamedTypeReference]bool, provenance map[m2NamedTypeReference]map[m2NamedTypeReference]bool) {
	queue := make([]m2NamedTypeReference, 0, len(references))
	for reference := range references {
		queue = append(queue, reference)
	}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		for referenced := range provenance[current] {
			if references[referenced] {
				continue
			}
			references[referenced] = true
			queue = append(queue, referenced)
		}
	}
}

func m2CollectNamedTypeReferences(candidate types.Type, references map[m2NamedTypeReference]bool, seen map[types.Type]bool) {
	if candidate == nil || seen[candidate] {
		return
	}
	seen[candidate] = true
	switch value := candidate.(type) {
	case *types.Alias:
		if object := value.Obj(); object != nil && object.Pkg() != nil {
			references[m2NamedTypeReference{Owner: object.Pkg().Path(), Name: object.Name()}] = true
		}
		m2CollectNamedTypeReferences(types.Unalias(value), references, seen)
	case *types.Named:
		if object := value.Obj(); object != nil && object.Pkg() != nil {
			references[m2NamedTypeReference{Owner: object.Pkg().Path(), Name: object.Name()}] = true
		}
		for index := 0; index < value.TypeArgs().Len(); index++ {
			m2CollectNamedTypeReferences(value.TypeArgs().At(index), references, seen)
		}
		m2CollectNamedTypeReferences(value.Underlying(), references, seen)
	case *types.Pointer:
		m2CollectNamedTypeReferences(value.Elem(), references, seen)
	case *types.Array:
		m2CollectNamedTypeReferences(value.Elem(), references, seen)
	case *types.Slice:
		m2CollectNamedTypeReferences(value.Elem(), references, seen)
	case *types.Map:
		m2CollectNamedTypeReferences(value.Key(), references, seen)
		m2CollectNamedTypeReferences(value.Elem(), references, seen)
	case *types.Chan:
		m2CollectNamedTypeReferences(value.Elem(), references, seen)
	case *types.Tuple:
		for index := 0; index < value.Len(); index++ {
			m2CollectNamedTypeReferences(value.At(index).Type(), references, seen)
		}
	case *types.Signature:
		m2CollectNamedTypeReferences(value.Params(), references, seen)
		m2CollectNamedTypeReferences(value.Results(), references, seen)
		if parameters := value.TypeParams(); parameters != nil {
			for index := 0; index < parameters.Len(); index++ {
				m2CollectNamedTypeReferences(parameters.At(index), references, seen)
			}
		}
		if parameters := value.RecvTypeParams(); parameters != nil {
			for index := 0; index < parameters.Len(); index++ {
				m2CollectNamedTypeReferences(parameters.At(index), references, seen)
			}
		}
	case *types.TypeParam:
		m2CollectNamedTypeReferences(value.Constraint(), references, seen)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			m2CollectNamedTypeReferences(value.Field(index).Type(), references, seen)
		}
	case *types.Interface:
		for index := 0; index < value.NumMethods(); index++ {
			m2CollectNamedTypeReferences(value.Method(index).Type(), references, seen)
		}
		for index := 0; index < value.NumEmbeddeds(); index++ {
			m2CollectNamedTypeReferences(value.EmbeddedType(index), references, seen)
		}
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			m2CollectNamedTypeReferences(value.Term(index).Type(), references, seen)
		}
	}
}

func m2FoundationOwnershipFixture(variant string) map[string]string {
	if !slices.Contains([]string{
		"COMPLETE", "MISSING", "ADAPTER-OWNED", "RENAMED-ADAPTER", "RENAMED-CALLABLE",
		"NAMED-FUNCTION-TYPE", "ALIAS-FUNCTION-TYPE", "UNRELATED-NAMED-FUNCTION", "UNRELATED-ALIAS-FUNCTION",
		"USED-GENERIC-CONSTRAINT", "UNUSED-GENERIC-CONSTRAINT", "UNION-CONSTRAINT", "GENERIC-ALIAS",
		"DEFINED-REQUEST-WRAPPER", "TRANSITIVE-DEFINED-WRAPPER", "VARIABLE-WRAPPER-POINTER", "FIELD-WRAPPER-POINTER",
		"NAMED-CALLABLE-POINTER", "INTERFACE-METHOD-POINTER", "RECEIVER-GENERIC-CONSTRAINT",
		"UNRELATED-GENERIC-CONSTRAINT", "FOUNDATION-ONLY-GENERIC", "UNRELATED-VARIABLE", "UNRELATED-FIELD",
		"FOUNDATION-ONLY-RECEIVER", "PRIVATE-GENERIC-CONSTRAINT", "PRIVATE-FIELD-WRAPPER",
		"ANONYMOUS-INTERFACE-VARIABLE", "UNEXPORTED-RECEIVER-VARIABLE", "ANONYMOUS-STRUCT-FUNCTION-FIELD",
		"FUNCTION-RETURNED-RECEIVER", "EXPORTED-FIELD-RECEIVER", "UNRELATED-COMPOSITE",
		"FOUNDATION-ONLY-COMPOSITE", "PRIVATE-UNREACHABLE-TYPE", "RECURSIVE-UNRELATED-COMPOSITE",
		"UNRELATED-PUBLIC", "PRIVATE-HELPER",
	}, variant) {
		return nil
	}
	names := m2FoundationContractTypeNames()
	if variant == "MISSING" {
		names = names[:len(names)-1]
	}
	var declarations strings.Builder
	for _, name := range names {
		if name == "DurablePublicationPortV2" {
			fmt.Fprintf(&declarations, "type %s interface{ publish() }\n", name)
			continue
		}
		fmt.Fprintf(&declarations, "type %s struct{}\n", name)
	}
	files := map[string]string{
		"internal/controljournal/v2/contracts.go": "package v2\n" + declarations.String(),
		"internal/controlstore/gapdb/adapter.go":  "package gapdb\ntype Adapter struct{}\n",
	}
	switch variant {
	case "ADAPTER-OWNED":
		files["internal/controlstore/gapdb/adapter.go"] += "type V2Record struct{}\n"
	case "RENAMED-ADAPTER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import (
    "context"
    journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
)
type Adapter struct{}
type Parcel struct{}
func (Adapter) ReadV2(context.Context, journalv2.ReadRequestV2) (Parcel, error) { return Parcel{}, nil }
`
	case "RENAMED-CALLABLE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import (
    "context"
    journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
)
type Adapter struct{}
type Parcel struct{}
func (Adapter) ReadV2(context.Context, journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
    return journalv2.RecoveryRecordV2{}, nil
}
func (Adapter) Fetch(context.Context, journalv2.ReadRequestV2) (Parcel, error) { return Parcel{}, nil }
`
	case "NAMED-FUNCTION-TYPE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Fetch func(journalv2.ReadRequestV2) (Parcel, error)
`
	case "ALIAS-FUNCTION-TYPE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Fetch = func(journalv2.ReadRequestV2) (Parcel, error)
`
	case "UNRELATED-NAMED-FUNCTION":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import "context"
type Diagnostic struct{}
type Inspect func(context.Context) (Diagnostic, error)
`
	case "UNRELATED-ALIAS-FUNCTION":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import "context"
type Diagnostic struct{}
type Inspect = func(context.Context) (Diagnostic, error)
`
	case "USED-GENERIC-CONSTRAINT":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type RecoveryInput interface{ journalv2.ReadRequestV2 }
func Fetch[T RecoveryInput](value T) (Parcel, error) { return Parcel{}, nil }
`
	case "UNUSED-GENERIC-CONSTRAINT":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
func Fetch[T interface{ journalv2.ReadRequestV2 }]() (Parcel, error) { return Parcel{}, nil }
`
	case "UNION-CONSTRAINT":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
func Fetch[T interface{ journalv2.ReadRequestV2 | journalv2.ScanRequestV2 }](value T) (*Parcel, error) {
    return &Parcel{}, nil
}
`
	case "GENERIC-ALIAS":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Fetch[T interface{ journalv2.ReadRequestV2 }] = func(T) (*Parcel, error)
`
	case "DEFINED-REQUEST-WRAPPER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Request journalv2.ReadRequestV2
func Fetch(Request) (Parcel, error) { return Parcel{}, nil }
`
	case "TRANSITIVE-DEFINED-WRAPPER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type BaseRequest journalv2.ReadRequestV2
type Request BaseRequest
func Fetch(*Request) (Parcel, error) { return Parcel{}, nil }
`
	case "VARIABLE-WRAPPER-POINTER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Request journalv2.ReadRequestV2
var Fetch func(*Request) (*Parcel, error)
`
	case "FIELD-WRAPPER-POINTER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Request journalv2.ReadRequestV2
type Adapter struct { Fetch func(*Request) (*Parcel, error) }
`
	case "NAMED-CALLABLE-POINTER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Request journalv2.ReadRequestV2
type Fetch func(*Request) (*Parcel, error)
`
	case "INTERFACE-METHOD-POINTER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Request journalv2.ReadRequestV2
type Reader interface { Fetch(*Request) (*Parcel, error) }
`
	case "RECEIVER-GENERIC-CONSTRAINT":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Adapter[T interface{ journalv2.ReadRequestV2 }] struct{}
func (Adapter[T]) Fetch() (Parcel, error) { return Parcel{}, nil }
`
	case "UNRELATED-GENERIC-CONSTRAINT":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
type Parcel struct{}
type Input interface{ string | []byte }
func Fetch[T Input](value T) (Parcel, error) { return Parcel{}, nil }
`
	case "FOUNDATION-ONLY-GENERIC":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
func Read[T interface{ journalv2.ReadRequestV2 }](value T) (journalv2.RecoveryRecordV2, error) {
    return journalv2.RecoveryRecordV2{}, nil
}
`
	case "UNRELATED-VARIABLE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
type Diagnostic struct{}
var Inspect func() (Diagnostic, error)
`
	case "UNRELATED-FIELD":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
type Diagnostic struct{}
type Adapter struct { Inspect func() (Diagnostic, error) }
`
	case "FOUNDATION-ONLY-RECEIVER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Adapter[T interface{ journalv2.ReadRequestV2 }] struct{}
func (Adapter[T]) Read() (journalv2.RecoveryRecordV2, error) {
    return journalv2.RecoveryRecordV2{}, nil
}
`
	case "PRIVATE-GENERIC-CONSTRAINT":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
func fetch[T interface{ journalv2.ReadRequestV2 }]() (Parcel, error) { return Parcel{}, nil }
`
	case "PRIVATE-FIELD-WRAPPER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type Request journalv2.ReadRequestV2
type Adapter struct { fetch func(*Request) (*Parcel, error) }
`
	case "ANONYMOUS-INTERFACE-VARIABLE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
var Reader interface { Fetch(journalv2.ReadRequestV2) (Parcel, error) }
`
	case "UNEXPORTED-RECEIVER-VARIABLE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type service struct{}
func (service) Fetch(journalv2.ReadRequestV2) (Parcel, error) { return Parcel{}, nil }
var Reader service
`
	case "ANONYMOUS-STRUCT-FUNCTION-FIELD":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
var Reader struct { Fetch func(journalv2.ReadRequestV2) (Parcel, error) }
`
	case "FUNCTION-RETURNED-RECEIVER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type service struct{}
func (service) Fetch(journalv2.ReadRequestV2) (Parcel, error) { return Parcel{}, nil }
func Reader() service { return service{} }
`
	case "EXPORTED-FIELD-RECEIVER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type service struct{}
func (service) Fetch(journalv2.ReadRequestV2) (Parcel, error) { return Parcel{}, nil }
type Adapter struct { Reader service }
`
	case "UNRELATED-COMPOSITE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
type Diagnostic struct{}
var Reader struct { Inspect func() (Diagnostic, error) }
`
	case "FOUNDATION-ONLY-COMPOSITE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
var Reader interface {
    Read(journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error)
}
`
	case "PRIVATE-UNREACHABLE-TYPE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Parcel struct{}
type service struct{}
func (service) Fetch(journalv2.ReadRequestV2) (Parcel, error) { return Parcel{}, nil }
var reader service
`
	case "RECURSIVE-UNRELATED-COMPOSITE":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
type Diagnostic struct{}
type node struct {
    Next *node
    Inspect func() (Diagnostic, error)
}
var Reader *node
`
	case "UNRELATED-PUBLIC":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import "context"
type Adapter struct{}
type Diagnostic struct{}
func (Adapter) Inspect(context.Context) (Diagnostic, error) { return Diagnostic{}, nil }
`
	case "PRIVATE-HELPER":
		files["internal/controlstore/gapdb/adapter.go"] = `package gapdb
import journalv2 "example.test/recovery-ownership/internal/controljournal/v2"
type Adapter struct{}
type Parcel struct{}
func (Adapter) decode(journalv2.ReadRequestV2) (Parcel, error) { return Parcel{}, nil }
`
	}
	return files
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
	for _, source := range sources {
		for _, seam := range m2EligibilitySeams(source) {
			if !reachable[source.importPath] {
				return fmt.Errorf("evidence=ELIGIBILITY-POLICY law=typed-owner observed=%s:%s expected=workgraph-or-gapdb-reachable", source.importPath, seam.Name)
			}
			if err := validateM2EligibilitySeam(seam, contract); err != nil {
				return fmt.Errorf("evidence=ELIGIBILITY-POLICY law=seam-dataflow owner=%s callable=%s: %w", source.importPath, seam.Name, err)
			}
		}
	}
	return nil
}

type m2EligibilitySeam struct {
	Name   string
	Values map[string]bool
}

type m2EligibilityAnalyzer struct {
	source       *productionSourcePackage
	initializers map[types.Object]ast.Expr
	functions    map[*types.Func]*ast.FuncDecl
	constructors map[string][]*ast.FuncDecl
}

func m2EligibilitySeams(source *productionSourcePackage) []m2EligibilitySeam {
	analyzer := newM2EligibilityAnalyzer(source)
	var seams []m2EligibilitySeam
	for function, declaration := range analyzer.functions {
		if !m2SignatureReturnsString(function.Type()) {
			continue
		}
		receiver := m2ReceiverTypeName(function)
		if receiver == "" {
			values := analyzer.valuesFrom(declaration.Body)
			if m2ValuesDescribeEligibility(values) {
				seams = append(seams, m2EligibilitySeam{Name: function.Name(), Values: values})
			}
			continue
		}
		constructors := analyzer.constructors[receiver]
		if len(constructors) == 0 {
			values := analyzer.valuesFrom(declaration.Body)
			if m2ValuesDescribeEligibility(values) {
				seams = append(seams, m2EligibilitySeam{Name: receiver + "." + function.Name(), Values: values})
			}
			continue
		}
		for _, constructor := range constructors {
			values := analyzer.valuesFrom(declaration.Body, constructor.Body)
			if m2ValuesDescribeEligibility(values) {
				seams = append(seams, m2EligibilitySeam{Name: constructor.Name.Name + "->" + receiver + "." + function.Name(), Values: values})
			}
		}
	}
	for _, file := range source.files {
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, specification := range generic.Specs {
				values, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range values.Names {
					object := source.info.Defs[name]
					if object == nil || !m2SignatureReturnsString(object.Type()) || index >= len(values.Values) {
						continue
					}
					evidence := analyzer.valuesFrom(values.Values[index])
					if m2ValuesDescribeEligibility(evidence) {
						seams = append(seams, m2EligibilitySeam{Name: name.Name, Values: evidence})
					}
				}
			}
		}
	}
	slices.SortFunc(seams, func(left, right m2EligibilitySeam) int { return strings.Compare(left.Name, right.Name) })
	return seams
}

func newM2EligibilityAnalyzer(source *productionSourcePackage) *m2EligibilityAnalyzer {
	analyzer := &m2EligibilityAnalyzer{
		source: source, initializers: make(map[types.Object]ast.Expr), functions: make(map[*types.Func]*ast.FuncDecl), constructors: make(map[string][]*ast.FuncDecl),
	}
	for _, file := range source.files {
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				function, _ := source.info.Defs[value.Name].(*types.Func)
				if function != nil {
					analyzer.functions[function] = value
					if result := m2FirstResultTypeName(function.Type()); result != "" {
						analyzer.constructors[result] = append(analyzer.constructors[result], value)
					}
				}
			case *ast.GenDecl:
				for _, specification := range value.Specs {
					values, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, name := range values.Names {
						if index < len(values.Values) {
							analyzer.initializers[source.info.Defs[name]] = values.Values[index]
						}
					}
				}
			}
		}
	}
	return analyzer
}

func (analyzer *m2EligibilityAnalyzer) valuesFrom(nodes ...ast.Node) map[string]bool {
	values := make(map[string]bool)
	seen := make(map[types.Object]bool)
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		ast.Inspect(node, func(candidate ast.Node) bool {
			if candidate == nil {
				return true
			}
			if expression, ok := candidate.(ast.Expr); ok {
				if typed, found := analyzer.source.info.Types[expression]; found && typed.Value != nil {
					switch typed.Value.Kind() {
					case constant.String:
						values[constant.StringVal(typed.Value)] = true
					case constant.Int:
						values[typed.Value.ExactString()] = true
					}
				}
			}
			identifier, ok := candidate.(*ast.Ident)
			if !ok {
				return true
			}
			object := analyzer.source.info.ObjectOf(identifier)
			if object == nil || seen[object] {
				return true
			}
			seen[object] = true
			if initializer := analyzer.initializers[object]; initializer != nil {
				visit(initializer)
			}
			if function, ok := object.(*types.Func); ok {
				if declaration := analyzer.functions[function]; declaration != nil {
					visit(declaration.Body)
				}
			}
			return true
		})
	}
	for _, node := range nodes {
		visit(node)
	}
	return values
}

func m2ReceiverTypeName(function *types.Func) string {
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil {
		return ""
	}
	return m2NamedTypeName(signature.Recv().Type())
}

func m2FirstResultTypeName(candidate types.Type) string {
	signature, _ := candidate.(*types.Signature)
	if signature == nil || signature.Results().Len() != 1 {
		return ""
	}
	return m2NamedTypeName(signature.Results().At(0).Type())
}

func m2NamedTypeName(candidate types.Type) string {
	candidate = types.Unalias(candidate)
	if pointer, ok := candidate.(*types.Pointer); ok {
		candidate = types.Unalias(pointer.Elem())
	}
	named, _ := candidate.(*types.Named)
	if named == nil || named.Obj() == nil {
		return ""
	}
	return named.Obj().Name()
}

func m2ValuesDescribeEligibility(values map[string]bool) bool {
	hasRoot, hasWorkgraph, hasEligibility := false, false, false
	for value := range values {
		lower := strings.ToLower(value)
		hasRoot = hasRoot || strings.Contains(lower, "spk/v2")
		hasWorkgraph = hasWorkgraph || strings.Contains(lower, "workgraph")
		hasEligibility = hasEligibility || strings.Contains(lower, "eligibility")
	}
	return hasRoot && hasWorkgraph && hasEligibility
}

func validateM2EligibilitySeam(seam m2EligibilitySeam, contract m2EligibilityContract) error {
	for _, value := range m2RequiredEligibilityValues(contract) {
		if !seam.Values[value] {
			return fmt.Errorf("observed=missing expected=%q", value)
		}
	}
	for value := range seam.Values {
		if strings.HasPrefix(value, "spk.workgraph.eligibility-shards.") && value != contract.PolicyVersion {
			return fmt.Errorf("observed=conflicting-policy-version value=%q", value)
		}
		if strings.Contains(value, "mission-work") && strings.Contains(value, "sha256") && value != contract.HashAlgorithm {
			return fmt.Errorf("observed=conflicting-hash value=%q", value)
		}
		if strings.Contains(value, "spk/v2") && strings.Contains(value, "eligibility") && value != m2EligibilityTemplate {
			return fmt.Errorf("observed=conflicting-template value=%q", value)
		}
	}
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

func m2SignatureReturnsString(candidate types.Type) bool {
	signature, ok := types.Unalias(candidate).Underlying().(*types.Signature)
	if !ok || signature.Results().Len() == 0 {
		return false
	}
	basic, ok := types.Unalias(signature.Results().At(0).Type()).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
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
type eligibilityEvidence struct {
    template, contract, admission, version, hash string
    shards, entries, writers, trials int
}
type %s struct{ evidence eligibilityEvidence }
func New%s(missionID string) %s {
    return %s{evidence: eligibilityEvidence{
        template: %s,
        contract: evidenceContract,
        admission: evidenceAdmission,
        version: evidenceVersion,
        hash: evidenceHash,
        shards: evidenceShards,
        entries: evidenceEntries,
        writers: evidenceWriters,
        trials: evidenceTrials,
    }}
}
func (codec %s) %s(missionID, shardID string) string { return codec.evidence.template }
`, packageName, policyVersion, shardCount, m2EligibilityContractSHA256, m2TupleSHA, keyDeclaration, typeName, typeName, typeName, typeName, keyExpression, typeName, functionName)
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

func m2EligibilityPairedSource(packageName string, firstWrong, secondWrong bool) string {
	version := func(wrong bool) string {
		if wrong {
			return "spk.workgraph.eligibility-shards.v2"
		}
		return "spk.workgraph.eligibility-shards.v1"
	}
	shards := func(wrong bool) int {
		if wrong {
			return 128
		}
		return 64
	}
	return fmt.Sprintf(`package %s
type descriptor struct { template, contract, admission, version, hash string; shards, entries, writers, trials int }
type FirstCodec struct{ descriptor descriptor }
type SecondCodec struct{ descriptor descriptor }
func NewFirst() FirstCodec { return FirstCodec{descriptor{%q,%q,%q,%q,"sha256-length-prefixed-mission-work-v1",%d,256,32,100}} }
func NewSecond() SecondCodec { return SecondCodec{descriptor{%q,%q,%q,%q,"sha256-length-prefixed-mission-work-v1",%d,256,32,100}} }
func (codec FirstCodec) Compose(string,string) string { return codec.descriptor.template }
func (codec SecondCodec) Compose(string,string) string { return codec.descriptor.template }
`, packageName,
		m2EligibilityTemplate, m2EligibilityContractSHA256, m2TupleSHA, version(firstWrong), shards(firstWrong),
		m2EligibilityTemplate, m2EligibilityContractSHA256, m2TupleSHA, version(secondWrong), shards(secondWrong))
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
				owners["function:"+name] |= m2TypedRawStorageShape(name, value.Type())
			case *types.TypeName:
				if m2IsAdmittedM1RawBridge(source.importPath, module, name, value.Type()) {
					continue
				}
				if structure, ok := types.Unalias(value.Type()).Underlying().(*types.Struct); ok {
					for index := 0; index < structure.NumFields(); index++ {
						owners[name] |= m2TypedRawStorageShape(structure.Field(index).Name(), structure.Field(index).Type())
					}
				}
				methodSet := types.NewMethodSet(value.Type())
				pointerSet := types.NewMethodSet(types.NewPointer(value.Type()))
				for index := 0; index < methodSet.Len(); index++ {
					method := methodSet.At(index).Obj()
					owners[name] |= m2TypedRawStorageShape(method.Name(), method.Type())
				}
				for index := 0; index < pointerSet.Len(); index++ {
					method := pointerSet.At(index).Obj()
					owners[name] |= m2TypedRawStorageShape(method.Name(), method.Type())
				}
			}
		}
		if owner, found := m2FirstGodStoreOwner(owners); found {
			return fmt.Errorf("evidence=RAW-KEY-DENIAL law=typed-god-store package=%s owner=%s", source.importPath, owner)
		}
	}
	return inspectM2AllSourceRawStorage(root, module)
}

func m2IsAdmittedM1RawBridge(importPath, module, owner string, candidate types.Type) bool {
	if module != m2ModulePath || importPath != m2ModulePath+"/internal/controlstore/gapdb" ||
		!slices.Contains([]string{"provider", "publicClient", "publicProvider"}, owner) || m2TupleSHA == "" {
		return false
	}
	set := types.NewMethodSet(candidate)
	if pointer := types.NewMethodSet(types.NewPointer(candidate)); pointer.Len() > set.Len() {
		set = pointer
	}
	got := make([]string, 0, set.Len())
	for index := 0; index < set.Len(); index++ {
		got = append(got, set.At(index).Obj().Name())
	}
	slices.Sort(got)
	want := []string{"AtomicBatch", "CompareAndSwap", "DeleteIfRevision", "DescribeConfig", "Get", "Health", "Put", "PutIfAbsent", "ScanPrefix", "Status", "Watch"}
	return slices.Equal(got, want)
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

func m2TypedRawStorageShape(name string, candidate types.Type) m2RawStorageShape {
	signature, ok := types.Unalias(candidate).Underlying().(*types.Signature)
	if !ok {
		return 0
	}
	hasStringKey := false
	hasPayloadParameter := false
	for index := 0; index < signature.Params().Len(); index++ {
		parameter := signature.Params().At(index).Type()
		if m2TypeIsRawStringKey(parameter) {
			hasStringKey = true
		} else if !m2TypeIsContext(parameter) {
			hasPayloadParameter = true
		}
	}
	hasValueResult := false
	for index := 0; index < signature.Results().Len(); index++ {
		hasValueResult = hasValueResult || !m2TypeIsError(signature.Results().At(index).Type())
	}
	return m2RawOperationShape(name, hasStringKey, hasPayloadParameter, hasValueResult)
}

func m2RawOperationShape(name string, hasRawKey, hasPayload, hasValueResult bool) m2RawStorageShape {
	if !hasRawKey {
		return 0
	}
	lower := strings.ToLower(name)
	if lower == "access" && hasPayload && hasValueResult {
		return m2RawRead | m2RawWrite
	}
	var shape m2RawStorageShape
	if hasValueResult && m2HasOperationVerb(lower, []string{"read", "get", "load", "fetch", "lookup", "find", "scan", "list", "query"}) {
		shape |= m2RawRead
	}
	if hasPayload && m2HasOperationVerb(lower, []string{"write", "put", "save", "set", "store", "delete", "remove", "append", "update"}) {
		shape |= m2RawWrite
	}
	return shape
}

func m2HasOperationVerb(name string, verbs []string) bool {
	for _, verb := range verbs {
		if strings.HasPrefix(name, verb) || strings.HasSuffix(name, verb) {
			return true
		}
	}
	return false
}

func m2TypeIsRawStringKey(candidate types.Type) bool {
	if _, ok := candidate.(*types.Alias); ok {
		basic, basicOK := types.Unalias(candidate).Underlying().(*types.Basic)
		return basicOK && basic.Kind() == types.String
	}
	if parameter, ok := candidate.(*types.TypeParam); ok {
		normalized := strings.ReplaceAll(types.TypeString(parameter.Constraint(), nil), " ", "")
		return strings.Contains(normalized, "~string")
	}
	if basic, ok := candidate.(*types.Basic); ok {
		return basic.Kind() == types.String
	}
	return false
}

func m2TypeIsContext(candidate types.Type) bool {
	return types.TypeString(candidate, func(pkg *types.Package) string { return pkg.Path() }) == "context.Context"
}

func m2TypeIsError(candidate types.Type) bool {
	return types.TypeString(candidate, nil) == "error"
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
				if typeSpec.TypeParams != nil {
					for _, field := range typeSpec.TypeParams.List {
						for _, name := range field.Names {
							model.aliases[name.Name] = field.Type
						}
					}
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
				model.owners[owner] |= m2ASTRawStorageShape(value.Name.Name, value.Type, model.aliases)
			case *ast.GenDecl:
				if value.Tok != token.TYPE {
					continue
				}
				for _, specification := range value.Specs {
					typeSpec := specification.(*ast.TypeSpec)
					switch composite := typeSpec.Type.(type) {
					case *ast.InterfaceType:
						for _, field := range composite.Methods.List {
							if functionType, ok := field.Type.(*ast.FuncType); ok {
								name := ""
								if len(field.Names) > 0 {
									name = field.Names[0].Name
								}
								model.owners[typeSpec.Name.Name] |= m2ASTRawStorageShape(name, functionType, model.aliases)
							}
						}
					case *ast.StructType:
						for _, field := range composite.Fields.List {
							if functionType, ok := field.Type.(*ast.FuncType); ok {
								name := ""
								if len(field.Names) > 0 {
									name = field.Names[0].Name
								}
								model.owners[typeSpec.Name.Name] |= m2ASTRawStorageShape(name, functionType, model.aliases)
							}
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
			if module == m2ModulePath && key == m2ModulePath+"/internal/controlstore/gapdb#gapdb" && slices.Contains([]string{"provider", "publicClient", "publicProvider"}, owner) {
				continue
			}
			return fmt.Errorf("evidence=RAW-KEY-DENIAL law=all-source-god-store package=%s owner=%s", key, owner)
		}
	}
	return nil
}

func m2ASTRawStorageShape(name string, signature *ast.FuncType, aliases map[string]ast.Expr) m2RawStorageShape {
	parameters := m2ASTFieldKinds(signature.Params, aliases)
	results := m2ASTFieldKinds(signature.Results, aliases)
	hasRawKey := slices.Contains(parameters, "string")
	hasPayload := false
	for _, kind := range parameters {
		if kind != "string" && kind != "context" {
			hasPayload = true
		}
	}
	hasValueResult := false
	for _, kind := range results {
		if kind != "error" {
			hasValueResult = true
		}
	}
	return m2RawOperationShape(name, hasRawKey, hasPayload, hasValueResult)
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
		if value.Name == "error" {
			return "error"
		}
		if alias := aliases[value.Name]; alias != nil && !seen[value.Name] {
			seen[value.Name] = true
			return m2ASTTypeKind(alias, aliases, seen)
		}
		return "payload"
	case *ast.SelectorExpr:
		if base, ok := value.X.(*ast.Ident); ok && base.Name == "context" && value.Sel.Name == "Context" {
			return "context"
		}
		return "payload"
	case *ast.ArrayType, *ast.StarExpr, *ast.MapType, *ast.StructType:
		return "payload"
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
