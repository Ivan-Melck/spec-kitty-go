package architecture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	m2MutantCensusCount  = 185
	m2MutantCensusSHA256 = "bb4e46d391a130b5050eab4ee4a1193755e4747dac09537d52636a953f194379"
)

const (
	m2ValidatorImport       = "all-go-imports-v1"
	m2ValidatorReach        = "production-reachability-v1"
	m2ValidatorPolicy       = "closed-policy-v2"
	m2ValidatorKeyspace     = "keyspace-contract-v2"
	m2ValidatorEligibility  = "eligibility-seams-v1"
	m2ValidatorDependency   = "dependency-graph-v2"
	m2ValidatorEvents       = "event-registry-v2"
	m2ValidatorLegacy       = "legacy-edge-filter-v1"
	m2ValidatorRawStore     = "raw-store-census-v1"
	m2ValidatorRecovery     = "foundation-contract-ownership-v2"
	m2ValidatorCapReach     = "capability-adapter-reachability-v2"
	m2ValidatorDurableCodec = "durable-codec-compatibility-v2"
)

type m2ViolationObservation struct {
	Validator string `json:"validator"`
	Code      string `json:"code"`
	Source    string `json:"source"`
	Target    string `json:"target,omitempty"`
}

type m2MutantCase struct {
	ID            string                 `json:"id"`
	Validator     string                 `json:"validator"`
	Fixture       string                 `json:"fixture"`
	FixtureSHA256 string                 `json:"fixture_sha256"`
	Source        string                 `json:"source,omitempty"`
	Target        string                 `json:"target,omitempty"`
	Kind          ImportKind             `json:"kind,omitempty"`
	Expected      m2ViolationObservation `json:"expected"`
}

type m2ImportObservation struct{ Source, Target string }

func TestM2MutantsAreKilledByOneExecutableCensus(t *testing.T) {
	verifyM2ExecutableMutants(t)
}

func verifyM2ExecutableMutants(t *testing.T) int {
	t.Helper()
	cases := m2ExecutableMutants()
	if err := validateM2ExecutableMutants(cases); err != nil {
		t.Fatal(err)
	}
	for _, mutant := range cases {
		mutant := mutant
		t.Run(mutant.ID, func(t *testing.T) {
			observed := observeM2Mutant(t, mutant)
			if observed != mutant.Expected {
				t.Fatalf("mutant=%s observed=%+v expected=%+v", mutant.ID, observed, mutant.Expected)
			}
		})
	}
	return len(cases)
}

func TestM2MutantCensusRejectsDeletionAndSubstitution(t *testing.T) {
	cases := m2ExecutableMutants()
	if err := validateM2ExecutableMutants(cases); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func([]m2MutantCase) []m2MutantCase
	}{
		{"delete", func(v []m2MutantCase) []m2MutantCase { return v[1:] }},
		{"duplicate", func(v []m2MutantCase) []m2MutantCase { return append(v, v[0]) }},
		{"fixture-substitution", func(v []m2MutantCase) []m2MutantCase { v[0].Fixture = v[1].Fixture; return v }},
		{"validator-substitution", func(v []m2MutantCase) []m2MutantCase { v[0].Validator = m2ValidatorEvents; return v }},
		{"expected-substitution", func(v []m2MutantCase) []m2MutantCase { v[0].Expected.Code = "SELF_REPORTED"; return v }},
		{"noop-validator", func(v []m2MutantCase) []m2MutantCase { v[0].Validator = "noop"; return v }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutant := mutation.mutate(slices.Clone(cases))
			if err := validateM2ExecutableMutants(mutant); err == nil {
				t.Fatalf("evidence=M2-MUTANT-CENSUS mutation=%s observed=accepted", mutation.name)
			}
		})
	}
	swapped := cases[0]
	swapped.Fixture = cases[1].Fixture
	if observed := observeM2Mutant(t, swapped); observed == swapped.Expected {
		t.Fatal("evidence=M2-MUTANT-CENSUS mutation=swapped-fixture observed=false-green")
	}
	noop := cases[0]
	noop.Validator = "noop"
	if observed := observeM2Mutant(t, noop); observed == noop.Expected {
		t.Fatal("evidence=M2-MUTANT-CENSUS mutation=noop-validator observed=false-green")
	}
}

