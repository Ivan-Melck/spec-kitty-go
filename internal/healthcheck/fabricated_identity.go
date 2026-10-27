package healthcheck

// FABRICATED_IDENTITY_SUSPECTED (audit/classifiers/fabricated_identity.py,
// #6668): a committed meta.json a baseline writer fabricated. A folder is
// suspected only when all three signals hold: meta.json is git-tracked and the
// folder holds nothing outside {meta.json, status.json, status.events.jsonl,
// decisions/**, *.lock}; its slug-derived fields equal the baseline of the
// slug; and the slug's mid8 differs from the declared one. Read-only.

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/mission"
)

const fabricatedIdentityDetail = "signals matched: tracked meta.json in an allow-listed folder; slug-derived fields equal the baseline; " +
	"slug mid8 differs from mission_id. To fix: remove this folder's meta.json and status.json in a commit"

// baselineSlugFields is mission_metadata.baseline_slug_fields.
func baselineSlugFields(slug string) [][2]string {
	number, tail, _ := strings.Cut(slug, "-")
	isDigit := number != ""
	for _, r := range number {
		isDigit = isDigit && r >= '0' && r <= '9'
	}
	if !isDigit {
		number = ""
	}
	friendly := strings.TrimSpace(strings.ReplaceAll(tail, "-", " "))
	if friendly == "" {
		friendly = slug
	}
	return [][2]string{{"mission_number", number}, {"slug", slug}, {"mission_slug", slug}, {"friendly_name", friendly}}
}

func fabricatedAllowed(rel string) bool {
	parts := strings.Split(rel, "/")
	if parts[0] == "decisions" && len(parts) > 1 {
		return true
	}
	if len(parts) != 1 {
		return false
	}
	switch parts[0] {
	case "meta.json", "status.json", "status.events.jsonl":
		return true
	}
	ok, _ := path.Match("*.lock", parts[0])
	return ok
}

func signalTrackedAllowlist(dir, root string, tracked map[string]bool) bool {
	if tracked == nil {
		return false
	}
	rel, err := filepath.Rel(resolvedAbs(root), resolvedAbs(dir))
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	prefix := filepath.ToSlash(rel) + "/"
	if !tracked[prefix+"meta.json"] {
		return false
	}
	allowed := true
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !allowed {
			return nil
		}
		if st, serr := os.Stat(p); serr == nil && st.Mode().IsRegular() {
			r, _ := filepath.Rel(dir, p)
			allowed = fabricatedAllowed(filepath.ToSlash(r))
		}
		return nil
	})
	if !allowed {
		return false
	}
	for t := range tracked {
		if strings.HasPrefix(t, prefix) && !fabricatedAllowed(strings.TrimPrefix(t, prefix)) {
			return false
		}
	}
	return true
}

func isBlankValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// numberMatches is _number_matches: mission_number as text, an int, or blank.
func numberMatches(v any, expected string) bool {
	if expected == "" {
		return isBlankValue(v)
	}
	want, _ := strconv.Atoi(expected)
	switch t := v.(type) {
	case bool:
		return false
	case int:
		return t == want
	case int64:
		return t == int64(want)
	case json.Number:
		n, err := strconv.ParseInt(t.String(), 10, 64)
		return err == nil && n == int64(want)
	case string:
		return strings.TrimSpace(t) == expected
	}
	return false
}

func signalBaselineFields(slug string, meta interface{ Get(string) (any, bool) }) bool {
	for _, kv := range baselineSlugFields(slug) {
		v, _ := meta.Get(kv[0])
		if kv[0] == "mission_number" {
			if !numberMatches(v, kv[1]) {
				return false
			}
			continue
		}
		if s, ok := v.(string); !ok || s != kv[1] {
			return false
		}
	}
	return true
}

// fabricatedIdentityFindings is fabricated_identity_finding: zero or one
// WARNING finding for the kitty-specs/ child dir.
func fabricatedIdentityFindings(dir, root string, tracked map[string]bool) []Finding {
	if tracked == nil {
		return nil
	}
	meta := mission.ReadMetaOrEmpty(dir)
	if meta.Len() == 0 {
		return nil
	}
	slug := filepath.Base(dir)
	if !signalTrackedAllowlist(dir, root, tracked) || !signalBaselineFields(slug, meta) {
		return nil
	}
	mid8 := mission.Mid8FromSlug(slug)
	missionID, isStr := func() (string, bool) { v, _ := meta.Get("mission_id"); s, ok := v.(string); return s, ok }()
	if mid8 == "" || !isStr {
		return nil
	}
	if declared := mission.ResolveMid8(slug, missionID); declared == "" || declared == mid8 {
		return nil
	}
	return []Finding{fnd("FABRICATED_IDENTITY_SUSPECTED", "warning", "meta.json", fabricatedIdentityDetail)}
}
