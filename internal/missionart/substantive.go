// Package missionart holds mission planning-artifact helpers shared by the
// planning commands: the substantive-content gate (missions/_substantive.py),
// committedness, the writer-artifact inventory (mission.yaml artifacts) and
// validate_feature_structure.
package missionart

import (
	"os"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/reqmap"
)

var placeholderPatterns = func() []*regexp.Regexp {
	lit := []string{
		`\[NEEDS CLARIFICATION[^\]]*\]`, `\[e\.g\.,[^\]]*\]`, `\[FEATURE\]`, `\[###-feature[^\]]*\]`, `\[Short title\]`,
		`\[Measurable threshold[^\]]*\]`, `\[role\]`, `\[goal\]`, `\[benefit\]`, `\[specific capability[^\]]*\]`,
		`\[key interaction[^\]]*\]`, `\[data requirement[^\]]*\]`, `\[behavior[^\]]*\]`, `\[domain-specific[^\]]*\]`,
		`\[if applicable[^\]]*\]`, `\[Project-specific[^\]]*\]`, `\[single/web/mobile[^\]]*\]`,
		regexp.QuoteMeta("[Sphinx | MkDocs | Docusaurus | Jekyll | Hugo | None (starting fresh) or NEEDS CLARIFICATION]"),
		regexp.QuoteMeta("[Python, JavaScript, Rust, etc. - from codebase analysis]"),
		regexp.QuoteMeta("[HTML | Markdown | PDF or NEEDS CLARIFICATION]"),
		regexp.QuoteMeta("[Read the Docs | GitHub Pages | GitBook | Custom or NEEDS CLARIFICATION]"),
		`\[Primary question\]`, `\[List search terms\]`, `\[What qualifies for review\]`, `\[What will be filtered out\]`,
		`\[Sub-problem statement\]`, `\[Cluster name\]`, `\[SP-# or none\]`, `\[SP-#\]`, `\[High/Low\]`, `\[Why it goes first\]`,
		`\[Why it goes next\]`, `\[Without this, the plan fails its purpose\]`, `\[Important, painful to omit, but not fatal if deferred\]`,
		`\[Desirable, included only if Must/Should leave room\]`, `\[Explicitly deferred — may return in a later cut\]`,
		`\[Problem, drivers, constraints forcing this decision now\]`, `\[Chosen option, stated plainly\]`,
		`\[Why this option wins\]`, `\[Accepted trade-offs, positive and negative\]`,
	}
	out := make([]*regexp.Regexp, len(lit))
	for i, l := range lit {
		out[i] = regexp.MustCompile(l)
	}
	return out
}()

func stripPlaceholders(s string) string {
	for _, p := range placeholderPatterns {
		s = p.ReplaceAllString(s, "")
	}
	return s
}

var emptyUserStory = map[string]bool{"as a i want so that": true, "as an i want so that": true}

// IsSubstantiveText is _is_substantive_text.
func IsSubstantiveText(raw string) bool {
	c := strings.TrimSpace(stripPlaceholders(raw))
	if c == "" {
		return false
	}
	n := strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimRight(c, "."), ",", " ")), " "))
	return !emptyUserStory[n]
}

var needsClar = regexp.MustCompile(`^NEEDS CLARIFICATION\.?$`)

func isRealTechValue(raw string) bool {
	v := strings.TrimSpace(stripPlaceholders(raw))
	return v != "" && !needsClar.MatchString(v)
}

func rowCellsSubstantive(rest string, skip int, take int) bool {
	cols := strings.Split(strings.TrimRight(rest, "|"), "|")
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	if skip < len(cols) {
		cols = cols[skip:]
	} else {
		cols = nil
	}
	if take >= 0 && take < len(cols) {
		cols = cols[:take]
	}
	for _, c := range cols {
		if IsSubstantiveText(c) {
			return true
		}
	}
	return false
}

func isLocalFR(tok string) bool {
	id, ok := reqmap.Parse(tok, "software-dev")
	return ok && id.IsFunctional() && id.Mission == ""
}

