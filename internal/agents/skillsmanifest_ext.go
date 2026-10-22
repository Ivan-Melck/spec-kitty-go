package agents

import (
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// RenderSkillsManifest is skills.manifest._render_manifest (json.dumps
// indent=2 of the dataclass, default provenance keys omitted, plus "\n").
func RenderSkillsManifest(m *SkillsManifest) string { return renderSkillsManifest(m) }

// Provenance returns the entry's (origin, source_ref, source_hash) with the
// dataclass defaults ("builtin", "", "") when unrecorded.
func (e SkillEntry) Provenance() (origin, ref, hash string) {
	origin = "builtin"
	for _, kv := range e.extra {
		s, _ := kv.Value.(string)
		switch kv.Key {
		case "origin":
			origin = s
		case "source_ref":
			ref = s
		case "source_hash":
			hash = s
		}
	}
	return origin, ref, hash
}

// WithProvenance returns e recording the pack provenance fields; values
// equal to the dataclass defaults are omitted like _render_manifest does.
func (e SkillEntry) WithProvenance(origin, ref, hash string) SkillEntry {
	e.extra = nil
	if origin != "" && origin != "builtin" {
		e.extra = append(e.extra, jsonx.KV{Key: "origin", Value: origin})
	}
	if ref != "" {
		e.extra = append(e.extra, jsonx.KV{Key: "source_ref", Value: ref})
	}
	if hash != "" {
		e.extra = append(e.extra, jsonx.KV{Key: "source_hash", Value: hash})
	}
	return e
}

// SameEntry is ManagedFileEntry equality (every field, provenance included).
func SameEntry(a, b SkillEntry) bool {
	if a.SkillName != b.SkillName || a.SourceFile != b.SourceFile || a.InstalledPath != b.InstalledPath ||
		a.InstallationClass != b.InstallationClass || a.AgentKey != b.AgentKey || a.ContentHash != b.ContentHash ||
		a.InstalledAt != b.InstalledAt || a.DeliveryMode != b.DeliveryMode {
		return false
	}
	ao, ar, ah := a.Provenance()
	bo, br, bh := b.Provenance()
	return ao == bo && ar == br && ah == bh
}