func TestM2ArbiterFixtureBindingProbe(t *testing.T) {
	cases := m2ExecutableMutants()
	find := func(validator, fixture string) m2MutantCase {
		t.Helper()
		for _, candidate := range cases {
			if candidate.Validator == validator && candidate.Fixture == fixture {
				return candidate
			}
		}
		t.Fatalf("evidence=M2-MUTANT-CENSUS fixture=%s/%s observed=missing", validator, fixture)
		return m2MutantCase{}
	}
	for _, swap := range []struct {
		validator string
		from      string
		to        string
	}{
		{m2ValidatorPolicy, "CLASS", "GAPDB-CLASS"},
		{m2ValidatorEvents, "RENAMED", "WRONG-OWNER"},
		{m2ValidatorRecovery, "RENAMED-CALLABLE", "UNRELATED-PUBLIC"},
		{m2ValidatorRecovery, "NAMED-FUNCTION-TYPE", "UNRELATED-NAMED-FUNCTION"},
		{m2ValidatorRecovery, "ALIAS-FUNCTION-TYPE", "UNRELATED-ALIAS-FUNCTION"},
		{m2ValidatorRecovery, "USED-GENERIC-CONSTRAINT", "UNRELATED-GENERIC-CONSTRAINT"},
		{m2ValidatorRecovery, "UNUSED-GENERIC-CONSTRAINT", "FOUNDATION-ONLY-GENERIC"},
		{m2ValidatorRecovery, "UNION-CONSTRAINT", "UNRELATED-GENERIC-CONSTRAINT"},
		{m2ValidatorRecovery, "GENERIC-ALIAS", "UNRELATED-ALIAS-FUNCTION"},
		{m2ValidatorRecovery, "DEFINED-REQUEST-WRAPPER", "UNRELATED-PUBLIC"},
		{m2ValidatorRecovery, "TRANSITIVE-DEFINED-WRAPPER", "UNRELATED-PUBLIC"},
		{m2ValidatorRecovery, "VARIABLE-WRAPPER-POINTER", "UNRELATED-VARIABLE"},
		{m2ValidatorRecovery, "FIELD-WRAPPER-POINTER", "UNRELATED-FIELD"},
		{m2ValidatorRecovery, "NAMED-CALLABLE-POINTER", "UNRELATED-NAMED-FUNCTION"},
		{m2ValidatorRecovery, "INTERFACE-METHOD-POINTER", "PRIVATE-GENERIC-CONSTRAINT"},
		{m2ValidatorRecovery, "RECEIVER-GENERIC-CONSTRAINT", "FOUNDATION-ONLY-RECEIVER"},
		{m2ValidatorRecovery, "ANONYMOUS-INTERFACE-VARIABLE", "FOUNDATION-ONLY-COMPOSITE"},
		{m2ValidatorRecovery, "UNEXPORTED-RECEIVER-VARIABLE", "PRIVATE-UNREACHABLE-TYPE"},
		{m2ValidatorRecovery, "ANONYMOUS-STRUCT-FUNCTION-FIELD", "UNRELATED-COMPOSITE"},
		{m2ValidatorRecovery, "FUNCTION-RETURNED-RECEIVER", "PRIVATE-UNREACHABLE-TYPE"},
		{m2ValidatorRecovery, "EXPORTED-FIELD-RECEIVER", "PRIVATE-UNREACHABLE-TYPE"},
		{m2ValidatorRecovery, "PUBLICATION-BOUNDARY-FULL-ID", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-BOUNDARY-MISSING-MATCH", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-SUMMARY-DUPLICATE-COLLISION", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-SUMMARY-DELETED-STATE-VALIDATION", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-BOUNDARY-METHOD-LEAK", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-MISSING", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-COMPLETION-ORDER", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-SEAL-OMITTED", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-SUMMARY-ENCODE-DELETED", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-SUMMARY-ENCODE-SUBSTITUTED", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-SUMMARY-DECODE-SUBSTITUTED", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-CHECKPOINT-OLD-FIELD", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-CHECKPOINT-SUMMARY-WIRE", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-ZERO-INVERTED", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-ZERO-NOOP", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-SEAL-DEAD-REFERENCE", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-ADDENDUM-ENCODE-ONLY", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-ADDENDUM-DECODE-ONLY", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-ADDENDUM-ACCESSOR-ONLY", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-OTHER-UINT64-ORDER", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-SELECT-BEFORE-SORT", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-COUPLED-COMPLETION-ORDER", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-COVERED-SEAL-HELPER-DISCARDS", "PUBLICATION-COMPLETE"},
		{m2ValidatorRecovery, "PUBLICATION-SUMMARY-DEFINED-BYTE", "PUBLICATION-COMPLETE"},
	} {
		candidate := find(swap.validator, swap.from)
		candidate.Fixture = swap.to
		if err := validateM2ExecutableMutants(replaceM2Mutant(cases, candidate.ID, candidate)); err == nil {
			t.Fatalf("evidence=M2-MUTANT-CENSUS validator=%s swap=%s->%s observed=accepted", swap.validator, swap.from, swap.to)
		}
		if observed := observeM2Mutant(t, candidate); observed == candidate.Expected {
			t.Fatalf("evidence=M2-MUTANT-CENSUS validator=%s swap=%s->%s observed=false-green", swap.validator, swap.from, swap.to)
		}
	}
	for _, validator := range []string{m2ValidatorPolicy, m2ValidatorEvents, m2ValidatorLegacy, m2ValidatorRecovery} {
		candidate := find(validator, map[string]string{m2ValidatorPolicy: "CLASS", m2ValidatorEvents: "RENAMED", m2ValidatorLegacy: "WORKGRAPH-SQLITE", m2ValidatorRecovery: "RENAMED-CALLABLE"}[validator])
		candidate.Fixture = "NOOP"
		if digest := m2ExpectedFixtureDigest(candidate); digest != "" {
			t.Fatalf("evidence=M2-MUTANT-CENSUS validator=%s noop observed-digest=%s expected=empty", validator, digest)
		}
		if observed := observeM2Mutant(t, candidate); observed == candidate.Expected {
			t.Fatalf("evidence=M2-MUTANT-CENSUS validator=%s noop observed=%+v expected=different-from=%+v", validator, observed, candidate.Expected)
		}
	}
}

func replaceM2Mutant(cases []m2MutantCase, id string, replacement m2MutantCase) []m2MutantCase {
	result := slices.Clone(cases)
	for index := range result {
		if result[index].ID == id {
			result[index] = replacement
			return result
		}
	}
	return result
}

func TestM2SemanticPositiveControls(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module example.test/positive\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/mission/mission.go", "package mission\n// modernc.org/sqlite is prose.\nconst Note = `github.com/spec-kitty/gapdb is not a call`\n")
	got, err := scanM2AllGoImports(root, "example.test/positive")
	if err != nil || len(got) != 0 {
		t.Fatalf("evidence=SEMANTIC-DECOYS observed=%v error=%v", got, err)
	}
}

