package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// This file ports preset_application's id resolution (_resolve_preset_ids,
// _org_roots_or_refuse, _check_anti_patterns, _check_mission_types) and
// _org_required: an id that resolves nowhere, or a layer that cannot be
// read to check it, refuses the preset with PRESET_ID_UNRESOLVED.

const orgPacksKey = "charter_packs.org.packs"

// PresetUnexpectedError wraps an exception _resolve_preset_ids does not
// catch (an org-merge refusal while loading the DRG for the anti-pattern
// check): the CLI crashes with a traceback.
type PresetUnexpectedError struct{ Err error }

func (e *PresetUnexpectedError) Error() string { return e.Err.Error() }

// RequireDeclaredOrgRoots is resolve_pack_chain(repo_root, strict=True)
// (require_declared_org_roots): every declared org pack root, failing
// closed on an unreadable registry or an unfetched pack.
func RequireDeclaredOrgRoots(repoRoot string) ([]string, error) {
	if cfg, err := dct.LoadConfig(repoRoot); err == nil {
		if cp, ok := cfg.Get("charter_packs"); ok && cp != nil {
			cpo, isObj := cp.(*jsonx.Object)
			if !isObj {
				return nil, fmt.Errorf("the org pack registry cannot be read: Invalid org-pack config in .kittify/config.yaml: `charter_packs` must be a mapping, got %s", rtTypeName(cp))
			}
			if org, ok := cpo.Get("org"); ok && org != nil {
				if _, isObj := org.(*jsonx.Object); !isObj {
					return nil, fmt.Errorf("the org pack registry cannot be read: Invalid org-pack config in .kittify/config.yaml: `charter_packs.org` must be a mapping, got %s", rtTypeName(org))
				}
			}
		}
	}
	reg := LoadPackRegistry(repoRoot)
	if w := reg.Warning; w != "" {
		const readPrefix = "Failed to read .kittify/config.yaml; org doctrine disabled: "
		const invalidPrefix = "Invalid org-pack config; ignoring org layer: "
		switch {
		case strings.HasPrefix(w, readPrefix):
			return nil, fmt.Errorf("the org pack registry cannot be read: Failed to read .kittify/config.yaml: %s", strings.TrimPrefix(w, readPrefix))
		case strings.HasPrefix(w, invalidPrefix):
			return nil, fmt.Errorf("the org pack registry cannot be read: Invalid org-pack config in .kittify/config.yaml: %s", strings.TrimPrefix(w, invalidPrefix))
		}
	}
	type named struct{ name, root string }
	var roots []named
	for _, p := range reg.Packs {
		r, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return nil, fmt.Errorf("the org pack registry cannot be read: %s", err.Error())
		}
		roots = append(roots, named{p.Name, r})
	}
	var out []string
	for _, n := range roots {
		if st, err := os.Stat(n.root); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("declared org pack %s is not fetched: its root %s is not a directory; run `spec-kitty charter fetch` to fetch it", dct.PyRepr(n.name), n.root)
		}
		out = append(out, n.root)
	}
	return out, nil
}

func rtTypeName(v any) string {
	switch v.(type) {
	case string:
		return "str"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case []any:
		return "CommentedSeq"
	case *jsonx.Object:
		return "CommentedMap"
	case nil:
		return "NoneType"
	}
	return fmt.Sprintf("%T", v)
}

// listsIDs is _lists_ids.
func (p *Preset) listsIDs() bool { return len(p.Activations) > 0 || p.ActivatedKinds != nil }

// presetUnresolved is PresetIdUnresolvedError(preset_file, unresolved, reasons).
func presetUnresolved(p *Preset, order []string, unresolved map[string][]string, reasonOrder []string, reasons map[string]string) error {
	var named []string
	var lines []string
	un := &jsonx.Object{}
	for _, k := range order {
		if len(unresolved[k]) == 0 {
			continue
		}
		named = append(named, k+": "+strings.Join(unresolved[k], ", "))
		for _, id := range unresolved[k] {
			lines = append(lines, "  "+k+": "+id)
		}
		un.Set(k, strsAny(unresolved[k]))
	}
	rs := &jsonx.Object{}
	for _, k := range reasonOrder {
		named = append(named, k+": "+reasons[k])
		lines = append(lines, "  "+k+": "+reasons[k])
		rs.Set(k, reasons[k])
	}
	sort.Strings(named)
	headline := "lists ids that resolve nowhere in the offering"
	if un.Len() == 0 {
		headline = "lists ids that cannot be checked against the offering"
	}
	return &CodedError{Code: "PRESET_ID_UNRESOLVED", Msg: fmt.Sprintf("%s %s (%s)", p.Source, headline, strings.Join(named, "; ")),
		Details: lines, Payload: jsonx.NewObject("preset_file", p.Source, "unresolved", un, "reasons", rs)}
}

// isDRGLoadFailure reports whether err is one of _DRG_LOAD_ERRORS
// (DRGLoadError, DRGValidationError, DRGGraphSchemaError) rather than an
// org-merge refusal.
func isDRGLoadFailure(err error) bool {
	switch err.(type) {
	case *OrgConflictError, *OrgPackSchemaError, *OrgPackParseError, *UnknownRelationError, *DuplicateURNError, *OrgPackMissingError:
		return false
	}
	return true
}

