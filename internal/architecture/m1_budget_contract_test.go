package architecture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

const (
	m1AdmittedRevision = "ff3dd478f87c6b4cbb1dcc168562f99879ba8c25"
	m1AdmittedModule   = "v0.0.0-20260824033134-ff3dd478f87c"
)

// byteLimit and countLimit deliberately prevent storage sizes from being
// compared with event, mutation, client, or evidence census counts.
type byteLimit int64
type countLimit int64

type m1BudgetFixture struct {
	SchemaVersion       int             `json:"schema_version"`
	PolicyID            string          `json:"policy_id"`
	AdmittedTupleSHA256 string          `json:"admitted_tuple_sha256"`
	AdmittedGapDB       m1AdmittedGapDB `json:"admitted_gapdb"`
	Limits              m1ProductLimits `json:"limits"`
	CheckpointDue       m1CheckpointDue `json:"checkpoint_due"`
	Restore             m1RestoreLimits `json:"restore"`
}

type m1AdmittedGapDB struct {
	SourceRevision         string `json:"source_revision"`
	SourceTree             string `json:"source_tree"`
	ModuleVersion          string `json:"module_version"`
	SidecarSHA256          string `json:"sidecar_sha256"`
	WireVersion            int    `json:"wire_version"`
	WireReferenceSHA256    string `json:"wire_reference_sha256"`
	StorageVersion         int    `json:"storage_version"`
	StorageReferenceSHA256 string `json:"storage_reference_sha256"`
}

type m1ProductLimits struct {
	KeyBytes           byteLimit  `json:"key_bytes"`
	OrdinaryValueBytes byteLimit  `json:"ordinary_value_bytes"`
	CheckpointBytes    byteLimit  `json:"checkpoint_value_bytes"`
	EventsPerCommand   countLimit `json:"events_per_command"`
	MutationsPerBatch  countLimit `json:"mutations_per_batch"`
	RawBatchBytes      byteLimit  `json:"raw_batch_bytes"`
	WireFrameBytes     byteLimit  `json:"wire_frame_bytes"`
	ScanRecordsPerPage countLimit `json:"scan_records_per_page"`
	ScanBytesPerPage   byteLimit  `json:"scan_bytes_per_page"`
	ConcurrentClients  countLimit `json:"concurrent_clients"`
}

type m1CheckpointDue struct {
	AcceptedCommands    countLimit `json:"accepted_commands"`
	Events              countLimit `json:"events"`
	CanonicalEventBytes byteLimit  `json:"canonical_event_bytes"`
}

type m1RestoreLimits struct {
	Events                countLimit `json:"events"`
	CanonicalEventBytes   byteLimit  `json:"canonical_event_bytes"`
	ScanPages             countLimit `json:"scan_pages"`
	StaleSnapshotRestarts countLimit `json:"stale_snapshot_restarts"`
}

// m1RuntimeCeilings is revision-pinned comparison data from plan.md section 6.
// Events per command is compared with GapDB's mutation capacity because the
// plan explicitly describes it as constrained by the 1,024-mutation batch.
type m1RuntimeCeilings struct {
	SourceRevision     string
	ModuleVersion      string
	KeyBytes           byteLimit
	ValueBytes         byteLimit
	EventsPerCommand   countLimit
	MutationsPerBatch  countLimit
	RawBatchBytes      byteLimit
	WireFrameBytes     byteLimit
	ScanRecordsPerPage countLimit
	ScanBytesPerPage   byteLimit
	ConcurrentClients  countLimit
}

