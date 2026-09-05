package productionruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

const (
	authorityBindingKey = "spk/v2/authority/binding"
	runtimeCurrentKey   = "spk/v2/runtime/current"
)

type AuthorityBindingV2 struct {
	SchemaVersion      uint32                    `json:"schema_version"`
	HostAuthorityID    journalv2.HostAuthorityID `json:"host_authority_id"`
	DatabaseID         journalv2.DatabaseID      `json:"database_id"`
	AdmittedTupleSHA   string                    `json:"admitted_tuple_sha256"`
	SidecarSHA         string                    `json:"sidecar_sha256"`
	WireProtocol       uint64                    `json:"wire_protocol"`
	StorageProtocol    uint64                    `json:"storage_protocol"`
	CreatedOperationID string                    `json:"created_operation_id"`
}

func (b AuthorityBindingV2) canonical() ([]byte, error) {
	if b.SchemaVersion != 2 || b.HostAuthorityID == "" || b.DatabaseID == "" || !hexDigest(b.AdmittedTupleSHA) || !hexDigest(b.SidecarSHA) || b.WireProtocol == 0 || b.StorageProtocol == 0 || b.CreatedOperationID == "" {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "authority_binding"}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(raw)
}

type authorityPort interface {
	Get(context.Context, string) (storegapdb.Record, error)
	ConditionalMutate(context.Context, string, storegapdb.Mutation, storegapdb.AckMode) (storegapdb.MutationResult, error)
	AdmitRuntimeEpoch(context.Context, storegapdb.RuntimeEpochBatch) (storegapdb.MutationResult, error)
	Inspect(context.Context) (storegapdb.Inspection, error)
}

func ensureAuthorityBinding(ctx context.Context, port authorityPort, expected AuthorityBindingV2, initialize bool) error {
	canonical, err := expected.canonical()
	if err != nil {
		return err
	}
	record, readErr := port.Get(ctx, authorityBindingKey)
	if readErr == nil {
		if record.Revision == 0 || !bytes.Equal(record.Value, canonical) {
			return &Error{Kind: ErrorStartup, State: HostRecovering, Field: "authority_binding_mismatch"}
		}
		return nil
	}
	if !storegapdb.IsKind(readErr, storegapdb.OutcomeNotFound) || !initialize {
		return &Error{Kind: ErrorStartup, State: HostRecovering, Field: "authority_binding_missing"}
	}
	result, mutateErr := port.ConditionalMutate(ctx, expected.CreatedOperationID, storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: authorityBindingKey, Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: canonical}, storegapdb.AckDurable)
	if mutateErr != nil {
		// A racing owner may have installed the same immutable binding. Compare
		// before accepting; never overwrite or select the healthier database.
		record, readErr = port.Get(ctx, authorityBindingKey)
		if readErr != nil || !bytes.Equal(record.Value, canonical) {
			return &Error{Kind: ErrorOwnerConflict, State: HostRecovering, Field: "authority_binding_create"}
		}
		return nil
	}
	if result.Revision == 0 || result.Ack != storegapdb.AckDurable || result.DurableThrough < result.Revision || result.MutationCount != 1 {
		return &Error{Kind: ErrorStartup, State: HostRecovering, Field: "authority_binding_ack"}
	}
	return nil
}

type runtimeCurrentV2 struct {
	SchemaVersion          uint32                    `json:"schema_version"`
	RuntimeEpochID         journalv2.RuntimeEpochID  `json:"runtime_epoch_id"`
	PredecessorEpochID     journalv2.RuntimeEpochID  `json:"predecessor_runtime_epoch_id,omitempty"`
	HostAuthorityID        journalv2.HostAuthorityID `json:"host_authority_id"`
	DatabaseID             journalv2.DatabaseID      `json:"database_id"`
	RecoveredStoreRevision uint64                    `json:"recovered_store_revision"`
	ProjectionSHA256       string                    `json:"projection_sha256"`
	CreatedOperationID     string                    `json:"created_operation_id"`
	RecoveryAttemptID      string                    `json:"recovery_attempt_id"`
	Status                 string                    `json:"status"`
}

