// Package fm reads and writes Markdown files with YAML frontmatter in the
// upstream FrontmatterManager format: "---\n" + yaml + "---\n" + body.
package fm

import (
	"bytes"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// ErrNoFrontmatter is returned when a document lacks a leading --- fence.
var ErrNoFrontmatter = errors.New("no frontmatter found")

// Doc is a parsed frontmatter document.
type Doc struct {
	Meta *jsonx.Object
	Body string // everything after the closing fence line, verbatim
}

// Split separates raw text into the YAML source and body. ok=false when there
// is no frontmatter.
func Split(text string) (yamlSrc, body string, ok bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(text, "---") {
		return "", text, false
	}
	first := strings.IndexByte(text, '\n')
	if first < 0 || strings.TrimRight(text[:first], " \t\r") != "---" {
		return "", text, false
	}
	rest := text[first+1:]
	pos := 0
	for pos <= len(rest) {
		end := strings.IndexByte(rest[pos:], '\n')
		var line string
		if end < 0 {
			line = rest[pos:]
		} else {
			line = rest[pos : pos+end]
		}
		if strings.TrimRight(line, " \t\r") == "---" {
			yamlSrc = rest[:pos]
			if end < 0 {
				body = ""
			} else {
				body = rest[pos+end+1:]
			}
			return yamlSrc, body, true
		}
		if end < 0 {
			break
		}
		pos += end + 1
	}
	return "", text, false
}

// Parse parses a frontmatter document.
func Parse(text string) (*Doc, error) {
	src, body, ok := Split(text)
	if !ok {
		return nil, ErrNoFrontmatter
	}
	meta, err := yamlx.ParseObject([]byte(src))
	if err != nil {
		return nil, err
	}
	return &Doc{Meta: meta, Body: body}, nil
}

// Read parses the file at path.
func Read(path string) (*Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(b))
}

// Render serialises a document. When order is non-nil, keys are emitted in
// that order first and the remaining keys alphabetically (the WP_FIELD_ORDER
// convention); with a nil order the insertion order is kept.
func Render(d *Doc, order []string) string {
	meta := d.Meta
	if order != nil {
		meta = Reorder(meta, order)
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	if meta.Len() > 0 {
		b.WriteString(yamlx.Emit(meta))
	}
	b.WriteString("---\n")
	b.WriteString(d.Body)
	return b.String()
}

// Reorder returns a copy of o with keys in order, then the rest sorted.
func Reorder(o *jsonx.Object, order []string) *jsonx.Object {
	out := &jsonx.Object{}
	for _, k := range order {
		if v, ok := o.Get(k); ok {
			out.Set(k, v)
		}
	}
	var rest []string
	for _, k := range o.Keys() {
		if !out.Has(k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		v, _ := o.Get(k)
		out.Set(k, v)
	}
	return out
}

// WPFieldOrder is upstream's WP_FIELD_ORDER.
var WPFieldOrder = []string{
	"work_package_id", "title", "dependencies", "requirement_refs",
	"planning_base_branch", "merge_target_branch", "branch_strategy",
	"base_branch", "base_commit", "created_at", "subtasks", "phase",
	"assignee", "agent", "shell_pid", "shell_pid_created_at", "scope", "history",
}

// Write atomically writes the document to path.
func Write(path string, d *Doc, order []string) error {
	return WriteFileAtomic(path, []byte(Render(d, order)), 0o644)
}

// WriteFileAtomic writes via a temp file + rename in the same directory.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := dirOf(path)
	f, err := os.CreateTemp(dir, ".tmp-"+baseOf(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	os.Chmod(tmp, perm)
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		if i == 0 {
			return "/"
		}
		return p[:i]
	}
	return "."
}

func baseOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
