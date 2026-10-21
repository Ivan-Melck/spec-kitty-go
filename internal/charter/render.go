package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const noneLabel = "(none)"

// CompactInput feeds RenderCompact.
type CompactInput struct {
	Action                                                           string
	Directives, Tactics, Styleguides, Toolguides, Procedures, Assets []string
	Profile                                                          *dct.Profile
	SuppressResolver                                                 bool
}

// SectionAnchors extracts ATX heading texts (ordered, deduplicated).
func SectionAnchors(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		lvl := 0
		for lvl < len(s) && s[lvl] == '#' {
			lvl++
		}
		if lvl == 0 || lvl > 6 || lvl == len(s) || s[lvl] != ' ' {
			continue
		}
		a := strings.TrimSpace(s[lvl+1:])
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

func section(lines []string, title string, vals []string, always bool) []string {
	if len(vals) == 0 && !always {
		return lines
	}
	lines = append(lines, title)
	if len(vals) == 0 {
		return append(lines, "  - "+noneLabel)
	}
	for _, v := range vals {
		lines = append(lines, "  - "+v)
	}
	return lines
}

// RenderCompact is upstream _render_compact_governance (without the
// token-budget pass and profile section prose; see doctrine.md).
func RenderCompact(root string, in CompactInput) string {
	tset, paradigms, tools := noneLabel, noneLabel, noneLabel
	var diags, resolverDirs []string
	if g, err := ResolveGovernance(root); err != nil {
		diags = append(diags, fmt.Sprintf("governance unavailable (%v)", err))
	} else {
		tset = g.TemplateSet
		if len(g.Paradigms) > 0 {
			paradigms = strings.Join(g.Paradigms, ", ")
		}
		if len(g.Tools) > 0 {
			tools = strings.Join(g.Tools, ", ")
		}
		diags = g.Diagnostics
		resolverDirs = g.Directives
	}
	if in.SuppressResolver {
		resolverDirs = nil
	}
	md, _ := os.ReadFile(Path(root, CharterMD))
	lines := []string{"Governance:", "  - Template set: " + tset, "  - Paradigms: " + paradigms, "  - Tools: " + tools}
	lines = section(lines, "Directive IDs:", dedupe(append(append([]string(nil), in.Directives...), resolverDirs...)), true)
	lines = section(lines, "Tactic IDs:", dedupe(in.Tactics), true)
	lines = section(lines, "Styleguide IDs:", dedupe(in.Styleguides), false)
	lines = section(lines, "Toolguide IDs:", dedupe(in.Toolguides), false)
	lines = section(lines, "Procedure IDs:", dedupe(in.Procedures), false)
	lines = section(lines, "Asset IDs:", dedupe(in.Assets), false)
	lines = section(lines, "Section Anchors:", SectionAnchors(string(md)), true)
	if len(diags) > 0 {
		lines = append(lines, "  - Diagnostics: "+strings.Join(diags, " | "))
	}
	text := strings.Join(lines, "\n")
	if in.Profile != nil {
		if pb := renderProfileSections(in.Profile); pb != "" {
			text += "\n\n" + pb
		}
	}
	return text
}

// renderProfileSections lists the profile-cited directives and tactics.
func renderProfileSections(p *dct.Profile) string {
	var lines []string
	refs, _ := p.Data.Get("directive-references")
	if l, ok := refs.([]any); ok && len(l) > 0 {
		lines = append(lines, "Profile-Cited Directives:")
		for _, x := range l {
			if o, ok := x.(*jsonx.Object); ok {
				lines = append(lines, fmt.Sprintf("  - %s: %s", dct.NormalizeDirectiveID(o.GetString("code")), o.GetString("name")))
			}
		}
	}
	trefs, _ := p.Data.Get("tactic-references")
	if l, ok := trefs.([]any); ok && len(l) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "Profile-Cited Tactics:")
		for _, x := range l {
			if o, ok := x.(*jsonx.Object); ok {
				lines = append(lines, "  - "+o.GetString("id"))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// Reference is one charter.yaml catalog.references entry.
type Reference struct {
	ID, Kind, Title, Summary, SourcePath, LocalPath string
}

// LoadReferences reads catalog.references from charter.yaml.
func LoadReferences(root string) []Reference {
	b := LoadBundle(root)
	if b == nil {
		return nil
	}
	c := b.GetObject("catalog")
	if c == nil {
		return nil
	}
	var out []Reference
	for _, x := range c.GetList("references") {
		o, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		out = append(out, Reference{ID: o.GetString("id"), Kind: o.GetString("kind"), Title: o.GetString("title"),
			Summary: o.GetString("summary"), SourcePath: o.GetString("source_path"), LocalPath: o.GetString("local_path")})
	}
	return out
}

// PolicySummary is upstream _extract_policy_summary.
func PolicySummary(content string) []string {
	lines := strings.Split(content, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## Policy Summary" {
			start = i
			break
		}
	}
	strip := func(s string) string { return strings.TrimSpace(strings.TrimLeft(s, "- ")) }
	var out []string
	if start < 0 {
		for _, l := range lines {
			if s := strings.TrimSpace(l); strings.HasPrefix(s, "-") {
				if v := strip(s); v != "" {
					out = append(out, v)
				}
			}
		}
		if len(out) > 8 {
			out = out[:8]
		}
		return out
	}
	for _, l := range lines[start+1:] {
		s := strings.TrimSpace(l)
		if strings.HasPrefix(s, "## ") {
			break
		}
		if strings.HasPrefix(s, "-") {
			out = append(out, strip(s))
		}
	}
	return out
}

func referencePath(r Reference) string {
	kind, id, _ := strings.Cut(r.ID, ":")
	k, err := dct.ParseKind(strings.ToLower(kind))
	if err == nil {
		if a := dct.NewOffering("", nil).Repo(k).Get(id); a != nil {
			return a.Path
		}
	}
	return r.LocalPath
}

// renderBootstrap is a simplified upstream _render_bootstrap_text.
func renderBootstrap(root, action string, b *ActionBundle, refs []Reference, profile *dct.Profile) string {
	mdPath := Path(root, CharterMD)
	content, _ := os.ReadFile(mdPath)
	lines := []string{"Charter Context (Bootstrap):", "  - Source: " + mdPath,
		"  - This is the first load for this action. Use the summary and follow references as needed.", "", "Policy Summary:"}
	sum := PolicySummary(string(content))
	if len(sum) == 0 {
		lines = append(lines, "  - "+noneLabel)
	}
	for _, s := range sum {
		lines = append(lines, "  - "+s)
	}
	if profile != nil {
		if pb := renderProfileSections(profile); pb != "" {
			lines = append(lines, "", pb)
		}
	}
	lines = append(lines, "", "Action Doctrine ("+action+"):")
	any := false
	if b != nil {
		for _, k := range []dct.Kind{dct.Directive, dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure} {
			ids := b.IDs[k]
			if len(ids) == 0 {
				continue
			}
			any = true
			lines = append(lines, "  "+strings.Title(strings.ReplaceAll(k.Plural(), "_", " "))+":")
			repo := b.Offer.Repo(k)
			for _, id := range ids {
				t := id
				if a := repo.Get(id); a != nil {
					t = id + ": " + a.Title()
				}
				lines = append(lines, "    - "+t)
			}
		}
	}
	if !any {
		lines = append(lines, "  - "+noneLabel)
	}
	lines = append(lines, "", "Reference Docs:")
	if len(refs) == 0 {
		lines = append(lines, "  - "+noneLabel)
	}
	for _, r := range refs {
		lines = append(lines, fmt.Sprintf("  - %s: %s (%s)", r.ID, r.Title, referencePath(r)))
	}
	return strings.Join(lines, "\n")
}

// readYAMLObject parses a YAML file into an object (nil on failure).
func readYAMLObject(p string) *jsonx.Object {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil {
		return nil
	}
	return o
}

func relPath(p, root string) string {
	r, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return p
	}
	return filepath.ToSlash(r)
}
