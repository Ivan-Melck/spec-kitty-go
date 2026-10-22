package planning

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Port of setup-plan's _run_documentation_wiring (doc_analysis.gap_analysis,
// doc_state, doc_generators): gap analysis for gap_filling /
// mission_specific documentation missions and generator detection.

// scandirNames lists a directory in readdir (os.scandir) order.
func scandirNames(dir string) []string {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	return names
}

// rglobBFS is pathlib.Path.rglob(pattern) on Python 3.13: matching entries
// level by level, each directory in scandir order.
func rglobBFS(root, pattern string, stopAtFirst bool) []string {
	var out []string
	queue := []string{root}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		for _, n := range scandirNames(dir) {
			p := filepath.Join(dir, n)
			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if ok, _ := filepath.Match(pattern, n); ok {
				out = append(out, p)
				if stopAtFirst {
					return out
				}
			}
			if st.IsDir() {
				queue = append(queue, p)
			}
		}
	}
	return out
}

// detectGenerators is the JSDoc / Sphinx / rustdoc detection.
func detectGenerators(root string) []*jsonx.Object {
	var out []*jsonx.Object
	has := func(patterns ...string) bool {
		for _, p := range patterns {
			if len(rglobBFS(root, p, true)) > 0 {
				return true
			}
		}
		return false
	}
	if fileExists(filepath.Join(root, "package.json")) || has("*.js", "*.jsx", "*.ts", "*.tsx") {
		out = append(out, jsonx.NewObject("name", "jsdoc", "language", "javascript", "config_path", ""))
	}
	if fileExists(filepath.Join(root, "setup.py")) || fileExists(filepath.Join(root, "pyproject.toml")) || has("*.py") {
		out = append(out, jsonx.NewObject("name", "sphinx", "language", "python", "config_path", ""))
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) || has("*.rs") {
		out = append(out, jsonx.NewObject("name", "rustdoc", "language", "rust", "config_path", ""))
	}
	return out
}

var generatorLanguages = map[string]string{"jsdoc": "javascript, typescript", "sphinx": "python", "rustdoc": "rust"}

// updateDocState is doc_state._update_documentation_state.
func updateDocState(dir string, apply func(*jsonx.Object)) error {
	meta, err := mission.ReadMeta(dir)
	if err != nil {
		return err
	}
	st, _ := meta.Get("documentation_state")
	so, ok := st.(*jsonx.Object)
	if !ok || so == nil {
		so = jsonx.NewObject()
		meta.Set("documentation_state", so)
	}
	apply(so)
	return mission.WriteMeta(dir, meta, false)
}

// divio classification ---------------------------------------------------

var divioMarkers = []struct {
	typ     string
	markers []string
}{
	{"tutorial", []string{"step 1", "step 2", "first,", "next,", "now,", "you should see", "let's", "you'll learn", "by the end", "what you'll build"}},
	{"how-to", []string{"how to", "to do", "follow these steps", "problem:", "solution:", "before you begin", "prerequisites:", "verification:"}},
	{"reference", []string{"parameters:", "returns:", "arguments:", "options:", "methods:", "properties:", "attributes:", "class:", "function:", "api"}},
	{"explanation", []string{"why", "background", "concepts", "architecture", "design decision", "alternatives", "trade-offs", "how it works", "understanding"}},
}

func classifyHeuristics(content string) string {
	lower := strings.ToLower(content)
	best, bestScore := "unclassified", 0
	for _, d := range divioMarkers {
		score := 0
		for _, m := range d.markers {
			if strings.Contains(lower, m) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = d.typ, score
		}
	}
	return best
}

func classifyDivio(content string) (string, float64) {
	if strings.HasPrefix(content, "---") {
		lines := strings.Split(content, "\n")
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				if v, err := yamlx.Parse([]byte(strings.Join(lines[1:i], "\n"))); err == nil {
					if fm, ok := v.(*jsonx.Object); ok && fm != nil {
						if t, ok := fm.Get("type"); ok {
							s, isS := t.(string)
							if !isS {
								// frontmatter["type"].lower() raises: the
								// file is recorded unclassified.
								return "unclassified", 0
							}
							{
								switch strings.ToLower(s) {
								case "tutorial":
									return "tutorial", 1.0
								case "how-to", "howto":
									return "how-to", 1.0
								case "reference":
									return "reference", 1.0
								case "explanation":
									return "explanation", 1.0
								}
							}
						}
					}
				}
				break
			}
		}
	}
	t := classifyHeuristics(content)
	if t == "unclassified" {
		return t, 0
	}
	return t, 0.7
}