// resolveIDs is _resolve_preset_ids.
func resolveIDs(root string, p *Preset) error {
	var chain []string
	if p.listsIDs() {
		c, err := RequireDeclaredOrgRoots(root)
		if err != nil {
			return presetUnresolved(p, nil, nil, []string{orgPacksKey}, map[string]string{orgPacksKey: err.Error()})
		}
		chain = c
	} else {
		chain = dct.OrgRoots(root)
	}
	lr := dct.LayerRoots(root)
	unresolved := map[string][]string{}
	reasons := map[string]string{}
	var order, reasonOrder []string
	setReason := func(k, why string) {
		if _, ok := reasons[k]; !ok {
			reasonOrder = append(reasonOrder, k)
		}
		reasons[k] = why
	}
	for _, k := range governedKinds {
		key := "activated_" + k.Plural()
		ids, ok := p.Activations[key]
		if !ok || len(ids) == 0 {
			continue
		}
		var missing []string
		if k == dct.AntiPattern {
			g, err := LoadOrgValidatedGraph(root, GraphOptions{})
			if err != nil {
				if !isDRGLoadFailure(err) {
					return &PresetUnexpectedError{err}
				}
				setReason(key, "the DRG cannot be loaded to check anti-pattern ids: "+err.Error())
				continue
			}
			known := map[string]bool{}
			for _, n := range g.Nodes {
				if n.Kind == "anti_pattern" {
					known[n.ID()] = true
				}
			}
			for _, id := range ids {
				if !known[id] {
					missing = append(missing, id)
				}
			}
		} else {
			stems := dct.StemsByDeclaredID(k, lr, chain)
			byStem := map[string]bool{}
			for _, s := range stems {
				byStem[s] = true
			}
			for _, id := range ids {
				_, isID := stems[id]
				if !isID && !byStem[id] && !(k == dct.Directive && stems[dct.NormalizeDirectiveID(id)] != "") {
					missing = append(missing, id)
				}
			}
		}
		order = append(order, key)
		unresolved[key] = missing
	}
	if len(p.MissionTypes) > 0 {
		const key = "mission_type_activations"
		available, err := availableMissionTypes(root, chain)
		if err != nil {
			setReason(key, "the mission-type roster cannot be read: "+err.Error())
		} else {
			var missing []string
			for _, mt := range p.MissionTypes {
				if !available[mt] {
					missing = append(missing, mt)
				}
			}
			order = append(order, key)
			unresolved[key] = missing
			var refusals []string
			var roster map[string]*dct.MissionType
			var rerr error
			loaded := false
			for _, mt := range p.MissionTypes {
				if !available[mt] {
					continue
				}
				if !loaded {
					roster, rerr = dct.LayeredMissionTypes(root)
					loaded = true
				}
				if rerr != nil {
					refusals = append(refusals, mt+": "+rerr.Error())
					continue
				}
				if t := roster[mt]; t != nil && len(dct.ExtendsFallbackSequence(t, roster)) == 0 {
					refusals = append(refusals, mt+": "+dct.EmptyActionSequenceMessage(mt, dct.MissionTypeSourceLayer(root, mt)))
				}
			}
			if len(refusals) > 0 {
				setReason(key, strings.Join(refusals, "; "))
			}
		}
	}
	any := len(reasons) > 0
	for _, k := range order {
		if len(unresolved[k]) > 0 {
			any = true
		}
	}
	if !any {
		return nil
	}
	return presetUnresolved(p, order, unresolved, reasonOrder, reasons)
}

// availableMissionTypes is _available_mission_types: the mission types the
// built-in, project and every org layer offer (each layer loud-fails on a
// malformed mission-type file).
func availableMissionTypes(root string, orgRoots []string) (map[string]bool, error) {
	out := map[string]bool{}
	ids, err := dct.ScanMissionTypeIDs(dct.BuiltInMissionTypesDir, root)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	if lr := dct.LayerRoots(root); lr[dct.LayerProject] != "" {
		ids, err := dct.ScanMissionTypeIDs(filepath.Join(root, filepath.FromSlash(dct.ProjectMissionTypesDir)), root)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	for _, r := range orgRoots {
		ids, err := dct.ScanMissionTypeIDs(filepath.Join(r, "mission_types"), root)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out, nil
}

// orgRequired is _org_required: the org charter's required_<plural> ids
// (non-empty lists only); none when the preset lists no ids.
func orgRequired(root string, p *Preset) (map[string][]string, error) {
	if !p.listsIDs() {
		return nil, nil
	}
	pol, err := LegacyOrgPolicy(root)
	if err != nil {
		return nil, presetUnresolved(p, nil, nil, []string{orgPacksKey}, map[string]string{orgPacksKey: "the org charter cannot be read: " + err.Error()})
	}
	out := map[string][]string{}
	for _, k := range OrgRequirableKinds {
		if ids := pol.Required[k]; len(ids) > 0 {
			out[k] = ids
		}
	}
	return out, nil
}
