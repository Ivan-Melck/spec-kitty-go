package toolsurface

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

const (
	bundleRepairHint = "spec-kitty doctor tool-surfaces --kind plugin-manifest --fix"
	bundleOutputDir  = "dist/spec-kitty-plugins"
	bundleLedger     = ".spec-kitty-bundle.json"
)

// bundleProjector is one staging-only plugin bundle projector.
type bundleProjector struct {
	target   string
	manifest string // manifest path relative to the target dir
	layout   map[string]string
	agentFn  func(stem string) string
}

func bundleProjectors() []bundleProjector {
	claude := map[string]string{KindCommandSkill: "skills", KindCharterSkill: "skills", KindAgentProfile: "agents", KindHook: "hooks", KindNativeConfig: ""}
	copilot := map[string]string{KindCommandSkill: "skills", KindCharterSkill: "skills", KindAgentProfile: "agents", KindHook: "", KindNativeConfig: ""}
	return []bundleProjector{
		{"claude_code_plugin", ".claude-plugin/plugin.json", claude, func(s string) string { return s + ".md" }},
		{"copilot_skill_package", "plugin.json", copilot, func(s string) string { return s + ".agent.md" }},
		{"vscode_extension", "plugin.json", copilot, func(s string) string { return s + ".agent.md" }},
	}
}

func projectorFor(target string) *bundleProjector {
	for _, p := range bundleProjectors() {
		if p.target == target {
			p := p
			return &p
		}
	}
	return nil
}

var bundleKinds = map[string]bool{KindCommandSkill: true, KindCharterSkill: true, KindAgentProfile: true, KindHook: true, KindNativeConfig: true}

type pluginBundleProvider struct{}

func (pluginBundleProvider) key() string              { return "plugin_bundle" }
func (pluginBundleProvider) handles(kind string) bool { return kind == KindPluginManifest }
func (pluginBundleProvider) definitions() []Definition {
	return []Definition{{Kind: KindPluginManifest, SourceKind: "generated", Scope: "plugin_bundle", PathPattern: "dist/spec-kitty-plugins/{target}/plugin.json",
		Required: "optional", Activation: "disabled", Provider: "plugin_bundle", RepairHint: bundleRepairHint}}
}

func (pluginBundleProvider) expand(def Definition, tool, root string) []Instance {
	if tool != PluginBundleToolKey {
		return nil
	}
	var out []Instance
	for _, p := range bundleProjectors() {
		out = append(out, Instance{Def: def, Path: filepath.Join(root, filepath.FromSlash(bundleOutputDir), p.target, filepath.FromSlash(p.manifest)), Owner: p.target})
	}
	return out
}

func (p pluginBundleProvider) probe(c *collectCtx, inst Instance) Status {
	if projectorFor(inst.Owner) == nil {
		return Status{inst, StateStale, []Finding{instFinding("plugin-manifest-stale-path", "warning", "Plugin manifest references an unknown distribution target: "+inst.Owner, inst, true, "")}}
	}
	if _, err := os.Stat(inst.Path); err != nil {
		return Status{inst, StateMissing, nil}
	}
	out := filepath.Dir(inst.Path)
	if filepath.Base(out) == ".claude-plugin" {
		out = filepath.Dir(out)
	}
	root := out
	for range strings.Split(bundleOutputDir, "/") {
		root = filepath.Dir(root)
	}
	root = filepath.Dir(root)
	a := assessBundle(c, AssessInputs{Root: Root{"project", "project", root}}, []string{inst.Owner})
	if a.Complete && len(a.Effects) == 0 {
		all := true
		for _, d := range a.Dispositions {
			all = all && d.State == "unchanged"
		}
		if all {
			return Status{inst, StatePresent, nil}
		}
	}
	return Status{inst, StateMissing, []Finding{instFinding("bundle-component-missing", "error", "Selected staged bundle has missing, drifted, or unavailable members.", inst, true, bundleRepairHint)}}
}

func (p pluginBundleProvider) assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	return assessBundle(c, in, nil)
}

// bundleEntry is bundles.model.BundleEntry.
type bundleEntry struct {
	kind     string
	rel      string // bundle-relative path
	owners   []string
	ids      []string
	sources  []string // absolute source paths
	claims   [][3]string
	retained []bundleMember
}