var ratifiedM1Budget = m1BudgetFixture{
	SchemaVersion:       1,
	PolicyID:            "m1-product-budgets/v1",
	AdmittedTupleSHA256: "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d",
	AdmittedGapDB: m1AdmittedGapDB{
		SourceRevision:         m1AdmittedRevision,
		SourceTree:             "64ada3cd6328aca73ef902e4ab9fe0405823841c",
		ModuleVersion:          m1AdmittedModule,
		SidecarSHA256:          "62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e",
		WireVersion:            1,
		WireReferenceSHA256:    "cd08e19252628a1cbfe5bd43018caae86569e041dc01f9d4472dc2eae72389c2",
		StorageVersion:         1,
		StorageReferenceSHA256: "6d4cca9611e9e272e02d6e566dccc887dacf738c1da8c5d580237f731018295c",
	},
	Limits: m1ProductLimits{
		KeyBytes: 1024, OrdinaryValueBytes: 262144, CheckpointBytes: 4194304,
		EventsPerCommand: 128, MutationsPerBatch: 256, RawBatchBytes: 6291456,
		WireFrameBytes: 12582912, ScanRecordsPerPage: 500,
		ScanBytesPerPage: 8388608, ConcurrentClients: 32,
	},
	CheckpointDue: m1CheckpointDue{AcceptedCommands: 128, Events: 512, CanonicalEventBytes: 4194304},
	Restore:       m1RestoreLimits{Events: 16000, CanonicalEventBytes: 50331648, ScanPages: 32, StaleSnapshotRestarts: 2},
}

var admittedM1RuntimeCeilings = m1RuntimeCeilings{
	SourceRevision: m1AdmittedRevision, ModuleVersion: m1AdmittedModule,
	KeyBytes: 4096, ValueBytes: 8388608, EventsPerCommand: 1024,
	MutationsPerBatch: 1024, RawBatchBytes: 16777216,
	WireFrameBytes: 16777216, ScanRecordsPerPage: 1000,
	ScanBytesPerPage: 16777216, ConcurrentClients: 256,
}

func TestM1BudgetFrozenContract(t *testing.T) {
	payload, err := os.ReadFile(m1ContractsDir(t) + "/m1-product-budgets-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	budget, err := decodeM1Budget(payload)
	if err != nil {
		t.Fatalf("positive contract fixture rejected: %v", err)
	}
	if err := qualifyM1BudgetForPublication(budget, admittedM1RuntimeCeilings); err != nil {
		t.Fatalf("positive contract fixture rejected: %v", err)
	}
}

func TestM1BudgetRejectsEveryWidenedBound(t *testing.T) {
	tests := []struct {
		field string
		widen func(*m1BudgetFixture)
	}{
		{"limits.key_bytes", func(v *m1BudgetFixture) { v.Limits.KeyBytes++ }},
		{"limits.ordinary_value_bytes", func(v *m1BudgetFixture) { v.Limits.OrdinaryValueBytes++ }},
		{"limits.checkpoint_value_bytes", func(v *m1BudgetFixture) { v.Limits.CheckpointBytes++ }},
		{"limits.events_per_command", func(v *m1BudgetFixture) { v.Limits.EventsPerCommand++ }},
		{"limits.mutations_per_batch", func(v *m1BudgetFixture) { v.Limits.MutationsPerBatch++ }},
		{"limits.raw_batch_bytes", func(v *m1BudgetFixture) { v.Limits.RawBatchBytes++ }},
		{"limits.wire_frame_bytes", func(v *m1BudgetFixture) { v.Limits.WireFrameBytes++ }},
		{"limits.scan_records_per_page", func(v *m1BudgetFixture) { v.Limits.ScanRecordsPerPage++ }},
		{"limits.scan_bytes_per_page", func(v *m1BudgetFixture) { v.Limits.ScanBytesPerPage++ }},
		{"limits.concurrent_clients", func(v *m1BudgetFixture) { v.Limits.ConcurrentClients++ }},
		{"checkpoint_due.accepted_commands", func(v *m1BudgetFixture) { v.CheckpointDue.AcceptedCommands++ }},
		{"checkpoint_due.events", func(v *m1BudgetFixture) { v.CheckpointDue.Events++ }},
		{"checkpoint_due.canonical_event_bytes", func(v *m1BudgetFixture) { v.CheckpointDue.CanonicalEventBytes++ }},
		{"restore.events", func(v *m1BudgetFixture) { v.Restore.Events++ }},
		{"restore.canonical_event_bytes", func(v *m1BudgetFixture) { v.Restore.CanonicalEventBytes++ }},
		{"restore.scan_pages", func(v *m1BudgetFixture) { v.Restore.ScanPages++ }},
		{"restore.stale_snapshot_restarts", func(v *m1BudgetFixture) { v.Restore.StaleSnapshotRestarts++ }},
	}
	for _, test := range tests {
		t.Run(test.field, func(t *testing.T) {
			mutant := ratifiedM1Budget
			test.widen(&mutant)
			err := qualifyM1BudgetForPublication(mutant, admittedM1RuntimeCeilings)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("widened %s was not rejected with a field-specific reason: %v", test.field, err)
			}
		})
	}
}

