package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

type SuffixMeasureV1 struct {
	CommittedCommands   uint64
	CommittedEvents     uint64
	CanonicalEventBytes uint64
}

func CheckpointDueV1(measure SuffixMeasureV1) bool {
	return measure.CommittedCommands >= CheckpointCommandCadence ||
		measure.CommittedEvents >= CheckpointEventCadence ||
		measure.CanonicalEventBytes >= CheckpointByteCadence
}

type BuildInputV1 struct {
	CheckpointID          CheckpointID
	Source                SourceIdentityV1
	PriorPrefix           controljournal.ValidatedJournalV1
	EventRegistry         controljournal.EventRegistryV1
	Reducers              reducer.RegistryV1
	Watermark             WatermarkV1
	Aggregates            []AggregateSnapshotV1
	Authority             AuthoritySummaryV1
	FormingBatchOperation controljournal.OperationID
}

type prefixTerminalV1 struct {
	version uint64
	event   controljournal.EventID
	epoch   controljournal.RuntimeEpochID
}

type checkpointPrefixAnalysisV1 struct {
	watermark WatermarkV1
	terminals map[reducer.AggregateKeyV1]prefixTerminalV1
	authority AuthoritySummaryV1
}

type checkpointValidationSealV1 struct {
	version               uint8
	digest                [32]byte
	formingBatchOperation controljournal.OperationID
}

// ValidatedCheckpointV1 is the sole checkpoint-publication capability. Its
// bytes and seal are private; every accessor revalidates them and returns
// defensive copies. Durable restore deliberately accepts raw persisted bytes
// and repeats their cryptographic and semantic validation after restart.
type ValidatedCheckpointV1 struct {
	seal        checkpointValidationSealV1
	recordBytes []byte
	headBytes   []byte
}

type InvalidValidatedCheckpointErrorV1 struct{}

func (InvalidValidatedCheckpointErrorV1) Error() string { return "invalid validated checkpoint" }

type CheckpointPublicationV1 struct {
	CheckpointID            CheckpointID
	FormingBatchOperationID controljournal.OperationID
	RecordBytes             []byte
	HeadBytes               []byte
}

