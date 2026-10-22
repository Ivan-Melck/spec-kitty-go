package agents

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pytoml"
)

// vibeConfigRel is the project-local Mistral Vibe config.
const vibeConfigRel = ".vibe/config.toml"

// VibeSkillPathConfigured is vibe_config.skill_path_configured: whether a
// top-level skill_paths (string or list) of .vibe/config.toml names
// ".agents/skills". Unreadable or invalid TOML counts as not configured.
func VibeSkillPathConfigured(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(vibeConfigRel)))
	if err != nil {
		return false
	}
	if !utf8.Valid(b) {
		return false
	}
	raw := string(b)
	data := &jsonx.Object{}
	if strings.TrimSpace(raw) != "" {
		if data, err = pytoml.Loads(raw); err != nil {
			return false
		}
	}
	switch v := mustGet(data, "skill_paths").(type) {
	case string:
		return v == VibeSkillPath
	case []any:
		for _, x := range v {
			if tomlStr(x) == VibeSkillPath {
				return true
			}
		}
	}
	return false
}

func mustGet(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

// tomlStr is str(value) for the scalar values a skill_paths list can hold.
func tomlStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// PreparedVibeConfig is vibe_config.PreparedVibeConfig: the current and
// desired bytes of .vibe/config.toml (Before == nil when absent).
type PreparedVibeConfig struct {
	Rel     string
	Exists  bool
	Before  []byte
	Mode    int // -1 when absent
	Desired []byte
}

// Changed is PreparedPresenceFile.changed.
func (p PreparedVibeConfig) Changed() bool {
	return !p.Exists || !bytes.Equal(p.Before, p.Desired)
}

// PrepareVibeSkillPath is vibe_config.prepare_project_skill_path: the owned
// top-level skill_paths entry is added without re-serializing unrelated TOML.
func PrepareVibeSkillPath(root string) (PreparedVibeConfig, error) {
	p := PreparedVibeConfig{Rel: vibeConfigRel, Mode: -1}
	target := filepath.Join(root, filepath.FromSlash(vibeConfigRel))
	if st, err := os.Lstat(target); err == nil {
		if !st.Mode().IsRegular() {
			return p, errors.New("Native config destination is not a regular file")
		}
		b, err := os.ReadFile(target)
		if err != nil {
			return p, err
		}
		p.Exists, p.Before, p.Mode = true, b, int(st.Mode().Perm())
	}
	if !utf8.Valid(p.Before) {
		return p, errors.New("'utf-8' codec can't decode bytes")
	}
	text := string(p.Before)
	data, err := pytoml.Loads(text)
	if err != nil {
		return p, err
	}
	paths, has := data.Get("skill_paths")
	if has {
		ok := false
		switch v := paths.(type) {
		case string:
			ok = true
		case []any:
			ok = true
			for _, x := range v {
				if _, isStr := x.(string); !isStr {
					ok = false
				}
			}
		}
		if !ok {
			return p, errors.New("Expected .vibe/config.toml skill_paths to be a string or list")
		}
	}
	if s, isStr := paths.(string); isStr && s == VibeSkillPath {
		p.Desired = p.Before
		return p, nil
	}
	if l, isList := paths.([]any); isList {
		for _, x := range l {
			if x == VibeSkillPath {
				p.Desired = p.Before
				return p, nil
			}
		}
	}
	desired, err := addSkillPath(text, paths, has)
	if err != nil {
		return p, err
	}
	parsed, err := pytoml.Loads(desired)
	if err != nil {
		return p, err
	}
	expected := data.Clone()
	var want []any
	switch v := paths.(type) {
	case string:
		want = []any{v}
	case []any:
		want = append(want, v...)
	}
	expected.Set("skill_paths", append(want, VibeSkillPath))
	if !sameTOML(parsed, expected) {
		return p, errors.New("Native config preparation changed an unowned TOML key")
	}
	p.Desired = []byte(desired)
	return p, nil
}

func sameTOML(a, b any) bool {
	switch x := a.(type) {
	case *jsonx.Object:
		y, ok := b.(*jsonx.Object)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for _, kv := range x.Pairs() {
			w, ok := y.Get(kv.Key)
			if !ok || !sameTOML(kv.Value, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameTOML(x[i], y[i]) {
				return false
			}
		}
		return true
	case float64:
		y, ok := b.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(x) {
			return math.IsNaN(y)
		}
		return x == y
	}
	return jsonx.Dumps(a, jsonx.Line) == jsonx.Dumps(b, jsonx.Line) && sameKind(a, b)
}

func sameKind(a, b any) bool {
	switch a.(type) {
	case string:
		_, ok := b.(string)
		return ok
	case bool:
		_, ok := b.(bool)
		return ok
	case int, int64:
		switch b.(type) {
		case int, int64:
			return true
		}
		return false
	}
	return true
}

// splitLinesKeepends is str.splitlines(keepends=True).
func splitLinesKeepends(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	pos := 0 // byte offset
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		w := utf8.RuneLen(r)
		switch r {
		case '\r':
			end := pos + w
			if i+1 < len(rs) && rs[i+1] == '\n' {
				end++
				i++
			}
			out = append(out, s[start:end])
			start = end
			pos = end
			continue
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', ' ', ' ':
			out = append(out, s[start:pos+w])
			start = pos + w
		}
		pos += w
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func addSkillPath(text string, paths any, has bool) (string, error) {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	if !has || paths == nil {
		return `skill_paths = [".agents/skills"]` + newline + text, nil
	}
	lines := splitLinesKeepends(text)
	offset, index := 0, 0
	for index < len(lines) {
		statement := lines[index]
		start := offset
		index++
		offset += len(statement)
		trim := strings.TrimSpace(statement)
		left := strings.TrimLeft(statement, " \t\n\r\v\f")
		if trim == "" || strings.HasPrefix(left, "#") {
			continue
		}
		if strings.HasPrefix(left, "[") {
			break
		}
		var parsed *jsonx.Object
		for {
			var err error
			parsed, err = pytoml.Loads(statement)
			if err == nil {
				break
			}
			if index == len(lines) {
				return "", err
			}
			statement += lines[index]
			offset += len(lines[index])
			index++
		}
		if !parsed.Has("skill_paths") {
			continue
		}
		valueStart := strings.Index(statement, "=") + 1
		for valueStart < len(statement) && (statement[valueStart] == ' ' || statement[valueStart] == '\t') {
			valueStart++
		}
		valueEnd, err := tomlValueEnd(statement, valueStart)
		if err != nil {
			return "", err
		}
		var value string
		if l, isList := paths.([]any); isList {
			insert := valueEnd - 1
			prefix := statement[valueStart:insert]
			suffix := `".agents/skills"]`
			if len(l) > 0 {
				suffix = " " + suffix
			}
			value = prefix + suffix
			if _, err := pytoml.Loads(statement[:valueStart] + value + statement[valueEnd:]); err != nil {
				value = prefix + "," + suffix
			}
		} else {
			value = jsonx.Dumps([]any{paths, VibeSkillPath}, jsonx.Options{Indent: -1})
		}
		return text[:start+valueStart] + value + text[start+valueEnd:], nil
	}
	return "", errors.New("Cannot locate top-level skill_paths entry")
}

// tomlValueEnd is vibe_config._toml_value_end.
func tomlValueEnd(text string, start int) (int, error) {
	quote := ""
	depth := 0
	i := start
	for i < len(text) {
		ch := text[i]
		if quote != "" {
			if quote[0] == '"' && ch == '\\' {
				i += 2
				continue
			}
			if strings.HasPrefix(text[i:], quote) {
				i += len(quote)
				if len(quote) == 3 {
					for k := 0; k < 2; k++ {
						if i < len(text) && text[i] == quote[0] {
							i++
						}
					}
				}
				quote = ""
				if depth == 0 {
					return i, nil
				}
				continue
			}
		} else if ch == '\'' || ch == '"' {
			q := string(ch)
			if strings.HasPrefix(text[i:], q+q+q) {
				q = q + q + q
			}
			quote = q
			i += len(q)
			continue
		} else if ch == '#' {
			end := strings.Index(text[i:], "\n")
			if end < 0 {
				i = len(text)
			} else {
				i += end
			}
			continue
		} else if ch == '[' {
			depth++
		} else if ch == ']' {
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		}
		i++
	}
	return 0, errors.New("Unterminated skill_paths value")
}

// EnsureVibeSkillPath is vibe_config.ensure_project_skill_path.
func EnsureVibeSkillPath(root string) error {
	p, err := PrepareVibeSkillPath(root)
	if err != nil {
		return err
	}
	if !p.Changed() {
		return nil
	}
	target := filepath.Join(root, filepath.FromSlash(p.Rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return writePresenceFile(target, p.Desired)
}
