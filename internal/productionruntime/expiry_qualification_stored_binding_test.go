//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

// These are component/memory-port tests of the actual ensureAuthorityBinding
// helper, not encoder-only tests or native constructor/restart qualification.
// The helper returns only an error: no public Host or proof is constructed or
// obtained here. Refusals below establish that binding prerequisite only; they
// do not substitute for public-root refusal or genuine native ACK evidence.
type expiryStoredBindingPort struct {
	*memoryAuthorityPort
	readKeys     []string
	readErrors   map[int]error
	mutations    []expiryStoredBindingMutation
	mutateErr    error
	ackOverride  *storegapdb.MutationResult
	epochCalls   int
	inspectCalls int
}

type expiryStoredBindingMutation struct {
	operation string
	mutation  storegapdb.Mutation
	ack       storegapdb.AckMode
}

var _ authorityPort = (*expiryStoredBindingPort)(nil)

func (p *expiryStoredBindingPort) Get(ctx context.Context, key string) (storegapdb.Record, error) {
	p.readKeys = append(p.readKeys, key)
	if err := p.readErrors[len(p.readKeys)]; err != nil {
		return storegapdb.Record{}, err
	}
	return p.memoryAuthorityPort.Get(ctx, key)
}

func (p *expiryStoredBindingPort) ConditionalMutate(ctx context.Context, operation string, mutation storegapdb.Mutation, ack storegapdb.AckMode) (storegapdb.MutationResult, error) {
	mutation.Value = bytes.Clone(mutation.Value)
	p.mutations = append(p.mutations, expiryStoredBindingMutation{operation, mutation, ack})
	if p.mutateErr != nil {
		return storegapdb.MutationResult{}, p.mutateErr
	}
	if p.ackOverride != nil {
		// Deliberately scripted ACK refusal, with no memory write. This does not
		// claim rollback if a real provider writes before returning a bad ACK.
		return *p.ackOverride, nil
	}
	return p.memoryAuthorityPort.ConditionalMutate(ctx, operation, mutation, ack)
}

func (p *expiryStoredBindingPort) AdmitRuntimeEpoch(context.Context, storegapdb.RuntimeEpochBatch) (storegapdb.MutationResult, error) {
	p.epochCalls++
	return storegapdb.MutationResult{}, errors.New("unexpected epoch fallback")
}

func (p *expiryStoredBindingPort) Inspect(context.Context) (storegapdb.Inspection, error) {
	p.inspectCalls++
	return storegapdb.Inspection{}, errors.New("unexpected inspection fallback")
}

type expiryStoredBindingSnapshot struct {
	revision uint64
	records  map[string]storegapdb.Record
}

func expiryStoredBindingSnapshotOf(p *memoryAuthorityPort) expiryStoredBindingSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := expiryStoredBindingSnapshot{p.revision, make(map[string]storegapdb.Record, len(p.records))}
	for key, record := range p.records {
		record.Value = bytes.Clone(record.Value)
		snapshot.records[key] = record
	}
	return snapshot
}

func expiryStoredBindingUnchanged(t *testing.T, p *expiryStoredBindingPort, before expiryStoredBindingSnapshot) {
	t.Helper()
	if after := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort); !reflect.DeepEqual(after, before) {
		t.Fatalf("binding bytes/revisions or other memory records changed: before=%+v after=%+v", before, after)
	}
}

func expiryStoredBindingError(t *testing.T, err error, kind ErrorKind, field string) {
	t.Helper()
	got, ok := err.(*Error)
	want := Error{Kind: kind, State: HostRecovering, Field: field}
	if !ok || got == nil || *got != want {
		t.Fatalf("error = %#v; want exact %+v", err, want)
	}
}

func expiryStoredBindingIO(t *testing.T, p *expiryStoredBindingPort, reads, mutations int) {
	t.Helper()
	if len(p.readKeys) != reads || len(p.mutations) != mutations || p.epochCalls != 0 || p.inspectCalls != 0 {
		t.Fatalf("I/O: reads=%v mutations=%d epoch=%d inspect=%d; want %d/%d/0/0 (no retry/fallback)", p.readKeys, len(p.mutations), p.epochCalls, p.inspectCalls, reads, mutations)
	}
	for _, key := range p.readKeys {
		if key != authorityBindingKey {
			t.Fatalf("read selected another key: %q", key)
		}
	}
}