func BuildV1(input BuildInputV1) (ValidatedCheckpointV1, error) {
	if _, err := controljournal.NewRecordID(string(input.CheckpointID)); err != nil {
		return ValidatedCheckpointV1{}, err
	}
	if _, err := controljournal.NewOperationID(string(input.FormingBatchOperation)); err != nil {
		return ValidatedCheckpointV1{}, err
	}
	if err := validateSource(input.Source); err != nil {
		return ValidatedCheckpointV1{}, err
	}
	if err := validateRegistryBindings(input.Source, input.EventRegistry, input.Reducers); err != nil {
		return ValidatedCheckpointV1{}, err
	}
	if err := validateAuthority(input.Authority); err != nil {
		return ValidatedCheckpointV1{}, err
	}
	if input.Authority.RunID != input.Source.RunID {
		return ValidatedCheckpointV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "checkpoint_run"}
	}
	analysis, err := analyzeCheckpointPrefix(input.PriorPrefix, input.FormingBatchOperation, input.Source.RunID)
	if err != nil {
		return ValidatedCheckpointV1{}, err
	}
	actual := analysis.watermark
	if actual != input.Watermark {
		return ValidatedCheckpointV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_watermark"}
	}
	if input.Authority.DurableRevision != actual.ThroughRecordRevision {
		return ValidatedCheckpointV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "checkpoint_authority_revision"}
	}
	projection, err := projectionFromAggregates(input.Aggregates)
	if err != nil {
		return ValidatedCheckpointV1{}, err
	}
	reduced, err := reducer.ApplyV1(input.Reducers, reducer.EmptyProjectionV1(), input.PriorPrefix)
	if err != nil {
		return ValidatedCheckpointV1{}, err
	}
	if err := verifySnapshotAgainstPrefix(reduced, input.Aggregates, analysis.terminals); err != nil {
		return ValidatedCheckpointV1{}, err
	}
	if projection.GlobalDigest() != reduced.GlobalDigest() {
		return ValidatedCheckpointV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_projection"}
	}
	if analysis.authority != input.Authority {
		return ValidatedCheckpointV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_authority"}
	}
	record := CheckpointRecordV1{
		SchemaVersion: SchemaVersionV1, CheckpointID: input.CheckpointID,
		ReducerSetDigest: input.Source.ReducerSetDigest, EventSchemaSetDigest: input.Source.EventSchemaSetDigest,
		ProjectID: input.Source.ProjectID, MissionID: input.Source.MissionID,
		Watermark: input.Watermark, CanonicalProjectionDigest: projection.GlobalDigest(),
		Codec: CodecCanonicalJSONV1, Aggregates: cloneAggregates(input.Aggregates), Authority: input.Authority,
	}
	projectionBytes, err := json.Marshal(checkpointProjectionV1FromRecord(record))
	if err != nil {
		return ValidatedCheckpointV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint_projection"}
	}
	record.UncompressedSize = uint64(len(projectionBytes))
	recordBytes, err := canonicalCheckpoint(record)
	if err != nil || len(recordBytes) > MaxCheckpointBytes {
		return ValidatedCheckpointV1{}, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "checkpoint_bytes"}
	}
	digest := sha256.Sum256(recordBytes)
	head := CheckpointHeadV1{
		SchemaVersion: SchemaVersionV1, CheckpointID: input.CheckpointID,
		CheckpointRecordDigest: digest, ThroughRecordRevision: actual.ThroughRecordRevision,
		ThroughEventID: actual.ThroughEventID,
	}
	headBytes, err := EncodeHeadV1(head)
	if err != nil {
		return ValidatedCheckpointV1{}, err
	}
	return sealCheckpoint(recordBytes, headBytes, input.FormingBatchOperation), nil
}

func sealCheckpoint(recordBytes, headBytes []byte, formingBatchOperation controljournal.OperationID) ValidatedCheckpointV1 {
	recordCopy := append([]byte(nil), recordBytes...)
	headCopy := append([]byte(nil), headBytes...)
	return ValidatedCheckpointV1{
		seal: checkpointValidationSealV1{
			version: 1, formingBatchOperation: formingBatchOperation,
			digest: checkpointCapabilityDigest(recordCopy, headCopy, formingBatchOperation),
		},
		recordBytes: recordCopy, headBytes: headCopy,
	}
}

func checkpointCapabilityDigest(recordBytes, headBytes []byte, formingBatchOperation controljournal.OperationID) [32]byte {
	var value bytes.Buffer
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(recordBytes)))
	value.Write(size[:])
	value.Write(recordBytes)
	binary.BigEndian.PutUint64(size[:], uint64(len(headBytes)))
	value.Write(size[:])
	value.Write(headBytes)
	binary.BigEndian.PutUint64(size[:], uint64(len(formingBatchOperation)))
	value.Write(size[:])
	value.WriteString(string(formingBatchOperation))
	return sha256.Sum256(value.Bytes())
}

func (checkpoint ValidatedCheckpointV1) validatedParts() (CheckpointRecordV1, CheckpointHeadV1, error) {
	if checkpoint.seal.version != 1 || checkpoint.seal.digest == [32]byte{} ||
		checkpointCapabilityDigest(checkpoint.recordBytes, checkpoint.headBytes, checkpoint.seal.formingBatchOperation) != checkpoint.seal.digest {
		return CheckpointRecordV1{}, CheckpointHeadV1{}, InvalidValidatedCheckpointErrorV1{}
	}
	if _, err := controljournal.NewOperationID(string(checkpoint.seal.formingBatchOperation)); err != nil {
		return CheckpointRecordV1{}, CheckpointHeadV1{}, InvalidValidatedCheckpointErrorV1{}
	}
	record, err := DecodeRecordV1(checkpoint.recordBytes)
	if err != nil {
		return CheckpointRecordV1{}, CheckpointHeadV1{}, InvalidValidatedCheckpointErrorV1{}
	}
	head, err := DecodeHeadV1(checkpoint.headBytes)
	if err != nil || head.CheckpointID != record.CheckpointID ||
		head.ThroughRecordRevision != record.Watermark.ThroughRecordRevision ||
		head.ThroughEventID != record.Watermark.ThroughEventID ||
		head.CheckpointRecordDigest != digestBytes(checkpoint.recordBytes) {
		return CheckpointRecordV1{}, CheckpointHeadV1{}, InvalidValidatedCheckpointErrorV1{}
	}
	return record, head, nil
}

