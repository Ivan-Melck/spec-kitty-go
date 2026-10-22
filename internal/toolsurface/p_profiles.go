package toolsurface

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

const profileRepairHint = "spec-kitty doctor tool-surfaces --kind agent-profile --fix"

// profileCapability is profiles.capability_matrix.HARNESS_CAPABILITY_MATRIX.
var profileCapability = map[string]struct {
	native bool
	reason string
}{
	"claude": {true, "Native: .claude/agents/<id>.md"}, "copilot": {true, "Native: .github/agents/<id>.agent.md"},
	"codex": {true, "Native: .codex/agents/<id>.toml"}, "auggie": {true, "Native: .augment/agents/<id>.md"},
	"q": {true, "User-global: ~/.aws/amazonq/cli-agents/<id>.json"}, "windsurf": {false, "No native agent primitive; use workflow/rule surfaces"},
	"cursor": {false, "No native agent primitive; use rule surfaces"}, "kiro": {false, "No native agent primitive; use prompt surfaces"},
	"gemini": {false, "No native agent primitive; use command surfaces"}, "llxprt": {false, "No native agent primitive; use command surfaces"},
	"qwen": {false, "No native agent primitive; use command surfaces"}, "opencode": {false, "No native agent primitive; use command surfaces"},
	"kilocode": {false, "No native agent primitive; use workflow surfaces"}, "antigravity": {false, "No native agent primitive; use workflow surfaces"},
	"vibe": {false, "No native agent primitive; use skill surfaces"}, "pi": {false, "No native agent primitive; use skill surfaces"},
	"letta": {false, "No native agent primitive; use skill surfaces"},
}

// profileRenderer is one native profile renderer.
type profileRenderer struct {
	format string
	render func(p agents.Profile) string
	path   func(id, root string) string // absolute output path
}

var qAliases = map[string]bool{"q": true, "amazon-q": true, "amazon-q-agent": true}

func rendererFor(tool string) *profileRenderer {
	md := func(h agents.ProfileHarness) *profileRenderer {
		return &profileRenderer{format: h.Format, render: func(p agents.Profile) string { return agents.RenderProfile(h, p) },
			path: func(id, root string) string {
				return filepath.Join(root, filepath.FromSlash(h.Dir), id+h.Suffix)
			}}
	}
	switch {
	case tool == "claude":
		return md(agents.ProfileHarnesses["claude"])
	case tool == "copilot" || tool == "vscode":
		return md(agents.ProfileHarnesses["copilot"])
	case tool == "codex":
		return md(agents.ProfileHarnesses["codex"])
	case tool == "auggie":
		return md(agents.ProfileHarnesses["auggie"])
	case qAliases[tool]:
		return &profileRenderer{format: "amazon-q-agent", render: renderAmazonQ, path: func(id, root string) string {
			return filepath.Join(project.UserHome(), ".aws", "amazonq", "cli-agents", id+".json")
		}}
	}
	return nil
}

func renderAmazonQ(p agents.Profile) string {
	name := p.Name
	if name == "" {
		name = p.ID
	}
	desc := p.Description
	if desc == "" {
		desc = p.Purpose
	}
	return jsonx.Dumps(jsonx.NewObject("name", name, "description", desc, "instructions", p.Purpose), jsonx.Pretty)
}

var reNativeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func nativeNameViolation(id string) string {
	switch {
	case id == "":
		return "empty profile id has no native filename stem"
	case id == "." || id == "..":
		return fmt.Sprintf("profile id '%s' is a path-traversal segment", id)
	case !reNativeName.MatchString(id):
		return fmt.Sprintf("profile id '%s' contains characters illegal in a native agent filename (allowed: letters, digits, '.', '_', '-')", id)
	}
	return ""
}

// profileModel is the resolved profile repository (built-in layer, the
// active charter's activation/language gate applied).
type profileModel struct {
	profiles  []agents.Profile // non-sentinel, sorted by id
	sentinels []agents.Profile
}

func loadProfileModel(root string) (*profileModel, error) {
	ents, err := assets.List(agents.ProfilesAssetDir)
	if err != nil {
		return nil, err
	}
	m := &profileModel{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".agent.yaml") {
			continue
		}
		b, err := assets.ReadFile(agents.ProfilesAssetDir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		p, err := agents.ParseProfile(b)
		if err != nil {
			continue
		}
		p.SourceFile = e.Name()
		if agents.ProfileAdmit != nil && !agents.ProfileAdmit(root, p) {
			continue
		}
		if p.Sentinel {
			m.sentinels = append(m.sentinels, p)
		} else {
			m.profiles = append(m.profiles, p)
		}
	}
	sort.Slice(m.profiles, func(i, j int) bool { return m.profiles[i].ID < m.profiles[j].ID })
	sort.Slice(m.sentinels, func(i, j int) bool { return m.sentinels[i].ID < m.sentinels[j].ID })
	return m, nil
}

