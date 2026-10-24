package toolsurface

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// LogWriter receives upstream's logger warnings (nil = silent, e.g. --json).
var LogWriter io.Writer

// PackSkillCatalogError is skills.catalog.PackSkillCatalogError.
type PackSkillCatalogError struct{ Msg string }

func (e *PackSkillCatalogError) Error() string { return e.Msg }

// packSkill is the subset of charter.offering.pack_skills.PackSkill the
// renderer and the source hash need, plus its tier and source file.
type packSkill struct {
	record *jsonx.Object // model_dump(mode="json")
	id     string
	title  string
	desc   string
	trig   []string
	form   string
	body   string // body_path
	target string
	args   string
	params []packParam
	user   bool
	model  bool
	tier   string
	source string
}

type packParam struct {
	name, desc string
	required   bool
	def        *string
}

var reSkillID = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func strList(v any) ([]string, bool) {
	l, ok := v.([]any)
	if !ok {
		return nil, v == nil
	}
	var out []string
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// parsePackSkill validates one *.skill.yaml like the PackSkill model.
func parsePackSkill(path, tier, namespace string) (*packSkill, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return nil, fmt.Errorf("not a YAML mapping")
	}
	str := func(k, def string) (string, error) {
		v, ok := o.Get(k)
		if !ok || v == nil {
			return def, nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%s: Input should be a valid string", k)
		}
		return s, nil
	}
	known := map[string]bool{"schema_version": true, "id": true, "title": true, "description": true, "triggers": true, "form": true, "body_path": true,
		"expands_to": true, "parameters": true, "invocation": true, "tools": true, "version": true, "maintainers": true, "overrides": true, "enhances": true}
	for _, k := range o.Keys() {
		if !known[k] {
			return nil, fmt.Errorf("%s: Extra inputs are not permitted", k)
		}
	}
	s := &packSkill{tier: tier, source: path, user: true, model: true}
	sv, _ := str("schema_version", "")
	if sv != "1.0" {
		return nil, errors.New("schema_version: Input should be '1.0'")
	}
	if s.id, err = str("id", ""); err != nil {
		return nil, err
	}
	if s.title, err = str("title", ""); err != nil || s.title == "" {
		return nil, errors.New("title: String should have at least 1 character")
	}
	if s.desc, err = str("description", ""); err != nil || s.desc == "" {
		return nil, errors.New("description: String should have at least 1 character")
	}
	if s.form, _ = str("form", ""); s.form != "prompt" && s.form != "wrapper" {
		return nil, errors.New("form: Input should be 'prompt' or 'wrapper'")
	}
	s.body, _ = str("body_path", "")
	trig, ok := strList(mustGet(o, "triggers"))
	if !ok {
		return nil, errors.New("triggers: Input should be a valid list")
	}
	s.trig = trig
	expands := any(nil)
	if e := o.GetObject("expands_to"); e != nil {
		s.target, s.args = e.GetString("target"), e.GetString("args")
		if s.target == "" {
			return nil, errors.New("expands_to.target: String should have at least 1 character")
		}
		expands = jsonx.NewObject("target", s.target, "args", s.args)
	}
	var params []any
	for _, p := range o.GetList("parameters") {
		po, ok := p.(*jsonx.Object)
		if !ok || po.GetString("name") == "" {
			return nil, errors.New("parameters: invalid parameter")
		}
		pp := packParam{name: po.GetString("name"), desc: po.GetString("description"), required: po.GetBool("required")}
		var def any
		if v, ok := po.Get("default"); ok && v != nil {
			d := fmt.Sprint(v)
			pp.def, def = &d, d
		}
		s.params = append(s.params, pp)
		params = append(params, jsonx.NewObject("name", pp.name, "required", pp.required, "description", pp.desc, "default", def))
	}
	sideEffects := []any{}
	if inv := o.GetObject("invocation"); inv != nil {
		if v, ok := inv.Get("user_invocable"); ok {
			s.user, _ = v.(bool)
		}
		if v, ok := inv.Get("model_invocable"); ok {
			s.model, _ = v.(bool)
		}
		for _, x := range inv.GetList("side_effects") {
			sideEffects = append(sideEffects, fmt.Sprint(x))
		}
		if len(sideEffects) > 0 {
			s.model = false
		}
	}
	tools := []any{"*"}
	if v, ok := o.Get("tools"); ok {
		l, _ := strList(v)
		tools = []any{}
		for _, x := range l {
			tools = append(tools, x)
		}
	}
	version, _ := str("version", "1.0.0")
	maint := []any{}
	for _, x := range o.GetList("maintainers") {
		maint = append(maint, fmt.Sprint(x))
	}
	overrides, _ := str("overrides", "")
	enhances, _ := str("enhances", "")
	if len(s.id) > 64 || !reSkillID.MatchString(s.id) {
		return nil, fmt.Errorf("Value error, skill id '%s' must be lowercase ASCII kebab-case of at most 64 characters", s.id)
	}
	if s.form == "prompt" && s.body == "" {
		return nil, errors.New("Value error, prompt-form skill requires 'body_path'")
	}
	if s.form == "wrapper" && s.target == "" {
		return nil, errors.New("Value error, wrapper-form skill requires 'expands_to'")
	}
	if s.form == "prompt" && overrides == "" && enhances == "" {
		bp := filepath.Clean(filepath.Join(filepath.Dir(path), s.body))
		if !strings.HasPrefix(bp, filepath.Dir(path)+string(filepath.Separator)) {
			return nil, fmt.Errorf("skill '%s': body_path '%s' escapes the skill directory", s.id, s.body)
		}
		if st, err := os.Stat(bp); err != nil || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("skill '%s': body file '%s' not found", s.id, s.body)
		}
	}
	if tier != "built-in" {
		for _, c := range []string{s.id, renderedName(s.id, namespace)} {
			for _, pre := range []string{"spk-", "spec-kitty-", "spec-kitty."} {
				if strings.HasPrefix(c, pre) {
					return nil, fmt.Errorf("skill '%s': name '%s' uses a prefix reserved for built-in skills ['spk-', 'spec-kitty-', 'spec-kitty.']", s.id, c)
				}
			}
		}
	}
	nz := func(x string) any {
		if x == "" {
			return nil
		}
		return x
	}
	trigAny := []any{}
	for _, t := range s.trig {
		trigAny = append(trigAny, t)
	}
	if params == nil {
		params = []any{}
	}
	s.record = jsonx.NewObject("schema_version", "1.0", "id", s.id, "title", s.title, "description", s.desc, "triggers", trigAny, "form", s.form,
		"body_path", nz(s.body), "expands_to", expands, "parameters", params,
		"invocation", jsonx.NewObject("user_invocable", s.user, "model_invocable", s.model, "side_effects", sideEffects),
		"tools", tools, "version", version, "maintainers", maint, "overrides", nz(overrides), "enhances", nz(enhances))
	return s, nil
}

