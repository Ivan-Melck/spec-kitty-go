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

// envelopeCorpus writes canonical-looking rows whose envelope fields fail
// spec_kitty_events.Event validation (timestamp, event_id, aggregate_id).
func envelopeCorpus(s *testkit.Sandbox, slug string, rows ...[3]string) {
	s.WriteFile("kitty-specs/"+slug+"/meta.json", `{"created_at": "2025-03-01T00:00:00+00:00", "friendly_name": "e", "mission_id": "01JEEEEEEEEEEEEEEEEEEEEE`+slug[1:3]+`", "mission_number": `+slug[1:3]+`, "mission_slug": "`+slug+`", "mission_type": "software-dev", "slug": "`+slug+`", "target_branch": "main"}`+"\n")
	var lines []string
	for _, r := range rows {
		lines = append(lines, `{"actor": "a", "at": "`+r[0]+`", "event_id": "`+r[1]+`", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "`+slug+`", "policy_metadata": null, "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "`+r[2]+`"}`)
	}
	s.WriteFile("kitty-specs/"+slug+"/status.events.jsonl", strings.Join(lines, "\n")+"\n")
	s.Git("add", "-A", "kitty-specs/"+slug)
	s.Git("commit", "-q", "-m", slug)
}

func TestDiffDoctorMissionStateTeamspaceEnvelopes(t *testing.T) {
	p := newOraclePair(t)
	cases := [][]string{
		{"not-a-date", "2025-13-01T00:00:00", "2025-01-01T25:00:00", "2025-1-1", "", "2025-02-29T00:00:00", "2024-02-29", "20250101",
			"2025-01-01T00:00", "2025-01-01 00:00:00Z", "2025-01-01T00:00:00.1234567+00:00", "2025/01/01", "2025-0a-01", "2025-01-0x", "2025-04-31",
			"2025-01-01X00:00", "2025-01-01T0", "2025-01-01T00:00:00+0000", "2025-01-01T00:00:61", "x025-01-01"},
	}
	ids := []string{"01JEEEEEEEEEEEEEEEEEEEEEI1", "short", "0123456789abcdef0123456789ABCDEF", "01234567-89ab-cdef-0123-456789abcdef"}
	p.Both(func(s *testkit.Sandbox) {
		var rows [][3]string
		for i, ts := range cases[0] {
			rows = append(rows, [3]string{ts, "01JEEEEEEEEEEEEEEEEEEEE" + string(rune('A'+i)) + "00", "WP01"})
		}
		envelopeCorpus(s, "040-timestamps", rows...)
		var idRows [][3]string
		for _, id := range ids {
			idRows = append(idRows, [3]string{"2025-03-01T00:00:00+00:00", id, "WP02"})
		}
		idRows = append(idRows, [3]string{"bad", "01JEEEEEEEEEEEEEEEEEEEEEZ9", ""})
		envelopeCorpus(s, "041-ids", idRows...)
	})
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--teamspace-dry-run", "--mission", "040-timestamps", "--json"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--mission", "041-ids", "--json"},
		step{"doctor", "mission-state", "--teamspace-dry-run", "--mission", "041-ids"},
	)
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
