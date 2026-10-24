package doctrine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// ConfigError is upstream ActiveCharterConfigError (code
// ACTIVE_CHARTER_CONFIG_INVALID): invalid config / activation shape.
type ConfigError struct{ Body string }

func (e *ConfigError) Error() string { return e.Body }

// Code is the stable error code.
func (e *ConfigError) Code() string { return "ACTIVE_CHARTER_CONFIG_INVALID" }

func configError(msg string) error {
	return &ConfigError{Body: msg + "\nRemediation: fix .kittify/config.yaml (or the charter.yaml it points to) or run `spec-kitty upgrade` to restore the default active charter shape."}
}

// ConfigPath is .kittify/config.yaml of repoRoot.
func ConfigPath(repoRoot string) string { return filepath.Join(repoRoot, ".kittify", "config.yaml") }

// LoadConfig parses .kittify/config.yaml (empty object when absent).
// Invalid YAML or a non-mapping root is a ConfigError.
func LoadConfig(repoRoot string) (*jsonx.Object, error) {
	b, err := os.ReadFile(ConfigPath(repoRoot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &jsonx.Object{}, nil
		}
		return nil, configError(fmt.Sprintf("Invalid YAML in .kittify/config.yaml: %v", err))
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, configError(fmt.Sprintf("Invalid YAML in .kittify/config.yaml: %v", err))
	}
	if v == nil {
		return &jsonx.Object{}, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, configError(".kittify/config.yaml root must be a mapping.")
	}
	return o, nil
}

// CharterPointer resolves the config.yaml `charter:` pointer (a string
// value) to a path, or "" when absent / not a string.
func CharterPointer(repoRoot string, cfg *jsonx.Object) string {
	v, ok := cfg.Get("charter")
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	if filepath.IsAbs(s) {
		return s
	}
	return filepath.Join(repoRoot, s)
}

// ActivationSource returns the mapping activation keys are read from: the
// charter.yaml named by the config `charter:` pointer, else config.yaml.
func ActivationSource(repoRoot string) (*jsonx.Object, error) {
	cfg, err := LoadConfig(repoRoot)
	if err != nil {
		return nil, err
	}
	p := CharterPointer(repoRoot, cfg)
	if p == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, configError(fmt.Sprintf(".kittify/config.yaml 'charter:' pointer names %s, which does not exist.", p))
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, configError(fmt.Sprintf("Invalid YAML in %s: %v", p, err))
	}
	if v == nil {
		return &jsonx.Object{}, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, configError(fmt.Sprintf("%s root must be a mapping.", p))
	}
	return o, nil
}

// IDSet is a three-state activation value: nil = key absent (everything
// available), empty non-nil = explicitly nothing, else the listed ids.
type IDSet map[string]bool

// Has reports membership; a nil set admits everything.
func (s IDSet) Has(id string) bool { return s == nil || s[id] }

// Sorted returns the ids sorted.
func (s IDSet) Sorted() []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Activation is the project activation state (upstream PackContext).
type Activation struct {
	// ActivatedKinds holds plural kind names; all kinds when the key is absent.
	ActivatedKinds IDSet
	// MissionTypes holds mission_type_activations in declaration order.
	MissionTypes []string
	// ByKind holds the three-state activated_<plural> lists.
	ByKind map[Kind]IDSet
	// OrgPackNames / OrgRoots in declaration order.
	OrgPackNames []string
	OrgRoots     []string
}

// Activated returns the three-state set for kind (nil when absent).
func (a *Activation) Activated(k Kind) IDSet {
	if a == nil {
		return nil
	}
	return a.ByKind[k]
}

func readList(o *jsonx.Object, key string) (IDSet, []string, error) {
	v, ok := o.Get(key)
	if !ok || v == nil {
		return nil, nil, nil
	}
	l, ok := v.([]any)
	if !ok {
		return nil, nil, configError(fmt.Sprintf("Activation key '%s' must be a list, got %s.", key, pyTypeName(v)))
	}
	set := IDSet{}
	var order []string
	for _, x := range l {
		s := pyStr(x)
		if !set[s] {
			order = append(order, s)
		}
		set[s] = true
	}
	return set, order, nil
}

// LoadActivation reads the activation state of repoRoot.
func LoadActivation(repoRoot string) (*Activation, error) {
	src, err := ActivationSource(repoRoot)
	if err != nil {
		return nil, err
	}
	a := &Activation{ByKind: map[Kind]IDSet{}}
	kinds, _, err := readList(src, "activated_kinds")
	if err != nil {
		return nil, err
	}
	if kinds == nil {
		kinds = IDSet{}
		for _, k := range AllKinds {
			kinds[k.Plural()] = true
		}
	}
	a.ActivatedKinds = kinds
	_, mts, err := readList(src, "mission_type_activations")
	if err != nil {
		return nil, err
	}
	a.MissionTypes = mts
	for _, k := range AllKinds {
		if !k.Activatable() || k == Template {
			continue
		}
		s, _, err := readList(src, "activated_"+k.Plural())
		if err != nil {
			return nil, err
		}
		a.ByKind[k] = s
	}
	if a.ByKind[Skill] == nil {
		// absent activated_skills => only org-required skills are in force.
		a.ByKind[Skill] = IDSet{}
	}
	for _, p := range OrgPacks(repoRoot) {
		a.OrgPackNames = append(a.OrgPackNames, p.Name)
		a.OrgRoots = append(a.OrgRoots, p.Root)
	}
	return a, nil
}

// pyStr renders a YAML scalar like Python str().
func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return jsonx.FormatFloat(t)
	}
	return fmt.Sprint(v)
}

func pyTypeName(v any) string {
	switch v.(type) {
	case string:
		return "str"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case *jsonx.Object:
		return "CommentedMap"
	case []any:
		return "CommentedSeq"
	}
	return "NoneType"
}