// skillGraph is the merged built-in + org + project DRG, read from the
// embedded built-in pack (no cache materialization).
func skillGraph(root string, orgRoots []string) (*dct.Graph, error) {
	merge := func(g, o *dct.Graph) *dct.Graph {
		if g == nil {
			return o
		}
		return dct.MergeLayers(g, o)
	}
	var g *dct.Graph
	if b, err := assets.ReadFile(assets.BuiltInPackRoot + "/graph.yaml"); err == nil {
		if g, err = dct.ParseGraph(b, "graph.yaml"); err != nil {
			return nil, err
		}
	} else {
		ents, err := assets.List(assets.BuiltInPackRoot)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".graph.yaml") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			b, err := assets.ReadFile(assets.BuiltInPackRoot + "/" + n)
			if err != nil {
				return nil, err
			}
			fg, err := dct.ParseGraph(b, n)
			if err != nil {
				return nil, err
			}
			g = merge(g, fg)
		}
	}
	disk := func(dir string) error {
		if !dct.HasGraphFiles(dir) {
			return nil
		}
		files := []string{filepath.Join(dir, "graph.yaml")}
		if _, err := os.Stat(files[0]); err != nil {
			files, _ = filepath.Glob(filepath.Join(dir, "*.graph.yaml"))
			sort.Strings(files)
		}
		var layer *dct.Graph
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			fg, err := dct.ParseGraph(b, f)
			if err != nil {
				return err
			}
			layer = merge(layer, fg)
		}
		g = merge(g, layer)
		return nil
	}
	var fragments []*dct.Graph
	for _, r := range orgRoots {
		if !dct.HasGraphFiles(r) {
			frag := filepath.Join(r, "drg", "fragment.yaml")
			if b, err := os.ReadFile(frag); err == nil {
				if fg, err := dct.ParseGraph(b, frag); err == nil {
					fragments = append(fragments, fg)
				}
				continue
			}
			if LogWriter != nil {
				rich.LogWarning(LogWriter, fmt.Sprintf("Org pack at %s ships no root-level DRG graph (graph.yaml / *.graph.yaml) and no drg/fragment.yaml; "+
					"it contributes no dependency graph to cascade and was skipped. Author a root-level *.graph.yaml, or supply its drg/fragment.yaml "+
					"through org_fragments, to contribute requires/suggests edges.", r))
			}
			continue
		}
		if err := disk(r); err != nil {
			return nil, err
		}
	}
	for _, f := range fragments {
		g = merge(g, f)
	}
	if err := disk(filepath.Join(root, ".kittify", "charter-packs")); err != nil {
		return nil, err
	}
	return g, nil
}

