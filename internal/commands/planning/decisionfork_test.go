package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffDecisionVerifyFork covers DECISION_LOG_FORKED: a decision-event
// stream that diverges between the PRIMARY and coordination surfaces is
// reported (with the reconcile steps) and always exits 1, even with
// --no-fail-on-stale; a prefix (coordination ahead) is not a fork.
func TestDiffDecisionVerifyFork(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	ev := func(id, dec string) string {
		return `{"event_id": "` + id + `", "event_type": "DecisionInputRequested", "payload": {"decision_id": "` + dec + `"}}`
	}
	coordDir := `C=$(ls -d .worktrees/*-coord/kitty-specs/*); `
	shellSteps(t, p, g, o,
		`$SK agent decision verify --mission $M`,
		coordDir+`echo '`+ev("E1", "D1")+`' > kitty-specs/$M/decisions.events.jsonl; echo '`+ev("E1", "D1")+`' > $C/decisions.events.jsonl; echo '`+ev("E2", "D2")+`' >> $C/decisions.events.jsonl; $SK agent decision verify --mission $M; echo rc=$?`,
		coordDir+`echo '`+ev("E3", "D3")+`' >> kitty-specs/$M/decisions.events.jsonl; echo 'not json' >> $C/decisions.events.jsonl; $SK agent decision verify --mission $M --no-fail-on-stale; echo rc=$?`,
		`$SK agent decision verify --mission $M --no-json; echo rc=$?`,
	)
}
