package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const legacyIntentSlug = "mission-type-scope-directive"
const projectIntentSlug = "project-intent-directive"

var legacyIntentFile = regexp.MustCompile(`(\d+)-mission-type-scope-directive\.directive\.yaml`)

type LegacyRenameRefusedError struct{ Reasons []string }

func (e *LegacyRenameRefusedError) Error() string {
	return "Refused to rename the legacy 'mission-type-scope-directive' directive to 'project-intent-directive': " + strings.Join(e.Reasons, "; ") + ". Nothing was changed. Resolve the above and re-run."
}

type RenameReport struct {
	DryRun  bool
	Actions []string
}

func renameIntentText(text string) string {
	return legacyIntentFile.ReplaceAllStringFunc(text, func(text string) string {
		number := legacyIntentFile.FindStringSubmatch(text)[1]
		return ArtifactFilename("directive", projectIntentSlug, "PROJECT_"+number)
	})
}
func renameIntentStem(token string) string {
	match := legacyIntentStem.FindStringSubmatch(token)
	if match == nil {
		return token
	}
	return strings.TrimSuffix(ArtifactFilename("directive", projectIntentSlug, "PROJECT_"+match[1]), ".directive.yaml")
}
func readRenameMapping(p string) (*jsonx.Object, error) {
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	value, err := yamlx.Parse(content)
	if err != nil {
		return nil, err
	}
	data, ok := value.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("top-level YAML document must be a mapping")
	}
	return data, nil
}
func writeRenameMapping(p string, data *jsonx.Object) error {
	return atomicfile.Write(p, []byte(canonicalYAML(data)))
}

