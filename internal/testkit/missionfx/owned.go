package missionfx

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// Owned-checkout fixture (upstream tests/integration/conftest.py
// make_owned_checkouts / the e2e walk's _build_site): the repository root
// checkout R is the sandbox project (oracle-initialised), the owned checkout
// P is a linked worktree on OwnedBranch at <BASE>/owned and a sibling linked
// checkout S sits at <BASE>/sibling on SiblingBranch.
const (
	OwnedBranch   = "kitty/demo-owned"
	SiblingBranch = "codex/sibling"
	// OwnedRel / SiblingRel are P and S relative to R (the sandbox Dir).
	OwnedRel   = "../owned"
	SiblingRel = "../sibling"
)

// OwnedDir returns P's absolute path in s.
func OwnedDir(s *testkit.Sandbox) string { return filepath.Join(s.Base, "owned") }

// SiblingDir returns S's absolute path in s.
func SiblingDir(s *testkit.Sandbox) string { return filepath.Join(s.Base, "sibling") }

// AddOwnedCheckouts creates P and S as linked worktrees of s's project.
func AddOwnedCheckouts(s *testkit.Sandbox) {
	s.Git("worktree", "add", "-q", "-b", OwnedBranch, OwnedDir(s))
	s.Git("worktree", "add", "-q", "-b", SiblingBranch, SiblingDir(s))
}

// NewOwnedPair returns an oracle-initialised pair whose projects each carry
// an owned checkout P and a sibling checkout S.
func NewOwnedPair(t testing.TB) *testkit.Pair {
	t.Helper()
	p := NewOraclePair(t)
	p.Both(AddOwnedCheckouts)
	return p
}

// GitIn runs git in dir (absolute) inside s and returns trimmed stdout.
func GitIn(t testing.TB, s *testkit.Sandbox, dir string, args ...string) string {
	t.Helper()
	r := s.Exec(dir, "git", args...)
	if r.Code != 0 {
		t.Fatalf("git %s in %s: %s%s", strings.Join(args, " "), dir, r.Stdout, r.Stderr)
	}
	return strings.TrimSpace(r.Stdout)
}

// OracleOwnedMission creates the owned single_branch "demo" mission in P
// with the ORACLE in both sandboxes (run from R) and returns the mission
// directory names (go, oracle).
func OracleOwnedMission(t testing.TB, p *testkit.Pair) (goSlug, oracleSlug string) {
	t.Helper()
	return OracleOwnedMissionNamed(t, p, "demo")
}

// OwnedCalcMission creates the owned single_branch "calc-ops" mission in P
// with the oracle in both sandboxes and authors the calc spec / plan / tasks
// / WP files there (committed on OwnedBranch; not finalized).
func OwnedCalcMission(t testing.TB, p *testkit.Pair) (goSlug, oracleSlug string) {
	t.Helper()
	g, o := OracleOwnedMissionNamed(t, p, "calc-ops")
	p.Both(func(s *testkit.Sandbox) {
		AuthorCalc(t, OwnedSandbox(s), OwnedMissionSlug(t, s))
	})
	return g, o
}

// OracleOwnedMissionNamed is OracleOwnedMission for mission name.
func OracleOwnedMissionNamed(t testing.TB, p *testkit.Pair, name string) (goSlug, oracleSlug string) {
	t.Helper()
	var slugs []string
	p.Both(func(s *testkit.Sandbox) {
		RunOracle(t, s, "agent", "mission", "create", name, "--mission-type", "software-dev", "--owned-checkout", OwnedRel,
			"--target-branch", OwnedBranch, "--topology", "single_branch", "--branch-strategy", "already-confirmed", "--json")
		slugs = append(slugs, OwnedMissionSlug(t, s))
	})
	return slugs[0], slugs[1]
}

// OwnedMissionSlug returns the single mission directory name in s's P.
func OwnedMissionSlug(t testing.TB, s *testkit.Sandbox) string {
	t.Helper()
	sub := &testkit.Sandbox{T: s.T, Base: s.Base, Home: s.Home, Dir: OwnedDir(s), Env: s.Env}
	return MissionSlug(t, sub)
}

// OwnedSandbox returns a view of s whose project Dir is P (for Exists /
// ReadFile / WriteFile / Git in the owned checkout).
func OwnedSandbox(s *testkit.Sandbox) *testkit.Sandbox {
	return &testkit.Sandbox{T: s.T, Base: s.Base, Home: s.Home, Dir: OwnedDir(s), Env: s.Env}
}