type bundleMember struct {
	path    string
	content []byte
	mode    int
}

// stagedFile is bundles.model.StagedFile.
type stagedFile struct {
	path     string // root-relative
	content  []byte
	mode     int
	owners   []string
	ids      []string
	manifest bool
	managed  bool
}

// PreparedBundle is bundles.model.PreparedBundle.
type PreparedBundle struct {
	Files map[string]stagedFile
}

func bundleRelativePath(kind, source string, p bundleProjector) (string, bool) {
	prefix, ok := p.layout[kind]
	if !ok {
		return "", false
	}
	join := func(leaf string) string {
		if prefix == "" {
			return leaf
		}
		return prefix + "/" + leaf
	}
	switch kind {
	case KindAgentProfile:
		base := filepath.Base(source)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		return join(p.agentFn(stem)), true
	case KindCommandSkill, KindCharterSkill:
		parts := strings.Split(filepath.ToSlash(source), "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] == "skills" {
				return prefix + "/" + strings.Join(parts[i+1:], "/"), true
			}
		}
		return prefix + "/" + filepath.Base(filepath.Dir(source)) + "/" + filepath.Base(source), true
	}
	return join(filepath.Base(source)), true
}

func bundleEntriesForPlans(plans []Plan, root string, p bundleProjector) []bundleEntry {
	seen := map[string]*bundleEntry{}
	for _, pl := range plans {
		for _, inst := range pl.Instances {
			kind := inst.Def.Kind
			if !bundleKinds[kind] || !inst.Def.enabled() || inst.Path == "<unsupported>" || inst.Path == "<not-applicable>" {
				continue
			}
			if !filepath.IsAbs(inst.Path) || inst.Path == root || !strings.HasPrefix(inst.Path, root+string(filepath.Separator)) {
				continue
			}
			rel, ok := bundleRelativePath(kind, inst.Path, p)
			if !ok {
				continue
			}
			id := inst.ID()
			if prior, ok := seen[rel]; ok {
				prior.owners = uniqueSorted(append(prior.owners, inst.Owner))
				prior.ids = uniqueSorted(append(prior.ids, id))
				prior.sources = uniqueSorted(append(prior.sources, inst.Path))
				claim := [3]string{inst.Path, inst.Owner, id}
				dup := false
				for _, c := range prior.claims {
					dup = dup || c == claim
				}
				if !dup {
					prior.claims = append(prior.claims, claim)
					sort.Slice(prior.claims, func(i, j int) bool {
						a, b := prior.claims[i], prior.claims[j]
						if a[0] != b[0] {
							return a[0] < b[0]
						}
						if a[1] != b[1] {
							return a[1] < b[1]
						}
						return a[2] < b[2]
					})
				}
				continue
			}
			seen[rel] = &bundleEntry{kind: kind, rel: rel, owners: []string{inst.Owner}, ids: []string{id}, sources: []string{inst.Path},
				claims: [][3]string{{inst.Path, inst.Owner, id}}}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]bundleEntry, len(keys))
	for i, k := range keys {
		out[i] = *seen[k]
	}
	return out
}

func supplierMembers(a *Assessment) ([]bundleMember, error) {
	if !a.Complete {
		return nil, fmt.Errorf("Incomplete bundle supplier: %s", a.OwnerKey)
	}
	root := a.Root.Path
	var out []bundleMember
	switch p := a.Prepared.(type) {
	case *PreparedCommands:
		for _, c := range p.Commands {
			if c.Content != nil {
				out = append(out, bundleMember{filepath.Join(root, filepath.FromSlash(c.Path)), c.Content, 0o644})
			}
		}
	case *PreparedProjectSkills:
		for _, w := range p.Writes {
			if w.Content != nil && w.Order == 2 {
				mode := 0o444
				if w.Effect.After.Mode > 0 {
					mode = w.Effect.After.Mode
				}
				out = append(out, bundleMember{w.Effect.Destination(), w.Content, mode})
			}
		}
	case *PreparedProfileBatch:
		for _, pp := range p.Projections {
			out = append(out, bundleMember{pp.native.Output, pp.content, 0o644})
		}
	case *PreparedSessionBatch:
		for _, f := range p.Files {
			if f.Member.Disposition != "preserve" {
				mode := 0o644
				if f.Member.Before.Mode > 0 {
					mode = f.Member.Before.Mode
				}
				out = append(out, bundleMember{filepath.Join(root, filepath.FromSlash(f.Member.Path)), f.Member.Content, mode})
			}
		}
	case *agents.PreparedVibeConfig:
		mode := 0o644
		if p.Mode > 0 {
			mode = p.Mode
		}
		out = append(out, bundleMember{filepath.Join(root, filepath.FromSlash(p.Rel)), p.Desired, mode})
	default:
		return nil, fmt.Errorf("Unsupported concrete bundle supplier: %s", a.OwnerKey)
	}
	return out, nil
}

