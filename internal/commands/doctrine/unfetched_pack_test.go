package doctrine_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// declareUnfetchedPack declares an org pack whose root was never fetched.
// Every governance surface that reads the declared chain strictly
// (resolve_pack_chain(strict=True), the effective-set seam) must fail
// closed the way upstream does; lenient surfaces skip the pack.
func declareUnfetchedPack(s *testkit.Sandbox) {
	cfg := s.ReadFile(".kittify/config.yaml")
	s.WriteFile(".kittify/config.yaml", cfg+"charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: packs/acme\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "declare acme")
}

func unfetchedPair(t *testing.T) *testkit.Pair {
	p := missionfx.NewOraclePair(t)
	p.Both(declareUnfetchedPack)
	return p
}

func compareUnfetched(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	name := strings.Join(args, " ")
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\ngo stderr: %s\noracle stderr: %s", name, g.Code, o.Code, g.Stderr, o.Stderr)
	}
	if gs, os_ := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout); gs != os_ {
		t.Errorf("%s: stdout differs\n%s", name, testkit.Diff(os_, gs))
	}
	gs, os_ := testkit.MaskTracebackFrames(p.Go.Normalize(g.Stderr)), testkit.MaskTracebackFrames(p.Oracle.Normalize(o.Stderr))
	if gs != os_ {
		t.Errorf("%s: stderr differs\n%s", name, testkit.Diff(os_, gs))
	}
}

func TestUnfetchedOrgPackSweepMatchesOracle(t *testing.T) {
	p := unfetchedPair(t)
	for _, args := range [][]string{
		{"charter", "status"},
		{"charter", "status", "--json"},
		{"charter", "context", "--action", "implement", "--mission-type", "software-dev", "--json", "--no-mark-loaded"},
		{"charter", "context", "--action", "specify", "--mission-type", "software-dev", "--no-mark-loaded"},
		{"charter", "list"},
		{"charter", "list", "--show-available"},
		{"charter", "lint"},
		{"charter", "validate"},
		{"charter", "consistency-check", "--json"},
		{"charter", "preflight"},
		{"charter", "bundle", "validate"},
		{"charter", "mission-type", "list"},
		{"charter", "pack", "list"},
		{"charter", "pack", "validate"},
		{"doctor", "skills"},
		{"doctor", "skills", "--json"},
		{"doctor", "charter-packs"},
		{"doctor", "charter-packs", "--json"},
		{"doctor", "tool-surfaces", "--json"},
		{"profiles", "list"},
		{"agent", "profile", "list"},
		{"agent", "config", "status"},
		{"verify-setup", "--json"},
		{"mission-type", "list"},
		{"agent", "config", "sync", "--dry-run"},
	} {
		compareUnfetched(t, p, args...)
	}
}

func TestUnfetchedOrgPackMutationsMatchOracle(t *testing.T) {
	for _, args := range [][]string{
		{"charter", "activate", "directive", "DIRECTIVE_024"},
		{"charter", "activate", "procedure", "situational-assessment"},
		{"charter", "activate", "mission-type", "software-dev"},
		{"charter", "activate", "skill", "spec-kitty-runtime-next"},
		{"charter", "deactivate", "directive", "DIRECTIVE_024"},
		{"charter", "interview", "--defaults", "--json"},
		{"charter", "generate", "--json"},
		{"charter", "synthesize", "--json"},
		{"charter", "resynthesize", "--json"},
		{"agent", "config", "sync"},
		{"dispatch", "fix the typo in README", "--json"},
		{"regen"},
	} {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			p := unfetchedPair(t)
			compareUnfetched(t, p, args...)
			p.CompareTree(".kittify", true)
		})
	}
}
