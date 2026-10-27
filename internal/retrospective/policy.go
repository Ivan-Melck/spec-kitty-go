package retrospective

// Retrospective policy resolution (retrospective.policy.resolve_policy):
// charter.yaml governance.retrospective (authority), charter.md frontmatter
// retrospective: (secondary), .kittify/config.yaml retrospective: (fills
// what no charter layer claims, or wins under precedence: config), then
// built-in defaults, with a flat per-leaf source map.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const (
	charterMDRel   = ".kittify/charter/charter.md"
	charterYAMLRel = ".kittify/charter/charter.yaml"
	configRel      = ".kittify/config.yaml"
	yamlBlockPath  = "governance.retrospective"
	configPrefix   = ".kittify/config.yaml#retrospective"
	defaultSource  = "<default>"
	resolutionErr  = "<resolution_error>"
)

var (
	knownKeys = []string{"enabled", "timing", "failure_policy", "write_record", "generate_proposals", "apply_proposals",
		"permissions", "precedence", "generator", "strict_keys"}
	knownPermissionKeys = []string{"write_record", "inspect_mission_artifacts", "propose_glossary_changes",
		"propose_drg_changes", "propose_doctrine_changes", "apply_low_risk_changes", "apply_structural_changes"}
	enumFields = map[string][]string{
		"timing":          {"post_completion", "before_completion"},
		"failure_policy":  {"warn", "block"},
		"apply_proposals": {"require_human", "low_risk_auto"},
		"precedence":      {"charter", "config"},
		"generator":       {"python"},
	}
)

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// PolicyError is PolicyResolutionError: str() is "[reason] in source: detail".
type PolicyError struct{ Source, Reason, Detail string }

func (e *PolicyError) Error() string {
	return fmt.Sprintf("[%s] in %s: %s", e.Reason, e.Source, e.Detail)
}

// Policy is the resolved RetrospectivePolicy. Values keep the type they
// were authored with (config.yaml and charter.md blocks are not
// type-checked beyond the enum fields), so truthiness is Python's.
type Policy struct {
	Fields      map[string]any // top-level fields except permissions
	Permissions map[string]any
}

// DefaultPolicy is default_policy().
func DefaultPolicy() *Policy {
	return &Policy{
		Fields: map[string]any{"enabled": true, "timing": "post_completion", "failure_policy": "warn", "write_record": true,
			"generate_proposals": true, "apply_proposals": "require_human", "precedence": nil, "generator": "python"},
		Permissions: map[string]any{"write_record": true, "inspect_mission_artifacts": true, "propose_glossary_changes": true,
			"propose_drg_changes": true, "propose_doctrine_changes": true, "apply_low_risk_changes": false,
			"apply_structural_changes": false},
	}
}

// Enabled is bool(policy.enabled).
func (p *Policy) Enabled() bool { return pyjson.Truthy(p.Fields["enabled"]) }

// BlocksCompletion is _retrospective_blocks_completion: the explicit strict
// pre-completion gate (enabled, timing=before_completion, failure_policy=block).
func (p *Policy) BlocksCompletion() bool {
	return p.Enabled() && p.Fields["timing"] == "before_completion" && p.Fields["failure_policy"] == "block"
}

// ResolutionErrorSource is _resolution_error_source_map().
func ResolutionErrorSource() *jsonx.Object {
	return jsonx.NewObject("enabled", resolutionErr, "timing", resolutionErr, "failure_policy", resolutionErr)
}

// block is one retrospective mapping with its source label.
type block struct {
	o      *jsonx.Object
	prefix string
}

