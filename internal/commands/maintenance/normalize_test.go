package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func wpFile(id, lane string) string {
	l := ""
	if lane != "" {
		l = "lane: " + lane + "\n"
	}
	return "---\nwork_package_id: " + id + "\ntitle: Do " + id + "\n" + l + "dependencies: []\n---\n# " + id + "\n"
}

// lifecycleCorpus is a set of pre-event-log missions (lane in WP
// frontmatter, legacy status.json) for normalize-lifecycle.
func lifecycleCorpus(s *testkit.Sandbox) {
	m := "kitty-specs/050-legacy-lanes/"
	s.WriteFile(m+"meta.json", `{"mission_id": "01JDDDDDDDDDDDDDDDDDDDDDDD", "mission_number": "050", "slug": "050-legacy-lanes", "created_at": "2025-03-01T10:00:00+00:00"}`+"\n")
	s.WriteFile(m+"tasks/WP01-a.md", wpFile("WP01", "done"))
	s.WriteFile(m+"tasks/WP02-b.md", wpFile("WP02", "doing"))
	s.WriteFile(m+"tasks/WP03-c.md", wpFile("WP03", ""))
	s.WriteFile(m+"tasks/WP04-d.md", wpFile("WP04", "for_review"))
	s.WriteFile(m+"status.json", `{"materialized_at": "2025-03-02T00:00:00+00:00", "work_packages": {"WP04": {"lane": "approved", "last_transition_at": "2025-03-02T00:00:00+00:00"}}}`+"\n")
	n := "kitty-specs/051-done-all/"
	s.WriteFile(n+"meta.json", `{"mission_id": "01JEEEEEEEEEEEEEEEEEEEEEEE", "slug": "051-done-all"}`+"\n")
	s.WriteFile(n+"tasks/WP01-x.md", wpFile("WP01", "done"))
	s.WriteFile("kitty-specs/052-nometa/spec.md", "# x\n")
	s.WriteFile("kitty-specs/053-sentinel/meta.json", `{"mission_id": "01JFFFFFFFFFFFFFFFFFFFFFFF", "mission_number": "pending"}`+"\n")
	s.WriteFile("kitty-specs/054-bare/meta.json", `{"mission_id": "01JGGGGGGGGGGGGGGGGGGGGGGG"}`+"\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "legacy lifecycle corpus")
}

func TestDiffMigrateNormalizeLifecycle(t *testing.T) {
	p := newOraclePair(t)
	p.Both(lifecycleCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "normalize-lifecycle", "--dry-run", "--json"},
		step{"migrate", "normalize-lifecycle", "--dry-run"},
		step{"migrate", "normalize-lifecycle", "--mission", "050-legacy-lanes", "--json"},
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/derived", true)
	compareSteps(t, p, "", "",
		step{"migrate", "normalize-lifecycle"},
		step{"migrate", "normalize-lifecycle", "--json"},
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/derived", true)
}

func TestNormalizeLifecycleWithoutOracle(t *testing.T) {
	s := missionfx.NewProject(t)
	lifecycleCorpus(s)
	r := s.Run("migrate", "normalize-lifecycle", "--json")
	if r.Code != 1 { // 053-sentinel errors
		t.Fatalf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if !s.Exists("kitty-specs/050-legacy-lanes/status.events.jsonl") {
		t.Fatal("event log not rebuilt")
	}
}