func expiryStoredBindingCreateRequest(t *testing.T, p *expiryStoredBindingPort, binding AuthorityBindingV2, canonical []byte) {
	t.Helper()
	if len(p.mutations) != 1 {
		t.Fatalf("create attempts = %d", len(p.mutations))
	}
	want := expiryStoredBindingMutation{binding.CreatedOperationID, storegapdb.Mutation{
		Kind: storegapdb.MutationPut, Key: authorityBindingKey,
		Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: canonical,
	}, storegapdb.AckDurable}
	if !reflect.DeepEqual(p.mutations[0], want) {
		t.Fatalf("create request = %+v; want %+v", p.mutations[0], want)
	}
}

func TestExpiryStoredBindingRejectsRowsAtActualHelper(t *testing.T) {
	binding := expiryTestBinding()
	canonical, err := binding.canonical()
	if err != nil {
		t.Fatal(err)
	}
	replace := func(old, next string) []byte {
		t.Helper()
		if strings.Count(string(canonical), old) != 1 || old == next {
			t.Fatalf("bad fixed row transformation %q -> %q", old, next)
		}
		return []byte(strings.Replace(string(canonical), old, next, 1))
	}
	rows := []struct {
		name     string
		value    []byte
		revision uint64
	}{
		{"unknown", append([]byte(`{"unknown":false,`), canonical[1:]...), 7},
		{"null_row", []byte(`null`), 7},
		{"null_member", replace(`"database_id":"database-expiry"`, `"database_id":null`), 7},
		{"duplicate_equal", append([]byte(`{"schema_version":1,`), canonical[1:]...), 7},
		{"duplicate_conflicting", append([]byte(`{"schema_version":2,`), canonical[1:]...), 7},
		{"noncanonical_space", append([]byte(" "), canonical...), 7},
		{"noncanonical_order", append([]byte(`{"schema_version":1,`), bytes.Replace(canonical[1:], []byte(`"schema_version":1,`), nil, 1)...), 7},
		{"float_schema", replace(`"schema_version":1`, `"schema_version":1.0`), 7},
		{"exponent_wire", replace(`"wire_protocol":1`, `"wire_protocol":1e0`), 7},
		{"schema2", replace(`"schema_version":1`, `"schema_version":2`), 7},
		{"mixed_legacy_tuple", append([]byte(`{"admitted_tuple_sha256":"`+expiryTupleSHA+`",`), canonical[1:]...), 7},
		{"mixed_legacy_image", append([]byte(`{"sidecar_sha256":"`+expiryExecutableSHA+`",`), canonical[1:]...), 7},
		{"wrong_input_pin", replace(expiryInputSHA, strings.Repeat("a", 64)), 7},
		{"wrong_tuple_pin", replace(expiryTupleSHA, strings.Repeat("b", 64)), 7},
		{"wrong_inventory_pin", replace(expiryInventorySHA, strings.Repeat("c", 64)), 7},
		{"wrong_image_pin", replace(expiryExecutableSHA, strings.Repeat("d", 64)), 7},
		{"wrong_recovery", replace(expiryRecoveryContract, "optional-reader"), 7},
		{"wrong_wire", replace(`"wire_protocol":1`, `"wire_protocol":2`), 7},
		{"wrong_storage", replace(`"storage_protocol":1`, `"storage_protocol":2`), 7},
		{"wrong_host", replace("host-expiry", "host-other"), 7},
		{"wrong_database", replace("database-expiry", "database-other"), 7},
		{"wrong_operation", replace("operation-expiry-binding", "operation-other"), 7},
		{"wrong_kind", replace("expiry-host-qualification-authority-binding", "ordinary"), 7},
		{"adopted", replace(`"adopted":false`, `"adopted":true`), 7},
		{"production_admission", replace(`"production_admission":false`, `"production_admission":true`), 7},
		{"release_ready", replace(`"release_ready":false`, `"release_ready":true`), 7},
		{"trailing_value", append(bytes.Clone(canonical), []byte(`{}`)...), 7},
		{"malformed", canonical[:len(canonical)-1], 7},
		{"zero_revision", canonical, 0},
	}
	for _, row := range rows {
		for _, initialize := range []bool{false, true} {
			mode := "existing_only"
			if initialize {
				mode = "initialize_must_not_overwrite"
			}
			t.Run(row.name+"/"+mode, func(t *testing.T) {
				p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort()}
				p.revision = 11
				p.records[authorityBindingKey] = storegapdb.Record{Key: authorityBindingKey, Value: bytes.Clone(row.value), Revision: row.revision}
				before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
				err := ensureAuthorityBinding(t.Context(), p, binding, initialize)
				expiryStoredBindingError(t, err, ErrorStartup, "authority_binding_mismatch")
				expiryStoredBindingIO(t, p, 1, 0)
				expiryStoredBindingUnchanged(t, p, before)
			})
		}
	}
}

