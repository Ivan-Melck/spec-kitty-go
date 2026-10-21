package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func charterCorpus(s *testkit.Sandbox) {
	s.WriteFile("kitty-specs/030-ch/meta.json", `{"mission_id": "01JCCCCCCCCCCCCCCCCCCCCCCC"}`+"\n")
	s.WriteFile("kitty-specs/030-ch/charter/ok.md", "# fine ✓\n")
	s.WriteFile("kitty-specs/030-ch/charter/bom16.txt", "\xff\xfeh\x00i\x00\n\x00")
	s.WriteFile("kitty-specs/030-ch/charter/sig.yaml", "\xef\xbb\xbfkey: v\n")
	s.WriteFile(".kittify/charter/charter.md", "# Charter\n")
	s.WriteFile(".kittify/charter/bom32.md", "\xff\xfe\x00\x00A\x00\x00\x00")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "charters")
}

func TestDiffMigrateCharterEncoding(t *testing.T) {
	p := newOraclePair(t)
	p.Both(charterCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "charter-encoding", "--dry-run", "--json"},
		step{"migrate", "charter-encoding", "--dry-run"},
		step{"migrate", "charter-encoding"},
	)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/charter/bom32.md.bak", "old") })
	compareSteps(t, p, "", "",
		step{"migrate", "charter-encoding", "--yes", "--json"},
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/charter", true)
	compareSteps(t, p, "", "",
		step{"migrate", "charter-encoding", "--yes"},
		step{"migrate", "--dry-run", "charter-encoding"},
	)
}