func detectDocFramework(docs string) string {
	switch {
	case fileExists(filepath.Join(docs, "conf.py")):
		return "sphinx"
	case fileExists(filepath.Join(docs, "mkdocs.yml")):
		return "mkdocs"
	case fileExists(filepath.Join(docs, "docusaurus.config.js")):
		return "docusaurus"
	case fileExists(filepath.Join(docs, "_config.yml")):
		return "jekyll"
	case fileExists(filepath.Join(docs, "config.toml")) || fileExists(filepath.Join(docs, "config.yaml")):
		return "hugo"
	case len(rglobBFS(docs, "*.md", true)) > 0:
		return "plain-markdown"
	}
	return "unknown"
}

func detectProjectAreas(docs, root string) []string {
	areas := map[string]bool{}
	for _, n := range scandirNames(docs) {
		if isRealDir(filepath.Join(docs, n)) && n != "_build" && n != "_static" && n != "_templates" {
			areas[n] = true
		}
	}
	dev := filepath.Join(docs, "development")
	if isRealDir(dev) {
		for _, c := range []string{"getting-started", "how-to", "reference", "reporting", "testing"} {
			if isRealDir(filepath.Join(dev, c)) {
				areas["development/"+c] = true
			}
		}
	}
	for _, n := range scandirNames(filepath.Join(root, "src")) {
		if isRealDir(filepath.Join(root, "src", n)) {
			areas[n] = true
		}
	}
	if len(areas) == 0 {
		areas[filepath.Base(root)] = true
	}
	return sortedSet(areas)
}

type gapEntry struct{ area, typ, priority, reason string }

var divioTypes = []string{"tutorial", "how-to", "reference", "explanation"}

func pyPct1(x float64) string { return fmt.Sprintf("%.1f", x) }