func m2ExecutableMutants() []m2MutantCase {
	var cases []m2MutantCase
	for _, fixture := range []string{"ALIAS", "BLANK", "BUILD-TAG", "DIRECT", "DOT", "GENERATED"} {
		target := m2SQLExpectedTarget(fixture)
		cases = append(cases, m2Case("M-IMPORT-"+fixture, m2ValidatorImport, fixture, "SQL_PROVIDER", "example.test/import/internal/mission", target))
	}
	for _, fixture := range []string{"CONSTRUCTOR-VAR", "EMBED", "GENERIC", "INTERFACE", "METHOD-VALUE", "RENAME"} {
		cases = append(cases, m2Case("M-REACH-"+fixture, m2ValidatorReach, fixture, "FORBIDDEN_REACHABILITY", "example.test/reach/internal/mission", "example.test/reach/internal/controlstore/gapdb"))
	}
	for _, fixture := range []string{"CLASS", "DIRECTION", "GAPDB-CLASS", "GAPDB-DENIAL", "PROVIDER"} {
		cases = append(cases, m2Case("M-POLICY-"+fixture, m2ValidatorPolicy, fixture, "POLICY_SHAPE", fixture, ""))
	}
	for index, entryID := range m2CanonicalKeyIDs() {
		fixture := "PATH-" + strconv.Itoa(index)
		cases = append(cases, m2Case("M-KEY-PATH-"+entryID, m2ValidatorKeyspace, fixture, "KEYSPACE_CONTRACT", entryID, ""))
	}
	for _, fixture := range []string{"AMBIGUOUS", "GLOBAL-HEAD", "MISSION-OPERATION", "MIXED-V1", "RAW", "SCOPE-FIRST-HEAD", "VARIABLE-VERSION", "SCOPE", "MUTABILITY", "SUFFIX", "BUDGET", "WITNESS-DELETE", "WITNESS-SUBSTITUTE", "WITNESS-PLAUSIBLE-PREFIX", "WITNESS-MALFORMED", "WITNESS-MUTABLE", "WITNESS-EXPIRING", "WITNESS-REVISION-CONDITION", "WITNESS-NO-CONDITION"} {
		cases = append(cases, m2Case("M-KEY-"+fixture, m2ValidatorKeyspace, fixture, "KEYSPACE_CONTRACT", fixture, ""))
	}
	for _, fixture := range []string{"ADAPTER", "DECOY-FIRST", "DECOY-LAST", "MULTI-BROKEN", "RUNTIME", "SAME-FILE-FIRST", "SAME-FILE-LAST", "SPLIT-FILE", "UNCLASSIFIED", "VALUE", "WRAPPER"} {
		cases = append(cases, m2Case("M-ELIGIBILITY-"+fixture, m2ValidatorEligibility, fixture, "ELIGIBILITY_CONTRACT", fixture, ""))
	}
	for _, target := range []string{"crypto/rand", "math/rand", "net", "os", "os/exec", "path/filepath", "time"} {
		cases = append(cases, m2DependencyCase("M-PURE-"+strings.NewReplacer("/", "-").Replace(strings.ToUpper(target)), "PURE", m2ModulePath+"/internal/controljournal/v2", target, ImportStandardLibrary, string(RuleDeferredSurface)))
	}
	cases = append(cases, m2DependencyCase(
		"M-PURE-TIME-RESTORE-OWNER-WIDENING", "RESTORE-OWNER-WIDENING",
		m2ModulePath+"/internal/controljournal/v2/restore/worker", "time", ImportStandardLibrary, string(RuleDeferredSurface),
	))
	cases = append(cases, m2DependencyCase("M-GAPDB-SCENARIO-CALLER", "GAPDB-SCENARIO", m2ModulePath+"/internal/scenario", "github.com/spec-kitty/gapdb/gapdb", ImportExternal, string(RuleExternalDirection)))
	for _, fixture := range []string{"DIRECT", "WRAPPER", "WITNESS-DIRECT"} {
		cases = append(cases, m2Case("M-CAPABILITY-REACH-"+fixture, m2ValidatorCapReach, fixture, string(RuleReachability), m2ModulePath+"/internal/execution", m2ModulePath+"/internal/controlstore/gapdb"))
	}
	for _, target := range []string{"github.com/spec-kitty/gapdb/internal/server", "github.com/spec-kitty/gapdb/server", "github.com/spec-kitty/gapdb/gapdb/internal/server", "github.com/spec-kitty/gapdb/gapdb/private-server"} {
		cases = append(cases, m2DependencyCase("M-GAPDB-PRIVATE-"+strings.NewReplacer("/", "-").Replace(strings.ToUpper(target)), "GAPDB-PRIVATE", m2ModulePath+"/internal/controlstore/gapdb", target, ImportExternal, string(RuleDeferredSurface)))
	}
	for _, target := range []string{"acceptance", "artifact", "execution", "gitrepo", "handoff", "integration", "mission", "review", "workspace"} {
		cases = append(cases, m2DependencyCase("M-TRACKER-"+strings.ToUpper(target), "TRACKER", m2ModulePath+"/internal/tracker/beads", m2ModulePath+"/internal/"+target, ImportModuleLocal, string(RuleLocalDirection)))
	}
	for _, fixture := range []string{"MISSING", "OPERATION-REDUCER", "OPERATION-SCHEMA", "ORDERING", "PUBLICATION", "REDUCER-VERSION", "REGISTRY-ID", "REGISTRY-SCHEMA", "RENAMED", "UNEXPECTED", "VERSION", "WRONG-OWNER"} {
		cases = append(cases, m2Case("M-EVENT-"+fixture, m2ValidatorEvents, fixture, "EVENT_REGISTRY", fixture, ""))
	}
	cases = append(cases, m2Case("M-LEGACY-CANNOT-HIDE-WORKGRAPH", m2ValidatorLegacy, "WORKGRAPH-SQLITE", string(RuleDeferredSurface), m2ModulePath+"/internal/workgraph", "modernc.org/sqlite"))
	for _, fixture := range []string{"ALIASES", "BUILD-VARIANT", "EMBEDDING", "FUNCTIONS", "GENERATED", "GENERIC-RECORD", "GENERICS", "INTERFACE", "RECORD-POINTER", "RECORD-VALUE", "SPLIT-METHODS", "STRUCT-FIELDS", "WRAPPER"} {
		cases = append(cases, m2Case("M-RAW-"+fixture, m2ValidatorRawStore, fixture, "RAW_KEY_GOD_STORE", fixture, ""))
	}
	for _, fixture := range []string{
		"ADAPTER-OWNED", "ALIAS-FUNCTION-TYPE", "ANONYMOUS-INTERFACE-VARIABLE", "ANONYMOUS-STRUCT-FUNCTION-FIELD",
		"DEFINED-REQUEST-WRAPPER", "EXPORTED-FIELD-RECEIVER", "FIELD-WRAPPER-POINTER", "FUNCTION-RETURNED-RECEIVER", "GENERIC-ALIAS",
		"INTERFACE-METHOD-POINTER", "MISSING", "NAMED-CALLABLE-POINTER", "NAMED-FUNCTION-TYPE", "RECEIVER-GENERIC-CONSTRAINT",
		"RENAMED-ADAPTER", "RENAMED-CALLABLE", "TRANSITIVE-DEFINED-WRAPPER", "UNION-CONSTRAINT", "UNUSED-GENERIC-CONSTRAINT",
		"UNEXPORTED-RECEIVER-VARIABLE", "USED-GENERIC-CONSTRAINT", "VARIABLE-WRAPPER-POINTER", "WEAKENED-CENSUS",
		"PUBLICATION-MISSING-TYPE", "PUBLICATION-MISSING-HELPER", "PUBLICATION-MISSING-BOUNDARIES",
		"PUBLICATION-ADAPTER-WITNESS", "PUBLICATION-ADAPTER-BOUNDARY-LOOKALIKE",
		"PUBLICATION-WRAPPED-WITNESS", "PUBLICATION-WRAPPED-BOUNDARY", "WEAKENED-PUBLICATION-CENSUS",
		"PUBLICATION-BOUNDARY-FULL-ID", "PUBLICATION-BOUNDARY-PUBLICATION-ID", "PUBLICATION-BOUNDARY-TERMINAL-RESULT",
		"PUBLICATION-BOUNDARY-STATUS", "PUBLICATION-BOUNDARY-MEMBERSHIP", "PUBLICATION-BOUNDARY-MISSING-MATCH",
		"PUBLICATION-SUMMARY-DUPLICATE-COLLISION", "PUBLICATION-SUMMARY-DELETED-STATE-VALIDATION",
		"PUBLICATION-SUMMARY-WRONG-BUDGET", "PUBLICATION-BOUNDARY-METHOD-LEAK",
		"PUBLICATION-BOUNDARY-VARIABLE-DIGEST", "PUBLICATION-BOUNDARY-WIDE-VERSION",
		"PUBLICATION-BOUNDARY-EMBEDDED-FIELD", "PUBLICATION-BOUNDARY-EXPORTED-FIELD", "PUBLICATION-SUMMARY-EXTRA-AUTHORITY",
		"PUBLICATION-COVERED-MISSING", "PUBLICATION-COVERED-SUBSTITUTED", "PUBLICATION-COVERED-ZERO",
		"PUBLICATION-COVERED-COMPLETION-ORDER", "PUBLICATION-COVERED-SEAL-OMITTED",
		"PUBLICATION-SUMMARY-ENCODE-DELETED", "PUBLICATION-SUMMARY-DECODE-DELETED", "PUBLICATION-SUMMARY-ENCODE-SUBSTITUTED", "PUBLICATION-SUMMARY-DECODE-SUBSTITUTED",
		"PUBLICATION-CHECKPOINT-OLD-FIELD", "PUBLICATION-CHECKPOINT-SUMMARY-WIRE", "PUBLICATION-ADAPTER-SUMMARY-WIRE",
		"PUBLICATION-COVERED-ZERO-INVERTED", "PUBLICATION-COVERED-ZERO-NOOP", "PUBLICATION-COVERED-SEAL-DEAD-REFERENCE",
		"PUBLICATION-ADDENDUM-ENCODE-ONLY", "PUBLICATION-ADDENDUM-DECODE-ONLY", "PUBLICATION-ADDENDUM-ACCESSOR-ONLY",
		"PUBLICATION-COVERED-OTHER-UINT64-ORDER", "PUBLICATION-COVERED-SELECT-BEFORE-SORT", "PUBLICATION-SUMMARY-DEFINED-BYTE",
		"PUBLICATION-COVERED-COUPLED-COMPLETION-ORDER", "PUBLICATION-COVERED-SEAL-HELPER-DISCARDS",
	} {
		cases = append(cases, m2Case("M-RECOVERY-OWNERSHIP-"+fixture, m2ValidatorRecovery, fixture, "FOUNDATION_CONTRACT_OWNERSHIP", fixture, ""))
	}
	for _, fixture := range []string{"RAW-STRUCT-ORDER", "TOP-LEVEL-ONLY", "DISCARDED-NORMALIZED-RESULT", "LENGTH-ONLY-RECOVERY", "REMOVED-DECODER-EQUALITY", "OMITTED-CODEC-CLASSIFICATION", "PARTIAL-REPAIR"} {
		cases = append(cases, m2Case("M-DURABLE-CODEC-"+fixture, m2ValidatorDurableCodec, fixture, "DURABLE_CODEC_COMPATIBILITY", fixture, ""))
	}
	return cases
}

