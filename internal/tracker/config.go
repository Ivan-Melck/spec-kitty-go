// Package tracker ports specify_cli.tracker: the project-scoped tracker
// binding in .kittify/config.yaml, the machine-global credential file
// (~/.spec-kitty/credentials, TOML), the per-binding SQLite cache
// (~/.spec-kitty/trackers/<scope>.db), the tracker-egress verdict and the
// local (beads/fp) sync engine that drives the providers' CLIs.
package tracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
	"gopkg.in/yaml.v3"
)

// Provider classification (config.LOCAL_PROVIDERS / REMOVED_PROVIDERS).
var (
	LocalProviders   = []string{"beads", "fp"}
	RemovedProviders = []string{"azure_devops", "github", "gitlab", "jira", "linear"}
)

// OwnershipModes are the accepted --ownership-mode values, in upstream order.
var OwnershipModes = []string{"external_authoritative", "spec_kitty_authoritative", "split_ownership"}

// IsLocal reports whether p is a local (beads/fp) provider.
func IsLocal(p string) bool { return contains(LocalProviders, p) }

// IsRemoved reports whether p is a removed (former hosted) provider.
func IsRemoved(p string) bool { return contains(RemovedProviders, p) }

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// NormalizeProvider is factory.normalize_provider.
func NormalizeProvider(p string) string { return strings.ToLower(strings.TrimSpace(p)) }

// Error is a RuntimeError-class tracker failure (TrackerServiceError,
// TrackerConfigError, LocalTrackerServiceError, ...): the CLI renders its
// message as one red line and exits 1.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errorf(format string, a ...any) error { return &Error{Msg: fmt.Sprintf(format, a...)} }

// Crash is an exception upstream does not catch (sqlite3.IntegrityError,
// spec_kitty_tracker errors): the CLI prints a traceback and exits 1.
type Crash struct{ Type, Msg string }

func (e *Crash) Error() string {
	if e.Msg == "" {
		return e.Type
	}
	return e.Type + ": " + e.Msg
}

type egressAbsent struct{}

func (egressAbsent) String() string { return "EGRESS_ABSENT" }

// EgressAbsent marks a tracker block without an `egress` key.
var EgressAbsent any = egressAbsent{}

// Egress consent values.
const (
	EgressRefused   = "refused"
	EgressPermitted = "permitted"
)

// ProjectConfig is TrackerProjectConfig. Empty strings stand for None.
type ProjectConfig struct {
	Provider        string
	BindingRef      string
	ProjectSlug     string
	DisplayLabel    string
	ProviderContext *jsonx.Object // nil = None
	Workspace       string
	OwnershipMode   string
	FieldOwners     *jsonx.Object
	// Egress is the raw recorded value (EgressAbsent when missing).
	Egress any
	// EgressStyle is the YAML quote style of a string egress value as
	// loaded ('"', '\'' or 0), preserved on save (ruamel preserve_quotes).
	EgressStyle byte
	Extra       *jsonx.Object
}

// NewProjectConfig returns the defaults.
func NewProjectConfig() ProjectConfig {
	return ProjectConfig{OwnershipMode: "external_authoritative", FieldOwners: &jsonx.Object{}, Egress: EgressAbsent, Extra: &jsonx.Object{}}
}

// IsConfigured mirrors TrackerProjectConfig.is_configured.
func (c ProjectConfig) IsConfigured() bool {
	return c.Provider != "" && IsLocal(c.Provider) && c.Workspace != ""
}

// EgressFault mirrors TrackerProjectConfig.egress_fault.
func (c ProjectConfig) EgressFault() bool {
	if c.Egress == EgressAbsent {
		return false
	}
	s, ok := c.Egress.(string)
	return !(ok && (s == EgressRefused || s == EgressPermitted))
}

func optStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ToDict mirrors TrackerProjectConfig.to_dict.
func (c ProjectConfig) ToDict() *jsonx.Object {
	o := &jsonx.Object{}
	if c.Extra != nil {
		for _, kv := range c.Extra.Pairs() {
			o.Set(kv.Key, kv.Value)
		}
	}
	o.Set("provider", optStr(c.Provider))
	o.Set("binding_ref", optStr(c.BindingRef))
	o.Set("project_slug", optStr(c.ProjectSlug))
	o.Set("display_label", optStr(c.DisplayLabel))
	if c.ProviderContext != nil && c.ProviderContext.Len() > 0 {
		o.Set("provider_context", c.ProviderContext.Clone())
	} else {
		o.Set("provider_context", nil)
	}
	o.Set("workspace", optStr(c.Workspace))
	fo := &jsonx.Object{}
	if c.FieldOwners != nil {
		fo = c.FieldOwners.Clone()
	}
	mode := c.OwnershipMode
	if mode == "" {
		mode = "external_authoritative"
	}
	o.Set("ownership", jsonx.NewObject("mode", mode, "field_owners", fo))
	if c.Egress != EgressAbsent {
		o.Set("egress", c.Egress)
	}
	return o
}

var knownKeys = map[string]bool{"provider": true, "binding_ref": true, "project_slug": true, "display_label": true, "provider_context": true, "workspace": true, "ownership": true, "egress": true}

func stripStr(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// FromDict mirrors TrackerProjectConfig.from_dict (nil -> defaults).
func FromDict(data *jsonx.Object) ProjectConfig {
	c := NewProjectConfig()
	if data == nil {
		return c
	}
	if block, ok := data.Get("ownership"); ok {
		if b, ok := block.(*jsonx.Object); ok {
			if m, ok := b.Get("mode"); ok {
				if s := stripStr(m); s != "" {
					c.OwnershipMode = s
				}
			}
			if raw, ok := b.Get("field_owners"); ok {
				if fo, ok := raw.(*jsonx.Object); ok {
					for _, kv := range fo.Pairs() {
						v := pyjson.Str(kv.Value)
						if strings.TrimSpace(kv.Key) != "" && strings.TrimSpace(v) != "" {
							c.FieldOwners.Set(kv.Key, v)
						}
					}
				}
			}
		}
	}
	get := func(k string) any { v, _ := data.Get(k); return v }
	c.Provider = stripStr(get("provider"))
	c.BindingRef = stripStr(get("binding_ref"))
	c.ProjectSlug = stripStr(get("project_slug"))
	c.DisplayLabel = stripStr(get("display_label"))
	c.Workspace = stripStr(get("workspace"))
	if pc, ok := get("provider_context").(*jsonx.Object); ok {
		c.ProviderContext = &jsonx.Object{}
		for _, kv := range pc.Pairs() {
			c.ProviderContext.Set(kv.Key, pyjson.Str(kv.Value))
		}
	}
	if v, ok := data.Get("egress"); ok {
		c.Egress = v
	}
	for _, kv := range data.Pairs() {
		if !knownKeys[kv.Key] {
			c.Extra.Set(kv.Key, kv.Value)
		}
	}
	return c
}

// ConfigPath is <root>/.kittify/config.yaml.
func ConfigPath(root string) string { return filepath.Join(root, ".kittify", "config.yaml") }

// readPayload loads config.yaml; ok=false when the file does not exist.
func readPayload(root string) (any, []byte, bool, error) {
	p := ConfigPath(root)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, false, nil
		}
		return nil, nil, true, err
	}
	v, err := yamlx.Parse(b)
	return v, b, true, err
}

// LoadConfig mirrors load_tracker_config.
func LoadConfig(root string) (ProjectConfig, error) {
	v, src, exists, err := readPayload(root)
	if !exists {
		return NewProjectConfig(), nil
	}
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			return ProjectConfig{}, err
		}
		return ProjectConfig{}, errorf("Failed to parse %s: %v", ConfigPath(root), err)
	}
	payload, _ := v.(*jsonx.Object)
	if payload == nil {
		return NewProjectConfig(), nil
	}
	tb, _ := payload.Get("tracker")
	t, _ := tb.(*jsonx.Object)
	c := FromDict(t)
	if t != nil {
		c.EgressStyle = egressStyle(src)
	}
	return c, nil
}