// gapAnalysisReport is generate_gap_analysis_report: the report text and
// the coverage fraction (an error mirrors upstream's to_markdown failure on
// a complete matrix, where the recommendations block reads an unbound name).
func gapAnalysisReport(docs, root string, date time.Time) (string, float64, string, error) {
	framework := detectDocFramework(docs)
	files := rglobBFS(docs, "*.md", false)
	type cls struct {
		typ  string
		conf float64
	}
	classified := map[string]cls{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			classified[f] = cls{"unclassified", 0}
			continue
		}
		t, c := classifyDivio(string(b))
		classified[f] = cls{t, c}
	}
	areas := detectProjectAreas(docs, root)
	cells := map[[2]string]string{}
	var cellOrder [][2]string
	for _, f := range files {
		c := classified[f]
		if c.typ == "unclassified" {
			continue
		}
		lower := strings.ToLower(f)
		area := ""
		for _, a := range areas {
			if strings.Contains(lower, strings.ToLower(a)) && len(a) > len(area) {
				area = a
			}
		}
		if area == "" && len(areas) > 0 {
			area = areas[0]
		}
		if area != "" {
			k := [2]string{area, c.typ}
			if _, ok := cells[k]; !ok {
				cellOrder = append(cellOrder, k)
			}
			cells[k] = f
		}
	}
	coverage := 0.0
	if total := len(areas) * len(divioTypes); total > 0 {
		coverage = float64(len(cells)) / float64(total)
	}
	var gaps []gapEntry
	for i, a := range areas {
		core := i < len(areas)/2
		for _, t := range divioTypes {
			if _, ok := cells[[2]string{a, t}]; ok {
				continue
			}
			g := gapEntry{area: a, typ: t}
			switch t {
			case "tutorial":
				if core {
					g.priority, g.reason = "high", "New users need tutorials to get started with core functionality"
				} else {
					g.priority, g.reason = "medium", "Users need tutorials for advanced features"
				}
			case "reference":
				if core {
					g.priority, g.reason = "high", "Users need API reference to use core features"
				} else {
					g.priority, g.reason = "medium", "API reference helps users discover all capabilities"
				}
			case "how-to":
				g.priority, g.reason = "medium", "Users need how-tos to solve common problems and tasks"
			default:
				g.priority, g.reason = "low", "Explanations aid understanding but are not blocking"
			}
			gaps = append(gaps, g)
		}
	}
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(gaps, func(i, j int) bool { return rank[gaps[i].priority] < rank[gaps[j].priority] })
	lines := []string{"# Gap Analysis: " + filepath.Base(root), "", "**Analysis Date**: " + date.Format("2006-01-02 15:04:05"),
		"**Documentation Framework**: " + framework, "**Coverage**: " + pyPct1(coverage*100) + "%", "", "## Coverage Matrix", ""}
	if len(areas) == 0 {
		lines = append(lines, "No project areas identified.")
	} else {
		var tl []string
		tl = append(tl, "| Area | "+strings.Join(divioTypes, " | ")+" |", "|"+strings.Repeat("---|", len(divioTypes)+1))
		for _, a := range areas {
			var cs []string
			for _, t := range divioTypes {
				if _, ok := cells[[2]string{a, t}]; ok {
					cs = append(cs, "✓")
				} else {
					cs = append(cs, "✗")
				}
			}
			tl = append(tl, "| "+a+" | "+strings.Join(cs, " | ")+" |")
		}
		lines = append(lines, strings.Join(tl, "\n")+fmt.Sprintf("\n**Coverage**: %d/%d cells = %s%%", len(cells), len(cells), pyPct1(coverage*100)))
	}
	lines = append(lines, "", "## Identified Gaps", "")
	if len(gaps) == 0 {
		return "", coverage, framework, fmt.Errorf("cannot access local variable 'high_gaps' where it is not associated with a value")
	}
	var high, med, low []gapEntry
	for _, g := range gaps {
		switch g.priority {
		case "high":
			high = append(high, g)
		case "medium":
			med = append(med, g)
		default:
			low = append(low, g)
		}
	}
	lines = append(lines, fmt.Sprintf("Found %d documentation gaps:", len(gaps)), "")
	for _, grp := range []struct {
		title string
		gs    []gapEntry
	}{{"### High Priority", high}, {"### Medium Priority", med}, {"### Low Priority", low}} {
		if len(grp.gs) == 0 {
			continue
		}
		lines = append(lines, grp.title, "")
		for _, g := range grp.gs {
			lines = append(lines, fmt.Sprintf("- **%s → %s**: %s", g.area, g.typ, g.reason))
		}
		lines = append(lines, "")
	}
	lines = append(lines, "## Existing Documentation", "")
	if len(files) == 0 {
		lines = append(lines, "No existing documentation found.")
	} else {
		lines = append(lines, fmt.Sprintf("Found %d documentation files:", len(files)), "")
		for _, t := range divioTypes {
			var group []string
			for _, f := range files {
				if c := classified[f]; c.typ == t {
					conf := ""
					if c.conf < 1.0 {
						conf = fmt.Sprintf("(%.0f%% confidence)", c.conf*100)
					}
					group = append(group, fmt.Sprintf("- %s %s", f, conf))
				}
			}
			if len(group) > 0 {
				title := strings.ToUpper(t[:1]) + t[1:]
				if t == "how-to" {
					title = "How-To"
				}
				lines = append(append(append(lines, "### "+title, ""), group...), "")
			}
		}
		var unc []string
		for _, f := range files {
			if classified[f].typ == "unclassified" {
				unc = append(unc, "- "+f)
			}
		}
		if len(unc) > 0 {
			lines = append(append(append(lines, "### Unclassified", ""), unc...), "")
		}
	}
	lines = append(lines, "## Recommendations", "")
	if len(high) > 0 {
		lines = append(lines, "**Immediate action needed**:")
		for i, g := range high {
			if i < 3 {
				lines = append(lines, fmt.Sprintf("1. Create %s for %s - %s", g.typ, g.area, g.reason))
			}
		}
		lines = append(lines, "")
	}
	if len(med) > 0 {
		lines = append(lines, "**Should address soon**:")
		for i, g := range med {
			if i < 3 {
				lines = append(lines, fmt.Sprintf("- Add %s for %s", g.typ, g.area))
			}
		}
		lines = append(lines, "")
	}
	if len(low) > 0 {
		lines = append(lines, fmt.Sprintf("**Nice to have**: %d low-priority gaps (see above)", len(low)), "")
	}
	_ = cellOrder
	return strings.Join(lines, "\n"), coverage, framework, nil
}

