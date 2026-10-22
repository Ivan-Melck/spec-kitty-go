package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports charter.activation.compiler: compile the charter catalog
// (catalog.references) from the activation state + DRG closure and write
// the charter.yaml bundle.

// CharterRef is one catalog reference.
type CharterRef struct {
	ID, Kind, Title, Summary, SourcePath, LocalPath string
}

// Compiled is CompiledCharter (the parts the CLI reports / writes).
type Compiled struct {
	Mission, TemplateSet string
	Paradigms            []string
	Directives           []string
	Tactics              []string
	AvailableTools       []string
	References           []CharterRef
	Diagnostics          []string
	Unresolved           []*jsonx.Object
	// Languages is the active language set (LanguagesKnown=false: None).
	Languages      []string
	LanguagesKnown bool
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(v string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(v), "-"), "-")
	if s == "" {
		return "item"
	}
	return s
}

func portableSourcePath(a *dct.Artifact, root string) string {
	if a == nil {
		return ""
	}
	if a.PackPath != "" {
		return "${SPEC_KITTY_PACKS_ROOT}/built-in/" + a.PackPath
	}
	if root != "" {
		if rel, err := filepath.Rel(root, a.Path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return a.Path
}

// configRoots is ConfigActivatedRoots.
type configRoots struct {
	byKind map[dct.Kind][]string
}

var rootKinds = []dct.Kind{dct.Directive, dct.Paradigm, dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure, dct.AgentProfile}

func resolveConfigRoots(root string, langs []string) (*configRoots, error) {
	t, err := LoadWriteTarget(root)
	if err != nil {
		return nil, err
	}
	return resolveConfigRootsFrom(root, t, langs)
}

// resolveConfigRootsFrom resolves the activation lists of t (possibly a
// proposed, unwritten activation state).
func resolveConfigRootsFrom(root string, t *WriteTarget, langs []string) (*configRoots, error) {
	configured := false
	for _, k := range rootKinds {
		if v, ok := t.Data.Get("activated_" + k.Plural()); ok && v != nil {
			configured = true
		}
	}
	off := dct.NewOffering(root, dct.OrgRoots(root))
	lr, chain := map[string]string{}, dct.OrgRoots(root)
	if p := dct.LayerRoots(root)[dct.LayerProject]; p != "" {
		lr[dct.LayerProject] = p
	}
	cr := &configRoots{byKind: map[dct.Kind][]string{}}
	for _, k := range rootKinds {
		key := "activated_" + k.Plural()
		cur, present, err := currentList(t.Data, key)
		if err != nil {
			return nil, err
		}
		if !present {
			if configured {
				cr.byKind[k] = []string{}
				continue
			}
			bo := dct.NewOffering("", nil)
			bo.Languages = langs
			ids := bo.Repo(k).IDs()
			if ids == nil {
				ids = []string{}
			}
			sort.Strings(ids)
			cr.byKind[k] = ids
			continue
		}
		stems := dct.StemsByDeclaredID(k, lr, chain)
		byStem := map[string]string{}
		for id, s := range stems {
			byStem[s] = id
		}
		set := map[string]bool{}
		for _, stem := range cur {
			switch {
			case byStem[stem] != "":
				set[byStem[stem]] = true
			case stems[stem] != "":
				set[stem] = true
			case k == dct.Directive && stems[dct.NormalizeDirectiveID(stem)] != "":
				set[dct.NormalizeDirectiveID(stem)] = true
			case off.Repo(k).Get(stem) != nil:
				set[off.Repo(k).Get(stem).ID] = true
			default:
				return nil, fmt.Errorf("%s Activation store: %s.", unknownConfigIDMessage(k, stem, chain, filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))), t.Path)
			}
		}
		var ids []string
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if ids == nil {
			ids = []string{}
		}
		cr.byKind[k] = ids
	}
	return cr, nil
}

func strOrEmpty(o *jsonx.Object, k string) string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return ""
	}
	return pyStrOf(v)
}

