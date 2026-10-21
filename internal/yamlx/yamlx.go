// Package yamlx parses YAML into insertion-ordered values (*jsonx.Object,
// []any, scalars) and emits block-style YAML matching the upstream
// ruamel.yaml configuration (indent mapping=2, sequence=2, offset=0,
// width=4096, single-quoted strings only when required).
package yamlx

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"gopkg.in/yaml.v3"
)

// Parse decodes a YAML document into ordered values. Timestamps are kept as
// their source strings.
func Parse(src []byte) (any, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(src, &n); err != nil {
		return nil, err
	}
	if n.Kind == 0 {
		return nil, nil
	}
	return convert(&n)
}

// ParseObject decodes a YAML mapping (nil document -> empty object).
func ParseObject(src []byte) (*jsonx.Object, error) {
	v, err := Parse(src)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return &jsonx.Object{}, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("yaml document is not a mapping")
	}
	return o, nil
}

func convert(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return convert(n.Content[0])
	case yaml.AliasNode:
		return convert(n.Alias)
	case yaml.MappingNode:
		o := &jsonx.Object{}
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind == yaml.ScalarNode && k.Tag == "!!merge" {
				v, err := convert(n.Content[i+1])
				if err != nil {
					return nil, err
				}
				if m, ok := v.(*jsonx.Object); ok {
					for _, kv := range m.Pairs() {
						if !o.Has(kv.Key) {
							o.Set(kv.Key, kv.Value)
						}
					}
				}
				continue
			}
			key := k.Value
			if seen[key] {
				return nil, fmt.Errorf("line %d: duplicate key %q", k.Line, key)
			}
			seen[key] = true
			v, err := convert(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			o.Set(key, v)
		}
		return o, nil
	case yaml.SequenceNode:
		l := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := convert(c)
			if err != nil {
				return nil, err
			}
			l = append(l, v)
		}
		return l, nil
	case yaml.ScalarNode:
		return scalar(n)
	}
	return nil, fmt.Errorf("unsupported yaml node kind %v", n.Kind)
}

func scalar(n *yaml.Node) (any, error) {
	switch n.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return n.Value, nil
		}
		return b, nil
	case "!!int":
		var i int64
		if err := n.Decode(&i); err != nil {
			return n.Value, nil
		}
		return int(i), nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return n.Value, nil
		}
		return f, nil
	}
	return n.Value, nil
}

// Options controls emission.
type Options struct {
	// SequenceIndent is the indentation of "- " under a mapping key
	// (0 = ruamel offset 0, the upstream frontmatter style).
	SequenceIndent int
	// NullAsNull renders None as "null" (PyYAML safe_dump) instead of an
	// empty value (ruamel round-trip).
	NullAsNull bool
}

// Emit renders v (normally *jsonx.Object) as block YAML.
func Emit(v any) string { return EmitWith(v, Options{}) }

// EmitWith renders v with options.
func EmitWith(v any, opts Options) string {
	var b strings.Builder
	e := emitter{b: &b, opts: opts}
	switch t := v.(type) {
	case *jsonx.Object:
		if t.Len() == 0 {
			return "{}\n"
		}
		e.mapping(t, 0)
	case []any:
		if len(t) == 0 {
			return "[]\n"
		}
		e.sequence(t, 0)
	default:
		b.WriteString(FormatScalar(v))
		b.WriteString("\n")
	}
	return b.String()
}

type emitter struct {
	b    *strings.Builder
	opts Options
}

func (e *emitter) pad(n int) { e.b.WriteString(strings.Repeat(" ", n)) }

func (e *emitter) mapping(o *jsonx.Object, indent int) {
	for _, kv := range o.Pairs() {
		e.pad(indent)
		e.b.WriteString(formatKey(kv.Key))
		e.b.WriteString(":")
		e.value(kv.Value, indent, false)
	}
}

// value writes the value after "key:" or "- ".
func (e *emitter) value(v any, indent int, inSeq bool) {
	switch t := v.(type) {
	case *jsonx.Object:
		if t.Len() == 0 {
			e.b.WriteString(" {}\n")
			return
		}
		e.b.WriteString("\n")
		e.mapping(t, indent+2)
	case []any:
		if len(t) == 0 {
			e.b.WriteString(" []\n")
			return
		}
		e.b.WriteString("\n")
		e.sequence(t, indent+e.opts.SequenceIndent)
	case []string:
		l := make([]any, len(t))
		for i, s := range t {
			l[i] = s
		}
		e.value(l, indent, inSeq)
	default:
		s := FormatScalar(v)
		if v == nil && e.opts.NullAsNull {
			s = "null"
		}
		if s == "" {
			e.b.WriteString("\n")
			return
		}
		e.b.WriteString(" ")
		e.b.WriteString(s)
		e.b.WriteString("\n")
	}
}