func suppliedEntries(entries []bundleEntry, suppliers []*Assessment) ([]bundleEntry, error) {
	var supplied []bundleMember
	retired := map[string]bool{}
	for _, s := range suppliers {
		ms, err := supplierMembers(s)
		if err != nil {
			return nil, err
		}
		supplied = append(supplied, ms...)
		for _, e := range s.Effects {
			if e.After.Kind == "absent" {
				retired[e.Destination()] = true
			}
		}
	}
	var out []bundleEntry
	for _, e := range entries {
		var paths []string
		for _, s := range e.sources {
			if !retired[s] {
				paths = append(paths, s)
			}
		}
		if len(paths) == 0 {
			continue
		}
		inPaths := func(p string) bool { return containsStr(paths, p) }
		var claims [][3]string
		for _, c := range e.claims {
			if inPaths(c[0]) {
				claims = append(claims, c)
			}
		}
		e.sources, e.claims = paths, claims
		if len(claims) > 0 {
			var o, ids []string
			for _, c := range claims {
				o, ids = append(o, c[1]), append(ids, c[2])
			}
			e.owners, e.ids = uniqueSorted(o), uniqueSorted(ids)
		} else {
			e.owners, e.ids = nil, nil
		}
		var cands []bundleMember
		for _, m := range supplied {
			if inPaths(m.path) {
				cands = append(cands, m)
			}
		}
		for _, s := range suppliers {
			for _, d := range s.Dispositions {
				if !d.HasPath || (d.State != "preserve" && d.State != "consent_required") {
					continue
				}
				p := filepath.Join(s.Root.Path, filepath.FromSlash(d.Path))
				if !inPaths(p) {
					continue
				}
				covered := false
				for _, c := range cands {
					covered = covered || c.path == p
				}
				if !covered {
					return nil, fmt.Errorf("Upstream owner retained no canonical bytes for %s", e.sources[0])
				}
			}
		}
		if len(cands) > 0 {
			for _, c := range cands[1:] {
				if string(c.content) != string(cands[0].content) || c.mode != cands[0].mode {
					return nil, fmt.Errorf("Conflicting prepared bundle member: %s", e.rel)
				}
			}
			e.retained = cands
		}
		out = append(out, e)
	}
	return out, nil
}

func validateMemberBytes(e bundleEntry, content []byte) error {
	src := e.sources[0]
	if strings.HasSuffix(src, ".json") {
		v, err := jsonx.Decode(content)
		if err != nil {
			return err
		}
		if _, ok := v.(*jsonx.Object); !ok {
			return fmt.Errorf("Required JSON bundle member is not an object: %s", src)
		}
	} else if filepath.Base(src) == "SKILL.md" {
		text := string(content)
		if strings.HasPrefix(text, "---") {
			pieces := strings.SplitN(text, "---", 3)
			if len(pieces) != 3 {
				return fmt.Errorf("Malformed required skill frontmatter: %s", src)
			}
			v, err := pyyaml.Load(pieces[1])
			if err != nil {
				return fmt.Errorf("Malformed required skill frontmatter: %s", src)
			}
			if _, ok := v.(*jsonx.Object); !ok {
				return fmt.Errorf("Malformed required skill frontmatter: %s", src)
			}
		}
	}
	return nil
}

