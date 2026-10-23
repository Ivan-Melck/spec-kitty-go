package toolsurface

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/agents"
)

// Presence is SurfacePresenceIndex.build: expand the surface plan once,
// then roll command-file and command-skill instances up to their managed
// directories. Other kinds still participate in plan expansion, including
// the profile repository and its provenance checks.
func Presence(root string, tools []string) map[string]bool {
	ps := providers()
	plans := buildPlansWith(newCtx(root), ps, &registry{keys: tools}, tools, root, nil)
	present := map[string]bool{}
	for _, plan := range plans {
		for _, inst := range plan.Instances {
			if inst.Path == "<unsupported>" {
				continue
			}
			var dir string
			switch inst.Def.Kind {
			case KindCommandFile:
				dir = agents.GlobalCommandDir(plan.Tool)
			case KindCommandSkill:
				dir = filepath.Join(root, ".agents", "skills")
			default:
				continue
			}
			if _, err := os.Stat(dir); err == nil {
				present[plan.Tool] = true
			}
		}
	}
	return present
}
