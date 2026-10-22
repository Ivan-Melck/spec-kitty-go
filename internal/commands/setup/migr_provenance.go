package setup

// Provenance-healing migrations: 4_0_0rc5_heal_template_set_provenance and
// 3.2.7_heal_provenance_paths. An absolute built-in pack path is recognised
// by its ".../packs/built-in/..." shape (the Go binary embeds the pack, so
// there is no installed pack root to compare against).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() {
	migrationImpls["4_0_0rc5_heal_template_set_provenance"] = mHealTemplateSetProvenance()
	migrationImpls["3.2.7_heal_provenance_paths"] = mHealProvenancePaths()
}

func charterYAMLPath(root string) string { return filepath.Join(root, ".kittify", "charter", "charter.yaml") }

func isAbsAnyOS(p string) bool { return filepath.IsAbs(p) || isWindowsAbs(p) }

// builtInSuffix returns the "built-in/..." suffix of an absolute path under
// a packs/built-in root ("" when the shape does not match).
func builtInSuffix(p string) string {
	parts := strings.Split(strings.ReplaceAll(p, "\\", "/"), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] == "packs" && parts[i+1] == "built-in" {
			return strings.Join(parts[i+1:], "/")
		}
	}
	return ""
}

func portableToken(p string) string {
	if s := builtInSuffix(p); s != "" {
		return "${SPEC_KITTY_PACKS_ROOT}/" + s
	}
	return p
}

func loadCharterDoc(root string) (*jsonx.Object, error) {
	b, err := os.ReadFile(charterYAMLPath(root))
	if err != nil {
		return nil, err
	}
	d, err := yamlx.ParseObject(b)
	if err != nil {
		return nil, failClosed("%s could not be read (YAMLError); provenance was not evaluated", charterYAMLPath(root))
	}
	if d == nil {
		d = &jsonx.Object{}
	}
	return d, nil
}

type healRef struct {
	ref   *jsonx.Object
	token string
}

func healableTemplateSetRefs(root string, doc *jsonx.Object) ([]healRef, error) {
	if !isFile(charterYAMLPath(root)) {
		return nil, nil
	}
	if doc == nil {
		var err error
		if doc, err = loadCharterDoc(root); err != nil {
			return nil, err
		}
	}
	cat := doc.GetObject("catalog")
	if cat == nil {
		return nil, nil
	}
	mission := cat.GetString("mission")
	ts := cat.GetString("template_set")
	if ts == "" || mission == "" || strings.ContainsAny(mission, "/\\") || mission == "." || mission == ".." {
		return nil, nil
	}
	if !assets.Exists("packs/built-in/missions/" + mission + "/mission.yaml") {
		return nil, nil
	}
	token := "${SPEC_KITTY_PACKS_ROOT}/built-in/missions/" + mission + "/mission.yaml"
	refs, _ := cat.Get("references")
	l, _ := refs.([]any)
	var out []healRef
	for _, r := range l {
		o, isObj := r.(*jsonx.Object)
		if !isObj || o.GetString("kind") != "template_set" || o.GetString("id") != "TEMPLATE_SET:"+ts {
			continue
		}
		sp := o.GetString("source_path")
		if sp == "" || !isAbsAnyOS(sp) || builtInSuffix(sp) != "built-in/missions/"+mission+"/mission.yaml" {
			continue
		}
		out = append(out, healRef{o, token})
	}
	return out, nil
}

func mHealTemplateSetProvenance() migrationImpl {
	return migrationImpl{
		noWorktrees: true,
		detectE: func(root string) (bool, error) {
			h, err := healableTemplateSetRefs(root, nil)
			return len(h) > 0, err
		},
		canApply: func(root string) (bool, string) {
			if h, _ := healableTemplateSetRefs(root, nil); len(h) > 0 {
				return ok()
			}
			return false, "no stale built-in template_set source_path found in charter.yaml"
		},
		applyE: func(root string, dryRun bool) (migrationResult, error) {
			if !isFile(charterYAMLPath(root)) {
				return migrationResult{Success: true}, nil
			}
			doc, err := loadCharterDoc(root)
			if err != nil {
				return migrationResult{}, err
			}
			h, _ := healableTemplateSetRefs(root, doc)
			if len(h) == 0 {
				return migrationResult{Success: true}, nil
			}
			var ch []string
			for _, r := range h {
				old := r.ref.GetString("source_path")
				ch = append(ch, fmt.Sprintf("charter.yaml catalog[%s].source_path: %s -> %s", r.ref.GetString("id"), old, r.token))
				if !dryRun {
					r.ref.Set("source_path", r.token)
				}
			}
			if !dryRun {
				writeCharterYAMLDoc(charterYAMLPath(root), doc)
			}
			return migrationResult{Success: true, Changes: ch}, nil
		},
	}
}

