package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// This file ports charter.offering.drg.override_policy: the
// replaceable-builtins allowlist (consumer + per-pack sanctions) and the
// adjudication of org overrides of built-in DRG nodes.

const (
	// PackPolicyFilename is the pack-root sanction file name.
	PackPolicyFilename = "replaceable-builtins.yaml"
	// LegacyTemplateRelpath is the advisory pre-contract location.
	LegacyTemplateRelpath = "templates/setup/replaceable-builtins.yaml"
)

// PolicyRelpath is the consumer allowlist path relative to the repo root.
var PolicyRelpath = dct.ProjectPackRoot + "/" + PackPolicyFilename

// ReplaceableBuiltin is one allowlist entry.
type ReplaceableBuiltin struct{ URN, Reason string }

// ReplaceablePolicy is ReplaceableBuiltinsPolicy.
type ReplaceablePolicy struct {
	Entries      []ReplaceableBuiltin
	RevokedURNs  map[string]bool
	RevokedPacks map[string]bool
}

func (p *ReplaceablePolicy) isAllowed(urn string) bool {
	if p == nil {
		return false
	}
	for _, e := range p.Entries {
		if e.URN == urn {
			return true
		}
	}
	return false
}

func (p *ReplaceablePolicy) reasonFor(urn string) (string, bool) {
	for _, e := range p.Entries {
		if e.URN == urn {
			return e.Reason, true
		}
	}
	return "", false
}

type overridePolicyError struct{ msg string }

func (e *overridePolicyError) Error() string { return e.msg }

func safeTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case string:
		return "str"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case *jsonx.Object:
		return "dict"
	}
	return fmt.Sprintf("%T", v)
}

func parsePolicyDocument(data any, label string, allowRevocations bool) (*ReplaceablePolicy, error) {
	pol := &ReplaceablePolicy{RevokedURNs: map[string]bool{}, RevokedPacks: map[string]bool{}}
	if data == nil {
		return pol, nil
	}
	o, ok := data.(*jsonx.Object)
	if !ok {
		return nil, &overridePolicyError{label + ": top-level document must be a mapping with a 'replaceable_builtins' list"}
	}
	raw, _ := o.Get("replaceable_builtins")
	if raw == nil {
		raw = []any{}
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, &overridePolicyError{label + ": 'replaceable_builtins' must be a list"}
	}
	for i, item := range list {
		io, ok := item.(*jsonx.Object)
		if !ok {
			return nil, &overridePolicyError{fmt.Sprintf("%s: entry #%d must be a mapping with a 'urn' key, got %s", label, i, safeTypeName(item))}
		}
		uv, _ := io.Get("urn")
		urn, ok := uv.(string)
		if !ok || urn == "" {
			return nil, &overridePolicyError{fmt.Sprintf("%s: entry #%d is missing a non-empty 'urn'", label, i)}
		}
		rv, has := io.Get("reason")
		reason := ""
		if has && rv != nil {
			s, ok := rv.(string)
			if !ok {
				return nil, &overridePolicyError{fmt.Sprintf("%s: entry #%d ('%s') has a non-string 'reason'", label, i, urn)}
			}
			reason = s
		}
		pol.Entries = append(pol.Entries, ReplaceableBuiltin{urn, reason})
	}
	rev, hasRev := o.Get("revoked_pack_sanctions")
	if hasRev && !allowRevocations {
		return nil, &overridePolicyError{label + ": 'revoked_pack_sanctions' is consumer-only and is not allowed in a pack sanction file"}
	}
	if rev == nil {
		return pol, nil
	}
	rl, ok := rev.([]any)
	if !ok {
		return nil, &overridePolicyError{label + ": 'revoked_pack_sanctions' must be a list"}
	}
	for i, item := range rl {
		where := fmt.Sprintf("%s: revoked_pack_sanctions entry #%d", label, i)
		io, ok := item.(*jsonx.Object)
		if !ok {
			return nil, &overridePolicyError{where + " must be a mapping with exactly one of 'urn' or 'pack'"}
		}
		var keys, unknown, selectors []string
		for _, kv := range io.Pairs() {
			keys = append(keys, kv.Key)
			if kv.Key != "urn" && kv.Key != "pack" && kv.Key != "reason" {
				unknown = append(unknown, kv.Key)
			}
		}
		for _, k := range []string{"urn", "pack"} {
			if _, ok := io.Get(k); ok {
				selectors = append(selectors, k)
			}
		}
		if len(unknown) > 0 || len(selectors) != 1 {
			sort.Strings(keys)
			q := make([]string, len(keys))
			for j, k := range keys {
				q[j] = dct.PyRepr(k)
			}
			return nil, &overridePolicyError{fmt.Sprintf("%s must have exactly one of 'urn' or 'pack' (and an optional 'reason'), got keys [%s]", where, strings.Join(q, ", "))}
		}
		vv, _ := io.Get(selectors[0])
		val, ok := vv.(string)
		if !ok || val == "" {
			return nil, &overridePolicyError{fmt.Sprintf("%s needs a non-empty string '%s'", where, selectors[0])}
		}
		if r, ok := io.Get("reason"); ok && r != nil {
			if _, isStr := r.(string); !isStr {
				return nil, &overridePolicyError{where + " has a non-string 'reason'"}
			}
		}
		if selectors[0] == "urn" {
			pol.RevokedURNs[val] = true
		} else {
			pol.RevokedPacks[val] = true
		}
	}
	return pol, nil
}

