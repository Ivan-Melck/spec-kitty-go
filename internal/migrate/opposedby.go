package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rtyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// rewrite-opposed-by (migration/rewrite_opposed_by.py): rewrite a pack's
// legacy `opposed_by` entries into in_tension_with / rejects DRG edges.
//
// Deviation: the source artifact is edited textually (the top-level
// `opposed_by:` block is removed, every other byte kept) instead of a ruamel
// round-trip re-dump.

var artifactTypes = []string{"directive", "tactic", "paradigm"}

// RewrittenEntry is rewrite_opposed_by.RewrittenEntry.
type RewrittenEntry struct {
	SourceFile, SourceType, SourceID, TargetType, TargetID, Relation string
	Reason                                                           *string
	CreatedAntiPatternNode                                           bool
}

// UnclassifiableEntry is rewrite_opposed_by.UnclassifiableEntry.
type UnclassifiableEntry struct {
	SourceFile, SourceType, SourceID, TargetType, TargetID, Message string
}

// RewriteResult is rewrite_opposed_by.RewriteResult.
type RewriteResult struct {
	Rewritten      []*RewrittenEntry
	Unclassifiable []UnclassifiableEntry
}

type srcFile struct{ path, typ string }

func discoverArtifacts(pack string) []srcFile {
	var out []srcFile
	for _, t := range artifactTypes {
		var paths []string
		filepath.Walk(pack, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), "."+t+".yaml") {
				paths = append(paths, p)
			}
			return nil
		})
		sort.SliceStable(paths, func(i, j int) bool { return PyPathLess(paths[i], paths[j]) })
		for _, p := range paths {
			out = append(out, srcFile{p, t})
		}
	}
	return out
}

func loadYAMLObject(p string) (*jsonx.Object, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, err
	}
	o, _ := v.(*jsonx.Object)
	return o, nil
}

func reprEntry(v any) string { return pyjson.Repr(v) }

func classifyOpposed(srcType, srcID, srcFile string, entry any, registry map[[2]string]string) (*RewrittenEntry, *UnclassifiableEntry) {
	o, ok := entry.(*jsonx.Object)
	if !ok {
		return nil, &UnclassifiableEntry{srcFile, srcType, srcID, "", "", fmt.Sprintf("%s: opposed_by entry on %s:%s is not a mapping (%s); expected {type, id, reason}. Fix the entry manually and re-run `spec-kitty migrate rewrite-opposed-by`.", srcFile, srcType, srcID, reprEntry(entry))}
	}
	tt, _ := o.Get("type")
	tid, _ := o.Get("id")
	reasonV, _ := o.Get("reason")
	ttS, ttOK := tt.(string)
	tidS, tidOK := tid.(string)
	valid := ttOK && (ttS == "directive" || ttS == "tactic" || ttS == "paradigm")
	if !valid || !tidOK || tidS == "" {
		tidStr := ""
		if tid != nil {
			tidStr = pyjson.Str(tid)
		}
		return nil, &UnclassifiableEntry{srcFile, srcType, srcID, pyjson.Str(tt), tidStr, fmt.Sprintf("%s: opposed_by entry on %s:%s has an invalid or missing type/id (%s); expected 'type' in ['directive', 'paradigm', 'tactic'] and a non-empty 'id'. Fix the entry manually and re-run `spec-kitty migrate rewrite-opposed-by`.", srcFile, srcType, srcID, reprEntry(entry))}
	}
	_, declared := registry[[2]string{ttS, tidS}]
	conflict := ""
	for _, other := range artifactTypes {
		if other != ttS {
			if _, ok := registry[[2]string{other, tidS}]; ok {
				conflict = other
				break
			}
		}
	}
	if conflict != "" {
		var mid string
		if declared {
			mid = fmt.Sprintf("id %s also exists as a %s artifact in this pack", pyjson.ReprString(tidS), conflict)
		} else {
			mid = fmt.Sprintf("no %s artifact named %s exists, but a %s artifact with that id does", ttS, pyjson.ReprString(tidS), conflict)
		}
		return nil, &UnclassifiableEntry{srcFile, srcType, srcID, ttS, tidS, fmt.Sprintf("%s: opposed_by target %s:%s (from %s:%s) is ambiguous -- %s. Unclear whether this is a mistyped peer reference or a coincidentally-named anti-pattern -- resolve manually (fix the declared type, or rename one of the two artifacts) before re-running `spec-kitty migrate rewrite-opposed-by`.", srcFile, ttS, tidS, srcType, srcID, mid)}
	}
	rel := "rejects"
	if declared {
		rel = "in_tension_with"
	}
	var reason *string
	if s, ok := reasonV.(string); ok {
		reason = &s
	}
	return &RewrittenEntry{SourceFile: srcFile, SourceType: srcType, SourceID: srcID, TargetType: ttS, TargetID: tidS, Relation: rel, Reason: reason}, nil
}

