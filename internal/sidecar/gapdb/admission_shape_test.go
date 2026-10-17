package gapdb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestHistoricalAdmissionShapeMatchesPinnedArchiveAndRemainsClosed(t *testing.T) {
	raw, err := os.ReadFile(admissionRecordPath(t))
	if err != nil {
		t.Fatal(err)
	}
	admission := historicalM1ExecutableAdmission()
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != admission.recordSHA256 {
		t.Fatal("fixture is not the immutable historical admission")
	}
	var record admissionRecord
	if !singleStrictJSONValue(raw, &record) || validateAdmission(record, admission) != nil {
		t.Fatal("published historical admission is rejected by its own exact verifier")
	}
	for _, path := range [][]string{
		{"unrecognized_authority"}, {"gapdb_tuple", "unrecognized_authority"},
		{"gapdb_tuple", "sidecar", "unrecognized_authority"},
		{"gapdb_tuple", "protocol", "unrecognized_authority"},
		{"gapdb_tuple", "platform", "unrecognized_authority"},
	} {
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		target := fields
		for _, component := range path[:len(path)-1] {
			target = target[component].(map[string]any)
		}
		target[path[len(path)-1]] = true
		mutated, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		var decoded admissionRecord
		if singleStrictJSONValue(mutated, &decoded) {
			t.Fatalf("strict admission shape accepted unknown authority field: %v", path)
		}
	}
}
