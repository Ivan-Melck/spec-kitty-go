package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// ProfilesAssetDir holds the built-in agent profiles.
const ProfilesAssetDir = "packs/built-in/agent_profiles"

// Profile is the subset of charter.profiles.AgentProfile used by native
// projections.
type Profile struct {
	ID                string
	Name              string
	Description       string
	Purpose           string
	Roles             []string
	PrimaryFocus      string
	AvoidanceBoundary string
	Sentinel          bool
	// AppliesToLanguages is the profile's language scope (empty = always
	// applicable).
	AppliesToLanguages []string
	// Optional codex fields.
	Model, ReasoningEffort, SandboxMode string
	// Source provenance.
	SourceFile string // file name within ProfilesAssetDir
	SourceHash string // sha256 of the source YAML bytes
}

func strField(o *jsonx.Object, keys ...string) string {
	for _, k := range keys {
		if v, ok := o.Get(k); ok && v != nil {
			if s, isS := v.(string); isS {
				return s
			}
			return yamlx.FormatScalar(v)
		}
	}
	return ""
}

// ParseProfile parses one *.agent.yaml document.
func ParseProfile(b []byte) (Profile, error) {
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return Profile{}, fmt.Errorf("invalid agent profile: %v", err)
	}
	p := Profile{
		ID:              strField(o, "profile-id", "profile_id"),
		Name:            strField(o, "name"),
		Description:     strField(o, "description"),
		Purpose:         strField(o, "purpose"),
		Model:           strField(o, "model"),
		ReasoningEffort: strField(o, "model-reasoning-effort", "model_reasoning_effort"),
		SandboxMode:     strField(o, "sandbox-mode", "sandbox_mode"),
		Sentinel:        o.GetBool("sentinel"),
	}
	for _, l := range o.GetList("applies_to_languages") {
		p.AppliesToLanguages = append(p.AppliesToLanguages, fmt.Sprint(l))
	}
	for _, r := range o.GetList("roles") {
		p.Roles = append(p.Roles, fmt.Sprint(r))
	}
	if s := o.GetObject("specialization"); s != nil {
		p.PrimaryFocus = strField(s, "primary-focus", "primary_focus")
		p.AvoidanceBoundary = strField(s, "avoidance-boundary", "avoidance_boundary")
	}
	if p.ID == "" {
		return p, fmt.Errorf("agent profile has no profile-id")
	}
	sum := sha256.Sum256(b)
	p.SourceHash = hex.EncodeToString(sum[:])
	return p, nil
}