func (e *emitter) sequence(l []any, indent int) {
	for _, item := range l {
		e.pad(indent)
		e.b.WriteString("-")
		switch t := item.(type) {
		case *jsonx.Object:
			if t.Len() == 0 {
				e.b.WriteString(" {}\n")
				continue
			}
			// first key on the dash line, rest aligned under it
			for i, kv := range t.Pairs() {
				if i == 0 {
					e.b.WriteString(" ")
				} else {
					e.pad(indent + 2)
				}
				e.b.WriteString(formatKey(kv.Key))
				e.b.WriteString(":")
				e.value(kv.Value, indent+2, false)
			}
		case []any:
			if len(t) == 0 {
				e.b.WriteString(" []\n")
				continue
			}
			// "- - a" compact nested sequence
			var sub strings.Builder
			se := emitter{b: &sub, opts: e.opts}
			se.sequence(t, indent+2)
			e.b.WriteString(" ")
			e.b.WriteString(strings.TrimLeft(sub.String(), " "))
		default:
			e.value(item, indent, true)
		}
	}
}

func blockLiteral(s string, indent int) string {
	var b strings.Builder
	chomp := "-"
	body := s
	if strings.HasSuffix(s, "\n") {
		chomp = ""
		body = strings.TrimSuffix(s, "\n")
		if strings.HasSuffix(body, "\n") {
			chomp = "+"
		}
	}
	b.WriteString("|" + chomp + "\n")
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(strings.Repeat(" ", indent))
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func formatKey(k string) string {
	if needsDouble(k) {
		return doubleQuote(k)
	}
	if needsQuote(k) && !(len(k) == 1 && strings.ContainsAny(k, "yYnN")) {
		return singleQuote(k)
	}
	return k
}

// FormatScalar renders a scalar value.
func FormatScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if math.IsInf(t, 1) {
			return ".inf"
		}
		if math.IsInf(t, -1) {
			return "-.inf"
		}
		if math.IsNaN(t) {
			return ".nan"
		}
		if t == math.Trunc(t) && math.Abs(t) < 1e16 {
			return strconv.FormatFloat(t, 'f', 1, 64)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case string:
		if needsDouble(t) {
			return doubleQuote(t)
		}
		if needsQuote(t) {
			return singleQuote(t)
		}
		return t
	}
	return fmt.Sprint(v)
}

func needsDouble(s string) bool {
	if strings.HasPrefix(s, "'") {
		return true
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func doubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func singleQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

var (
	reInt       = regexp.MustCompile(`^[-+]?(0|[1-9][0-9_]*)$|^0o?[0-7_]+$|^0x[0-9a-fA-F_]+$|^0b[01_]+$`)
	reFloat     = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9][0-9_]*(\.[0-9_]*)?)([eE][-+]?[0-9]+)?$|^[-+]?\.(inf|Inf|INF)$|^\.(nan|NaN|NAN)$`)
	reTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}([Tt ]|$)`)
	reBool      = regexp.MustCompile(`^(true|True|TRUE|false|False|FALSE)$`)
	reNull      = regexp.MustCompile(`^(null|Null|NULL|~)$`)
)

func needsQuote(s string) bool {
	if s == "" || s == "=" || s == "<<" {
		return true
	}
	if reNull.MatchString(s) || reBool.MatchString(s) || reInt.MatchString(s) || reFloat.MatchString(s) || reTimestamp.MatchString(s) {
		return true
	}
	if strings.TrimSpace(s) != s {
		return true
	}
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
		if (s[0] == '-' || s[0] == '?' || s[0] == ':') && len(s) > 1 && s[1] != ' ' {
			// "-foo" is plain-safe in block context
			if s[0] == '-' && s == "---" {
				return true
			}
			break
		}
		return true
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") {
		return true
	}
	for _, r := range s {
		if r < 0x20 && r != '\t' {
			return true
		}
	}
	return false
}
