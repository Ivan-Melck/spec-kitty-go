package architecture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	m2MissionDir                  = "gapdb-only-production-authority-01M1GWDJ"
	m2EventRegistryIdentitySHA256 = "c56f715a05fd8169760fdc78006c82b789fc9c56b948d2426f90f764a2acf1cf"

	m2SourceCommit = "e05a5e46feb3e65ff42c4544c16678c440ef3818"
	m2SourceTree   = "c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73"
	m2ManifestSHA  = "397f93a9b0f1ea24715ba57b0cb58aeb489e13a7e27e8bc76259597f36f2f309"
	m2TupleSHA     = "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d"
	m2SidecarSHA   = "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e"
	m2Signer       = "github:lynncoleart"
	m2ReleaseTag   = "m1-gapdb-evidence-v1"
	m2ArchiveName  = "spec-kitty-m1-crown-e05a5e4.tar.zst"
	m2ArchiveSHA   = "86b8cd608f5cd9d1dce7ce25dffa5d87e1df25a233847316416f14e7948a18df"
)

type m2EventFamily struct {
	Family      string `json:"family"`
	EventSchema int    `json:"event_schema"`
	Reducer     int    `json:"reducer"`
	Owner       string `json:"owner"`
}

type m2EventRegistry struct {
	SchemaVersion   int             `json:"schema_version"`
	RegistryID      string          `json:"registry_id"`
	PublicationRule string          `json:"publication_rule"`
	Ordering        []string        `json:"ordering"`
	Families        []m2EventFamily `json:"families"`
}

type m2AdoptionSignature struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type m2Adoption struct {
	SchemaVersion       int                 `json:"schema_version"`
	ProgramID           string              `json:"program_id"`
	MissionID           string              `json:"mission_id"`
	Decision            string              `json:"decision"`
	EvidenceManifestSHA string              `json:"evidence_manifest_sha256"`
	AdmittedTupleSHA    string              `json:"admitted_tuple_sha256"`
	SourceCommit        string              `json:"source_commit"`
	SourceTree          string              `json:"source_tree"`
	SidecarSHA          string              `json:"sidecar_sha256"`
	Signer              string              `json:"signer"`
	Rationale           string              `json:"rationale"`
	SignedAt            string              `json:"signed_at"`
	Signature           m2AdoptionSignature `json:"signature"`
}

