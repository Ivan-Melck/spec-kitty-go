package maintenance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const (
	evA = `{"actor": "claude", "at": "2025-02-01T10:00:00+00:00", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAA1", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "020-legacy-rows", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP01"}`
	evB = `{"actor": "claude", "at": "2025-02-01T10:05:00+00:00", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAA2", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "claimed", "mission_slug": "020-legacy-rows", "reason": null, "review_ref": null, "to_lane": "in_progress", "wp_id": "WP01"}`
)

// repairCorpus writes historical status-log shapes for `doctor
// mission-state --fix` on top of legacyCorpus.
func repairCorpus(s *testkit.Sandbox) {
	legacyCorpus(s)
	rows := []string{
		evA,
		// legacy identity spelling, lane alias, dict actor, missing event id / force / mode
		`{"feature_slug": "020-legacy-rows", "work_package_id": "WP02", "feature_number": "020", "from_lane": "planned", "to_lane": "doing", "at": "2025-02-01T11:00:00+00:00", "actor": {"role": "Implementer Bot", "extra": 1}}`,
		evB,
		evA, // duplicate event id
		// legacy typed lane transition
		`{"event_type": "WPStatusChanged", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAA3", "wp_id": "WP03", "from_lane": "planned", "to_lane": "claimed", "at": "2025-02-01T12:00:00+00:00", "actor": "bot", "force": "yes", "execution_mode": "worktree", "mission_slug": "020-legacy-rows"}`,
		// preserved non-lane decision row
		`{"event_type": "DecisionPointOpened", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAA4", "payload": {"x": 1}, "at": "2025-02-01T12:30:00+00:00"}`,
		// quarantined event_name row
		`{"event_name": "handoff.created", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAA5", "at": "2025-02-01T12:40:00+00:00"}`,
		// mistyped scalars and a bare-uuid event id
		`{"actor": "", "at": 12345, "event_id": "0123456789abcdef0123456789abcdef", "execution_mode": "bogus", "force": 0, "from_lane": null, "mission_slug": "020-legacy-rows", "to_lane": "planned", "wp_id": "WP04", "reason": null, "review_result": null, "mission_id": null}`,
	}
	s.WriteFile("kitty-specs/020-legacy-rows/meta.json", `{"created_at": "2025-02-01T09:00:00+00:00", "friendly_name": "legacy rows", "mission_id": "01JRRRRRRRRRRRRRRRRRRRRRR0", "mission_number": 20, "mission_slug": "020-legacy-rows", "mission_type": "software-dev", "slug": "020-legacy-rows", "target_branch": "main"}`+"\n")
	s.WriteFile("kitty-specs/020-legacy-rows/status.events.jsonl", strings.Join(rows, "\n")+"\n")
	s.WriteFile("kitty-specs/020-legacy-rows/status.json", "{}\n")
	s.WriteFile("kitty-specs/020-legacy-rows/spec.md", "# rows\n")
	s.WriteFile("kitty-specs/020-legacy-rows/tasks/WP01-a.md", "---\nwork_package_id: WP01\nreview_feedback: ''\ntitle: A\nreview_feedback: review-cycle://020/WP01/1\n---\n# body\n")
	// a row-level error (missing to_lane) aborts that mission before writing
	s.WriteFile("kitty-specs/021-bad-row/meta.json", `{"created_at": "2025-02-02T00:00:00+00:00", "friendly_name": "bad", "mission_id": "01JRRRRRRRRRRRRRRRRRRRRRR1", "mission_number": 21, "mission_slug": "021-bad-row", "mission_type": "software-dev", "slug": "021-bad-row", "target_branch": "main", "change_mode": "regular"}`+"\n")
	s.WriteFile("kitty-specs/021-bad-row/status.events.jsonl", `{"event_id": "01JAAAAAAAAAAAAAAAAAAAAAB1", "wp_id": "WP01", "from_lane": "planned", "at": "2025-02-02T00:00:00+00:00", "actor": "x"}`+"\n"+
		`{"event_id": "01JAAAAAAAAAAAAAAAAAAAAAB2", "wp_id": "WP01", "from_lane": "bogus", "to_lane": "claimed", "at": "2025-02-02T00:00:00+00:00", "actor": "x"}`+"\n")
	// a healthy CRLF log with a drifted (tracked) status.json is reported, not rewritten
	crlf := strings.ReplaceAll(evA, "020-legacy-rows", "022-crlf")
	s.WriteFile("kitty-specs/022-crlf/meta.json", `{"created_at": "2025-02-03T00:00:00+00:00", "friendly_name": "crlf", "mission_id": "01JRRRRRRRRRRRRRRRRRRRRRR2", "mission_number": 22, "mission_slug": "022-crlf", "mission_type": "software-dev", "slug": "022-crlf", "target_branch": "main"}`+"\n")
	s.WriteFile("kitty-specs/022-crlf/status.events.jsonl", crlf+"\r\n")
	s.WriteFile("kitty-specs/022-crlf/status.json", "{\"stale\": true}\n")
	s.WriteFile("kitty-specs/023-badjson/meta.json", `{"created_at": "2025-02-04T00:00:00+00:00", "mission_number": "23x", "slug": "023-badjson"}`+"\n")
	s.WriteFile("kitty-specs/024-badlog/spec.md", "# x\n")
	s.WriteFile("kitty-specs/024-badlog/status.events.jsonl", "\n{oops\n")
	s.WriteFile("kitty-specs/091-residue/notes.lock", "x")
	s.Git("add", "-A", "kitty-specs")
	s.Git("commit", "-q", "-m", "repair corpus")
}