// ResolvePolicy is resolve_policy(repo_root): the policy and its source map,
// or the *PolicyError of a malformed source.
func ResolvePolicy(root string) (*Policy, *jsonx.Object, error) {
	policy := DefaultPolicy()
	source := DefaultPolicySource()

	yamlBlock, err := loadCharterYAMLBlock(root)
	if err != nil {
		if charterYAMLMentionsRetrospective(filepath.Join(root, charterYAMLRel)) {
			markSourceError(source)
			return policy, source, err
		}
		yamlBlock = nil
	}
	mdBlock, err := loadCharterMDBlock(root)
	if err != nil {
		markSourceError(source)
		return policy, source, err
	}
	configBlock, err := loadConfigBlock(root)
	if err != nil {
		markSourceError(source)
		return policy, source, err
	}

	var layers []block
	if mdBlock != nil {
		layers = append(layers, block{mdBlock, charterMDRel + ":retrospective"})
	}
	if yamlBlock != nil {
		layers = append(layers, block{yamlBlock, charterYAMLRel + ":" + yamlBlockPath})
	}
	precedence := ""
	for _, l := range layers {
		if s, ok := l.o.Get("precedence"); ok {
			if str, isStr := s.(string); isStr && contains(enumFields["precedence"], str) {
				precedence = str
			}
		}
	}
	strict := resolveStrictKeys(yamlBlock, mdBlock, configBlock)
	applyLayers := func() error {
		for _, l := range layers {
			if err := applyBlock(policy, source, l.o, l.prefix, strict, nil); err != nil {
				return err
			}
		}
		return nil
	}
	if precedence == "config" {
		if configBlock != nil {
			if err := applyBlock(policy, source, configBlock, configPrefix, strict, nil); err != nil {
				return policy, source, err
			}
		}
		if err := applyLayers(); err != nil {
			return policy, source, err
		}
	} else {
		if err := applyLayers(); err != nil {
			return policy, source, err
		}
		if configBlock != nil {
			claimed := map[string]bool{}
			for _, l := range layers {
				for _, k := range l.o.Keys() {
					if k != "strict_keys" {
						claimed[k] = true
					}
				}
			}
			eligible := map[string]bool{}
			for _, k := range knownKeys {
				if !claimed[k] && k != "strict_keys" {
					eligible[k] = true
				}
			}
			if eligible["permissions"] {
				for _, pk := range knownPermissionKeys {
					eligible["permissions."+pk] = true
				}
			}
			if err := applyBlock(policy, source, configBlock, configPrefix, strict, eligible); err != nil {
				return policy, source, err
			}
		}
	}

	// Env-var observation (FR-015): recorded in the source map, never applied.
	if strings.TrimSpace(os.Getenv("SPEC_KITTY_RETROSPECTIVE")) != "" {
		if source.GetString("enabled") == defaultSource {
			source.Set("enabled", "<env:SPEC_KITTY_RETROSPECTIVE>")
		}
		warnEnvDeprecated("SPEC_KITTY_RETROSPECTIVE", "retrospective.enabled")
	}
	if strings.TrimSpace(os.Getenv("SPEC_KITTY_MODE")) != "" {
		for _, k := range []string{"timing", "failure_policy"} {
			if source.GetString(k) == defaultSource {
				source.Set(k, "<env:SPEC_KITTY_MODE>")
			}
		}
		warnEnvDeprecated("SPEC_KITTY_MODE", "retrospective.timing + retrospective.failure_policy")
	}
	return policy, source, nil
}

var emittedDeprecations = map[string]bool{}

// warnEnvDeprecated is warn_env_var_deprecated: once per process, a yellow
// DEPRECATED: notice on stderr unless SPEC_KITTY_NO_DEPRECATION_WARNINGS=1.
func warnEnvDeprecated(name, replacement string) {
	if emittedDeprecations[name] {
		return
	}
	emittedDeprecations[name] = true
	if os.Getenv("SPEC_KITTY_NO_DEPRECATION_WARNINGS") == "1" {
		return
	}
	rich.Print(os.Stderr, fmt.Sprintf("[yellow]DEPRECATED:[/yellow] %s is a test/dev override only. Set %s instead. See docs/guides/use-retrospective-learning.md", name, replacement))
}

