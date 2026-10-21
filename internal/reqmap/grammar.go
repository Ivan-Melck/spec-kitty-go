// Package reqmap ports upstream's requirement-ID grammar
// (requirement_mapping/grammar.py) and the requirement-mapping helpers
// (requirement_mapping/__init__.py): declared-ID scanning of spec.md,
// coverage, raw requirement_refs tokenisation and the FR-019 verdict table.
package reqmap

import (
	"fmt"
	"regexp"
	"strings"
)

// ID is a parsed requirement id (RequirementId).
type ID struct {
	Kind    string
	Digits  string
	Suffix  string // "" = None (lowercase)
	Mission string // "" = None (qualifier)
}

// Canonical is the unqualified canonical string.
func (r ID) Canonical() string { return r.Kind + "-" + r.Digits + r.Suffix }

// String is the qualified form when foreign.
func (r ID) String() string {
	if r.Mission != "" {
		return r.Mission + "#" + r.Canonical()
	}
	return r.Canonical()
}

// IsFunctional reports an FR id.
func (r ID) IsFunctional() bool { return r.Kind == "FR" }

const (
	kindAlt         = "FR|NFR|SC|C"
	slugRe          = `[a-z0-9][a-z0-9-]*(?:-[0-9A-Z]{8})?`
	kindDigits      = `(?i:` + kindAlt + `)-\d+`
	declaredCore    = kindDigits + `(?:[a-z])?`
	qualifiedDigits = `(?:(?P<mission>` + slugRe + `)#)?(?P<kind>(?i:` + kindAlt + `))-(?P<digits>\d+)`
)

func kindAltFor(missionType string) string {
	if missionType == "research" {
		return "FR|NFR|SC|C|DR|AR|QR"
	}
	return kindAlt
}

var (
	strictRef      = regexp.MustCompile(`^` + qualifiedDigits + `(?P<suffix>[a-zA-Z])?$`)
	strictResearch = regexp.MustCompile(`^(?:(?P<mission>` + slugRe + `)#)?(?P<kind>(?i:` + kindAltFor("research") + `))-(?P<digits>\d+)(?P<suffix>[a-zA-Z])?$`)
	tokenLower     = regexp.MustCompile(`\b` + qualifiedDigits + `(?P<suffix>(?-i:[a-z]))?\b`)
	tokenAny       = regexp.MustCompile(`\b` + qualifiedDigits + `(?P<suffix>[a-zA-Z])?\b`)
	compoundTail   = regexp.MustCompile(`^[-.][A-Za-z0-9]`)
	tokenSplit     = regexp.MustCompile(`[,\s]+`)

	// DeclaredTableRow is DECLARED_TABLE_ROW (group 1 = id).
	DeclaredTableRow = regexp.MustCompile(`^\s*\|\s*(?:\*\*|~~){0,2}(` + declaredCore + `)(?:\*\*|~~){0,2}\s*\|`)
	// DeclaredListItem is DECLARED_LIST_ITEM.
	DeclaredListItem = regexp.MustCompile(`^\s*(?:[-*]|\d+\.)\s*\*{0,2}(` + declaredCore + `)\b`)
	// DeclaredShapes is DECLARED_SHAPE_PATTERNS.
	DeclaredShapes = []*regexp.Regexp{
		DeclaredTableRow,
		regexp.MustCompile(`^#{1,6}\s*(` + declaredCore + `)\b`),
		DeclaredListItem,
		regexp.MustCompile(`^\s*\*\*(` + declaredCore + `)\b`),
	}
	researchCore   = `(?i:` + kindAltFor("research") + `)-\d+(?:[a-z])?`
	researchShapes = []*regexp.Regexp{
		regexp.MustCompile(`^\s*\|\s*(?:\*\*|~~){0,2}(` + researchCore + `)(?:\*\*|~~){0,2}\s*\|`),
		regexp.MustCompile(`^#{1,6}\s*(` + researchCore + `)\b`),
		regexp.MustCompile(`^\s*(?:[-*]|\d+\.)\s*\*{0,2}(` + researchCore + `)\b`),
		regexp.MustCompile(`^\s*\*\*(` + researchCore + `)\b`),
	}
	// MalformedDeclaredLead is MALFORMED_DECLARED_LEAD.
	MalformedDeclaredLead = regexp.MustCompile(`^(?:\s*\|\s*(?:\*\*|~~){0,2}|#{1,6}\s*|\s*(?:[-*]|\d+\.)\s*\*{0,2}|\s*\*\*)(?P<lead>(?:` + kindAlt + `)[-_][0-9A-Z][A-Za-z0-9_.\-]*)`)
	htmlTerminated        = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlUnterminated      = regexp.MustCompile(`(?s)<!--.*`)
)