func TestM2ContractCensusAndSchemas(t *testing.T) {
	dir := m2ContractsDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			got = append(got, entry.Name())
		}
	}
	slices.Sort(got)
	want := []string{
		"authority-journal-v2.md",
		"event-family-registry-v2.json",
		"historical-state-disposition-v1.schema.json",
		"m2-qualification-v1.schema.json",
		"operation-binding-v2.schema.json",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("contract=C-M2-CENSUS law=closed-set observed=%v expected=%v", got, want)
	}

	schemas := compileM2Schemas(t)
	if len(schemas) != 3 {
		t.Fatalf("contract=C-M2-SCHEMAS law=exact-schema-count observed=%d expected=3", len(schemas))
	}

	tests := []struct {
		name   string
		schema string
		valid  map[string]any
		mutate func(map[string]any)
	}{
		{
			name: "historical-disposition", schema: "historical-state-disposition-v1.schema.json",
			valid: map[string]any{
				"schema_version": 1, "control_root_sha256": strings.Repeat("a", 64),
				"classification": "historical", "findings": []any{map[string]any{"kind": "sqlite.header", "relative_path": "state.db", "detail_code": "SQLITE_HEADER"}},
				"current_mutation_count": 0, "action": "offline_disposition_required", "diagnosis_code": "OFFLINE_DISPOSITION_REQUIRED",
			},
			mutate: func(value map[string]any) { value["current_mutation_count"] = 1 },
		},
		{
			name: "operation-binding", schema: "operation-binding-v2.schema.json",
			valid: map[string]any{
				"schema_version": 2, "operation_id": "op-1", "command_kind": "mission.advance", "command_schema_version": 1,
				"canonical_intent_sha256": strings.Repeat("b", 64), "scope": map[string]any{"kind": "mission", "mission_id": "mission-1"},
				"originating_runtime_epoch_id": "epoch-1", "observed_created_at": "2026-09-02T12:00:00Z",
			},
			mutate: func(value map[string]any) { value["canonical_intent_sha256"] = "not-a-digest" },
		},
		{
			name: "qualification", schema: "m2-qualification-v1.schema.json",
			valid: m2QualificationFixture(),
			mutate: func(value map[string]any) {
				value["census"].(map[string]any)["sqlite_edges"] = 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled := schemas[test.schema]
			if err := compiled.Validate(test.valid); err != nil {
				t.Fatalf("contract=%s law=positive-boundary observed=%v expected=valid", test.schema, err)
			}
			mutant := cloneM2Map(t, test.valid)
			test.mutate(mutant)
			if err := compiled.Validate(mutant); err == nil {
				t.Fatalf("contract=%s law=closed-negative-boundary observed=accepted expected=rejected", test.schema)
			}
			missing := cloneM2Map(t, test.valid)
			delete(missing, "schema_version")
			if err := compiled.Validate(missing); err == nil {
				t.Fatalf("contract=%s law=required-fields observed=accepted expected=rejected", test.schema)
			}
			extra := cloneM2Map(t, test.valid)
			extra["unregistered_future_field"] = true
			if err := compiled.Validate(extra); err == nil {
				t.Fatalf("contract=%s law=closed-fields observed=accepted expected=rejected", test.schema)
			}
		})
	}

	boundaryMutants := []struct {
		name   string
		schema string
		value  map[string]any
	}{
		{"historical-invalid-enum", "historical-state-disposition-v1.schema.json", func() map[string]any { v := cloneM2Map(t, tests[0].valid); v["classification"] = "maybe"; return v }()},
		{"historical-findings-over-bound", "historical-state-disposition-v1.schema.json", func() map[string]any { v := cloneM2Map(t, tests[0].valid); v["findings"] = make([]any, 257); return v }()},
		{"operation-kind-invalid", "operation-binding-v2.schema.json", func() map[string]any {
			v := cloneM2Map(t, tests[1].valid)
			v["command_kind"] = "Mission Advance"
			return v
		}()},
		{"operation-id-over-bound", "operation-binding-v2.schema.json", func() map[string]any {
			v := cloneM2Map(t, tests[1].valid)
			v["operation_id"] = strings.Repeat("x", 129)
			return v
		}()},
		{"operation-date-invalid", "operation-binding-v2.schema.json", func() map[string]any {
			v := cloneM2Map(t, tests[1].valid)
			v["observed_created_at"] = "yesterday"
			return v
		}()},
		{"qualification-run-over-bound", "m2-qualification-v1.schema.json", func() map[string]any {
			v := cloneM2Map(t, tests[2].valid)
			v["run_id"] = strings.Repeat("r", 129)
			return v
		}()},
		{"qualification-recovery-under-bound", "m2-qualification-v1.schema.json", func() map[string]any {
			v := cloneM2Map(t, tests[2].valid)
			v["performance"].(map[string]any)["recovery_operations"] = 9999
			return v
		}()},
	}
	for _, mutant := range boundaryMutants {
		t.Run(mutant.name, func(t *testing.T) {
			if err := schemas[mutant.schema].Validate(mutant.value); err == nil {
				t.Fatalf("contract=%s law=closed-boundary mutant=%s observed=accepted expected=rejected", mutant.schema, mutant.name)
			}
		})
	}
}

