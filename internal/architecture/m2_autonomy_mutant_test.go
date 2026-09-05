package architecture

import (
	"strings"
	"testing"
)

func TestM2AutonomyMutationCampaignCoversCeremonyFailureClasses(t *testing.T) {
	source := readM2AutonomySource(t, m2RepositoryRoot(t), "internal/mission/readiness_mutant_test.go")
	if count := strings.Count(source, `id: "M18-`); count != 15 {
		t.Fatalf("autonomy mutant census=%d want=15", count)
	}
	for _, identity := range []string{
		"M18-CURRENT-STEP-AUTHORITY",
		"M18-FIRST-ACTION-ONLY",
		"M18-MULTIPLE-ACTIONS-AMBIGUOUS",
		"M18-GLOBAL-BLOCKER",
		"M18-PROJECTION-MINTS-AUTHORITY",
		"M18-PRODUCERLESS-FACT",
		"M18-INAPPLICABLE-ARTIFACT-REQUIRED",
		"M18-UNAVAILABLE-COLLAPSED",
		"M18-SKIPPED-MEMBER-HIDDEN",
		"M18-ACTIVITY-ORDER-AFFECTS-AUTHORITY",
		"M18-IO-EVALUATOR",
		"M18-FINALIZATION-PHASE-GATE",
		"M18-LEGACY-PHASE-STATE-ACCEPTED",
		"M18-LIFECYCLE-ADVANCE-RETURNS",
		"M18-UNSTABLE-GAPDB-READ",
	} {
		if strings.Count(source, identity) != 1 {
			t.Fatalf("autonomy mutant %s is absent or duplicated", identity)
		}
	}
	if !strings.Contains(source, "readinessMutantDigest") || !strings.Contains(source, "production control failed") || !strings.Contains(source, "mutant failed for wrong reason") {
		t.Fatal("autonomy mutant campaign lacks sealed census, positive controls, or cause-specific killers")
	}
}
