package nextrt

// Post-composition guards of the research and documentation mission
// families (runtime_bridge_cores._evaluate_research_guards /
// _evaluate_documentation_guards over a gather_artifact_presence snapshot).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// artifactHome is _ArtifactPresenceHomes.read_dir: a classified PRIMARY
// artifact is read from the primary mission dir; an unclassified file (and a
// COORD-partition one) from the status dir, the coordination dir under
// coordination topology.
func (m *missionCtx) artifactHome(name string) string {
	k := mission.ArtifactKindForPath(filepath.Join("kitty-specs", m.Slug, name))
	if k == "" || mission.CoordinationKinds[k] {
		return m.StatusDir
	}
	return m.Primary
}

// missionEventsMatch counts the mission-events.jsonl records of featureDir
// that satisfy match; undecodable lines are skipped and an unreadable log
// counts nothing (the guard fails closed).
func missionEventsMatch(featureDir string, match func(*jsonx.Object) bool) int {
	b, err := os.ReadFile(filepath.Join(featureDir, "mission-events.jsonl"))
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if o, err := jsonx.DecodeObject([]byte(line)); err == nil && o != nil && match(o) {
			n++
		}
	}
	return n
}

// hasGeneratedDocs is _has_generated_docs: any *.md under featureDir/docs.
func hasGeneratedDocs(featureDir string) bool {
	root := filepath.Join(featureDir, "docs")
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if ok, _ := filepath.Match("*.md", d.Name()); ok {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// researchDocumentationGuards is evaluate_guards_strict for the research and
// documentation families; an action without a registered guard fails closed.
func (c *advanceCtx) researchDocumentationGuards(family, action string) []string {
	tags := presenceTags(c.m.Root, family)
	need := func(name string) []string {
		if tags[name] {
			st, err := os.Stat(filepath.Join(c.m.artifactHome(name), name))
			if err == nil && st.Mode().IsRegular() {
				return nil
			}
		}
		return []string{"Required artifact missing: " + name}
	}
	if family == "research" {
		switch action {
		case "scoping":
			return need("spec.md")
		case "methodology":
			return need("plan.md")
		case "gathering":
			out := need("source-register.csv")
			if missionEventsMatch(c.featureDir, func(o *jsonx.Object) bool { return o.GetString("type") == "source_documented" }) < 3 {
				out = append(out, "Insufficient sources documented (need >=3)")
			}
			return out
		case "synthesis":
			return need("findings.md")
		case "output":
			out := need("report.md")
			if missionEventsMatch(c.featureDir, func(o *jsonx.Object) bool {
				return o.GetString("type") == "gate_passed" && o.GetString("name") == "publication_approved"
			}) == 0 {
				out = append(out, "Publication approval gate not passed")
			}
			return out
		}
		return []string{"No guard registered for research action: " + action}
	}
	switch action {
	case "discover":
		return need("spec.md")
	case "audit":
		return need("gap-analysis.md")
	case "design":
		return need("plan.md")
	case "generate":
		if hasGeneratedDocs(c.featureDir) {
			return nil
		}
		return []string{"Required artifact missing: docs/**/*.md (no Markdown files found under docs/)"}
	case "validate":
		return need("audit-report.md")
	case "publish":
		return need("release.md")
	case "accept":
		return nil
	}
	return []string{"No guard registered for documentation action: " + action}
}