func TestExpiryStoredBindingInvalidPrerequisitesReachHelperBeforeIO(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*AuthorityBindingV2)
	}{
		{"partial_input", func(b *AuthorityBindingV2) { b.qualification.inputRecordSHA256 = "" }},
		{"partial_tuple", func(b *AuthorityBindingV2) { b.qualification.technicalTupleSHA256 = "" }},
		{"partial_inventory", func(b *AuthorityBindingV2) { b.qualification.modifiedInventorySHA256 = "" }},
		{"partial_recovery", func(b *AuthorityBindingV2) { b.qualification.recoveryContract = "" }},
		{"wrong_input", func(b *AuthorityBindingV2) { b.qualification.inputRecordSHA256 = expiryTupleSHA }},
		{"wrong_tuple", func(b *AuthorityBindingV2) { b.qualification.technicalTupleSHA256 = expiryInputSHA }},
		{"wrong_inventory", func(b *AuthorityBindingV2) { b.qualification.modifiedInventorySHA256 = expiryInputSHA }},
		{"wrong_recovery", func(b *AuthorityBindingV2) { b.qualification.recoveryContract = "optional" }},
		{"schema2_marked", func(b *AuthorityBindingV2) { b.SchemaVersion = 2 }},
		{"mixed_legacy", func(b *AuthorityBindingV2) { b.AdmittedTupleSHA = expiryTupleSHA }},
		{"wrong_image", func(b *AuthorityBindingV2) { b.SidecarSHA = strings.Repeat("a", 64) }},
		{"wrong_wire", func(b *AuthorityBindingV2) { b.WireProtocol = 2 }},
		{"wrong_storage", func(b *AuthorityBindingV2) { b.StorageProtocol = 0 }},
		{"host_empty", func(b *AuthorityBindingV2) { b.HostAuthorityID = "" }},
		{"host_long", func(b *AuthorityBindingV2) { b.HostAuthorityID = journalv2.HostAuthorityID(strings.Repeat("H", 129)) }},
		{"host_escape", func(b *AuthorityBindingV2) { b.HostAuthorityID = "host\"escape" }},
		{"database_empty", func(b *AuthorityBindingV2) { b.DatabaseID = "" }},
		{"database_long", func(b *AuthorityBindingV2) { b.DatabaseID = journalv2.DatabaseID(strings.Repeat("D", 129)) }},
		{"database_escape", func(b *AuthorityBindingV2) { b.DatabaseID = "db\\escape" }},
		{"operation_empty", func(b *AuthorityBindingV2) { b.CreatedOperationID = "" }},
		{"operation_long", func(b *AuthorityBindingV2) { b.CreatedOperationID = strings.Repeat("O", 129) }},
		{"operation_escape", func(b *AuthorityBindingV2) { b.CreatedOperationID = "op\nescape" }},
	}
	valid := expiryTestBinding()
	canonical, err := valid.canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		for _, initialize := range []bool{false, true} {
			mode := "existing_only"
			if initialize {
				mode = "initialize"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort()}
				p.revision = 9
				p.records[authorityBindingKey] = storegapdb.Record{Key: authorityBindingKey, Value: bytes.Clone(canonical), Revision: 9}
				before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
				binding := valid
				tc.mutate(&binding)
				err := ensureAuthorityBinding(t.Context(), p, binding, initialize)
				expiryStoredBindingError(t, err, ErrorStartup, "expiry_binding")
				expiryStoredBindingIO(t, p, 0, 0)
				expiryStoredBindingUnchanged(t, p, before)
			})
		}
	}
}

