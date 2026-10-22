package planning

import (
	"regexp"
	"strings"
)

// Port of specify_cli.ownership.inference: best-effort ownership inference
// for WPs that lack execution_mode / owned_files, and the post-integration
// acceptance-criteria warning.

const (
	srcFallbackGlob    = "src/**"
	srcFallbackWarning = "No file paths found in WP body text; using broad fallback 'src/**'. Consider adding explicit owned_files to WP frontmatter."
)

var (
	planningSignals = compileAll(`\bkitty-specs/`, `\bspec\.md\b`, `\bplan\.md\b`, `\btasks\.md\b`, `\bdata-model\.md\b`, `\bquickstart\.md\b`)
	codeSignals     = compileAll(`\bsrc/`, `\btests?/`, `\.py\b`, `\.ts\b`, `\.js\b`)
	// Python's \s (str patterns) also matches \v, \x1c-\x1f, \x85 and the
	// Unicode separators.
	inferPathRe = regexp.MustCompile("(?:src|tests?|kitty-specs|docs)/[^\\s\\v\\x{1c}-\\x{1f}\\x{85}\\p{Z}`'\")\\]>]+")

	postIntegrationMarkers = compileAll(`(?i)\bpost[-\s]?merge\b`, `(?i)\bafter (?:the )?merge\b`, `(?i)\bonce merged\b`,
		`(?i)\bpost[-\s]?integration\b`, `(?i)\bafter integration\b`, `(?i)\bonce integrated\b`,
		`(?i)\bafter (?:the )?deploy(?:ment)?\b`, `(?i)\bonce deployed\b`)
	acceptanceHeading = regexp.MustCompile(`(?i)(?:acceptance|success\s+crit|review\s+guidance)`)
	headingLine       = regexp.MustCompile(`^#{1,6}[\s\v]`)
	explicitEmptyOF   = regexp.MustCompile(`(?m)^owned_files:[\s\v]*\[[\s\v]*\][\s\v]*$`)
)

func compileAll(pats ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

// inferExecutionMode is infer_execution_mode.
func inferExecutionMode(content string, files []string) string {
	combined := content + "\n" + strings.Join(files, "\n")
	planning, code := 0, 0
	for _, r := range planningSignals {
		if r.MatchString(combined) {
			planning++
		}
	}
	for _, r := range codeSignals {
		if r.MatchString(combined) {
			code++
		}
	}
	if planning > 0 && code == 0 {
		return "planning_artifact"
	}
	return "code_change"
}

// pySplitlines is str.splitlines().
func pySplitlines(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\x85', ' ', ' ':
			out = append(out, string(rs[start:i]))
			start = i + 1
		case '\r':
			out = append(out, string(rs[start:i]))
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

func acceptanceCriteriaScope(body string) string {
	var scoped []string
	capturing := false
	scopeLevel := 0
	for _, line := range pySplitlines(body) {
		if headingLine.MatchString(line) {
			level := len(line) - len(strings.TrimLeft(line, "#"))
			if capturing && level > scopeLevel {
				continue
			}
			capturing = acceptanceHeading.MatchString(line)
			if capturing {
				scopeLevel = level
			}
			continue
		}
		if capturing {
			scoped = append(scoped, line)
		}
	}
	return strings.Join(scoped, "\n")
}

// detectPostIntegrationAcceptance is detect_post_integration_acceptance.
func detectPostIntegrationAcceptance(content string, files []string) []string {
	if inferExecutionMode(content, files) != "code_change" {
		return nil
	}
	scope := acceptanceCriteriaScope(content)
	if scope == "" {
		return nil
	}
	set := map[string]bool{}
	for _, r := range postIntegrationMarkers {
		for _, m := range r.FindAllString(scope, -1) {
			set[strings.ToLower(strings.TrimSpace(m))] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	matched := sortedSet(set)
	quoted := make([]string, len(matched))
	for i, m := range matched {
		quoted[i] = "'" + m + "'"
	}
	return []string{"acceptance criteria appear observable only post-integration (" + strings.Join(quoted, ", ") +
		") -- an action, not a diff. The work package cannot be honestly reviewed while its lane is open; re-home this observation at planning time so the WP can terminate on a diff-inspectable criterion."}
}

// inferOwnedFiles is infer_owned_files.
func inferOwnedFiles(content, slug string) ([]string, []string) {
	if inferExecutionMode(content, nil) == "planning_artifact" {
		return []string{"kitty-specs/" + slug + "/**"}, nil
	}
	found := map[string]bool{}
	for _, m := range inferPathRe.FindAllString(content, -1) {
		found[m] = true
	}
	paths := sortedSet(found)
	var globs []string
	seen := map[string]bool{}
	for _, p := range paths {
		var g string
		switch {
		case strings.HasSuffix(p, "/"):
			g = p + "**"
		case !strings.Contains(p[strings.LastIndex(p, "/")+1:], "."):
			g = strings.TrimRight(p, "/") + "/**"
		default:
			g = p
		}
		parts := strings.Split(g, "/")
		skip := false
		for i := 1; i < len(parts); i++ {
			if seen[strings.Join(parts[:i], "/")+"/**"] {
				skip = true
				break
			}
		}
		if !skip {
			seen[g] = true
			globs = append(globs, g)
		}
	}
	if len(globs) == 0 {
		return []string{srcFallbackGlob}, []string{srcFallbackWarning}
	}
	return globs, nil
}

// inferAuthoritativeSurface is infer_authoritative_surface.
func inferAuthoritativeSurface(owned []string) string {
	if len(owned) == 0 {
		return ""
	}
	strip := func(p string) string {
		head, tail := "", p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			head, tail = p[:i], p[i+1:]
		}
		if strings.ContainsAny(tail, "*?[") || strings.Contains(tail, ".") {
			return head
		}
		return strings.TrimRight(p, "/")
	}
	stripped := make([]string, len(owned))
	for i, p := range owned {
		stripped[i] = strip(p)
	}
	if len(stripped) == 1 {
		c := strings.TrimRight(stripped[0], "/") + "/"
		if strings.HasPrefix(owned[0], c) {
			return c
		}
		return owned[0]
	}
	split := make([][]string, len(stripped))
	minLen := -1
	for i, s := range stripped {
		split[i] = strings.Split(s, "/")
		if minLen < 0 || len(split[i]) < minLen {
			minLen = len(split[i])
		}
	}
	var common []string
	for j := 0; j < minLen; j++ {
		same := true
		for _, sp := range split[1:] {
			if sp[j] != split[0][j] {
				same = false
				break
			}
		}
		if !same {
			break
		}
		common = append(common, split[0][j])
	}
	if len(common) == 0 {
		common = split[0]
	}
	c := strings.TrimRight(strings.Join(common, "/"), "/") + "/"
	for _, p := range owned {
		if strings.HasPrefix(p, c) {
			return c
		}
	}
	return owned[0]
}

// ownedFilesExplicitlyEmpty is _owned_files_yaml_is_explicit_empty_list.
func ownedFilesExplicitlyEmpty(raw string) bool {
	if !strings.HasPrefix(raw, "---") {
		return false
	}
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) < 3 {
		return false
	}
	return explicitEmptyOF.MatchString(parts[1])
}
