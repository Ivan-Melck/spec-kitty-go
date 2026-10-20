package events

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// ResolvedMission mirrors context.mission_resolver.ResolvedMission.
type ResolvedMission struct {
	MissionID  string
	Slug       string
	FeatureDir string
	Mid8       string
}

// NotFoundError is MissionNotFoundError.
type NotFoundError struct{ Handle string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("No mission found for handle \"%s\". If this is a legacy mission whose meta.json lacks a mission_id, run `spec-kitty migrate backfill-identity` to backfill it.", e.Handle)
}

// AmbiguousError is AmbiguousHandleError.
type AmbiguousError struct {
	Handle     string
	Candidates []ResolvedMission
}

func (e *AmbiguousError) Error() string {
	lines := []string{fmt.Sprintf("Error: mission handle \"%s\" matches multiple missions.", e.Handle), "", "Candidates:"}
	for _, c := range e.Candidates {
		lines = append(lines, fmt.Sprintf("  %s (mission_id %s, mid8 %s)", c.Slug, c.MissionID, c.Mid8))
	}
	lines = append(lines, "", "Re-run with a more specific handle:")
	for _, c := range e.Candidates {
		lines = append(lines, "  spec-kitty <command> --mission "+c.Mid8, "  spec-kitty <command> --mission "+c.Slug)
	}
	lines = append(lines, "", "For JSON output of all candidates:", fmt.Sprintf("  spec-kitty doctor identity --mission %s --json", e.Handle))
	return strings.Join(lines, "\n")
}

var (
	ulidRe     = regexp.MustCompile(`^[0-9A-Z]{26}$`)
	mid8Re     = regexp.MustCompile(`^[0-9A-Z]{8}$`)
	prefixRe   = regexp.MustCompile(`^(\d{3})-`)
	numPrefRe  = regexp.MustCompile(`^\d{3}-(.+)$`)
	allDigitRe = regexp.MustCompile(`^[0-9]+$`)
)

// LoadMetaNone is load_meta(on_malformed="none") with strict UTF-8: nil when
// meta.json is missing, malformed or not an object.
func LoadMetaNone(dir string) *jsonx.Object {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil
	}
	v, err := pyjson.LoadsBytes(b)
	if err != nil {
		return nil
	}
	o, _ := v.(*jsonx.Object)
	return o
}

func stripNumericPrefix(slug string) string {
	if m := numPrefRe.FindStringSubmatch(slug); m != nil && m[1] != "" {
		return m[1]
	}
	return slug
}

// buildIndex is _build_index.
func buildIndex(repoRoot string) []ResolvedMission {
	specs := project.Specs(repoRoot)
	entries, err := os.ReadDir(specs)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []ResolvedMission
	for _, name := range names {
		dir := filepath.Join(specs, name)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		meta := LoadMetaNone(dir)
		if meta == nil {
			continue
		}
		raw, _ := meta.Get("mission_id")
		id, ok := raw.(string)
		if !ok || id == "" {
			continue
		}
		mid8 := ""
		if len([]rune(id)) >= 8 {
			mid8 = string([]rune(id)[:8])
		}
		out = append(out, ResolvedMission{MissionID: id, Slug: name, FeatureDir: dir, Mid8: mid8})
	}
	return out
}

func pick(handle string, matches []ResolvedMission) (ResolvedMission, error) {
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return ResolvedMission{}, &NotFoundError{Handle: handle}
	}
	return ResolvedMission{}, &AmbiguousError{Handle: handle, Candidates: matches}
}

// ResolveMission is resolve_mission(handle, repo_root).
func ResolveMission(handle, repoRoot string) (ResolvedMission, error) {
	missions := buildIndex(repoRoot)
	filter := func(f func(m ResolvedMission) bool) []ResolvedMission {
		var out []ResolvedMission
		for _, m := range missions {
			if f(m) {
				out = append(out, m)
			}
		}
		return out
	}
	if ulidRe.MatchString(handle) {
		return pick(handle, filter(func(m ResolvedMission) bool { return m.MissionID == handle }))
	}
	if mid8Re.MatchString(handle) {
		return pick(handle, filter(func(m ResolvedMission) bool { return strings.HasPrefix(m.MissionID, handle) }))
	}
	if ms := filter(func(m ResolvedMission) bool { return m.Slug == handle }); len(ms) > 0 {
		return pick(handle, ms)
	}
	numeric := allDigitRe.MatchString(handle)
	if !numeric {
		if ms := filter(func(m ResolvedMission) bool { return stripNumericPrefix(m.Slug) == handle }); len(ms) > 0 {
			return pick(handle, ms)
		}
		return ResolvedMission{}, &NotFoundError{Handle: handle}
	}
	return pick(handle, filter(func(m ResolvedMission) bool {
		p := prefixRe.FindStringSubmatch(m.Slug)
		return p != nil && p[1] == handle
	}))
}

// JSONErrorPayload renders resolve_mission_handle's json_mode envelope.
func JSONErrorPayload(err error) string {
	o := jsonx.NewObject("success", false)
	switch e := err.(type) {
	case *AmbiguousError:
		o.Set("error_code", "MISSION_AMBIGUOUS_SELECTOR")
		o.Set("error", e.Error())
		o.Set("handle", e.Handle)
		var cands []any
		for _, c := range e.Candidates {
			cands = append(cands, jsonx.NewObject("mission_id", c.MissionID, "mid8", c.Mid8, "slug", c.Slug, "feature_dir", c.FeatureDir))
		}
		o.Set("candidates", cands)
	case *NotFoundError:
		o.Set("error_code", "MISSION_NOT_FOUND")
		o.Set("error", e.Error())
		o.Set("handle", e.Handle)
	}
	return jsonx.Dumps(o, jsonx.Options{Indent: -1, EnsureASCII: true})
}
