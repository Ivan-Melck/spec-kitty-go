package status

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// specsRoot is _SlugResolver._find_mission_specs_root.
func specsRoot(featureDir string) string {
	c := filepath.Dir(featureDir)
	if filepath.Base(c) == "kitty-specs" {
		return c
	}
	if two := filepath.Dir(c); filepath.Base(two) == "kitty-specs" {
		return two
	}
	return c
}

// SlugResolverWarnings returns the logger.warning lines one upstream
// read_events() pass emits while back-filling mission_id for legacy lane
// rows (store._SlugResolver): an unsafe slug, an unreadable meta.json, or
// an orphaned event whose sibling mission has no meta.json. Each slug warns
// at most once per pass.
func SlugResolverWarnings(featureDir string) []string {
	rows, _, err := readRowsWithLines(featureDir)
	if err != nil {
		return nil
	}
	return slugResolverWarnings(featureDir, rows)
}

// SlugWarnLog receives the _SlugResolver logger.warning records of every
// read_events pass (ReadStream). The CLI logging bootstrap installs a
// stderr sink in human mode; nil (--json, tests) discards them.
var SlugWarnLog func(msg string)

// quietReads > 0 suppresses SlugWarnLog (reads with no upstream
// read_events counterpart).
var quietReads int

// Quietly runs fn with ReadStream's slug-resolver logging suppressed.
func Quietly(fn func()) {
	quietReads++
	defer func() { quietReads-- }()
	fn()
}

// ExtraReadPasses logs the slug-resolver warnings of n read_events passes
// upstream makes over featureDir's log where the Go command reads it fewer
// times (a command-level read the port folds into one).
func ExtraReadPasses(featureDir string, n int) {
	if SlugWarnLog == nil || quietReads > 0 || n <= 0 {
		return
	}
	rows, _, err := readRowsWithLines(featureDir)
	if err != nil {
		return
	}
	for i := 0; i < n; i++ {
		logSlugWarnings(featureDir, rows)
	}
}

func logSlugWarnings(featureDir string, rows []*jsonx.Object) {
	if SlugWarnLog == nil || quietReads > 0 {
		return
	}
	for _, w := range slugResolverWarnings(featureDir, rows) {
		SlugWarnLog(w)
	}
}

func slugResolverWarnings(featureDir string, rows []*jsonx.Object) []string {
	root := specsRoot(featureDir)
	seen := map[string]bool{}
	var out []string
	for _, o := range rows {
		if k, ok := o.Get("kind"); ok && k != nil {
			continue
		}
		if IsNonLaneRow(o) {
			continue
		}
		if v, _ := o.Get("mission_id"); truthyJSON(v) {
			continue
		}
		slug := ""
		for _, k := range []string{"mission_slug", "feature_slug"} {
			if v, _ := o.Get(k); truthyJSON(v) {
				slug = PyStr(v)
				break
			}
		}
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		if reason := mission.UnsafeSegmentReason(slug); reason != "" {
			out = append(out, fmt.Sprintf("Refusing to resolve unsafe mission_slug %s (traversal guard); mission_id will be None: %s", pyjson.ReprString(slug), reason))
			continue
		}
		metaPath := filepath.Join(root, slug, mission.MetaFile)
		if !contained(root, metaPath) {
			continue
		}
		if _, err := os.Stat(metaPath); err != nil {
			out = append(out, fmt.Sprintf("No meta.json found for mission_slug %s (orphaned event); mission_id will be None for these events", pyjson.ReprString(slug)))
			continue
		}
		if _, err := mission.ReadMeta(filepath.Dir(metaPath)); err != nil {
			out = append(out, fmt.Sprintf("Could not read meta.json for slug %s: %v", pyjson.ReprString(slug), err))
		}
	}
	return out
}

func truthyJSON(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case *jsonx.Object:
		return x.Len() > 0
	case []any:
		return len(x) > 0
	}
	return true
}

func contained(root, p string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		r = root
	}
	d, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		d = filepath.Dir(p)
	}
	rel, err := filepath.Rel(r, d)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