func m2Case(id, validator, fixture, code, source, target string) m2MutantCase {
	return m2MutantCase{ID: id, Validator: validator, Fixture: fixture, FixtureSHA256: m2FixtureDigest(validator, fixture), Expected: m2ViolationObservation{Validator: validator, Code: code, Source: source, Target: target}}
}

func m2DependencyCase(id, fixture, source, target string, kind ImportKind, code string) m2MutantCase {
	value := m2Case(id, m2ValidatorDependency, fixture, code, source, target)
	value.Source, value.Target, value.Kind = source, target, kind
	value.FixtureSHA256 = m2DigestJSON(struct {
		Fixture string
		Source  string
		Target  string
		Kind    ImportKind
	}{fixture, source, target, kind})
	return value
}

func observeM2Mutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	t.Helper()
	switch mutant.Validator {
	case m2ValidatorImport:
		return observeM2ImportMutant(t, mutant)
	case m2ValidatorReach:
		return observeM2ReachMutant(t, mutant)
	case m2ValidatorPolicy:
		return observeM2PolicyMutant(t, mutant)
	case m2ValidatorKeyspace:
		return observeM2KeyspaceMutant(mutant)
	case m2ValidatorEligibility:
		return observeM2EligibilityMutant(t, mutant)
	case m2ValidatorDependency:
		return observeM2DependencyMutant(t, mutant)
	case m2ValidatorEvents:
		return observeM2EventMutant(t, mutant)
	case m2ValidatorLegacy:
		return observeM2LegacyMutant(t, mutant)
	case m2ValidatorRawStore:
		return observeM2RawStoreMutant(t, mutant)
	case m2ValidatorRecovery:
		return observeM2RecoveryOwnershipMutant(t, mutant)
	case m2ValidatorCapReach:
		return observeM2CapabilityReachMutant(t, mutant)
	case m2ValidatorDurableCodec:
		if err := validateM2DurableCodecCompatibility(t, mutant.Fixture); err != nil {
			return m2ViolationObservation{Validator: mutant.Validator, Code: "DURABLE_CODEC_COMPATIBILITY", Source: mutant.Fixture}
		}
		return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
	default:
		return m2ViolationObservation{Validator: mutant.Validator, Code: "HARNESS_UNKNOWN_VALIDATOR", Source: mutant.Fixture}
	}
}