func (checkpoint ValidatedCheckpointV1) RecordV1() (CheckpointRecordV1, error) {
	record, _, err := checkpoint.validatedParts()
	if err != nil {
		return CheckpointRecordV1{}, err
	}
	record.Aggregates = cloneAggregates(record.Aggregates)
	return record, nil
}

func (checkpoint ValidatedCheckpointV1) HeadV1() (CheckpointHeadV1, error) {
	_, head, err := checkpoint.validatedParts()
	return head, err
}

// PublicationV1 yields the immutable batch inputs needed by the application
// layer without exporting the checkpoint's proof seal or backing storage.
func (checkpoint ValidatedCheckpointV1) PublicationV1(formingBatchOperation controljournal.OperationID) (CheckpointPublicationV1, error) {
	record, _, err := checkpoint.validatedParts()
	if err != nil {
		return CheckpointPublicationV1{}, err
	}
	if _, err := controljournal.NewOperationID(string(formingBatchOperation)); err != nil || formingBatchOperation != checkpoint.seal.formingBatchOperation {
		return CheckpointPublicationV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "checkpoint_forming_batch_operation"}
	}
	return CheckpointPublicationV1{
		CheckpointID:            record.CheckpointID,
		FormingBatchOperationID: checkpoint.seal.formingBatchOperation,
		RecordBytes:             append([]byte(nil), checkpoint.recordBytes...),
		HeadBytes:               append([]byte(nil), checkpoint.headBytes...),
	}, nil
}

func validateRegistryBindings(source SourceIdentityV1, events controljournal.EventRegistryV1, reducers reducer.RegistryV1) error {
	eventDigest, err := events.Digest()
	if err != nil {
		return controljournal.ValidationError{Kind: controljournal.RefusalUnsupportedVersion, Field: "event_schema_set"}
	}
	reducerDigest, err := reducers.Digest()
	if err != nil {
		return controljournal.ValidationError{Kind: controljournal.RefusalUnsupportedVersion, Field: "reducer_set"}
	}
	if source.EventSchemaSetDigest != eventDigest {
		return controljournal.ValidationError{Kind: controljournal.RefusalUnsupportedVersion, Field: "event_schema_set_digest"}
	}
	if source.ReducerSetDigest != reducerDigest {
		return controljournal.ValidationError{Kind: controljournal.RefusalUnsupportedVersion, Field: "reducer_set_digest"}
	}
	return nil
}

func verifySnapshotAgainstPrefix(reduced reducer.ProjectionV1, aggregates []AggregateSnapshotV1, terminals map[reducer.AggregateKeyV1]prefixTerminalV1) error {
	if len(terminals) != len(aggregates) {
		return controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_aggregate_head_set"}
	}
	for _, aggregate := range aggregates {
		key := reducer.AggregateKeyV1{Kind: aggregate.Kind, ID: aggregate.ID}
		terminal, ok := terminals[key]
		state, stateOK := reduced.State(key)
		head, headOK := reduced.Head(key)
		if !ok || !stateOK || !headOK || terminal.version != aggregate.LogicalVersion || terminal.event != aggregate.LastEventID || terminal.epoch != aggregate.RuntimeEpochID || head != aggregate.LogicalVersion || !bytes.Equal(state.CanonicalBytes(), aggregate.State) || state.Digest() != aggregate.StateDigest {
			return controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_aggregate_head"}
		}
	}
	return nil
}

