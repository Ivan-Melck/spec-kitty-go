package gapdb

import (
	"strings"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// v2KeyKind is the closed authority-journal key family. It is deliberately
// semantic: callers cannot smuggle a raw provider key through the v2 adapter.
type v2KeyKind string

const (
	v2KeyAuthorityBinding v2KeyKind = "authority_binding"
	v2KeyRuntimeCurrent   v2KeyKind = "runtime_current"
	v2KeyRuntimeEpoch     v2KeyKind = "runtime_epoch"
	v2KeyOperationBinding v2KeyKind = "operation_binding"
	v2KeyOperationHead    v2KeyKind = "operation_head"
	v2KeyEffectIntent     v2KeyKind = "effect_intent"
	v2KeyAggregateHead    v2KeyKind = "aggregate_head"
	v2KeyJournalEvent     v2KeyKind = "journal_event"
	v2KeyCheckpointSlot   v2KeyKind = "checkpoint_slot"
	v2KeyCheckpointRecord v2KeyKind = "checkpoint_record"
	v2KeyEligibilityGuard v2KeyKind = "eligibility_guard"
)

type v2Key struct {
	kind             v2KeyKind
	encoded          string
	runtimeEpochID   journalv2.RuntimeEpochID
	operationID      journalv2.OperationID
	effectID         journalv2.EffectID
	coordinate       journalv2.AggregateCoordinate
	logicalVersion   journalv2.LogicalVersion
	checkpointID     journalv2.CheckpointID
	checkpointSlotID journalv2.CheckpointSlotID
	missionID        journalv2.MissionID
	shardID          journalv2.EligibilityShardID
}

// v2Prefix is a closed scan coordinate. Prefix values always end at a key
// family boundary and cannot be used as an alternate spelling of a key.
type v2Prefix struct {
	kind    v2KeyKind
	encoded string
}

func (p v2Prefix) Kind() v2KeyKind { return p.kind }
func (p v2Prefix) String() string  { return p.encoded }

func (k v2Key) Kind() v2KeyKind                              { return k.kind }
func (k v2Key) String() string                               { return k.encoded }
func (k v2Key) RuntimeEpochID() journalv2.RuntimeEpochID     { return k.runtimeEpochID }
func (k v2Key) OperationID() journalv2.OperationID           { return k.operationID }
func (k v2Key) EffectID() journalv2.EffectID                 { return k.effectID }
func (k v2Key) Coordinate() journalv2.AggregateCoordinate    { return k.coordinate }
func (k v2Key) LogicalVersion() journalv2.LogicalVersion     { return k.logicalVersion }
func (k v2Key) CheckpointID() journalv2.CheckpointID         { return k.checkpointID }
func (k v2Key) CheckpointSlotID() journalv2.CheckpointSlotID { return k.checkpointSlotID }
func (k v2Key) MissionID() journalv2.MissionID               { return k.missionID }
func (k v2Key) ShardID() journalv2.EligibilityShardID        { return k.shardID }

func v2AuthorityBindingKey() v2Key {
	return v2Key{kind: v2KeyAuthorityBinding, encoded: "spk/v2/authority/binding"}
}

func v2RuntimeCurrentKey() v2Key {
	return v2Key{kind: v2KeyRuntimeCurrent, encoded: "spk/v2/runtime/current"}
}

func v2RuntimeEpochKey(id journalv2.RuntimeEpochID) (v2Key, error) {
	validated, err := journalv2.NewRuntimeEpochID(string(id))
	if err != nil {
		return v2Key{}, v2CodecError("runtime_epoch")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("runtime_epoch", "spk", "v2", "runtime", "epochs", component)
	return v2Key{kind: v2KeyRuntimeEpoch, encoded: encoded, runtimeEpochID: validated}, err
}

func v2OperationBindingKey(id journalv2.OperationID) (v2Key, error) {
	return v2OperationKey(id, v2KeyOperationBinding, "binding")
}

func v2OperationHeadKey(id journalv2.OperationID) (v2Key, error) {
	return v2OperationKey(id, v2KeyOperationHead, "head")
}

func v2OperationKey(id journalv2.OperationID, kind v2KeyKind, suffix string) (v2Key, error) {
	validated, err := journalv2.NewOperationID(string(id))
	if err != nil {
		return v2Key{}, v2CodecError("operation")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("operation", "spk", "v2", "operations", component, suffix)
	return v2Key{kind: kind, encoded: encoded, operationID: validated}, err
}

func v2EffectIntentKey(id journalv2.EffectID) (v2Key, error) {
	validated, err := journalv2.NewEffectID(string(id))
	if err != nil {
		return v2Key{}, v2CodecError("effect")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("effect", "spk", "v2", "effects", component, "intent")
	return v2Key{kind: v2KeyEffectIntent, encoded: encoded, effectID: validated}, err
}

func v2AggregateHeadKey(coordinate journalv2.AggregateCoordinate) (v2Key, error) {
	validated, err := journalv2.NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope)
	if err != nil || validated.Kind == journalv2.AggregateKind("operation") {
		return v2Key{}, v2CodecError("aggregate_head")
	}
	scope, _ := validated.Scope.CanonicalComponent()
	kind, _ := journalv2.EncodeKeyComponent(string(validated.Kind))
	id, _ := journalv2.EncodeKeyComponent(string(validated.ID))
	encoded, err := joinV2Key("aggregate_head", "spk", "v2", "heads", scope, kind, id)
	return v2Key{kind: v2KeyAggregateHead, encoded: encoded, coordinate: validated}, err
}

func v2JournalEventKey(coordinate journalv2.AggregateCoordinate, version journalv2.LogicalVersion) (v2Key, error) {
	validated, err := journalv2.NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope)
	if err != nil || validated.Kind == journalv2.AggregateKind("operation") && validated.Scope.Kind != journalv2.ScopeHost {
		return v2Key{}, v2CodecError("journal_event")
	}
	versionHex, err := version.Hex16()
	if err != nil {
		return v2Key{}, v2CodecError("logical_version")
	}
	scope, _ := validated.Scope.CanonicalComponent()
	kind, _ := journalv2.EncodeKeyComponent(string(validated.Kind))
	id, _ := journalv2.EncodeKeyComponent(string(validated.ID))
	encoded, err := joinV2Key("journal_event", "spk", "v2", "scopes", scope, "journal", kind, id, versionHex)
	return v2Key{kind: v2KeyJournalEvent, encoded: encoded, coordinate: validated, logicalVersion: version}, err
}

func v2CheckpointSlotKey(id journalv2.CheckpointSlotID) (v2Key, error) {
	validated, err := journalv2.NewCheckpointSlotID(string(id))
	if err != nil {
		return v2Key{}, v2CodecError("checkpoint_slot")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("checkpoint_slot", "spk", "v2", "checkpoints", "slots", component)
	return v2Key{kind: v2KeyCheckpointSlot, encoded: encoded, checkpointSlotID: validated}, err
}

func v2CheckpointRecordKey(id journalv2.CheckpointID) (v2Key, error) {
	validated, err := journalv2.NewCheckpointID(string(id))
	if err != nil {
		return v2Key{}, v2CodecError("checkpoint_record")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("checkpoint_record", "spk", "v2", "checkpoints", "records", component)
	return v2Key{kind: v2KeyCheckpointRecord, encoded: encoded, checkpointID: validated}, err
}

func v2EligibilityGuardKey(missionID journalv2.MissionID, shardID journalv2.EligibilityShardID) (v2Key, error) {
	mission, err := journalv2.NewMissionID(string(missionID))
	if err != nil {
		return v2Key{}, v2CodecError("eligibility_guard")
	}
	shard, err := journalv2.NewEligibilityShardID(string(shardID))
	if err != nil {
		return v2Key{}, v2CodecError("eligibility_guard")
	}
	missionComponent, _ := journalv2.EncodeKeyComponent(string(mission))
	shardComponent, _ := journalv2.EncodeKeyComponent(string(shard))
	encoded, err := joinV2Key("eligibility_guard", "spk", "v2", "workgraphs", missionComponent, "eligibility", shardComponent)
	return v2Key{kind: v2KeyEligibilityGuard, encoded: encoded, missionID: mission, shardID: shard}, err
}

func v2RuntimeEpochsPrefix() v2Prefix {
	return v2Prefix{kind: v2KeyRuntimeEpoch, encoded: "spk/v2/runtime/epochs/"}
}

func v2OperationsPrefix() v2Prefix {
	return v2Prefix{kind: v2KeyOperationBinding, encoded: "spk/v2/operations/"}
}

func v2EffectsPrefix() v2Prefix {
	return v2Prefix{kind: v2KeyEffectIntent, encoded: "spk/v2/effects/"}
}

func v2AggregateHeadsPrefix() v2Prefix {
	return v2Prefix{kind: v2KeyAggregateHead, encoded: "spk/v2/heads/"}
}

func v2JournalPrefix(scope journalv2.Scope) (v2Prefix, error) {
	validated, err := journalv2.NewScope(scope)
	if err != nil {
		return v2Prefix{}, v2CodecError("scope")
	}
	component, _ := validated.CanonicalComponent()
	encoded, err := joinV2Key("journal_prefix", "spk", "v2", "scopes", component, "journal")
	if err != nil {
		return v2Prefix{}, err
	}
	return v2Prefix{kind: v2KeyJournalEvent, encoded: encoded + "/"}, nil
}

func v2AggregateJournalPrefix(coordinate journalv2.AggregateCoordinate) (v2Prefix, error) {
	validated, err := journalv2.NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope)
	if err != nil {
		return v2Prefix{}, v2CodecError("aggregate_coordinate")
	}
	scope, _ := validated.Scope.CanonicalComponent()
	kind, _ := journalv2.EncodeKeyComponent(string(validated.Kind))
	id, _ := journalv2.EncodeKeyComponent(string(validated.ID))
	encoded, err := joinV2Key("aggregate_journal_prefix", "spk", "v2", "scopes", scope, "journal", kind, id)
	if err != nil {
		return v2Prefix{}, err
	}
	return v2Prefix{kind: v2KeyJournalEvent, encoded: encoded + "/"}, nil
}

func v2CheckpointSlotsPrefix() v2Prefix {
	return v2Prefix{kind: v2KeyCheckpointSlot, encoded: "spk/v2/checkpoints/slots/"}
}

func v2CheckpointRecordsPrefix() v2Prefix {
	return v2Prefix{kind: v2KeyCheckpointRecord, encoded: "spk/v2/checkpoints/records/"}
}

func v2EligibilityPrefix(missionID journalv2.MissionID) (v2Prefix, error) {
	validated, err := journalv2.NewMissionID(string(missionID))
	if err != nil {
		return v2Prefix{}, v2CodecError("mission")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("eligibility_prefix", "spk", "v2", "workgraphs", component, "eligibility")
	if err != nil {
		return v2Prefix{}, err
	}
	return v2Prefix{kind: v2KeyEligibilityGuard, encoded: encoded + "/"}, nil
}

func parseV2Prefix(encoded string) (v2Prefix, error) {
	if len(encoded) == 0 || len(encoded) > MaxKeyBytes || encoded[len(encoded)-1] != '/' {
		return v2Prefix{}, v2CodecError("prefix")
	}
	var candidate v2Prefix
	switch encoded {
	case v2RuntimeEpochsPrefix().encoded:
		candidate = v2RuntimeEpochsPrefix()
	case v2OperationsPrefix().encoded:
		candidate = v2OperationsPrefix()
	case v2EffectsPrefix().encoded:
		candidate = v2EffectsPrefix()
	case v2AggregateHeadsPrefix().encoded:
		candidate = v2AggregateHeadsPrefix()
	case v2CheckpointSlotsPrefix().encoded:
		candidate = v2CheckpointSlotsPrefix()
	case v2CheckpointRecordsPrefix().encoded:
		candidate = v2CheckpointRecordsPrefix()
	default:
		parts := strings.Split(encoded, "/")
		switch {
		case len(parts) == 6 && parts[2] == "scopes" && parts[4] == "journal" && parts[5] == "":
			scope, err := decodeV2Scope(parts[3])
			if err != nil {
				return v2Prefix{}, err
			}
			candidate, err = v2JournalPrefix(scope)
			if err != nil {
				return v2Prefix{}, err
			}
		case len(parts) == 8 && parts[2] == "scopes" && parts[4] == "journal" && parts[7] == "":
			coordinate, err := decodeV2Coordinate(parts[3], parts[5], parts[6])
			if err != nil {
				return v2Prefix{}, err
			}
			candidate, err = v2AggregateJournalPrefix(coordinate)
			if err != nil {
				return v2Prefix{}, err
			}
		case len(parts) == 6 && parts[2] == "workgraphs" && parts[4] == "eligibility" && parts[5] == "":
			mission, err := decodeV2Component(parts[3])
			if err != nil {
				return v2Prefix{}, v2CodecError("prefix")
			}
			candidate, err = v2EligibilityPrefix(journalv2.MissionID(mission))
			if err != nil {
				return v2Prefix{}, err
			}
		default:
			return v2Prefix{}, v2CodecError("prefix_family")
		}
	}
	if candidate.encoded != encoded {
		return v2Prefix{}, v2CodecError("canonical_prefix")
	}
	return candidate, nil
}

func parseV2Key(encoded string) (v2Key, error) {
	if len(encoded) == 0 || len(encoded) > MaxKeyBytes {
		return v2Key{}, v2CodecError("key_bytes")
	}
	parts := strings.Split(encoded, "/")
	if len(parts) < 4 || parts[0] != "spk" || parts[1] != "v2" {
		return v2Key{}, v2CodecError("key_family")
	}
	var key v2Key
	var err error
	switch {
	case encoded == "spk/v2/authority/binding":
		key = v2AuthorityBindingKey()
	case encoded == "spk/v2/runtime/current":
		key = v2RuntimeCurrentKey()
	case len(parts) == 5 && parts[2] == "runtime" && parts[3] == "epochs":
		value, decodeErr := decodeV2Component(parts[4])
		if decodeErr != nil {
			return v2Key{}, decodeErr
		}
		key, err = v2RuntimeEpochKey(journalv2.RuntimeEpochID(value))
	case len(parts) == 5 && parts[2] == "operations" && (parts[4] == "binding" || parts[4] == "head"):
		value, decodeErr := decodeV2Component(parts[3])
		if decodeErr != nil {
			return v2Key{}, decodeErr
		}
		if parts[4] == "binding" {
			key, err = v2OperationBindingKey(journalv2.OperationID(value))
		} else {
			key, err = v2OperationHeadKey(journalv2.OperationID(value))
		}
	case len(parts) == 5 && parts[2] == "effects" && parts[4] == "intent":
		value, decodeErr := decodeV2Component(parts[3])
		if decodeErr != nil {
			return v2Key{}, decodeErr
		}
		key, err = v2EffectIntentKey(journalv2.EffectID(value))
	case len(parts) == 6 && parts[2] == "heads":
		coordinate, decodeErr := decodeV2Coordinate(parts[3], parts[4], parts[5])
		if decodeErr != nil {
			return v2Key{}, decodeErr
		}
		key, err = v2AggregateHeadKey(coordinate)
	case len(parts) == 8 && parts[2] == "scopes" && parts[4] == "journal":
		coordinate, decodeErr := decodeV2Coordinate(parts[3], parts[5], parts[6])
		if decodeErr != nil {
			return v2Key{}, decodeErr
		}
		version, decodeErr := journalv2.ParseLogicalVersion(parts[7])
		if decodeErr != nil {
			return v2Key{}, v2CodecError("logical_version")
		}
		key, err = v2JournalEventKey(coordinate, version)
	case len(parts) == 5 && parts[2] == "checkpoints" && parts[3] == "slots":
		value, decodeErr := decodeV2Component(parts[4])
		if decodeErr != nil {
			return v2Key{}, decodeErr
		}
		key, err = v2CheckpointSlotKey(journalv2.CheckpointSlotID(value))
	case len(parts) == 5 && parts[2] == "checkpoints" && parts[3] == "records":
		value, decodeErr := decodeV2Component(parts[4])
		if decodeErr != nil {
			return v2Key{}, decodeErr
		}
		key, err = v2CheckpointRecordKey(journalv2.CheckpointID(value))
	case len(parts) == 6 && parts[2] == "workgraphs" && parts[4] == "eligibility":
		mission, missionErr := decodeV2Component(parts[3])
		shard, shardErr := decodeV2Component(parts[5])
		if missionErr != nil || shardErr != nil {
			return v2Key{}, v2CodecError("eligibility_guard")
		}
		key, err = v2EligibilityGuardKey(journalv2.MissionID(mission), journalv2.EligibilityShardID(shard))
	default:
		return v2Key{}, v2CodecError("key_family")
	}
	if err != nil || key.encoded != encoded {
		return v2Key{}, v2CodecError("canonical_key")
	}
	return key, nil
}

func v2KeyForRecoveryAddress(address journalv2.RecordAddressV2) (v2Key, error) {
	switch address.Kind() {
	case journalv2.AddressAggregateHead:
		return v2AggregateHeadKey(address.Coordinate())
	case journalv2.AddressJournalEvent:
		return v2JournalEventKey(address.Coordinate(), address.LogicalVersion())
	case journalv2.AddressOperationBinding:
		return v2OperationBindingKey(address.OperationID())
	case journalv2.AddressOperationHead:
		return v2OperationHeadKey(address.OperationID())
	case journalv2.AddressCheckpointRecord:
		return v2CheckpointRecordKey(address.CheckpointID())
	case journalv2.AddressCheckpointSlot:
		return v2CheckpointSlotKey(address.CheckpointSlotID())
	default:
		return v2Key{}, v2CodecError("record_address")
	}
}

func v2PrefixForScanTarget(target journalv2.ScanTargetV2) (v2Prefix, error) {
	switch target.Kind() {
	case journalv2.ScanAllAggregateHeads:
		return v2AggregateHeadsPrefix(), nil
	case journalv2.ScanAllOperationRecords:
		return v2OperationsPrefix(), nil
	case journalv2.ScanCheckpointSlots:
		return v2CheckpointSlotsPrefix(), nil
	default:
		return v2Prefix{}, v2CodecError("scan_target")
	}
}

func joinV2Key(family string, parts ...string) (string, error) {
	size := len(parts) - 1
	for _, part := range parts {
		if part == "" || strings.Contains(part, "/") || len(part) > MaxKeyBytes-size {
			return "", v2CodecError(family)
		}
		size += len(part)
	}
	if size > MaxKeyBytes {
		return "", v2CodecError("key_bytes")
	}
	return strings.Join(parts, "/"), nil
}

func decodeV2Component(encoded string) (string, error) {
	value, err := journalv2.DecodeKeyComponent(encoded)
	if err != nil || value == "." || value == ".." {
		return "", v2CodecError("key_component")
	}
	return value, nil
}

func decodeV2Coordinate(scopeComponent, kindComponent, idComponent string) (journalv2.AggregateCoordinate, error) {
	scope, err := decodeV2Scope(scopeComponent)
	if err != nil {
		return journalv2.AggregateCoordinate{}, err
	}
	kind, err := decodeV2Component(kindComponent)
	if err != nil {
		return journalv2.AggregateCoordinate{}, err
	}
	id, err := decodeV2Component(idComponent)
	if err != nil {
		return journalv2.AggregateCoordinate{}, err
	}
	coordinate, err := journalv2.NewAggregateCoordinate(journalv2.AggregateKind(kind), journalv2.AggregateID(id), scope)
	if err != nil {
		return journalv2.AggregateCoordinate{}, v2CodecError("aggregate_coordinate")
	}
	return coordinate, nil
}

func decodeV2Scope(component string) (journalv2.Scope, error) {
	parts := strings.Split(component, ":")
	if len(parts) == 0 {
		return journalv2.Scope{}, v2CodecError("scope")
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeKind(parts[0])}
	want := 0
	switch scope.Kind {
	case journalv2.ScopeHost:
		want = 1
	case journalv2.ScopeProject:
		want = 2
	case journalv2.ScopeMission, journalv2.ScopeRepository:
		want = 3
	case journalv2.ScopeWorkRevision:
		want = 4
	case journalv2.ScopeWork:
		want = 5
	default:
		return journalv2.Scope{}, v2CodecError("scope_kind")
	}
	if len(parts) != want {
		return journalv2.Scope{}, v2CodecError("scope_components")
	}
	decoded := make([]string, len(parts)-1)
	for index := range decoded {
		value, err := decodeV2Component(parts[index+1])
		if err != nil {
			return journalv2.Scope{}, v2CodecError("scope_component")
		}
		decoded[index] = value
	}
	switch scope.Kind {
	case journalv2.ScopeProject:
		scope.ProjectID = journalv2.ProjectID(decoded[0])
	case journalv2.ScopeMission:
		scope.ProjectID, scope.MissionID = journalv2.ProjectID(decoded[0]), journalv2.MissionID(decoded[1])
	case journalv2.ScopeRepository:
		scope.ProjectID, scope.RepositoryID = journalv2.ProjectID(decoded[0]), journalv2.RepositoryID(decoded[1])
	case journalv2.ScopeWorkRevision:
		scope.ProjectID, scope.MissionID, scope.WorkRevisionID = journalv2.ProjectID(decoded[0]), journalv2.MissionID(decoded[1]), journalv2.WorkRevisionID(decoded[2])
	case journalv2.ScopeWork:
		scope.ProjectID, scope.MissionID, scope.WorkRevisionID, scope.WorkID = journalv2.ProjectID(decoded[0]), journalv2.MissionID(decoded[1]), journalv2.WorkRevisionID(decoded[2]), journalv2.WorkID(decoded[3])
	}
	validated, err := journalv2.NewScope(scope)
	if err != nil {
		return journalv2.Scope{}, v2CodecError("scope")
	}
	canonical, _ := validated.CanonicalComponent()
	if canonical != component {
		return journalv2.Scope{}, v2CodecError("canonical_scope")
	}
	return validated, nil
}

func v2CodecError(field string) *Error {
	return adapterError("v2_key", "", OutcomeInvalid, RecoveryFixRequest, "invalid "+boundedText(field, 64), nil)
}
