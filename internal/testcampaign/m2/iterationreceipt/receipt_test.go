package iterationreceipt

import (
	"testing"
	"time"
)

func TestRecordBindsExecutingIterationFacts(t *testing.T) {
	record, err := New("fault/example", 0, "seed-0", "before-publication", "typed-conflict", time.Nanosecond, map[string]any{"winner": 1, "loser": 1}, map[string]any{"universal": "pass"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(record)
	if err != nil || len(encoded) == 0 || record.ObservedFactsSHA256 == "" {
		t.Fatalf("record=%+v encoded=%q err=%v", record, encoded, err)
	}
	record.DurationNanoseconds = 0
	if record.Validate() == nil {
		t.Fatal("zero-duration manufactured receipt passed")
	}
}
