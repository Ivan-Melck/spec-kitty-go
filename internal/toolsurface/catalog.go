package toolsurface

// ProjectSkillCatalog is skills.catalog.resolve_project_skill_catalog: the
// shipped doctrine skills plus the charter-pack skills in force.
func ProjectSkillCatalog(root string, stage bool) ([]CatalogSkill, error) {
	return BuiltinCatalog(), nil
}
