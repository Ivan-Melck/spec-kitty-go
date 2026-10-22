package autorebase

// ResidueContext.is_disposable (coordination.coherence._is_disposable_residue)
// for one mission and one checkout role.

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Checkout roles (CheckoutRole).
const (
	RoleLane           = "lane"
	RoleCoordination   = "coordination"
	RoleRepositoryRoot = "repository_root"
)

var derivedViewRe = regexp.MustCompile(`(?:^|/)\.kittify/derived/([^/]+)/[^/]+$`)

// Residue is ResidueContext.for_mission(root, slug, role).is_disposable; nil
// when the mission has no meta.json (nothing is residue then).
func Residue(root, slug, role string) func(string) bool {
	if slug == "" {
		return nil
	}
	meta, err := mission.ReadMeta(project.Specs(root, slug))
	if err != nil || meta == nil {
		return nil
	}
	topo := meta.GetString("topology")
	if topo == "" && strings.TrimSpace(meta.GetString("coordination_branch")) == "" {
		topo = "lanes"
	}
	coordTopo := topo != "lanes" && topo != "single_branch"
	own := map[string]bool{slug: true}
	if mid8 := meta.GetString("mid8"); mid8 != "" {
		own[mission.CoordMissionDirName(slug, mid8)] = true
	}
	return func(p string) bool {
		n := strings.TrimRight(filepath.ToSlash(p), "/")
		if m := derivedViewRe.FindStringSubmatch(n); m != nil {
			if !own[m[1]] {
				return false
			}
		} else if parts := strings.Split(n, "/"); len(parts) > 0 {
			for i, s := range parts {
				if s == "kitty-specs" {
					if len(parts)-i-1 >= 2 && !own[parts[i+1]] {
						return false
					}
					break
				}
			}
		}
		if acceptance.IsSelfBookkeeping(n) {
			return true
		}
		inOwn := false
		parts := strings.Split(n, "/")
		for i, s := range parts {
			if s == "kitty-specs" && i+1 < len(parts) && own[parts[i+1]] {
				inOwn = true
				break
			}
		}
		if !inOwn {
			return false
		}
		kind := mission.ArtifactKindForPath(n)
		if role == RoleCoordination {
			return kind == mission.KindStatusState
		}
		return coordTopo && mission.CoordinationKinds[kind]
	}
}
