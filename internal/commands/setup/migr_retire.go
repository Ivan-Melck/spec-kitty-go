package setup

// Retired-doctrine-id migrations (upgrade/migrations/_retired_activation.py):
// 3.2.6_retire_rtk_search_tooling and 4.0.0rc5_retire_single_owner_doctrine_ids.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

type retirement struct {
	kindKey, stem, prefix string
	successors            [][2]string
}

func (r retirement) referenceID() string { return r.prefix + ":" + r.stem }

type retireSurface struct {
	rel                 string
	memberKey, blockKey string
	successorEligible   bool
	successorKeyPrefix  string
}

func selectedKey(kindKey string) string { return "selected_" + strings.TrimPrefix(kindKey, "activated_") }

func retireSurfaces(r retirement, answers bool) []retireSurface {
	s := []retireSurface{
		{rel: ".kittify/config.yaml", memberKey: r.kindKey, successorEligible: true},
		{rel: ".kittify/charter/charter.yaml", memberKey: r.kindKey, blockKey: "catalog", successorEligible: true},
		{rel: ".kittify/charter/charter.yaml", memberKey: "governance.charter." + selectedKey(r.kindKey), successorEligible: true, successorKeyPrefix: "governance.charter."},
		{rel: ".kittify/charter/references.yaml", blockKey: "references"},
	}
	if answers {
		s = append(s, retireSurface{rel: ".kittify/charter/interview/answers.yaml", memberKey: selectedKey(r.kindKey)})
	}
	return s
}

func loadRetireMapping(p string, warnings *[]string) *jsonx.Object {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	v, err := yamlx.Parse(b)
	warnOnce := func(m string) {
		if warnings == nil {
			return
		}
		for _, w := range *warnings {
			if w == m {
				return
			}
		}
		*warnings = append(*warnings, m)
	}
	if err != nil {
		warnOnce(filepath.ToSlash(p) + " could not be parsed as YAML and was skipped; retired ids in it were not migrated")
		return nil
	}
	if v == nil {
		return &jsonx.Object{}
	}
	o, isObj := v.(*jsonx.Object)
	if !isObj {
		warnOnce(filepath.ToSlash(p) + " is not a mapping and was skipped; retired ids in it were not migrated")
		return nil
	}
	return o
}

func resolveMemberList(data *jsonx.Object, key string) ([]any, func([]any)) {
	parts := strings.Split(key, ".")
	node := data
	for _, p := range parts[:len(parts)-1] {
		next := node.GetObject(p)
		if next == nil {
			return nil, nil
		}
		node = next
	}
	last := parts[len(parts)-1]
	v, _ := node.Get(last)
	l, isL := v.([]any)
	if !isL {
		return nil, nil
	}
	return l, func(nl []any) { node.Set(last, nl) }
}

func memberIndex(l []any, stem string) int {
	for i, x := range l {
		if s, ok := x.(string); ok && s == stem {
			return i
		}
	}
	return -1
}

func blockList(data *jsonx.Object, key string) ([]any, func([]any)) {
	v, _ := data.Get(key)
	if l, isL := v.([]any); isL {
		return l, func(nl []any) { data.Set(key, nl) }
	}
	if o, isObj := v.(*jsonx.Object); isObj {
		if n, ok := o.Get("references"); ok {
			if l, isL := n.([]any); isL {
				return l, func(nl []any) { o.Set("references", nl) }
			}
		}
	}
	return nil, nil
}

func blockPresent(data *jsonx.Object, key, id string) bool {
	l, _ := blockList(data, key)
	for _, b := range l {
		if o, isObj := b.(*jsonx.Object); isObj && o.GetString("id") == id {
			return true
		}
	}
	return false
}

func stillResolvesViaOrgPack(root string, r retirement) bool {
	kindDir := strings.ToLower(r.prefix) + "s"
	file := r.stem + "." + strings.ToLower(r.prefix) + ".yaml"
	roots := []string{}
	if pr := filepath.Join(root, ".kittify", "charter-packs"); isDir(pr) {
		roots = append(roots, pr)
	}
	roots = append(roots, dct.OrgRoots(root)...)
	for _, rt := range roots {
		if isFile(filepath.Join(rt, kindDir, file)) {
			return true
		}
	}
	return false
}

func surfaceNeedsChange(data *jsonx.Object, r retirement, s retireSurface) bool {
	if s.memberKey != "" {
		if l, _ := resolveMemberList(data, s.memberKey); l != nil && memberIndex(l, r.stem) >= 0 {
			return true
		}
	}
	return s.blockKey != "" && blockPresent(data, s.blockKey, r.referenceID())
}

func detectRetirements(root string, rs []retirement, answers bool) bool {
	for _, r := range rs {
		if stillResolvesViaOrgPack(root, r) {
			continue
		}
		for _, s := range retireSurfaces(r, answers) {
			if d := loadRetireMapping(filepath.Join(root, filepath.FromSlash(s.rel)), nil); d != nil && surfaceNeedsChange(d, r, s) {
				return true
			}
		}
	}
	return false
}

