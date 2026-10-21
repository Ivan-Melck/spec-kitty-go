package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

var safeSegmentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// UnsafeSegmentReason returns assert_safe_path_segment's error text for v,
// or "" when v is a safe single path segment.
func UnsafeSegmentReason(v string) string {
	s := strings.TrimSpace(v)
	r := pyjson.ReprString(v)
	switch {
	case s == "":
		return fmt.Sprintf("Not a safe path segment: %s — value must not be empty or whitespace-only.", r)
	case s != v:
		return fmt.Sprintf("Not a safe path segment: %s — value must not contain leading or trailing whitespace.", r)
	case strings.Contains(s, ".."):
		return fmt.Sprintf("Not a safe path segment: %s — value must not contain '..' (traversal guard).", r)
	case strings.HasPrefix(s, "."):
		return fmt.Sprintf("Not a safe path segment: %s — value must not begin with '.' (traversal guard).", r)
	case strings.ContainsAny(s, `/\`):
		return fmt.Sprintf("Not a safe path segment: %s — value must not contain path separators.", r)
	case !safeSegmentRe.MatchString(s):
		return fmt.Sprintf("Not a safe path segment: %s — value must match the canonical segment grammar "+
			"(ASCII alphanumerics, hyphens, underscores, and interior dots only; must begin with an alphanumeric character).", r)
	}
	return ""
}

// ResolveError is a failed mission resolution with its envelope code.
type ResolveError struct {
	Code    string
	Message string
	Data    *jsonx.Object
}

func (e *ResolveError) Error() string { return e.Message }

// MissionNotFoundMessage is the shared genuine-not-found message.
func MissionNotFoundMessage(handle string) string {
	return fmt.Sprintf("Mission '%s' not found in kitty-specs/", handle)
}

// Mission is a resolved mission: its canonical directory name, the primary
// (planning) directory and the coordination-aware status directory.
type Mission struct {
	Root       string
	Slug       string // kitty-specs directory name
	PrimaryDir string
	StatusDir  string
}

// ResolveMission is _resolve_mission_dir_or_fail's resolution: the traversal
// guard (INVALID_MISSION), the handle resolver, and the existence check
// (MISSION_NOT_FOUND).
func ResolveMission(root, handle string) (*Mission, error) {
	if reason := UnsafeSegmentReason(handle); reason != "" {
		return nil, &ResolveError{Code: "INVALID_MISSION",
			Message: fmt.Sprintf("Mission slug is not a safe path segment: %s", pyjson.ReprString(handle)),
			Data:    jsonx.NewObject("reason", reason, "mission_slug", handle)}
	}
	slug, err := mission.ResolveHandle(root, handle)
	if err != nil {
		var amb *mission.SelectorAmbiguousError
		if errors.As(err, &amb) {
			return nil, &ResolveError{Code: "MISSION_NOT_FOUND", Message: amb.Error()}
		}
		return nil, &ResolveError{Code: "MISSION_NOT_FOUND", Message: MissionNotFoundMessage(handle)}
	}
	m := &Mission{Root: root, Slug: slug, PrimaryDir: project.Specs(root, slug)}
	m.StatusDir = coord.Resolve(root, slug).StatusDir()
	if !isDir(m.StatusDir) {
		if !isDir(m.PrimaryDir) {
			return nil, &ResolveError{Code: "MISSION_NOT_FOUND", Message: MissionNotFoundMessage(handle)}
		}
		m.StatusDir = m.PrimaryDir
	}
	return m, nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Identity is _mission_identity_payload(dir): mission_slug, mission_number
// and mission_type read from dir/meta.json (absent meta degrades to the
// directory name, null and "").
func Identity(dir string) *jsonx.Object {
	meta := readMeta(dir)
	slug := filepath.Base(dir)
	raw := meta.GetString("mission_slug")
	if raw == "" {
		raw = meta.GetString("slug")
	}
	if raw != "" && UnsafeSegmentReason(raw) == "" {
		slug = raw
	}
	mt := meta.GetString("mission_type")
	return jsonx.NewObject("mission_slug", slug, "mission_number", coerceNumber(meta), "mission_type", mt)
}

func readMeta(dir string) *jsonx.Object {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return jsonx.NewObject()
	}
	v, err := pyjson.LoadsBytes(b)
	if err != nil {
		return jsonx.NewObject()
	}
	if o, ok := v.(*jsonx.Object); ok {
		return o
	}
	return jsonx.NewObject()
}

func coerceNumber(meta *jsonx.Object) any {
	v, ok := meta.Get("mission_number")
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case bool:
		return nil
	case int:
		return t
	case json.Number:
		return t
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return nil
}

// WPIDPrefix extracts the canonical WP id from a task filename stem.
var WPIDPrefix = regexp.MustCompile(`^(WP\d+)`)

// ResolveWPFile locates tasks/<wp>.md, else the first tasks/<wp>-*.md.
func ResolveWPFile(tasksDir, wp string) string {
	exact := filepath.Join(tasksDir, wp+".md")
	if st, err := os.Stat(exact); err == nil && !st.IsDir() {
		return exact
	}
	matches, _ := filepath.Glob(filepath.Join(tasksDir, globEscape(wp)+"-*.md"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func globEscape(s string) string {
	return strings.NewReplacer(`[`, `\[`, `*`, `\*`, `?`, `\?`, `\`, `\\`).Replace(s)
}