// drgGraph is a loaded <kind>.graph.yaml document.
type drgGraph struct {
	doc   *jsonx.Object // schema_version, generated_at, generated_by
	nodes []*jsonx.Object
	edges []*jsonx.Object
}

func loadDRG(pack, typ string) (*drgGraph, error) {
	p := filepath.Join(pack, typ+".graph.yaml")
	if _, err := os.Stat(p); err != nil {
		return &drgGraph{doc: jsonx.NewObject("schema_version", "1.0", "generated_at", clock.NowUTCISO(), "generated_by", "rewrite-opposed-by-v1")}, nil
	}
	o, err := loadYAMLObject(p)
	if err != nil {
		return nil, err
	}
	g := &drgGraph{doc: &jsonx.Object{}}
	if o == nil {
		o = &jsonx.Object{}
	}
	for _, k := range []string{"schema_version", "generated_at", "generated_by"} {
		v, _ := o.Get(k)
		g.doc.Set(k, v)
	}
	for _, n := range o.GetList("nodes") {
		if no, ok := n.(*jsonx.Object); ok {
			g.nodes = append(g.nodes, no)
		}
	}
	for _, e := range o.GetList("edges") {
		if eo, ok := e.(*jsonx.Object); ok {
			g.edges = append(g.edges, eo)
		}
	}
	return g, nil
}

func strip(v any) any {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = strip(x)
		}
		return out
	}
	return v
}

func emptyField(v any, ok bool) bool {
	if !ok || v == nil {
		return true
	}
	if l, isList := v.([]any); isList && len(l) == 0 {
		return true
	}
	return false
}

func (g *drgGraph) render() string {
	doc := jsonx.NewObject()
	for _, kv := range g.doc.Pairs() {
		doc.Set(kv.Key, kv.Value)
	}
	nodes := []any{}
	for _, n := range g.nodes {
		r := &jsonx.Object{}
		for _, f := range []string{"urn", "kind", "label", "tags"} {
			v, ok := n.Get(f)
			if (f == "label" || f == "tags") && emptyField(v, ok) {
				continue
			}
			if f == "tags" && !ok {
				v = []any{}
			}
			r.Set(f, strip(v))
		}
		nodes = append(nodes, r)
	}
	edges := []any{}
	for _, e := range g.edges {
		r := &jsonx.Object{}
		for _, f := range []string{"source", "target", "relation", "when", "reason"} {
			v, ok := e.Get(f)
			if (f == "when" || f == "reason") && emptyField(v, ok) {
				continue
			}
			r.Set(f, strip(v))
		}
		edges = append(edges, r)
	}
	doc.Set("nodes", nodes)
	doc.Set("edges", edges)
	// _write_graph: yaml_rt.dump(payload) with width 4096
	if out, err := rtyaml.DumpWidth(doc, 4096); err == nil {
		return out
	}
	return yamlx.Emit(doc)
}

func (g *drgGraph) hasEdge(src, tgt, rel string) bool {
	for _, e := range g.edges {
		if e.GetString("source") == src && e.GetString("target") == tgt && e.GetString("relation") == rel {
			return true
		}
	}
	return false
}

// dropOpposedBy is `del data["opposed_by"]; yaml_rt.dump(data, fh)` on the
// round-trip load of the artifact (comments and styles kept, width 4096).
func dropOpposedBy(text string) string {
	doc, err := rtyaml.LoadString(text)
	if err == nil {
		if m, ok := doc.Root.(*rtyaml.Map); ok {
			m.Delete("opposed_by")
			if out, err := doc.DumpWidth(4096); err == nil {
				return out
			}
		}
	}
	return removeTopLevelKey(text, "opposed_by")
}

