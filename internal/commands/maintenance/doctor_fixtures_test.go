package maintenance_test

import (
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// fixtureCorpus is a --fixture-dir scan root outside kitty-specs/: every
// directory under it is audited as a Mission.
func fixtureCorpus(s *testkit.Sandbox) {
	s.WriteFile("fx/a-clean/meta.json", `{"mission_id": "01JQQQQQQQQQQQQQQQQQQQQQQQ", "mission_slug": "a-clean", "mission_number": 1, "mission_type": "software-dev", "friendly_name": "A", "target_branch": "main"}`+"\n")
	s.WriteFile("fx/b-legacy/meta.json", `{"feature_slug": "b", "mission_slug": "b-legacy"}`+"\n")
	s.WriteFile("fx/b-legacy/status.events.jsonl", `{"actor": "x", "at": "2025-01-01T00:00:00+00:00", "event_id": "01JQQQQQQQQQQQQQQQQQQQQQQ1", "evidence": null, "execution_mode": "worktree", "feature_slug": "b-legacy", "force": false, "from_lane": "planned", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP01"}`+"\nnot json\n")
	s.WriteFile("fx/c-empty/notes.txt", "residue-shaped, but a custom scan root keeps it\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "fixture corpus")
}

func TestDiffDoctorMissionStateFixtures(t *testing.T) {
	p := newOraclePair(t)
	p.Both(fixtureCorpus)
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--audit", "--include-fixtures", "--fixture-dir", "fx"},
		step{"doctor", "mission-state", "--audit", "--include-fixtures"},
		step{"doctor", "mission-state", "--audit", "--include-fixtures", "--json"},
		step{"doctor", "mission-state", "--audit", "--include-fixtures", "--fail-on", "error"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--include-fixtures", "--json"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--include-fixtures"},
		step{"doctor", "mission-state", "--audit", "--fixture-dir", "fx"},
		step{"doctor", "mission-state", "--audit", "--fixture-dir", "fx", "--json"},
		step{"doctor", "mission-state", "--audit", "--fixture-dir", "kitty-specs", "--json"},
		step{"doctor", "mission-state", "--audit", "--fixture-dir", "missing-dir", "--json"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--fixture-dir", "fx", "--json"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--fixture-dir", "fx", "--mission", "a-clean", "--json"},
	)
	// --fix over a sandbox-owned fixture tree (never the oracle's packaged
	// fixtures: upstream would rewrite them inside site-packages).
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--fix", "--fixture-dir", "fx", "--json", "--allow-dirty"},
	)
	p.CompareTree("fx", true)
	// outside any project: the fixture tree's parent stands in for the root
	for _, args := range [][]string{
		{"doctor", "mission-state", "--audit", "--include-fixtures", "--json"},
		{"doctor", "mission-state", "--audit", "--include-fixtures"},
		{"doctor", "mission-state", "--teamspace-dry-run", "--include-fixtures", "--json"},
		{"doctor", "mission-state", "--audit", "--json"},
	} {
		gd, od := t.TempDir(), t.TempDir()
		g := p.Go.Exec(gd, testkit.Binary(t), args...)
		o := p.Oracle.Exec(od, testkit.Oracle(t), args...)
		norm := func(s *testkit.Sandbox, dir, out string) string {
			return s.Normalize(strings.ReplaceAll(out, dir, "<CWD>"))
		}
		if g.Code != o.Code || norm(p.Go, gd, g.Stdout) != norm(p.Oracle, od, o.Stdout) {
			t.Errorf("%v (no project): go=%d oracle=%d\n%s", args, g.Code, o.Code, testkit.Diff(norm(p.Oracle, od, o.Stdout), norm(p.Go, gd, g.Stdout)))
		}
	}
	if os.Getenv("KITTY_TEST_TRACE") != "" {
		t.Log(p.Go.ReadFile("fx/b-legacy/meta.json"))
	}
}