func mustGet(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

func renderedName(id, namespace string) string {
	if namespace != "" {
		return namespace + "-" + id
	}
	return id
}

// loadPackSkills is PackSkillRepository: built-in, then org packs in order,
// then the project pack (later tiers replace earlier ids). Unloadable files
// are reported as load problems.
func loadPackSkills(root string, orgRoots []string, namespace string) (map[string]*packSkill, []string) {
	items := map[string]*packSkill{}
	var problems []string
	scan := func(dir, tier string) {
		var files []string
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".skill.yaml") {
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			s, err := parsePackSkill(f, tier, namespace)
			if err != nil {
				problems = append(problems, fmt.Sprintf("Skipping invalid %s pack-skill %s: %v", tier, filepath.Base(f), err))
				continue
			}
			items[s.id] = s
		}
	}
	for _, r := range orgRoots {
		scan(filepath.Join(r, "skills"), "org")
	}
	scan(filepath.Join(root, ".kittify", "charter-packs", "skills"), "project")
	return items, problems
}

// PreparedPackSkill is skill_preparation.PreparedSkill plus its rendering.
type PreparedPackSkill struct {
	ID, RenderedName, SourcePath, SourceHash, Text string
}

func orgCharterDocs(root string) []*jsonx.Object {
	var out []*jsonx.Object
	for _, p := range dct.OrgPacks(root) {
		b, err := os.ReadFile(filepath.Join(p.Root, "org-charter.yaml"))
		if err != nil {
			continue
		}
		o, err := yamlx.ParseObject(b)
		if err != nil || o == nil {
			continue
		}
		out = append(out, o)
	}
	return out
}

func validNamespace(ns string) bool { return len(ns) <= 32 && reSkillID.MatchString(ns) }

func namespaceError(ns, remedy string) error {
	return fmt.Errorf("skill namespace '%s' is not valid: it must be lowercase ASCII kebab-case (letters and digits joined by single '-', starting with a letter, at most 32 characters); %s", ns, remedy)
}