func modelRef(k dct.Kind, a *dct.Artifact) CharterRef {
	d := a.Data
	var id, title, summary string
	switch k {
	case dct.Directive:
		id, title, summary = a.ID, strOrEmpty(d, "title"), strOrEmpty(d, "intent")
	case dct.Tactic:
		id, title = a.ID, strOrEmpty(d, "name")
		summary = strOrEmpty(d, "purpose")
		if summary == "" {
			summary = "Tactic: " + title
		}
	case dct.Styleguide:
		id, title = a.ID, strOrEmpty(d, "title")
		if p := d.GetList("principles"); len(p) > 0 {
			summary = pyStrOf(p[0])
		} else {
			summary = "Styleguide: " + title
		}
	case dct.Toolguide:
		id, title, summary = a.ID, strOrEmpty(d, "title"), strOrEmpty(d, "summary")
	case dct.Procedure:
		id, title, summary = a.ID, strOrEmpty(d, "name"), strOrEmpty(d, "purpose")
	case dct.AgentProfile:
		id, title = a.ID, strOrEmpty(d, "name")
		summary = strOrEmpty(d, "description")
		if summary == "" {
			summary = "Agent profile: " + title
		}
	}
	kind := string(k)
	return CharterRef{ID: strings.ToUpper(kind) + ":" + id, Kind: kind, Title: title, Summary: summary,
		LocalPath: "_LIBRARY/" + kind + "-" + slugify(id) + ".md"}
}

// CompileCharter is compile_charter (rederive_languages=False).
func CompileCharter(root, mission string, iv *Interview, templateSet string) (*Compiled, error) {
	return CompileCharterWith(root, mission, iv, templateSet, false)
}

// CompileCharterWith is compile_charter with rederive_languages.
func CompileCharterWith(root, mission string, iv *Interview, templateSet string, rederive bool) (*Compiled, error) {
	c := &Compiled{Mission: mission}
	c.Languages, c.LanguagesKnown = InferRepoLanguages(root, iv, rederive)
	var langs []string
	if c.LanguagesKnown {
		langs = c.Languages
		if langs == nil {
			langs = []string{}
		}
	}
	cr, err := resolveConfigRoots(root, langs)
	if err != nil {
		return nil, err
	}
	// template set
	sets := templateSets()
	switch {
	case templateSet != "":
		if len(sets) > 0 && !sets[templateSet] {
			var opts []string
			for s := range sets {
				opts = append(opts, s)
			}
			sort.Strings(opts)
			return nil, fmt.Errorf("Unknown template set '%s'. Available template sets: %s", templateSet, strings.Join(opts, ", "))
		}
		c.TemplateSet = templateSet
	case sets[mission+"-default"]:
		c.TemplateSet = mission + "-default"
	case len(sets) > 0:
		var opts []string
		for s := range sets {
			opts = append(opts, s)
		}
		sort.Strings(opts)
		c.TemplateSet = opts[0]
	default:
		c.TemplateSet = mission + "-default"
	}
	// available tools
	allowed := map[string]string{}
	for _, t := range DefaultToolRegistry {
		allowed[strings.ToLower(t)] = t
	}
	var missing []string
	tools := []string{}
	for _, raw := range iv.AvailableTools {
		k := strings.TrimSpace(raw)
		if k == "" {
			continue
		}
		canon, ok := allowed[strings.ToLower(k)]
		if !ok {
			missing = append(missing, k)
			continue
		}
		tools = appendUnique(tools, canon)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		var q []string
		for _, m := range missing {
			q = append(q, "'"+m+"'")
		}
		verb := "is not a registered tool id"
		if len(missing) > 1 {
			verb = "are not registered tool ids"
		}
		c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("available_tools: %s %s; ignored (tool ids are validated separately from project languages)", strings.Join(q, ", "), verb))
	}
	c.AvailableTools = tools
	c.Paradigms, c.Directives, c.Tactics = cr.byKind[dct.Paradigm], cr.byKind[dct.Directive], cr.byKind[dct.Tactic]

	refs := []CharterRef{userProfileRef(iv)}
	indexParadigmAssets()
	off := dct.NewOffering(root, dct.OrgRoots(root))
	off.Languages = langs
	for _, p := range c.Paradigms {
		a := dct.NewOffering("", nil).Repo(dct.Paradigm).Get(p)
		ref := CharterRef{ID: "PARADIGM:" + p, Kind: "paradigm", LocalPath: "_LIBRARY/paradigm-" + slugify(p) + ".md"}
		if a == nil {
			ref.Title, ref.Summary = p, "Definition unavailable in bundled doctrine."
		} else {
			ref.Title = strOrEmpty(a.Data, "title")
			if ref.Title == "" {
				ref.Title = strOrEmpty(a.Data, "name")
			}
			if ref.Title == "" {
				ref.Title = p
			}
			ref.Summary = strOrEmpty(a.Data, "summary")
			if ref.Summary == "" {
				ref.Summary = strOrEmpty(a.Data, "intent")
			}
			if ref.Summary == "" {
				ref.Summary = "No summary provided."
			}
			ref.SourcePath = portableSourcePath(a, root)
		}
		refs = append(refs, ref)
	}
	// DRG transitive closure over requires/suggests from the activated roots.
	start := map[string]bool{}
	for _, d := range c.Directives {
		start["directive:"+d] = true
	}
	for _, k := range []dct.Kind{dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure, dct.AgentProfile} {
		for _, id := range cr.byKind[k] {
			start[string(k)+":"+id] = true
		}
	}
	buckets := map[string][]string{}
	if len(start) > 0 {
		// compile_charter's load_validated_graph(repo_root) is org-inert.
		g, err := dct.LoadValidatedGraph(root, nil)
		if err != nil {
			summary := dct.DRGErrorClass(err) + ": " + err.Error()
			c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("Graph load failed: %s. Transitive closure not resolved; direct-root ids only.", summary))
			c.Unresolved = append(c.Unresolved, jsonx.NewObject("kind", "_graph", "id", "_load_failure", "cause", "graph_load_failed", "detail", summary))
			buckets[string(dct.Directive)] = append([]string(nil), c.Directives...)
			for _, k := range []dct.Kind{dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure, dct.AgentProfile} {
				buckets[string(k)] = append([]string(nil), cr.byKind[k]...)
			}
		} else {
			var st []string
			for u := range start {
				st = append(st, u)
			}
			visited := dct.WalkEdges(g, st, map[string]bool{dct.RelRequires: true, dct.RelSuggests: true}, -1)
			nodes := g.NodeURNs()
			for u := range visited {
				if !nodes[u] {
					kind, id := dct.SplitURN(u)
					c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("Unresolved reference: %s/%s (missing_artifact): no node in the doctrine reference graph", kind, id))
					c.Unresolved = append(c.Unresolved, jsonx.NewObject("kind", kind, "id", id, "cause", "missing_artifact", "detail", "no node in the doctrine reference graph"))
					continue
				}
				kind, id := dct.SplitURN(u)
				buckets[kind] = append(buckets[kind], id)
			}
		}
	}
	for _, k := range []dct.Kind{dct.Directive, dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure, dct.AgentProfile} {
		ids := buckets[string(k)]
		sort.Strings(ids)
		repo := off.Repo(k)
		for _, id := range ids {
			if a := repo.Get(id); a != nil {
				refs = append(refs, modelRef(k, a))
				continue
			}
			cause, detail := "missing_artifact", "no artifact with this id in any doctrine layer (project, org, built-in); run `spec-kitty charter validate` to rule out a silent schema-validation drop"
			if repo.ScopeFiltered[id] {
				cause, detail = "scope_filtered", scopeFilteredSuggestion(id, langs, root)
			} else if sug := closeMatch(id, repo.IDs()); sug != "" {
				cause, detail = "typo_suspected", "did you mean '"+sug+"'?"
			}
			c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("Unresolved reference: %s/%s (%s): %s", k, id, cause, detail))
			c.Unresolved = append(c.Unresolved, jsonx.NewObject("kind", string(k), "id", id, "cause", cause, "detail", detail))
			if cause == "scope_filtered" {
				refs = append(refs, CharterRef{ID: strings.ToUpper(string(k)) + ":" + id, Kind: string(k), Title: id,
					Summary:   "Definition unavailable in bundled doctrine. Reason: scope_filtered — " + detail,
					LocalPath: "_LIBRARY/" + string(k) + "-" + slugify(id) + ".md"})
			}
		}
	}
	refs = append(refs, templateSetRef(mission, c.TemplateSet))
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	c.References = refs
	return c, nil
}