func analyzeCheckpointPrefix(journal controljournal.ValidatedJournalV1, forming controljournal.OperationID, runID controljournal.RunID) (checkpointPrefixAnalysisV1, error) {
	records, err := controljournal.SnapshotJournalV1(journal)
	length := len(records)
	if err != nil {
		return checkpointPrefixAnalysisV1{}, err
	}
	if length == 0 {
		return checkpointPrefixAnalysisV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint_prefix"}
	}
	analysis := checkpointPrefixAnalysisV1{
		terminals: make(map[reducer.AggregateKeyV1]prefixTerminalV1),
		authority: AuthoritySummaryV1{RunID: runID},
	}
	digest := [32]byte{}
	for index, record := range records {
		if forming != "" && record.Envelope.OperationID == forming {
			return checkpointPrefixAnalysisV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "current_batch_checkpoint"}
		}
		if record.RecordRevision <= analysis.watermark.ThroughRecordRevision {
			return checkpointPrefixAnalysisV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "suffix_revision"}
		}
		encoded, encodeErr := controljournal.EncodeEventEnvelopeV1(record.Envelope)
		if encodeErr != nil {
			return checkpointPrefixAnalysisV1{}, encodeErr
		}
		var folded bytes.Buffer
		folded.Write(digest[:])
		var revision [8]byte
		binary.BigEndian.PutUint64(revision[:], record.RecordRevision)
		folded.Write(revision[:])
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(encoded)))
		folded.Write(size[:])
		folded.Write(encoded)
		digest = sha256.Sum256(folded.Bytes())
		analysis.watermark = WatermarkV1{
			ThroughRecordRevision: record.RecordRevision,
			ThroughEventID:        record.Envelope.EventID,
			EventCount:            uint64(index + 1),
			EventPrefixDigest:     digest,
		}
		key := reducer.AggregateKeyV1{Kind: record.Envelope.AggregateKind, ID: record.Envelope.AggregateID}
		analysis.terminals[key] = prefixTerminalV1{
			version: record.Envelope.AggregateLogicalVersion,
			event:   record.Envelope.EventID,
			epoch:   record.Envelope.RuntimeEpochID,
		}
		analysis.authority.RuntimeEpochID = record.Envelope.RuntimeEpochID
		analysis.authority.DurableRevision = record.RecordRevision
		if record.Envelope.LeaseID != "" {
			analysis.authority.LeaseID = record.Envelope.LeaseID
			analysis.authority.LeaseGeneration = record.Envelope.LeaseGeneration
			analysis.authority.FenceToken = record.Envelope.FenceToken
		}
	}
	if analysis.authority.RunID != runID || validateAuthority(analysis.authority) != nil {
		return checkpointPrefixAnalysisV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_authority"}
	}
	return analysis, nil
}

func EncodeRecordV1(record CheckpointRecordV1) ([]byte, error) {
	if err := validateRecord(record); err != nil {
		return nil, err
	}
	encoded, err := canonicalCheckpoint(record)
	if err != nil || len(encoded) > MaxCheckpointBytes {
		return nil, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "checkpoint_bytes"}
	}
	return encoded, nil
}

