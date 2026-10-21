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
	var blocks []string
	profileBlock := RenderProfileSections(root, in.Profile)
	if profileBlock != "" {
		blocks = append(blocks, profileBlock)
	}
	if len(blocks) == 0 {
		return text
	}
	return enforceTokenBudget(text+"\n\n"+strings.Join(blocks, "\n\n"), in.Action, profileBlock, "")
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