func TestM2ContractEventFamilyRegistry(t *testing.T) {
	payload := readM2File(t, filepath.Join("kitty-specs", m2MissionDir, "contracts", "event-family-registry-v2.json"))
	registry, err := validateM2EventRegistry(payload)
	if err != nil {
		t.Fatal(err)
	}
	wantFamilies := m2ExpectedEventFamilies()
	want := make([]string, 0, len(wantFamilies))
	for _, family := range wantFamilies {
		want = append(want, family.Family+"@"+family.Owner)
	}
	slices.Sort(want)
	got := make([]string, 0, len(registry.Families))
	for _, family := range registry.Families {
		got = append(got, family.Family+"@"+family.Owner)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("contract=C-M2-EVENT-REGISTRY law=closed-family-census observed=%v expected=%v", got, want)
	}
	for _, omitted := range []string{"schema_version", "registry_id", "publication_rule", "ordering", "families"} {
		var document map[string]any
		if err := json.Unmarshal(payload, &document); err != nil {
			t.Fatal(err)
		}
		delete(document, omitted)
		mutant, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := validateM2EventRegistry(mutant); err == nil {
			t.Fatalf("contract=C-M2-EVENT-REGISTRY law=complete-identity omitted=%s observed=accepted expected=rejected", omitted)
		}
	}

	mutants := []struct {
		name   string
		mutate func(*m2EventRegistry)
	}{
		{"registry-schema-version", func(v *m2EventRegistry) { v.SchemaVersion = 1 }},
		{"registry-id", func(v *m2EventRegistry) { v.RegistryID = "spec-kitty.authority-event-families" }},
		{"duplicate-family", func(v *m2EventRegistry) { v.Families = append(v.Families, v.Families[0]) }},
		{"zero-reducer", func(v *m2EventRegistry) { v.Families[0].Reducer = 0 }},
		{"wrong-order", func(v *m2EventRegistry) { v.Ordering[0] = "global_sequence" }},
		{"unnormalized-owner", func(v *m2EventRegistry) { v.Families[0].Owner += "/" }},
		{"wrong-normalized-owner", func(v *m2EventRegistry) { v.Families[0].Owner = "internal/mission" }},
		{"renamed-family", func(v *m2EventRegistry) { v.Families[0].Family = "runtime" }},
		{"missing-family", func(v *m2EventRegistry) { v.Families = v.Families[1:] }},
		{"unexpected-family", func(v *m2EventRegistry) {
			v.Families[len(v.Families)-1] = m2EventFamily{Family: "unexpected", EventSchema: 1, Reducer: 1, Owner: "internal/controlstore"}
		}},
		{"event-version-change", func(v *m2EventRegistry) { v.Families[0].EventSchema = 2 }},
		{"reducer-version-change", func(v *m2EventRegistry) { v.Families[0].Reducer = 2 }},
		{"operation-schema-1", func(v *m2EventRegistry) { v.Families[1].EventSchema = 1 }},
		{"operation-reducer-1", func(v *m2EventRegistry) { v.Families[1].Reducer = 1 }},
		{"open-publication", func(v *m2EventRegistry) { v.PublicationRule = "many_events_per_aggregate" }},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			value := registry
			value.Ordering = slices.Clone(registry.Ordering)
			value.Families = slices.Clone(registry.Families)
			mutant.mutate(&value)
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateM2EventRegistry(encoded); err == nil {
				t.Fatalf("contract=C-M2-EVENT-REGISTRY mutant=%s observed=accepted expected=rejected", mutant.name)
			}
		})
	}
}

func TestM2ContractAuthorityJournalLaws(t *testing.T) {
	text := string(readM2File(t, filepath.Join("kitty-specs", m2MissionDir, "contracts", "authority-journal-v2.md")))
	laws := map[string][]string{
		"C-M2-GLOBAL-OPERATION": {"spk/v2/operations/<operation-id>/binding", "host-global", "IDEMPOTENCY_CONFLICT"},
		"C-M2-ONE-BATCH":        {"exactly one GapDB AtomicBatch", "one positive", "GapDB revision", "At most one event"},
		"C-M2-IMMUTABLE-EVENT":  {"positive 16-character lowercase hexadecimal", "never updated or deleted"},
		"C-M2-DETERMINISTIC":    {"(Record.Revision, EventIndex, EventID)", "pure reduction"},
		"C-M2-PURE-PLAYBACK":    {"No ambiguity path", "calls an external adapter", "pure reduction"},
	}
	for id, phrases := range laws {
		for _, phrase := range phrases {
			if !strings.Contains(text, phrase) {
				t.Fatalf("contract=%s law=normative-phrase observed=missing expected=%q", id, phrase)
			}
		}
	}
}

