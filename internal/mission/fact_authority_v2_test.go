package mission

import (
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestMissionFactDetailDigestSurvivesCanonicalRestoreV2(t *testing.T) {
	detail, err := journalv2.ParseDigest(
		"detail_sha256",
		"847a826f7a378010f9824fb8dfd85d29d8fe1c65ad7ddfb8fc79a34317dc6015",
	)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := NewMissionFactStateV2(MissionFactStateInputV2{
		ProjectID:       "project-1",
		MissionID:       "mission-1",
		FactID:          "decision-review-exception-1",
		Kind:            MissionFactDecisionV2,
		Status:          MissionFactResolvedV2,
		LogicalVersion:  2,
		DetailDigest:    detail,
		LastOperationID: "operation-resolve-exception",
		ObservedAt:      "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fact.DetailDigest(); got != detail {
		t.Fatalf("constructed detail digest = %q, want %q", got, detail)
	}
	canonical := fact.CanonicalBytes()
	returned := fact.DetailDigest()
	returned[0] ^= 0xff
	if fact.DetailDigest() != detail || string(fact.CanonicalBytes()) != string(canonical) {
		t.Fatal("mutating a returned detail digest changed mission-fact state")
	}

	restored, err := DecodeMissionFactStateV2(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.DetailDigest(); got != detail {
		t.Fatalf("restored detail digest = %q, want %q", got, detail)
	}
	if got := (MissionFactStateV2{}).DetailDigest(); !got.IsZero() {
		t.Fatalf("zero fact exposed non-zero detail digest %q", got)
	}
}
