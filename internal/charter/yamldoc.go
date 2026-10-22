package charter

import (
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/rtyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// topSpan is one top-level mapping entry of a YAML document's source text.
type topSpan struct {
	key  string
	lead string // comment / blank lines before the entry (not part of its span)
	body string // the entry's own lines (key line, continuations, inner comments)
}

// splitTopLevel splits a block-mapping document into its top-level entries.
// Comment and blank lines between entries are the next entry's lead (they
// stay in place when the entry is replaced, like upstream's key-indicator
// spans); those after the last entry are returned as tail.
func splitTopLevel(src string) (pre string, spans []topSpan, tail string) {
	lines := strings.SplitAfter(src, "\n")
	var b strings.Builder
	var pending strings.Builder
	for _, l := range lines {
		if l == "" {
			continue
		}
		trim := strings.TrimRight(l, "\r\n")
		isKey := trim != "" && trim[0] != ' ' && trim[0] != '#' && trim[0] != '-' && trim != "---" && strings.Contains(trim, ":")
		if isKey {
			key := trim[:strings.Index(trim, ":")]
			if o, err := yamlx.ParseObject([]byte(trim + "\n")); err == nil && o != nil && o.Len() == 1 {
				key = o.Keys()[0]
			} else if strings.HasPrefix(key, "'") || strings.HasPrefix(key, "\"") {
				key = strings.Trim(key, "'\"")
			}
			if len(spans) == 0 {
				b.WriteString(pending.String())
				pending.Reset()
			}
			spans = append(spans, topSpan{key: key, lead: pending.String(), body: l})
			pending.Reset()
			continue
		}
		if len(spans) == 0 {
			pending.WriteString(l)
			continue
		}
		if trim == "" || trim[0] == '#' {
			pending.WriteString(l)
			continue
		}
		// continuation: comments seen inside the entry belong to it
		spans[len(spans)-1].body += pending.String() + l
		pending.Reset()
	}
	if len(spans) == 0 {
		b.WriteString(pending.String())
		return b.String(), nil, ""
	}
	return b.String(), spans, pending.String()
}

func valuesEqual(a, b any) bool {
	return jsonx.Dumps(a, jsonx.Line) == jsonx.Dumps(b, jsonx.Line)
}

// dumpPlain is _dump_document / _dump_fragment of plain values with a fresh
// YAML() instance (round-trip emitter at width).
func dumpPlain(doc *jsonx.Object, width int) string {
	if out, err := rtyaml.DumpWidth(doc, width); err == nil {
		return out
	}
	return yamlx.EmitWith(doc, yamlx.Options{Width: width})
}

// renderedSpans renders doc over before through the ruamel round trip
// (YAML(); load; edit the CommentedMap; dump): the replacement text of each
// changed top-level entry, keeping the comments ruamel attaches to that key
// (its line comment and the comment lines inside it). ok is false when the
// round trip is unavailable.
func renderedSpans(before []byte, orig, doc *jsonx.Object, width int) (map[string]string, bool) {
	d, err := rtyaml.Load(pyyaml.StringInput(string(before)))
	if err != nil {
		return nil, false
	}
	d.Apply(doc, rtyaml.ContentSnapshot(orig))
	out, err := d.DumpWidth(width)
	if err != nil {
		return nil, false
	}
	_, spans, _ := splitTopLevel(out)
	m := map[string]string{}
	for _, sp := range spans {
		m[sp.key] = sp.body
	}
	return m, true
}

// RenderYAMLDocument is charter_yaml_io.render_yaml_document: top-level
// entries whose value is unchanged keep their source text; changed entries
// are replaced by their round-trip rendering (the comments around the
// entry stay), new entries are appended, removed entries dropped.
func RenderYAMLDocument(before []byte, doc *jsonx.Object, width int) string {
	if before == nil {
		if doc.Len() == 0 {
			return "{}\n"
		}
		return dumpPlain(doc, width)
	}
	orig, _ := yamlx.ParseObject(before)
	if orig != nil && valuesEqual(orig, doc) {
		return string(before)
	}
	pre, spans, tail := splitTopLevel(string(before))
	if orig == nil || len(spans) == 0 {
		return pre + dumpPlain(doc, width)
	}
	rendered, rtOK := renderedSpans(before, orig, doc, width)
	var b strings.Builder
	b.WriteString(pre)
	seen := map[string]bool{}
	for _, sp := range spans {
		seen[sp.key] = true
		b.WriteString(sp.lead)
		nv, ok := doc.Get(sp.key)
		if !ok {
			continue
		}
		ov, _ := orig.Get(sp.key)
		if valuesEqual(ov, nv) {
			text := sp.body
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			b.WriteString(text)
			continue
		}
		if r, ok := rendered[sp.key]; rtOK && ok {
			b.WriteString(r)
			continue
		}
		b.WriteString(dumpPlain(jsonx.NewObject(sp.key, nv), width))
	}
	b.WriteString(tail)
	additions := &jsonx.Object{}
	for _, kv := range doc.Pairs() {
		if !seen[kv.Key] {
			additions.Set(kv.Key, kv.Value)
		}
	}
	if additions.Len() > 0 {
		s := b.String()
		if s != "" && !strings.HasSuffix(s, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(dumpPlain(additions, width))
	}
	return b.String()
}

// writeYAMLDocument renders doc over path's current bytes and writes it.
func writeYAMLDocument(path string, doc *jsonx.Object, width int) error {
	before, err := os.ReadFile(path)
	if err != nil {
		before = nil
	}
	out := RenderYAMLDocument(before, doc, width)
	if before != nil && out == string(before) {
		return nil
	}
	// charter_yaml_io.apply_yaml_write writes the target in place (O_TRUNC),
	// not via a temp file + rename.
	return os.WriteFile(path, []byte(out), 0o644)
}