var runIDRe = regexp.MustCompile(`\b[0-9a-f]{16}\b`)

func maskRunID(s string) string { return runIDRe.ReplaceAllString(s, "<RUNID>") }

// treeFiles lists project-relative files under rel (sorted).
func treeFiles(t *testing.T, s *testkit.Sandbox, rel string) []string {
	t.Helper()
	var out []string
	base := filepath.Join(s.Dir, rel)
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			r, _ := filepath.Rel(s.Dir, p)
			out = append(out, r)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func TestDiffDoctorMissionStateFix(t *testing.T) {
	p := newOraclePair(t)
	p.Both(repairCorpus)
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--fix", "--mission", "nope"},
		step{"doctor", "mission-state", "--fix", "--json"},
		step{"doctor", "mission-state", "--fix"},
		step{"doctor", "mission-state", "--fix", "--allow-dirty", "--mission", "091-residue"},
		step{"doctor", "mission-state", "--fix", "--allow-dirty", "--json"},
	)
	gf, of := treeFiles(t, p.Go, "kitty-specs"), treeFiles(t, p.Oracle, "kitty-specs")
	if strings.Join(gf, "\n") != strings.Join(of, "\n") {
		t.Errorf("kitty-specs files differ:\n%s", testkit.Diff(strings.Join(of, "\n"), strings.Join(gf, "\n")))
	}
	compareFiles(t, p, "", "", gf...)
	ga, oa := treeFiles(t, p.Go, ".kittify/mission-state-audit"), treeFiles(t, p.Oracle, ".kittify/mission-state-audit")
	if strings.Join(ga, "\n") != strings.Join(oa, "\n") {
		t.Errorf("audit files differ:\n%s", testkit.Diff(strings.Join(oa, "\n"), strings.Join(ga, "\n")))
	} else {
		compareFiles(t, p, "", "", ga...)
	}
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--fix", "--allow-dirty"},
		step{"doctor", "mission-state", "--fix", "--allow-dirty", "--mission", "020", "--manifest-path", "custom/manifest.json", "--json"},
		step{"doctor", "mission-state", "--audit", "--json"},
	)
	compareFiles(t, p, "", "", "custom/manifest.json")
}

func TestDiffDoctorMissionStateFixCalc(t *testing.T) {
	p := newOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	// the run id hashes the (per-side) mission files
	for _, a := range [][]string{{"--fix", "--mission", "$M"}, {"--fix", "--json"}} {
		args := append([]string{"doctor", "mission-state"}, a...)
		gr, or := p.Go.Run(subst(args, g)...), p.Oracle.RunOracle(subst(args, o)...)
		gs, os_ := maskRunID(p.Go.Normalize(gr.Stdout)), maskRunID(p.Oracle.Normalize(or.Stdout))
		if gr.Code != or.Code || gs != os_ {
			t.Errorf("%v: exit go=%d oracle=%d\n%s", a, gr.Code, or.Code, testkit.Diff(os_, gs))
		}
	}
	compareFiles(t, p, g, o, "kitty-specs/$M/meta.json", "kitty-specs/$M/status.events.jsonl", "kitty-specs/$M/status.json")
}

func TestMissionStateFixWithoutOracle(t *testing.T) {
	s := missionfx.NewProject(t)
	repairCorpus(s)
	r := s.Run("doctor", "mission-state", "--fix", "--json")
	if r.Code != 1 || !strings.Contains(r.Stdout, "MISSION_STATE_REPAIR_FAILED") {
		t.Fatalf("fix: %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("doctor", "mission-state", "--fix", "--json", "--allow-dirty")
	if r.Code != 1 || !strings.Contains(r.Stdout, `"missions_error": 4`) {
		t.Fatalf("fix: %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(s.ReadFile("kitty-specs/020-legacy-rows/tasks/WP01-a.md"), "review_feedback: review-cycle://020/WP01/1") {
		t.Fatal("dup-key heal did not keep the last non-empty value")
	}
	r = s.Run("doctor", "mission-state", "--fix", "--allow-dirty")
	if r.Code != 1 || !strings.Contains(r.Stdout, "Mission-state repair complete") {
		t.Fatalf("expected dirty refusal: %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
}
