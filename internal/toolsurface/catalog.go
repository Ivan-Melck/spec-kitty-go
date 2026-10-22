package toolsurface

import "fmt"

// ProjectSkillCatalog is skills.catalog.resolve_project_skill_catalog: the
// shipped doctrine skills plus the charter-pack skills in force (tagged
// origin "pack"). stage=true materializes them under
// .kittify/runtime/pack-skills (install/apply paths only).
func ProjectSkillCatalog(root string, stage bool) ([]CatalogSkill, error) {
	cat := BuiltinCatalog()
	rendered, err := PreparePackSkills(root)
	if err != nil {
		return nil, err
	}
	if stage {
		if err := stagePackSkills(root, rendered); err != nil {
			return nil, err
		}
	}
	for _, r := range rendered {
		r := r
		cat = append(cat, CatalogSkill{Name: r.RenderedName, Files: []string{"SKILL.md"}, SourceMode: 0o644, Origin: "pack",
			SourceRef: portableRef(r.SourcePath, root), SourceHash: r.SourceHash,
			Read: func(rel string) ([]byte, error) {
				if rel != "SKILL.md" {
					return nil, fmt.Errorf("no such pack skill file: %s", rel)
				}
				return []byte(r.Text), nil
			}})
	}
	return cat, nil
}