func templateSets() map[string]bool {
	out := map[string]bool{}
	bi := dct.BuiltIn()
	for _, rel := range bi.Walk("missions", "mission.yaml", true) {
		parts := strings.Split(rel, "/")
		if len(parts) == 3 {
			out[parts[1]+"-default"] = true
		}
	}
	return out
}

func templateSetRef(mission, set string) CharterRef {
	bi := dct.BuiltIn()
	rel := "missions/" + mission + "/mission.yaml"
	summary := "Mission template set for " + mission + "."
	if b, err := bi.ReadFile(rel); err == nil {
		if o, err := yamlx.ParseObject(b); err == nil && o != nil {
			if d := strOrEmpty(o, "description"); d != "" {
				summary = d
			}
		}
	}
	return CharterRef{ID: "TEMPLATE_SET:" + set, Kind: "template_set", Title: set, Summary: summary,
		SourcePath: "${SPEC_KITTY_PACKS_ROOT}/built-in/" + rel, LocalPath: "_LIBRARY/template-set-" + slugify(set) + ".md"}
}

func userProfileRef(iv *Interview) CharterRef {
	return CharterRef{ID: "USER:PROJECT_PROFILE", Kind: "user_profile", Title: "User Project Profile",
		Summary: "Project-specific interview answers captured for charter compilation.", SourcePath: ".kittify/charter/interview/answers.yaml",
		LocalPath: "_LIBRARY/user-project-profile.md"}
}

