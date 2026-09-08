package m2

import "testing"

func TestQualificationSidecarRolesAreClosedAndNonAliasing(t *testing.T) {
	if err := validateQualificationSidecarPair(QualificationCandidateSidecarSHA256, WP16AbsenceSidecarSHA256); err != nil {
		t.Fatalf("exact candidate and frozen WP16 sidecar refused: %v", err)
	}
	for name, pair := range map[string][2]string{
		"candidate-substituted-with-wp16": {WP16AbsenceSidecarSHA256, WP16AbsenceSidecarSHA256},
		"wp16-substituted-with-candidate": {QualificationCandidateSidecarSHA256, QualificationCandidateSidecarSHA256},
		"candidate-unknown":               {SHA256([]byte("candidate-substitute")), WP16AbsenceSidecarSHA256},
		"wp16-unknown":                    {QualificationCandidateSidecarSHA256, SHA256([]byte("wp16-substitute"))},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateQualificationSidecarPair(pair[0], pair[1]); err == nil {
				t.Fatal("substituted or aliased sidecar roles were accepted")
			}
		})
	}
}