func TestM1BudgetDecodeFailsClosed(t *testing.T) {
	valid, err := json.Marshal(ratifiedM1Budget)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(valid, &document); err != nil {
		t.Fatal(err)
	}

	unknown := cloneMap(t, document)
	unknown["environment_secret"] = "must-not-appear"
	unknownPayload, _ := json.Marshal(unknown)
	if _, err := decodeM1Budget(unknownPayload); err == nil || strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("unknown field did not fail closed with safe diagnostics: %v", err)
	}

	omitted := cloneMap(t, document)
	delete(object(omitted, "limits"), "key_bytes")
	omittedPayload, _ := json.Marshal(omitted)
	if _, err := decodeM1Budget(omittedPayload); err == nil || !strings.Contains(err.Error(), "limits.key_bytes") {
		t.Fatalf("omitted field did not fail closed with a field-specific reason: %v", err)
	}
}

func TestM1BudgetRuntimeComparisonFailsClosed(t *testing.T) {
	t.Run("absent provenance", func(t *testing.T) {
		ceilings := admittedM1RuntimeCeilings
		ceilings.SourceRevision = ""
		if err := qualifyM1BudgetForPublication(ratifiedM1Budget, ceilings); err == nil || !strings.Contains(err.Error(), "comparison.source_revision") {
			t.Fatalf("absent comparison provenance was not rejected: %v", err)
		}
	})
	t.Run("stale provenance", func(t *testing.T) {
		ceilings := admittedM1RuntimeCeilings
		ceilings.SourceRevision = strings.Repeat("0", 40)
		if err := qualifyM1BudgetForPublication(ratifiedM1Budget, ceilings); err == nil || !strings.Contains(err.Error(), "comparison.source_revision") {
			t.Fatalf("stale comparison provenance was not rejected: %v", err)
		}
	})
	t.Run("equal ceiling", func(t *testing.T) {
		ceilings := admittedM1RuntimeCeilings
		ceilings.WireFrameBytes = ratifiedM1Budget.Limits.WireFrameBytes
		if err := qualifyM1BudgetForPublication(ratifiedM1Budget, ceilings); err == nil || !strings.Contains(err.Error(), "limits.wire_frame_bytes") {
			t.Fatalf("non-strict product/runtime boundary was not rejected: %v", err)
		}
	})
}

