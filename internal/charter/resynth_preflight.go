package charter

import (
	"fmt"
	"sort"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// This file ports the read-only parts of
// specify_cli.cli.commands.charter._resynthesis_preflight and the
// activate-time tension scan (charter.activation.consistency_check
// .scan_unreconciled_tensions).

// ScanUnreconciledTensions is scan_unreconciled_tensions; ok=false when the
// DRG cannot be loaded (callers treat it as "no warning", best-effort).
func ScanUnreconciledTensions(root string) ([]TensionFinding, bool) {
	rep := &ConsistencyReport{}
	g := newGateRes(root)
	g.tensions(rep)
	if len(rep.VerificationErrors) > 0 {
		return nil, false
	}
	return rep.UnreconciledTensions, true
}

// FutureSelections is _future_selections: for each requested activation key
// the current list (or, when the key is absent and the kind is seeded from
// the effective set, that set) plus the requested ids.
func FutureSelections(root string, requested map[string][]string) (map[string][]string, error) {
	t, err := LoadWriteTarget(root)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	keys := make([]string, 0, len(requested))
	for k := range requested {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		token := tokenForKey(key)
		set := map[string]bool{}
		cur, present, err := currentList(t.Data, key)
		if err != nil {
			return nil, err
		}
		switch {
		case present:
			for _, id := range cur {
				set[id] = true
			}
		case token != "skill" && token != dct.MissionTypeToken:
			ids, reason := EffectiveSet(root, token)
			if reason != "" {
				return nil, fmt.Errorf("Cannot resolve the effective %s set for resynthesis: %s. The activation was not written.", token, reason)
			}
			for _, id := range ids {
				set[id] = true
			}
		}
		for _, id := range requested[key] {
			set[id] = true
		}
		var ids []string
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		out[key] = ids
	}
	return out, nil
}

func tokenForKey(key string) string {
	for _, tok := range dct.CharterKindTokens {
		if dct.YAMLKey(tok) == key {
			return tok
		}
	}
	return ""
}

// PreflightActivationRoots is resolve_config_activated_roots over the
// activation state with selections applied (nothing is written).
func PreflightActivationRoots(root string, selections map[string][]string) error {
	t, err := LoadWriteTarget(root)
	if err != nil {
		return err
	}
	data := t.Data.Clone()
	for k, ids := range selections {
		l := make([]any, len(ids))
		for i, id := range ids {
			l[i] = id
		}
		data.Set(k, l)
	}
	_, err = resolveConfigRootsFrom(root, &WriteTarget{Path: t.Path, Data: data, IsCharter: t.IsCharter}, nil)
	return err
}

// PreflightProjectLayer is emit_project_layer([], version,
// load_built_in_graph(), project_root=root): the project-profile support
// rules, run before any activation write.
func PreflightProjectLayer(root, version string) error {
	base, err := dct.BuiltInGraph()
	if err != nil {
		return err
	}
	_, _, err = emitProjectLayer(nil, version, base, nil, root)
	return err
}