func DecodeRecordV1(encoded []byte) (CheckpointRecordV1, error) {
	if len(encoded) == 0 || len(encoded) > MaxCheckpointBytes {
		return CheckpointRecordV1{}, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "checkpoint_bytes"}
	}
	var wire checkpointWireV1
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || decoder.More() {
		return CheckpointRecordV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint"}
	}
	reducerDigest, err := parseDigest("reducer_set_digest", wire.ReducerSetDigest)
	if err != nil {
		return CheckpointRecordV1{}, err
	}
	eventSchemaDigest, err := parseDigest("event_schema_set_digest", wire.EventSchemaSetDigest)
	if err != nil {
		return CheckpointRecordV1{}, err
	}
	prefixDigest, err := parseDigest("event_prefix_digest", wire.EventPrefixDigest)
	if err != nil {
		return CheckpointRecordV1{}, err
	}
	projectionDigest, err := parseDigest("canonical_projection_digest", wire.CanonicalProjectionDigest)
	if err != nil {
		return CheckpointRecordV1{}, err
	}
	aggregates := make([]AggregateSnapshotV1, len(wire.Projection.Aggregates))
	for index, aggregate := range wire.Projection.Aggregates {
		stateDigest, digestErr := parseDigest("checkpoint_state_digest", aggregate.StateDigest)
		if digestErr != nil {
			return CheckpointRecordV1{}, digestErr
		}
		aggregates[index] = AggregateSnapshotV1{
			Kind: controljournal.AggregateKind(aggregate.AggregateKind), ID: controljournal.AggregateID(aggregate.AggregateID),
			LogicalVersion: aggregate.LogicalVersion, RuntimeEpochID: controljournal.RuntimeEpochID(aggregate.RuntimeEpochID),
			LastEventID: controljournal.EventID(aggregate.LastEventID), State: append([]byte(nil), aggregate.State...), StateDigest: stateDigest,
		}
	}
	record := CheckpointRecordV1{
		SchemaVersion: wire.SchemaVersion, CheckpointID: CheckpointID(wire.CheckpointID),
		ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventSchemaDigest,
		ProjectID: controljournal.ProjectID(wire.ProjectID), MissionID: controljournal.MissionID(wire.MissionID),
		Watermark:                 WatermarkV1{ThroughRecordRevision: wire.ThroughRecordRevision, ThroughEventID: controljournal.EventID(wire.ThroughEventID), EventCount: wire.EventCount, EventPrefixDigest: prefixDigest},
		CanonicalProjectionDigest: projectionDigest, UncompressedSize: wire.UncompressedSize,
		Codec: wire.Codec, Aggregates: aggregates,
		Authority: AuthoritySummaryV1{RunID: controljournal.RunID(wire.Projection.Authority.RunID), RuntimeEpochID: controljournal.RuntimeEpochID(wire.Projection.Authority.RuntimeEpochID), LeaseID: controljournal.LeaseID(wire.Projection.Authority.LeaseID), LeaseGeneration: wire.Projection.Authority.LeaseGeneration, FenceToken: wire.Projection.Authority.FenceToken, DurableRevision: wire.Projection.Authority.DurableRevision},
	}
	if err := validateRecord(record); err != nil {
		return CheckpointRecordV1{}, err
	}
	canonical, err := canonicalCheckpoint(record)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return CheckpointRecordV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_canonical_bytes"}
	}
	return record, nil
}

type checkpointHeadWireV1 struct {
	CheckpointID           string `json:"checkpoint_id"`
	CheckpointRecordDigest string `json:"checkpoint_record_digest"`
	SchemaVersion          uint32 `json:"schema_version"`
	ThroughEventID         string `json:"through_event_id"`
	ThroughRecordRevision  uint64 `json:"through_record_revision"`
}

func EncodeHeadV1(head CheckpointHeadV1) ([]byte, error) {
	if err := validateHead(head); err != nil {
		return nil, err
	}
	return json.Marshal(checkpointHeadWireV1{CheckpointID: string(head.CheckpointID), CheckpointRecordDigest: hexDigest(head.CheckpointRecordDigest), SchemaVersion: head.SchemaVersion, ThroughEventID: string(head.ThroughEventID), ThroughRecordRevision: head.ThroughRecordRevision})
}

