// Package wp reads and writes work package prompt files
// (kitty-specs/<mission>/tasks/WPnn-<slug>.md): discovery, frontmatter
// (fm.WPFieldOrder), subtasks and dependencies.
package wp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// IDPattern is a work package id (WP + 2 or more digits).
var IDPattern = regexp.MustCompile(`^WP\d{2,}$`)

var fileIDRe = regexp.MustCompile(`^(WP\d+)`)

// WP is a parsed work package file.
type WP struct {
	ID   string
	Path string
	Doc  *fm.Doc
	// Raw is the original file text.
	Raw string
}

// TasksDir returns <missionDir>/tasks.
func TasksDir(missionDir string) string { return filepath.Join(missionDir, "tasks") }

func idRegexp(id string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(id) + `(?:[-_.]|\.md$)`)
}

// Match returns every tasks/*.md file (README excluded) whose name matches id.
func Match(missionDir, id string) []string {
	tasks := TasksDir(missionDir)
	entries, err := os.ReadDir(tasks)
	if err != nil {
		return nil
	}
	re := idRegexp(id)
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".md") || strings.ToLower(n) == "readme.md" {
			continue
		}
		if re.MatchString(n) {
			out = append(out, filepath.Join(tasks, n))
		}
	}
	sort.Strings(out)
	return out
}

// Find returns the single file for id ("" when none; error when ambiguous).
func Find(missionDir, id string) (string, error) {
	m := Match(missionDir, id)
	if len(m) > 1 {
		var names []string
		for _, p := range m {
			names = append(names, filepath.Base(p))
		}
		return "", fmt.Errorf("ambiguous match (%d files in %s: %s)", len(m), TasksDir(missionDir), strings.Join(names, ", "))
	}
	if len(m) == 0 {
		return "", nil
	}
	return m[0], nil
}

// Files lists WP markdown files in tasks/ (sorted by name, README excluded,
// only names starting with WP<digits>).
func Files(missionDir string) []string {
	entries, err := os.ReadDir(TasksDir(missionDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".md") || strings.ToLower(n) == "readme.md" {
			continue
		}
		if fileIDRe.MatchString(n) {
			out = append(out, filepath.Join(TasksDir(missionDir), n))
		}
	}
	sort.Strings(out)
	return out
}

// IDFromFilename extracts "WPnn" from a WP file name.
func IDFromFilename(name string) string {
	if m := fileIDRe.FindStringSubmatch(filepath.Base(name)); m != nil {
		return m[1]
	}
	return ""
}

// Read parses a WP file. The id comes from work_package_id, falling back to
// the filename.
func Read(path string) (*WP, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(b), "\ufeff")
	doc, err := fm.Parse(text)
	if err != nil {
		return nil, err
	}
	w := &WP{Path: path, Doc: doc, Raw: text}
	w.ID = strings.TrimSpace(doc.Meta.GetString("work_package_id"))
	if w.ID == "" {
		w.ID = IDFromFilename(path)
	}
	return w, nil
}

// Meta returns the frontmatter object.
func (w *WP) Meta() *jsonx.Object { return w.Doc.Meta }

// Stem is the file name without ".md".
func (w *WP) Stem() string { return strings.TrimSuffix(filepath.Base(w.Path), ".md") }

// Title returns title (legacy work_package_title fallback).
func (w *WP) Title() string {
	if t := scalarString(w.Doc.Meta, "title"); t != "" {
		return t
	}
	return scalarString(w.Doc.Meta, "work_package_title")
}

func scalarString(o *jsonx.Object, k string) string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case int:
		return fmt.Sprint(t)
	case float64:
		return jsonx.FormatFloat(t)
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(v)
}

// String returns a frontmatter scalar as a string ("" when absent).
func (w *WP) String(k string) string { return scalarString(w.Doc.Meta, k) }

// List returns a frontmatter list of strings (nil when absent).
func (w *WP) List(k string) []string { return StringList(w.Doc.Meta, k) }

// StringList reads a list-of-strings key (scalar → single item).
func StringList(o *jsonx.Object, k string) []string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []any:
		out := []string{}
		for _, x := range t {
			out = append(out, scalarString(jsonx.NewObject("v", x), "v"))
		}
		return out
	case string:
		return []string{t}
	}
	return nil
}

var depSplitRe = regexp.MustCompile(`[,\s]+`)

// Dependencies returns declared dependencies, coercing legacy string forms
// ("[]", "WP01, WP02", bare "WP01").
func (w *WP) Dependencies() []string { return CoerceDependencies(w.Doc.Meta) }

// CoerceDependencies implements coerce_legacy_dependencies on a frontmatter.
func CoerceDependencies(o *jsonx.Object) []string {
	v, ok := o.Get("dependencies")
	if !ok || v == nil {
		return []string{}
	}
	switch t := v.(type) {
	case []any:
		out := []string{}
		for _, x := range t {
			s := strings.TrimSpace(scalarString(jsonx.NewObject("v", x), "v"))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		s := strings.TrimSpace(t)
		s = strings.TrimPrefix(s, "[")
		s = strings.TrimSuffix(s, "]")
		out := []string{}
		for _, p := range depSplitRe.Split(s, -1) {
			p = strings.Trim(strings.TrimSpace(p), `"'`)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return []string{}
}

// Subtasks returns the authored subtask roster (string ids).
func (w *WP) Subtasks() []string {
	v, ok := w.Doc.Meta.Get("subtasks")
	if !ok || v == nil {
		return nil
	}
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range l {
		switch t := x.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				out = append(out, s)
			}
		case *jsonx.Object:
			if id := scalarString(t, "id"); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// Write renders the document with WP_FIELD_ORDER and writes it atomically.
func (w *WP) Write() error {
	text := fm.Render(w.Doc, fm.WPFieldOrder)
	return fm.WriteFileAtomic(w.Path, []byte(text), 0o644)
}

// LoadAll reads every WP file in the mission (parse errors are returned
// alongside the successfully parsed ones).
func LoadAll(missionDir string) ([]*WP, []error) {
	var out []*WP
	var errs []error
	for _, p := range Files(missionDir) {
		w, err := Read(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(p), err))
			continue
		}
		out = append(out, w)
	}
	return out, errs
}
