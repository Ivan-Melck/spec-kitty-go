package missionstate

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// FrontmatterReadError is str(FrontmatterError) that
// FrontmatterManager().read(path) raises for a markdown file (missing file,
// no frontmatter, no closing fence, invalid YAML, duplicate keys, a
// non-mapping document), or "" when the frontmatter reads.
func FrontmatterReadError(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("File not found: %s", path)
		}
		return pyjson.OSErrorString(err)
	}
	content, err := pyjson.DecodeUTF8(b) // utf-8-sig
	if err != nil {
		return err.Error()
	}
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---") {
		return fmt.Sprintf("File has no frontmatter: %s", path)
	}
	lines := strings.Split(content, "\n")
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r\n\v\f") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return fmt.Sprintf("Malformed frontmatter (no closing ---): %s", path)
	}
	v, err := yamlx.Parse([]byte(strings.Join(lines[1:closing], "\n")))
	if err != nil {
		if pyyaml.ClassName(err) == "DuplicateKeyError" {
			if order, dups := scanOccurrences(lines, frontmatterClosing(lines)); len(order) > 0 {
				var parts []string
				for _, k := range order {
					var ls []string
					for _, o := range dups[k] {
						ls = append(ls, fmt.Sprint(o.idx+1))
					}
					parts = append(parts, fmt.Sprintf("'%s' (lines %s)", k, strings.Join(ls, ", ")))
				}
				return fmt.Sprintf("Duplicate frontmatter key(s) in %s: %s", path, strings.Join(parts, "; "))
			}
			return fmt.Sprintf("Duplicate frontmatter key in %s: %v", path, err)
		}
		return fmt.Sprintf("Invalid YAML in %s: %v", path, err)
	}
	if v == nil {
		return ""
	}
	if _, ok := v.(*jsonx.Object); !ok {
		typeName := pyjson.TypeName(v)
		if _, ok := v.([]any); ok {
			typeName = "CommentedSeq"
		}
		return fmt.Sprintf("Frontmatter is not a mapping in %s: parsed as %s", path, typeName)
	}
	return ""
}
