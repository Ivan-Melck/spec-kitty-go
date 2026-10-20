package charter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// kindLabel is the include header label of a kind.
func kindLabel(k dct.Kind) string {
	s := strings.ReplaceAll(string(k), "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

type fieldSpec struct{ key, label string }

var includeFields = map[dct.Kind][]fieldSpec{
	dct.Directive:    {{"intent", "Intent"}, {"scope", "Scope"}, {"procedures", "Procedures"}, {"integrity_rules", "Integrity rules"}, {"validation_criteria", "Validation criteria"}},
	dct.Tactic:       {{"name", "Name"}, {"purpose", "Purpose"}, {"steps", "Steps"}},
	dct.Styleguide:   {{"title", "Title"}, {"scope", "Scope"}, {"principles", "Principles"}},
	dct.Toolguide:    {{"title", "Title"}, {"tool", "Tool"}, {"summary", "Summary"}},
	dct.Paradigm:     {{"name", "Name"}, {"summary", "Summary"}},
	dct.Procedure:    {{"name", "Name"}, {"purpose", "Purpose"}, {"steps", "Steps"}},
	dct.AgentProfile: {{"name", "Name"}, {"purpose", "Purpose"}, {"roles", "Roles"}},
}

func scalarText(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func renderArtifactInclude(k dct.Kind, a *dct.Artifact) string {
	lines := []string{fmt.Sprintf("%s %s: %s", kindLabel(k), a.ID, a.Title())}
	for _, f := range includeFields[k] {
		v, ok := a.Data.Get(f.key)
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case []any:
			if len(t) == 0 {
				continue
			}
			if f.key == "roles" {
				var xs []string
				for _, x := range t {
					xs = append(xs, scalarText(x))
				}
				lines = append(lines, "    "+f.label+": "+strings.Join(xs, ", "))
				continue
			}
			lines = append(lines, "    "+f.label+":")
			for _, x := range t {
				if o, ok := x.(*jsonx.Object); ok {
					title := o.GetString("title")
					if title == "" {
						title = o.GetString("name")
					}
					lines = append(lines, "      - "+strings.TrimSpace(title))
					if d := o.GetString("description"); d != "" {
						lines = append(lines, "        "+strings.TrimSpace(d))
					}
					continue
				}
				lines = append(lines, "      - "+scalarText(x))
			}
		default:
			if s := scalarText(t); s != "" {
				lines = append(lines, "    "+f.label+": "+s)
			}
		}
	}
	lines = append(lines, "    Full artifact:")
	for _, l := range strings.Split(jsonx.Dumps(a.Data, jsonx.Options{Indent: 2, SortKeys: true}), "\n") {
		lines = append(lines, "      "+l)
	}
	return strings.Join(lines, "\n")
}

// templateInclude renders template:<mission>/<name>.
func templateInclude(root, ident string) (string, error) {
	mission, name, ok := strings.Cut(ident, "/")
	if !ok || mission == "" || name == "" {
		return "", fmt.Errorf("Template selector must be 'template:<mission>/<name>' (got %s).", dct.PyRepr("template:"+ident))
	}
	cands := []struct{ tier, path string }{
		{"override", filepath.Join(root, ".kittify", "overrides", "templates", name)},
		{"legacy", filepath.Join(root, ".kittify", "templates", name)},
		{"global_mission", filepath.Join(project.GlobalKittify(), "missions", mission, "templates", name)},
	}
	for _, c := range cands {
		if b, err := os.ReadFile(c.path); err == nil {
			return fmt.Sprintf("Template %s/%s (tier: %s):\n%s", mission, name, c.tier, string(b)), nil
		}
	}
	if b, err := assets.ReadFile(assets.BuiltInPackRoot + "/missions/" + mission + "/templates/" + name); err == nil {
		return fmt.Sprintf("Template %s/%s (tier: package_default):\n%s", mission, name, string(b)), nil
	}
	return "", fmt.Errorf("No template found for selector 'template:%s'.", ident)
}

// sectionInclude renders section:<id> (a charter.md heading and its body).
func sectionInclude(root, ident string) (string, error) {
	b, err := os.ReadFile(Path(root, CharterMD))
	if err != nil {
		return "", errors.New("No charter.md found for section selector.")
	}
	lines := strings.Split(string(b), "\n")
	slug := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		var out []rune
		dash := false
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				out = append(out, r)
				dash = false
			} else if !dash && len(out) > 0 {
				out = append(out, '-')
				dash = true
			}
		}
		return strings.Trim(string(out), "-")
	}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		lvl := len(t) - len(strings.TrimLeft(t, "#"))
		h := strings.TrimSpace(t[lvl:])
		if slug(h) != slug(ident) && h != ident {
			continue
		}
		out := []string{l}
		for _, m := range lines[i+1:] {
			mt := strings.TrimSpace(m)
			if strings.HasPrefix(mt, "#") && len(mt)-len(strings.TrimLeft(mt, "#")) <= lvl {
				break
			}
			out = append(out, m)
		}
		return strings.TrimRight(strings.Join(out, "\n"), "\n"), nil
	}
	return "", fmt.Errorf("No charter section found for selector 'section:%s'.", ident)
}

// BuildInclude is upstream build_charter_context_include.
func BuildInclude(root, selector string) (string, error) {
	kind, ident, ok := strings.Cut(selector, ":")
	kind, ident = strings.TrimSpace(kind), strings.TrimSpace(ident)
	if !ok || kind == "" || ident == "" {
		return "", fmt.Errorf("Invalid --include selector %s; expected <kind>:<id>.", dct.PyRepr(selector))
	}
	switch strings.ToLower(kind) {
	case "section":
		return sectionInclude(root, ident)
	case "template":
		return templateInclude(root, ident)
	case "artifact":
		var hits []string
		var text string
		off := dct.NewOffering(root, dct.OrgRoots(root))
		for _, k := range dct.AllKinds {
			if k == dct.Template || k == dct.Asset {
				continue
			}
			if a := off.Repo(k).Get(ident); a != nil {
				hits = append(hits, string(k))
				text = renderArtifactInclude(k, a)
			}
		}
		if len(hits) == 0 {
			return "", fmt.Errorf("No artifact found for selector 'artifact:%s'.", ident)
		}
		if len(hits) > 1 {
			return "", fmt.Errorf("Ambiguous artifact selector 'artifact:%s' matched kinds: %s.", ident, strings.Join(hits, ", "))
		}
		return text, nil
	}
	k, err := dct.ParseKind(kind)
	if err != nil {
		if errors.Is(err, dct.ErrMissionTypeToken) {
			return "", fmt.Errorf("--include does not support the 'mission-type' selector (selector %s); mission types are not addressable governance artifacts.", dct.PyRepr(selector))
		}
		return "", err
	}
	if k == dct.Template {
		return templateInclude(root, ident)
	}
	off := dct.NewOffering(root, dct.OrgRoots(root))
	a := off.Repo(k).Get(ident)
	if a == nil || (k == dct.AgentProfile && !profileActivated(root, ident)) {
		return "", fmt.Errorf("No %s found for selector '%s'.", string(k), selector)
	}
	return renderArtifactInclude(k, a), nil
}

func profileActivated(root, id string) bool {
	act, err := dct.LoadActivation(root)
	if err != nil {
		return true
	}
	return act.Activated(dct.AgentProfile).Has(id)
}
