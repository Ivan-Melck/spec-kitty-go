package missionstate

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/rtyaml"
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
	doc, err := rtyaml.LoadString(strings.Join(lines[1:closing], "\n"))
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
	if doc.Root == nil {
		return ""
	}
	if m, ok := doc.Root.(*rtyaml.Map); ok && !m.IsSet {
		return ""
	}
	return fmt.Sprintf("Frontmatter is not a mapping in %s: parsed as %s", path, frontmatterTypeName(doc.Root))
}

// FrontmatterManager uses the round-trip loader with preserve_quotes=False.
// Its diagnostic names retain scalar formats, block styles and YAML tags.
func frontmatterTypeName(v any) string {
	switch x := v.(type) {
	case *rtyaml.Seq:
		return "CommentedSeq"
	case *rtyaml.Map:
		return "CommentedSet"
	case *rtyaml.Str:
		switch x.Style {
		case '|':
			return "LiteralScalarString"
		case '>':
			return "FoldedScalarString"
		}
		if x.Anchor != nil {
			return "PlainScalarString"
		}
		return "str"
	case *rtyaml.Int:
		return map[rtyaml.IntKind]string{rtyaml.IntScalar: "ScalarInt", rtyaml.IntBinary: "BinaryInt", rtyaml.IntOctal: "OctalInt", rtyaml.IntHex: "HexInt", rtyaml.IntHexCaps: "HexCapsInt"}[x.Kind]
	case *rtyaml.Float:
		return "ScalarFloat"
	case *rtyaml.Bool:
		return "ScalarBoolean"
	case rtyaml.Date, *rtyaml.Date:
		return "date"
	case *rtyaml.TimeStamp:
		if x.Plain {
			return "datetime"
		}
		return "TimeStamp"
	case *rtyaml.Tagged:
		return "TaggedScalar"
	case []byte:
		return "bytes"
	}
	return pyjson.TypeName(v)
}