func observeM2ImportMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	const module = "example.test/import"
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/mission/m.go", "package mission\n"+m2SQLImportSource(mutant.Fixture)+"\n")
	imports, err := scanM2AllGoImports(root, module)
	if err != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "PARSE_FAILURE", Source: mutant.Fixture}
	}
	for _, edge := range imports {
		if matchesPrefix(edge.Target, m2SQLProviderPrefixes) {
			return m2ViolationObservation{Validator: mutant.Validator, Code: "SQL_PROVIDER", Source: edge.Source, Target: edge.Target}
		}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2ReachMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	const module = "example.test/reach"
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/controlstore/gapdb/p.go", "package gapdb\ntype Provider struct{}\nfunc Open() Provider{return Provider{}}\nfunc(Provider) Identity()string{return \"x\"}\n")
	writeGoFixture(t, root, "internal/wrapper/w.go", "package wrapper\nimport provider \""+module+"/internal/controlstore/gapdb\"\n"+m2ReachFixture(mutant.Fixture)+"\n")
	writeGoFixture(t, root, "internal/mission/m.go", "package mission\nimport w \""+module+"/internal/wrapper\"\nvar _=w.New\n")
	setIsolatedGoPaths(t)
	t.Setenv("GOPROXY", "off")
	graph, err := LoadGraph(t.Context(), root)
	if err != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "LOAD_FAILURE", Source: mutant.Fixture}
	}
	if len(m2ForbiddenPaths(graph, module+"/internal/mission", []string{module + "/internal/controlstore/gapdb"})) != 0 {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "FORBIDDEN_REACHABILITY", Source: module + "/internal/mission", Target: module + "/internal/controlstore/gapdb"}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2PolicyMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	policy, ok := m2PolicyFixture(mutant.Fixture)
	if !ok {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	if validateM2PolicyShape(policy) != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "POLICY_SHAPE", Source: mutant.Fixture}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2KeyspaceMutant(mutant m2MutantCase) m2ViolationObservation {
	registry := slices.Clone(m2KeyspaceRegistry())
	observedSource := mutant.Fixture
	if strings.HasPrefix(mutant.Fixture, "PATH-") {
		index, err := strconv.Atoi(strings.TrimPrefix(mutant.Fixture, "PATH-"))
		if err != nil || index < 0 || index >= len(registry) {
			return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
		}
		observedSource = registry[index].ID
		registry[index].Template = strings.Replace(registry[index].Template, "spk/v2/", "spk/v2/alternate/", 1)
	} else if !mutateM2Keyspace(mutant.Fixture, &registry) {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	if validateM2KeyspaceRegistry(registry) != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "KEYSPACE_CONTRACT", Source: observedSource}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2EligibilityMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	const module = "example.test/eligibility"
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
	files := m2EligibilityFixture(mutant.Fixture)
	if files == nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	for path, source := range files {
		writeGoFixture(t, root, path, source)
	}
	setIsolatedGoPaths(t)
	t.Setenv("GOPROXY", "off")
	contract, err := loadM2EligibilityContract()
	if err == nil {
		err = inspectM2EligibilityModule(t.Context(), root, module, contract)
	}
	if err != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "ELIGIBILITY_CONTRACT", Source: mutant.Fixture}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2DependencyMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	policy := m2ClosedPolicy(t, m2ModulePath)
	packages := []string{mutant.Source}
	if mutant.Kind == ImportModuleLocal {
		packages = append(packages, mutant.Target)
	}
	graph := fixtureGraph(packages, ImportEdge{SourcePackage: mutant.Source, TargetPackage: mutant.Target, Kind: mutant.Kind})
	graph.ModulePath = m2ModulePath
	for _, violation := range m2EvaluateClosedGraph(policy, graph) {
		if violation.SourcePackage == mutant.Source && violation.TargetPackage == mutant.Target {
			return m2ViolationObservation{Validator: mutant.Validator, Code: string(violation.Rule), Source: violation.SourcePackage, Target: violation.TargetPackage}
		}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Source, Target: mutant.Target}
}