func DecodeHeadV1(encoded []byte) (CheckpointHeadV1, error) {
	if len(encoded) == 0 || len(encoded) > controljournal.MaxOrdinaryValueBytes {
		return CheckpointHeadV1{}, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "checkpoint_head"}
	}
	var wire checkpointHeadWireV1
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return CheckpointHeadV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint_head"}
	}
	digest, err := parseDigest("checkpoint_record_digest", wire.CheckpointRecordDigest)
	if err != nil {
		return CheckpointHeadV1{}, err
	}
	head := CheckpointHeadV1{SchemaVersion: wire.SchemaVersion, CheckpointID: CheckpointID(wire.CheckpointID), CheckpointRecordDigest: digest, ThroughRecordRevision: wire.ThroughRecordRevision, ThroughEventID: controljournal.EventID(wire.ThroughEventID)}
	if err := validateHead(head); err != nil {
		return CheckpointHeadV1{}, err
	}
	canonical, _ := EncodeHeadV1(head)
	if !bytes.Equal(canonical, encoded) {
		return CheckpointHeadV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_head_canonical_bytes"}
	}
	return head, nil
}

func journalWatermark(journal controljournal.ValidatedJournalV1, forming controljournal.OperationID) (WatermarkV1, error) {
	return extendWatermark(WatermarkV1{}, journal, forming)
}

func extendWatermark(prior WatermarkV1, journal controljournal.ValidatedJournalV1, forming controljournal.OperationID) (WatermarkV1, error) {
	records, err := controljournal.SnapshotJournalV1(journal)
	length := len(records)
	if err != nil {
		return WatermarkV1{}, err
	}
	if prior.EventCount == 0 {
		if prior.ThroughRecordRevision != 0 || prior.ThroughEventID != "" || prior.EventPrefixDigest != [32]byte{} {
			return WatermarkV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "event_prefix"}
		}
	} else if prior.ThroughRecordRevision == 0 || prior.ThroughEventID == "" || prior.EventPrefixDigest == [32]byte{} {
		return WatermarkV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "event_prefix"}
	}
	digest := prior.EventPrefixDigest
	var last controljournal.StoredEventV1
	if prior.EventCount > ^uint64(0)-uint64(length) {
		return WatermarkV1{}, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "event_prefix"}
	}
	for _, record := range records {
		if forming != "" && record.Envelope.OperationID == forming {
			return WatermarkV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "current_batch_checkpoint"}
		}
		if record.RecordRevision <= prior.ThroughRecordRevision {
			return WatermarkV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "suffix_revision"}
		}
		encoded, encodeErr := controljournal.EncodeEventEnvelopeV1(record.Envelope)
		if encodeErr != nil {
			return WatermarkV1{}, encodeErr
		}
		var folded bytes.Buffer
		folded.Write(digest[:])
		var revision [8]byte
		binary.BigEndian.PutUint64(revision[:], record.RecordRevision)
		folded.Write(revision[:])
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(encoded)))
		folded.Write(size[:])
		folded.Write(encoded)
		digest = sha256.Sum256(folded.Bytes())
		last = record
	}
	if length == 0 {
		return prior, nil
	}
	return WatermarkV1{ThroughRecordRevision: last.RecordRevision, ThroughEventID: last.Envelope.EventID, EventCount: prior.EventCount + uint64(length), EventPrefixDigest: digest}, nil
}