func markSourceError(source *jsonx.Object) {
	for _, k := range source.Keys() {
		if source.GetString(k) == defaultSource {
			source.Set(k, resolutionErr)
		}
	}
}

func resolveStrictKeys(blocks ...*jsonx.Object) bool {
	for _, b := range blocks {
		if b == nil {
			continue
		}
		if v, ok := b.Get("strict_keys"); ok {
			if bv, isBool := v.(bool); isBool {
				return bv
			}
		}
	}
	return false
}

// applyBlock is _apply_block_to_policy.
func applyBlock(policy *Policy, source *jsonx.Object, b *jsonx.Object, prefix string, strict bool, eligible map[string]bool) error {
	var unknown []string
	for _, key := range b.Keys() {
		value, _ := b.Get(key)
		if key == "strict_keys" {
			continue
		}
		if !contains(knownKeys, key) {
			unknown = append(unknown, key)
			continue // ignored unless strict (the module's warning is not shown)
		}
		if eligible != nil && !eligible[key] {
			continue
		}
		if key == "permissions" {
			perms, ok := value.(*jsonx.Object)
			if !ok {
				return &PolicyError{prefix, "invalid_type_for_retrospective_block",
					fmt.Sprintf("'permissions' must be a mapping; got %s.", pyjson.TypeName(value))}
			}
			if err := applyPermissions(policy, source, perms, prefix, strict); err != nil {
				return err
			}
			continue
		}
		if valid, isEnum := enumFields[key]; isEnum {
			if s, ok := value.(string); !ok || !contains(valid, s) {
				sorted := append([]string(nil), valid...)
				sort.Strings(sorted)
				return &PolicyError{prefix, "invalid_enum",
					fmt.Sprintf("%s: got %s, expected one of %s", key, pyjson.Repr(value), pyjson.ReprStrings(sorted))}
			}
		}
		policy.Fields[key] = value
		source.Set(key, prefix+"."+key)
	}
	if strict && len(unknown) > 0 {
		sort.Strings(unknown)
		return &PolicyError{prefix, "unknown_key", "Unknown keys in retrospective block: " + pyjson.ReprStrings(unknown)}
	}
	return nil
}

// applyPermissions is _apply_permissions_block.
func applyPermissions(policy *Policy, source *jsonx.Object, b *jsonx.Object, prefix string, strict bool) error {
	var unknown []string
	for _, key := range b.Keys() {
		value, _ := b.Get(key)
		if !contains(knownPermissionKeys, key) {
			unknown = append(unknown, key)
			continue
		}
		if _, ok := value.(bool); !ok {
			return &PolicyError{prefix, "invalid_enum",
				fmt.Sprintf("permissions.%s: expected boolean, got %s.", key, pyjson.TypeName(value))}
		}
		policy.Permissions[key] = value
		source.Set("permissions."+key, prefix+".permissions."+key)
	}
	if strict && len(unknown) > 0 {
		sort.Strings(unknown)
		return &PolicyError{prefix, "unknown_key", "Unknown permission keys in retrospective block: " + pyjson.ReprStrings(unknown)}
	}
	return nil
}

// readText is Path.read_text(encoding="utf-8"): str(exc) of the OSError or
// UnicodeDecodeError on failure, universal newlines on success.
func readText(p string) (string, bool, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", true, fmt.Errorf("%s", pyjson.OSErrorString(err))
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return "", true, err
	}
	return pyjson.TranslateNewlines(s), true, nil
}

