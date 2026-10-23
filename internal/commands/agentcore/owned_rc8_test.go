package agentcore_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffOwnedRC8Lifecycle(t *testing.T) {
	for _, adopt := range []bool{false, true} {
		name := "explicit"
		if adopt {
			name = "adopted"
		}
		t.Run(name, func(t *testing.T) {
			p := missionfx.NewOwnedPair(t)
			g, o := missionfx.OracleOwnedMission(t, p)
			cwd := ""
			own := []string{"--owned-checkout", missionfx.OwnedRel}
			if adopt {
				cwd = missionfx.OwnedRel
				own = nil
			}
			with := func(args ...string) []string { return append(args, own...) }
			ownedSteps(t, p, cwd, g, o, [][]string{
				with("agent", "issue-verdict", "--mission", "$M", "--issue", "123", "--verdict", "fixed", "--actor", "me", "--json"),
				with("agent", "issue-verdict", "--mission", "$M", "--issue", "456", "--verdict", "in-mission", "--actor", "me"),
				with("agent", "issue-verdict", "--mission", "$M", "--issue", "123", "--verdict", "fixed", "--actor", "me", "--json"),
				with("agent", "mission", "accept", "--mission", "$M", "--diagnose", "--json"),
			})
			for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
				if s.Exists(filepath.Join("kitty-specs", missionfx.OwnedMissionSlug(t, s), "issue-matrix.json")) {
					t.Error("issue matrix written into root checkout")
				}
			}
			goRows := missionfx.OwnedSandbox(p.Go).ReadFile(filepath.Join("kitty-specs", g, "issue-matrix.json"))
			orRows := missionfx.OwnedSandbox(p.Oracle).ReadFile(filepath.Join("kitty-specs", o, "issue-matrix.json"))
			if goRows != orRows {
				t.Error(testkit.Diff(orRows, goRows))
			}
		})
	}
}