func catalogHealableRefs(doc *jsonx.Object) []*jsonx.Object {
	cat := doc.GetObject("catalog")
	if cat == nil {
		return nil
	}
	refs, _ := cat.Get("references")
	l, _ := refs.([]any)
	var out []*jsonx.Object
	for _, r := range l {
		o, isObj := r.(*jsonx.Object)
		if !isObj || o.GetString("kind") == "template_set" {
			continue
		}
		sp := o.GetString("source_path")
		if sp != "" && filepath.IsAbs(sp) && builtInSuffix(sp) != "" {
			out = append(out, o)
		}
	}
	return out
}

func manifestHealable(root string) ([]string, *jsonx.Object) {
	b, err := os.ReadFile(agents.ProfileManifestPath(root))
	if err != nil {
		return nil, nil
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, nil
	}
	var out []string
	for i, e := range o.GetList("entries") {
		eo, isObj := e.(*jsonx.Object)
		if !isObj {
			continue
		}
		sp := eo.GetString("source_path")
		if sp != "" && filepath.IsAbs(sp) && builtInSuffix(sp) != "" {
			out = append(out, fmt.Sprint(i))
		}
	}
	return out, o
}

func mHealProvenancePaths() migrationImpl {
	detect := func(root string) bool {
		if doc, err := loadCharterDoc(root); err == nil && len(catalogHealableRefs(doc)) > 0 {
			return true
		}
		h, _ := manifestHealable(root)
		return len(h) > 0
	}
	return migrationImpl{
		noWorktrees: true,
		detect:      detect,
		canApply: func(root string) (bool, string) {
			if detect(root) {
				return ok()
			}
			return false, "no absolute built-in-pack source_path found in charter.yaml's catalog or agent_profiles_manifest.json"
		},
		apply: func(root string, dryRun bool) migrationResult {
			var ch []string
			if doc, err := loadCharterDoc(root); err == nil {
				refs := catalogHealableRefs(doc)
				for _, r := range refs {
					old := r.GetString("source_path")
					nv := portableToken(old)
					id := r.GetString("id")
					if id == "" {
						id = "?"
					}
					ch = append(ch, fmt.Sprintf("charter.yaml catalog[%s].source_path: %s -> %s", id, old, nv))
					if !dryRun {
						r.Set("source_path", nv)
					}
				}
				if len(refs) > 0 && !dryRun {
					writeCharterYAMLDoc(charterYAMLPath(root), doc)
				}
			}
			idx, m := manifestHealable(root)
			if len(idx) > 0 {
				entries := m.GetList("entries")
				for _, e := range entries {
					eo, isObj := e.(*jsonx.Object)
					if !isObj {
						continue
					}
					sp := eo.GetString("source_path")
					if sp == "" || !filepath.IsAbs(sp) || builtInSuffix(sp) == "" {
						continue
					}
					nv := portableToken(sp)
					ch = append(ch, fmt.Sprintf("agent_profiles_manifest.json[%s/%s].source_path: %s -> %s", eo.GetString("profile_urn"), eo.GetString("tool_key"), sp, nv))
					if !dryRun {
						eo.Set("source_path", nv)
					}
				}
				if !dryRun {
					os.WriteFile(agents.ProfileManifestPath(root), []byte(jsonx.Dumps(m, jsonx.SortedPretty)+"\n"), 0o644)
				}
			}
			return migrationResult{Success: true, Changes: ch}
		},
	}
}