func readCurrentEpoch(ctx context.Context, port authorityPort) (runtimeCurrentV2, uint64, error) {
	record, err := port.Get(ctx, runtimeCurrentKey)
	if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		return runtimeCurrentV2{}, 0, nil
	}
	if err != nil || record.Revision == 0 {
		return runtimeCurrentV2{}, 0, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "runtime_current_read"}
	}
	var current runtimeCurrentV2
	if json.Unmarshal(record.Value, &current) != nil || current.SchemaVersion != 2 || current.RuntimeEpochID == "" || current.DatabaseID == "" || current.Status != "ready" || !hexDigest(current.ProjectionSHA256) {
		return runtimeCurrentV2{}, 0, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "runtime_current_malformed"}
	}
	canonical, canonicalErr := journalv2.CanonicalObject(record.Value)
	if canonicalErr != nil || !bytes.Equal(canonical, record.Value) {
		return runtimeCurrentV2{}, 0, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "runtime_current_noncanonical"}
	}
	return current, record.Revision, nil
}

func admitFreshEpoch(ctx context.Context, port authorityPort, binding AuthorityBindingV2, prior runtimeCurrentV2, priorRevision uint64, recovered journalrestore.ResultV2, attemptID string) (journalv2.RuntimeEpochID, error) {
	request, err := recovered.FreshEpochAdmissionRequestV2()
	if err != nil || request.DatabaseID() != binding.DatabaseID || request.HostAuthorityID() != binding.HostAuthorityID || request.RecoveryAttemptID() != attemptID {
		return "", &Error{Kind: ErrorStartup, State: HostRecovering, Field: "restore_admission"}
	}
	epoch, err := randomIdentity[journalv2.RuntimeEpochID]("epoch-")
	if err != nil {
		return "", err
	}
	operationID, err := randomIdentity[string]("runtime-admit-")
	if err != nil {
		return "", err
	}
	// The batch's provider revision does not exist until GapDB acknowledges the
	// commit, so it cannot honestly be embedded in a value inside that batch.
	// Record the exact recovered product prefix consumed by admission. The
	// runtime/current record's own revision is the post-admission coordinate.
	current := runtimeCurrentV2{SchemaVersion: 2, RuntimeEpochID: epoch, PredecessorEpochID: prior.RuntimeEpochID, HostAuthorityID: binding.HostAuthorityID, DatabaseID: binding.DatabaseID, RecoveredStoreRevision: request.ThroughRecordRevision(), ProjectionSHA256: request.CanonicalProjectionDigest().String(), CreatedOperationID: operationID, RecoveryAttemptID: attemptID, Status: "ready"}
	currentRaw, _ := json.Marshal(current)
	currentBytes, err := journalv2.CanonicalObject(currentRaw)
	if err != nil {
		return "", err
	}
	epochRecord := struct {
		SchemaVersion uint32           `json:"schema_version"`
		Current       runtimeCurrentV2 `json:"current"`
	}{2, current}
	epochRaw, _ := json.Marshal(epochRecord)
	epochBytes, _ := journalv2.CanonicalObject(epochRaw)
	operationBytes, _ := journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"operation_id":%q,"runtime_epoch_id":%q,"schema_version":2,"status":"resolved"}`, operationID, epoch)))
	eventBytes, _ := journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"event_type":"runtime_host.epoch_admitted","operation_id":%q,"runtime_epoch_id":%q,"schema_version":2}`, operationID, epoch)))
	headBytes, _ := journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"runtime_epoch_id":%q,"schema_version":2,"status":"ready"}`, epoch)))
	currentCondition := storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
	headCondition := storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
	if priorRevision > 0 {
		currentCondition = storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: priorRevision}
		// The private admission head is written at the same provider revision as
		// runtime/current, so its previous revision is the current record's.
		headCondition = storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: priorRevision}
	}
	batch := storegapdb.RuntimeEpochBatch{
		OperationID:    operationID,
		Operation:      storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/runtime-admission/v2/operations/" + operationID, Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: operationBytes},
		Event:          storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/runtime-admission/v2/events/" + string(epoch), Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: eventBytes},
		AggregateHead:  storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/runtime-admission/v2/head", Condition: headCondition, Value: headBytes},
		EpochRecord:    storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/v2/runtime/epochs/" + string(epoch), Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: epochBytes},
		CurrentBinding: storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: runtimeCurrentKey, Condition: currentCondition, Value: currentBytes},
	}
	result, err := port.AdmitRuntimeEpoch(ctx, batch)
	if err != nil || result.Revision <= request.ThroughRecordRevision() || result.Ack != storegapdb.AckDurable || result.DurableThrough < result.Revision || result.MutationCount != 5 {
		return "", &Error{Kind: ErrorStartup, State: HostRecovering, Field: "runtime_epoch_admission"}
	}
	return epoch, nil
}

func randomIdentity[T ~string](prefix string) (T, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", &Error{Kind: ErrorStartup, State: HostRecovering, Field: "identity_entropy"}
	}
	return T(prefix + hex.EncodeToString(value[:])), nil
}

func hexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == string(bytes.ToLower([]byte(value)))
}

// RuntimeHostStateV2 is the pure event payload used by the closed runtime_host
// event family. Runtime admission itself is performed by the owner-only batch;
// playback of this family performs no I/O.
type RuntimeHostStateV2 struct {
	SchemaVersion      uint32                    `json:"schema_version"`
	Lifecycle          HostState                 `json:"lifecycle"`
	RuntimeEpochID     journalv2.RuntimeEpochID  `json:"runtime_epoch_id"`
	PredecessorEpochID journalv2.RuntimeEpochID  `json:"predecessor_runtime_epoch_id,omitempty"`
	HostAuthorityID    journalv2.HostAuthorityID `json:"host_authority_id"`
	DatabaseID         journalv2.DatabaseID      `json:"database_id"`
}

func ReduceRuntimeHostEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	if event.Family() != "runtime_host" || event.EventSchemaVersion() != 1 || event.ReducerVersion() != 1 || event.Coordinate().Kind != "runtime_host" {
		return nil, &Error{Kind: ErrorProjection, State: HostRecovering, Field: "runtime_event_tuple"}
	}
	var next RuntimeHostStateV2
	if json.Unmarshal(event.Payload(), &next) != nil || next.SchemaVersion != 2 || next.RuntimeEpochID != event.CommittedRuntimeEpochID() || next.DatabaseID == "" || next.HostAuthorityID == "" {
		return nil, &Error{Kind: ErrorProjection, State: HostRecovering, Field: "runtime_event_payload"}
	}
	if len(prior) != 0 && !bytes.Equal(prior, []byte(`{}`)) {
		var previous RuntimeHostStateV2
		if json.Unmarshal(prior, &previous) != nil || previous.RuntimeEpochID != next.PredecessorEpochID || previous.RuntimeEpochID == next.RuntimeEpochID {
			return nil, &Error{Kind: ErrorProjection, State: HostRecovering, Field: "runtime_event_predecessor"}
		}
	}
	return journalv2.CanonicalObject(event.Payload())
}

func RuntimeReducerRegistrationV2() journalv2.ReducerRegistration {
	digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"implementation":"production-runtime-host-v2"}`))
	return journalv2.ReducerRegistration{Family: "runtime_host", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/productionruntime", ImplementationDigest: digest, Reduce: ReduceRuntimeHostEventV2}
}

func canonicalRegistrations(registrations []journalv2.ReducerRegistration) []journalv2.ReducerRegistration {
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].Family < registrations[j].Family })
	return registrations
}