func (c *collectCtx) profileModel(root string) (*profileModel, error) {
	if m, ok := c.profiles[root]; ok {
		return m, nil
	}
	m, err := loadProfileModel(root)
	if err != nil {
		return nil, err
	}
	c.profiles[root] = m
	return m, nil
}

// nativeProfile is model.NativeAgentProfile.
type nativeProfile struct {
	URN, Layer, Tool, Output, Format, FileHash, SourcePath, SourceHash string
	Version                                                            int
	HasHash, HasSourcePath, HasSourceHash, HasVersion                  bool
}

func (n nativeProfile) equal(o nativeProfile) bool { return n == o }

type preparedProjection struct {
	native  nativeProfile
	content []byte
}

func (m *profileModel) project(tool, root string) []nativeProfile {
	r := rendererFor(tool)
	if r == nil {
		return nil
	}
	var out []nativeProfile
	for _, p := range m.profiles {
		out = append(out, nativeProfile{URN: "agent_profile:" + p.ID, Layer: "built-in", Tool: tool, Output: r.path(p.ID, root), Format: r.format,
			SourcePath: "${SPEC_KITTY_PACKS_ROOT}/built-in/agent_profiles/" + p.SourceFile, HasSourcePath: true, SourceHash: p.SourceHash, HasSourceHash: true,
			Version: 1, HasVersion: true})
	}
	return out
}

func (m *profileModel) prepare(tool, root string) []preparedProjection {
	r := rendererFor(tool)
	var out []preparedProjection
	for i, n := range m.project(tool, root) {
		body := r.render(m.profiles[i])
		n.FileHash, n.HasHash = Digest([]byte(body)), true
		out = append(out, preparedProjection{n, []byte(body)})
	}
	return out
}

func (m *profileModel) diagnose(tool string) []Finding {
	if rendererFor(tool) == nil {
		return nil
	}
	var out []Finding
	for _, p := range m.profiles {
		if v := nativeNameViolation(p.ID); v != "" {
			out = append(out, Finding{Code: "profile-name-invalid", Severity: "error", Message: fmt.Sprintf("Agent profile id '%s' is invalid: %s", p.ID, v),
				Tool: tool, HasTool: true, SurfaceID: p.ID, HasSurface: true, Details: jsonx.NewObject("reason", v)})
		}
	}
	for _, p := range m.sentinels {
		out = append(out, Finding{Code: "profile-sentinel-skipped", Severity: "info",
			Message: fmt.Sprintf("Sentinel profile '%s' is a workflow routing signal and is intentionally not projected.", p.ID),
			Tool:    tool, HasTool: true, SurfaceID: "agent_profile:" + p.ID, HasSurface: true})
	}
	return out
}

// profileManifest is profiles.manifest.ProfileManifest (keyed by absolute
// output path).
type profileManifest struct {
	path    string
	root    string
	entries map[string]nativeProfile
}

func profileManifestPath(root string) string {
	return filepath.Join(root, ".kittify", "agent_profiles_manifest.json")
}

