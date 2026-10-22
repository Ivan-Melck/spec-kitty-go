package charter

import (
	"os"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ProjectCharterBlock is upstream _project_charter_json_block.
func ProjectCharterBlock(root string) *jsonx.Object {
	y := Path(root, CharterYAML)
	o := jsonx.NewObject("present", exists(y), "path", CharterYAML, "charter_md_present", exists(Path(root, CharterMD)), "charter_md_path", CharterMD)
	st, err := os.Stat(y)
	if err != nil {
		return o
	}
	o.Set("bytes", int(st.Size()))
	md := readYAMLObject(Path(root, MetadataYAML))
	if md == nil {
		return o
	}
	if s := md.GetString("charter_hash"); s != "" {
		o.Set("hash", s)
	}
	if s := md.GetString("source_path"); s != "" {
		o.Set("source_path", s)
	}
	if v, ok := md.Get("bundle_schema_version"); ok {
		if n, ok := v.(int); ok {
			o.Set("bundle_schema_version", n)
		}
	}
	if s := md.GetString("schema_version"); s != "" {
		o.Set("schema_version", s)
	}
	return o
}

// AllDirectives is the `all_directives` array (project governance resolver
// directives, local charter entries first-class).
func AllDirectives(root string) []any {
	out := []any{}
	local := map[string]LocalDirective{}
	for _, d := range LoadLocalDirectives(root) {
		local[d.ID] = d
	}
	var ids []string
	if g, err := ResolveGovernance(root); err == nil {
		ids = g.Directives
	} else {
		for _, d := range LoadLocalDirectives(root) {
			ids = append(ids, d.ID)
		}
	}
	repo := dct.NewOffering(root, nil).Repo(dct.Directive) // _build_offering_service(repo_root): built-in + project, no org layer
	for _, id := range ids {
		if l, ok := local[id]; ok {
			e := jsonx.NewObject("id", id, "source", "project")
			if l.Title != "" {
				e.Set("title", l.Title)
			}
			if l.Description != "" {
				e.Set("summary", l.Description)
			}
			out = append(out, e)
			continue
		}
		out = append(out, ArtifactDTO(repo.Get(id), id))
	}
	return out
}

// ContextJSON is the `charter context --json` envelope.
func ContextJSON(root string, res *Result, missionType string, includeAll bool) *jsonx.Object {
	o := jsonx.NewObject("result", "success", "success", true, "context_schema_version", SchemaVersion,
		"action", res.Action, "mode", res.Mode, "first_load", res.FirstLoad, "references_count", res.ReferencesCount,
		"context", res.Text, "text", res.Text)
	empty := func() []any { return []any{} }
	directives, tactics, styleguides, toolguides, refs := empty(), empty(), empty(), empty(), empty()
	if BootstrapActions[res.Action] || actionNodeDeclared(root, missionType, res.Action) {
		b := ResolveActionBundle(root, res.Action, missionType, res.Depth)
		if b.Merged != nil {
			d := b.disclosure(includeAll)
			get := func(k string) []any { v, _ := d.Get(k); l, _ := v.([]any); return l }
			directives, tactics, styleguides, toolguides, refs = get("directives"), get("tactics"), get("styleguides"), get("toolguides"), get("references")
		}
	}
	o.Set("directives", directives)
	o.Set("all_directives", AllDirectives(root))
	o.Set("tactics", tactics)
	o.Set("styleguides", styleguides)
	o.Set("toolguides", toolguides)
	o.Set("references", refs)
	o.Set("governance_references", []any{})
	o.Set("project_charter", ProjectCharterBlock(root))
	o.Set("org_charter", OrgCharterJSONBlock(root))
	return o
}
