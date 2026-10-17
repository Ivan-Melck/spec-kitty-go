package playback

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
)

// Construction is deliberately outside the measured interval. The history is
// byte-oversized, so neither reducers nor their allocations participate.
func TestPlaybackBudgetPreflightAllocation(t *testing.T) {
	versions, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	const count = 4096
	payload := []byte(`{"value":"` + strings.Repeat("a", 32<<10) + `"}`)
	records := make([]controljournal.StoredEventV1, count)
	for i := range records {
		envelope, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
			EnvelopeVersion: 1, EventID: controljournal.EventID("event-" + decimal(i+1)),
			EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1",
			ProjectID: "project-1", AggregateKind: "mission", AggregateID: "aggregate-1",
			AggregateLogicalVersion: uint64(i + 1), OperationID: controljournal.OperationID("operation-" + decimal(i+1)),
			RuntimeEpochID: "epoch-1", EventCount: 1, Payload: payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		records[i] = controljournal.StoredEventV1{RecordRevision: uint64(i + 1), Envelope: envelope}
	}
	journal, err := controljournal.ValidateJournalV1(versions, records)
	if err != nil {
		t.Fatal(err)
	}
	records = nil
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result, err := RunV1(InputV1{Journal: journal})
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(journal)
	var refusal controljournal.ValidationError
	if !errors.As(err, &refusal) || refusal.Kind != controljournal.RefusalBudgetExceeded || refusal.Field != "playback_bytes" || !reflect.DeepEqual(result, ResultV1{}) {
		t.Fatalf("budget result=%+v error=%T %v", result, err, err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	// Encoding the bounded 48 MiB prefix legitimately uses several temporary
	// buffers. The extra 128 MiB full-history copy must not precede refusal.
	const ceiling = 340 << 20
	t.Logf("budget preflight allocated=%d ceiling=%d", allocated, ceiling)
	if allocated > ceiling {
		t.Fatalf("cloned full history before budget refusal: allocated=%d ceiling=%d", allocated, ceiling)
	}
}

func TestPlaybackPreservesEnvelopeBudgetRefusal(t *testing.T) {
	versions, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: "event-1", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1",
		ProjectID: "project-1", AggregateKind: "mission", AggregateID: "aggregate-1", AggregateLogicalVersion: 1,
		OperationID: "operation-1", RuntimeEpochID: "epoch-1", EventCount: 1,
		Payload: []byte(`{"value":"` + strings.Repeat("a", controljournal.MaxOrdinaryValueBytes-12) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := controljournal.ValidateJournalV1(versions, []controljournal.StoredEventV1{{RecordRevision: 1, Envelope: envelope}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunV1(InputV1{Journal: journal})
	var refusal controljournal.ValidationError
	if !errors.As(err, &refusal) || refusal.Kind != controljournal.RefusalBudgetExceeded || refusal.Field != "event_envelope" || !reflect.DeepEqual(result, ResultV1{}) {
		t.Fatalf("codec refusal was remapped: result=%+v error=%T %v", result, err, err)
	}
}

func TestPlaybackEventBudgetBeforeMissingReducer(t *testing.T) {
	journal := playbackJournal(t, maxPlaybackEvents+1)
	result, err := RunV1(InputV1{Journal: journal})
	var refusal controljournal.ValidationError
	if !errors.As(err, &refusal) || refusal.Kind != controljournal.RefusalBudgetExceeded || refusal.Field != "playback_events" || !reflect.DeepEqual(result, ResultV1{}) {
		t.Fatalf("event budget result=%+v error=%T %v", result, err, err)
	}
}
