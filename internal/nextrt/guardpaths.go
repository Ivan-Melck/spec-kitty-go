package nextrt

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// presenceTags is runtime_bridge_io._presence_filenames_for: every
// path_pattern of the family's expected-artifacts manifest (required_always,
// required_by_step, optional_always); an org-pack manifest replaces the
// built-in one whole-file. Families without a manifest have no tags.
func presenceTags(root, family string) map[string]bool {
	var src []byte
	for _, org := range dct.OrgRoots(root) {
		if b, err := os.ReadFile(filepath.Join(org, "missions", family, "expected-artifacts.yaml")); err == nil {
			src = b
			break
		}
	}
	if src == nil {
		b, err := assets.ReadFile("packs/built-in/missions/" + family + "/expected-artifacts.yaml")
		if err != nil {
			return nil
		}
		src = b
	}
	o, err := yamlx.ParseObject(src)
	if err != nil || o == nil {
		return nil
	}
	tags := map[string]bool{}
	visit := func(v any) {
		l, _ := v.([]any)
		for _, x := range l {
			if e, ok := x.(*jsonx.Object); ok && e.GetString("path_pattern") != "" {
				tags[e.GetString("path_pattern")] = true
			}
		}
	}
	v, _ := o.Get("required_always")
	visit(v)
	if rb := o.GetObject("required_by_step"); rb != nil {
		for _, kv := range rb.Pairs() {
			visit(kv.Value)
		}
	}
	v, _ = o.Get("optional_always")
	visit(v)
	return tags
}

// withGuardFailurePaths is decision._with_guard_failure_paths: each
// genuinely missing (or stale-analysis) artifact failure keyed by its real
// artifact tag, mapped to the repo-relative path the presence check read.
func withGuardFailurePaths(root, slug string, d *Decision) *Decision {
	if d == nil || len(d.GuardFailures) == 0 || (d.GuardFailurePaths != nil && d.GuardFailurePaths.Len() > 0) {
		return d
	}
	m := newMissionCtx(root, slug)
	family := d.Mission
	if family == "" {
		family = m.MissionType
	}
	known := presenceTags(root, family)
	prefixes := []string{strings.TrimSuffix(MissingArtifactMessage, "%s"), strings.TrimSuffix(AnalysisStaleMessage, "%s")}
	var tags []string
	seen := map[string]bool{}
	for _, f := range d.GuardFailures {
		cand := f
		for _, p := range prefixes {
			if strings.HasPrefix(f, p) {
				cand = f[len(p):]
				break
			}
		}
		if known[cand] && !seen[cand] {
			seen[cand] = true
			tags = append(tags, cand)
		}
	}
	o := &jsonx.Object{}
	sort.Strings(tags)
	for _, t := range tags {
		p := filepath.Join(m.Primary, t)
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = rel
		}
		o.Set(t, p)
	}
	d.GuardFailurePaths = o
	return d
}
