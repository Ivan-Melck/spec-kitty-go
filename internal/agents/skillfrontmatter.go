package agents

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

var specificationSkillFields = map[string]bool{"name": true, "description": true, "license": true, "compatibility": true, "metadata": true, "allowed-tools": true}
var nativeSkillFields = []string{"user-invocable", "argument-hint", "disable-model-invocation"}
var frontmatterBlock = regexp.MustCompile(`(?s)\A---\n(.*?\n)?---(?:\n|$)`)
var frontmatterKey = regexp.MustCompile(`^([^\s#-][^:]*?)\s*:(?:\s|$)`)
var plainFrontmatterKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type frontmatterChunk struct {
	key   string
	lines []string
}

func IsSharedSkillRoot(root string) bool {
	switch strings.Trim(strings.ReplaceAll(root, `\`, "/"), "/") {
	case ".claude/skills", ".qwen/skills", ".kilocode/skills":
		return false
	}
	return true
}

func metadataString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case nil:
		return ""
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return jsonx.FormatFloat(v)
	case int:
		return fmt.Sprint(v)
	}
	return jsonx.Dumps(v, jsonx.Options{Indent: -1, SortKeys: true})
}

func skillDoubleQuoted(text string) string {
	quoted := jsonx.Dumps(text, jsonx.Options{Indent: -1})
	var out strings.Builder
	for _, r := range quoted {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 || (r >= 0xd800 && r <= 0xdfff) || r == 0xfffe || r == 0xffff {
			fmt.Fprintf(&out, `\u%04x`, r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func metadataChunk(entries *jsonx.Object) frontmatterChunk {
	c := frontmatterChunk{key: "metadata", lines: []string{"metadata:"}}
	for _, p := range entries.Pairs() {
		key := p.Key
		if !plainFrontmatterKey.MatchString(key) {
			key = skillDoubleQuoted(key)
		}
		c.lines = append(c.lines, "  "+key+": "+skillDoubleQuoted(metadataString(p.Value)))
	}
	return c
}

func chunkValue(c frontmatterChunk) (any, error) {
	o, e := yamlx.ParseObject([]byte(strings.Join(c.lines, "\n")))
	if e != nil {
		return nil, fmt.Errorf("SKILL.md frontmatter key %s is not valid YAML: %v", skillPythonRepr(c.key), e)
	}
	if o == nil || o.Len() != 1 {
		return nil, fmt.Errorf("SKILL.md frontmatter key %s is not a single mapping entry", skillPythonRepr(c.key))
	}
	return o.Pairs()[0].Value, nil
}

func skillPythonRepr(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

func chunkMetadata(c frontmatterChunk) (*jsonx.Object, error) {
	v, e := chunkValue(c)
	if e != nil {
		return nil, e
	}
	if v == nil {
		return &jsonx.Object{}, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("SKILL.md frontmatter 'metadata' must be a mapping")
	}
	return o, nil
}

// ProjectSkillForRoot preserves source chunks and comments unless a field
// moves. The destination root decides the form, so shared roots have identical
// bytes for every agent that consumes them.
func ProjectSkillForRoot(content, root, skillName string) (string, error) {
	content = EnsureSkillFrontmatter(content, skillName)
	m := frontmatterBlock.FindStringSubmatchIndex(content)
	if m == nil {
		return content, nil
	}
	inner := ""
	if m[2] >= 0 {
		inner = content[m[2]:m[3]]
	}
	chunks := []frontmatterChunk{{}}
	for _, line := range SplitLines(inner) {
		match := frontmatterKey.FindStringSubmatch(line)
		if len(match) == 0 {
			chunks[len(chunks)-1].lines = append(chunks[len(chunks)-1].lines, line)
		} else {
			key := strings.Trim(strings.TrimSpace(match[1]), `"'`)
			chunks = append(chunks, frontmatterChunk{key, []string{line}})
		}
	}
	var projected []frontmatterChunk
	if IsSharedSkillRoot(root) {
		moved := &jsonx.Object{}
		for _, c := range chunks {
			if c.key != "" && !specificationSkillFields[c.key] {
				v, e := chunkValue(c)
				if e != nil {
					return "", e
				}
				moved.Set(c.key, v)
			} else {
				projected = append(projected, c)
			}
		}
		if moved.Len() == 0 {
			return content, nil
		}
		found := false
		for i, c := range projected {
			if c.key == "metadata" {
				existing, e := chunkMetadata(c)
				if e != nil {
					return "", e
				}
				for _, p := range existing.Pairs() {
					moved.Set(p.Key, p.Value)
				}
				projected[i] = metadataChunk(moved)
				found = true
				break
			}
		}
		if !found {
			projected = append(projected, metadataChunk(moved))
		}
	} else {
		index := -1
		present := map[string]bool{}
		for i, c := range chunks {
			present[c.key] = true
			if c.key == "metadata" && index < 0 {
				index = i
			}
		}
		if index < 0 {
			return content, nil
		}
		entries, e := chunkMetadata(chunks[index])
		if e != nil {
			return "", e
		}
		var lifted []frontmatterChunk
		for _, key := range nativeSkillFields {
			v, ok := entries.Get(key)
			if !ok || present[key] {
				continue
			}
			text := ""
			if s, ok := v.(string); ok && (s == "true" || s == "false") {
				text = s
			} else {
				text = skillScalarValue(v)
			}
			lifted = append(lifted, frontmatterChunk{key, []string{key + ": " + text}})
			entries.Delete(key)
		}
		if len(lifted) == 0 {
			return content, nil
		}
		projected = append(projected, chunks[:index]...)
		if entries.Len() > 0 {
			projected = append(projected, metadataChunk(entries))
		}
		projected = append(projected, lifted...)
		projected = append(projected, chunks[index+1:]...)
	}
	lines := []string{"---"}
	for _, c := range projected {
		lines = append(lines, c.lines...)
	}
	lines = append(lines, "---")
	// The regex consumes the fence's newline; keep that separator exactly.
	end := m[1]
	if end > 0 && content[end-1] == '\n' {
		end--
	}
	return strings.Join(lines, "\n") + content[end:], nil
}

func skillScalarValue(v any) string {
	switch v := v.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return jsonx.FormatFloat(v)
	case int:
		return fmt.Sprint(v)
	}
	if v == nil {
		return skillYAMLScalar("None")
	}
	return skillYAMLScalar(fmt.Sprint(v))
}
