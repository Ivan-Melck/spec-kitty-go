package fm

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// FlowKeys lists the top-level frontmatter keys whose value was authored as
// a flow sequence ("[a, b]") in raw; ruamel's round-trip dump keeps that
// style when the value is written back unchanged.
func FlowKeys(raw string) map[string]bool {
	out := map[string]bool{}
	src, _, ok := Split(raw)
	if !ok {
		return out
	}
	var n yaml.Node
	if yaml.Unmarshal([]byte(src), &n) != nil || len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
		return out
	}
	m := n.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		v := m.Content[i+1]
		if v.Kind == yaml.SequenceNode && v.Style&yaml.FlowStyle != 0 {
			out[m.Content[i].Value] = true
		}
	}
	return out
}

// RestoreFlow rewrites the block sequences of plain scalars under keys in a
// rendered frontmatter document back to flow style (skip names keys whose
// value the caller replaced).
func RestoreFlow(text string, keys, skip map[string]bool) string {
	if len(keys) == 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return text
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return text
	}
	out := []string{lines[0]}
	for i := 1; i < end; i++ {
		l := lines[i]
		key := strings.TrimSuffix(l, ":")
		if key != l && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "-") && keys[key] && !skip[key] {
			var items []string
			j := i + 1
			simple := true
			for ; j < end && strings.HasPrefix(lines[j], "- "); j++ {
				it := lines[j][2:]
				if strings.ContainsAny(it, ",[]{}") && !strings.HasPrefix(it, "'") {
					simple = false
				}
				items = append(items, it)
			}
			if j < end && (strings.HasPrefix(lines[j], " ") || strings.HasPrefix(lines[j], "-")) {
				simple = false
			}
			if simple && len(items) > 0 {
				out = append(out, key+": ["+strings.Join(items, ", ")+"]")
				i = j - 1
				continue
			}
		}
		out = append(out, l)
	}
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}
