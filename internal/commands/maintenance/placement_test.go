package maintenance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// rePackPath masks the mission-type definition path (the oracle's
// site-packages vs the Go pack cache) in `mission current` panels.
var rePackPath = regexp.MustCompile(`(?m)^│ Path: .*$`)

// seamTail is the final non-empty line (an uncaught exception's
// "Type: message" line; the traceback frames above it are Python-specific).
func seamTail(s string) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(ls) - 1; i >= 0; i-- {
		if strings.TrimSpace(ls[i]) != "" {
			return ls[i]
		}
	}
	return ""
}

// compareSeamSteps diffs exit code, normalized stdout and the last stderr
// line of each step.
func compareSeamSteps(t *testing.T, p *testkit.Pair, goVars, orVars map[string]string, steps ...step) {
	t.Helper()
	for _, s := range steps {
		g := p.Go.Run(substVars(s, goVars)...)
		o := p.Oracle.RunOracle(substVars(s, orVars)...)
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\ngo:\n%s%s\noracle:\n%s%s", s, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			continue
		}
		gs := rePackPath.ReplaceAllString(p.Go.Normalize(g.Stdout), "│ Path: <P>")
		os_ := rePackPath.ReplaceAllString(p.Oracle.Normalize(o.Stdout), "│ Path: <P>")
		if gs != os_ {
			t.Errorf("%v: stdout differs\n%s", s, testkit.Diff(os_, gs))
		}
		if ge, oe := seamTail(p.Go.Normalize(g.Stderr)), seamTail(p.Oracle.Normalize(o.Stderr)); ge != oe {
			t.Errorf("%v: stderr tail differs\ngo:     %s\noracle: %s", s, ge, oe)
		}
	}
}

func missionDirWithPrefix(t *testing.T, s *testkit.Sandbox, prefix string) string {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(s.Dir, "kitty-specs"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) {
			return e.Name()
		}
	}
	t.Fatalf("no %s* mission in %s", prefix, s.Dir)
	return ""
}

// seamCorpus creates alpha (single_branch), beta (coord), two "dup"
// missions and a legacy 001-legacy directory, returning the per-side
// handle variables.
func seamCorpus(t *testing.T, p *testkit.Pair) (goVars, orVars map[string]string) {
	t.Helper()
	vars := []map[string]string{}
	p.Both(func(s *testkit.Sandbox) {
		s.Git("switch", "-q", "-c", "feat/x")
		for _, m := range [][]string{{"alpha", "single_branch"}, {"beta", "coord"}, {"dup", "single_branch"}, {"dup", "single_branch"}} {
			missionfx.RunOracle(t, s, "agent", "mission", "create", m[0], "--mission-type", "software-dev", "--topology", m[1], "--json")
		}
		s.WriteFile("kitty-specs/001-legacy/spec.md", "# legacy\n")
		s.WriteFile("kitty-specs/001-legacy/meta.json", `{"mission_number": "001", "slug": "001-legacy", "mission_type": "software-dev"}`+"\n")
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "missions")
		a := missionDirWithPrefix(t, s, "alpha-")
		vars = append(vars, map[string]string{"$A": a, "$MID": a[strings.LastIndex(a, "-")+1:], "$B": missionDirWithPrefix(t, s, "beta")})
	})
	return vars[0], vars[1]
}

// TestDiffPlacementSeamHandleReads drives every PRIMARY read that goes
// through placement_seam(...).read_dir(PRIMARY_METADATA / WORK_PACKAGE_TASK)
// (validate-encoding, validate-tasks, doctor identity / topology) and
// `mission current`'s resolve_action_context read over every handle form:
// full slug, bare human slug, mid8, numeric prefix, legacy dir, ambiguous
// bare slug, unknown handle and a coord mission.
func TestDiffPlacementSeamHandleReads(t *testing.T) {
	p := newOraclePair(t)
	gv, ov := seamCorpus(t, p)
	var steps []step
	for _, h := range []string{"$A", "alpha", "$MID", "001", "001-legacy", "legacy", "dup", "nonexist", "$B", "beta"} {
		steps = append(steps,
			step{"validate-encoding", "--mission", h},
			step{"validate-tasks", "--mission", h},
			step{"doctor", "identity", "--mission", h, "--json"},
			step{"doctor", "topology", "--mission", h, "--json"},
			step{"mission", "current", "--mission", h},
		)
	}
	compareSeamSteps(t, p, gv, ov, steps...)
}

