package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffDoctorIdentityTopology(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "identity"},
		step{"doctor", "identity", "--json"},
		step{"doctor", "topology", "--json"},
	)
	p.Both(legacyCorpus)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/001-other/meta.json", `{"mission_id": "01JKKKKKKKKKKKKKKKKKKKKKKK", "mission_number": 1}`+"\n")
		s.WriteFile("kitty-specs/008-sb/meta.json", `{"topology": "single_branch", "flattened": true}`+"\n")
		s.WriteFile("kitty-specs/008-sb/lanes.json", `{"version": 1, "mission_slug": "008-sb", "mission_branch": "b", "target_branch": "main", "lanes": [{"lane_id": "lane-a", "wp_ids": ["WP01"]}], "computed_at": "x", "computed_from": "y"}`)
		s.WriteFile("kitty-specs/009-sb-bad/meta.json", `{"topology": "single_branch"}`+"\n")
		s.WriteFile("kitty-specs/009-sb-bad/lanes.json", `{"version": 1}`)
	})
	compareSteps(t, p, "", "",
		step{"doctor", "identity"},
		step{"doctor", "identity", "--json"},
		step{"doctor", "identity", "--fail-on", "legacy,orphan"},
		step{"doctor", "identity", "--mission", "001-legacy-alpha", "--json"},
		step{"doctor", "identity", "--mission", "nope"},
		step{"doctor", "topology"},
		step{"doctor", "topology", "--json"},
		step{"doctor", "topology", "--mission", "008-sb", "--json"},
		step{"doctor", "topology", "--mission", "nope"},
	)
}
