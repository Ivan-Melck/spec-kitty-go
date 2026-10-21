package doctrine

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// CharterKindTokens is CHARTER_KIND_TOKENS: the operator kind universe of
// charter activate / deactivate / list (hand-authorable kinds + mission-type).
var CharterKindTokens = []string{"directive", "tactic", "styleguide", "toolguide", "paradigm", "procedure",
	"agent-profile", "mission-step-contract", "glossary-pack", "skill", "mission-type"}

// MissionTypeToken is the mission-type outlier token.
const MissionTypeToken = "mission-type"

// YAMLKey is YAML_KEY_MAP[token].
func YAMLKey(token string) string {
	if token == MissionTypeToken {
		return "mission_type_activations"
	}
	k, err := ParseKind(token)
	if err != nil {
		return ""
	}
	return "activated_" + k.Plural()
}

// AvailableArtifact is one (config-stem id, layer) availability entry.
type AvailableArtifact struct {
	ID    string
	Layer string
}

// LayerRoots is resolve_layer_roots: {"project": pack root when a dir,
// "org": first existing org root}.
func LayerRoots(repoRoot string) map[string]string {
	out := map[string]string{}
	pr := filepath.Join(repoRoot, filepath.FromSlash(ProjectPackRoot))
	if st, err := os.Stat(pr); err == nil && st.IsDir() {
		out[LayerProject] = pr
	}
	if roots := OrgRoots(repoRoot); len(roots) > 0 {
		out[LayerOrg] = roots[0]
	}
	return out
}

// ConfigStem is the file name with every extension removed.
func ConfigStem(p string) string {
	n := filepath.Base(p)
	if i := strings.Index(n, "."); i >= 0 {
		return n[:i]
	}
	return n
}

func declaredID(b []byte, k Kind) string {
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return ""
	}
	field := "id"
	if k == AgentProfile {
		field = "profile-id"
	}
	v, ok := o.Get(field)
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(pyStr(v))
}

type scanDir struct {
	layer string
	pack  *PackFS
	dir   string // pack-relative
}

// scanDirs is ActiveCharterManager._scan_layer_dirs.
func scanDirs(token string, layerRoots map[string]string, orgChain []string) []scanDir {
	var k Kind
	isMT := token == MissionTypeToken
	if !isMT {
		k, _ = ParseKind(token)
	}
	var out []scanDir
	add := func(layer string, p *PackFS, dir string) {
		if p.Exists(dir) {
			out = append(out, scanDir{layer, p, dir})
		}
	}
	// built-in
	switch {
	case isMT:
		add(LayerBuiltIn, BuiltIn(), "missions/mission_types")
	case k == MissionStepContract:
		add(LayerBuiltIn, BuiltIn(), "missions/built_in_step_contracts")
	default:
		add(LayerBuiltIn, BuiltIn(), k.Plural())
	}
	// org (last-declared first when a chain is supplied)
	var orgs []string
	if len(orgChain) > 0 {
		for i := len(orgChain) - 1; i >= 0; i-- {
			orgs = append(orgs, orgChain[i])
		}
	} else if r := layerRoots[LayerOrg]; r != "" {
		orgs = []string{r}
	}
	for _, r := range orgs {
		switch {
		case isMT:
			add(LayerOrg, DiskPack(r), "mission_types")
		case k == MissionStepContract:
		default:
			add(LayerOrg, DiskPack(r), k.Plural())
		}
	}
	if r := layerRoots[LayerProject]; r != "" {
		switch {
		case isMT:
			add(LayerProject, DiskPack(filepath.Dir(r)), "missions/mission_types")
		case k == MissionStepContract:
		default:
			add(LayerProject, DiskPack(r), ProjectKindDir(k))
		}
	}
	return out
}

// ListAvailable is list_available_detailed: every artifact declaring an id
// (config-stem ids), per layer. Mission types are listed by id.
func ListAvailable(token string, layerRoots map[string]string, orgChain []string) []AvailableArtifact {
	var out []AvailableArtifact
	isMT := token == MissionTypeToken
	var k Kind
	if !isMT {
		k, _ = ParseKind(token)
	}
	for _, sd := range scanDirs(token, layerRoots, orgChain) {
		if isMT {
			for _, rel := range sd.pack.Walk(sd.dir, ".yaml", false) {
				b, err := sd.pack.ReadFile(rel)
				if err != nil {
					continue
				}
				o, err := yamlx.ParseObject(b)
				if err != nil || o == nil {
					continue
				}
				id := o.GetString("id")
				if id == "" {
					id = ConfigStem(rel)
				}
				out = append(out, AvailableArtifact{id, sd.layer})
			}
			continue
		}
		for _, rel := range sd.pack.Walk(sd.dir, k.Suffix(), true) {
			b, err := sd.pack.ReadFile(rel)
			if err != nil || declaredID(b, k) == "" {
				continue
			}
			out = append(out, AvailableArtifact{ConfigStem(rel), sd.layer})
		}
	}
	return out
}

// AvailableIDs is list_available (flat id set).
func AvailableIDs(token string, layerRoots map[string]string, orgChain []string) map[string]bool {
	out := map[string]bool{}
	for _, a := range ListAvailable(token, layerRoots, orgChain) {
		out[a.ID] = true
	}
	return out
}