func TestM1BudgetMutantControls(t *testing.T) {
	tests := []struct {
		id     string
		mutate func(*m1BudgetFixture, *m1RuntimeCeilings)
	}{
		{"budget-widening", func(b *m1BudgetFixture, _ *m1RuntimeCeilings) { b.Limits.KeyBytes++ }},
		{"runtime-coordinate-conflation", func(b *m1BudgetFixture, _ *m1RuntimeCeilings) {
			b.CheckpointDue.Events = countLimit(b.Limits.RawBatchBytes)
		}},
		{"stale-limit-provenance", func(_ *m1BudgetFixture, c *m1RuntimeCeilings) { c.SourceRevision = strings.Repeat("0", 40) }},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			cleanBudget, cleanCeilings := ratifiedM1Budget, admittedM1RuntimeCeilings
			if err := qualifyM1BudgetForPublication(cleanBudget, cleanCeilings); err != nil {
				t.Fatalf("clean control rejected: %v", err)
			}
			test.mutate(&cleanBudget, &cleanCeilings)
			if err := qualifyM1BudgetForPublication(cleanBudget, cleanCeilings); err == nil {
				t.Fatal("mutant survived its budget guard")
			}
		})
	}
}

func decodeM1Budget(payload []byte) (m1BudgetFixture, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var budget m1BudgetFixture
	if err := decoder.Decode(&budget); err != nil {
		return m1BudgetFixture{}, fmt.Errorf("m1 product budget contract: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return m1BudgetFixture{}, fmt.Errorf("m1 product budget contract: trailing document")
	}
	if err := validateRatifiedM1Budget(budget); err != nil {
		return m1BudgetFixture{}, err
	}
	return budget, nil
}

func validateRatifiedM1Budget(got m1BudgetFixture) error {
	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"schema_version", got.SchemaVersion, ratifiedM1Budget.SchemaVersion},
		{"policy_id", got.PolicyID, ratifiedM1Budget.PolicyID},
		{"admitted_tuple_sha256", got.AdmittedTupleSHA256, ratifiedM1Budget.AdmittedTupleSHA256},
		{"admitted_gapdb.source_revision", got.AdmittedGapDB.SourceRevision, ratifiedM1Budget.AdmittedGapDB.SourceRevision},
		{"admitted_gapdb.source_tree", got.AdmittedGapDB.SourceTree, ratifiedM1Budget.AdmittedGapDB.SourceTree},
		{"admitted_gapdb.module_version", got.AdmittedGapDB.ModuleVersion, ratifiedM1Budget.AdmittedGapDB.ModuleVersion},
		{"admitted_gapdb.sidecar_sha256", got.AdmittedGapDB.SidecarSHA256, ratifiedM1Budget.AdmittedGapDB.SidecarSHA256},
		{"admitted_gapdb.wire_version", got.AdmittedGapDB.WireVersion, ratifiedM1Budget.AdmittedGapDB.WireVersion},
		{"admitted_gapdb.wire_reference_sha256", got.AdmittedGapDB.WireReferenceSHA256, ratifiedM1Budget.AdmittedGapDB.WireReferenceSHA256},
		{"admitted_gapdb.storage_version", got.AdmittedGapDB.StorageVersion, ratifiedM1Budget.AdmittedGapDB.StorageVersion},
		{"admitted_gapdb.storage_reference_sha256", got.AdmittedGapDB.StorageReferenceSHA256, ratifiedM1Budget.AdmittedGapDB.StorageReferenceSHA256},
		{"limits.key_bytes", got.Limits.KeyBytes, ratifiedM1Budget.Limits.KeyBytes},
		{"limits.ordinary_value_bytes", got.Limits.OrdinaryValueBytes, ratifiedM1Budget.Limits.OrdinaryValueBytes},
		{"limits.checkpoint_value_bytes", got.Limits.CheckpointBytes, ratifiedM1Budget.Limits.CheckpointBytes},
		{"limits.events_per_command", got.Limits.EventsPerCommand, ratifiedM1Budget.Limits.EventsPerCommand},
		{"limits.mutations_per_batch", got.Limits.MutationsPerBatch, ratifiedM1Budget.Limits.MutationsPerBatch},
		{"limits.raw_batch_bytes", got.Limits.RawBatchBytes, ratifiedM1Budget.Limits.RawBatchBytes},
		{"limits.wire_frame_bytes", got.Limits.WireFrameBytes, ratifiedM1Budget.Limits.WireFrameBytes},
		{"limits.scan_records_per_page", got.Limits.ScanRecordsPerPage, ratifiedM1Budget.Limits.ScanRecordsPerPage},
		{"limits.scan_bytes_per_page", got.Limits.ScanBytesPerPage, ratifiedM1Budget.Limits.ScanBytesPerPage},
		{"limits.concurrent_clients", got.Limits.ConcurrentClients, ratifiedM1Budget.Limits.ConcurrentClients},
		{"checkpoint_due.accepted_commands", got.CheckpointDue.AcceptedCommands, ratifiedM1Budget.CheckpointDue.AcceptedCommands},
		{"checkpoint_due.events", got.CheckpointDue.Events, ratifiedM1Budget.CheckpointDue.Events},
		{"checkpoint_due.canonical_event_bytes", got.CheckpointDue.CanonicalEventBytes, ratifiedM1Budget.CheckpointDue.CanonicalEventBytes},
		{"restore.events", got.Restore.Events, ratifiedM1Budget.Restore.Events},
		{"restore.canonical_event_bytes", got.Restore.CanonicalEventBytes, ratifiedM1Budget.Restore.CanonicalEventBytes},
		{"restore.scan_pages", got.Restore.ScanPages, ratifiedM1Budget.Restore.ScanPages},
		{"restore.stale_snapshot_restarts", got.Restore.StaleSnapshotRestarts, ratifiedM1Budget.Restore.StaleSnapshotRestarts},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("%s differs from the ratified contract", check.field)
		}
	}
	return nil
}