// egressStyle returns the quote style of tracker.egress in the YAML source.
func egressStyle(src []byte) byte {
	var n yaml.Node
	if yaml.Unmarshal(src, &n) != nil || len(n.Content) == 0 {
		return 0
	}
	root := n.Content[0]
	if root.Kind != yaml.MappingNode {
		return 0
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "tracker" || root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		tb := root.Content[i+1]
		for j := 0; j+1 < len(tb.Content); j += 2 {
			if tb.Content[j].Value == "egress" && tb.Content[j+1].Kind == yaml.ScalarNode {
				switch tb.Content[j+1].Style {
				case yaml.DoubleQuotedStyle:
					return '"'
				case yaml.SingleQuotedStyle:
					return '\''
				}
			}
		}
	}
	return 0
}

// renderPayload emits config.yaml, restoring the quote style of a string
// tracker.egress value.
func renderPayload(payload *jsonx.Object, egress any, style byte) string {
	out := yamlx.Emit(payload)
	s, ok := egress.(string)
	if !ok || style == 0 {
		return out
	}
	plain := "  egress: " + yamlx.FormatScalar(s) + "\n"
	var quoted string
	if style == '"' {
		quoted = "  egress: " + jsonx.QuoteString(s, false) + "\n"
	} else {
		quoted = "  egress: '" + strings.ReplaceAll(s, "'", "''") + "'\n"
	}
	lines := strings.SplitAfter(out, "\n")
	in := false
	for i, l := range lines {
		if l == "tracker:\n" || strings.HasPrefix(l, "tracker:") {
			in = true
			continue
		}
		if in && l != "" && !strings.HasPrefix(l, " ") {
			in = false
		}
		if in && l == plain {
			lines[i] = quoted
			break
		}
	}
	return strings.Join(lines, "")
}

// SaveConfig mirrors save_tracker_config (atomic write, other sections kept).
func SaveConfig(root string, c ProjectConfig) error {
	v, _, _, err := readPayload(root)
	if err != nil {
		return err
	}
	payload, ok := v.(*jsonx.Object)
	if !ok || payload == nil {
		payload = &jsonx.Object{}
	}
	payload.Set("tracker", c.ToDict())
	return atomicfile.Write(ConfigPath(root), []byte(renderPayload(payload, c.Egress, c.EgressStyle)))
}

// ClearConfig mirrors clear_tracker_config: the tracker block is removed,
// except that a recorded egress decision outlives the binding.
func ClearConfig(root string) error {
	v, src, exists, err := readPayload(root)
	if !exists {
		return nil
	}
	if err != nil {
		return err
	}
	payload, ok := v.(*jsonx.Object)
	if !ok || payload == nil || !payload.Has("tracker") {
		return nil
	}
	tb, _ := payload.Get("tracker")
	t, _ := tb.(*jsonx.Object)
	recorded := FromDict(t).Egress
	style := byte(0)
	if recorded == EgressAbsent {
		payload.Delete("tracker")
	} else {
		payload.Set("tracker", jsonx.NewObject("egress", recorded))
		style = egressStyle(src)
	}
	p := ConfigPath(root)
	mode := os.FileMode(0o644)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, mode)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(renderPayload(payload, recorded, style)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RequireRepoRoot mirrors require_repo_root.
func RequireRepoRoot() (string, error) {
	root := project.Locate("")
	if root == "" {
		return "", errorf("Not inside a spec-kitty project. Run this command from a project with .kittify/.")
	}
	return root, nil
}

// SortedCopy returns a sorted copy of xs.
func SortedCopy(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}
