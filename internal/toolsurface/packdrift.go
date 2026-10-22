package toolsurface

import "github.com/spec-kitty/go-kitty3/internal/jsonx"

// PackSkillFinding is skills.pack_skill_drift.PackSkillFinding.
type PackSkillFinding struct {
	Kind          string // missing, drift, stale
	SkillName     string
	InstalledPath string
	SourceRef     string
	Message       string
}

// JSON is PackSkillFinding.to_dict.
func (f PackSkillFinding) JSON() *jsonx.Object {
	return jsonx.NewObject("kind", f.Kind, "skill_name", f.SkillName, "installed_path", f.InstalledPath, "source_ref", f.SourceRef, "message", f.Message)
}

// FindPackSkillFindings is pack_skill_drift.find_pack_skill_findings.
func FindPackSkillFindings(root string) []PackSkillFinding {
	return nil
}
