package maintenance_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// transitionsCorpus writes a canonical log whose transitions break the
// spec-kitty-events transition matrix / business rules.
func transitionsCorpus(s *testkit.Sandbox) {
	s.WriteFile("kitty-specs/030-transitions/meta.json", `{"created_at": "2025-03-01T00:00:00+00:00", "friendly_name": "t", "mission_id": "01JTTTTTTTTTTTTTTTTTTTTTT0", "mission_number": 30, "mission_slug": "030-transitions", "mission_type": "software-dev", "slug": "030-transitions", "target_branch": "main"}`+"\n")
	row := func(n, wp, from, to string, force bool) string {
		f := "false"
		if force {
			f = "true"
		}
		return `{"actor": "a", "at": "2025-03-01T00:00:0` + n + `+00:00", "event_id": "01JTTTTTTTTTTTTTTTTTTTTTT` + n + `", "evidence": null, "execution_mode": "worktree", "force": ` + f + `, "from_lane": "` + from + `", "mission_slug": "030-transitions", "policy_metadata": null, "reason": null, "review_ref": null, "to_lane": "` + to + `", "wp_id": "` + wp + `"}`
	}
	rows := []string{
		row("1", "WP01", "planned", "in_progress", false),
		row("2", "WP01", "in_progress", "in_review", false),
		row("3", "WP01", "in_review", "planned", false),
		row("4", "WP02", "planned", "done", true),
		row("5", "WP03", "done", "for_review", false),
		row("6", "WP04", "planned", "approved", false),
		row("7", "WP05", "for_review", "in_progress", false),
		row("8", "WP06", "in_review", "done", false),
	}
	s.WriteFile("kitty-specs/030-transitions/status.events.jsonl", strings.Join(rows, "\n")+"\n")
	s.WriteFile("kitty-specs/030-transitions/mission-events.jsonl", "{\"type\": \"x\"}\n\n{\"type\": \"y\"}\n")
	s.WriteFile("kitty-specs/030-transitions/_archive/old.events.jsonl", "{}\n")
	s.Git("add", "-A", "kitty-specs/030-transitions")
	s.Git("commit", "-q", "-m", "transitions")
}

var envelopeHashRe = regexp.MustCompile(`"envelope_sha256": "([0-9a-f]{64})"`)

func envelopeHashes(s string) []string {
	var out []string
	for _, m := range envelopeHashRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestDiffDoctorMissionStateTeamspaceDryRun(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--teamspace-dry-run"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--json"},
	)
	g, o := missionfx.OracleCalcMission(t, p)
	compareSteps(t, p, g, o,
		step{"doctor", "mission-state", "--teamspace-dry-run"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--json"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--mission", "$M", "--json"},
	)
	p.Both(transitionsCorpus)
	compareSteps(t, p, g, o,
		step{"doctor", "mission-state", "--teamspace-dry-run", "--mission", "030-transitions"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--mission", "030-transitions", "--json"},
	)
	// envelope hashes are content-addressed and identical on both sides here
	gr := p.Go.Run("doctor", "mission-state", "--teamspace-dry-run", "--mission", "030-transitions", "--json")
	or := p.Oracle.RunOracle("doctor", "mission-state", "--teamspace-dry-run", "--mission", "030-transitions", "--json")
	gh, oh := envelopeHashes(gr.Stdout), envelopeHashes(or.Stdout)
	if len(gh) != 8 || strings.Join(gh, ",") != strings.Join(oh, ",") {
		t.Errorf("envelope hashes differ:\ngo:     %v\noracle: %v", gh, oh)
	}
	p.Both(repairCorpus)
	compareSteps(t, p, g, o,
		step{"doctor", "mission-state", "--teamspace-dry-run"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--json"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--mission", "020-legacy-rows", "--json"},
	)
}