// DeclaredShapePatterns is declared_shape_patterns(mission_type).
func DeclaredShapePatterns(missionType string) []*regexp.Regexp {
	if missionType == "research" {
		return researchShapes
	}
	return DeclaredShapes
}

func group(re *regexp.Regexp, m []string, name string) string {
	return m[re.SubexpIndex(name)]
}

func normalizeKind(k string) string { return strings.ToUpper(k) }

// Parse is grammar.parse (strict full match).
func Parse(token, missionType string) (ID, bool) {
	re := strictRef
	if missionType == "research" {
		re = strictResearch
	}
	m := re.FindStringSubmatch(token)
	if m == nil {
		return ID{}, false
	}
	return ID{Kind: normalizeKind(group(re, m, "kind")), Digits: group(re, m, "digits"),
		Suffix: strings.ToLower(group(re, m, "suffix")), Mission: group(re, m, "mission")}, true
}

// Canonical is grammar.canonical ("" when unparseable).
func Canonical(token, missionType string) string {
	if id, ok := Parse(token, missionType); ok {
		return id.String()
	}
	return ""
}

// IsCompoundTail is is_compound_tail(text, end) (byte offsets).
func IsCompoundTail(text string, end int) bool {
	if end > len(text) {
		return false
	}
	rest := text[end:]
	if len(rest) > 2 {
		rest = rest[:2]
	}
	return compoundTail.MatchString(rest)
}

func hasInvalidQualifierPrefix(text string, start int, mission string) bool {
	if start == 0 {
		return false
	}
	lead := text[start-1 : start]
	if lead != "#" && lead != "." {
		return false
	}
	return lead == "#" || mission != ""
}

// FindAll is grammar.find_all.
func FindAll(text string, specScan bool, missionType string) []ID {
	re := tokenAny
	if specScan {
		re = tokenLower
	}
	if missionType == "research" {
		suffix := `(?P<suffix>[a-zA-Z])?`
		if specScan {
			suffix = `(?P<suffix>(?-i:[a-z]))?`
		}
		re = regexp.MustCompile(`\b(?:(?P<mission>` + slugRe + `)#)?(?P<kind>(?i:` + kindAltFor(missionType) + `))-(?P<digits>\d+)` + suffix + `\b`)
	}
	var out []ID
	for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
		if IsCompoundTail(text, loc[1]) {
			continue
		}
		get := func(name string) string {
			i := re.SubexpIndex(name)
			if loc[2*i] < 0 {
				return ""
			}
			return text[loc[2*i]:loc[2*i+1]]
		}
		mission := get("mission")
		if hasInvalidQualifierPrefix(text, loc[0], mission) {
			continue
		}
		out = append(out, ID{Kind: normalizeKind(get("kind")), Digits: get("digits"), Suffix: strings.ToLower(get("suffix")), Mission: mission})
	}
	return out
}

// TokenizeRefs is grammar.tokenize_refs over a decoded frontmatter value.
func TokenizeRefs(value any) []string {
	var tokens []string
	split := func(s string) {
		for _, t := range tokenSplit.Split(s, -1) {
			if strings.TrimSpace(t) != "" {
				tokens = append(tokens, t)
			}
		}
	}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				split(s)
			} else {
				tokens = append(tokens, fmt.Sprintf("<NON_STRING:%s>", pyStr(item)))
			}
		}
	case string:
		split(v)
	}
	return tokens
}

func pyStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case int:
		return fmt.Sprint(x)
	case int64:
		return fmt.Sprint(x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d.0", int64(x))
		}
		return fmt.Sprint(x)
	}
	return fmt.Sprint(v)
}

// BlankHTMLComments is blank_html_comments (position preserving).
func BlankHTMLComments(text string) string {
	blank := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r == '\n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		}
		return b.String()
	}
	text = htmlTerminated.ReplaceAllStringFunc(text, blank)
	return htmlUnterminated.ReplaceAllStringFunc(text, blank)
}

// Verdict reasons.
const (
	Malformed        = "malformed"
	UnknownSpecID    = "unknown_spec_id"
	ForeignQualified = "foreign_qualified"
	RuleText         = "<kind>-<digits>[<lowercase letter>]"
)

// FailingReasons is FAILING_REASONS.
var FailingReasons = map[string]bool{Malformed: true, UnknownSpecID: true}

// Classify is grammar.classify: (accepted id, "") or (zero, reason).
func Classify(raw string, declared map[string]bool, missionType string) (ID, string) {
	id, ok := Parse(raw, missionType)
	if !ok {
		return ID{}, Malformed
	}
	if id.Mission != "" {
		return ID{}, ForeignQualified
	}
	if !declared[id.Canonical()] {
		return ID{}, UnknownSpecID
	}
	return id, ""
}
