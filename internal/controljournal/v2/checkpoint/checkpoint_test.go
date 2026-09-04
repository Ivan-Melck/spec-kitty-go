package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func testDigest(domain, value string) journal.Digest {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(value))
	var result journal.Digest
	copy(result[:], h.Sum(nil))
	return result
}

func checkpointFixture(t *testing.T) CheckpointV2 {
	t.Helper()
	coordinate, err := journal.NewAggregateCoordinate("mission", "mission-1", journal.Scope{Kind: journal.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"})
	if err != nil {
		t.Fatal(err)
	}
	state := []byte(`{"value":1}`)
	stateDigest, _ := journal.ReducerStateDigestV2(state)
	publication := journal.PublicationID(testDigest("publication", "one").String())
	operation, err := journal.NewOperationStateV2(journal.OperationStateInput{OperationID: "operation-1", LogicalVersion: 1, Status: journal.StatusAccepted, EventIDs: []journal.EventID{"event-1"}, LastPublicationID: publication})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := journal.BuildOperationSummaryV2([]journal.OperationStateV2{operation})
	if err != nil {
		t.Fatal(err)
	}
	c := CheckpointV2{checkpointID: "checkpoint-1", throughRevision: 7, eventRegistry: testDigest("registry", "event"), reducerRegistry: testDigest("registry", "reducer"), codecVersion: journal.CanonicalCodecVersionV2, rollingPrefix: testDigest("prefix", "one"), aggregates: []AggregateEntryV2{{coordinate, 1, "event-1", state, stateDigest, true}}, operationSummary: summary, projectionDigest: testDigest("projection", "one"), coveredThrough: publication}
	encoded, digest, err := encodeCheckpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	c.canonical, c.checkpointDigest = encoded, digest
	c.seal = checkpointSeal(c)
	return c
}

func TestCheckpointCanonicalRoundTripAndDefensiveCopies(t *testing.T) {
	want := checkpointFixture(t)
	encoded, err := EncodeV2(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeV2(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.CheckpointDigest() != want.CheckpointDigest() || got.CoveredThroughPublicationID() != want.CoveredThroughPublicationID() || len(got.Aggregates()) != 1 || !got.Aggregates()[0].Tombstone() {
		t.Fatalf("round trip mismatch")
	}
	first := got.Aggregates()
	first[0].state[0] = 'x'
	if bytes.Equal(first[0].state, got.Aggregates()[0].state) {
		t.Fatal("aggregate state aliases checkpoint")
	}
	copyBytes := got.CanonicalBytes()
	copyBytes[0] = 'x'
	if bytes.Equal(copyBytes, got.CanonicalBytes()) {
		t.Fatal("canonical bytes alias checkpoint")
	}
}

func TestCheckpointAndSlotCanonicalBytesCrossDurableRecoveryAdmission(t *testing.T) {
	checkpoint := checkpointFixture(t)
	checkpointAddress, err := journal.NewCheckpointRecordAddressV2(checkpoint.CheckpointID())
	if err != nil {
		t.Fatal(err)
	}
	checkpointRecord, err := journal.NewRecoveryRecordV2(checkpointAddress, checkpoint.CanonicalBytes(), 8, "")
	if err != nil {
		t.Fatalf("checkpoint bytes are not durable-canonical: %v", err)
	}
	if !bytes.Equal(checkpointRecord.CanonicalValue(), checkpoint.CanonicalBytes()) {
		t.Fatal("checkpoint recovery admission changed bytes")
	}

	slot, slotBytes, err := NewSlotV2("slot-1", checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	slotAddress, _ := journal.NewCheckpointSlotAddressV2(slot.SlotID())
	slotRecord, err := journal.NewRecoveryRecordV2(slotAddress, slotBytes, 8, "")
	if err != nil {
		t.Fatalf("slot bytes are not durable-canonical: %v", err)
	}
	if !bytes.Equal(slotRecord.CanonicalValue(), slotBytes) {
		t.Fatal("slot recovery admission changed bytes")
	}

	large := largeCheckpointFixture(t, 1_200)
	if len(large.CanonicalBytes()) <= journal.MaxCanonicalPayloadBytes {
		t.Fatalf("large checkpoint did not cross ordinary bound: %d", len(large.CanonicalBytes()))
	}
	largeAddress, _ := journal.NewCheckpointRecordAddressV2(large.CheckpointID())
	largeRecord, err := journal.NewRecoveryRecordV2(largeAddress, large.CanonicalBytes(), large.ThroughRecordRevision()+1, "")
	if err != nil || !bytes.Equal(largeRecord.CanonicalValue(), large.CanonicalBytes()) {
		t.Fatalf("large checkpoint did not survive recovery admission byte-for-byte: %v", err)
	}
}

func TestCheckpointDecodeRejectsCorruptionUnknownDuplicateAndTrailingJSON(t *testing.T) {
	valid := checkpointFixture(t).CanonicalBytes()
	mutants := [][]byte{append(append([]byte(nil), valid...), []byte(" {}")...), bytes.Replace(valid, []byte(`"schema_version":2`), []byte(`"schema_version":2,"unknown":1`), 1), bytes.Replace(valid, []byte(`"schema_version":2`), []byte(`"schema_version":2,"schema_version":2`), 1), bytes.Replace(valid, []byte(`"through_record_revision":7`), []byte(`"through_record_revision":8`), 1)}
	for i, mutant := range mutants {
		if _, err := DecodeV2(mutant); err == nil {
			t.Fatalf("mutant %d accepted", i)
		}
	}
}

func TestCheckpointSlotRoundTripAndForgedSealRefusal(t *testing.T) {
	c := checkpointFixture(t)
	slot, encoded, err := NewSlotV2("slot-1", c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSlotV2(encoded)
	if err != nil || decoded != slot {
		t.Fatalf("slot=%+v err=%v", decoded, err)
	}
	forged := c
	forged.seal = journal.Digest{}
	if _, err := EncodeV2(forged); err == nil {
		t.Fatal("forged checkpoint capability accepted")
	}
}

func TestCheckpointCandidateRingFallbackContradictionAndOrigin(t *testing.T) {
	older := checkpointFixture(t)
	newer := checkpointWithIdentity(t, older, "checkpoint-2", 9, "newer")
	attempt, _ := journal.NewStoreRevisionV2("database-1", 12, 12)
	compatibility := CompatibilityV2{older.EventRegistryDigest(), older.ReducerRegistryDigest(), older.CanonicalCodecVersion()}
	olderCandidate := checkpointCandidate(t, "slot-1", older, 8)
	newerCandidate := checkpointCandidate(t, "slot-2", newer, 10)

	selection, err := SelectV2([]CandidateV2{olderCandidate, newerCandidate}, compatibility, attempt)
	if err != nil || selection.IsOrigin() || selection.Checkpoint().CheckpointID() != newer.CheckpointID() || selection.CommitRevision() != 10 {
		t.Fatalf("newest valid candidate not selected: %+v %v", selection, err)
	}

	corruptNewest := newerCandidate
	corruptNewest.CheckpointRecord = olderCandidate.CheckpointRecord
	selection, err = SelectV2([]CandidateV2{corruptNewest, olderCandidate}, compatibility, attempt)
	if err != nil || selection.Checkpoint().CheckpointID() != older.CheckpointID() {
		t.Fatalf("corrupt newest did not fall back to older valid candidate: %+v %v", selection, err)
	}

	peer := checkpointWithIdentity(t, older, "checkpoint-peer", older.ThroughRecordRevision(), "peer")
	peerCandidate := checkpointCandidate(t, "slot-peer", peer, 9)
	if _, err := SelectV2([]CandidateV2{olderCandidate, peerCandidate}, compatibility, attempt); err == nil {
		t.Fatal("contradictory valid candidates at one watermark were accepted")
	}

	incompatible := compatibility
	incompatible.EventRegistryDigest = testDigest("registry", "unknown")
	selection, err = SelectV2([]CandidateV2{olderCandidate}, incompatible, attempt)
	if err != nil || !selection.IsOrigin() {
		t.Fatalf("sole incompatible candidate did not select bounded origin reconstruction: %+v %v", selection, err)
	}

	overRing := make([]CandidateV2, RetainedCandidateSlots+1)
	if _, err := SelectV2(overRing, compatibility, attempt); err == nil {
		t.Fatal("candidate ring maximum-plus-one accepted")
	}
}

func TestCheckpointCandidateRingDuplicateCannotMaskConflict(t *testing.T) {
	checkpointA := checkpointFixture(t)
	checkpointB := checkpointWithIdentity(t, checkpointA, "checkpoint-peer", checkpointA.ThroughRecordRevision(), "peer")
	attempt, _ := journal.NewStoreRevisionV2("database-1", 12, 12)
	compatibility := CompatibilityV2{checkpointA.EventRegistryDigest(), checkpointA.ReducerRegistryDigest(), checkpointA.CanonicalCodecVersion()}
	candidates := []CandidateV2{
		checkpointCandidate(t, "slot-a-1", checkpointA, 8),
		checkpointCandidate(t, "slot-a-2", checkpointA, 8),
		checkpointCandidate(t, "slot-b", checkpointB, 9),
	}
	_, err := SelectV2(candidates, compatibility, attempt)
	var checkpointError *Error
	if !errors.As(err, &checkpointError) || checkpointError.Kind != ErrorContradictory {
		t.Fatalf("evidence=CHECKPOINT-CONTRADICTORY-CENSUS duplicate identity masked conflicting peer: %v", err)
	}
}

func TestCheckpointCandidateConflictSourceMutationIsKilled(t *testing.T) {
	root := checkpointRepositoryRoot(t)
	sourcePath := filepath.Join(root, "internal/controljournal/v2/checkpoint/checkpoint.go")
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	anchor := `	selected := valid[0]
	for _, peer := range valid[1:] {
		if peer.checkpoint.throughRevision != selected.checkpoint.throughRevision {
			break
		}
		if peer.checkpoint.checkpointDigest != selected.checkpoint.checkpointDigest {
			return SelectionV2{}, &Error{ErrorContradictory, "watermark"}
		}
	}
	return SelectionV2{checkpoint: selected.checkpoint, commitRevision: selected.commitRevision}, nil`
	firstPairOnly := `	if len(valid) > 1 && valid[0].checkpoint.throughRevision == valid[1].checkpoint.throughRevision && valid[0].checkpoint.checkpointDigest != valid[1].checkpoint.checkpointDigest {
		return SelectionV2{}, &Error{ErrorContradictory, "watermark"}
	}
	return SelectionV2{checkpoint: valid[0].checkpoint, commitRevision: valid[0].commitRevision}, nil`
	if count := strings.Count(source, anchor); count != 1 {
		t.Fatalf("checkpoint conflict mutant anchor count=%d want=1", count)
	}
	mutated := strings.Replace(source, anchor, firstPairOnly, 1)
	temporary := t.TempDir()
	mutatedPath := filepath.Join(temporary, "checkpoint.go")
	if err := os.WriteFile(mutatedPath, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	overlayBytes, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{sourcePath: mutatedPath}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(temporary, "overlay.json")
	if err := os.WriteFile(overlayPath, overlayBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-overlay="+overlayPath, "./internal/controljournal/v2/checkpoint", "-run", "^TestCheckpointCandidateRingDuplicateCannotMaskConflict$", "-count=1")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off")
	output, runErr := command.CombinedOutput()
	if runErr == nil || !strings.Contains(string(output), "evidence=CHECKPOINT-CONTRADICTORY-CENSUS") {
		t.Fatalf("mutant=M-CHECKPOINT-FIRST-PAIR-ONLY survived-or-wrong-law err=%v output=%s", runErr, output)
	}
	t.Logf("mutant=M-CHECKPOINT-FIRST-PAIR-ONLY killer=TestCheckpointCandidateRingDuplicateCannotMaskConflict source_sha256=%x mutant_sha256=%x", sha256.Sum256(sourceBytes), sha256.Sum256([]byte(mutated)))
}

func checkpointRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}

func largeCheckpointFixture(t *testing.T, aggregateCount int) CheckpointV2 {
	t.Helper()
	base := checkpointFixture(t)
	state := []byte(`{"value":1}`)
	stateDigest, _ := journal.ReducerStateDigestV2(state)
	aggregates := make([]AggregateEntryV2, aggregateCount)
	for i := range aggregates {
		id := journal.AggregateID(fmt.Sprintf("mission-%05d", i))
		coordinate, err := journal.NewAggregateCoordinate("mission", id, journal.Scope{Kind: journal.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"})
		if err != nil {
			t.Fatal(err)
		}
		aggregates[i] = AggregateEntryV2{coordinate, 1, journal.EventID(fmt.Sprintf("event-%05d", i)), state, stateDigest, false}
	}
	base.checkpointID = "checkpoint-large"
	base.aggregates = aggregates
	encoded, checksum, err := encodeCheckpoint(base)
	if err != nil {
		t.Fatal(err)
	}
	base.canonical, base.checkpointDigest = encoded, checksum
	base.seal = checkpointSeal(base)
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	return base
}

func checkpointWithIdentity(t *testing.T, base CheckpointV2, id journal.CheckpointID, through uint64, publicationLabel string) CheckpointV2 {
	t.Helper()
	base.checkpointID = id
	base.throughRevision = through
	base.coveredThrough = journal.PublicationID(testDigest("publication", publicationLabel).String())
	encoded, checksum, err := encodeCheckpoint(base)
	if err != nil {
		t.Fatal(err)
	}
	base.canonical, base.checkpointDigest = encoded, checksum
	base.seal = checkpointSeal(base)
	return base
}

func checkpointCandidate(t *testing.T, slotID journal.CheckpointSlotID, checkpoint CheckpointV2, commitRevision uint64) CandidateV2 {
	t.Helper()
	_, slotBytes, err := NewSlotV2(slotID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	slotAddress, _ := journal.NewCheckpointSlotAddressV2(slotID)
	checkpointAddress, _ := journal.NewCheckpointRecordAddressV2(checkpoint.CheckpointID())
	witnessAddress, _ := checkpoint.CoveredThroughPublicationID().WitnessAddressV2()
	return CandidateV2{
		SlotRecord:       checkpointRecoveryRecord(t, slotAddress, slotBytes, commitRevision),
		CheckpointRecord: checkpointRecoveryRecord(t, checkpointAddress, checkpoint.CanonicalBytes(), commitRevision),
		CoveredWitness:   checkpointRecoveryRecord(t, witnessAddress, checkpointWitnessBytes(t, checkpoint.CoveredThroughPublicationID()), checkpoint.ThroughRecordRevision()),
	}
}

func checkpointRecoveryRecord(t *testing.T, address journal.RecordAddressV2, value []byte, revision uint64) journal.RecoveryRecordV2 {
	t.Helper()
	record, err := journal.NewRecoveryRecordV2(address, value, revision, "")
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func checkpointWitnessBytes(t *testing.T, publicationID journal.PublicationID) []byte {
	t.Helper()
	plan, records := testDigest("plan", string(publicationID)), testDigest("records", string(publicationID))
	preimage, _ := json.Marshal(struct {
		SchemaVersion   uint32 `json:"schema_version"`
		PublicationID   string `json:"publication_id"`
		OperationID     string `json:"operation_id"`
		LogicalVersion  uint64 `json:"operation_logical_version"`
		PlanSHA256      string `json:"plan_sha256"`
		RecordSetSHA256 string `json:"authority_record_set_sha256"`
		RecordCount     uint32 `json:"authority_record_count"`
	}{1, string(publicationID), "operation-checkpoint", 1, plan.String(), records.String(), 2})
	witnessDigest := testDigest("spec-kitty.publication-witness.v2", string(preimage))
	wire, _ := json.Marshal(map[string]any{"schema_version": uint32(1), "publication_id": string(publicationID), "operation_id": "operation-checkpoint", "operation_logical_version": uint64(1), "plan_sha256": plan.String(), "authority_record_set_sha256": records.String(), "authority_record_count": uint32(2), "witness_sha256": witnessDigest.String()})
	encoded, err := journal.CanonicalObject(wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.DecodePublicationWitnessV2(encoded); err != nil {
		t.Fatal(err)
	}
	return encoded
}