// TestDiffPlacementSeamCoordStates drives the STATUS_STATE / REVIEW_CYCLE
// reads of doctor decisions and doctor review-cycle-reconcile across the
// coordination surface's states: materialized, unmaterialized (worktree
// removed, branch alive) and deleted (branch gone).
func TestDiffPlacementSeamCoordStates(t *testing.T) {
	p := newOraclePair(t)
	gv, ov := seamCorpus(t, p)
	probe := []step{
		{"doctor", "decisions", "--mission", "$B", "--json"},
		{"doctor", "review-cycle-reconcile", "--json"},
		{"doctor", "review-cycle-reconcile", "--mission", "$B", "--json"},
	}
	compareSeamSteps(t, p, gv, ov, probe...)
	p.Both(func(s *testkit.Sandbox) {
		for _, l := range strings.Split(s.Git("worktree", "list", "--porcelain"), "\n") {
			if wt, ok := strings.CutPrefix(l, "worktree "); ok && strings.HasSuffix(wt, "-coord") {
				s.Git("worktree", "remove", "--force", wt)
			}
		}
	})
	compareSeamSteps(t, p, gv, ov, probe...)
	p.Both(func(s *testkit.Sandbox) {
		b := missionDirWithPrefix(t, s, "beta")
		var meta map[string]any
		if err := json.Unmarshal([]byte(s.ReadFile("kitty-specs/"+b+"/meta.json")), &meta); err != nil {
			t.Fatal(err)
		}
		cb, _ := meta["coordination_branch"].(string)
		if cb == "" {
			t.Fatal("beta has no coordination_branch")
		}
		s.Git("branch", "-D", cb)
	})
	compareSeamSteps(t, p, gv, ov, probe...)
}

// TestDiffBackfillRuntimeStateFlipPlacement is MAINT-16: a mission
// directory named after another mission's mid8 canonicalizes (the identity
// ladder tries mid8 before the exact directory name) to that other
// mission, so _flip_phase's placement cross-check refuses the flip with
// PlacementMismatchError (recorded per mission in the corpus run).
func TestDiffBackfillRuntimeStateFlipPlacement(t *testing.T) {
	p := newOraclePair(t)
	vars := []map[string]string{}
	p.Both(func(s *testkit.Sandbox) {
		s.Git("switch", "-q", "-c", "feat/x")
		missionfx.RunOracle(t, s, "agent", "mission", "create", "alpha", "--mission-type", "software-dev", "--topology", "single_branch", "--json")
		a := missionDirWithPrefix(t, s, "alpha-")
		mid8 := a[strings.LastIndex(a, "-")+1:]
		var meta map[string]any
		if err := json.Unmarshal([]byte(s.ReadFile("kitty-specs/"+a+"/meta.json")), &meta); err != nil {
			t.Fatal(err)
		}
		meta["mission_id"] = "01KZZZZZZZZZZZZZZZZZZZZZZZ"
		delete(meta, "mid8")
		b, _ := json.MarshalIndent(meta, "", "  ")
		s.WriteFile("kitty-specs/"+mid8+"/meta.json", string(b)+"\n")
		s.WriteFile("kitty-specs/"+mid8+"/spec.md", s.ReadFile("kitty-specs/"+a+"/spec.md"))
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "odd dir")
		vars = append(vars, map[string]string{"$A": a, "$MID": mid8})
	})
	compareStepsVars(t, p, vars[0], vars[1],
		step{"migrate", "backfill-runtime-state", "--dry-run", "--json"},
		step{"migrate", "backfill-runtime-state", "--json"},
		step{"migrate", "backfill-runtime-state"},
		step{"migrate", "backfill-runtime-state", "--mission", "$A", "--json"},
	)
	compareFiles(t, p, vars[0]["$MID"], vars[1]["$MID"], "kitty-specs/$M/meta.json", "kitty-specs/"+"$M"+"/spec.md")
}