func TestM2EntryAdmissionIsExactAndLocal(t *testing.T) {
	payload := readM2File(t, filepath.Join("docs", "programs", "evidence", "m1-adoption.json"))
	record, err := validateM2Adoption(payload)
	if err != nil {
		t.Fatal(err)
	}
	closeout := string(readM2File(t, filepath.Join("docs", "programs", "evidence", "m1-adoption-closeout.md")))
	if err := validateM2Closeout(record, closeout); err != nil {
		t.Fatal(err)
	}
	admissionPayload := readM2File(t, filepath.Join("docs", "programs", "evidence", "m1-admission.json"))
	if err := validateM2AdmissionCrosscheck(record, admissionPayload); err != nil {
		t.Fatal(err)
	}

	mutants := []struct {
		name   string
		mutate func(*m2Adoption)
	}{
		{"decision", func(v *m2Adoption) { v.Decision = "refuse" }},
		{"source-commit", func(v *m2Adoption) { v.SourceCommit = strings.Repeat("0", 40) }},
		{"source-tree", func(v *m2Adoption) { v.SourceTree = strings.Repeat("1", 40) }},
		{"manifest", func(v *m2Adoption) { v.EvidenceManifestSHA = strings.Repeat("2", 64) }},
		{"tuple", func(v *m2Adoption) { v.AdmittedTupleSHA = strings.Repeat("3", 64) }},
		{"sidecar", func(v *m2Adoption) { v.SidecarSHA = strings.Repeat("4", 64) }},
		{"signer", func(v *m2Adoption) { v.Signer = "github:someone-else" }},
		{"signature-kind", func(v *m2Adoption) { v.Signature.Kind = "" }},
		{"signature-value", func(v *m2Adoption) { v.Signature.Value = "" }},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			value := record
			mutant.mutate(&value)
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateM2Adoption(encoded); err == nil {
				t.Fatalf("contract=C-M2-M1-ENTRY mutant=%s observed=accepted expected=rejected", mutant.name)
			}
		})
	}

	closeoutMutants := []struct {
		name   string
		mutate func(string) string
	}{
		{"inconsistent-source", func(v string) string { return strings.ReplaceAll(v, m2SourceCommit, strings.Repeat("0", 40)) }},
		{"mutable-release-alias", func(v string) string { return strings.ReplaceAll(v, m2ReleaseTag, "latest") }},
		{"wrong-archive", func(v string) string { return strings.ReplaceAll(v, m2ArchiveName, "different.tar.zst") }},
		{"wrong-archive-digest", func(v string) string { return strings.ReplaceAll(v, m2ArchiveSHA, strings.Repeat("f", 64)) }},
	}
	for _, mutant := range closeoutMutants {
		t.Run("closeout-"+mutant.name, func(t *testing.T) {
			if err := validateM2Closeout(record, mutant.mutate(closeout)); err == nil {
				t.Fatalf("contract=C-M2-M1-ENTRY mutant=%s observed=accepted expected=rejected", mutant.name)
			}
		})
	}
}

func compileM2Schemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(m2ContractsDir(t), "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	ids := make(map[string]string, len(paths))
	seenIDs := make(map[string]string, len(paths))
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(payload, &document); err != nil {
			t.Fatalf("contract=%s law=json-syntax observed=%v expected=valid", filepath.Base(path), err)
		}
		id, ok := document.(map[string]any)["$id"].(string)
		if !ok || id == "" {
			t.Fatalf("contract=%s law=schema-id observed=missing expected=non-empty", filepath.Base(path))
		}
		if prior := seenIDs[id]; prior != "" {
			t.Fatalf("contract=%s law=unique-schema-id observed=duplicate-with-%s expected=unique", filepath.Base(path), prior)
		}
		seenIDs[id] = filepath.Base(path)
		ids[filepath.Base(path)] = id
		if err := compiler.AddResource(id, document); err != nil {
			t.Fatal(err)
		}
	}
	compiled := make(map[string]*jsonschema.Schema, len(ids))
	for name, id := range ids {
		schema, err := compiler.Compile(id)
		if err != nil {
			t.Fatalf("contract=%s law=schema-compilation observed=%v expected=valid", name, err)
		}
		compiled[name] = schema
	}
	return compiled
}

func validateM2EventRegistry(payload []byte) (m2EventRegistry, error) {
	var registry m2EventRegistry
	if err := decodeM2Strict(payload, &registry); err != nil {
		return registry, fmt.Errorf("contract=C-M2-EVENT-REGISTRY law=closed-json: %w", err)
	}
	if registry.SchemaVersion != 2 || registry.RegistryID != "spec-kitty.authority-event-families.v2" || registry.PublicationRule != "one_transition_event_per_aggregate_per_publication" {
		return registry, fmt.Errorf("contract=C-M2-EVENT-REGISTRY law=identity observed=%+v", registry)
	}
	if !slices.Equal(registry.Ordering, []string{"gapdb_record_revision", "event_index", "event_id"}) {
		return registry, fmt.Errorf("contract=C-M2-EVENT-REGISTRY law=ordering observed=%v", registry.Ordering)
	}
	seen := make(map[string]bool, len(registry.Families))
	for _, family := range registry.Families {
		if family.Family == "" || seen[family.Family] || family.EventSchema <= 0 || family.Reducer <= 0 ||
			!strings.HasPrefix(family.Owner, "internal/") || strings.HasSuffix(family.Owner, "/") || strings.Contains(family.Owner, "//") {
			return registry, fmt.Errorf("contract=C-M2-EVENT-REGISTRY law=family observed=%+v", family)
		}
		seen[family.Family] = true
	}
	want := m2ExpectedEventFamilies()
	got := slices.Clone(registry.Families)
	slices.SortFunc(got, func(left, right m2EventFamily) int { return strings.Compare(left.Family, right.Family) })
	slices.SortFunc(want, func(left, right m2EventFamily) int { return strings.Compare(left.Family, right.Family) })
	if !slices.Equal(got, want) {
		return registry, fmt.Errorf("contract=C-M2-EVENT-REGISTRY law=closed-family-tuples observed=%+v expected=%+v", got, want)
	}
	if digest := m2EventRegistryIdentityDigest(registry); digest != m2EventRegistryIdentitySHA256 {
		return registry, fmt.Errorf("contract=C-M2-EVENT-REGISTRY law=compatibility-identity-digest observed=%s expected=%s", digest, m2EventRegistryIdentitySHA256)
	}
	return registry, nil
}