func readPolicyFile(p, label string, allowRevocations bool) (*ReplaceablePolicy, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, &overridePolicyError{fmt.Sprintf("%s: cannot read file: %v", label, err)}
	}
	v, err := pyyaml.SafeLoad(string(b))
	if err != nil {
		return nil, &overridePolicyError{fmt.Sprintf("%s: YAML parse error: %v", label, err)}
	}
	return parsePolicyDocument(v, label, allowRevocations)
}

func consumerPolicyPath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(dct.ProjectPackRoot), PackPolicyFilename)
}

func loadConsumerPolicy(repoRoot string) (*ReplaceablePolicy, error) {
	p := consumerPolicyPath(repoRoot)
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return &ReplaceablePolicy{RevokedURNs: map[string]bool{}, RevokedPacks: map[string]bool{}}, nil
	}
	return readPolicyFile(p, PolicyRelpath, true)
}

func packSanctionPresent(packRoot string) bool {
	_, err := os.Lstat(filepath.Join(packRoot, PackPolicyFilename))
	return err == nil
}

func loadPackSanction(name, packRoot string) (*ReplaceablePolicy, error) {
	empty := &ReplaceablePolicy{RevokedURNs: map[string]bool{}, RevokedPacks: map[string]bool{}}
	if !packSanctionPresent(packRoot) {
		return empty, nil
	}
	cand := filepath.Join(packRoot, PackPolicyFilename)
	label := fmt.Sprintf("pack '%s' %s", name, cand)
	p, err := ResolveWithinRoot(packRoot, PackPolicyFilename)
	if err != nil {
		return nil, &overridePolicyError{label + ": " + err.Error()}
	}
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return nil, &overridePolicyError{label + ": not a regular file"}
	}
	return readPolicyFile(p, label, false)
}

// LegacyTemplateEntries is legacy_template_entries.
func LegacyTemplateEntries(packRoot string, urns []string) map[string]string {
	wanted := map[string]bool{}
	for _, u := range urns {
		wanted[u] = true
	}
	p, err := ResolveWithinRoot(packRoot, LegacyTemplateRelpath)
	if err != nil {
		return nil
	}
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return nil
	}
	pol, err := readPolicyFile(p, "legacy template", false)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range pol.Entries {
		if wanted[e.URN] {
			out[e.URN] = e.Reason
		}
	}
	return out
}

// EffectiveOverridePolicy is the consumer allowlist plus pack sanctions.
type EffectiveOverridePolicy struct {
	Consumer         *ReplaceablePolicy
	Packs            map[string]*ReplaceablePolicy
	PackErrors       []string
	PackErrorNames   map[string]bool
	ConsumerError    string
	RevocationErrors []string
}

// PackRootsFromFragments is pack_roots_from_fragments (name -> root),
// keeping fragment order in names.
func PackRootsFromFragments(frags []*OrgFragment, repoRoot string) (map[string]string, []string) {
	out := map[string]string{}
	var names []string
	for _, f := range frags {
		r := f.SourceRef
		if !filepath.IsAbs(r) {
			r = filepath.Join(repoRoot, r)
		}
		if _, ok := out[f.PackName]; !ok {
			names = append(names, f.PackName)
		}
		out[f.PackName] = r
	}
	return out, names
}

func sameFile(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil {
		ra = pyResolve(a)
	}
	if err2 != nil {
		rb = pyResolve(b)
	}
	return ra == rb
}