// loadConfigBlock is _load_config_retrospective_block.
func loadConfigBlock(root string) (*jsonx.Object, error) {
	p := filepath.Join(root, configRel)
	if !exists(p) {
		return nil, nil
	}
	raw, _, err := readText(p)
	if err != nil {
		return nil, &PolicyError{configRel, "invalid_yaml", err.Error()}
	}
	data, err := yamlx.ParseWith([]byte(raw), yamlx.ParseOptions{Safe: true})
	if err != nil {
		return nil, &PolicyError{configRel, "invalid_yaml", err.Error()}
	}
	if data == nil {
		return nil, nil
	}
	o, ok := data.(*jsonx.Object)
	if !ok {
		return nil, &PolicyError{configRel, "invalid_type_for_retrospective_block",
			fmt.Sprintf("Config root is not a YAML mapping; got %s.", pyjson.TypeName(data))}
	}
	v, _ := o.Get("retrospective")
	if v == nil {
		return nil, nil
	}
	b, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &PolicyError{configRel, "invalid_type_for_retrospective_block",
			fmt.Sprintf(".kittify/config.yaml 'retrospective:' value must be a mapping; got %s.", pyjson.TypeName(v))}
	}
	return b, nil
}

// loadCharterMDBlock is _load_charter_retrospective_block (frontmatter).
func loadCharterMDBlock(root string) (*jsonx.Object, error) {
	p := filepath.Join(root, charterMDRel)
	if !exists(p) {
		return nil, nil
	}
	raw, _, err := readText(p)
	if err != nil {
		return nil, &PolicyError{charterMDRel, "invalid_yaml", err.Error()}
	}
	if !strings.HasPrefix(raw, "---") {
		return nil, nil
	}
	lines := pyjson.Splitlines(raw)
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if pyjson.Strip(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return nil, &PolicyError{charterMDRel, "invalid_yaml",
			fmt.Sprintf("Charter at %s has an unclosed YAML frontmatter block.", p)}
	}
	data, err := yamlx.ParseWith([]byte(strings.Join(lines[1:closeIdx], "\n")), yamlx.ParseOptions{Safe: true})
	if err != nil {
		return nil, &PolicyError{charterMDRel, "invalid_yaml", err.Error()}
	}
	if data == nil {
		return nil, nil
	}
	o, ok := data.(*jsonx.Object)
	if !ok {
		return nil, &PolicyError{charterMDRel, "invalid_type_for_retrospective_block",
			fmt.Sprintf("Charter frontmatter is not a YAML mapping; got %s.", pyjson.TypeName(data))}
	}
	v, _ := o.Get("retrospective")
	if v == nil {
		return nil, nil
	}
	b, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &PolicyError{charterMDRel, "invalid_type_for_retrospective_block",
			fmt.Sprintf("Charter frontmatter 'retrospective:' value must be a mapping; got %s.", pyjson.TypeName(v))}
	}
	return b, nil
}

// loadCharterYAMLBlock is _load_charter_yaml_retrospective_block: the
// governance.retrospective block validated as RetrospectiveGovernance and
// reduced to the keys the operator authored.
func loadCharterYAMLBlock(root string) (*jsonx.Object, error) {
	p := filepath.Join(root, charterYAMLRel)
	if !exists(p) {
		return nil, nil
	}
	yerr := func(reason, detail string) error { return &PolicyError{charterYAMLRel, reason, detail} }
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, yerr("invalid_yaml", pyjson.OSErrorString(err))
	}
	doc, err := yamlx.ParseWith(b, yamlx.ParseOptions{TextFile: p})
	if err != nil {
		return nil, yerr("invalid_yaml", err.Error())
	}
	if doc == nil {
		return nil, nil
	}
	d, ok := doc.(*jsonx.Object)
	if !ok {
		return nil, yerr("invalid_type_for_retrospective_block",
			fmt.Sprintf("charter.yaml root is not a YAML mapping; got %s.", pydantic.RuamelTypeName(doc)))
	}
	gov, _ := d.Get("governance")
	if gov == nil {
		return nil, nil
	}
	g, ok := gov.(*jsonx.Object)
	if !ok {
		return nil, yerr("invalid_type_for_retrospective_block",
			fmt.Sprintf("charter.yaml 'governance:' value must be a mapping; got %s.", pydantic.RuamelTypeName(gov)))
	}
	raw, _ := g.Get("retrospective")
	if raw == nil {
		return nil, nil
	}
	rb, ok := raw.(*jsonx.Object)
	if !ok {
		return nil, yerr("invalid_type_for_retrospective_block",
			fmt.Sprintf("charter.yaml '%s:' value must be a mapping; got %s.", yamlBlockPath, pydantic.RuamelTypeName(raw)))
	}
	out, errs := validateGovernance(rb)
	if len(errs) > 0 {
		reason := "invalid_enum"
		var parts []string
		for _, e := range errs {
			if e.typ != "literal_error" {
				reason = "invalid_type"
			}
			parts = append(parts, e.loc+": "+e.msg)
		}
		return nil, yerr(reason, strings.Join(parts, "; "))
	}
	if out.Len() == 0 {
		return nil, nil
	}
	return out, nil
}