func filesForEntries(entries []bundleEntry, outputDir, root string) ([]stagedFile, error) {
	var out []stagedFile
	for _, e := range entries {
		var variants []bundleMember
		for _, s := range e.sources {
			found := false
			for _, r := range e.retained {
				if r.path == s {
					variants = append(variants, r)
					found = true
				}
			}
			if found {
				continue
			}
			st, err := observeNode(s)
			if err != nil {
				return nil, err
			}
			if st.Kind != "file" {
				return nil, fmt.Errorf("Required bundle source unavailable: %s", s)
			}
			b, err := os.ReadFile(s)
			if err != nil {
				return nil, err
			}
			variants = append(variants, bundleMember{s, b, st.Mode})
		}
		if len(variants) == 0 {
			return nil, fmt.Errorf("Conflicting bundle members: %s", e.rel)
		}
		for _, v := range variants[1:] {
			if string(v.content) != string(variants[0].content) || v.mode != variants[0].mode {
				return nil, fmt.Errorf("Conflicting bundle members: %s", e.rel)
			}
		}
		if err := validateMemberBytes(e, variants[0].content); err != nil {
			return nil, err
		}
		owners := e.owners
		if len(owners) == 0 {
			owners = []string{"plugin_bundle"}
		}
		p := relPosix(root, filepath.Join(outputDir, filepath.FromSlash(e.rel)))
		out = append(out, stagedFile{path: p, content: variants[0].content, mode: variants[0].mode, owners: owners, ids: e.ids, managed: true})
	}
	return out, nil
}

func jsonBytesSorted(o *jsonx.Object) []byte {
	return []byte(jsonx.Dumps(o, jsonx.SortedPretty) + "\n")
}

func pluginManifestPayload(target string) *jsonx.Object {
	return jsonx.NewObject("name", "spec-kitty", "version", "0.0.0", "description", "Spec Kitty tool surfaces (staging bundle).", "distribution_target", target)
}

