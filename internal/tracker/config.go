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
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/rtyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
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
type Crash struct {
	Type, Msg string
	// Cause is the exception this one was raised from ("raise ... from
	// exc"), rendered first in the traceback; Frames are its upstream
	// frames (spec_kitty_tracker raise sites).
	Cause  *Crash
	Frames []rich.Frame
}

// Exception converts the crash (and its cause chain) for rich.PrintTraceback;
// frames are the caller's command-level frames.
func (e *Crash) Exception(frames ...rich.Frame) *rich.Exception {
	x := &rich.Exception{Type: e.Type, Value: e.Msg, Frames: append(append([]rich.Frame(nil), frames...), e.Frames...)}
	if e.Cause != nil {
		x.Cause = e.Cause.Exception()
	}
	return x
}

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
	// loaded / rtBlock are the tracker block as loaded (plain and round
	// trip): _extra and egress values keep their ruamel types on save.
	loaded  *jsonx.Object
	rtBlock *rtyaml.Map
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
	// YAML().load(open(path, encoding="utf-8")): a text stream
	v, err := yamlx.ParseWith(b, yamlx.ParseOptions{TextFile: p})
	return v, b, true, err
}

// loadDoc is the round-trip load (YAML() with preserve_quotes) of a
// config.yaml whose plain parse already succeeded.
func loadDoc(root string, src []byte) (*rtyaml.Doc, error) {
	in, err := pyyaml.TextFileInput(src, ConfigPath(root))
	if err != nil {
		return nil, err
	}
	return rtyaml.Load(in)
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
		c.loaded = t
		if doc, err := loadDoc(root, src); err == nil {
			if m, ok := doc.Root.(*rtyaml.Map); ok {
				if b, ok := m.Get("tracker"); ok {
					c.rtBlock, _ = b.(*rtyaml.Map)
				}
			}
		}
		if c.rtBlock != nil {
			if e, ok := c.rtBlock.Get("egress"); ok {
				if q, ok := e.(*rtyaml.Str); ok && (q.Style == '"' || q.Style == '\'') {
					c.EgressStyle = byte(q.Style)
				}
			}
		}
	}
	return c, nil
}

// rtValue is the value to_dict() carries for a tracker key taken over from
// the loaded block (_extra entries and egress keep their ruamel types:
// quote style, comments), unless the value was changed since loading.
func (c ProjectConfig) rtValue(key string, v any) any {
	if c.rtBlock == nil || c.loaded == nil {
		return v
	}
	lv, ok := c.loaded.Get(key)
	if !ok || !rtyaml.PlainEqual(lv, v) {
		return v
	}
	if rv, ok := c.rtBlock.Get(key); ok {
		return rv
	}
	return v
}

// toRT is to_dict() for dumping: a plain mapping holding the loaded
// round-trip values of _extra and egress.
func (c ProjectConfig) toRT() *rtyaml.Map {
	m := rtyaml.NewMap()
	for _, kv := range c.ToDict().Pairs() {
		v := kv.Value
		if !knownKeys[kv.Key] || kv.Key == "egress" {
			v = c.rtValue(kv.Key, v)
		}
		m.Set(kv.Key, v)
	}
	return m
}

// rootMap is `payload = yaml.load(handle) or {}` + `if not isinstance(
// payload, dict): payload = {}`.
func rootMap(doc *rtyaml.Doc) *rtyaml.Map {
	if doc != nil {
		if m, ok := doc.Root.(*rtyaml.Map); ok && m.Len() > 0 {
			return m
		}
	}
	return rtyaml.NewMap()
}

// SaveConfig mirrors save_tracker_config: the round-trip document gets
// payload["tracker"] = config.to_dict() and is dumped back (comments, quote
// styles and formatting elsewhere kept), written atomically.
func SaveConfig(root string, c ProjectConfig) error {
	_, src, exists, err := readPayload(root)
	if err != nil {
		return err
	}
	doc := &rtyaml.Doc{}
	if exists {
		if doc, err = loadDoc(root, src); err != nil {
			return err
		}
	}
	payload := rootMap(doc)
	payload.Set("tracker", c.toRT())
	doc.Root = payload
	out, err := doc.Dump()
	if err != nil {
		return err
	}
	return atomicfile.Write(ConfigPath(root), []byte(out))
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
	doc, err := loadDoc(root, src)
	if err != nil {
		return err
	}
	rt := rootMap(doc)
	tb, _ := payload.Get("tracker")
	t, _ := tb.(*jsonx.Object)
	recorded := FromDict(t).Egress
	if recorded == EgressAbsent {
		rt.Delete("tracker")
	} else {
		raw := recorded
		if b, ok := rt.Get("tracker"); ok {
			if bm, ok := b.(*rtyaml.Map); ok {
				if e, ok := bm.Get("egress"); ok {
					raw = e
				}
			}
		}
		block := rtyaml.NewMap()
		block.Set("egress", raw)
		rt.Set("tracker", block)
	}
	doc.Root = rt
	out, err := doc.Dump()
	if err != nil {
		return err
	}
	p := ConfigPath(root)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(out); err != nil {
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