// inForceSkills is establish_in_force_skill_ids (nil = none in force).
func inForceSkills(root string, installed bool) ([]string, error) {
	src, err := dct.ActivationSource(root)
	if err != nil {
		return nil, err
	}
	if v, ok := src.Get("activated_skills"); ok {
		l, isList := strList(v)
		if !isList {
			return nil, fmt.Errorf("activated_skills must be a list")
		}
		return uniqueSorted(l), nil
	}
	var out []string
	seen := map[string]bool{}
	for _, doc := range orgCharterDocs(root) {
		l, _ := strList(mustGet(doc, "required_skills"))
		for _, x := range l {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	return out, nil
}

func packSkillsMatter(root string, installed bool) bool {
	if installed {
		return true
	}
	src, err := dct.ActivationSource(root)
	if err != nil {
		return true
	}
	v, ok := src.Get("activated_skills")
	if !ok {
		return false
	}
	l, _ := strList(v)
	return len(l) > 0
}

// PreparePackSkills is catalog._render_active_pack_skills: the rendered
// pack skills in force (nil when none); a *PackSkillCatalogError refuses.
func PreparePackSkills(root string) ([]PreparedPackSkill, error) {
	installed := false
	if m, err := loadSkillsManifestStrict(root); err == nil && m != nil {
		for _, e := range m.Entries {
			if o, _, _ := e.Provenance(); o == "pack" {
				installed = true
			}
		}
	}
	mayHave := packSkillsMatter(root, installed) || isDir(filepath.Join(root, ".kittify", "charter-packs", "skills")) || len(dct.OrgRoots(root)) > 0
	if !mayHave {
		return nil, nil
	}
	ids, err := inForceSkills(root, installed)
	if err != nil {
		if !packSkillsMatter(root, installed) {
			return nil, nil
		}
		return nil, &PackSkillCatalogError{err.Error()}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := preparePackSkillIDs(root, ids)
	if err != nil {
		return nil, &PackSkillCatalogError{err.Error()}
	}
	shipped := map[string]bool{}
	for _, s := range agents.Skills() {
		shipped[s.Name] = true
	}
	for _, p := range out {
		if shipped[p.RenderedName] {
			return nil, &PackSkillCatalogError{fmt.Sprintf("pack skill '%s' renders as '%s', which is the name of a built-in skill; change its id or the pack's skill namespace", p.ID, p.RenderedName)}
		}
	}
	return out, nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func preparePackSkillIDs(root string, ids []string) ([]PreparedPackSkill, error) {
	orgNS := ""
	for _, doc := range orgCharterDocs(root) {
		if s, ok := mustGet(doc, "skill_namespace").(string); ok && strings.TrimSpace(s) != "" {
			orgNS = strings.TrimSpace(s)
			if !validNamespace(orgNS) {
				return nil, namespaceError(orgNS, "fix `skill_namespace` in the org pack's org-charter.yaml")
			}
		}
	}
	projNS := ""
	if cfg, err := dct.LoadConfig(root); err == nil && cfg != nil {
		if cp := cfg.GetObject("charter_packs"); cp != nil {
			if pr := cp.GetObject("project"); pr != nil {
				if s, ok := mustGet(pr, "skill_namespace").(string); ok && strings.TrimSpace(s) != "" {
					projNS = strings.TrimSpace(s)
					if !validNamespace(projNS) {
						return nil, namespaceError(projNS, "set `charter_packs.project.skill_namespace` in .kittify/config.yaml")
					}
				}
			}
		}
	}
	orgRoots := dct.OrgRoots(root)
	items, problems := loadPackSkills(root, orgRoots, orgNS)
	graph, gerr := skillGraph(root, orgRoots)
	var out []PreparedPackSkill
	owners := map[string]string{}
	for _, id := range uniqueSorted(ids) {
		s := items[id]
		if s == nil {
			reasons := ""
			if len(problems) > 0 {
				reasons = " (the loader reported: " + strings.Join(problems, "; ") + ")"
			}
			return nil, fmt.Errorf("activated skill '%s' is not available in any pack tier%s", id, reasons)
		}
		ns := orgNS
		remedy := "set `skill_namespace` in the org pack's org-charter.yaml"
		if s.tier == "project" {
			ns, remedy = projNS, "set `charter_packs.project.skill_namespace` in .kittify/config.yaml"
		}
		if ns == "" {
			return nil, fmt.Errorf("%s-tier skill '%s' has no skill namespace to render under; %s", s.tier, id, remedy)
		}
		name := renderedName(id, ns)
		var requires []string
		if gerr == nil {
			for _, e := range graph.EdgesFrom("skill:"+id, "requires") {
				requires = append(requires, e.Target)
			}
		}
		requires = uniqueSorted(requires)
		body := ""
		if s.form == "prompt" && s.body != "" {
			if b, err := os.ReadFile(filepath.Join(filepath.Dir(s.source), filepath.FromSlash(s.body))); err == nil {
				body = string(b)
			}
		}
		reqAny := []any{}
		for _, r := range requires {
			reqAny = append(reqAny, r)
		}
		canonical := jsonx.Dumps(jsonx.NewObject("record", s.record, "requires", reqAny, "rendered_name", name), jsonx.Line)
		h := sha256.Sum256(append(append([]byte(canonical), 0), []byte(body)...))
		text, err := renderPackSkill(s, name, requires, body)
		if err != nil {
			return nil, err
		}
		if prev, ok := owners[name]; ok && prev != id {
			return nil, fmt.Errorf("skills '%s' and '%s' both render as '%s'; give one a different id or namespace", prev, id, name)
		}
		owners[name] = id
		out = append(out, PreparedPackSkill{ID: id, RenderedName: name, SourcePath: s.source, SourceHash: "sha256:" + hex.EncodeToString(h[:]), Text: text})
	}
	return out, nil
}

func jsonQuoted(s string) string { return jsonx.QuoteString(s, false) }

// renderPackSkill is pack_skill_renderer.render_pack_skill.
func renderPackSkill(s *packSkill, name string, requires []string, body string) (string, error) {
	desc := strings.Join(strings.Fields(s.desc), " ")
	if len(s.trig) > 0 {
		desc = desc + " Triggers: " + strings.Join(s.trig, ", ") + "."
	}
	fm := []string{"---", "name: " + jsonQuoted(name), "description: " + jsonQuoted(desc), fmt.Sprintf("user-invocable: %t", s.user)}
	if !s.model {
		fm = append(fm, "disable-model-invocation: true")
	}
	fm = append(fm, "---")
	sections := []string{strings.Join(fm, "\n"),
		"<!-- Generated by spec-kitty from a charter pack skill. A local edit is kept and reported as drift; to change this skill, edit its pack source and re-run the activation. -->",
		"# " + s.title}
	if len(requires) > 0 {
		var cmds []string
		for _, u := range requires {
			cmds = append(cmds, "- `spec-kitty charter context --include "+u+"`")
		}
		sections = append(sections, "## Governing context\n\nBefore acting, load the doctrine this skill depends on:\n\n"+strings.Join(cmds, "\n"))
	}
	if len(s.params) > 0 {
		lines := []string{"## Parameters", ""}
		for _, p := range s.params {
			details := []string{"optional"}
			if p.required {
				details[0] = "required"
			}
			if p.def != nil {
				details = append(details, "default `"+*p.def+"`")
			}
			suffix := ""
			if p.desc != "" {
				suffix = ": " + p.desc
			}
			lines = append(lines, "- `"+p.name+"` ("+strings.Join(details, ", ")+")"+suffix)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if s.form == "prompt" {
		b := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n"))
		if b == "" {
			return "", fmt.Errorf("prompt-form skill '%s' has an empty body", s.id)
		}
		sections = append(sections, "## Instructions\n\n"+b)
	} else {
		args := ""
		if s.args != "" {
			args = " Pass these arguments: `" + s.args + "`."
		}
		switch {
		case strings.HasPrefix(s.target, "builtin:"):
			n := strings.TrimSpace(strings.TrimPrefix(s.target, "builtin:"))
			cmd := ""
			if strings.HasPrefix(n, "spec-kitty.") {
				cmd = strings.TrimPrefix(n, "spec-kitty.")
			}
			if !containsStr(agents.CommandSkillCommands, cmd) {
				var known []string
				for _, c := range agents.CommandSkillCommands {
					known = append(known, "spec-kitty."+c)
				}
				return "", fmt.Errorf("wrapper skill '%s' targets unknown built-in command '%s'; known commands: %s", s.id, n, strings.Join(known, ", "))
			}
			sections = append(sections, "## Instructions\n\nInvoke the `spec-kitty."+cmd+"` command skill."+args)
		case strings.HasPrefix(s.target, "cli:"):
			argv := strings.TrimSpace(strings.TrimPrefix(s.target, "cli:"))
			if s.args != "" {
				argv += " " + s.args
			}
			sections = append(sections, "## Instructions\n\nRun the CLI command:\n\n```bash\n"+argv+"\n```")
		default:
			return "", fmt.Errorf("wrapper skill '%s' has an unsupported target '%s'", s.id, s.target)
		}
	}
	return strings.Join(sections, "\n\n") + "\n", nil
}

// portableRef is catalog._portable_ref.
func portableRef(source, root string) string {
	rs, err1 := filepath.EvalSymlinks(source)
	rr, err2 := filepath.EvalSymlinks(root)
	if err1 == nil && err2 == nil {
		if r, err := filepath.Rel(rr, rs); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(source)
}

// stagePackSkills is catalog._stage: materialize the rendered pack skills
// under .kittify/runtime/pack-skills, removing stale entries.
func stagePackSkills(root string, rendered []PreparedPackSkill) error {
	dir := filepath.Join(root, ".kittify", "runtime", "pack-skills")
	if len(rendered) == 0 {
		if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 {
			return os.RemoveAll(dir)
		}
		return nil
	}
	want := map[string]bool{}
	for _, r := range rendered {
		want[r.RenderedName] = true
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if !want[e.Name()] {
				os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	for _, r := range rendered {
		p := filepath.Join(dir, r.RenderedName, "SKILL.md")
		if b, err := os.ReadFile(p); err == nil && string(b) == r.Text {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(p, []byte(r.Text), 0o644); err != nil {
			return err
		}
	}
	return nil
}