func loadProfileManifest(root string) (*profileManifest, error) {
	m := &profileManifest{path: profileManifestPath(root), root: root, entries: map[string]nativeProfile{}}
	st, err := observeNode(m.path)
	if err != nil {
		return nil, err
	}
	if st.Kind == "absent" {
		return m, nil
	}
	if st.Kind != "file" {
		return nil, fmt.Errorf("Profile manifest is not a regular file: %s", m.path)
	}
	b, err := os.ReadFile(m.path)
	if err != nil {
		return nil, err
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, err
	}
	if v, ok := o.GetInt("schema_version"); !ok || v != 1 || o.GetList("entries") == nil && !o.Has("entries") {
		return nil, errors.New("Invalid profile manifest schema or entries")
	}
	for _, x := range o.GetList("entries") {
		eo, ok := x.(*jsonx.Object)
		if !ok {
			return nil, errors.New("Invalid profile manifest entry")
		}
		n := nativeProfile{URN: eo.GetString("profile_urn"), Layer: eo.GetString("source_layer"), Tool: eo.GetString("tool_key"), Format: eo.GetString("format")}
		out := eo.GetString("output_path")
		if filepath.IsAbs(out) {
			n.Output = out
		} else {
			n.Output = filepath.Join(root, filepath.FromSlash(out))
		}
		if v, ok := eo.Get("file_hash"); ok && v != nil {
			n.FileHash, n.HasHash = fmt.Sprint(v), true
		}
		if v, ok := eo.Get("source_path"); ok && v != nil {
			n.SourcePath, n.HasSourcePath = fmt.Sprint(v), true
		}
		if v, ok := eo.Get("source_hash"); ok && v != nil {
			n.SourceHash, n.HasSourceHash = fmt.Sprint(v), true
		}
		if v, ok := eo.GetInt("projection_version"); ok {
			n.Version, n.HasVersion = v, true
		}
		if _, dup := m.entries[n.Output]; dup {
			return nil, fmt.Errorf("Duplicate profile manifest output: %s", n.Output)
		}
		m.entries[n.Output] = n
	}
	return m, nil
}

func (m *profileManifest) all() []nativeProfile {
	keys := make([]string, 0, len(m.entries))
	for k := range m.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]nativeProfile, len(keys))
	for i, k := range keys {
		out[i] = m.entries[k]
	}
	return out
}

func (m *profileManifest) render() []byte {
	l := []any{}
	opt := func(has bool, s string) any {
		if !has {
			return nil
		}
		return s
	}
	for _, e := range m.all() {
		var ver any
		if e.HasVersion {
			ver = e.Version
		}
		out := relPosix(m.root, e.Output)
		if strings.HasPrefix(out, "../") {
			out = e.Output
		}
		l = append(l, jsonx.NewObject("profile_urn", e.URN, "source_layer", e.Layer, "tool_key", e.Tool, "output_path", out, "format", e.Format,
			"file_hash", opt(e.HasHash, e.FileHash), "source_path", opt(e.HasSourcePath, e.SourcePath), "source_hash", opt(e.HasSourceHash, e.SourceHash),
			"projection_version", ver))
	}
	return []byte(jsonx.Dumps(jsonx.NewObject("schema_version", 1, "entries", l), jsonx.SortedPretty) + "\n")
}

// fileMatchesHash is manifest.file_matches_hash (raw, then LF-normalized).
func fileMatchesHash(path, expected, raw string) bool {
	if raw == "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		raw = Digest(b)
	}
	if raw == expected {
		return true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
	return Digest([]byte(s)) == expected
}

type agentProfilesProvider struct{}

func (agentProfilesProvider) key() string              { return "agent_profiles" }
func (agentProfilesProvider) handles(kind string) bool { return kind == KindAgentProfile }
func (agentProfilesProvider) definitions() []Definition {
	return []Definition{agentProfileDefinition()}
}

func agentProfileDefinition() Definition {
	return Definition{Kind: KindAgentProfile, SourceKind: "generated", Scope: "project", PathPattern: ".claude/agents/{profile_id}.md",
		Required: "repairable_required", Activation: "user_invoked", Provider: "agent_profiles", RepairHint: profileRepairHint}
}

func (p agentProfilesProvider) expand(def Definition, tool, root string) []Instance {
	if c, ok := profileCapability[tool]; ok && !c.native {
		return []Instance{{Def: def, Path: "<not-applicable>", Owner: tool}}
	}
	m, err := loadProfileModel(root)
	var projected []nativeProfile
	if err == nil {
		projected = m.project(tool, root)
	}
	if len(projected) == 0 {
		return []Instance{{Def: def, Path: "<unsupported>", Owner: tool}}
	}
	manifest, _ := loadProfileManifest(root)
	var out []Instance
	for _, n := range projected {
		inst := Instance{Def: def, Path: n.Output, Owner: n.Tool}
		if manifest != nil {
			if e, ok := manifest.entries[n.Output]; ok && e.HasHash {
				inst.FileHash, inst.HasHash = e.FileHash, true
			}
		}
		out = append(out, inst)
	}
	return append(out, Instance{Def: def, Path: root, Owner: tool, SurfaceID: tool + ".agent_profile.<profile-diagnostics>"})
}

