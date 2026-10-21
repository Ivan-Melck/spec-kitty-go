package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const legacyMatrix = `{
  "mission_slug": "020-matrix",
  "criteria": [],
  "negative_invariants": [
    {"invariant_id": "NI-1", "description": "no foo", "verification_method": "grep_absence", "result": "confirmed_absent", "evidence": "none é"},
    {"invariant_id": "NI-2", "description": "no bar", "verification_method": "grep_absence", "result": "pending"},
    {"invariant_id": "NI-3", "description": "no baz", "verification_method": "custom_command", "result": "still_present", "provenance_origin": "recorded", "score": 1.0}
  ],
  "verdict": "pass"
}
`

func provenanceCorpus(s *testkit.Sandbox) {
	s.WriteFile("kitty-specs/020-matrix/meta.json", `{"slug": "020-matrix"}`+"\n")
	s.WriteFile("kitty-specs/020-matrix/acceptance-matrix.json", legacyMatrix)
	s.WriteFile("kitty-specs/021-clean/acceptance-matrix.json", `{"negative_invariants": []}`)
	s.WriteFile("kitty-specs/022-broken/acceptance-matrix.json", `{"negative_invariants": [`)
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "matrices")
}

func TestDiffMigrateBackfillProvenance(t *testing.T) {
	p := newOraclePair(t)
	p.Both(provenanceCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-provenance", "--dry-run", "--json"},
		step{"migrate", "backfill-provenance", "--dry-run"},
		step{"migrate", "backfill-provenance", "--json"},
	)
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-provenance"},
		step{"migrate", "backfill-provenance", "--project-root", "kitty-specs", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("kitty-specs/022-broken/acceptance-matrix.json", `{}`) })
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-provenance"},
	)
}