func applyRetirements(root string, rs []retirement, answers, dryRun bool) migrationResult {
	var ch, errs, warn []string
	for _, r := range rs {
		if stillResolvesViaOrgPack(root, r) {
			continue
		}
		for _, s := range retireSurfaces(r, answers) {
			p := filepath.Join(root, filepath.FromSlash(s.rel))
			data := loadRetireMapping(p, &warn)
			if data == nil {
				continue
			}
			var touched []string
			memberRemoved := false
			if s.memberKey != "" {
				if l, set := resolveMemberList(data, s.memberKey); l != nil && memberIndex(l, r.stem) >= 0 {
					var keep []any
					for _, x := range l {
						if sv, ok := x.(string); ok && sv == r.stem {
							continue
						}
						keep = append(keep, x)
					}
					if keep == nil {
						keep = []any{}
					}
					set(keep)
					touched = append(touched, s.memberKey)
					memberRemoved = true
				}
			}
			if s.blockKey != "" {
				if l, set := blockList(data, s.blockKey); l != nil && blockPresent(data, s.blockKey, r.referenceID()) {
					var keep []any
					for _, b := range l {
						if o, isObj := b.(*jsonx.Object); isObj && o.GetString("id") == r.referenceID() {
							continue
						}
						keep = append(keep, b)
					}
					if keep == nil {
						keep = []any{}
					}
					set(keep)
					touched = append(touched, s.blockKey)
				}
			}
			var added [][2]string
			if memberRemoved && s.successorEligible {
				for _, succ := range r.successors {
					key := succ[0]
					if s.successorKeyPrefix != "" {
						key = s.successorKeyPrefix + selectedKey(succ[0])
					}
					if l, set := resolveMemberList(data, key); l != nil && memberIndex(l, succ[1]) < 0 {
						set(append(l, succ[1]))
						added = append(added, [2]string{key, succ[1]})
					}
				}
			}
			if len(touched) == 0 && len(added) == 0 {
				continue
			}
			rv, sv := "Removed", "Activated"
			if dryRun {
				rv, sv = "Would remove", "Would activate"
			} else if err := os.WriteFile(p, []byte(yamlx.Emit(data)), filePermOr(p, 0o644)); err != nil {
				errs = append(errs, fmt.Sprintf("Failed writing %s: %s", s.rel, pyOSError(err)))
				continue
			}
			for _, k := range touched {
				ch = append(ch, fmt.Sprintf("%s %s from %s (%s)", rv, r.stem, s.rel, k))
			}
			for _, a := range added {
				ch = append(ch, fmt.Sprintf("%s successor %s in %s (%s)", sv, a[1], s.rel, a[0]))
			}
		}
	}
	return migrationResult{Success: len(errs) == 0, Changes: ch, Errors: errs, Warnings: warn}
}

func filePermOr(p string, def os.FileMode) os.FileMode {
	if st, err := os.Stat(p); err == nil {
		return st.Mode().Perm()
	}
	return def
}

var rtkRetirement = []retirement{{kindKey: "activated_toolguides", stem: "rtk-search-tooling", prefix: "TOOLGUIDE"}}

var singleOwnerRetirements = []retirement{
	{kindKey: "activated_styleguides", stem: "adversarial-squad-cadence", prefix: "STYLEGUIDE"},
	{kindKey: "activated_tactics", stem: "bug-fixing-checklist", prefix: "TACTIC", successors: [][2]string{{"activated_procedures", "test-first-bug-fixing"}}},
	{kindKey: "activated_tactics", stem: "locality-of-change", prefix: "TACTIC", successors: [][2]string{{"activated_tactics", "avoid-gold-plating"}}},
	{kindKey: "activated_tactics", stem: "common-docs-curation", prefix: "TACTIC", successors: [][2]string{{"activated_tactics", "common-docs-scaffold"},
		{"activated_tactics", "common-docs-write"}, {"activated_tactics", "common-docs-find"}}},
	{kindKey: "activated_tactics", stem: "boring-code-review", prefix: "TACTIC", successors: [][2]string{{"activated_styleguides", "boring-code-review"}}},
	{kindKey: "activated_tactics", stem: "behavior-driven-development", prefix: "TACTIC", successors: [][2]string{{"activated_tactics", "bdd-scenario-formulation"}}},
	{kindKey: "activated_tactics", stem: "iterative-deepening-review", prefix: "TACTIC"},
	{kindKey: "activated_procedures", stem: "tracker-organisation-workflow", prefix: "PROCEDURE"},
}

func init() {
	migrationImpls["3.2.6_retire_rtk_search_tooling"] = migrationImpl{
		detect: func(root string) bool { return detectRetirements(root, rtkRetirement, false) },
		canApply: func(root string) (bool, string) {
			if detectRetirements(root, rtkRetirement, false) {
				return ok()
			}
			return false, "rtk-search-tooling is not activated in this project"
		},
		apply: func(root string, dryRun bool) migrationResult {
			r := applyRetirements(root, rtkRetirement, false, dryRun)
			if len(r.Changes) == 0 && len(r.Errors) == 0 {
				return migrationResult{Success: true, Changes: []string{"rtk-search-tooling already absent; nothing to remove"}, Warnings: r.Warnings}
			}
			return r
		},
	}
	migrationImpls["4.0.0rc5_retire_single_owner_doctrine_ids"] = migrationImpl{
		detect: func(root string) bool { return detectRetirements(root, singleOwnerRetirements, true) },
		canApply: func(root string) (bool, string) {
			if detectRetirements(root, singleOwnerRetirements, true) {
				return ok()
			}
			return false, "no retired single-owner doctrine id is activated in this project"
		},
		apply: func(root string, dryRun bool) migrationResult {
			r := applyRetirements(root, singleOwnerRetirements, true, dryRun)
			if len(r.Changes) == 0 && len(r.Errors) == 0 {
				return migrationResult{Success: true, Changes: []string{"no retired single-owner doctrine id is activated in this project; nothing to remove"}, Warnings: r.Warnings}
			}
			return r
		},
	}
}