func qualifyM1BudgetForPublication(budget m1BudgetFixture, runtime m1RuntimeCeilings) error {
	if err := validateRatifiedM1Budget(budget); err != nil {
		return err
	}
	if runtime.SourceRevision == "" || runtime.SourceRevision != budget.AdmittedGapDB.SourceRevision {
		return fmt.Errorf("comparison.source_revision is absent or stale")
	}
	if runtime.ModuleVersion == "" || runtime.ModuleVersion != budget.AdmittedGapDB.ModuleVersion {
		return fmt.Errorf("comparison.module_version is absent or stale")
	}
	strict := func(field string, product, ceiling int64) error {
		if product >= ceiling {
			return fmt.Errorf("%s must remain strictly below its admitted runtime ceiling", field)
		}
		return nil
	}
	comparisons := []struct {
		field            string
		product, ceiling int64
	}{
		{"limits.key_bytes", int64(budget.Limits.KeyBytes), int64(runtime.KeyBytes)},
		{"limits.ordinary_value_bytes", int64(budget.Limits.OrdinaryValueBytes), int64(runtime.ValueBytes)},
		{"limits.checkpoint_value_bytes", int64(budget.Limits.CheckpointBytes), int64(runtime.ValueBytes)},
		{"limits.events_per_command", int64(budget.Limits.EventsPerCommand), int64(runtime.EventsPerCommand)},
		{"limits.mutations_per_batch", int64(budget.Limits.MutationsPerBatch), int64(runtime.MutationsPerBatch)},
		{"limits.raw_batch_bytes", int64(budget.Limits.RawBatchBytes), int64(runtime.RawBatchBytes)},
		{"limits.wire_frame_bytes", int64(budget.Limits.WireFrameBytes), int64(runtime.WireFrameBytes)},
		{"limits.scan_records_per_page", int64(budget.Limits.ScanRecordsPerPage), int64(runtime.ScanRecordsPerPage)},
		{"limits.scan_bytes_per_page", int64(budget.Limits.ScanBytesPerPage), int64(runtime.ScanBytesPerPage)},
		{"limits.concurrent_clients", int64(budget.Limits.ConcurrentClients), int64(runtime.ConcurrentClients)},
	}
	for _, comparison := range comparisons {
		if err := strict(comparison.field, comparison.product, comparison.ceiling); err != nil {
			return err
		}
	}
	return nil
}