// bundleSuppliers is PluginBundleProvider._source_assessments.
func bundleSuppliers(c *collectCtx, in AssessInputs, plans []Plan) ([]*Assessment, error) {
	ps := providers()
	type sel struct {
		tool string
		def  Definition
	}
	var selected []sel
	for _, pl := range plans {
		for _, inst := range pl.Instances {
			if !filepath.IsAbs(inst.Path) || inst.Path == in.Root.Path || !strings.HasPrefix(inst.Path, in.Root.Path+string(filepath.Separator)) {
				continue
			}
			if !bundleKinds[inst.Def.Kind] || !inst.Def.enabled() {
				continue
			}
			s := sel{pl.Tool, inst.Def}
			dup := false
			for _, x := range selected {
				dup = dup || x == s
			}
			if !dup {
				selected = append(selected, s)
			}
		}
	}
	var out []*Assessment
	for _, p := range ps {
		var sels []Selection
		for _, s := range selected {
			if s.def.Provider == p.key() && p.handles(s.def.Kind) {
				sels = append(sels, Selection{s.tool, s.def})
			}
		}
		if len(sels) == 0 {
			continue
		}
		oin := AssessInputs{Root: in.Root, Automatic: in.Automatic, Overwrite: in.Overwrite}
		if p.key() == "managed_skills" {
			var tools []string
			for _, s := range sels {
				tools = append(tools, s.Tool)
			}
			catalog, err := ProjectSkillCatalog(in.Root.Path, false)
			if err != nil {
				return nil, err
			}
			oin.Projected = AssessSkillInstallation(in.Root, catalog, tools, false, false, nil, SkillAssessOptions{Now: nowISO()})
		}
		a := p.assess(c, oin, nil, sels)
		if a == nil {
			continue
		}
		if !a.Complete {
			var msgs []string
			for _, d := range a.Diagnostics {
				msgs = append(msgs, d.Message)
			}
			return nil, fmt.Errorf("%s: %s", p.key(), strings.Join(msgs, "; "))
		}
		if a.Prepared != nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// assessBundle is PluginBundleProvider.assess for explicitly selected
// staging targets.
func assessBundle(c *collectCtx, in AssessInputs, targets []string) *Assessment {
	if len(targets) == 0 {
		return &Assessment{OwnerKey: "plugin_bundle", Root: in.Root, Complete: true,
			Dispositions: []Disposition{NewDisposition("plugin_bundle", in.Root.ID, bundleOutputDir, true, "not_applicable", "Optional disabled staging was not selected")}}
	}
	a, err := prepareBundle(c, in, targets)
	if err != nil {
		return Incomplete("plugin_bundle", in.Root, "bundle_input_invalid", err.Error())
	}
	return a
}

func prepareBundle(c *collectCtx, in AssessInputs, targets []string) (*Assessment, error) {
	root := in.Root.Path
	cfg, err := observeConfined(root, ".kittify/config.yaml")
	if err != nil {
		return nil, err
	}
	if cfg.Kind != "file" && cfg.Kind != "absent" {
		return nil, errors.New("Bundle source configuration is not a regular file")
	}
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return nil, err
	}
	ps := providers()
	reg := &registry{keys: ac.Available}
	plans := buildPlansWith(c, ps, reg, ac.Available, root, nil)
	for _, pl := range plans {
		var msgs []string
		for _, d := range pl.Diagnostics {
			if d.Severity == "error" {
				msgs = append(msgs, d.Message)
			}
		}
		if len(msgs) > 0 {
			return nil, errors.New(strings.Join(msgs, "; "))
		}
	}
	suppliers, err := bundleSuppliers(c, in, plans)
	if err != nil {
		return nil, err
	}
	var files []stagedFile
	var dirs []string
	for _, t := range uniqueSorted(targets) {
		p := projectorFor(t)
		if p == nil {
			return nil, fmt.Errorf("Unknown bundle target: %s", t)
		}
		dir := filepath.Join(root, filepath.FromSlash(bundleOutputDir), t)
		entries, err := suppliedEntries(bundleEntriesForPlans(plans, root, *p), suppliers)
		if err != nil {
			return nil, err
		}
		fs, err := filesForEntries(entries, dir, root)
		if err != nil {
			return nil, err
		}
		files = append(files, fs...)
		files = append(files, stagedFile{path: relPosix(root, filepath.Join(dir, filepath.FromSlash(p.manifest))), content: jsonBytesSorted(pluginManifestPayload(t)),
			mode: 0o644, owners: []string{"plugin_bundle"}, manifest: true, managed: true})
		dirs = append(dirs, dir)
	}
	return prepareStaging(in, files, dirs)
}

func readLedger(raw []byte) (map[string][2]any, error) {
	v, err := jsonx.Decode(raw)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonx.Object)
	sv, _ := o.GetInt("schema_version")
	if !ok || o.GetString("owner") != "plugin_bundle" || sv != 1 {
		return nil, errors.New("Invalid required bundle ownership manifest")
	}
	members := o.GetObject("members")
	if members == nil {
		return nil, errors.New("Invalid required bundle ownership members")
	}
	out := map[string][2]any{}
	for _, kv := range members.Pairs() {
		eo, ok := kv.Value.(*jsonx.Object)
		if !ok {
			return nil, errors.New("Invalid bundle ownership entry")
		}
		mode, _ := eo.GetInt("mode")
		out[kv.Key] = [2]any{eo.GetString("sha256"), mode}
	}
	return out, nil
}

func stageEffect(root Root, m stagedFile, before FileState, proof Proof, effects *[]Effect, disps *[]Disposition) {
	after := FileOf(Digest(m.content), m.mode)
	var action string
	switch {
	case before.Kind == "absent":
		action = "create"
	case before.Kind != "file":
		action = "replace"
	case before.SHA256 != after.SHA256:
		action = "update"
	case before.Mode != after.Mode:
		action = "chmod"
	default:
		*disps = append(*disps, NewDisposition("plugin_bundle", root.ID, m.path, true, "unchanged", "Staged bytes and mode are current"))
		return
	}
	*effects = append(*effects, NewEffect("plugin_bundle", "surface_repair", root, m.path, action, before, after, "Prepare staged bundle output", []Proof{proof}, m.owners, m.ids))
}

