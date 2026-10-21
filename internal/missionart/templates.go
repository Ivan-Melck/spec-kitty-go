package missionart

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// planTemplateNames is the configured plan template per mission type.
var planTemplateNames = map[string]string{
	"software-dev":  "plan-template.md",
	"research":      "research-plan-template.md",
	"documentation": "documentation-plan-template.md",
	"plan":          "plan-plan-skeleton.md",
}

// PlanTemplateName is the configured "plan" template filename.
func PlanTemplateName(missionType string) string {
	if n, ok := planTemplateNames[missionType]; ok {
		return n
	}
	return "plan-template.md"
}

// ResolveTemplate is runtime.resolver.resolve_template (org-pack tier
// omitted): returns the template bytes or false.
func ResolveTemplate(name, root, missionType string) ([]byte, bool) {
	home := project.GlobalKittify()
	for _, p := range []string{
		filepath.Join(root, ".kittify", "overrides", "templates", name),
		filepath.Join(root, ".kittify", "templates", name),
		filepath.Join(home, "missions", missionType, "templates", name),
		filepath.Join(home, "templates", name),
	} {
		if b, err := os.ReadFile(p); err == nil {
			return b, true
		}
	}
	if b, err := assets.ReadFile("packs/built-in/missions/" + missionType + "/templates/" + name); err == nil {
		return b, true
	}
	return nil, false
}
