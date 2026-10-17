// Package doctrine loads and indexes the doctrine offering: the built-in
// Charter Pack shipped with spec-kitty (directives, tactics, procedures,
// paradigms, styleguides, toolguides, agent profiles, assets, glossary
// packs, mission step contracts, presets, the Doctrine Reference Graph) plus
// org packs and the project layer (.kittify/charter-packs), and the
// project activation state read from .kittify/config.yaml (or the
// charter.yaml it points to).
//
// It mirrors the observable behaviour of upstream charter.offering /
// charter.activation (built-in -> org -> project layering with field-level
// merge, three-state activation, DRG action resolution) without
// reproducing the Python module layout.
package doctrine

import (
	"fmt"
	"strings"
)

// Kind is a canonical artifact kind (singular, underscore form).
type Kind string

// Canonical artifact kinds (upstream ArtifactKind order).
const (
	Directive           Kind = "directive"
	Tactic              Kind = "tactic"
	Styleguide          Kind = "styleguide"
	Toolguide           Kind = "toolguide"
	Paradigm            Kind = "paradigm"
	Procedure           Kind = "procedure"
	AgentProfile        Kind = "agent_profile"
	MissionStepContract Kind = "mission_step_contract"
	Template            Kind = "template"
	Asset               Kind = "asset"
	GlossaryPack        Kind = "glossary_pack"
	AntiPattern         Kind = "anti_pattern"
	Skill               Kind = "skill"
)

// AllKinds lists every artifact kind in upstream enum order.
var AllKinds = []Kind{Directive, Tactic, Styleguide, Toolguide, Paradigm, Procedure, AgentProfile,
	MissionStepContract, Template, Asset, GlossaryPack, AntiPattern, Skill}

var plurals = map[Kind]string{
	Directive: "directives", Tactic: "tactics", Styleguide: "styleguides", Toolguide: "toolguides",
	Paradigm: "paradigms", Procedure: "procedures", AgentProfile: "agent_profiles",
	MissionStepContract: "mission_step_contracts", Template: "templates", Asset: "assets",
	GlossaryPack: "glossary_packs", AntiPattern: "anti_patterns", Skill: "skills",
}

var patterns = map[Kind]string{
	Directive: ".directive.yaml", Tactic: ".tactic.yaml", Styleguide: ".styleguide.yaml",
	Toolguide: ".toolguide.yaml", Paradigm: ".paradigm.yaml", Procedure: ".procedure.yaml",
	AgentProfile: ".agent.yaml", MissionStepContract: ".step-contract.yaml", Template: "",
	Asset: ".asset.yaml", GlossaryPack: ".glossary-pack.yaml", AntiPattern: ".anti_pattern.yaml",
	Skill: ".skill.yaml",
}

// Plural is the directory name of the kind (e.g. "agent_profiles").
func (k Kind) Plural() string { return plurals[k] }

// Suffix is the file-name suffix matched by the kind's glob ("" for template).
func (k Kind) Suffix() string { return patterns[k] }

// Glob is the upstream glob pattern ("*.directive.yaml").
func (k Kind) Glob() string {
	if s := patterns[k]; s != "" {
		return "*" + s
	}
	return ""
}

// OperatorToken is the hyphenated CLI form ("agent-profile").
func (k Kind) OperatorToken() string { return strings.ReplaceAll(string(k), "_", "-") }

// Activatable reports whether the kind can be activated in a charter.
func (k Kind) Activatable() bool { return k != Template && k != Asset }

// HasBuiltInContentDir reports whether packs/built-in/<plural>/ ships content.
func (k Kind) HasBuiltInContentDir() bool {
	switch k {
	case MissionStepContract, Template, AntiPattern:
		return false
	}
	return true
}

// HasLayeredRepository reports whether the kind resolves through a
// built-in + org + project layered repository.
func (k Kind) HasLayeredRepository() bool { return k != Template && k != AntiPattern }

// ErrMissionTypeToken is returned by ParseKind for "mission-type".
var ErrMissionTypeToken = fmt.Errorf("'mission-type' is part of the charter kind universe but is not an ArtifactKind; route it through the mission-tier handler.")

// ParseKind resolves an operator token (hyphen or underscore, any case).
func ParseKind(token string) (Kind, error) {
	n := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(token)), "-", "_")
	if n == "mission_type" {
		return "", ErrMissionTypeToken
	}
	for _, k := range AllKinds {
		if string(k) == n {
			return k, nil
		}
	}
	valid := make([]string, len(AllKinds))
	for i, k := range AllKinds {
		valid[i] = k.OperatorToken()
	}
	return "", fmt.Errorf("Unknown artifact kind token %s. Valid operator tokens: %s.", pyRepr(token), strings.Join(valid, ", "))
}

// KindFromPlural maps a plural directory name to its kind.
func KindFromPlural(p string) (Kind, bool) {
	for k, v := range plurals {
		if v == p {
			return k, true
		}
	}
	return "", false
}

// pyRepr renders s like Python's repr() of a str.
func pyRepr(s string) string {
	q := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		q = "\""
	}
	var b strings.Builder
	b.WriteString(q)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case string(r) == q:
			b.WriteString(`\` + q)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(q)
	return b.String()
}

// PyRepr exposes Python str repr() formatting for error messages.
func PyRepr(s string) string { return pyRepr(s) }