func prepareStaging(in AssessInputs, files []stagedFile, dirs []string) (*Assessment, error) {
	root := in.Root
	known := map[string][2]any{}
	type ledger struct {
		path string
		old  map[string][2]any
	}
	var ledgers []ledger
	for _, d := range dirs {
		lp := relPosix(root.Path, filepath.Join(d, bundleLedger))
		st, err := observeConfined(root.Path, lp)
		if err != nil {
			return nil, err
		}
		if st.Kind != "file" && st.Kind != "absent" {
			return nil, fmt.Errorf("Unsafe bundle ownership manifest: %s", filepath.Join(d, bundleLedger))
		}
		entries := map[string][2]any{}
		if st.Kind == "file" {
			b, err := os.ReadFile(filepath.Join(root.Path, filepath.FromSlash(lp)))
			if err != nil {
				return nil, err
			}
			if entries, err = readLedger(b); err != nil {
				return nil, err
			}
		}
		prefix := relPosix(root.Path, d)
		for k, v := range entries {
			known[prefix+"/"+k] = v
		}
		ledgers = append(ledgers, ledger{lp, entries})
	}
	var effects []Effect
	var disps []Disposition
	unique := map[string]stagedFile{}
	var order []string
	for _, m := range files {
		if prev, ok := unique[m.path]; ok {
			if string(prev.content) != string(m.content) || prev.mode != m.mode {
				return nil, fmt.Errorf("Conflicting staged output: %s", m.path)
			}
			prev.owners = uniqueSorted(append(prev.owners, m.owners...))
			prev.ids = uniqueSorted(append(prev.ids, m.ids...))
			unique[m.path] = prev
			continue
		}
		unique[m.path] = m
		order = append(order, m.path)
	}
	preserved := map[string]bool{}
	for _, p := range order {
		m := unique[p]
		before, err := observeConfined(root.Path, m.path)
		if err != nil {
			return nil, err
		}
		digest := Digest(m.content)
		_, prior := known[m.path]
		if before.Kind == "symlink" {
			disps = append(disps, NewDisposition("plugin_bundle", root.ID, m.path, true, "preserve", "Unknown staged link is not owned by a file manifest"))
			preserved[m.path] = true
			continue
		}
		if before.Kind != "absent" && before.SHA256 != digest {
			state := "preserve"
			if prior {
				state = "consent_required"
			}
			if !prior || !containsStr(in.Overwrite, m.path) {
				disps = append(disps, NewDisposition("plugin_bundle", root.ID, m.path, true, state, "Staged content requires exact prior ownership and consent"))
				preserved[m.path] = true
				continue
			}
		}
		if before.Kind != "file" && before.Kind != "absent" && before.Kind != "symlink" {
			return nil, fmt.Errorf("Unsupported bundle destination: %s", m.path)
		}
		proof := Proof{"managed_path", "bundle member:" + m.path}
		switch {
		case prior:
			proof = Proof{"manifest", bundleLedger + ":" + m.path}
		case before.Kind == "file":
			proof = Proof{"canonical_content", "bundle member:" + m.path}
		}
		stageEffect(root, m, before, proof, &effects, &disps)
	}
	blocked := map[string]bool{}
	if len(preserved) > 0 {
		for _, p := range order {
			if unique[p].manifest {
				blocked[p] = true
			}
		}
		var kept []Effect
		for _, e := range effects {
			if !blocked[e.Path] {
				kept = append(kept, e)
			}
		}
		effects = kept
	}
	for _, l := range ledgers {
		dir := path.Dir(l.path)
		members := map[string][2]any{}
		for k, v := range l.old {
			members[k] = v
		}
		for _, p := range order {
			m := unique[p]
			if m.managed && strings.HasPrefix(m.path, dir+"/") && !preserved[m.path] && !blocked[m.path] {
				members[strings.TrimPrefix(m.path, dir+"/")] = [2]any{Digest(m.content), m.mode}
			}
		}
		mo := &jsonx.Object{}
		keys := make([]string, 0, len(members))
		for k := range members {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			mo.Set(k, jsonx.NewObject("sha256", members[k][0], "mode", members[k][1]))
		}
		content := jsonBytesSorted(jsonx.NewObject("schema_version", 1, "owner", "plugin_bundle", "members", mo))
		m := stagedFile{path: l.path, content: content, mode: 0o644, owners: []string{"plugin_bundle"}, manifest: true, managed: true}
		if _, ok := unique[l.path]; !ok {
			order = append(order, l.path)
		}
		unique[l.path] = m
		before, err := observeConfined(root.Path, l.path)
		if err != nil {
			return nil, err
		}
		stageEffect(root, m, before, Proof{"managed_path", "bundle ownership manifest:" + l.path}, &effects, &disps)
	}
	for _, e := range append([]Effect(nil), effects...) {
		for _, parent := range parentsOf(e.Path) {
			st, err := observeConfined(root.Path, parent)
			if err != nil {
				return nil, err
			}
			if st.Kind == "absent" {
				effects = append(effects, NewEffect("plugin_bundle", "surface_repair", root, parent, "create", st, DirOf(0o755), "Create staged parent", e.Ownership, e.LogicalOwners, e.SurfaceIDs))
			}
		}
	}
	co, err := Coalesce(effects)
	if err != nil {
		return nil, err
	}
	return &Assessment{OwnerKey: "plugin_bundle", Root: root, Effects: co, Dispositions: disps, Complete: true, Prepared: &PreparedBundle{Files: unique}}, nil
}

