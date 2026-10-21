package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// legacyCorpus writes legacy-shaped missions (pre-identity / pre-topology /
// pre-mission_type meta.json shapes) into a project.
func legacyCorpus(s *testkit.Sandbox) {
	s.WriteFile("kitty-specs/001-legacy-alpha/meta.json", `{
  "created_at": "2025-01-02T03:04:05+00:00",
  "mission": "software-dev",
  "mission_number": "001",
  "slug": "001-legacy-alpha",
  "target_branch": "main"
}
`)
	s.WriteFile("kitty-specs/001-legacy-alpha/spec.md", "# Alpha\n")
	s.WriteFile("kitty-specs/002-legacy-beta/meta.json", `{"mission_id": "01JABCDEFGHJKMNPQRSTVWXYZ0", "mission_number": 2, "mission": "research", "coordination_branch": "kitty/coord-002-legacy-beta", "slug": "002-legacy-beta"}`+"\n")
	s.WriteFile("kitty-specs/003-corrupt/meta.json", "{not json\n")
	s.WriteFile("kitty-specs/004-nometa/spec.md", "# no meta\n")
	s.WriteFile("kitty-specs/005-unknown-type/meta.json", `{"mission": "bogus-type", "mission_number": "0005", "slug": "005-unknown-type", "topology": "lanes", "mission_type": ""}`+"\n")
	s.WriteFile("kitty-specs/006-typed/meta.json", `{"mission": " plan ", "mission_number": 6, "slug": "006-typed"}`+"\n")
	s.WriteFile("kitty-specs/006-typed/lanes.json", `{"version": 1, "mission_slug": "006-typed", "mission_branch": "kitty/mission-006", "target_branch": "main", "lanes": [{"lane_id": "lane-a", "wp_ids": ["WP01"]}], "computed_at": "2025-01-01T00:00:00+00:00", "computed_from": "x"}`+"\n")
	s.WriteFile("kitty-specs/007-array/meta.json", "[1, 2]\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "legacy corpus")
}

func TestDiffMigrateBackfills(t *testing.T) {
	p := newOraclePair(t)
	p.Both(legacyCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-identity", "--dry-run", "--json"},
		step{"migrate", "backfill-identity", "--dry-run"},
		step{"migrate", "backfill-topology", "--dry-run", "--json"},
		step{"migrate", "backfill-topology", "--restamp-single-branch", "--dry-run", "--json"},
		step{"migrate", "backfill-mission-type", "--dry-run", "--json"},
		step{"migrate", "backfill-mission-type", "--dry-run"},
		step{"migrate", "backfill-mission-type", "--mission", "nope"},
		step{"migrate", "--dry-run", "backfill-topology", "--json"},
		step{"migrate", "--force", "backfill-topology"},
		step{"migrate", "-v", "backfill-identity"},
	)
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-topology"},
		step{"migrate", "backfill-topology", "--json"},
		step{"migrate", "backfill-mission-type"},
		step{"migrate", "backfill-mission-type", "--json"},
		step{"migrate", "backfill-topology", "--restamp-single-branch"},
	)
	p.CompareTree("kitty-specs", true)
	// identity: mission ids are random, compare shapes via normalization
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-identity", "--mission", "001-legacy-alpha", "--json"},
		step{"migrate", "backfill-identity", "--json"},
		step{"migrate", "backfill-identity"},
	)
	p.CompareTree("kitty-specs", true)
}

func TestDiffMigrateIdentitySentinel(t *testing.T) {
	p := newOraclePair(t)
	writeBoth(p, "kitty-specs/001-x/meta.json", `{"mission_number": "pending", "mission_id": "01JABCDEFGHJKMNPQRSTVWXYZ0"}`+"\n")
	g, o := p.Run("migrate", "backfill-identity", "--json")
	if g.Code != o.Code || g.Stdout != o.Stdout {
		t.Errorf("sentinel: go=%d %q oracle=%d %q", g.Code, g.Stdout, o.Code, o.Stdout)
	}
}

func TestDiffMigrateLayout(t *testing.T) {
	p := newOraclePair(t)
	writeBoth(p, ".kittify/templates/custom-thing.md", "mine\n")
	writeBoth(p, ".kittify/missions/software-dev/mission.yaml", "custom: true\n")
	writeBoth(p, ".kittify/random.txt", "x\n")
	writeBoth(p, ".kittify/overrides/templates/blocked/x.md", "y\n")
	writeBoth(p, ".kittify/templates/blocked/x.md", "z\n")
	compareSteps(t, p, "", "",
		step{"migrate", "--dry-run"},
		step{"migrate", "--dry-run", "--verbose"},
		step{"migrate"},
		step{"migrate", "--force"},
		step{"migrate", "--force", "-v"},
	)
	p.CompareTree(".kittify", true)
}

func TestMigrateWithoutOracle(t *testing.T) {
	s := missionfx.NewProject(t)
	legacyCorpus(s)
	r := s.Run("migrate", "backfill-topology", "--json")
	if r.Code != 1 { // 003-corrupt is an error
		t.Fatalf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if got := s.ReadFile("kitty-specs/001-legacy-alpha/meta.json"); !contains(got, `"topology": "single_branch"`) {
		t.Errorf("topology not written:\n%s", got)
	}
	r = s.Run("migrate", "--json", "backfill-topology")
	if r.Code != 2 {
		t.Errorf("group --json should be a usage error, got %d", r.Code)
	}
	r = s.Run("migrate", "--verbose", "backfill-topology")
	if r.Code != 2 {
		t.Errorf("--verbose is not declared by backfill-topology: exit %d", r.Code)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool { return indexOf(s, sub) >= 0 })()
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
