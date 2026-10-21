package agentcore_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// Short local names for the shared mission fixtures (internal/testkit/missionfx).

const localSlug = missionfx.LocalSlug

func tdPath(rel string) string                 { return missionfx.Path(rel) }
func td(t testing.TB, rel string) string       { t.Helper(); return missionfx.Read(t, rel) }
func newProject(t testing.TB) *testkit.Sandbox { t.Helper(); return missionfx.NewProject(t) }
func newOraclePair(t testing.TB) *testkit.Pair { t.Helper(); return missionfx.NewOraclePair(t) }
func missionSlug(t testing.TB, s *testkit.Sandbox) string {
	t.Helper()
	return missionfx.MissionSlug(t, s)
}
func runOracle(t testing.TB, s *testkit.Sandbox, args ...string) testkit.Result {
	t.Helper()
	return missionfx.RunOracle(t, s, args...)
}
func authorCalc(t testing.TB, s *testkit.Sandbox, slug string) {
	t.Helper()
	missionfx.AuthorCalc(t, s, slug)
}
func oracleCalcMission(t testing.TB, p *testkit.Pair, createArgs ...string) (string, string) {
	t.Helper()
	return missionfx.OracleCalcMission(t, p, createArgs...)
}
func newLocalMission(t *testing.T) *testkit.Sandbox { t.Helper(); return missionfx.NewLocalMission(t) }
func seedRepo(s *testkit.Sandbox)                   { missionfx.SeedRepo(s) }
func oracleInit(t testing.TB, s *testkit.Sandbox)   { t.Helper(); missionfx.OracleInit(t, s) }
