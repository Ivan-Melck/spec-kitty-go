package productionruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func paddedProgressOperation(t *testing.T, id string, size int) ProgressItem {
	t.Helper()
	raw, err := json.Marshal(struct {
		Stage       string `json:"stage"`
		Observation string `json:"observation"`
	}{"pending_reconciliation", strings.Repeat("x", size)})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil {
		t.Fatalf("fixture exceeds individual durable-state bound: %v", err)
	}
	return progressItem(progressView("operation", id, string(canonical)))
}

func TestProgressMissionAboveDurablePayloadBudgetKeepsCompleteCensus(t *testing.T) {
	query := progressFixture()
	for i := 0; i < 3; i++ {
		item := paddedProgressOperation(t, fmt.Sprintf("operation-size-%d", i), 48*1024)
		query.items = append(query.items, progressView(item.Family, item.AggregateID, string(item.CanonicalState)))
	}
	service, err := NewProgressService(query, fixedActionCensus{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Mission("project-1", "mission-1")
	if err != nil {
		t.Fatalf("valid complete mission exceeded unrelated durable payload budget: %v", err)
	}
	wire, err := json.Marshal(got)
	if err != nil || len(wire) <= journalv2.MaxCanonicalPayloadBytes {
		t.Fatalf("fixture wire bytes=%d err=%v", len(wire), err)
	}
	if !got.Complete || len(got.Items) != len(query.items) || len(got.Operations) != 4 || len(got.PendingReconciliation) != 4 || !hexDigest(got.SnapshotSHA256) {
		t.Fatalf("complete census lost: items=%d operations=%d pending=%d digest=%s", len(got.Items), len(got.Operations), len(got.PendingReconciliation), got.SnapshotSHA256)
	}
	if _, err := journalv2.CanonicalObject(wire); !journalv2.IsKind(err, journalv2.ErrorBudgetExceeded) {
		t.Fatalf("durable payload budget changed: %v", err)
	}
	before := query.queryCalls.Load()
	got.Operations[0].CanonicalState[0] ^= 0xff
	again, err := service.Mission("project-1", "mission-1")
	if err != nil || again.SnapshotSHA256 != got.SnapshotSHA256 || bytes.Equal(again.Operations[0].CanonicalState, got.Operations[0].CanonicalState) || query.queryCalls.Load()-before != 1 {
		t.Fatalf("immutable cached complete census changed: %v", err)
	}
}

func TestProgressSnapshotChargesEveryDualMissionClassification(t *testing.T) {
	query := progressFixture()
	query.items = append(query.items, progressView("mission", "blocker-decision", `{"kind":"blocker-decision","status":"active"}`))
	service, err := NewProgressService(query, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Mission("project-1", "mission-1")
	if err != nil || len(got.Items) != len(query.items) || len(got.Blockers) != 1 || len(got.Decisions) != 1 {
		t.Fatalf("dual mission membership lost: items=%d blockers=%d decisions=%d err=%v", len(got.Items), len(got.Blockers), len(got.Decisions), err)
	}
	// A refused three-destination append must not copy/publish even the first.
	item := got.Blockers[0]
	wire, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	budget := progressSnapshotBudget{used: MaxProgressSnapshotBytes - 64 - 3*(len(wire)+1) + 1}
	var items, blockers, decisions []ProgressItem
	if err := budget.append(item, &items, &blockers, &decisions); !IsProgressKind(err, ProgressIncomplete) || len(items)+len(blockers)+len(decisions) != 0 {
		t.Fatalf("multi-destination refusal published partial membership: %v", err)
	}
}

func TestProgressSnapshotDigestRetainsLegacyCanonicalPreimage(t *testing.T) {
	query := progressFixture()
	query.base.Revision = math.MaxUint64
	query.base.DatabaseID = "database-☃<>&\u2028"
	query.items[0].LogicalVersion = journalv2.LogicalVersion(math.MaxUint64)
	query.items[0].CanonicalState = []byte(`{"lifecycle":"open","note":"<>&☃\\\""}`)
	service, err := NewProgressService(query, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Mission("project-1", "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	want := got.SnapshotSHA256
	got.SnapshotSHA256 = ""
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(wire)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := journalv2.ReducerStateDigestV2(canonical)
	if err != nil || want != digest.String() {
		t.Fatalf("snapshot digest compatibility: got=%s want=%s err=%v", want, digest.String(), err)
	}
}

func TestProgressSnapshotReturnedByteBoundaryIncludesDigestAndDerivedMetadata(t *testing.T) {
	value := MissionProgress{Actions: []ActionAvailability{{Action: "metadata"}}}
	wire, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	// An owned derived field bypasses per-item charges but not final encoding.
	value.Actions[0].Action += strings.Repeat("x", MaxProgressSnapshotBytes-len(wire)-64)
	digest, err := progressSnapshotDigest(value)
	if err != nil {
		t.Fatalf("exact returned snapshot budget refused: %v", err)
	}
	value.SnapshotSHA256 = digest
	wire, err = json.Marshal(value)
	if err != nil || len(wire) != MaxProgressSnapshotBytes {
		t.Fatalf("returned bytes=%d want=%d err=%v", len(wire), MaxProgressSnapshotBytes, err)
	}
	// Supplying an existing digest must not create a self-referential preimage.
	again, err := progressSnapshotDigest(value)
	if err != nil || again != digest {
		t.Fatalf("digest field entered its own commitment: %v", err)
	}
	value.Actions[0].Action += "x"
	_, err = progressSnapshotDigest(value)
	var refusal *ProgressError
	if !errors.As(err, &refusal) || refusal.Kind != ProgressIncomplete || refusal.Field != "snapshot_budget" {
		t.Fatalf("oversized derived metadata did not refuse: %v", err)
	}
}

func TestProgressSnapshotBudgetRefusesWholeMissionWithoutCachingPartialCensus(t *testing.T) {
	query := progressFixture()
	for i := 0; i < 40; i++ {
		item := paddedProgressOperation(t, fmt.Sprintf("operation-budget-%d", i), 128*1024)
		query.items = append(query.items, progressView(item.Family, item.AggregateID, string(item.CanonicalState)))
	}
	service, err := NewProgressService(query, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Mission("project-1", "mission-1")
	var refusal *ProgressError
	if !errors.As(err, &refusal) || refusal.Kind != ProgressIncomplete || refusal.Field != "snapshot_budget" || got.SnapshotSHA256 != "" || len(got.Items) != 0 {
		t.Fatalf("snapshot budget returned partial/untyped progress: kind=%v result items=%d", err, len(got.Items))
	}
	// Same coordinate now serves a small complete fixture: a failed read must
	// not publish a partial cache entry that masks the next observation.
	query.items = progressFixture().items
	again, err := service.Mission("project-1", "mission-1")
	if err != nil || len(again.Items) != len(query.items) || !hexDigest(again.SnapshotSHA256) {
		t.Fatalf("refusal polluted cache: %v", err)
	}
}
