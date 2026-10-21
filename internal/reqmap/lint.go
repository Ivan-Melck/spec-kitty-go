package reqmap

import (
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// RuleLowercaseSuffix is lint.RULE_LOWERCASE_SUFFIX.
const RuleLowercaseSuffix = "write the letter suffix in lowercase (FR-006a); replace placeholders such as FR-00N with a real ID"

const warningRemediation = "Declare it in a recognised shape (a table row, an id-naming heading, a bulleted or numbered item, or a bold-led paragraph), or if it belongs to another mission, cite it as <mission-slug>#<ID>."

// LintError is InvalidRequirementId.
type LintError struct {
	Token string
	Line  int
	Rule  string
}

// LintWarning is RequirementIdWarning.
type LintWarning struct {
	Token   string
	Line    int
	Message string
}

// Object renders as_dict().
func (e LintError) Object() *jsonx.Object {
	return jsonx.NewObject("token", e.Token, "line", e.Line, "rule", e.Rule)
}

// Object renders as_dict().
func (w LintWarning) Object() *jsonx.Object {
	return jsonx.NewObject("token", w.Token, "line", w.Line, "message", w.Message)
}

var researchMalformedLead = regexp.MustCompile(`^(?:\s*\|\s*(?:\*\*|~~){0,2}|#{1,6}\s*|\s*(?:[-*]|\d+\.)\s*\*{0,2}|\s*\*\*)(?P<lead>(?:` + kindAltFor("research") + `)[-_][0-9A-Z][A-Za-z0-9_.\-]*)`)

func normaliseLead(t string) string {
	s := strings.Trim(t, "*~")
	if s != "" && strings.ContainsAny(s[len(s)-1:], ".:,") {
		s = s[:len(s)-1]
	}
	return s
}

func wellFormed(token, mt string) bool {
	c := Canonical(token, mt)
	if c == "" {
		return false
	}
	m := FindAll(token, true, mt)
	return len(m) == 1 && m[0].Canonical() == c
}

// LintSpec is lint_spec_requirement_ids: (blocking errors, warnings).
func LintSpec(spec, missionType string) ([]LintError, []LintWarning) {
	visible := BlankHTMLComments(spec)
	declared := map[string]bool{}
	for _, id := range ParseSpecIDs(spec, missionType).All {
		declared[id] = true
	}
	lead := MalformedDeclaredLead
	if missionType == "research" {
		lead = researchMalformedLead
	}
	var errs []LintError
	var warns []LintWarning
	warned := map[string]bool{}
	for i, line := range pySplitLines(visible) {
		n := i + 1
		if m := lead.FindStringSubmatch(line); m != nil {
			tok := normaliseLead(m[lead.SubexpIndex("lead")])
			if !wellFormed(tok, missionType) {
				rule := RuleText
				if _, ok := Parse(tok, missionType); ok {
					rule = RuleLowercaseSuffix
				}
				errs = append(errs, LintError{Token: tok, Line: n, Rule: rule})
				continue
			}
		}
		shape := false
		for _, p := range DeclaredShapePatterns(missionType) {
			if p.MatchString(line) {
				shape = true
				break
			}
		}
		if shape {
			continue
		}
		for _, id := range FindAll(line, true, missionType) {
			if id.Mission != "" {
				continue
			}
			c := id.Canonical()
			if declared[c] || warned[c] {
				continue
			}
			warned[c] = true
			warns = append(warns, LintWarning{Token: c, Line: n, Message: c + " looks like a requirement ID but this spec does not declare it. " + warningRemediation})
		}
	}
	return errs, warns
}