func frBulletDescription(line string) (string, bool) {
	m := reqmap.DeclaredListItem.FindStringSubmatchIndex(line)
	if m == nil || !isLocalFR(line[m[2]:m[3]]) {
		return "", false
	}
	if reqmap.IsCompoundTail(line, m[3]) {
		return "", false
	}
	suffix := line[m[3]:]
	if strings.HasSuffix(line[:m[2]], "**") {
		if !strings.HasPrefix(suffix, "**") {
			return "", false
		}
		suffix = suffix[2:]
	}
	suffix = strings.TrimLeft(suffix, " \t\n\r\f\v")
	if suffix == "" || (suffix[0] != ':' && suffix[0] != '-') {
		return "", false
	}
	d := strings.TrimSpace(suffix[1:])
	return d, d != ""
}

// HasSubstantiveFRRow is _has_substantive_fr_row.
func HasSubstantiveFRRow(body string) bool {
	for _, line := range splitLines(reqmap.BlankHTMLComments(body)) {
		if m := reqmap.DeclaredTableRow.FindStringSubmatchIndex(line); m != nil && isLocalFR(line[m[2]:m[3]]) {
			if rowCellsSubstantive(line[m[1]:], 0, 2) {
				return true
			}
		}
		if d, ok := frBulletDescription(line); ok && IsSubstantiveText(d) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if s == "" {
		return nil
	}
	l := strings.Split(s, "\n")
	if l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}

// extractSection is _extract_section_body: body under "## heading" up to the
// next level-2 heading.
func extractSection(body, heading string) (string, bool) {
	re := regexp.MustCompile(`##\s+` + regexp.QuoteMeta(heading) + `\s*\n`)
	loc := re.FindStringIndex(body)
	if loc == nil {
		return "", false
	}
	rest := body[loc[1]:]
	// stop at "\n##" not followed by a third '#'
	i := 0
	for {
		j := strings.Index(rest[i:], "\n##")
		if j < 0 {
			return rest, true
		}
		k := i + j
		if k+3 < len(rest) && rest[k+3] == '#' {
			i = k + 1
			continue
		}
		return rest[:k], true
	}
}

var boldFieldLine = regexp.MustCompile(`(?m)^[ \t]*(?:[-*][ \t]+)?\*\*(?P<label>[^*\n]+)\*\*[ \t]*:[ \t]*(?P<val>[^\n]*)`)

func hasSubstantiveSublist(rem string) bool {
	lbl := regexp.MustCompile(`^[ \t]*\*\*[^*\n]+\*\*[ \t]*:`)
	for _, line := range splitLines(rem) {
		if lbl.MatchString(line) {
			break
		}
		st := strings.TrimSpace(line)
		if st == "" {
			continue
		}
		if st[0] != '-' && st[0] != '*' {
			break
		}
		if IsSubstantiveText(strings.TrimSpace(st[1:])) {
			return true
		}
	}
	return false
}

func boldFieldSubstantive(section, label string, subList bool) bool {
	stripped := stripPlaceholders(section)
	re := regexp.MustCompile(`(?m)^[ \t]*(?:[-*][ \t]+)?\*\*` + regexp.QuoteMeta(label) + `\*\*[ \t]*:[ \t]*(?P<val>[^\n]*)`)
	m := re.FindStringSubmatchIndex(stripped)
	if m == nil {
		return false
	}
	if isRealTechValue(stripped[m[2]:m[3]]) {
		return true
	}
	if !subList {
		return false
	}
	return hasSubstantiveSublist(stripped[m[1]:])
}

func anyOtherBoldFieldSubstantive(section, exclude string) bool {
	stripped := stripPlaceholders(section)
	for _, m := range boldFieldLine.FindAllStringSubmatch(stripped, -1) {
		if exclude != "" && strings.TrimSpace(m[1]) == exclude {
			continue
		}
		if isRealTechValue(m[2]) {
			return true
		}
	}
	return false
}

var (
	genericRow   = regexp.MustCompile(`(?m)^[ \t]*\|(?P<rest>.+)\|[ \t]*$`)
	separatorRow = regexp.MustCompile(`^[ \t]*\|(?:[ \t]*:?-{2,}:?[ \t]*\|)+[ \t]*$`)
)

func tableFieldSubstantive(body, heading string) bool {
	sec, ok := extractSection(body, heading)
	if !ok {
		return false
	}
	lines := splitLines(sec)
	sep := -1
	var pipe []int
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "|") {
			pipe = append(pipe, i)
			if sep < 0 && separatorRow.MatchString(l) {
				sep = i
			}
		}
	}
	if sep < 0 {
		return false
	}
	var rows []string
	for _, i := range pipe {
		if i > sep {
			rows = append(rows, lines[i])
		}
	}
	if len(rows) == 0 {
		return false
	}
	for _, m := range genericRow.FindAllStringSubmatch(strings.Join(rows, "\n"), -1) {
		if rowCellsSubstantive(m[1], 1, -1) {
			return true
		}
	}
	return false
}

