package productionruntime

import (
	"context"
	"encoding/json"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

type observationFixture struct {
	port     *memoryAuthorityPort
	binding  AuthorityBindingV2
	registry journalv2.EventRegistryV2
	reducers journalv2.ReducerSetV2
	epoch    journalv2.RuntimeEpochID
}

func newObservationFixture(t *testing.T) observationFixture {
	t.Helper()
	f := observationFixture{
		port: newMemoryAuthorityPort(),
		binding: AuthorityBindingV2{SchemaVersion: 2, HostAuthorityID: "host-binding", DatabaseID: "database-binding",
			AdmittedTupleSHA: storegapdb.AdmittedTupleSHA256, SidecarSHA: storegapdb.AdmittedSidecarSHA256,
			WireProtocol: storegapdb.AdmittedWireVersion, StorageProtocol: storegapdb.AdmittedStorageVersion, CreatedOperationID: "operation-bind"},
		registry: journalv2.DefaultEventRegistryV2(),
	}
	if err := ensureAuthorityBinding(t.Context(), f.port, f.binding, true); err != nil {
		t.Fatal(err)
	}
	f.reducers = fixtureReducers(t, f.registry)
	recovered, err := journalrestore.RestoreV2(t.Context(), journalrestore.InputV2{
		Reader: emptyRecoveryReader{f.port}, EventRegistry: f.registry, Reducers: f.reducers,
		HostAuthorityID: f.binding.HostAuthorityID, RecoveryAttemptID: "restore-observation-fixture", Budgets: journalrestore.DefaultBudgetsV2(),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.epoch, err = admitFreshEpoch(t.Context(), f.port, f.binding, runtimeCurrentV2{}, 0, recovered, "restore-observation-fixture")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestReadOnlyObservationRejectsInvalidEpochAdmissionAnchor(t *testing.T) {
	for _, change := range []string{"missing", "wrong_content", "wrong_revision"} {
		t.Run(change, func(t *testing.T) {
			f := newObservationFixture(t)
			key := "spk/v2/runtime/epochs/" + string(f.epoch)
			record, exists := f.port.records[key]
			if !exists {
				t.Fatal("production admission did not create its immutable epoch record")
			}
			switch change {
			case "missing":
				delete(f.port.records, key)
			case "wrong_content":
				record.Value = []byte(`{"schema_version":2,"current":{}}`)
				f.port.records[key] = record
			case "wrong_revision":
				record.Revision--
				f.port.records[key] = record
			}
			before := f.port.revision
			_, _, _, err := restoreReadOnlyObservation(t.Context(), memoryObservationPort{f.port}, f.binding, f.registry, f.reducers, journalrestore.DefaultBudgetsV2())
			if err == nil {
				t.Fatal("invalid epoch admission anchor became a ready observation")
			}
			if f.port.revision != before {
				t.Fatal("refused observation mutated durable authority")
			}
		})
	}
}

func TestReadOnlyObservationRefusesEpochReplacementAfterRestore(t *testing.T) {
	f := newObservationFixture(t)
	restoredThenRotate := func(ctx context.Context, input journalrestore.InputV2) (journalrestore.ResultV2, error) {
		restored, err := journalrestore.RestoreV2(ctx, input)
		if err != nil {
			return restored, err
		}
		current, revision, err := readCurrentEpoch(ctx, f.port)
		if err != nil {
			return restored, err
		}
		// A concurrent owner replaces the epoch after the observer captured its
		// sealed journal snapshot. Use the real admission seam, not forged state.
		_, err = admitFreshEpoch(ctx, f.port, f.binding, current, revision, restored, input.RecoveryAttemptID)
		return restored, err
	}
	_, _, _, err := restoreReadOnlyObservationWith(t.Context(), memoryObservationPort{f.port}, f.binding, f.registry, f.reducers, journalrestore.DefaultBudgetsV2(), restoredThenRotate)
	if err == nil {
		t.Fatal("observer served a ready snapshot across a concurrent epoch replacement")
	}
	current, _, err := readCurrentEpoch(t.Context(), f.port)
	if err != nil || current.RuntimeEpochID == f.epoch {
		t.Fatalf("fixture did not replace the epoch: current=%+v err=%v", current, err)
	}
}

func TestReadOnlyObservationRefusesPublicationAfterRestore(t *testing.T) {
	f := newObservationFixture(t)
	restoredThenPublish := func(ctx context.Context, input journalrestore.InputV2) (journalrestore.ResultV2, error) {
		restored, err := journalrestore.RestoreV2(ctx, input)
		if err != nil {
			return restored, err
		}
		// Owner metadata is outside the product journal, but advancing its
		// provider revision still invalidates the observer's stable coordinate.
		_, err = f.port.ConditionalMutate(ctx, "operation-after-observation", storegapdb.Mutation{
			Kind: storegapdb.MutationPut, Key: "observation-test-provider-metadata",
			Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: []byte(`{"published":true}`),
		}, storegapdb.AckDurable)
		return restored, err
	}
	_, _, _, err := restoreReadOnlyObservationWith(t.Context(), memoryObservationPort{f.port}, f.binding, f.registry, f.reducers, journalrestore.DefaultBudgetsV2(), restoredThenPublish)
	if err == nil {
		t.Fatal("observer served a ready snapshot after its captured provider revision changed")
	}
}

func TestReadOnlyObservationRejectsAdmissionDigestMismatch(t *testing.T) {
	for _, phase := range []string{"admission_revision", "after_metadata_publication"} {
		t.Run(phase, func(t *testing.T) {
			f := newObservationFixture(t)
			current, revision, err := readCurrentEpoch(t.Context(), f.port)
			if err != nil {
				t.Fatal(err)
			}
			current.ProjectionSHA256 = repeatByte('a', 64)
			currentBytes, _ := json.Marshal(current)
			currentBytes, _ = journalv2.CanonicalObject(currentBytes)
			f.port.records[runtimeCurrentKey] = storegapdb.Record{Key: runtimeCurrentKey, Value: currentBytes, Revision: revision}
			// Agreement between owner records is not proof that their digest
			// corresponds to the admitted product prefix.
			epochBytes, _ := json.Marshal(struct {
				SchemaVersion uint32           `json:"schema_version"`
				Current       runtimeCurrentV2 `json:"current"`
			}{2, current})
			epochBytes, _ = journalv2.CanonicalObject(epochBytes)
			key := "spk/v2/runtime/epochs/" + string(f.epoch)
			f.port.records[key] = storegapdb.Record{Key: key, Value: epochBytes, Revision: revision}
			if phase == "after_metadata_publication" {
				if _, err := f.port.ConditionalMutate(t.Context(), "operation-digest-regression", storegapdb.Mutation{
					Kind: storegapdb.MutationPut, Key: "observation-test-provider-metadata",
					Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: []byte(`{"published":true}`),
				}, storegapdb.AckDurable); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := restoreReadOnlyObservation(t.Context(), memoryObservationPort{f.port}, f.binding, f.registry, f.reducers, journalrestore.DefaultBudgetsV2()); err == nil {
				t.Fatal("admission digest mismatch became a ready observation")
			}
		})
	}
}