func m2EventRegistryIdentityDigest(registry m2EventRegistry) string {
	registry.Ordering = slices.Clone(registry.Ordering)
	registry.Families = slices.Clone(registry.Families)
	slices.SortFunc(registry.Families, func(left, right m2EventFamily) int {
		return strings.Compare(left.Family, right.Family)
	})
	return m2DigestJSON(registry)
}

func m2ExpectedEventFamilies() []m2EventFamily {
	return []m2EventFamily{
		{Family: "runtime_host", EventSchema: 1, Reducer: 1, Owner: "internal/productionruntime"},
		{Family: "operation", EventSchema: 2, Reducer: 2, Owner: "internal/controljournal/application"},
		{Family: "mission", EventSchema: 1, Reducer: 1, Owner: "internal/mission"},
		{Family: "artifact_catalog", EventSchema: 1, Reducer: 1, Owner: "internal/artifact"},
		{Family: "artifact_revision", EventSchema: 1, Reducer: 1, Owner: "internal/artifact"},
		{Family: "workgraph", EventSchema: 1, Reducer: 1, Owner: "internal/workgraph"},
		{Family: "execution_slot", EventSchema: 1, Reducer: 1, Owner: "internal/execution"},
		{Family: "workspace", EventSchema: 1, Reducer: 1, Owner: "internal/workspace"},
		{Family: "handoff_slot", EventSchema: 1, Reducer: 1, Owner: "internal/handoff"},
		{Family: "review_slot", EventSchema: 1, Reducer: 1, Owner: "internal/review"},
		{Family: "acceptance", EventSchema: 1, Reducer: 1, Owner: "internal/integration"},
		{Family: "integration_target", EventSchema: 1, Reducer: 1, Owner: "internal/integration"},
		{Family: "integration_attempt", EventSchema: 1, Reducer: 1, Owner: "internal/integration"},
		{Family: "external_effect", EventSchema: 1, Reducer: 1, Owner: "internal/execution"},
		{Family: "outbox", EventSchema: 1, Reducer: 1, Owner: "internal/controlstore"},
	}
}

func validateM2Adoption(payload []byte) (m2Adoption, error) {
	var record m2Adoption
	if err := decodeM2Strict(payload, &record); err != nil {
		return record, fmt.Errorf("contract=C-M2-M1-ENTRY law=closed-json: %w", err)
	}
	want := m2Adoption{
		SchemaVersion: 1, ProgramID: "production-runtime", MissionID: "M1", Decision: "adopt",
		EvidenceManifestSHA: m2ManifestSHA, AdmittedTupleSHA: m2TupleSHA, SourceCommit: m2SourceCommit,
		SourceTree: m2SourceTree, SidecarSHA: m2SidecarSHA, Signer: m2Signer,
	}
	if record.SchemaVersion != want.SchemaVersion || record.ProgramID != want.ProgramID || record.MissionID != want.MissionID ||
		record.Decision != want.Decision || record.EvidenceManifestSHA != want.EvidenceManifestSHA ||
		record.AdmittedTupleSHA != want.AdmittedTupleSHA || record.SourceCommit != want.SourceCommit ||
		record.SourceTree != want.SourceTree || record.SidecarSHA != want.SidecarSHA || record.Signer != want.Signer {
		return record, fmt.Errorf("contract=C-M2-M1-ENTRY law=exact-coordinate observed=%+v expected=%+v", record, want)
	}
	if record.Rationale == "" || record.Signature.Kind != "approved-local-reference" || record.Signature.Value == "" {
		return record, fmt.Errorf("contract=C-M2-M1-ENTRY law=signed-decision observed=%+v", record.Signature)
	}
	if _, err := time.Parse(time.RFC3339, record.SignedAt); err != nil {
		return record, fmt.Errorf("contract=C-M2-M1-ENTRY law=signed-at observed=%q: %w", record.SignedAt, err)
	}
	return record, nil
}