func observeM2EventMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	registry, ok := m2EventFixture(mutant.Fixture)
	if !ok {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	payload, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateM2EventRegistry(payload); err != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "EVENT_REGISTRY", Source: mutant.Fixture}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2LegacyMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	if mutant.Fixture != "WORKGRAPH-SQLITE" {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	policy := m2ClosedPolicy(t, m2ModulePath)
	graph := m2LegacyFixture()
	for _, violation := range m2EvaluateIncrementalRepository(policy, graph) {
		if violation.SourcePackage == m2ModulePath+"/internal/workgraph" && violation.TargetPackage == "modernc.org/sqlite" {
			return m2ViolationObservation{Validator: mutant.Validator, Code: string(violation.Rule), Source: violation.SourcePackage, Target: violation.TargetPackage}
		}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2RawStoreMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	const module = "example.test/raw"
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
	files := m2RawFixture(mutant.Fixture)
	if files == nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	for path, source := range files {
		writeGoFixture(t, root, path, source)
	}
	setIsolatedGoPaths(t)
	t.Setenv("GOPROXY", "off")
	if err := inspectM2RawStorageModule(t.Context(), root, module); err != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "RAW_KEY_GOD_STORE", Source: mutant.Fixture}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2RecoveryOwnershipMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	if mutant.Fixture == "WEAKENED-CENSUS" {
		names := m2FoundationContractTypeNames()
		if err := validateM2FoundationContractTypeNames(names[:len(names)-1]); err != nil {
			return m2ViolationObservation{Validator: mutant.Validator, Code: "FOUNDATION_CONTRACT_OWNERSHIP", Source: mutant.Fixture}
		}
		return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
	}
	if mutant.Fixture == "WEAKENED-PUBLICATION-CENSUS" {
		helpers := m2PublicationRecoveryHelperNames()
		if err := validateM2PublicationRecoveryNames(m2PublicationRecoveryTypeNames(), helpers[:len(helpers)-1]); err != nil {
			return m2ViolationObservation{Validator: mutant.Validator, Code: "FOUNDATION_CONTRACT_OWNERSHIP", Source: mutant.Fixture}
		}
		return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
	}
	const module = "example.test/recovery-ownership"
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module "+module+"\n\ngo 1.26\n")
	files := m2FoundationOwnershipFixture(mutant.Fixture)
	if strings.HasPrefix(mutant.Fixture, "PUBLICATION-") {
		files = m2PublicationRecoveryFixture(strings.TrimPrefix(mutant.Fixture, "PUBLICATION-"))
	}
	if files == nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	for path, source := range files {
		writeGoFixture(t, root, path, source)
	}
	setIsolatedGoPaths(t)
	t.Setenv("GOPROXY", "off")
	if err := inspectM2FoundationContractOwnership(t.Context(), root, module); err != nil {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "FOUNDATION_CONTRACT_OWNERSHIP", Source: mutant.Fixture}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func observeM2CapabilityReachMutant(t *testing.T, mutant m2MutantCase) m2ViolationObservation {
	policy := m2ClosedPolicy(t, m2ModulePath)
	graph, ok := m2CapabilityReachFixture(mutant.Fixture)
	if !ok {
		return m2ViolationObservation{Validator: mutant.Validator, Code: "UNKNOWN_FIXTURE", Source: mutant.Fixture}
	}
	for _, violation := range m2CapabilityGapDBReachability(policy, graph) {
		if violation.SourcePackage == mutant.Expected.Source && violation.TargetPackage == mutant.Expected.Target {
			return m2ViolationObservation{Validator: mutant.Validator, Code: string(violation.Rule), Source: violation.SourcePackage, Target: violation.TargetPackage}
		}
	}
	return m2ViolationObservation{Validator: mutant.Validator, Code: "NO_VIOLATION", Source: mutant.Fixture}
}

func validateM2ExecutableMutants(cases []m2MutantCase) error {
	if len(cases) != m2MutantCensusCount {
		return fmt.Errorf("evidence=M2-MUTANT-CENSUS observed-count=%d expected=%d", len(cases), m2MutantCensusCount)
	}
	identities := make([]string, 0, len(cases))
	seen := make(map[string]bool, len(cases))
	for _, candidate := range cases {
		if candidate.ID == "" || candidate.Fixture == "" || !m2KnownValidator(candidate.Validator) || seen[candidate.ID] || candidate.Expected.Validator != candidate.Validator || candidate.Expected.Code == "" || candidate.Expected.Source == "" || candidate.FixtureSHA256 != m2ExpectedFixtureDigest(candidate) {
			return fmt.Errorf("evidence=M2-MUTANT-CENSUS invalid=%+v", candidate)
		}
		seen[candidate.ID] = true
		payload, err := json.Marshal(candidate)
		if err != nil {
			return err
		}
		identities = append(identities, string(payload))
	}
	slices.Sort(identities)
	digest := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	got := hex.EncodeToString(digest[:])
	if got != m2MutantCensusSHA256 {
		return fmt.Errorf("evidence=M2-MUTANT-CENSUS observed-sha256=%s expected=%s", got, m2MutantCensusSHA256)
	}
	return nil
}

func m2ExpectedFixtureDigest(candidate m2MutantCase) string {
	if candidate.Validator == m2ValidatorDependency {
		return m2DigestJSON(struct {
			Fixture string
			Source  string
			Target  string
			Kind    ImportKind
		}{candidate.Fixture, candidate.Source, candidate.Target, candidate.Kind})
	}
	return m2FixtureDigest(candidate.Validator, candidate.Fixture)
}

func m2FixtureDigest(validator, fixture string) string {
	var material any
	switch validator {
	case m2ValidatorImport:
		material = m2SQLImportSource(fixture)
	case m2ValidatorReach:
		material = m2ReachFixture(fixture)
	case m2ValidatorPolicy:
		value, ok := m2PolicyFixture(fixture)
		if !ok {
			return ""
		}
		material = value
	case m2ValidatorEvents:
		value, ok := m2EventFixture(fixture)
		if !ok {
			return ""
		}
		material = value
	case m2ValidatorLegacy:
		if fixture != "WORKGRAPH-SQLITE" {
			return ""
		}
		material = m2LegacyFixture()
	case m2ValidatorKeyspace:
		registry := slices.Clone(m2KeyspaceRegistry())
		if strings.HasPrefix(fixture, "PATH-") {
			index, err := strconv.Atoi(strings.TrimPrefix(fixture, "PATH-"))
			if err != nil || index < 0 || index >= len(registry) {
				return ""
			}
			registry[index].Template = strings.Replace(registry[index].Template, "spk/v2/", "spk/v2/alternate/", 1)
		} else if !mutateM2Keyspace(fixture, &registry) {
			return ""
		}
		material = registry
	case m2ValidatorEligibility:
		material = m2EligibilityFixture(fixture)
	case m2ValidatorRawStore:
		material = m2RawFixture(fixture)
	case m2ValidatorRecovery:
		if fixture == "WEAKENED-CENSUS" {
			names := m2FoundationContractTypeNames()
			material = names[:len(names)-1]
		} else if fixture == "WEAKENED-PUBLICATION-CENSUS" {
			helpers := m2PublicationRecoveryHelperNames()
			material = append(m2PublicationRecoveryTypeNames(), helpers[:len(helpers)-1]...)
		} else {
			fixtureMaterial := m2FoundationOwnershipFixture(fixture)
			if strings.HasPrefix(fixture, "PUBLICATION-") {
				fixtureMaterial = m2PublicationRecoveryFixture(strings.TrimPrefix(fixture, "PUBLICATION-"))
			}
			if fixtureMaterial == nil {
				return ""
			}
			material = fixtureMaterial
		}
	case m2ValidatorCapReach:
		graph, ok := m2CapabilityReachFixture(fixture)
		if !ok {
			return ""
		}
		material = graph
	case m2ValidatorDurableCodec:
		material = m2DurableCodecFixtureMaterial(fixture)
	default:
		return ""
	}
	return m2DigestJSON(material)
}

func m2PolicyFixture(fixture string) (Policy, bool) {
	policy, err := m2ClosedPolicyValue(m2ModulePath)
	if err != nil {
		return Policy{}, false
	}
	switch fixture {
	case "CLASS":
		removeM2Pattern(&policy, m2ModulePath+"/internal/controljournal/v2")
	case "DIRECTION":
		for index := range policy.Tiers {
			if policy.Tiers[index].Name == TierAuthority {
				policy.Tiers[index].AllowedLocalTargets = append(policy.Tiers[index].AllowedLocalTargets, TierAdapter)
			}
		}
	case "GAPDB-CLASS":
		removeM2Pattern(&policy, m2ModulePath+"/internal/controlstore/gapdb")
	case "GAPDB-DENIAL":
		policy.ForbiddenLocalDirections = policy.ForbiddenLocalDirections[1:]
	case "PROVIDER":
		policy.ForbiddenImportPrefixes = removeM2String(policy.ForbiddenImportPrefixes, "modernc.org/sqlite")
	default:
		return Policy{}, false
	}
	return policy, true
}

func m2EventFixture(fixture string) (m2EventRegistry, bool) {
	registry := m2EventRegistry{SchemaVersion: 2, RegistryID: "spec-kitty.authority-event-families.v2", PublicationRule: "one_transition_event_per_aggregate_per_publication", Ordering: []string{"gapdb_record_revision", "event_index", "event_id"}, Families: m2ExpectedEventFamilies()}
	registry.Ordering = slices.Clone(registry.Ordering)
	registry.Families = slices.Clone(registry.Families)
	switch fixture {
	case "MISSING":
		registry.Families = registry.Families[1:]
	case "REDUCER-VERSION":
		registry.Families[0].Reducer = 2
	case "OPERATION-REDUCER":
		registry.Families[1].Reducer = 1
	case "OPERATION-SCHEMA":
		registry.Families[1].EventSchema = 1
	case "ORDERING":
		registry.Ordering[0] = "global_sequence"
	case "PUBLICATION":
		registry.PublicationRule = "many_events_per_aggregate"
	case "REGISTRY-ID":
		registry.RegistryID = "spec-kitty.authority-event-families"
	case "REGISTRY-SCHEMA":
		registry.SchemaVersion = 1
	case "RENAMED":
		registry.Families[0].Family = "runtime"
	case "UNEXPECTED":
		registry.Families[len(registry.Families)-1].Family = "unexpected"
	case "VERSION":
		registry.Families[0].EventSchema = 2
	case "WRONG-OWNER":
		registry.Families[0].Owner = "internal/mission"
	default:
		return m2EventRegistry{}, false
	}
	return registry, true
}

func m2LegacyFixture() Graph {
	graph := fixtureGraph([]string{m2ModulePath + "/internal/controlstore/sqlite", m2ModulePath + "/internal/workgraph"},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/controlstore/sqlite", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal},
		ImportEdge{SourcePackage: m2ModulePath + "/internal/workgraph", TargetPackage: "modernc.org/sqlite", Kind: ImportExternal})
	graph.ModulePath = m2ModulePath
	return graph
}

func m2DigestJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func m2KnownValidator(value string) bool {
	return slices.Contains([]string{m2ValidatorImport, m2ValidatorReach, m2ValidatorPolicy, m2ValidatorKeyspace, m2ValidatorEligibility, m2ValidatorDependency, m2ValidatorEvents, m2ValidatorLegacy, m2ValidatorRawStore, m2ValidatorRecovery, m2ValidatorCapReach, m2ValidatorDurableCodec}, value)
}

func m2SQLImportSource(fixture string) string {
	switch fixture {
	case "ALIAS":
		return `import driver "modernc.org/sqlite/lib"`
	case "BLANK":
		return `import _ "github.com/mattn/go-sqlite3"`
	case "BUILD-TAG":
		return "//go:build never_enabled\n\nimport _ \"github.com/glebarez/sqlite\""
	case "DIRECT":
		return `import "modernc.org/sqlite"`
	case "DOT":
		return `import . "github.com/ncruces/go-sqlite3"`
	case "GENERATED":
		return "// Code generated. DO NOT EDIT.\nimport _ \"zombiezen.com/go/sqlite\""
	default:
		return ""
	}
}

func m2SQLExpectedTarget(fixture string) string {
	switch fixture {
	case "ALIAS":
		return "modernc.org/sqlite/lib"
	case "BLANK":
		return "github.com/mattn/go-sqlite3"
	case "BUILD-TAG":
		return "github.com/glebarez/sqlite"
	case "DIRECT":
		return "modernc.org/sqlite"
	case "DOT":
		return "github.com/ncruces/go-sqlite3"
	case "GENERATED":
		return "zombiezen.com/go/sqlite"
	default:
		return "unknown"
	}
}

func m2CanonicalKeyIDs() []string {
	return []string{"K-AUTHORITY-BINDING", "K-RUNTIME-CURRENT", "K-RUNTIME-EPOCH", "K-OPERATION-BINDING", "K-OPERATION-HEAD", "K-AGGREGATE-HEAD", "K-AGGREGATE-EVENT", "K-CHECKPOINT-SLOT", "K-CHECKPOINT-RECORD", "K-WORKGRAPH-ELIGIBILITY", "K-PUBLICATION-WITNESS"}
}

func m2ReachFixture(fixture string) string {
	switch fixture {
	case "CONSTRUCTOR-VAR":
		return "var New = provider.Open"
	case "EMBED":
		return "type Holder struct{ provider.Provider }\nfunc New() any { return Holder{} }"
	case "GENERIC":
		return "func hold[T any](v T) T { return v }\nfunc New() any { return hold(provider.Open()) }"
	case "INTERFACE":
		return "type Port interface{ Identity() string }\nfunc New() Port { return provider.Open() }"
	case "METHOD-VALUE":
		return "var New = provider.Provider.Identity"
	case "RENAME":
		return "type Harmless = provider.Provider\nfunc New() any { return provider.Open() }"
	default:
		return ""
	}
}

func mutateM2Keyspace(fixture string, registry *[]m2KeyEntry) bool {
	values := *registry
	switch fixture {
	case "AMBIGUOUS":
		values[5].Template = strings.Replace(values[5].Template, "<scope>", "<raw-scope>", 1)
	case "GLOBAL-HEAD":
		values = append(values, m2KeyEntry{ID: "K-GLOBAL", Template: "spk/v2/journal/head"})
	case "MISSION-OPERATION":
		values[3].Template = "spk/v2/missions/<mission-id>/operations/<operation-id>/binding"
	case "MIXED-V1":
		values[0].Template = "spk/v1/authority/binding"
	case "RAW":
		values = append(values, m2KeyEntry{ID: "K-RAW", Template: "<caller-key>"})
	case "SCOPE-FIRST-HEAD":
		values[5].Template = "spk/v2/scopes/<scope>/heads/<aggregate-kind>/<aggregate-id>"
	case "VARIABLE-VERSION":
		values[6].Template = strings.Replace(values[6].Template, "hex16", "decimal", 1)
	case "SCOPE":
		values[0].Scope = "mission"
	case "MUTABILITY":
		values[0].Mutable = true
	case "SUFFIX":
		values[6].SuffixRule = "decimal"
	case "BUDGET":
		values[0].MaxKeyBytes++
	case "WITNESS-DELETE":
		values = values[:len(values)-1]
	case "WITNESS-SUBSTITUTE":
		values[len(values)-1].Template = "spk/v2/publications/<publication-id>/proof"
	case "WITNESS-PLAUSIBLE-PREFIX":
		values[len(values)-1].Template = "spk/v2/operations/<publication-id>/witness"
	case "WITNESS-MALFORMED":
		values[len(values)-1].Template = "spk/v2/publications//<publication-id>/witness"
	case "WITNESS-MUTABLE":
		values[len(values)-1].Mutable = true
	case "WITNESS-EXPIRING":
		values[len(values)-1].Expires = true
	case "WITNESS-REVISION-CONDITION":
		values[len(values)-1].WriteCondition = "require-revision"
	case "WITNESS-NO-CONDITION":
		values[len(values)-1].WriteCondition = ""
	default:
		return false
	}
	*registry = values
	return true
}

func m2CapabilityReachFixture(fixture string) (Graph, bool) {
	capability := m2ModulePath + "/internal/execution"
	coordinator := m2ModulePath + "/internal/controljournal/v2"
	adapter := m2ModulePath + "/internal/controlstore/gapdb"
	var edges []ImportEdge
	switch fixture {
	case "DIRECT":
		edges = []ImportEdge{{SourcePackage: capability, TargetPackage: adapter, Kind: ImportModuleLocal}}
	case "WITNESS-DIRECT":
		// Returning an adapter-owned witness necessarily creates this forbidden
		// kernel-to-adapter edge, independent of the local type's spelling.
		edges = []ImportEdge{
			{SourcePackage: capability, TargetPackage: adapter, Kind: ImportModuleLocal},
			{SourcePackage: adapter, TargetPackage: coordinator, Kind: ImportModuleLocal},
		}
	case "WRAPPER":
		edges = []ImportEdge{
			{SourcePackage: capability, TargetPackage: coordinator, Kind: ImportModuleLocal},
			{SourcePackage: coordinator, TargetPackage: adapter, Kind: ImportModuleLocal},
		}
	default:
		return Graph{}, false
	}
	graph := fixtureGraph([]string{capability, coordinator, adapter}, edges...)
	graph.ModulePath = m2ModulePath
	return graph, true
}

func m2EligibilityFixture(fixture string) map[string]string {
	switch fixture {
	case "ADAPTER":
		return map[string]string{"internal/controlstore/gapdb/codec.go": m2EligibilitySource("gapdb", "wrong")}
	case "DECOY-FIRST":
		return map[string]string{"internal/workgraph/a_decoy.go": m2EligibilityDecoy("workgraph"), "internal/controlstore/gapdb/z_codec.go": m2EligibilitySource("gapdb", "wrong")}
	case "DECOY-LAST":
		return map[string]string{"internal/workgraph/z_decoy.go": m2EligibilityDecoy("workgraph"), "internal/controlstore/gapdb/a_codec.go": m2EligibilitySource("gapdb", "wrong")}
	case "MULTI-BROKEN":
		return map[string]string{"internal/workgraph/a.go": m2EligibilitySource("workgraph", "exact"), "internal/workgraph/z.go": m2EligibilitySource("workgraph", "wrong")}
	case "RUNTIME":
		return map[string]string{"internal/workgraph/e.go": m2EligibilitySource("workgraph", "runtime")}
	case "SAME-FILE-FIRST":
		return map[string]string{"internal/workgraph/e.go": m2EligibilityPairedSource("workgraph", true, false)}
	case "SAME-FILE-LAST":
		return map[string]string{"internal/workgraph/e.go": m2EligibilityPairedSource("workgraph", false, true)}
	case "SPLIT-FILE":
		return map[string]string{"internal/workgraph/evidence.go": m2EligibilityDecoy("workgraph"), "internal/workgraph/codec.go": "package workgraph\nfunc Compose(missionID, shardID string) string { return decoyTemplate }\n"}
	case "UNCLASSIFIED":
		return map[string]string{"internal/unowned/e.go": m2EligibilitySource("unowned", "exact")}
	case "VALUE":
		return map[string]string{"internal/workgraph/e.go": m2EligibilitySource("workgraph", "wrong")}
	case "WRAPPER":
		return map[string]string{"internal/workgraph/e.go": "package workgraph\nimport k \"example.test/eligibility/internal/keybuilder\"\nvar Build = k.EligibilityCodec{}.Compose\n", "internal/keybuilder/k.go": m2EligibilitySource("keybuilder", "wrong")}
	default:
		return nil
	}
}

func m2RawFixture(fixture string) map[string]string {
	switch fixture {
	case "ALIASES":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Key = string\ntype Bytes = []byte\ntype Store interface { Read(Key)(Bytes,error); Write(Key,Bytes)error }\n"}
	case "BUILD-VARIANT":
		return map[string]string{"internal/mission/normal.go": "package mission\nconst Present = true\n", "internal/mission/variant.go": "//go:build never_guard_variant\n\npackage mission\ntype Store interface { Read(string)([]byte,error); Write(string,[]byte)error }\n"}
	case "EMBEDDING":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Raw interface { Read(string)([]byte,error); Write(string,[]byte)error }\ntype Store interface { Raw }\n"}
	case "FUNCTIONS":
		return map[string]string{"internal/mission/store.go": "package mission\nfunc Access(string,[]byte)([]byte,error){return nil,nil}\n"}
	case "GENERATED":
		return map[string]string{"internal/mission/generated.go": "// Code generated by guard-test. DO NOT EDIT.\npackage mission\ntype Store interface { Read(string)([]byte,error); Write(string,[]byte)error }\n"}
	case "GENERICS":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Store[K ~string, V ~[]byte] interface { Read(K)(V,error); Write(K,V)error }\n"}
	case "GENERIC-RECORD":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Store[K ~string, R any] interface { Get(K)(R,error); Put(K,R)error }\n"}
	case "INTERFACE":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Store interface { Read(string)([]byte,error); Write(string,[]byte)error }\n"}
	case "RECORD-POINTER":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Record struct{ N int }; type Store interface { Get(string)(*Record,error); Put(string,*Record)error }\n"}
	case "RECORD-VALUE":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Record struct{ N int }; type Store interface { Get(string)(Record,error); Put(string,Record)error }\n"}
	case "SPLIT-METHODS":
		return map[string]string{"internal/mission/read.go": "package mission\ntype Record struct{ N int }; type Store struct{}; func(Store)Get(string)(Record,error){return Record{},nil}\n", "internal/mission/write.go": "package mission\nfunc(*Store)Put(string,Record)error{return nil}\n"}
	case "STRUCT-FIELDS":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Record struct{ N int }; type Store struct { Get func(string)(Record,error); Put func(string,Record)error }\n"}
	case "WRAPPER":
		return map[string]string{"internal/mission/store.go": "package mission\ntype Client struct{}\nfunc(Client)Read(string)([]byte,error){return nil,nil}\nfunc(Client)Write(string,[]byte)error{return nil}\ntype Wrapper struct{Client}\n"}
	default:
		return nil
	}
}

func scanM2AllGoImports(root, module string) ([]m2ImportObservation, error) {
	var result []m2ImportObservation
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		source := module
		if relative != "." {
			source += "/" + filepath.ToSlash(relative)
		}
		for _, declaration := range file.Imports {
			target, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				return err
			}
			result = append(result, m2ImportObservation{source, target})
		}
		return nil
	})
	return result, err
}

func m2ForbiddenPaths(graph Graph, source string, targets []string) []string {
	adjacent := make(map[string][]string)
	for _, edge := range graph.Edges {
		if edge.Kind != ImportTestOnly {
			adjacent[edge.SourcePackage] = append(adjacent[edge.SourcePackage], edge.TargetPackage)
		}
	}
	queue := []string{source}
	seen := map[string]bool{source: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacent[current] {
			if seen[next] {
				continue
			}
			seen[next] = true
			if matchesPrefix(next, targets) {
				return []string{source, next}
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func TestM2MalformedSourceFailsClosedAndBounded(t *testing.T) {
	root := t.TempDir()
	writeGoFixture(t, root, "go.mod", "module example.test/malformed\n\ngo 1.26\n")
	writeGoFixture(t, root, "internal/mission/broken.go", "package mission\nimport (")
	started := time.Now()
	_, err := scanM2AllGoImports(root, "example.test/malformed")
	if err == nil {
		t.Fatal("malformed source accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("malformed source check exceeded one second")
	}
	if len(err.Error()) > 2048 {
		t.Fatal("malformed source diagnostic exceeded bound")
	}
}