type govError struct{ loc, msg, typ string }

// laxBool is pydantic's lax bool validation.
func laxBool(v any) (bool, string, string) {
	switch x := v.(type) {
	case bool:
		return x, "", ""
	case int:
		if x == 0 || x == 1 {
			return x == 1, "", ""
		}
		return false, "Input should be a valid boolean, unable to interpret input", "bool_parsing"
	case float64:
		if x == 0 || x == 1 {
			return x == 1, "", ""
		}
		return false, "Input should be a valid boolean, unable to interpret input", "bool_parsing"
	case string:
		switch strings.ToLower(x) {
		case "0", "off", "f", "false", "n", "no":
			return false, "", ""
		case "1", "on", "t", "true", "y", "yes":
			return true, "", ""
		}
		return false, "Input should be a valid boolean, unable to interpret input", "bool_parsing"
	}
	return false, "Input should be a valid boolean", "bool_type"
}

// validateGovernance is RetrospectiveGovernance.model_validate followed by
// model_dump(exclude_none=True) (an empty permissions block dropped).
func validateGovernance(raw *jsonx.Object) (*jsonx.Object, []govError) {
	out := &jsonx.Object{}
	var errs []govError
	boolField := func(o *jsonx.Object, key, loc string, dst *jsonx.Object) {
		v, _ := o.Get(key)
		if v == nil {
			return
		}
		b, msg, typ := laxBool(v)
		if msg != "" {
			errs = append(errs, govError{loc, msg, typ})
			return
		}
		dst.Set(key, b)
	}
	literalField := func(key string) {
		v, _ := raw.Get(key)
		if v == nil {
			return
		}
		valid := enumFields[key]
		if s, ok := v.(string); ok && contains(valid, s) {
			out.Set(key, s)
			return
		}
		errs = append(errs, govError{key, pydantic.LiteralMsg(valid), "literal_error"})
	}
	boolField(raw, "enabled", "enabled", out)
	literalField("timing")
	literalField("failure_policy")
	boolField(raw, "write_record", "write_record", out)
	boolField(raw, "generate_proposals", "generate_proposals", out)
	literalField("apply_proposals")
	if pv, _ := raw.Get("permissions"); pv != nil {
		if po, ok := pv.(*jsonx.Object); ok {
			perms := &jsonx.Object{}
			for _, k := range knownPermissionKeys {
				boolField(po, k, "permissions."+k, perms)
			}
			if perms.Len() > 0 {
				out.Set("permissions", perms)
			}
		} else {
			errs = append(errs, govError{"permissions", "Input should be a valid dictionary or instance of RetrospectiveGovernancePermissions", "model_type"})
		}
	}
	literalField("precedence")
	literalField("generator")
	boolField(raw, "strict_keys", "strict_keys", out)
	return out, errs
}

// charterYAMLMentionsRetrospective is _charter_yaml_mentions_retrospective:
// a malformed charter.yaml that never spells "retrospective" is provably
// unrelated (fail closed when it cannot be read or decoded).
func charterYAMLMentionsRetrospective(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return true
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return true
	}
	return strings.Contains(strings.ToLower(strings.ReplaceAll(s, "\x00", "")), "retrospective")
}
