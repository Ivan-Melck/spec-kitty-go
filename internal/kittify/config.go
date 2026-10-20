// Package kittify owns the project-level .kittify/ metadata files:
// config.yaml (project identity, agents, vcs, mission-type activations),
// metadata.yaml (version / schema stamp / applied migrations) and
// canonical-events.jsonl. Other areas should read configuration through this
// package instead of parsing config.yaml themselves.
//
// It mirrors specify_cli.core.agent_config, identity/project.py,
// upgrade/metadata.py and init's _save_vcs_config / _stamp_schema_metadata.
package kittify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Dir is the project metadata directory name.
const Dir = ".kittify"

// ConfigFile, MetadataFile and EventsFile are the managed file names.
const (
	ConfigFile   = "config.yaml"
	MetadataFile = "metadata.yaml"
	EventsFile   = "canonical-events.jsonl"
)

// Path returns <root>/.kittify/<parts...>.
func Path(root string, parts ...string) string {
	return filepath.Join(append([]string{root, Dir}, parts...)...)
}

// ConfigPath is <root>/.kittify/config.yaml.
func ConfigPath(root string) string { return Path(root, ConfigFile) }

// ErrConfig wraps config.yaml parse/shape failures (AgentConfigError).
type ErrConfig struct{ Msg string }

func (e *ErrConfig) Error() string { return e.Msg }

// LoadConfig parses .kittify/config.yaml. A missing or empty file yields an
// empty object; invalid YAML or a non-mapping document is an *ErrConfig.
func LoadConfig(root string) (*jsonx.Object, error) {
	p := ConfigPath(root)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &jsonx.Object{}, nil
		}
		return nil, &ErrConfig{fmt.Sprintf("Invalid YAML in %s: %v", p, err)}
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, &ErrConfig{fmt.Sprintf("Invalid YAML in %s: %v", p, err)}
	}
	if v == nil {
		return &jsonx.Object{}, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &ErrConfig{fmt.Sprintf("Invalid config shape in %s: expected a YAML mapping at the top level, got %s", p, pyTypeName(v))}
	}
	return o, nil
}

// ReadConfig is LoadConfig that never fails (empty object on any error).
func ReadConfig(root string) *jsonx.Object {
	o, err := LoadConfig(root)
	if err != nil || o == nil {
		return &jsonx.Object{}
	}
	return o
}

// ConfigExists reports whether .kittify/config.yaml exists.
func ConfigExists(root string) bool {
	_, err := os.Stat(ConfigPath(root))
	return err == nil
}

// RenderConfig renders cfg in the ruamel round-trip style upstream uses for
// config.yaml (2-space mappings, sequences not indented under their key).
func RenderConfig(cfg *jsonx.Object) string {
	if cfg == nil || cfg.Len() == 0 {
		return "{}\n"
	}
	return yamlx.Emit(cfg)
}

// SaveConfig writes cfg to config.yaml in place (open(..., "w") semantics:
// an existing file keeps its mode, a new file is created 0644).
func SaveConfig(root string, cfg *jsonx.Object) error {
	p := ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writeInPlace(p, []byte(RenderConfig(cfg)), 0o644)
}

// UpdateConfig loads config.yaml, applies fn and saves it.
func UpdateConfig(root string, fn func(cfg *jsonx.Object) error) error {
	cfg, err := LoadConfig(root)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return SaveConfig(root, cfg)
}

func writeInPlace(path string, data []byte, newMode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, newMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
	case []any:
		return "list"
	case nil:
		return "NoneType"
	}
	return fmt.Sprintf("%T", v)
}

// ---------------------------------------------------------------------------
// agents section
// ---------------------------------------------------------------------------

// AgentConfig is the agents: section (core/agent_config.AgentConfig).
type AgentConfig struct {
	Available  []string
	AutoCommit bool
	LintOnEdit bool
}

// KnownAgent reports whether an agent key is valid; set by internal/agents
// at init time to avoid an import cycle (defaults to "any non-empty key").
var KnownAgent = func(key string) bool { return key != "" }

// ValidAgentKeys lists all valid keys for error messages (set by internal/agents).
var ValidAgentKeys = func() []string { return nil }