func (p agentProfilesProvider) probe(c *collectCtx, inst Instance) Status {
	if strings.HasSuffix(inst.SurfaceID, "<profile-diagnostics>") {
		m, err := c.profileModel(inst.Path)
		var fs []Finding
		if err == nil {
			fs = m.diagnose(inst.Owner)
		}
		return Status{inst, StateNotApplicable, fs}
	}
	switch inst.Path {
	case "<not-applicable>":
		reason := "No native agent primitive."
		if c, ok := profileCapability[inst.Owner]; ok {
			reason = c.reason
		}
		f := Finding{Code: "profile-projection-unsupported", Severity: "info",
			Message: fmt.Sprintf("%s does not support native agent profile projection; profiles are exposed through other surfaces instead. Reason: %s", inst.Owner, reason),
			Tool:    inst.Owner, HasTool: true, SurfaceID: inst.ID(), HasSurface: true, Details: jsonx.NewObject("status", "not_applicable", "reason", reason)}
		return Status{inst, StateNotApplicable, []Finding{f}}
	case "<unsupported>":
		f := Finding{Code: "research-gap-surface", Severity: "info",
			Message: fmt.Sprintf("No verified native agent-profile primitive for %s; profiles are not projected.", inst.Owner),
			Tool:    inst.Owner, HasTool: true, SurfaceID: inst.ID(), HasSurface: true, Details: jsonx.NewObject("status", "research_gap")}
		return Status{inst, StateNotApplicable, []Finding{f}}
	}
	if _, err := os.Stat(inst.Path); err != nil {
		return Status{inst, StateMissing, []Finding{instFinding("native-agent-profile-missing", "error", "Native agent profile is missing: "+inst.Path, inst, true, profileRepairHint)}}
	}
	if inst.HasHash && !fileMatchesHash(inst.Path, inst.FileHash, "") {
		return Status{inst, StateDrifted, []Finding{instFinding("native-agent-profile-drift", "warning", "Native agent profile drifted from manifest hash: "+inst.Path, inst, true, profileRepairHint)}}
	}
	return Status{inst, StatePresent, nil}
}

// PreparedProfileBatch is projection.PreparedProfileBatch.
type PreparedProfileBatch struct {
	Projections []preparedProjection
	Contents    map[string][]byte // absolute path -> bytes
}

type profileBatch struct {
	in           AssessInputs
	sels         []Selection
	manifest     *profileManifest
	original     []nativeProfile
	effects      []Effect
	dispositions []Disposition
	diagnostics  []Diagnostic
	contents     map[string][]byte
	tools        []string
	observed     map[string]FileState
	dests        map[string]nodeSig
}

func (b *profileBatch) selectedTools() []string {
	var sel []string
	for _, s := range b.sels {
		excluded := s.Def.Required != "repairable_required" || !s.Def.enabled() || (s.Def.Kind == KindAgentProfile && qAliases[s.Tool]) || rendererFor(s.Tool) == nil
		if excluded {
			b.dispositions = append(b.dispositions, NewDisposition("agent_profiles", b.in.Root.ID, "", false, "not_applicable", "Profile policy excludes "+s.Tool))
		} else {
			sel = append(sel, s.Tool)
		}
	}
	b.tools = uniqueSorted(sel)
	return b.tools
}

func (b *profileBatch) relOf(path string) (string, bool) {
	r, err := filepath.Rel(b.in.Root.Path, path)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", false
	}
	return filepath.ToSlash(r), true
}

func (b *profileBatch) preserve(path, reason string, drift bool) {
	rel, ok := b.relOf(path)
	state := "preserve"
	if drift {
		state = "consent_required"
	}
	b.dispositions = append(b.dispositions, NewDisposition("agent_profiles", b.in.Root.ID, rel, ok, state, reason))
}

