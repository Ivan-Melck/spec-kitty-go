package gapdb

import (
	"strings"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// V2KeyKind is the closed authority-journal key family. It is deliberately
// semantic: callers cannot smuggle a raw provider key through the v2 adapter.
type V2KeyKind string

const (
	V2KeyAuthorityBinding V2KeyKind = "authority_binding"
	V2KeyRuntimeCurrent   V2KeyKind = "runtime_current"
	V2KeyRuntimeEpoch     V2KeyKind = "runtime_epoch"
	V2KeyOperationBinding V2KeyKind = "operation_binding"
	V2KeyOperationHead    V2KeyKind = "operation_head"
	V2KeyEffectIntent     V2KeyKind = "effect_intent"
	V2KeyAggregateHead    V2KeyKind = "aggregate_head"
	V2KeyJournalEvent     V2KeyKind = "journal_event"
	V2KeyCheckpointSlot   V2KeyKind = "checkpoint_slot"
	V2KeyCheckpointRecord V2KeyKind = "checkpoint_record"
	V2KeyEligibilityGuard V2KeyKind = "eligibility_guard"
)

type V2Key struct {
	kind             V2KeyKind
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

// V2Prefix is a closed scan coordinate. Prefix values always end at a key
// family boundary and cannot be used as an alternate spelling of a key.
type V2Prefix struct {
	kind    V2KeyKind
	encoded string
}

func (p V2Prefix) Kind() V2KeyKind { return p.kind }
func (p V2Prefix) String() string  { return p.encoded }

func (k V2Key) Kind() V2KeyKind                              { return k.kind }
func (k V2Key) String() string                               { return k.encoded }
func (k V2Key) RuntimeEpochID() journalv2.RuntimeEpochID     { return k.runtimeEpochID }
func (k V2Key) OperationID() journalv2.OperationID           { return k.operationID }
func (k V2Key) EffectID() journalv2.EffectID                 { return k.effectID }
func (k V2Key) Coordinate() journalv2.AggregateCoordinate    { return k.coordinate }
func (k V2Key) LogicalVersion() journalv2.LogicalVersion     { return k.logicalVersion }
func (k V2Key) CheckpointID() journalv2.CheckpointID         { return k.checkpointID }
func (k V2Key) CheckpointSlotID() journalv2.CheckpointSlotID { return k.checkpointSlotID }
func (k V2Key) MissionID() journalv2.MissionID               { return k.missionID }
func (k V2Key) ShardID() journalv2.EligibilityShardID        { return k.shardID }

func V2AuthorityBindingKey() V2Key {
	return V2Key{kind: V2KeyAuthorityBinding, encoded: "spk/v2/authority/binding"}
}

func V2RuntimeCurrentKey() V2Key {
	return V2Key{kind: V2KeyRuntimeCurrent, encoded: "spk/v2/runtime/current"}
}

func V2RuntimeEpochKey(id journalv2.RuntimeEpochID) (V2Key, error) {
	validated, err := journalv2.NewRuntimeEpochID(string(id))
	if err != nil {
		return V2Key{}, v2CodecError("runtime_epoch")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("runtime_epoch", "spk", "v2", "runtime", "epochs", component)
	return V2Key{kind: V2KeyRuntimeEpoch, encoded: encoded, runtimeEpochID: validated}, err
}

func V2OperationBindingKey(id journalv2.OperationID) (V2Key, error) {
	return v2OperationKey(id, V2KeyOperationBinding, "binding")
}

func V2OperationHeadKey(id journalv2.OperationID) (V2Key, error) {
	return v2OperationKey(id, V2KeyOperationHead, "head")
}

func v2OperationKey(id journalv2.OperationID, kind V2KeyKind, suffix string) (V2Key, error) {
	validated, err := journalv2.NewOperationID(string(id))
	if err != nil {
		return V2Key{}, v2CodecError("operation")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("operation", "spk", "v2", "operations", component, suffix)
	return V2Key{kind: kind, encoded: encoded, operationID: validated}, err
}

func V2EffectIntentKey(id journalv2.EffectID) (V2Key, error) {
	validated, err := journalv2.NewEffectID(string(id))
	if err != nil {
		return V2Key{}, v2CodecError("effect")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("effect", "spk", "v2", "effects", component, "intent")
	return V2Key{kind: V2KeyEffectIntent, encoded: encoded, effectID: validated}, err
}

func V2AggregateHeadKey(coordinate journalv2.AggregateCoordinate) (V2Key, error) {
	validated, err := journalv2.NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope)
	if err != nil {
		return V2Key{}, v2CodecError("aggregate_head")
	}
	scope, _ := validated.Scope.CanonicalComponent()
	kind, _ := journalv2.EncodeKeyComponent(string(validated.Kind))
	id, _ := journalv2.EncodeKeyComponent(string(validated.ID))
	encoded, err := joinV2Key("aggregate_head", "spk", "v2", "scopes", scope, "heads", kind, id)
	return V2Key{kind: V2KeyAggregateHead, encoded: encoded, coordinate: validated}, err
}

func V2JournalEventKey(coordinate journalv2.AggregateCoordinate, version journalv2.LogicalVersion) (V2Key, error) {
	validated, err := journalv2.NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope)
	if err != nil {
		return V2Key{}, v2CodecError("journal_event")
	}
	versionHex, err := version.Hex16()
	if err != nil {
		return V2Key{}, v2CodecError("logical_version")
	}
	scope, _ := validated.Scope.CanonicalComponent()
	kind, _ := journalv2.EncodeKeyComponent(string(validated.Kind))
	id, _ := journalv2.EncodeKeyComponent(string(validated.ID))
	encoded, err := joinV2Key("journal_event", "spk", "v2", "scopes", scope, "journal", kind, id, versionHex)
	return V2Key{kind: V2KeyJournalEvent, encoded: encoded, coordinate: validated, logicalVersion: version}, err
}

func V2CheckpointSlotKey(id journalv2.CheckpointSlotID) (V2Key, error) {
	validated, err := journalv2.NewCheckpointSlotID(string(id))
	if err != nil {
		return V2Key{}, v2CodecError("checkpoint_slot")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("checkpoint_slot", "spk", "v2", "checkpoints", "slots", component)
	return V2Key{kind: V2KeyCheckpointSlot, encoded: encoded, checkpointSlotID: validated}, err
}

func V2CheckpointRecordKey(id journalv2.CheckpointID) (V2Key, error) {
	validated, err := journalv2.NewCheckpointID(string(id))
	if err != nil {
		return V2Key{}, v2CodecError("checkpoint_record")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("checkpoint_record", "spk", "v2", "checkpoints", "records", component)
	return V2Key{kind: V2KeyCheckpointRecord, encoded: encoded, checkpointID: validated}, err
}

func V2EligibilityGuardKey(missionID journalv2.MissionID, shardID journalv2.EligibilityShardID) (V2Key, error) {
	mission, err := journalv2.NewMissionID(string(missionID))
	if err != nil {
		return V2Key{}, v2CodecError("eligibility_guard")
	}
	shard, err := journalv2.NewEligibilityShardID(string(shardID))
	if err != nil {
		return V2Key{}, v2CodecError("eligibility_guard")
	}
	missionComponent, _ := journalv2.EncodeKeyComponent(string(mission))
	shardComponent, _ := journalv2.EncodeKeyComponent(string(shard))
	encoded, err := joinV2Key("eligibility_guard", "spk", "v2", "workgraphs", missionComponent, "eligibility", shardComponent)
	return V2Key{kind: V2KeyEligibilityGuard, encoded: encoded, missionID: mission, shardID: shard}, err
}

func V2RuntimeEpochsPrefix() V2Prefix {
	return V2Prefix{kind: V2KeyRuntimeEpoch, encoded: "spk/v2/runtime/epochs/"}
}

func V2OperationsPrefix() V2Prefix {
	return V2Prefix{kind: V2KeyOperationBinding, encoded: "spk/v2/operations/"}
}

func V2EffectsPrefix() V2Prefix {
	return V2Prefix{kind: V2KeyEffectIntent, encoded: "spk/v2/effects/"}
}

func V2AggregateHeadsPrefix(scope journalv2.Scope) (V2Prefix, error) {
	validated, err := journalv2.NewScope(scope)
	if err != nil {
		return V2Prefix{}, v2CodecError("scope")
	}
	component, _ := validated.CanonicalComponent()
	encoded, err := joinV2Key("aggregate_heads_prefix", "spk", "v2", "scopes", component, "heads")
	if err != nil {
		return V2Prefix{}, err
	}
	return V2Prefix{kind: V2KeyAggregateHead, encoded: encoded + "/"}, nil
}

func V2JournalPrefix(scope journalv2.Scope) (V2Prefix, error) {
	validated, err := journalv2.NewScope(scope)
	if err != nil {
		return V2Prefix{}, v2CodecError("scope")
	}
	component, _ := validated.CanonicalComponent()
	encoded, err := joinV2Key("journal_prefix", "spk", "v2", "scopes", component, "journal")
	if err != nil {
		return V2Prefix{}, err
	}
	return V2Prefix{kind: V2KeyJournalEvent, encoded: encoded + "/"}, nil
}

func V2AggregateJournalPrefix(coordinate journalv2.AggregateCoordinate) (V2Prefix, error) {
	validated, err := journalv2.NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope)
	if err != nil {
		return V2Prefix{}, v2CodecError("aggregate_coordinate")
	}
	scope, _ := validated.Scope.CanonicalComponent()
	kind, _ := journalv2.EncodeKeyComponent(string(validated.Kind))
	id, _ := journalv2.EncodeKeyComponent(string(validated.ID))
	encoded, err := joinV2Key("aggregate_journal_prefix", "spk", "v2", "scopes", scope, "journal", kind, id)
	if err != nil {
		return V2Prefix{}, err
	}
	return V2Prefix{kind: V2KeyJournalEvent, encoded: encoded + "/"}, nil
}

func V2CheckpointSlotsPrefix() V2Prefix {
	return V2Prefix{kind: V2KeyCheckpointSlot, encoded: "spk/v2/checkpoints/slots/"}
}

func V2CheckpointRecordsPrefix() V2Prefix {
	return V2Prefix{kind: V2KeyCheckpointRecord, encoded: "spk/v2/checkpoints/records/"}
}

func V2EligibilityPrefix(missionID journalv2.MissionID) (V2Prefix, error) {
	validated, err := journalv2.NewMissionID(string(missionID))
	if err != nil {
		return V2Prefix{}, v2CodecError("mission")
	}
	component, _ := journalv2.EncodeKeyComponent(string(validated))
	encoded, err := joinV2Key("eligibility_prefix", "spk", "v2", "workgraphs", component, "eligibility")
	if err != nil {
		return V2Prefix{}, err
	}
	return V2Prefix{kind: V2KeyEligibilityGuard, encoded: encoded + "/"}, nil
}

func ParseV2Prefix(encoded string) (V2Prefix, error) {
	if len(encoded) == 0 || len(encoded) > MaxKeyBytes || encoded[len(encoded)-1] != '/' {
		return V2Prefix{}, v2CodecError("prefix")
	}
	var candidate V2Prefix
	switch encoded {
	case V2RuntimeEpochsPrefix().encoded:
		candidate = V2RuntimeEpochsPrefix()
	case V2OperationsPrefix().encoded:
		candidate = V2OperationsPrefix()
	case V2EffectsPrefix().encoded:
		candidate = V2EffectsPrefix()
	case V2CheckpointSlotsPrefix().encoded:
		candidate = V2CheckpointSlotsPrefix()
	case V2CheckpointRecordsPrefix().encoded:
		candidate = V2CheckpointRecordsPrefix()
	default:
		parts := strings.Split(encoded, "/")
		switch {
		case len(parts) == 6 && parts[2] == "scopes" && parts[4] == "heads" && parts[5] == "":
			scope, err := decodeV2Scope(parts[3])
			if err != nil {
				// A heads prefix stops before aggregate kind; the empty
				// component comes only from the required trailing slash.
				return V2Prefix{}, v2CodecError("prefix")
			}
			candidate, err = V2AggregateHeadsPrefix(scope)
			if err != nil {
				return V2Prefix{}, err
			}
		case len(parts) == 6 && parts[2] == "scopes" && parts[4] == "journal" && parts[5] == "":
			scope, err := decodeV2Scope(parts[3])
			if err != nil {
				return V2Prefix{}, err
			}
			candidate, err = V2JournalPrefix(scope)
			if err != nil {
				return V2Prefix{}, err
			}
		case len(parts) == 8 && parts[2] == "scopes" && parts[4] == "journal" && parts[7] == "":
			coordinate, err := decodeV2Coordinate(parts[3], parts[5], parts[6])
			if err != nil {
				return V2Prefix{}, err
			}
			candidate, err = V2AggregateJournalPrefix(coordinate)
			if err != nil {
				return V2Prefix{}, err
			}
		case len(parts) == 6 && parts[2] == "workgraphs" && parts[4] == "eligibility" && parts[5] == "":
			mission, err := decodeV2Component(parts[3])
			if err != nil {
				return V2Prefix{}, v2CodecError("prefix")
			}
			candidate, err = V2EligibilityPrefix(journalv2.MissionID(mission))
			if err != nil {
				return V2Prefix{}, err
			}
		default:
			return V2Prefix{}, v2CodecError("prefix_family")
		}
	}
	if candidate.encoded != encoded {
		return V2Prefix{}, v2CodecError("canonical_prefix")
	}
	return candidate, nil
}

func ParseV2Key(encoded string) (V2Key, error) {
	if len(encoded) == 0 || len(encoded) > MaxKeyBytes {
		return V2Key{}, v2CodecError("key_bytes")
	}
	parts := strings.Split(encoded, "/")
	if len(parts) < 4 || parts[0] != "spk" || parts[1] != "v2" {
		return V2Key{}, v2CodecError("key_family")
	}
	var key V2Key
	var err error
	switch {
	case encoded == "spk/v2/authority/binding":
		key = V2AuthorityBindingKey()
	case encoded == "spk/v2/runtime/current":
		key = V2RuntimeCurrentKey()
	case len(parts) == 5 && parts[2] == "runtime" && parts[3] == "epochs":
		value, decodeErr := decodeV2Component(parts[4])
		if decodeErr != nil {
			return V2Key{}, decodeErr
		}
		key, err = V2RuntimeEpochKey(journalv2.RuntimeEpochID(value))
	case len(parts) == 5 && parts[2] == "operations" && (parts[4] == "binding" || parts[4] == "head"):
		value, decodeErr := decodeV2Component(parts[3])
		if decodeErr != nil {
			return V2Key{}, decodeErr
		}
		if parts[4] == "binding" {
			key, err = V2OperationBindingKey(journalv2.OperationID(value))
		} else {
			key, err = V2OperationHeadKey(journalv2.OperationID(value))
		}
	case len(parts) == 5 && parts[2] == "effects" && parts[4] == "intent":
		value, decodeErr := decodeV2Component(parts[3])
		if decodeErr != nil {
			return V2Key{}, decodeErr
		}
		key, err = V2EffectIntentKey(journalv2.EffectID(value))
	case len(parts) == 7 && parts[2] == "scopes" && parts[4] == "heads":
		coordinate, decodeErr := decodeV2Coordinate(parts[3], parts[5], parts[6])
		if decodeErr != nil {
			return V2Key{}, decodeErr
		}
		key, err = V2AggregateHeadKey(coordinate)
	case len(parts) == 8 && parts[2] == "scopes" && parts[4] == "journal":
		coordinate, decodeErr := decodeV2Coordinate(parts[3], parts[5], parts[6])
		if decodeErr != nil {
			return V2Key{}, decodeErr
		}
		version, decodeErr := journalv2.ParseLogicalVersion(parts[7])
		if decodeErr != nil {
			return V2Key{}, v2CodecError("logical_version")
		}
		key, err = V2JournalEventKey(coordinate, version)
	case len(parts) == 5 && parts[2] == "checkpoints" && parts[3] == "slots":
		value, decodeErr := decodeV2Component(parts[4])
		if decodeErr != nil {
			return V2Key{}, decodeErr
		}
		key, err = V2CheckpointSlotKey(journalv2.CheckpointSlotID(value))
	case len(parts) == 5 && parts[2] == "checkpoints" && parts[3] == "records":
		value, decodeErr := decodeV2Component(parts[4])
		if decodeErr != nil {
			return V2Key{}, decodeErr
		}
		key, err = V2CheckpointRecordKey(journalv2.CheckpointID(value))
	case len(parts) == 6 && parts[2] == "workgraphs" && parts[4] == "eligibility":
		mission, missionErr := decodeV2Component(parts[3])
		shard, shardErr := decodeV2Component(parts[5])
		if missionErr != nil || shardErr != nil {
			return V2Key{}, v2CodecError("eligibility_guard")
		}
		key, err = V2EligibilityGuardKey(journalv2.MissionID(mission), journalv2.EligibilityShardID(shard))
	default:
		return V2Key{}, v2CodecError("key_family")
	}
	if err != nil || key.encoded != encoded {
		return V2Key{}, v2CodecError("canonical_key")
	}
	return key, nil
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