// LoadAgentConfig reads the agents (or legacy tools) section with upstream's
// defaults and validation (load_agent_config).
func LoadAgentConfig(root string) (AgentConfig, error) {
	out := AgentConfig{AutoCommit: true}
	if !ConfigExists(root) {
		return out, nil
	}
	data, err := LoadConfig(root)
	if err != nil {
		return out, err
	}
	section := "agents"
	raw, ok := data.Get(section)
	if !ok || raw == nil || isEmptyObject(raw) {
		section = "tools"
		raw, ok = data.Get(section)
	}
	var agents *jsonx.Object
	if ok && raw != nil {
		o, isObj := raw.(*jsonx.Object)
		if !isObj {
			return out, &ErrConfig{fmt.Sprintf("Invalid config shape in %s: expected a YAML mapping for %s, got %s", ConfigPath(root), section, pyTypeName(raw))}
		}
		agents = o
	} else {
		agents = &jsonx.Object{}
	}
	ac, hasAC := agents.Get("auto_commit")
	if !hasAC || ac == nil {
		ac, _ = data.Get("auto_commit")
	}
	if b, isB := ac.(bool); isB {
		out.AutoCommit = b
	}
	le, hasLE := agents.Get("lint_on_edit")
	if !hasLE || le == nil {
		le, _ = data.Get("lint_on_edit")
	}
	if b, isB := le.(bool); isB {
		out.LintOnEdit = b
	}
	if agents.Len() == 0 {
		return out, nil
	}
	av, _ := agents.Get("available")
	switch t := av.(type) {
	case nil:
	case string:
		out.Available = []string{t}
	case []any:
		for _, x := range t {
			s, isS := x.(string)
			if !isS {
				s = fmt.Sprint(x)
			}
			out.Available = append(out.Available, s)
		}
	default:
		return out, &ErrConfig{"Invalid agents.available in config.yaml: expected a list of agent keys"}
	}
	var invalid []string
	for _, a := range out.Available {
		if !KnownAgent(a) {
			invalid = append(invalid, a)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		valid := append([]string(nil), ValidAgentKeys()...)
		sort.Strings(valid)
		return out, &ErrConfig{fmt.Sprintf("Unknown agent key(s) in config.yaml: %s. Valid agents: %s", strings.Join(invalid, ", "), strings.Join(valid, ", "))}
	}
	return out, nil
}

func isEmptyObject(v any) bool {
	o, ok := v.(*jsonx.Object)
	return ok && o.Len() == 0
}

// SaveAgentConfig replaces the agents: section (save_agent_config).
func SaveAgentConfig(root string, ac AgentConfig) error {
	cfg := &jsonx.Object{}
	if ConfigExists(root) {
		var err error
		if cfg, err = LoadConfig(root); err != nil {
			return err
		}
	}
	avail := make([]any, len(ac.Available))
	for i, a := range ac.Available {
		avail[i] = a
	}
	cfg.Set("agents", jsonx.NewObject("available", avail, "auto_commit", ac.AutoCommit, "lint_on_edit", ac.LintOnEdit))
	return SaveConfig(root, cfg)
}

// Agents returns the configured agent keys (empty on any error).
func Agents(root string) []string {
	ac, err := LoadAgentConfig(root)
	if err != nil {
		return nil
	}
	return ac.Available
}

// AutoCommit returns agents.auto_commit (default true; true on error).
func AutoCommit(root string) bool {
	ac, err := LoadAgentConfig(root)
	if err != nil {
		return true
	}
	return ac.AutoCommit
}

// ---------------------------------------------------------------------------
// vcs / activations
// ---------------------------------------------------------------------------

// VCSType returns vcs.type ("" when absent).
func VCSType(root string) string {
	if v := ReadConfig(root).GetObject("vcs"); v != nil {
		return v.GetString("type")
	}
	return ""
}

// SaveVCSConfig sets vcs: {type: git} (init._save_vcs_config).
func SaveVCSConfig(root string) error {
	return UpdateConfig(root, func(cfg *jsonx.Object) error {
		cfg.Set("vcs", jsonx.NewObject("type", "git"))
		return nil
	})
}

// MissionTypeActivations returns mission_type_activations (nil when absent).
func MissionTypeActivations(root string) []string {
	l := ReadConfig(root).GetList("mission_type_activations")
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