// applyStaging is projection.apply_staging: directories first, then
// members, manifests, and ownership ledgers last.
func applyStaging(a *Assessment) ApplyResult {
	prep, _ := a.Prepared.(*PreparedBundle)
	rank := func(e Effect) int {
		if e.After.Kind == "directory" {
			return 0
		}
		if path.Base(e.Path) == bundleLedger {
			return 3
		}
		if prep.Files[e.Path].manifest {
			return 2
		}
		return 1
	}
	effects := append([]Effect(nil), a.Effects...)
	sort.SliceStable(effects, func(i, j int) bool {
		ri, rj := rank(effects[i]), rank(effects[j])
		if ri != rj {
			return ri < rj
		}
		di, dj := len(strings.Split(effects[i].Path, "/")), len(strings.Split(effects[j].Path, "/"))
		if di != dj {
			return di < dj
		}
		return effects[i].Path < effects[j].Path
	})
	var succeeded, failed []string
	var diags []Diagnostic
	for _, e := range effects {
		var err error
		cur, oerr := observeNode(e.Destination())
		if oerr != nil {
			err = oerr
		} else if cur.Kind != e.Before.Kind || cur.SHA256 != e.Before.SHA256 {
			err = fmt.Errorf("Staged destination changed during apply: %s", e.Path)
		} else if e.After.Kind == "directory" {
			if err = os.Mkdir(e.Destination(), os.FileMode(e.After.Mode)); err == nil {
				err = os.Chmod(e.Destination(), os.FileMode(e.After.Mode))
			}
		} else {
			m := prep.Files[e.Path]
			err = writeFileAtomic(e.Destination(), m.content, os.FileMode(m.mode))
		}
		if err != nil {
			failed = append(failed, e.ID())
			diags = append(diags, Diag("bundle_write_failed", "plugin_bundle", "error", err.Error()))
			break
		}
		succeeded = append(succeeded, e.ID())
	}
	done := map[string]bool{}
	for _, id := range append(append([]string(nil), succeeded...), failed...) {
		done[id] = true
	}
	var skipped []string
	for _, e := range effects {
		if !done[e.ID()] {
			skipped = append(skipped, e.ID())
		}
	}
	out := "applied"
	if len(failed) > 0 {
		out = "failed"
		if len(succeeded) > 0 {
			out = "partial"
		}
	}
	return ApplyResult{Owner: "plugin_bundle", Succeeded: succeeded, Failed: failed, Skipped: skipped, Diagnostics: diags, Outcome: out}
}

func (p pluginBundleProvider) repair(c *collectCtx, root string, statuses []Status) RepairResult {
	actionable := statusesOf(statuses, StateMissing, StateStale)
	if len(actionable) == 0 {
		return RepairResult{}
	}
	var targets []string
	for _, s := range actionable {
		targets = append(targets, s.Inst.Owner)
	}
	a := assessBundle(c, AssessInputs{Root: Root{"project", "project", absPath(root)}, Automatic: true}, targets)
	if !a.Complete {
		return RepairResult{Failed: idsOf(actionable)}
	}
	res := applyStaging(a)
	return RepairResult{Repaired: res.Succeeded, Failed: res.Failed, Skipped: res.Skipped}
}