func (c *Compiled) catalogObject() *jsonx.Object {
	refs := []any{}
	for _, r := range c.References {
		refs = append(refs, jsonx.NewObject("id", r.ID, "kind", r.Kind, "title", r.Title, "summary", r.Summary, "source_path", r.SourcePath, "local_path", r.LocalPath))
	}
	o := jsonx.NewObject("mission", c.Mission, "template_set", c.TemplateSet)
	if c.LanguagesKnown {
		o.Set("languages", strs(c.Languages))
	}
	o.Set("references", refs)
	return o
}

// scopeFilteredSuggestion is classify_scope_filtered_miss's suggestion.
func scopeFilteredSuggestion(id string, active []string, root string) string {
	if LacksSpecialistGuidance(active, true, root) {
		return "artifact '" + id + "' is scoped to specific languages and does not apply to a project whose language Spec Kitty does not recognise. " + CharterExtensionAdvisory
	}
	if len(active) > 0 {
		var q []string
		for _, l := range active {
			q = append(q, dct.PyRepr(l))
		}
		return "artifact '" + id + "' is present but its applies_to_languages scope does not include the active language set (" + strings.Join(q, ", ") + "). Add the active language to the artifact's applies_to_languages field, or remove the field to make it always-applicable."
	}
	return "artifact '" + id + "' is present but its applies_to_languages scope was filtered out. Check the artifact's applies_to_languages field, or remove it to make the artifact always-applicable."
}

func strs(l []string) []any {
	out := []any{}
	for _, s := range l {
		out = append(out, s)
	}
	return out
}

// defaultGovernance is GovernanceConfig().model_dump(mode="json").
func defaultGovernance() *jsonx.Object {
	empty := func() []any { return []any{} }
	return jsonx.NewObject(
		"testing", jsonx.NewObject("min_coverage", 0, "tdd_required", false, "framework", "", "type_checking", ""),
		"quality", jsonx.NewObject("linting", "", "pr_approvals", 1, "pre_commit_hooks", false),
		"commits", &jsonx.Object{},
		"performance", jsonx.NewObject("cli_timeout_seconds", 2.0, "dashboard_max_wps", 100),
		"branch_strategy", jsonx.NewObject("main_branch", "main", "rules", empty()),
		"charter", jsonx.NewObject("selected_paradigms", empty(), "selected_directives", empty(), "selected_tactics", empty(),
			"selected_styleguides", empty(), "selected_toolguides", empty(), "selected_procedures", empty(),
			"selected_agent_profiles", empty(), "selected_mission_step_contracts", empty(), "selected_glossary_packs", empty(),
			"selected_assets", empty(), "available_tools", empty(), "authority_paths", empty(), "governance_references", empty()),
		"activations", empty(),
		"enforcement", &jsonx.Object{},
	)
}