func TestExpiryStoredBindingCanonicalContentAndFreshMemoryACK(t *testing.T) {
	binding := expiryTestBinding()
	canonical, err := binding.canonical()
	if err != nil {
		t.Fatal(err)
	}
	copy := binding
	copy.HostAuthorityID = journalv2.HostAuthorityID(strings.Clone(string(binding.HostAuthorityID)))
	copy.DatabaseID = journalv2.DatabaseID(strings.Clone(string(binding.DatabaseID)))
	copy.CreatedOperationID = strings.Clone(binding.CreatedOperationID)
	copy.SidecarSHA = strings.Clone(binding.SidecarSHA)
	copy.qualification = authorityQualificationBinding{
		strings.Clone(binding.qualification.inputRecordSHA256), strings.Clone(binding.qualification.technicalTupleSHA256),
		strings.Clone(binding.qualification.modifiedInventorySHA256), strings.Clone(binding.qualification.recoveryContract),
	}
	if copy != binding {
		t.Fatal("distinct string storage changed comparable content")
	}
	for _, initialize := range []bool{false, true} {
		mode := "existing_only"
		if initialize {
			mode = "initialize_existing"
		}
		t.Run(mode, func(t *testing.T) {
			p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort()}
			p.revision = 13
			p.records[authorityBindingKey] = storegapdb.Record{Key: authorityBindingKey, Value: bytes.Clone(canonical), Revision: 7}
			before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
			if err := ensureAuthorityBinding(t.Context(), p, copy, initialize); err != nil {
				t.Fatal(err)
			}
			expiryStoredBindingIO(t, p, 1, 0)
			expiryStoredBindingUnchanged(t, p, before)
		})
	}
	t.Run("maximum_tokens_fresh_and_stored", func(t *testing.T) {
		maximum := binding
		maximum.HostAuthorityID = journalv2.HostAuthorityID(strings.Repeat("H", 128))
		maximum.DatabaseID = journalv2.DatabaseID(strings.Repeat("D", 128))
		maximum.CreatedOperationID = strings.Repeat("O", 128)
		value, err := maximum.canonical()
		if err != nil || len(value) != expiryBindingMaximum {
			t.Fatalf("maximum canonical length=%d error=%v", len(value), err)
		}
		p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort()}
		if err := ensureAuthorityBinding(t.Context(), p, maximum, true); err != nil {
			t.Fatal(err)
		}
		expiryStoredBindingIO(t, p, 1, 1)
		expiryStoredBindingCreateRequest(t, p, maximum, value)
		before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
		if record := before.records[authorityBindingKey]; before.revision != 1 || record.Revision != 1 || !bytes.Equal(record.Value, value) {
			t.Fatal("maximum binding not stored canonically at revision 1")
		}
		if err := ensureAuthorityBinding(t.Context(), p, maximum, false); err != nil {
			t.Fatal(err)
		}
		expiryStoredBindingIO(t, p, 2, 1)
		expiryStoredBindingUnchanged(t, p, before)
	})
	t.Run("fresh_real_memory_mutation_ack_then_existing_only", func(t *testing.T) {
		p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort()}
		if err := ensureAuthorityBinding(t.Context(), p, binding, true); err != nil {
			t.Fatal(err)
		}
		expiryStoredBindingIO(t, p, 1, 1)
		expiryStoredBindingCreateRequest(t, p, binding, canonical)
		record := p.records[authorityBindingKey]
		if p.revision != 1 || len(p.records) != 1 || record.Key != authorityBindingKey || record.Revision != 1 || !bytes.Equal(record.Value, canonical) {
			t.Fatalf("fresh schema1 binding not installed exactly once: revision=%d records=%+v", p.revision, p.records)
		}
		before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
		if err := ensureAuthorityBinding(t.Context(), p, copy, false); err != nil {
			t.Fatal(err)
		}
		expiryStoredBindingIO(t, p, 2, 1)
		expiryStoredBindingUnchanged(t, p, before)
	})
}

func TestExpiryStoredBindingMissingAndReadFailuresNeverRetry(t *testing.T) {
	cases := []struct {
		name       string
		initialize bool
		readErr    error
	}{
		{"missing_restart_initialize_false", false, nil},
		{"read_failure_existing_only", false, errors.New("read failed")},
		{"read_failure_initialize", true, errors.New("read failed")},
		{"canceled_read_initialize", true, context.Canceled},
		{"deadline_read_initialize", true, context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort(), readErrors: map[int]error{1: tc.readErr}}
			before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
			err := ensureAuthorityBinding(t.Context(), p, expiryTestBinding(), tc.initialize)
			expiryStoredBindingError(t, err, ErrorStartup, "authority_binding_missing")
			expiryStoredBindingIO(t, p, 1, 0)
			expiryStoredBindingUnchanged(t, p, before)
		})
	}
}