// LoadEffectiveOverridePolicy is load_effective_override_policy.
func LoadEffectiveOverridePolicy(repoRoot string, roots map[string]string, order []string, configured []string) *EffectiveOverridePolicy {
	eff := &EffectiveOverridePolicy{Packs: map[string]*ReplaceablePolicy{}, PackErrorNames: map[string]bool{}}
	c, err := loadConsumerPolicy(repoRoot)
	if err != nil {
		c = &ReplaceablePolicy{RevokedURNs: map[string]bool{}, RevokedPacks: map[string]bool{}}
		eff.ConsumerError = err.Error()
	}
	eff.Consumer = c
	cp := consumerPolicyPath(repoRoot)
	for _, name := range order {
		root := roots[name]
		if sameFile(filepath.Join(root, PackPolicyFilename), cp) {
			eff.Packs[name] = &ReplaceablePolicy{RevokedURNs: map[string]bool{}, RevokedPacks: map[string]bool{}}
			continue
		}
		p, err := loadPackSanction(name, root)
		if err != nil {
			eff.PackErrors = append(eff.PackErrors, err.Error())
			eff.PackErrorNames[name] = true
			continue
		}
		eff.Packs[name] = p
	}
	conf := map[string]bool{}
	for _, n := range configured {
		conf[n] = true
	}
	var revoked []string
	for p := range c.RevokedPacks {
		if !conf[p] {
			revoked = append(revoked, p)
		}
	}
	sort.Strings(revoked)
	for _, p := range revoked {
		eff.RevocationErrors = append(eff.RevocationErrors, fmt.Sprintf("%s: revoked_pack_sanctions names pack '%s', which is not a configured org pack", PolicyRelpath, p))
	}
	return eff
}

// OverriddenBuiltin is a built-in URN now carrying org provenance.
type OverriddenBuiltin struct{ URN, Kind, Pack string }

// SanctionedOverride is a sanctioned override.
type SanctionedOverride struct{ URN, Kind, Pack, Source, Reason string }

// UnsanctionedOverride is an unsanctioned override.
type UnsanctionedOverride struct{ URN, Kind, Why string }

// FindOverriddenBuiltins is find_overridden_builtins.
func FindOverriddenBuiltins(merged *MergedGraph, builtIn map[string]bool) []OverriddenBuiltin {
	var out []OverriddenBuiltin
	for _, n := range merged.Nodes {
		if strings.HasPrefix(n.Provenance, "org:") && builtIn[n.URN] {
			out = append(out, OverriddenBuiltin{n.URN, n.Kind, strings.TrimPrefix(n.Provenance, "org:")})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].URN < out[j].URN })
	return out
}

// SanctionReasonMissing is sanction_reason_missing.
func SanctionReasonMissing(urn, reason string) bool {
	k, _, _ := strings.Cut(urn, ":")
	return k == "directive" && strings.TrimSpace(reason) == ""
}

func validEntry(p *ReplaceablePolicy, urn string) (string, bool) {
	if p == nil || !p.isAllowed(urn) {
		return "", false
	}
	r, _ := p.reasonFor(urn)
	if SanctionReasonMissing(urn, r) {
		return "", false
	}
	return r, true
}

// AdjudicateOverrides is adjudicate_overrides.
func AdjudicateOverrides(overrides []OverriddenBuiltin, eff *EffectiveOverridePolicy) ([]SanctionedOverride, []UnsanctionedOverride) {
	var sanc []SanctionedOverride
	var unsanc []UnsanctionedOverride
	for _, o := range overrides {
		if r, ok := validEntry(eff.Consumer, o.URN); ok {
			sanc = append(sanc, SanctionedOverride{o.URN, o.Kind, o.Pack, "consumer", r})
			continue
		}
		pp := eff.Packs[o.Pack]
		pr, pok := validEntry(pp, o.URN)
		revoked := eff.Consumer.RevokedURNs[o.URN] || eff.Consumer.RevokedPacks[o.Pack]
		if pok && !revoked {
			sanc = append(sanc, SanctionedOverride{o.URN, o.Kind, o.Pack, "pack", pr})
			continue
		}
		var why string
		switch {
		case pok && revoked:
			why = fmt.Sprintf("pack '%s' sanction revoked by %s (revoked_pack_sanctions)", o.Pack, PolicyRelpath)
		case eff.Consumer.isAllowed(o.URN):
			why = "directive override requires a non-empty reason"
		case pp != nil && pp.isAllowed(o.URN):
			why = fmt.Sprintf("directive override requires a non-empty reason (pack '%s' %s)", o.Pack, PackPolicyFilename)
		case eff.PackErrorNames[o.Pack]:
			why = fmt.Sprintf("pack '%s' %s could not be read (see the sanction file errors)", o.Pack, PackPolicyFilename)
		default:
			why = fmt.Sprintf("not on %s or pack '%s' %s", PolicyRelpath, o.Pack, PackPolicyFilename)
		}
		unsanc = append(unsanc, UnsanctionedOverride{o.URN, o.Kind, why})
	}
	return sanc, unsanc
}

// RenderSanctionEntries is render_sanction_entries (yaml.safe_dump,
// sort_keys=False, block style).
func RenderSanctionEntries(entries []ReplaceableBuiltin) string {
	var items []any
	for _, e := range entries {
		o := jsonx.NewObject("urn", e.URN)
		if e.Reason != "" {
			o.Set("reason", e.Reason)
		}
		items = append(items, o)
	}
	if items == nil {
		items = []any{}
	}
	return pyyaml.Dump(jsonx.NewObject("replaceable_builtins", items), pyyaml.Options{SortKeys: false, AllowUnicode: true})
}
