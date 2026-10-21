package healthcheck

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func revList(root, rng string) ([]string, bool) {
	out, err := gitx.Run(root, "rev-list", rng)
	if err != nil {
		return nil, false
	}
	return strings.Fields(out), true
}

func commitPaths(root, c string) ([]string, bool) {
	out, err := gitx.Run(root, "diff-tree", "--no-commit-id", "--name-only", "-r", "-m", "--first-parent", c)
	if err != nil {
		return nil, false
	}
	return strings.Fields(out), true
}

func isCoordOnlyPath(p, dirName string) bool {
	parts := strings.Split(p, "/")
	if len(parts) < 3 || parts[0] != "kitty-specs" || parts[1] != dirName {
		return false
	}
	return mission.CoordinationKinds[mission.ArtifactKindForPath(p)]
}

// expectedDivergence is missions._create.is_expected_coordination_divergence:
// the coordination branch is ahead of target only by this mission's
// COORD-partition commits, and target never touched those paths.
func expectedDivergence(root, coordBranch, target, dirName string) bool {
	mb, err := gitx.Run(root, "merge-base", coordBranch, target)
	if err != nil || strings.TrimSpace(mb) == "" {
		return false
	}
	cs, ok := revList(root, target+".."+coordBranch)
	if !ok {
		return false
	}
	for _, c := range cs {
		ps, ok := commitPaths(root, c)
		if !ok {
			return false
		}
		for _, p := range ps {
			if !isCoordOnlyPath(p, dirName) {
				return false
			}
		}
	}
	ts, ok := revList(root, strings.TrimSpace(mb)+".."+target)
	if !ok {
		return false
	}
	for _, c := range ts {
		ps, ok := commitPaths(root, c)
		if !ok {
			return false
		}
		for _, p := range ps {
			if isCoordOnlyPath(p, dirName) {
				return false
			}
		}
	}
	return true
}