func validateM2Closeout(record m2Adoption, closeout string) error {
	coordinates := map[string]string{
		"source commit": record.SourceCommit, "source tree": record.SourceTree,
		"manifest": record.EvidenceManifestSHA, "tuple": record.AdmittedTupleSHA,
		"sidecar": record.SidecarSHA, "signer": record.Signer,
		"signature kind": record.Signature.Kind, "signature value": record.Signature.Value,
		"release tag": m2ReleaseTag, "archive": m2ArchiveName, "archive sha": m2ArchiveSHA,
	}
	for label, coordinate := range coordinates {
		if !strings.Contains(closeout, coordinate) {
			return fmt.Errorf("contract=C-M2-M1-ENTRY coordinate=%s observed=missing expected=%s", label, coordinate)
		}
	}
	if strings.Contains(closeout, "/releases/tag/latest") || strings.Contains(closeout, "/releases/latest") {
		return fmt.Errorf("contract=C-M2-M1-ENTRY coordinate=release-tag observed=mutable-alias expected=%s", m2ReleaseTag)
	}
	return nil
}

func validateM2AdmissionCrosscheck(record m2Adoption, payload []byte) error {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("contract=C-M2-M1-ENTRY coordinate=machine-admission observed=%v", err)
	}
	gapdbTuple, ok := document["gapdb_tuple"].(map[string]any)
	if !ok {
		return fmt.Errorf("contract=C-M2-M1-ENTRY coordinate=gapdb-tuple observed=missing")
	}
	sidecar, ok := gapdbTuple["sidecar"].(map[string]any)
	if !ok || sidecar["sha256"] != record.SidecarSHA {
		return fmt.Errorf("contract=C-M2-M1-ENTRY coordinate=sidecar observed=%v expected=%s", sidecar["sha256"], record.SidecarSHA)
	}
	human, ok := document["human_adoption"].(map[string]any)
	if !ok || human["accountable_signer"] != record.Signer {
		return fmt.Errorf("contract=C-M2-M1-ENTRY coordinate=signer observed=%v expected=%s", human["accountable_signer"], record.Signer)
	}
	return nil
}

func decodeM2Strict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) == nil {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

func m2QualificationFixture() map[string]any {
	hash := strings.Repeat("c", 64)
	return map[string]any{
		"schema_version": 1, "program_id": "production-runtime", "mission_id": "M2", "run_id": "run-1",
		"source":       map[string]any{"commit": strings.Repeat("a", 40), "tree": strings.Repeat("b", 40), "go_version": "go1.26.7", "go_mod_sha256": hash, "go_sum_sha256": hash},
		"m1_admission": map[string]any{"adoption_sha256": hash, "evidence_manifest_sha256": hash, "admitted_tuple_sha256": hash, "sidecar_sha256": hash},
		"census":       map[string]any{"authority_families_total": 15, "authority_families_passed": 15, "behaviors_total": 1, "behaviors_passed": 1, "fault_cuts_total": 1, "fault_cuts_passed": 1, "mutants_total": 1, "mutants_killed": 1, "sqlite_edges": 0},
		"performance":  map[string]any{"recovery_operations": 10000, "recovery_runs": 30, "recovery_p95_ms": 1999.0, "query_observations": 10000, "query_p95_ms": 1.9, "mutation_observations": 10000, "mutation_p95_ms": 7.9},
		"artifacts":    []any{map[string]any{"path": "evidence/result.json", "sha256": hash, "kind": "campaign.result"}}, "verdict": "pass",
	}
}

func m2ContractsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(m2RepositoryRoot(t), "kitty-specs", m2MissionDir, "contracts")
}

func m2RepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("M2 architecture test source path unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func readM2File(t *testing.T, relative string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(m2RepositoryRoot(t), relative))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func cloneM2Map(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(payload, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