func nestedSubstantive(body, container, shape, child string) bool {
	cb, ok := extractSection(body, container)
	if !ok {
		return false
	}
	if shape == "repeatable" {
		re := regexp.MustCompile(`(?ms)^###[ \t]+` + regexp.QuoteMeta(child) + `\S*[^\n]*\n`)
		for _, loc := range re.FindAllStringIndex(cb, -1) {
			if anyOtherBoldFieldSubstantive(untilHeading(cb[loc[1]:]), "") {
				return true
			}
		}
		return false
	}
	re := regexp.MustCompile(`(?m)^###[ \t]+` + regexp.QuoteMeta(child) + `[ \t]*\n`)
	loc := re.FindStringIndex(cb)
	if loc == nil {
		return false
	}
	return anyOtherBoldFieldSubstantive(untilHeading(cb[loc[1]:]), "")
}

var anyHeadingAfterNL = regexp.MustCompile(`\n#{1,6}[ \t]`)

func untilHeading(s string) string {
	if loc := anyHeadingAfterNL.FindStringIndex(s); loc != nil {
		return s[:loc[0]]
	}
	return s
}

type fieldSpec struct {
	kind, heading, label, exclude, example, shape, child string
	subList                                              bool
}

type planDecl struct {
	primary fieldSpec
	peers   []fieldSpec
}

var planDecls = map[string]planDecl{
	"software-dev": {fieldSpec{kind: "bold", heading: "Technical Context", label: "Language/Version"},
		[]fieldSpec{{kind: "any", heading: "Technical Context", exclude: "Language/Version", example: "Primary Dependencies"}}},
	"documentation": {fieldSpec{kind: "bold", heading: "Technical Context", label: "Documentation Framework"},
		[]fieldSpec{{kind: "bold", heading: "Technical Context", label: "Languages Detected"}, {kind: "bold", heading: "Technical Context", label: "Output Format"},
			{kind: "bold", heading: "Technical Context", label: "Hosting Platform"}, {kind: "bold", heading: "Technical Context", label: "Build Commands", subList: true}}},
	"research": {fieldSpec{kind: "bold", heading: "Research Context", label: "Research Question"},
		[]fieldSpec{{kind: "nested", heading: "Methodology", shape: "named_sibling", child: "Data Sources"}}},
	"plan": {fieldSpec{kind: "table", heading: "Problem Decomposition"},
		[]fieldSpec{{kind: "any", heading: "Scope — MoSCoW"}, {kind: "table", heading: "Sequencing & Prioritisation"},
			{kind: "nested", heading: "Decisions", shape: "repeatable", child: "Decision D-"}}},
}

func fieldSubstantive(body string, f fieldSpec) bool {
	switch f.kind {
	case "bold":
		sec, ok := extractSection(body, f.heading)
		return ok && boldFieldSubstantive(sec, f.label, f.subList)
	case "any":
		sec, ok := extractSection(body, f.heading)
		return ok && anyOtherBoldFieldSubstantive(sec, f.exclude)
	case "table":
		return tableFieldSubstantive(body, f.heading)
	}
	return nestedSubstantive(body, f.heading, f.shape, f.child)
}

func fieldDisplayName(f fieldSpec) string {
	switch f.kind {
	case "bold":
		return f.label
	case "any":
		if f.example != "" {
			return f.example
		}
		return "a peer field in " + f.heading
	case "table":
		return f.heading
	}
	if f.shape == "named_sibling" {
		return f.child
	}
	return f.heading
}

// IsPlanSubstantive is _is_plan_substantive_for_type.
func IsPlanSubstantive(body, missionType string) bool {
	d, ok := planDecls[missionType]
	if !ok || !fieldSubstantive(body, d.primary) {
		return false
	}
	for _, p := range d.peers {
		if fieldSubstantive(body, p) {
			return true
		}
	}
	return false
}

