package charter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/glossary"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports glossary.entity_pages (GlossaryEntityPageRenderer): the
// regenerable Markdown entity page per glossary term, written to
// .kittify/charter/compiled/glossary/<term-urn-slug>.md.

// ErrTermNotFound is entity_pages.TermNotFoundError.
var ErrTermNotFound = errors.New("term not found")

// loadEntityDRG is entity_pages._load_merged_drg: the first project pack
// root candidate (graph.yaml, merged_drg.json, drg.json, compiled_drg.json)
// that validates as a DRGGraph; nil otherwise.
func loadEntityDRG(root string) *jsonx.Object {
	dir := filepath.Join(root, ".kittify", "charter-packs")
	for _, name := range []string{"graph.yaml", "merged_drg.json", "drg.json", "compiled_drg.json"} {
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var v any
		if strings.HasSuffix(name, ".yaml") {
			v, err = yamlx.Parse(b)
		} else {
			v, err = pyjson.LoadsBytes(b)
		}
		if err != nil || len(drgGraphErrors(v)) > 0 {
			return nil
		}
		return v.(*jsonx.Object)
	}
	return nil
}

type backlink struct{ sourceID, sourceType, label string }

// GenerateEntityPage is GlossaryEntityPageRenderer.generate_one: it writes
// the page and returns its path, or ErrTermNotFound.
func GenerateEntityPage(root, termID string) (string, error) {
	g := loadEntityDRG(root)
	if g == nil {
		return "", ErrTermNotFound
	}
	nodes := map[string]*jsonx.Object{}
	for _, x := range g.GetList("nodes") {
		n := x.(*jsonx.Object)
		if _, dup := nodes[n.GetString("urn")]; !dup {
			nodes[n.GetString("urn")] = n
		}
	}
	index := map[string][]backlink{}
	for _, x := range g.GetList("edges") {
		e := x.(*jsonx.Object)
		if e.GetString("relation") != "vocabulary" || !strings.HasPrefix(e.GetString("target"), "glossary:") {
			continue
		}
		src := e.GetString("source")
		n := nodes[src]
		typ := ""
		if n != nil {
			switch n.GetString("kind") {
			case "agent_profile":
				typ = "other"
			case "action":
				typ = "mission_step"
			}
		}
		if typ == "" {
			prefix := ""
			if i := strings.Index(src, ":"); i >= 0 {
				prefix = src[:i]
			}
			typ = map[string]string{"wp": "wp", "adr": "adr", "step": "mission_step", "action": "mission_step", "retro": "retro_finding", "charter": "charter_section"}[prefix]
			if typ == "" {
				typ = "other"
			}
		}
		label := src
		if n != nil && n.GetString("label") != "" {
			label = n.GetString("label")
		}
		index[e.GetString("target")] = append(index[e.GetString("target")], backlink{src, typ, label})
	}
	for _, x := range g.GetList("nodes") {
		n := x.(*jsonx.Object)
		urn := n.GetString("urn")
		if !strings.HasPrefix(urn, "glossary:") || urn != termID {
			continue
		}
		label := n.GetString("label")
		def := label
		if def == "" {
			def = "_No definition recorded._"
		}
		display := label
		if display == "" {
			display = strings.SplitN(urn, ":", 2)[1]
		}
		lines := []string{"# " + display, "", "**ID**: `" + urn + "`", "", "## Definition", "", def, ""}
		if p := n.GetString("provenance"); p != "" {
			lines = append(lines, "## Provenance", "", "First introduced: "+p, "")
		}
		if bls := index[urn]; len(bls) > 0 {
			lines = append(lines, "## References", "")
			var order []string
			groups := map[string][]backlink{}
			for _, bl := range bls {
				if _, ok := groups[bl.sourceType]; !ok {
					order = append(order, bl.sourceType)
				}
				groups[bl.sourceType] = append(groups[bl.sourceType], bl)
			}
			typeLabels := map[string]string{"wp": "Work Packages", "adr": "ADRs", "mission_step": "Mission Steps", "retro_finding": "Retrospective Findings", "charter_section": "Charter Sections", "other": "Other"}
			for _, t := range order {
				lines = append(lines, "### "+typeLabels[t], "")
				for _, bl := range groups[t] {
					lines = append(lines, "- "+bl.label+" (`"+bl.sourceID+"`)")
				}
				lines = append(lines, "")
			}
		}
		var hist [][4]string
		for _, c := range glossary.IterSemanticConflictRecords(root) {
			if c.TermID != urn {
				continue
			}
			res := c.Resolution
			if res == "" {
				res = "unresolved"
			}
			hist = append(hist, [4]string{c.Timestamp, c.Severity, c.ConflictType, res})
		}
		if len(hist) > 0 {
			lines = append(lines, "## Conflict History", "", "| Timestamp | Severity | Type | Resolution |", "|-----------|----------|------|------------|")
			for _, h := range hist {
				lines = append(lines, "| "+h[0]+" | "+h[1]+" | "+h[2]+" | "+h[3]+" |")
			}
			lines = append(lines, "")
		}
		lines = append(lines, "---", "*Regenerated automatically. Do not edit manually.*")
		out := filepath.Join(root, ".kittify", "charter", "compiled", "glossary")
		if err := os.MkdirAll(out, 0o755); err != nil {
			return "", err
		}
		slug := strings.ReplaceAll(strings.ReplaceAll(urn, ":", "-"), "/", "-")
		final := filepath.Join(out, slug+".md")
		tmp := strings.TrimSuffix(final, ".md") + ".md.tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return "", err
		}
		return final, os.Rename(tmp, final)
	}
	return "", ErrTermNotFound
}