// runDocumentationWiring is _run_documentation_wiring: (gap analysis path,
// detected generators); a no-op for non-documentation missions.
func runDocumentationWiring(out io.Writer, jsonMode bool, root, slug string) (string, []*jsonx.Object) {
	dir := project.Specs(root, slug)
	if missionart.MissionTypeFor(dir) != "documentation" {
		return "", nil
	}
	metaFile := filepath.Join(dir, "meta.json")
	gapPath := ""
	if fileExists(metaFile) {
		meta := mission.ReadMetaOrEmpty(dir)
		mode := "initial"
		if meta.GetString("mission_type") == "documentation" {
			if st, ok := meta.Get("documentation_state"); ok {
				if so, isO := st.(*jsonx.Object); isO && so != nil {
					switch m := so.GetString("iteration_mode"); m {
					case "feature_specific":
						mode = "mission_specific"
					case "initial", "gap_filling", "mission_specific":
						mode = m
					default:
						mode = ""
					}
				}
			}
		}
		if mode == "gap_filling" || mode == "mission_specific" {
			docs := filepath.Join(root, "docs")
			if !isRealDir(docs) {
				if !jsonMode {
					say(out, "[yellow]Warning:[/yellow] No docs/ directory found, skipping gap analysis")
				}
			} else {
				outFile := filepath.Join(dir, "gap-analysis.md")
				date := clock.Now().UTC()
				report, coverage, _, err := gapAnalysisReport(docs, root, date)
				if err == nil {
					err = os.WriteFile(outFile, []byte(report), 0o644)
				}
				if err == nil {
					err = updateDocState(dir, func(s *jsonx.Object) {
						s.Set("last_audit_date", clock.ISO(date))
						s.Set("coverage_percentage", coverage)
					})
				}
				if err != nil {
					if !jsonMode {
						say(out, "[yellow]Warning:[/yellow] Gap analysis failed: "+err.Error())
					}
				} else {
					r := safecommit.CommitForMission(root, slug, []string{outFile, metaFile}, "Add gap analysis for mission "+slug, mission.KindPrimaryMetadataUnknown, safecommit.RouterOptions{})
					if !jsonMode && surfaceNeedsWarningPlain(r) {
						r.PrintRendered(out, wrapPlain)
					}
					if !jsonMode {
						say(out, fmt.Sprintf("[cyan]→ Gap analysis generated: gap-analysis.md (coverage: %s%%)[/cyan]", pyPct1(coverage*100)))
					}
					gapPath = outFile
				}
			}
		}
	}
	gens := detectGenerators(root)
	for _, g := range gens {
		if !jsonMode {
			say(out, fmt.Sprintf("[cyan]→ Detected %s generator (languages: %s)[/cyan]", g.GetString("name"), generatorLanguages[g.GetString("name")]))
		}
	}
	if len(gens) > 0 && fileExists(metaFile) {
		list := make([]any, len(gens))
		for i, g := range gens {
			list[i] = g
		}
		if err := updateDocState(dir, func(s *jsonx.Object) { s.Set("generators_configured", list) }); err != nil {
			if !jsonMode {
				say(out, "[yellow]Warning:[/yellow] Failed to save generator config: "+err.Error())
			}
		} else {
			r := safecommit.CommitForMission(root, slug, []string{metaFile}, "Update generator config for mission "+slug, mission.KindPrimaryMetadataUnknown, safecommit.RouterOptions{})
			if !jsonMode && surfaceNeedsWarningPlain(r) {
				r.PrintRendered(out, wrapPlain)
			}
		}
	}
	return gapPath, gens
}