// WriteCompiled is write_compiled_charter (+ config pointer mint and
// mission-type provisioning). Returns files_written.
func WriteCompiled(root string, c *Compiled) ([]string, error) {
	dir := Path(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	yp := Path(root, CharterYAML)
	catalog := c.catalogObject()
	if b, err := os.ReadFile(yp); err == nil {
		doc, _ := yamlx.ParseObject(b)
		if doc == nil {
			doc = &jsonx.Object{}
		}
		// The update path dumps CharterCatalog without exclude_none:
		// languages (None for an unconfigured project) is present.
		var langs any
		if c.LanguagesKnown {
			langs = strs(c.Languages)
		}
		catalog = jsonx.NewObject("mission", c.Mission, "template_set", c.TemplateSet, "languages", langs, "references", mustGetAny(catalog, "references"))
		stamp := nowStamp()
		if old := doc.GetObject("catalog"); old != nil && equalVals(old, catalog) {
			if m := doc.GetObject("metadata"); m != nil && m.GetString("generated_at") != "" {
				stamp = m.GetString("generated_at")
			}
		}
		doc.Set("catalog", catalog)
		doc.Set("metadata", jsonx.NewObject("generated_at", stamp, "bundle_schema_version", 2))
		if err := writeYAMLDocument(yp, doc, 4096); err != nil {
			return nil, err
		}
	} else {
		doc := jsonx.NewObject("schema_version", "2.0.0", "governance", defaultGovernance(), "directives", jsonx.NewObject("directives", []any{}),
			"catalog", catalog, "overrides", &jsonx.Object{}, "metadata", jsonx.NewObject("generated_at", nowStamp(), "bundle_schema_version", 2))
		if cfg, err := dct.LoadConfig(root); err == nil {
			for _, k := range ActivationYAMLKeys() {
				if v, ok := cfg.Get(k); ok {
					if l, isL := v.([]any); isL {
						out := []any{}
						for _, x := range l {
							out = append(out, pyStrOf(x))
						}
						doc.Set(k, out)
					}
				}
			}
		}
		if err := os.WriteFile(yp, []byte(yamlx.EmitWith(doc, yamlx.Options{Width: 4096})), 0o644); err != nil {
			return nil, err
		}
		// mint the config.yaml `charter:` pointer
		cfgPath := dct.ConfigPath(root)
		cfgDoc := &jsonx.Object{}
		if b, err := os.ReadFile(cfgPath); err == nil {
			if o, err := yamlx.ParseObject(b); err == nil && o != nil {
				cfgDoc = o
			}
		}
		cfgDoc.Set("charter", ".kittify/charter/charter.yaml")
		if err := writeYAMLDocument(cfgPath, cfgDoc, 80); err != nil {
			return nil, err
		}
	}
	if err := ProvisionMissionTypes(root); err != nil {
		return nil, err
	}
	return []string{"charter.yaml"}, nil
}

// ProvisionMissionTypes seeds mission_type_activations from the built-in
// default preset when the key is absent (additive, idempotent).
func ProvisionMissionTypes(root string) error {
	t, err := LoadWriteTarget(root)
	if err != nil {
		return err
	}
	if _, ok := t.Data.Get("mission_type_activations"); ok {
		return nil
	}
	path := filepath.Join(dct.BuiltInRoot(), "presets", "default.yaml")
	p, err := LoadPresetFile(path)
	if err != nil || len(p.MissionTypes) == 0 {
		return &dct.ConfigError{Body: fmt.Sprintf("The built-in pack's `default` preset %s does not exist. Cannot provision this project's mission types. This indicates a broken spec-kitty install: reinstall spec-kitty and re-run the command.", path)}
	}
	t.Data.Set("mission_type_activations", strsAny(p.MissionTypes))
	return t.Save()
}

func init() {
	recompileCompiledCharter = func(root string) error {
		mission := "software-dev"
		if b, err := os.ReadFile(Path(root, CharterYAML)); err == nil {
			if o, err := yamlx.ParseObject(b); err == nil && o != nil {
				if m := o.GetObject("catalog").GetString("mission"); strings.TrimSpace(m) != "" {
					mission = m
				}
			}
		}
		if mission == "software-dev" {
			if iv := ReadInterview(InterviewPath(root)); iv != nil && iv.Mission != "" {
				mission = iv.Mission
			}
		}
		c, err := CompileCharter(root, mission, DefaultInterview(mission, "minimal"), "")
		if err != nil {
			return err
		}
		_, err = WriteCompiled(root, c)
		return err
	}
}

// unknownConfigIDMessage is kind_vocabulary's UnknownArtifactIdError text
// for resolve_artifact_urn with layer_roots={"project": projectRoot}.
func unknownConfigIDMessage(k dct.Kind, stem string, orgRoots []string, projectRoot string) string {
	clause := ""
	if len(orgRoots) > 0 {
		q := make([]string, len(orgRoots))
		for i, r := range orgRoots {
			q[i] = dct.PyRepr(r)
		}
		clause = " or org/project pack roots [" + strings.Join(q, ", ") + "]"
	}
	var layers []string
	if projectRoot != "" {
		layers = append(layers, "project: "+filepath.Join(projectRoot, dct.ProjectKindDir(k)))
	}
	for _, r := range orgRoots {
		layers = append(layers, "org: "+filepath.Join(r, k.Plural()))
	}
	biDir := k.Plural()
	if k == dct.MissionStepContract {
		biDir = "missions/built_in_step_contracts"
	}
	layers = append(layers, "built-in: "+filepath.Join(dct.BuiltInRoot(), biDir))
	return fmt.Sprintf("No %s artifact with config ID %s found under doctrine root %s%s. Searched layers: %s. Check activated_%s in the charter.yaml activation store selected by `.kittify/config.yaml` (or its legacy inline activations) for a stale or misspelled entry, or run `spec-kitty doctor charter-packs` to verify the doctrine corpus (including any org packs) is intact.",
		string(k), dct.PyRepr(stem), dct.BuiltInRoot(), clause, strings.Join(layers, "; "), k.Plural())
}
