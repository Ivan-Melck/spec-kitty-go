package v2

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRecoverySemanticAddressesAndScanTargets(t *testing.T) {
	coordinate, err := NewAggregateCoordinate("mission", "mission-01", Scope{Kind: ScopeMission, ProjectID: "project-01", MissionID: "mission-01"})
	if err != nil {
		t.Fatal(err)
	}
	constructors := []func() (RecordAddressV2, error){
		func() (RecordAddressV2, error) { return NewAggregateHeadAddressV2(coordinate) },
		func() (RecordAddressV2, error) { return NewJournalEventAddressV2(coordinate, 7) },
		func() (RecordAddressV2, error) { return NewOperationBindingAddressV2("operation-01") },
		func() (RecordAddressV2, error) { return NewOperationHeadAddressV2("operation-01") },
		func() (RecordAddressV2, error) { return NewCheckpointRecordAddressV2("checkpoint-01") },
		func() (RecordAddressV2, error) { return NewCheckpointSlotAddressV2("slot-01") },
	}
	for _, construct := range constructors {
		address, constructErr := construct()
		if constructErr != nil || address.validate() != nil || address.Kind() == "" {
			t.Fatalf("semantic address rejected: %+v %v", address, constructErr)
		}
	}
	for _, target := range []ScanTargetV2{AggregateHeadScanTargetV2(), OperationRecordScanTargetV2(), CheckpointSlotScanTargetV2()} {
		if target.validate() != nil || target.Kind() == "" {
			t.Fatalf("scan target rejected: %+v", target)
		}
	}
	if _, err := NewAggregateHeadAddressV2(AggregateCoordinate{Kind: "operation", ID: "operation-01", Scope: Scope{Kind: ScopeHost}}); err == nil {
		t.Fatal("operation head admitted through aggregate-head address")
	}
	if (RecordAddressV2{}).validate() == nil || (ScanTargetV2{}).validate() == nil {
		t.Fatal("zero semantic target admitted")
	}
}

func TestRecoveryValuesAreBoundedAndDefensive(t *testing.T) {
	attempt, err := NewStoreRevisionV2("database-01", 17, 17)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := NewOperationHeadAddressV2("operation-01")
	value := []byte(`{"status":"resolved"}`)
	expiry := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	record, err := NewRecoveryRecordV2(address, value, 16, &expiry)
	if err != nil {
		t.Fatal(err)
	}
	cursor := []byte("opaque-provider-cursor")
	page, err := NewRecoveryPageV2(17, expiry, []RecoveryRecordV2{record}, cursor, true)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewScanRequestV2(OperationRecordScanTargetV2(), attempt, cursor, 500, MaxRecoveryPageBytes)
	if err != nil {
		t.Fatal(err)
	}
	value[2], cursor[0] = 'x', 'x'
	expiry = expiry.Add(time.Hour)
	gotRecords := page.Records()
	gotValue, gotCursor, requestCursor := gotRecords[0].CanonicalValue(), page.Cursor(), request.Cursor()
	if !bytes.Equal(gotValue, []byte(`{"status":"resolved"}`)) || string(gotCursor) != "opaque-provider-cursor" || string(requestCursor) != "opaque-provider-cursor" {
		t.Fatal("recovery construction aliased caller bytes")
	}
	gotValue[0], gotCursor[0], requestCursor[0] = 'x', 'x', 'x'
	gotRecords[0].canonical[0] = 'x'
	if !bytes.Equal(page.Records()[0].CanonicalValue(), []byte(`{"status":"resolved"}`)) || string(page.Cursor()) != "opaque-provider-cursor" || string(request.Cursor()) != "opaque-provider-cursor" {
		t.Fatal("recovery accessors exposed writable internals")
	}
	if page.Records()[0].ExpiresAt().Equal(expiry) {
		t.Fatal("expiry aliased caller time")
	}
	if request.AttemptRevision() != attempt || request.Target().Kind() != ScanAllOperationRecords {
		t.Fatal("request lost semantic attempt")
	}
	if _, err := NewScanRequestV2(OperationRecordScanTargetV2(), attempt, make([]byte, MaxRecoveryCursorBytes+1), 1, 1); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("cursor maximum-plus-one accepted: %v", err)
	}
	if _, err := NewRecoveryRecordV2(address, []byte(strings.Repeat("x", MaxCheckpointValueBytes+1)), 1, nil); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("record maximum-plus-one accepted: %v", err)
	}
}

func TestStableRecoveryAttemptRejectsEveryCoordinateMismatch(t *testing.T) {
	capture, _ := NewStoreRevisionV2("database-01", 17, 17)
	final, _ := NewStoreRevisionV2("database-01", 17, 19)
	address, _ := NewOperationBindingAddressV2("operation-01")
	record, _ := NewRecoveryRecordV2(address, []byte(`{"binding":true}`), 17, nil)
	page, _ := NewRecoveryPageV2(17, time.Now().UTC(), []RecoveryRecordV2{record}, nil, false)
	if err := ValidateStableRecoveryAttemptV2(capture, []RecoveryPageV2{page}, []RecoveryRecordV2{record}, final); err != nil {
		t.Fatalf("stable attempt rejected: %v", err)
	}
	for name, run := range map[string]func() error{
		"zero_capture": func() error { return ValidateStableRecoveryAttemptV2(StoreRevisionV2{}, nil, nil, final) },
		"zero_final":   func() error { return ValidateStableRecoveryAttemptV2(capture, nil, nil, StoreRevisionV2{}) },
		"store_moved": func() error {
			moved, _ := NewStoreRevisionV2("database-02", 17, 17)
			return ValidateStableRecoveryAttemptV2(capture, nil, nil, moved)
		},
		"final_revision_moved": func() error {
			moved, _ := NewStoreRevisionV2("database-01", 18, 18)
			return ValidateStableRecoveryAttemptV2(capture, nil, nil, moved)
		},
		"page_not_capture_revision": func() error {
			moved, _ := NewRecoveryPageV2(16, time.Now().UTC(), nil, nil, false)
			return ValidateStableRecoveryAttemptV2(capture, []RecoveryPageV2{moved}, nil, final)
		},
		"record_after_capture": func() error {
			future, _ := NewRecoveryRecordV2(address, []byte(`{"binding":true}`), 18, nil)
			return ValidateStableRecoveryAttemptV2(capture, nil, []RecoveryRecordV2{future}, final)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("unstable recovery attempt accepted")
			}
		})
	}
	if _, err := NewStoreRevisionV2("database-01", 17, 16); !IsKind(err, ErrorInvalidVersion) {
		t.Fatalf("durable-through below current accepted: %v", err)
	}
}

func TestReadRequestCarriesAttemptProofNotAsOfPromise(t *testing.T) {
	attempt, _ := NewStoreRevisionV2("database-01", 41, 41)
	address, _ := NewJournalEventAddressV2(AggregateCoordinate{Kind: "mission", ID: "mission-01", Scope: Scope{Kind: ScopeMission, ProjectID: "project-01", MissionID: "mission-01"}}, 3)
	request, err := NewReadRequestV2(address, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if request.Address().LogicalVersion() != 3 || request.AttemptRevision().CurrentRevision() != 41 {
		t.Fatal("read request lost semantic attempt verification")
	}
	// There intentionally is no requested-as-of-revision API. The attempt
	// coordinate is used to reject movement after latest-value reads.
}