func TestExpiryStoredBindingCreateRaceFallbackAtActualHelper(t *testing.T) {
	binding := expiryTestBinding()
	canonical, err := binding.canonical()
	if err != nil {
		t.Fatal(err)
	}
	originalMutateErr := errors.New("original conditional mutation failure")
	cases := []struct {
		name      string
		value     []byte
		readErr   error
		mutateErr error
		accept    bool
	}{
		{"same_canonical_memory_conflict", bytes.Clone(canonical), nil, nil, true},
		{"same_canonical_original_mutate_error", bytes.Clone(canonical), nil, originalMutateErr, true},
		{"wrong_host", bytes.Replace(canonical, []byte("host-expiry"), []byte("host-other"), 1), nil, originalMutateErr, false},
		{"wrong_database", bytes.Replace(canonical, []byte("database-expiry"), []byte("database-other"), 1), nil, originalMutateErr, false},
		{"wrong_operation", bytes.Replace(canonical, []byte("operation-expiry-binding"), []byte("operation-other"), 1), nil, originalMutateErr, false},
		{"noncanonical", append([]byte(" "), canonical...), nil, originalMutateErr, false},
		{"fallback_read_failure", canonical, errors.New("fallback read failed"), originalMutateErr, false},
		{"fallback_still_missing", canonical, &storegapdb.Error{Kind: storegapdb.OutcomeNotFound}, originalMutateErr, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Deterministic interleaving: first read reports absence; the memory
			// row represents the contender already installed by mutation time.
			// No goroutine scheduling or native owner race is claimed.
			p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort(), mutateErr: tc.mutateErr,
				readErrors: map[int]error{1: &storegapdb.Error{Kind: storegapdb.OutcomeNotFound}, 2: tc.readErr}}
			p.revision = 17
			p.records[authorityBindingKey] = storegapdb.Record{Key: authorityBindingKey, Value: bytes.Clone(tc.value), Revision: 17}
			before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
			err := ensureAuthorityBinding(t.Context(), p, binding, true)
			if tc.accept {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				expiryStoredBindingError(t, err, ErrorOwnerConflict, "authority_binding_create")
			}
			expiryStoredBindingIO(t, p, 2, 1)
			expiryStoredBindingCreateRequest(t, p, binding, canonical)
			expiryStoredBindingUnchanged(t, p, before)
		})
	}
	// Intentionally no raced zero-revision assertion: unlike the initial-read
	// branch, the actual fallback compares bytes without checking Revision.
	// Whether an actual Adapter rejects zero first is outside this fixture.
}

func TestExpiryStoredBindingFreshRejectsScriptedInvalidACK(t *testing.T) {
	binding := expiryTestBinding()
	canonical, err := binding.canonical()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		result storegapdb.MutationResult
	}{
		{"zero_revision", storegapdb.MutationResult{Revision: 0, DurableThrough: 1, Ack: storegapdb.AckDurable, MutationCount: 1}},
		{"wrong_ack", storegapdb.MutationResult{Revision: 1, DurableThrough: 1, MutationCount: 1}},
		{"not_durable_through", storegapdb.MutationResult{Revision: 2, DurableThrough: 1, Ack: storegapdb.AckDurable, MutationCount: 1}},
		{"zero_mutations", storegapdb.MutationResult{Revision: 1, DurableThrough: 1, Ack: storegapdb.AckDurable, MutationCount: 0}},
		{"multiple_mutations", storegapdb.MutationResult{Revision: 1, DurableThrough: 1, Ack: storegapdb.AckDurable, MutationCount: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &expiryStoredBindingPort{memoryAuthorityPort: newMemoryAuthorityPort(), ackOverride: &tc.result}
			before := expiryStoredBindingSnapshotOf(p.memoryAuthorityPort)
			err := ensureAuthorityBinding(t.Context(), p, binding, true)
			expiryStoredBindingError(t, err, ErrorStartup, "authority_binding_ack")
			expiryStoredBindingIO(t, p, 1, 1)
			expiryStoredBindingCreateRequest(t, p, binding, canonical)
			expiryStoredBindingUnchanged(t, p, before)
		})
	}
}
