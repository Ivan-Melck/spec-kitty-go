package tracker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pytoml"
	"math/big"
)

// ParseTOML decodes a TOML document exactly like tomllib.loads
// (internal/pytoml): *jsonx.Object, []any, string, int64 (*big.Int),
// float64, bool, pytoml.Date/Time/DateTime; errors are TOMLDecodeError text.
func ParseTOML(src string) (*jsonx.Object, error) {
	o, err := pytoml.Loads(src)
	if err != nil {
		return nil, err
	}
	return o, nil
}

// DumpTOML renders v like the python `toml` package's toml.dumps.
func DumpTOML(o *jsonx.Object) (string, error) {
	var ret strings.Builder
	add, sections, err := dumpSections(o, "")
	if err != nil {
		return "", err
	}
	ret.WriteString(add)
	for sections.Len() > 0 {
		next := &jsonx.Object{}
		for _, kv := range sections.Pairs() {
			add, sub, err := dumpSections(kv.Value.(*jsonx.Object), kv.Key)
			if err != nil {
				return "", err
			}
			if add != "" || sub.Len() == 0 {
				s := ret.String()
				if s != "" && !strings.HasSuffix(s, "\n\n") {
					ret.WriteString("\n")
				}
				ret.WriteString("[" + kv.Key + "]\n")
				ret.WriteString(add)
			}
			for _, skv := range sub.Pairs() {
				next.Set(kv.Key+"."+skv.Key, skv.Value)
			}
		}
		sections = next
	}
	return ret.String(), nil
}

var tomlBareSectionRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func dumpSections(o *jsonx.Object, sup string) (string, *jsonx.Object, error) {
	var ret, arrays strings.Builder
	if sup != "" && !strings.HasSuffix(sup, ".") {
		sup += "."
	}
	tables := &jsonx.Object{}
	for _, kv := range o.Pairs() {
		q := kv.Key
		if !tomlBareSectionRe.MatchString(q) {
			s, err := dumpTOMLStr(q)
			if err != nil {
				return "", nil, err
			}
			q = s
		}
		if sub, ok := kv.Value.(*jsonx.Object); ok {
			tables.Set(q, sub)
			continue
		}
		if l, ok := kv.Value.([]any); ok && hasTable(l) {
			for _, a := range l {
				at, _ := a.(*jsonx.Object)
				if at == nil {
					at = &jsonx.Object{}
				}
				arrTab := "\n"
				arrays.WriteString("[[" + sup + q + "]]\n")
				s, d, err := dumpSections(at, sup+q)
				if err != nil {
					return "", nil, err
				}
				if s != "" {
					if s[0] == '[' {
						arrTab += s
					} else {
						arrays.WriteString(s)
					}
				}
				for d.Len() > 0 {
					nd := &jsonx.Object{}
					for _, dkv := range d.Pairs() {
						s1, d1, err := dumpSections(dkv.Value.(*jsonx.Object), sup+q+"."+dkv.Key)
						if err != nil {
							return "", nil, err
						}
						if s1 != "" {
							arrTab += "[" + sup + q + "." + dkv.Key + "]\n" + s1
						}
						for _, k1 := range d1.Pairs() {
							nd.Set(dkv.Key+"."+k1.Key, k1.Value)
						}
					}
					d = nd
				}
				arrays.WriteString(arrTab)
			}
			continue
		}
		if kv.Value == nil {
			continue
		}
		s, err := dumpTOMLValue(kv.Value)
		if err != nil {
			return "", nil, err
		}
		ret.WriteString(q + " = " + s + "\n")
	}
	ret.WriteString(arrays.String())
	return ret.String(), tables, nil
}

func hasTable(l []any) bool {
	for _, a := range l {
		if _, ok := a.(*jsonx.Object); ok {
			return true
		}
	}
	return false
}

func dumpTOMLValue(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return dumpTOMLStr(t)
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case int:
		return strconv.Itoa(t), nil
	case float64:
		s := pyjson.ReprFloat(t)
		s = strings.ReplaceAll(s, "e+0", "e+")
		return strings.ReplaceAll(s, "e-0", "e-"), nil
	case pytoml.DateTime:
		// datetime.isoformat().replace('+00:00', 'Z')
		return strings.ReplaceAll(t.ISO(), "+00:00", "Z"), nil
	case pytoml.Date:
		return t.ISO(), nil
	case pytoml.Time:
		return t.ISO(), nil
	case *big.Int:
		return t.String(), nil
	case []any:
		var b strings.Builder
		b.WriteString("[")
		for _, u := range t {
			s, err := dumpTOMLValue(u)
			if err != nil {
				return "", err
			}
			b.WriteString(" " + s + ",")
		}
		b.WriteString("]")
		return b.String(), nil
	case *jsonx.Object:
		// Only reachable inside arrays: python's toml treats a dict as an
		// iterable of its keys.
		l := make([]any, 0, t.Len())
		for _, k := range t.Keys() {
			l = append(l, k)
		}
		return dumpTOMLValue(l)
	}
	return dumpTOMLStr(pyjson.Str(v))
}

// dumpTOMLStr ports toml.encoder._dump_str (including its quirks).
func dumpTOMLStr(s string) (string, error) {
	v := pyjson.ReprString(s)
	single := strings.HasPrefix(v, "'")
	if single || strings.HasPrefix(v, `"`) {
		v = v[1 : len(v)-1]
	}
	if single {
		v = strings.ReplaceAll(v, `\'`, "'")
		v = strings.ReplaceAll(v, `"`, `\"`)
	}
	parts := strings.Split(v, `\x`)
	for len(parts) > 1 {
		if parts[0] == "" {
			parts = parts[1:]
		}
		parts[0] = strings.ReplaceAll(parts[0], `\\`, `\`)
		p0 := parts[0]
		if p0 == "" || len(parts) < 2 {
			return "", fmt.Errorf("list index out of range")
		}
		i := -1
		joinx := p0[len(p0)-1] != '\\'
		for len(p0) > -i && p0[len(p0)+i] == '\\' {
			joinx = !joinx
			i--
		}
		joiner := "u00"
		if joinx {
			joiner = "x"
		}
		parts = append([]string{p0 + joiner + parts[1]}, parts[2:]...)
	}
	return `"` + parts[0] + `"`, nil
}