// removeTopLevelKey drops the `key:` block (and the lines attached to it)
// from a block-style YAML document.
func removeTopLevelKey(text, key string) string {
	lines := strings.SplitAfter(text, "\n")
	var out []string
	in := false
	for _, l := range lines {
		if in {
			if l == "" || l == "\n" || strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, "#") {
				continue
			}
			in = false
		}
		if strings.HasPrefix(l, key+":") {
			in = true
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "")
}

// RewriteOpposedBy is rewrite_opposed_by_pack(pack_root, dry_run).
func RewriteOpposedBy(pack string, dryRun bool) (*RewriteResult, error) {
	res := &RewriteResult{}
	registry := map[[2]string]string{}
	files := discoverArtifacts(pack)
	for _, f := range files {
		o, err := loadYAMLObject(f.path)
		if err != nil {
			warnf("Skipping unreadable artifact file %s: %v", f.path, err)
			continue
		}
		if o == nil {
			continue
		}
		if id, ok := o.Get("id"); ok {
			if s, ok := id.(string); ok && s != "" {
				registry[[2]string{f.typ, s}] = f.path
			}
		}
	}
	graphs := map[string]*drgGraph{}
	for _, t := range artifactTypes {
		g, err := loadDRG(pack, t)
		if err != nil {
			return nil, &UncaughtError{"ValidationError", err.Error()}
		}
		graphs[t] = g
	}
	touched := map[string]bool{}
	for _, f := range files {
		o, err := loadYAMLObject(f.path)
		if err != nil {
			return nil, &UncaughtError{"YAMLError", err.Error()}
		}
		if o == nil {
			continue
		}
		ob, _ := o.Get("opposed_by")
		if !pyjson.Truthy(ob) {
			continue
		}
		idV, _ := o.Get("id")
		srcID, ok := idV.(string)
		if !ok || srcID == "" {
			res.Unclassifiable = append(res.Unclassifiable, UnclassifiableEntry{f.path, f.typ, "", "", "", f.path + ": has an opposed_by block but no usable top-level 'id' field; cannot build the source URN for any edge. Fix the artifact's id and re-run `spec-kitty migrate rewrite-opposed-by`."})
			continue
		}
		list, _ := ob.([]any)
		var rewritten []*RewrittenEntry
		var bad []UnclassifiableEntry
		for _, e := range list {
			r, u := classifyOpposed(f.typ, srcID, f.path, e, registry)
			if u != nil {
				bad = append(bad, *u)
			} else {
				rewritten = append(rewritten, r)
			}
		}
		if len(bad) > 0 {
			res.Unclassifiable = append(res.Unclassifiable, bad...)
			continue
		}
		for _, r := range rewritten {
			srcURN := r.SourceType + ":" + r.SourceID
			var reason any
			if r.Reason != nil {
				reason = *r.Reason
			}
			if r.Relation == "rejects" {
				target := "anti_pattern:" + r.TargetID
				g := graphs[r.SourceType]
				found := false
				for _, n := range g.nodes {
					if n.GetString("urn") == target {
						found = true
					}
				}
				if !found {
					g.nodes = append(g.nodes, jsonx.NewObject("urn", target, "kind", "anti_pattern", "tags", []any{"anti-pattern"}))
					r.CreatedAntiPatternNode = true
				}
				if !g.hasEdge(srcURN, target, "rejects") {
					g.edges = append(g.edges, jsonx.NewObject("source", srcURN, "target", target, "relation", "rejects", "reason", reason))
				}
				touched[r.SourceType] = true
				continue
			}
			tgtURN := r.TargetType + ":" + r.TargetID
			es, et := srcURN, tgtURN
			if srcURN > tgtURN {
				es, et = tgtURN, srcURN
			}
			kind := strings.SplitN(es, ":", 2)[0]
			g := graphs[kind]
			if !g.hasEdge(es, et, "in_tension_with") {
				g.edges = append(g.edges, jsonx.NewObject("source", es, "target", et, "relation", "in_tension_with", "reason", reason))
			}
			touched[kind] = true
		}
		res.Rewritten = append(res.Rewritten, rewritten...)
		if !dryRun && len(rewritten) > 0 {
			b, _ := os.ReadFile(f.path)
			os.WriteFile(f.path, []byte(dropOpposedBy(string(b))), 0o644)
		}
	}
	if !dryRun {
		var kinds []string
		for k := range touched {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			os.WriteFile(filepath.Join(pack, k+".graph.yaml"), []byte(graphs[k].render()), 0o644)
		}
	}
	return res, nil
}