// DescribeTechnicalContextGap is describe_technical_context_gap.
func DescribeTechnicalContextGap(body, missionType string) (string, bool) {
	d, ok := planDecls[missionType]
	if !ok {
		return "No field declaration is registered for mission type " + pyRepr(missionType) + "; plan.md's substantive-content gate cannot evaluate this mission type until a declaration entry is added for it.", true
	}
	status := func(f fieldSpec) string {
		if _, ok := extractSection(body, f.heading); !ok {
			return "absent"
		}
		if fieldSubstantive(body, f) {
			return "ok"
		}
		return "placeholder"
	}
	container, primary := d.primary.heading, fieldDisplayName(d.primary)
	switch status(d.primary) {
	case "absent":
		return container + " section is missing from plan.md.", true
	case "placeholder":
		return container + " **" + primary + "** is missing or carries only placeholder content.", true
	}
	for _, p := range d.peers {
		if status(p) == "ok" {
			return "", false
		}
	}
	var names []string
	hint := ""
	for _, p := range d.peers {
		names = append(names, fieldDisplayName(p))
		if p.kind == "bold" || p.kind == "any" {
			hint = " (bulleted '- **Field**: value' fields are accepted — populate at least one peer field)"
		}
	}
	return container + " has **" + primary + "** but no peer field with non-placeholder content" + hint + ". Checked peer(s): " + strings.Join(names, ", ") + ".", true
}

// DescribePlanFieldRequirements is describe_plan_field_requirements.
func DescribePlanFieldRequirements(missionType string) (container, primary, examplePeer string, ok bool) {
	d, ok := planDecls[missionType]
	if !ok {
		return "", "", "", false
	}
	primary = fieldDisplayName(d.primary)
	examplePeer = primary
	if len(d.peers) > 0 {
		examplePeer = fieldDisplayName(d.peers[0])
	}
	return d.primary.heading, primary, examplePeer, true
}

func pyRepr(s string) string { return "'" + strings.ReplaceAll(s, "'", `\'`) + "'" }

var (
	scopeLine = regexp.MustCompile(`(?m)^\s*\*\*Scope\*\*:\s*$`)
)

func hasSubstantiveResearchSpec(body string) bool {
	tpl, _ := assets.ReadFile("packs/built-in/missions/research/templates/research-spec-template.md")
	shapes := reqmap.DeclaredShapePatterns("research")
	tplLines := map[string]string{}
	for _, l := range splitLines(string(tpl)) {
		if m := shapes[2].FindStringSubmatch(l); m != nil {
			tplLines[m[1]] = strings.TrimSpace(l)
		}
	}
	sec, ok := extractSection(body, "Research Question & Scope")
	if !ok || !scopeLine.MatchString(sec) {
		return false
	}
	for _, label := range []string{"Primary Research Question", "In Scope"} {
		re := regexp.MustCompile(`(?m)^\s*(?:-\s*)?\*\*` + regexp.QuoteMeta(label) + `\*\*:\s*(.+)$`)
		m := re.FindStringSubmatch(sec)
		if m == nil || strings.Contains(m[1], "[") || !IsSubstantiveText(m[1]) {
			return false
		}
	}
	reqs, ok := extractSection(body, "Research Requirements")
	if !ok {
		return false
	}
	found := map[string]bool{}
	for _, line := range splitLines(reqmap.BlankHTMLComments(reqs)) {
		m := shapes[2].FindStringSubmatchIndex(line)
		if m == nil || reqmap.IsCompoundTail(line, m[3]) {
			continue
		}
		id, ok := reqmap.Parse(line[m[2]:m[3]], "research")
		if !ok || (id.Kind != "DR" && id.Kind != "AR" && id.Kind != "QR") {
			continue
		}
		desc := strings.TrimLeft(line[m[3]:], "* :\t-")
		if desc != "" && !strings.Contains(desc, "[") && IsSubstantiveText(desc) && strings.TrimSpace(line) != tplLines[id.Canonical()] {
			found[id.Kind] = true
		}
	}
	return len(found) == 3
}

// IsSubstantive is is_substantive(file, kind, mission_type).
func IsSubstantive(path, kind, missionType string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	body := string(b)
	if kind == "spec" {
		if missionType == "research" {
			return hasSubstantiveResearchSpec(body)
		}
		return HasSubstantiveFRRow(body)
	}
	return IsPlanSubstantive(body, missionType)
}
