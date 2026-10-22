package charter

import (
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// topSpan is one top-level mapping entry of a YAML document's source text.
type topSpan struct {
	key  string
	text string // full source lines of the entry (including trailing newline)
}

// splitTopLevel splits a block-mapping document into its top-level entries
// (comments / blank lines before an entry belong to that entry; a leading
// preamble is returned separately).
func splitTopLevel(src string) (string, []topSpan) {
	lines := strings.SplitAfter(src, "\n")
	var pre strings.Builder
	var spans []topSpan
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
				pre.WriteString(pending.String())
				pending.Reset()
			}
			spans = append(spans, topSpan{key: key, text: pending.String() + l})
			pending.Reset()
			continue
		}
		if len(spans) == 0 {
			pending.WriteString(l)
			continue
		}
		// continuation (indented / sequence item / comment inside the entry)
		if trim == "" || trim[0] == '#' {
			pending.WriteString(l)
			continue
		}
		spans[len(spans)-1].text += pending.String() + l
		pending.Reset()
	}
	if len(spans) > 0 {
		spans[len(spans)-1].text += pending.String()
	} else {
		pre.WriteString(pending.String())
	}
	return pre.String(), spans
}

func emitEntry(key string, v any, width int) string {
	return yamlx.EmitWith(jsonx.NewObject(key, v), yamlx.Options{Width: width})
}

func valuesEqual(a, b any) bool {
	return jsonx.Dumps(a, jsonx.Line) == jsonx.Dumps(b, jsonx.Line)
}

// RenderYAMLDocument is charter_yaml_io.render_yaml_document: top-level
// entries whose value is unchanged keep their source text; changed entries
// are re-emitted in place, new entries are appended, removed entries dropped.
func RenderYAMLDocument(before []byte, doc *jsonx.Object, width int) string {
	if before == nil {
		if doc.Len() == 0 {
			return "{}\n"
		}
		return yamlx.EmitWith(doc, yamlx.Options{Width: width})
	}
	orig, _ := yamlx.ParseObject(before)
	if orig != nil && valuesEqual(orig, doc) {
		return string(before)
	}
	pre, spans := splitTopLevel(string(before))
	if orig == nil || len(spans) == 0 {
		return pre + yamlx.EmitWith(doc, yamlx.Options{Width: width})
	}
	var b strings.Builder
	b.WriteString(pre)
	seen := map[string]bool{}
	for _, sp := range spans {
		seen[sp.key] = true
		nv, ok := doc.Get(sp.key)
		if !ok {
			continue
		}
		ov, _ := orig.Get(sp.key)
		if valuesEqual(ov, nv) {
			text := sp.text
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			b.WriteString(text)
			continue
		}
		b.WriteString(emitEntry(sp.key, nv, width))
	}
	for _, kv := range doc.Pairs() {
		if !seen[kv.Key] {
			b.WriteString(emitEntry(kv.Key, kv.Value, width))
		}
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
