package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// landedMissions builds two missions landed on main: 010-merged through a
// --no-ff merge commit and 011-squashed through a single-parent commit; plus
// 012-unlanded, committed only on a side branch.
func landedMissions(s *testkit.Sandbox) map[string]string {
	vars := map[string]string{}
	s.Git("switch", "-q", "-c", "feat/merged")
	s.WriteFile("kitty-specs/010-merged/meta.json", `{"mission_id": "01JAAAAAAAAAAAAAAAAAAAAAAA", "mission_slug": "010-merged", "slug": "010-merged", "target_branch": "main", "mission_number": 10}`+"\n")
	s.WriteFile("kitty-specs/010-merged/spec.md", "# merged\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "corpus 010")
	vars["$BRANCHTIP"] = s.Git("rev-parse", "HEAD")
	s.Git("switch", "-q", "main")
	s.Git("merge", "-q", "--no-ff", "-m", "Merge feat/merged", "feat/merged")
	vars["$MERGE"] = s.Git("rev-parse", "HEAD")
	s.WriteFile("kitty-specs/011-squashed/meta.json", `{"mission_slug": "011-squashed", "slug": "011-squashed", "mission_number": 11}`+"\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "squash 011")
	vars["$SQUASH"] = s.Git("rev-parse", "HEAD")
	s.Git("switch", "-q", "-c", "side")
	s.WriteFile("kitty-specs/012-unlanded/meta.json", `{"mission_id": "01JBBBBBBBBBBBBBBBBBBBBBBB", "slug": "012-unlanded", "target_branch": "main"}`+"\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "unlanded 012")
	vars["$SIDE"] = s.Git("rev-parse", "HEAD")
	s.Git("switch", "-q", "main")
	s.Git("checkout", "-q", "side", "--", "kitty-specs/012-unlanded")
	s.Git("commit", "-q", "-m", "bring 012 meta")
	return vars
}

func TestDiffMigrateBackfillMergeCommit(t *testing.T) {
	p := newOraclePair(t)
	gv, ov := landedMissions(p.Go), landedMissions(p.Oracle)
	compareStepsVars(t, p, gv, ov,
		step{"migrate", "backfill-merge-commit", "--mission", "nope", "--merge-commit", "abcd"},
		step{"migrate", "backfill-merge-commit", "--mission", "nope", "--merge-commit", "abcd", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "010-merged", "--merge-commit", "zz"},
		step{"migrate", "backfill-merge-commit", "--mission", "010-merged", "--merge-commit", "$MERGE", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "010-merged", "--merge-commit", "$BRANCHTIP", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "010-merged", "--merge-commit", "$MERGE", "--attest-first-landing-commit", "--dry-run"},
		step{"migrate", "backfill-merge-commit", "--mission", "011-squashed", "--merge-commit", "$SQUASH", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "012-unlanded", "--merge-commit", "$SIDE", "--attest-first-landing-commit", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "012-unlanded", "--merge-commit", "$SIDE", "--attest-first-landing-commit", "--target-branch", "side", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "011-squashed", "--merge-commit", "$SQUASH", "--target-branch", "nobranch", "--attest-first-landing-commit"},
		step{"migrate", "backfill-merge-commit", "--mission", "010-merged", "--merge-commit", "$MERGE", "--attest-first-landing-commit"},
		step{"migrate", "backfill-merge-commit", "--mission", "011-squashed", "--merge-commit", "$SQUASH", "--attest-first-landing-commit", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "010-merged", "--merge-commit", "$MERGE", "--attest-first-landing-commit", "--json"},
		step{"migrate", "backfill-merge-commit", "--mission", "010-merged"},
	)
	p.CompareTree("kitty-specs", true)
}