func validateRecord(record CheckpointRecordV1) error {
	if record.SchemaVersion != SchemaVersionV1 {
		return controljournal.ValidationError{Kind: controljournal.RefusalUnsupportedVersion, Field: "checkpoint"}
	}
	if _, err := controljournal.NewRecordID(string(record.CheckpointID)); err != nil {
		return err
	}
	if err := validateSource(SourceIdentityV1{ProjectID: record.ProjectID, MissionID: record.MissionID, RunID: record.Authority.RunID, ReducerSetDigest: record.ReducerSetDigest, EventSchemaSetDigest: record.EventSchemaSetDigest}); err != nil {
		return err
	}
	if record.Codec != CodecCanonicalJSONV1 || record.Watermark.ThroughRecordRevision == 0 || record.Watermark.EventCount == 0 || record.Watermark.EventPrefixDigest == [32]byte{} || record.CanonicalProjectionDigest == [32]byte{} {
		return controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint"}
	}
	if _, err := controljournal.NewEventID(string(record.Watermark.ThroughEventID)); err != nil {
		return err
	}
	if err := validateAuthority(record.Authority); err != nil {
		return err
	}
	if record.Authority.DurableRevision != record.Watermark.ThroughRecordRevision {
		return controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_authority_revision"}
	}
	projection, err := projectionFromAggregates(record.Aggregates)
	if err != nil {
		return err
	}
	if projection.GlobalDigest() != record.CanonicalProjectionDigest {
		return controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "canonical_projection_digest"}
	}
	projectionBytes, err := json.Marshal(checkpointProjectionV1FromRecord(record))
	if err != nil || uint64(len(projectionBytes)) != record.UncompressedSize || record.UncompressedSize > MaxCheckpointBytes {
		return controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "uncompressed_size"}
	}
	return nil
}

func validateHead(head CheckpointHeadV1) error {
	if head.SchemaVersion != SchemaVersionV1 {
		return controljournal.ValidationError{Kind: controljournal.RefusalUnsupportedVersion, Field: "checkpoint_head"}
	}
	if _, err := controljournal.NewRecordID(string(head.CheckpointID)); err != nil {
		return err
	}
	if _, err := controljournal.NewEventID(string(head.ThroughEventID)); err != nil {
		return err
	}
	if head.ThroughRecordRevision == 0 || head.CheckpointRecordDigest == [32]byte{} {
		return controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint_head"}
	}
	return nil
}

func validateSource(source SourceIdentityV1) error {
	if _, err := controljournal.NewProjectID(string(source.ProjectID)); err != nil {
		return err
	}
	if _, err := controljournal.NewMissionID(string(source.MissionID)); err != nil {
		return err
	}
	if _, err := controljournal.NewRunID(string(source.RunID)); err != nil {
		return err
	}
	if source.ReducerSetDigest == [32]byte{} || source.EventSchemaSetDigest == [32]byte{} {
		return controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint_source"}
	}
	return nil
}

func checkpointProjectionV1FromRecord(record CheckpointRecordV1) checkpointProjectionV1 {
	aggregates := append([]AggregateSnapshotV1(nil), record.Aggregates...)
	sort.Slice(aggregates, func(i, j int) bool {
		if aggregates[i].Kind != aggregates[j].Kind {
			return aggregates[i].Kind < aggregates[j].Kind
		}
		return aggregates[i].ID < aggregates[j].ID
	})
	wire := checkpointProjectionV1{Aggregates: make([]aggregateWireV1, len(aggregates)), Authority: authorityWireV1{RunID: string(record.Authority.RunID), RuntimeEpochID: string(record.Authority.RuntimeEpochID), LeaseID: string(record.Authority.LeaseID), LeaseGeneration: record.Authority.LeaseGeneration, FenceToken: record.Authority.FenceToken, DurableRevision: record.Authority.DurableRevision}}
	for index, aggregate := range aggregates {
		wire.Aggregates[index] = aggregateWireV1{AggregateID: string(aggregate.ID), AggregateKind: string(aggregate.Kind), LastEventID: string(aggregate.LastEventID), LogicalVersion: aggregate.LogicalVersion, RuntimeEpochID: string(aggregate.RuntimeEpochID), State: aggregate.State, StateDigest: hexDigest(aggregate.StateDigest)}
	}
	return wire
}

func cloneAggregates(values []AggregateSnapshotV1) []AggregateSnapshotV1 {
	cloned := append([]AggregateSnapshotV1(nil), values...)
	for i := range cloned {
		cloned[i].State = append([]byte(nil), cloned[i].State...)
	}
	return cloned
}
func hexDigest(value [32]byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, 64)
	for i, b := range value {
		result[i*2], result[i*2+1] = digits[b>>4], digits[b&15]
	}
	return string(result)
}