// RenameLegacyProjectIntentDirective preflights the complete conversion before
// changing any artifact, provenance, manifest, graph, or activation token.
func RenameLegacyProjectIntentDirective(root string, dry bool) (*RenameReport, error) {
	report := &RenameReport{DryRun: dry}
	var refusals, soft []string
	var writes []func() error
	rel := func(p string) string { name, _ := filepath.Rel(root, p); return filepath.ToSlash(name) }
	unreadable := func(p string, err error) string { return rel(p) + " is unreadable (" + err.Error() + ")" }
	collision := func(src, dst string) string {
		return rel(dst) + " already exists, so " + rel(src) + " cannot move onto it"
	}
	// Artifact and harness input filenames retain their declared PROJECT id.
	for _, directory := range []string{dct.ProjectPackRoot + "/directive", ".kittify/charter/generated/directives"} {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(directory), "*-"+legacyIntentSlug+".directive.yaml"))
		for _, src := range matches {
			name := filepath.Base(src)
			match := legacyIntentFile.FindStringSubmatch(name)
			if match == nil || match[0] != name {
				continue
			}
			dst := filepath.Join(filepath.Dir(src), renameIntentText(name))
			if exists(dst) {
				refusals = append(refusals, collision(src, dst))
			} else {
				report.Actions = append(report.Actions, "move "+rel(src)+" -> "+rel(dst))
				writes = append(writes, func() error { return os.Rename(src, dst) })
			}
		}
	}
	// Provenance is not the artifact body: rewrite only the documented fields.
	provenanceRoot := filepath.Join(root, ".kittify/charter/provenance")
	provenanceNew := ".kittify/charter/provenance/directive-" + projectIntentSlug + ".yaml"
	matches, _ := filepath.Glob(filepath.Join(provenanceRoot, "directive-*.yaml"))
	for _, src := range matches {
		data, err := readRenameMapping(src)
		if err != nil {
			if strings.Contains(filepath.Base(src), legacyIntentSlug) {
				refusals = append(refusals, unreadable(src, err))
			}
			continue
		}
		updated := data.Clone()
		if data.GetString("artifact_slug") == legacyIntentSlug {
			updated.Set("artifact_slug", projectIntentSlug)
		}
		if value, ok := data.Get("source_section"); ok {
			if text, ok := value.(string); ok {
				updated.Set("source_section", renameIntentText(text))
			}
		}
		if value, ok := data.Get("source_input_ids"); ok {
			if inputs, ok := value.([]any); ok {
				renamed := append([]any{}, inputs...)
				for i, value := range renamed {
					if text, ok := value.(string); ok {
						renamed[i] = renameIntentText(text)
					}
				}
				updated.Set("source_input_ids", renamed)
			}
		}
		if reflect.DeepEqual(data, updated) {
			continue
		}
		dst := src
		if data.GetString("artifact_slug") == legacyIntentSlug {
			dst = filepath.Join(root, filepath.FromSlash(provenanceNew))
		}
		if dst != src && exists(dst) {
			existing, err := readRenameMapping(dst)
			if err != nil || jsonx.Dumps(existing, jsonx.SortedPretty) != jsonx.Dumps(updated, jsonx.SortedPretty) {
				refusals = append(refusals, collision(src, dst))
				continue
			}
		}
		action := "rewrite provenance " + rel(src)
		if dst != src {
			action = "move and rewrite provenance " + rel(src) + " -> " + rel(dst)
		}
		report.Actions = append(report.Actions, action)
		writes = append(writes, func() error {
			if err := writeRenameMapping(dst, updated); err != nil {
				return err
			}
			if dst != src {
				return os.Remove(src)
			}
			return nil
		})
	}
	// Manifest entries are validated and re-finalized through the same model as synthesis.
	mp := filepath.Join(root, ManifestPath)
	if info, err := os.Stat(mp); err == nil && info.Mode().IsRegular() {
		manifest, err := LoadSynthManifest(mp)
		if err != nil {
			soft = append(soft, unreadable(mp, err))
		} else {
			renamed := append([]ManifestEntry(nil), manifest.Artifacts...)
			for i, e := range renamed {
				if e.Kind != "directive" {
					continue
				}
				if e.Slug == legacyIntentSlug {
					e.Slug = projectIntentSlug
				}
				e.Path = renameIntentText(e.Path)
				if e.ProvenancePath == ".kittify/charter/provenance/directive-"+legacyIntentSlug+".yaml" {
					e.ProvenancePath = provenanceNew
				}
				renamed[i] = e
			}
			if !reflect.DeepEqual(renamed, manifest.Artifacts) {
				report.Actions = append(report.Actions, "rewrite "+rel(mp)+" (path/slug/provenance_path, manifest_hash)")
				for _, field := range []string{"slug", "path"} {
					seen, duplicate := map[string]bool{}, false
					for _, e := range renamed {
						if e.Kind != "directive" {
							continue
						}
						value := e.Slug
						if field == "path" {
							value = e.Path
						}
						duplicate = duplicate || seen[value]
						seen[value] = true
					}
					if duplicate {
						refusals = append(refusals, "the synthesis manifest would list two directive entries with the same "+field+" after the rename")
					}
				}
				manifest.Artifacts = renamed
				writes = append(writes, func() error { return writeRenameMapping(mp, manifest.Finalize().Object(true)) })
			}
		}
	}
	// The historical mission-type edge is dead; all other graph data is retained.
	graphPath := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), "graph.yaml")
	if info, err := os.Stat(graphPath); err == nil && info.Mode().IsRegular() {
		data, err := readRenameMapping(graphPath)
		if err != nil {
			soft = append(soft, unreadable(graphPath, err))
		} else {
			updated := data.Clone()
			relabeled, dropped := false, false
			if value, ok := data.Get("nodes"); ok {
				if nodes, ok := value.([]any); ok {
					renamed := append([]any{}, nodes...)
					for i, value := range nodes {
						if node, ok := value.(*jsonx.Object); ok && node.GetString("kind") == "directive" && (node.GetString("label") == "Mission Type Directive" || node.GetString("label") == "Mission Type Scope Directive") {
							n := node.Clone()
							n.Set("label", "Project Intent Directive")
							renamed[i] = n
							relabeled = true
						}
					}
					updated.Set("nodes", renamed)
				}
			}
			if value, ok := data.Get("edges"); ok {
				if edges, ok := value.([]any); ok {
					kept := []any{}
					for _, value := range edges {
						if edge, ok := value.(*jsonx.Object); ok && edge.GetString("relation") == "requires" && strings.HasPrefix(edge.GetString("source"), "directive:PROJECT_") && strings.HasPrefix(edge.GetString("target"), "mission_type:") {
							dropped = true
						} else {
							kept = append(kept, value)
						}
					}
					updated.Set("edges", kept)
				}
			}
			if relabeled {
				report.Actions = append(report.Actions, "relabel the directive overlay node as 'Project Intent Directive' in "+rel(graphPath))
			}
			if dropped {
				report.Actions = append(report.Actions, "drop the dead 'requires --> mission_type:*' overlay edge in "+rel(graphPath))
			}
			if relabeled || dropped {
				writes = append(writes, func() error { return writeRenameMapping(graphPath, updated) })
			}
		}
	}
	target, err := LoadWriteTarget(root)
	if err != nil {
		soft = append(soft, "the activation write target is unreadable ("+err.Error()+")")
	} else {
		changed := false
		for _, key := range ActivationYAMLKeys() {
			value, ok := target.Data.Get(key)
			if !ok {
				continue
			}
			tokens, ok := value.([]any)
			if !ok {
				continue
			}
			hasLegacy := false
			for _, value := range tokens {
				if text, ok := value.(string); ok && renameIntentStem(text) != text {
					hasLegacy = true
				}
			}
			if !hasLegacy {
				continue
			}
			report.Actions = append(report.Actions, "rewrite '"+key+"' stem token(s) in "+rel(target.Path))
			changed = true
			renamed := []any{}
			for _, value := range tokens {
				if text, ok := value.(string); ok {
					value = renameIntentStem(text)
				}
				found := false
				for _, old := range renamed {
					found = found || reflect.DeepEqual(old, value)
				}
				if !found {
					renamed = append(renamed, value)
				}
			}
			target.Data.Set(key, renamed)
		}
		if changed {
			writes = append(writes, target.Save)
		}
	}
	if len(report.Actions) > 0 {
		refusals = append(refusals, soft...)
	}
	if len(refusals) > 0 {
		return nil, &LegacyRenameRefusedError{refusals}
	}
	if dry {
		return report, nil
	}
	for _, write := range writes {
		if err := write(); err != nil {
			return nil, err
		}
	}
	if err := RestampBundleHash(root); err != nil && len(report.Actions) > 0 {
		return nil, err
	}
	return report, nil
}