// BuiltinProfiles loads the shipped profiles (sorted by id), excluding
// sentinel profiles (human-in-charge), which have no native projection.
func BuiltinProfiles() ([]Profile, error) {
	ents, err := assets.List(ProfilesAssetDir)
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".agent.yaml") {
			continue
		}
		b, err := assets.ReadFile(ProfilesAssetDir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		p, err := ParseProfile(b)
		if err != nil {
			continue
		}
		p.SourceFile = e.Name()
		if p.Sentinel {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ProfileHarness is one native named-agent projection format.
type ProfileHarness struct {
	Tool   string // agent key
	Format string // manifest format key
	Dir    string // project-relative output directory
	Suffix string
}

// ProfileHarnesses lists the harnesses with a verified native agent format.
var ProfileHarnesses = map[string]ProfileHarness{
	"claude":  {"claude", "claude-agent", ".claude/agents", ".md"},
	"copilot": {"copilot", "copilot-agent", ".github/agents", ".agent.md"},
	"codex":   {"codex", "codex-agent", ".codex/agents", ".toml"},
	"auggie":  {"auggie", "augment-agent", ".augment/agents", ".md"},
}

// OutputPath is the project-relative projection path.
func (h ProfileHarness) OutputPath(p Profile) string { return h.Dir + "/" + p.ID + h.Suffix }

func profileYAMLScalar(v string) string {
	e := strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`)
	e = strings.ReplaceAll(strings.ReplaceAll(e, "\n", " "), "\r", " ")
	return `"` + e + `"`
}

// RenderMarkdownProfile is _render_helpers.render_markdown_agent.
func RenderMarkdownProfile(p Profile) string {
	desc := p.Description
	if desc == "" {
		desc = p.Purpose
	}
	avoid := p.AvoidanceBoundary
	if avoid == "" {
		avoid = "(none declared)"
	}
	lines := []string{
		"---",
		"name: " + p.ID,
		"description: " + profileYAMLScalar(desc),
		"roles: [" + strings.Join(p.Roles, ", ") + "]",
		"---",
		"",
		"# " + p.Name,
		"",
		p.Purpose,
		"",
		"## Specialization",
		"",
		"- Primary focus: " + p.PrimaryFocus,
		"- Avoidance boundary: " + avoid,
		"",
		"_Projected from Spec Kitty agent profile `" + p.ID + "`; do not edit by hand._",
		"",
	}
	return strings.Join(lines, "\n")
}

// TOMLString renders a tomli_w basic string.
func TOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RenderCodexProfile is CodexProfileRenderer.render (tomli_w.dumps).
func RenderCodexProfile(p Profile) string {
	name := p.Name
	if name == "" {
		name = p.ID
	}
	desc := p.Description
	if desc == "" {
		desc = p.Purpose
	}
	out := "name = " + TOMLString(name) + "\n" +
		"description = " + TOMLString(desc) + "\n" +
		"developer_instructions = " + TOMLString(p.Purpose) + "\n"
	if p.Model != "" {
		out += "model = " + TOMLString(p.Model) + "\n"
	}
	if p.ReasoningEffort != "" {
		out += "model_reasoning_effort = " + TOMLString(p.ReasoningEffort) + "\n"
	}
	if p.SandboxMode != "" {
		out += "sandbox_mode = " + TOMLString(p.SandboxMode) + "\n"
	}
	return out
}

// RenderProfile renders p for harness h.
func RenderProfile(h ProfileHarness, p Profile) string {
	if h.Format == "codex-agent" {
		return RenderCodexProfile(p)
	}
	return RenderMarkdownProfile(p)
}

// ProfileManifestPath is .kittify/agent_profiles_manifest.json.
func ProfileManifestPath(root string) string {
	return filepath.Join(root, ".kittify", "agent_profiles_manifest.json")
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// loadProfileManifest returns entries keyed by output_path.
func loadProfileManifest(root string) map[string]*jsonx.Object {
	out := map[string]*jsonx.Object{}
	b, err := os.ReadFile(ProfileManifestPath(root))
	if err != nil {
		return out
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return out
	}
	for _, e := range o.GetList("entries") {
		if eo, ok := e.(*jsonx.Object); ok {
			out[eo.GetString("output_path")] = eo
		}
	}
	return out
}

func saveProfileManifest(root string, entries map[string]*jsonx.Object) error {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	l := make([]any, 0, len(keys))
	for _, k := range keys {
		l = append(l, entries[k])
	}
	content := jsonx.Dumps(jsonx.NewObject("schema_version", 1, "entries", l), jsonx.SortedPretty) + "\n"
	p := ProfileManifestPath(root)
	if b, err := os.ReadFile(p); err == nil && string(b) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// ProjectionResult reports profile projection outcomes.
type ProjectionResult struct {
	Created  []string
	Repaired []string // stale managed files rewritten
	Drifted  []string // locally edited, preserved
	// Pruned are managed projections of profiles no longer admitted
	// (deleted, manifest entries dropped).
	Pruned []string
}

// ProfileAdmitter builds the activation/language gate of the active
// charter service for root (set by the setup package; nil admits every
// profile). One gate is one service build: the activation lists and the
// inferred repo languages are read once per gate.
var ProfileAdmitter func(root string) func(p Profile) bool

// profileGate returns the gate for root (admit-all when unset).
func profileGate(root string) func(p Profile) bool {
	if ProfileAdmitter == nil {
		return func(Profile) bool { return true }
	}
	return ProfileAdmitter(root)
}

// ProjectProfiles writes native agent-profile projections for the profile
// harnesses among agentKeys (in agentKeys order) and records them in
// agent_profiles_manifest.json. Existing files are rewritten only when they
// still match their recorded hash (stale) or already match the render.
func ProjectProfiles(root string, agentKeys []string) (ProjectionResult, error) {
	var res ProjectionResult
	var harnesses []ProfileHarness
	for _, k := range agentKeys {
		if h, ok := ProfileHarnesses[k]; ok {
			harnesses = append(harnesses, h)
		}
	}
	if len(harnesses) == 0 {
		return res, nil
	}
	all, err := BuiltinProfiles()
	if err != nil {
		return res, err
	}
	var profiles []Profile
	admit := profileGate(root)
	for _, p := range all {
		if admit(p) {
			profiles = append(profiles, p)
		}
	}
	manifest := loadProfileManifest(root)
	admitted := map[string]bool{}
	for _, h := range harnesses {
		for _, p := range profiles {
			admitted[h.OutputPath(p)] = true
		}
	}
	tools := map[string]bool{}
	for _, h := range harnesses {
		tools[h.Tool] = true
	}
	var stale []string
	for rel, e := range manifest {
		if admitted[rel] || !tools[e.GetString("tool_key")] {
			continue
		}
		stale = append(stale, rel)
	}
	sort.Strings(stale)
	for _, rel := range stale {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if b, err := os.ReadFile(abs); err == nil {
			if sha256Hex(b) != manifest[rel].GetString("file_hash") {
				continue
			}
			if err := os.Remove(abs); err != nil {
				continue
			}
		}
		delete(manifest, rel)
		res.Pruned = append(res.Pruned, rel)
	}
	for _, h := range harnesses {
		for _, p := range profiles {
			rel := h.OutputPath(p)
			content := RenderProfile(h, p)
			abs := filepath.Join(root, filepath.FromSlash(rel))
			existing, rerr := os.ReadFile(abs)
			switch {
			case rerr != nil:
				res.Created = append(res.Created, rel)
			case string(existing) == content:
			default:
				prev := manifest[rel]
				if prev != nil && sha256Hex(existing) == prev.GetString("file_hash") {
					res.Repaired = append(res.Repaired, rel)
				} else {
					res.Drifted = append(res.Drifted, rel)
					continue
				}
			}
			if rerr != nil || string(existing) != content {
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return res, err
				}
				if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
					return res, err
				}
			}
			manifest[rel] = jsonx.NewObject(
				"file_hash", sha256Hex([]byte(content)),
				"format", h.Format,
				"output_path", rel,
				"profile_urn", "agent_profile:"+p.ID,
				"projection_version", 1,
				"source_hash", p.SourceHash,
				"source_layer", "built-in",
				"source_path", "${SPEC_KITTY_PACKS_ROOT}/built-in/agent_profiles/"+p.SourceFile,
				"tool_key", h.Tool,
			)
		}
	}
	return res, saveProfileManifest(root, manifest)
}
