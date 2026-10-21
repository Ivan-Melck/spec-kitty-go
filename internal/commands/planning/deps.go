package planning

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// --- core.dependency_parser ---------------------------------------------------

var (
	wpIDTitle      = regexp.MustCompile(`^(?:Work Package\s+)?(WP\d{2})(?:\b|:)`)
	anyH2Header    = regexp.MustCompile(`(?m)^## `)
	dependsOnRe    = regexp.MustCompile(`(?im)^\s*(?:[-*]\s*)?Depends?\s+on\s+(WP\d{2}(?:\s*,\s*WP\d{2})*)\b`)
	depsColonLine  = regexp.MustCompile(`(?im)^\s*(?:[-*]\s*)?\*?\*?Dependencies\*?\*?\s*:\s*([^|\n]*)`)
	depsColonPipe  = regexp.MustCompile(`(?i)\|\s*\*?\*?Dependencies\*?\*?\s*:\s*([^|\n]*)`)
	depsHeading    = regexp.MustCompile(`(?im)^#{1,4}\s*\*?\*?Dependencies\*?\*?\s*$`)
	depsHeadingOne = regexp.MustCompile(`(?i)^#{1,4}\s*\*?\*?Dependencies\*?\*?\s*$`)
	bulletWP       = regexp.MustCompile(`^\s*[-*]\s*(WP\d{2})`)
	wpIDRe         = regexp.MustCompile(`WP\d{2}`)
	depsListRe     = regexp.MustCompile(`(?i)^(WP\d{2}(?:\s*,\s*WP\d{2})*)\b`)
	dependsOnOne   = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?Depends?\s+on\s+(WP\d{2}(?:\s*,\s*WP\d{2})*)\b`)
)

func matchWPSectionID(title string) string {
	if m := wpIDTitle.FindStringSubmatch(title); m != nil {
		return m[1]
	}
	if !strings.HasPrefix(title, "Work Package ") {
		return ""
	}
	suffix := []rune(title[len("Work Package "):])
	n := 0
	for n < len(suffix) && n < 2 && unicode.IsDigit(suffix[n]) {
		n++
	}
	if n == 0 || (n < len(suffix) && unicode.IsDigit(suffix[n])) {
		return ""
	}
	rem := suffix[n:]
	if len(rem) > 0 && !(unicode.IsSpace(rem[0]) || strings.ContainsRune("—:-", rem[0])) {
		return ""
	}
	v, _ := strconv.Atoi(string(suffix[:n]))
	return fmt.Sprintf("WP%02d", v)
}

type wpSection struct {
	id         string
	start, end int
}

func splitWPSections(content string) (map[string]string, []string) {
	var matches []wpSection
	offset := 0
	for _, raw := range strings.SplitAfter(content, "\n") {
		if raw == "" {
			continue
		}
		line := strings.TrimRight(raw, "\r\n")
		var title string
		ok := false
		if strings.HasPrefix(line, "## ") {
			title, ok = strings.TrimSpace(line[3:]), true
		} else if strings.HasPrefix(line, "### ") {
			title, ok = strings.TrimSpace(line[4:]), true
		}
		if ok {
			if id := matchWPSectionID(title); id != "" {
				matches = append(matches, wpSection{id, offset, offset + len(raw)})
			}
		}
		offset += len(raw)
	}
	out := map[string]string{}
	var order []string
	for i, m := range matches {
		start := m.end
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1].start
		} else {
			for _, loc := range anyH2Header.FindAllStringIndex(content[start:], -1) {
				hs := start + loc[0]
				le := strings.IndexByte(content[hs:], '\n')
				heading := content[hs:]
				if le >= 0 {
					heading = content[hs : hs+le]
				}
				if matchWPSectionID(strings.TrimSpace(heading[3:])) != "" {
					continue
				}
				if depsHeadingOne.MatchString(strings.TrimSpace(heading)) {
					continue
				}
				end = hs
				break
			}
		}
		if _, dup := out[m.id]; !dup {
			order = append(order, m.id)
		}
		out[m.id] = content[start:end]
	}
	return out, order
}

func parseColonDependencyValue(v string) []string {
	s := strings.TrimSpace(v)
	n := strings.ToLower(s)
	if s == "" || n == "[]" || strings.HasPrefix(n, "none") || strings.HasPrefix(n, "no deps") || strings.HasPrefix(n, "no dependencies") {
		return nil
	}
	if m := dependsOnOne.FindStringSubmatch(s); m != nil {
		return wpIDRe.FindAllString(m[1], -1)
	}
	m := depsListRe.FindStringSubmatchIndex(s)
	if m == nil {
		return nil
	}
	rest := strings.TrimLeft(s[m[1]:], " \t\n\r\f\v")
	if rest != "" && !strings.ContainsAny(rest[:1], ".;(#") {
		return nil
	}
	return wpIDRe.FindAllString(s[m[2]:m[3]], -1)
}

func parseSectionDeps(sec string) []string {
	var deps []string
	for _, m := range dependsOnRe.FindAllStringSubmatch(sec, -1) {
		deps = append(deps, wpIDRe.FindAllString(m[1], -1)...)
	}
	type cm struct {
		start int
		line  string
		value string
	}
	var cms []cm
	for _, re := range []*regexp.Regexp{depsColonLine, depsColonPipe} {
		for _, loc := range re.FindAllStringSubmatchIndex(sec, -1) {
			cms = append(cms, cm{loc[0], sec[loc[0]:loc[1]], sec[loc[2]:loc[3]]})
		}
	}
	for i := 1; i < len(cms); i++ {
		for j := i; j > 0 && cms[j].start < cms[j-1].start; j-- {
			cms[j], cms[j-1] = cms[j-1], cms[j]
		}
	}
	for _, c := range cms {
		if depsHeadingOne.MatchString(strings.TrimSpace(c.line)) {
			continue
		}
		deps = append(deps, parseColonDependencyValue(c.value)...)
	}
	for _, loc := range depsHeading.FindAllStringIndex(sec, -1) {
		for _, line := range strings.Split(sec[loc[1]:], "\n") {
			st := strings.TrimSpace(line)
			if st == "" {
				continue
			}
			if strings.HasPrefix(st, "#") {
				break
			}
			if m := bulletWP.FindStringSubmatch(line); m != nil {
				deps = append(deps, m[1])
			} else {
				break
			}
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, d := range deps {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// parseDependenciesFromTasksMD is parse_dependencies_from_tasks_md.
func parseDependenciesFromTasksMD(content string) (map[string][]string, []string) {
	secs, order := splitWPSections(content)
	out := map[string][]string{}
	for id, s := range secs {
		out[id] = parseSectionDeps(s)
	}
	return out, order
}

// --- core.dependency_graph -----------------------------------------------------

// detectCycles is detect_cycles (DFS over graph in key order).
func detectCycles(graph map[string][]string, order []string) [][]string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var cycles [][]string
	var dfs func(n string, path []string)
	dfs = func(n string, path []string) {
		color[n] = gray
		path = append(path, n)
		for _, nb := range graph[n] {
			switch color[nb] {
			case gray:
				for i, p := range path {
					if p == nb {
						c := append(append([]string{}, path[i:]...), nb)
						cycles = append(cycles, c)
						break
					}
				}
			case white:
				dfs(nb, path)
			}
		}
		color[n] = black
	}
	for _, n := range order {
		if color[n] == white {
			dfs(n, nil)
		}
	}
	return cycles
}

var wpExact = regexp.MustCompile(`^WP\d{2}$`)

// validateDependencies is validate_dependencies.
func validateDependencies(wpID string, deps []string, graph map[string][]string, order []string) []string {
	var errs []string
	for _, d := range deps {
		if !wpExact.MatchString(d) {
			errs = append(errs, fmt.Sprintf("Invalid WP ID format: %s (must be WP## like WP01)", d))
			continue
		}
		if d == wpID {
			errs = append(errs, fmt.Sprintf("Cannot depend on self: %s → %s", wpID, wpID))
			continue
		}
		if _, ok := graph[d]; !ok {
			errs = append(errs, fmt.Sprintf("Dependency %s not found in graph", d))
		}
	}
	test := map[string][]string{}
	for k, v := range graph {
		test[k] = v
	}
	o := order
	if _, ok := graph[wpID]; !ok {
		o = append(append([]string{}, order...), wpID)
	}
	test[wpID] = deps
	for _, c := range detectCycles(test, o) {
		in := false
		for _, x := range c {
			if x == wpID {
				in = true
			}
		}
		if in {
			errs = append(errs, "Circular dependency detected: "+strings.Join(c, " → "))
			break
		}
	}
	return errs
}

// pyListRepr renders a list of strings like Python repr.
func pyListRepr(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = "'" + strings.ReplaceAll(x, "'", `\'`) + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