func (b *profileBatch) observe(path string) (FileState, error) {
	rel, ok := b.relOf(path)
	if !ok || rel == "." {
		return FileState{}, fmt.Errorf("Unconfined profile path: %s", path)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "." || part == ".." || strings.ContainsAny(part, `:\`) {
			return FileState{}, fmt.Errorf("Unconfined profile path: %s", path)
		}
	}
	parent := b.in.Root.Path
	for _, part := range strings.Split(rel, "/") {
		st, err := observeNode(parent)
		if err != nil {
			return FileState{}, err
		}
		if st.Kind != "directory" && st.Kind != "absent" {
			return FileState{}, fmt.Errorf("Unsafe profile parent: %s", parent)
		}
		parent = filepath.Join(parent, part)
	}
	if st, ok := b.observed[path]; ok {
		return st, nil
	}
	for d := filepath.Dir(path); d != b.in.Root.Path && strings.HasPrefix(d, b.in.Root.Path+string(filepath.Separator)); d = filepath.Dir(d) {
		if _, ok := b.dests[d]; !ok {
			b.dests[d] = observeSig(d, false, false)
		}
	}
	st, err := observeNode(path)
	if err != nil {
		return FileState{}, err
	}
	b.observed[path] = st
	if _, ok := b.dests[path]; !ok {
		b.dests[path] = observeSig(path, false, false)
	}
	return st, nil
}

func (b *profileBatch) effect(path string, after FileState, owners []string, proof Proof, reason string) error {
	before, err := b.observe(path)
	if err != nil {
		return err
	}
	var action string
	switch {
	case before.Kind == "absent":
		action = "create"
	case after.Kind == "absent":
		action = "delete"
	case before.SHA256 != after.SHA256:
		action = "update"
	default:
		return nil
	}
	rel, _ := b.relOf(path)
	var ids []string
	for _, o := range owners {
		ids = append(ids, o+".agent_profile."+filepath.Base(path))
	}
	b.effects = append(b.effects, NewEffect("agent_profiles", "surface_repair", b.in.Root, rel, action, before, after, reason, []Proof{proof}, owners, ids))
	return nil
}

func validProfileEntry(e nativeProfile, root string) bool {
	if e.Format == "amazon-q-agent" || qAliases[e.Tool] || !strings.HasPrefix(e.URN, "agent_profile:") {
		return false
	}
	name := strings.SplitN(e.URN, ":", 2)[1]
	r := rendererFor(e.Tool)
	if nativeNameViolation(name) != "" || r == nil || r.format != e.Format {
		return false
	}
	return r.path(name, root) == e.Output
}

func (b *profileBatch) entryOwners(e nativeProfile) []string {
	var out []string
	for _, t := range b.tools {
		x := e
		x.Tool = t
		if validProfileEntry(x, b.in.Root.Path) {
			out = append(out, t)
		}
	}
	return out
}

func (b *profileBatch) prepareOutput(group []preparedProjection, only map[string]bool) error {
	item := group[0]
	path := item.native.Output
	state, err := b.observe(path)
	if err != nil {
		b.preserve(path, err.Error(), false)
		return nil
	}
	if only != nil && !only[path] {
		return nil
	}
	var original *nativeProfile
	for i := range b.original {
		if b.original[i].Output == path {
			original = &b.original[i]
		}
	}
	proof := Proof{"managed_path", "profile renderer:" + item.native.Tool + ":" + item.native.URN}
	if state.Kind != "file" && state.Kind != "absent" {
		b.preserve(path, "Profile destination is a custom link or unsupported node", false)
		return nil
	}
	if original != nil && !validProfileEntry(*original, b.in.Root.Path) {
		b.preserve(path, "Profile manifest identity does not authorize this path", false)
		return nil
	}
	rel, _ := b.relOf(path)
	if state.Kind == "file" {
		if original == nil && !fileMatchesHash(path, item.native.FileHash, state.SHA256) {
			b.preserve(path, "Unknown profile content; no adoption authority", false)
			return nil
		}
		if original != nil && !fileMatchesHash(path, original.FileHash, state.SHA256) && !containsStr(b.in.Overwrite, rel) {
			b.preserve(path, "Managed profile drift requires exact-path consent", true)
			return nil
		}
		kind := "canonical_content"
		if original != nil {
			kind = "manifest"
		}
		proof = Proof{kind, "agent_profiles_manifest.json:" + rel}
	}
	var owners []string
	for _, g := range group {
		owners = append(owners, g.native.Tool)
	}
	mode := 0o644
	if state.Kind == "file" {
		mode = state.Mode
	}
	after := FileOf(item.native.FileHash, mode)
	if state.Kind == "absent" || !fileMatchesHash(path, after.SHA256, state.SHA256) {
		b.contents[path] = item.content
		if err := b.effect(path, after, owners, proof, "Install prepared native profile"); err != nil {
			return err
		}
	} else {
		b.dispositions = append(b.dispositions, NewDisposition("agent_profiles", b.in.Root.ID, rel, true, "unchanged", "Native profile content matches"))
	}
	native := item.native
	if original != nil && !containsStr(owners, original.Tool) {
		native.Tool = original.Tool
	}
	b.manifest.entries[path] = native
	return nil
}

func (b *profileBatch) prepareOrphan(e nativeProfile) error {
	path := e.Output
	if !validProfileEntry(e, b.in.Root.Path) {
		b.preserve(path, "Unconfined or unsupported orphan manifest identity", false)
		return nil
	}
	state, err := b.observe(path)
	if err != nil {
		b.preserve(path, err.Error(), false)
		return nil
	}
	switch {
	case state.Kind == "absent":
		delete(b.manifest.entries, path)
	case state.Kind == "file" && fileMatchesHash(path, e.FileHash, state.SHA256):
		if err := b.effect(path, Absent(), b.entryOwners(e), Proof{"manifest", "agent_profiles_manifest.json:" + e.URN}, "Prune unchanged deactivated profile"); err != nil {
			return err
		}
		delete(b.manifest.entries, path)
	default:
		b.preserve(path, "Orphan drift or custom link; retain ownership record", true)
	}
	return nil
}

func (b *profileBatch) prepareManifest() error {
	all := b.manifest.all()
	same := len(all) == len(b.original)
	if same {
		for i := range all {
			same = same && all[i].equal(b.original[i])
		}
	}
	if same {
		return nil
	}
	state, err := b.observe(b.manifest.path)
	if err != nil {
		return err
	}
	content := b.manifest.render()
	b.contents[b.manifest.path] = content
	set := map[string]bool{}
	for _, e := range append(append([]nativeProfile(nil), b.original...), all...) {
		if e.Format != "amazon-q-agent" {
			set[e.Tool] = true
		}
		for _, t := range b.entryOwners(e) {
			set[t] = true
		}
	}
	var owners []string
	for t := range set {
		owners = append(owners, t)
	}
	sort.Strings(owners)
	if len(owners) == 0 {
		owners = b.tools
	}
	mode := 0o644
	if state.Kind == "file" {
		mode = state.Mode
	}
	return b.effect(b.manifest.path, FileOf(Digest(content), mode), owners, Proof{"managed_path", ".kittify/agent_profiles_manifest.json schema 1"}, "Persist exact prepared profile ownership")
}

func (b *profileBatch) prepareParents() error {
	children := append([]Effect(nil), b.effects...)
	for _, child := range children {
		if child.Action == "delete" {
			continue
		}
		parent := filepath.Dir(child.Destination())
		for parent != b.in.Root.Path && strings.HasPrefix(parent, b.in.Root.Path+string(filepath.Separator)) {
			st, err := b.observe(parent)
			if err != nil {
				return err
			}
			if st.Kind == "absent" {
				if err := b.effect(parent, DirOf(0o755), child.LogicalOwners, child.Ownership[0], "Create profile supporting directory"); err != nil {
					return err
				}
			}
			parent = filepath.Dir(parent)
		}
	}
	return nil
}

func (b *profileBatch) finish(projections []preparedProjection) *Assessment {
	complete := true
	for _, d := range b.diagnostics {
		complete = complete && d.Severity != "error"
	}
	a := &Assessment{OwnerKey: "agent_profiles", Root: b.in.Root, Dispositions: b.dispositions, Diagnostics: b.diagnostics, Complete: complete,
		Prepared: &PreparedProfileBatch{Projections: projections, Contents: b.contents}}
	if complete {
		co, err := Coalesce(b.effects)
		if err != nil {
			return Incomplete("agent_profiles", b.in.Root, "profile_input_invalid", err.Error())
		}
		a.Effects = co
	}
	return a
}

func (p agentProfilesProvider) assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	return assessProfiles(c, in, sels, nil)
}

func assessProfiles(c *collectCtx, in AssessInputs, sels []Selection, only map[string]bool) *Assessment {
	root := in.Root.Path
	fail := func(err error) *Assessment {
		return Incomplete("agent_profiles", in.Root, "profile_input_invalid", err.Error())
	}
	if _, err := kittify.LoadAgentConfig(root); err != nil {
		return fail(err)
	}
	manifest, err := loadProfileManifest(root)
	if err != nil {
		return fail(err)
	}
	b := &profileBatch{in: in, sels: dedupeSelections(sels), manifest: &profileManifest{path: manifest.path, root: root, entries: map[string]nativeProfile{}},
		original: manifest.all(), contents: map[string][]byte{}, observed: map[string]FileState{}, dests: map[string]nodeSig{}}
	for k, v := range manifest.entries {
		b.manifest.entries[k] = v
	}
	eligible := b.selectedTools()
	if len(eligible) == 0 {
		return b.finish(nil)
	}
	m, err := c.profileModel(root)
	if err != nil {
		return fail(err)
	}
	for _, t := range eligible {
		for _, f := range m.diagnose(t) {
			b.diagnostics = append(b.diagnostics, Diag(f.Code, "agent_profiles", f.Severity, f.Message))
		}
	}
	for _, d := range b.diagnostics {
		if d.Severity == "error" {
			return b.finish(nil)
		}
	}
	inputsUnchanged := treeSnapshot(profileInputRoots(root))
	var projected []preparedProjection
	for _, t := range eligible {
		projected = append(projected, m.prepare(t, root)...)
	}
	var order []string
	grouped := map[string][]preparedProjection{}
	for _, pp := range projected {
		if _, ok := grouped[pp.native.Output]; !ok {
			order = append(order, pp.native.Output)
		}
		grouped[pp.native.Output] = append(grouped[pp.native.Output], pp)
	}
	for _, path := range order {
		g := grouped[path]
		for _, x := range g[1:] {
			if string(x.content) != string(g[0].content) {
				return fail(fmt.Errorf("Conflicting profile projections: %s", path))
			}
		}
		if err := b.prepareOutput(g, only); err != nil {
			return fail(err)
		}
	}
	for _, e := range b.original {
		if _, ok := grouped[e.Output]; !ok && containsStr(b.tools, e.Tool) {
			if err := b.prepareOrphan(e); err != nil {
				return fail(err)
			}
		}
	}
	if err := b.prepareManifest(); err != nil {
		return fail(err)
	}
	if err := b.prepareParents(); err != nil {
		return fail(err)
	}
	if _, err := b.observe(b.manifest.path); err != nil {
		return fail(err)
	}
	a := b.finish(projected)
	a.Recheck = func() []Diagnostic {
		planned := map[string]FileState{}
		for _, e := range a.Effects {
			if e.Action == "create" && e.After.Kind == "directory" {
				planned[e.Destination()] = e.After
			}
		}
		paths := make([]string, 0, len(b.dests))
		for p := range b.dests {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			old := b.dests[p]
			now := observeSig(p, false, false)
			if pl, ok := planned[p]; ok && old.kind == "absent" && now.kind == "directory" && now.mode == pl.Mode {
				continue
			}
			if now != old {
				return []Diagnostic{Diag("precondition_changed", "agent_profiles", "error", "Profile destination changed: "+p)}
			}
		}
		if !inputsUnchanged() {
			return []Diagnostic{Diag("precondition_changed", "agent_profiles", "error", "Profile source/config input root changed")}
		}
		return nil
	}
	return a
}

// profileInputRoots is agent_profiles._profile_input_roots (project side;
// the package-side roots never change during a run).
func profileInputRoots(root string) []string {
	roots := []string{
		filepath.Join(root, ".kittify", "config.yaml"), filepath.Join(root, ".kittify", "charter"), filepath.Join(root, ".kittify", "agent_profiles"),
		filepath.Join(root, "pyproject.toml"), filepath.Join(root, ".kittify", "charter-packs"), profileManifestPath(root),
	}
	if cfg, err := kittify.LoadConfig(root); err == nil {
		if v, ok := cfg.Get("charter"); ok {
			if s, isStr := v.(string); isStr && s != "" {
				if !filepath.IsAbs(s) {
					s = filepath.Join(root, s)
				}
				roots = append(roots, s)
			}
		}
	}
	sort.Strings(roots)
	return uniqueSorted(roots)
}

func dedupeSelections(sels []Selection) []Selection {
	var out []Selection
	for _, s := range sels {
		dup := false
		for _, x := range out {
			dup = dup || x == s
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
}

func applyProfiles(a *Assessment) ApplyResult {
	prep, _ := a.Prepared.(*PreparedProfileBatch)
	manifestPath := profileManifestPath(a.Root.Path)
	effects := append([]Effect(nil), a.Effects...)
	sort.SliceStable(effects, func(i, j int) bool {
		ei, ej := effects[i], effects[j]
		mi, mj := ei.Destination() == manifestPath, ej.Destination() == manifestPath
		if mi != mj {
			return !mi
		}
		di, dj := ei.After.Kind != "directory", ej.After.Kind != "directory"
		if di != dj {
			return !di
		}
		li, lj := strings.Count(ei.Destination(), string(filepath.Separator)), strings.Count(ej.Destination(), string(filepath.Separator))
		if li != lj {
			return li < lj
		}
		return ei.Path < ej.Path
	})
	var succeeded, failed []string
	var diags []Diagnostic
	for _, e := range effects {
		dest := e.Destination()
		if dest == manifestPath && len(failed) > 0 {
			failed = append(failed, e.ID())
			diags = append(diags, Diag("profile_manifest_not_applied", "agent_profiles", "error", "Partial file failure; original ownership records retained"))
			continue
		}
		var err error
		switch {
		case e.After.Kind == "directory":
			if st, _ := observeNode(dest); st.Kind == "directory" {
				err = nil
			} else if err = os.Mkdir(dest, 0o755); err == nil {
				err = os.Chmod(dest, 0o755)
			}
		case e.Action == "delete":
			err = os.Remove(dest)
		default:
			content := prep.Contents[dest]
			mode := os.FileMode(0o644)
			if e.After.Mode >= 0 {
				mode = os.FileMode(e.After.Mode)
			}
			err = os.WriteFile(dest, content, mode)
		}
		if err != nil {
			failed = append(failed, e.ID())
			diags = append(diags, Diag("profile_apply_failed", "agent_profiles", "error", e.Path+": "+err.Error()))
		} else {
			succeeded = append(succeeded, e.ID())
		}
	}
	out := "applied"
	if len(failed) > 0 {
		out = "failed"
		if len(succeeded) > 0 {
			out = "partial"
		}
	}
	return ApplyResult{Owner: "agent_profiles", Succeeded: succeeded, Failed: failed, Diagnostics: diags, Outcome: out}
}

func (p agentProfilesProvider) repair(c *collectCtx, root string, statuses []Status) RepairResult {
	actionable := statusesOf(statuses, StateMissing, StateDrifted)
	skipped := idsOf(statusesOf(statuses, StateNotApplicable, StateUnsupported))
	var repaired, failed []string
	var qs, rest []Status
	for _, s := range actionable {
		if qAliases[s.Inst.Owner] {
			qs = append(qs, s)
		} else {
			rest = append(rest, s)
		}
	}
	if len(qs) > 0 {
		m, err := c.profileModel(root)
		for _, s := range qs {
			id := s.Inst.ID()
			if err != nil {
				failed = append(failed, id+": "+err.Error())
				continue
			}
			var body string
			found := false
			for _, pp := range m.prepare(s.Inst.Owner, root) {
				if pp.native.Output == s.Inst.Path {
					body, found = string(pp.content), true
				}
			}
			if !found {
				failed = append(failed, fmt.Sprintf("%s: no projection for %s", id, s.Inst.Path))
				continue
			}
			if err := os.MkdirAll(filepath.Dir(s.Inst.Path), 0o755); err != nil {
				failed = append(failed, id+": "+err.Error())
				continue
			}
			if err := os.WriteFile(s.Inst.Path, []byte(body), 0o644); err != nil {
				failed = append(failed, id+": "+err.Error())
				continue
			}
			repaired = append(repaired, id)
		}
	}
	var sels []Selection
	for _, s := range statuses {
		sels = append(sels, Selection{s.Inst.Owner, s.Inst.Def})
	}
	var overwrite []string
	only := map[string]bool{}
	for _, s := range rest {
		only[s.Inst.Path] = true
		if s.State == StateDrifted {
			overwrite = append(overwrite, relPosix(root, s.Inst.Path))
		}
	}
	sort.Strings(overwrite)
	a := assessProfiles(c, AssessInputs{Root: Root{"project", "project", root}, Overwrite: overwrite, Automatic: true}, sels, only)
	var res ApplyResult
	if a.Complete && len(a.Effects) > 0 {
		res = applyProfiles(a)
	}
	ok := map[string]bool{}
	for _, id := range res.Succeeded {
		ok[id] = true
	}
	succeededPaths := map[string]bool{}
	for _, e := range a.Effects {
		if ok[e.ID()] {
			succeededPaths[e.Destination()] = true
		}
	}
	for _, s := range actionable {
		if succeededPaths[s.Inst.Path] {
			repaired = append(repaired, s.Inst.ID())
		}
	}
	for _, d := range append(append([]Diagnostic(nil), a.Diagnostics...), res.Diagnostics...) {
		if d.Severity == "error" {
			failed = append(failed, d.Message)
		}
	}
	for _, d := range a.Dispositions {
		if d.State == "consent_required" {
			failed = append(failed, "profile drift: "+d.Path)
		}
	}
	return RepairResult{Repaired: repaired, Failed: failed, Skipped: skipped}
}
