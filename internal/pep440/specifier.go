package pep440

import (
	"fmt"
	"regexp"
	"strings"
)

type constraint struct {
	operator, text string
	version        Version
	wildcard       bool
}

var constraintPattern = regexp.MustCompile(`^(~=|===|==|!=|<=|>=|<|>)\s*(\S+)$`)

func parseConstraints(text string) ([]constraint, error) {
	var out []constraint
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		invalid := func() ([]constraint, error) { return nil, fmt.Errorf("Invalid specifier: '%s'", part) }
		match := constraintPattern.FindStringSubmatch(part)
		if match == nil {
			return invalid()
		}
		c := constraint{operator: match[1], text: match[2]}
		if c.operator == "===" {
			out = append(out, c)
			continue
		}
		value := c.text
		if strings.HasSuffix(value, ".*") {
			if c.operator != "==" && c.operator != "!=" {
				return invalid()
			}
			c.wildcard = true
			value = strings.TrimSuffix(value, ".*")
		}
		v, ok := Parse(value)
		if !ok {
			return invalid()
		}
		c.version = v
		if c.wildcard && (v.PreKind != 3 || v.post != -1 || v.Dev != -1 || strings.Contains(value, "+")) {
			return invalid()
		}
		if strings.Contains(value, "+") && c.operator != "==" && c.operator != "!=" {
			return invalid()
		}
		if c.operator == "~=" && len(v.release) < 2 {
			return invalid()
		}
		out = append(out, c)
	}
	return out, nil
}
func ValidateSpecifier(text string) error { _, err := parseConstraints(text); return err }
func (v Version) prerelease() bool        { return v.PreKind != 3 || v.Dev != -1 }
func sameRelease(left, right Version) bool {
	if left.epoch != right.epoch {
		return false
	}
	for i := 0; i < max(len(left.release), len(right.release)); i++ {
		a, b := 0, 0
		if i < len(left.release) {
			a = left.release[i]
		}
		if i < len(right.release) {
			b = right.release[i]
		}
		if a != b {
			return false
		}
	}
	return true
}
func prefixRelease(v Version, parts []int, epoch int) bool {
	if v.epoch != epoch {
		return false
	}
	for i, part := range parts {
		value := 0
		if i < len(v.release) {
			value = v.release[i]
		}
		if part != value {
			return false
		}
	}
	return true
}

// Satisfies follows SpecifierSet membership, including packaging 26's acceptance of a prerelease when checking one version.
func Satisfies(version, text string) (bool, error) {
	constraints, err := parseConstraints(text)
	if err != nil {
		return false, err
	}
	value, ok := Parse(version)
	if !ok {
		return false, nil
	}

	for _, c := range constraints {
		match := false
		cmp := value.Compare(c.version)
		switch c.operator {
		case "===":
			match = strings.EqualFold(version, c.text)
		case "==", "!=":
			if c.wildcard {
				match = prefixRelease(value, c.version.release, c.version.epoch)
			} else {
				match = cmp == 0
				if _, local, has := strings.Cut(c.text, "+"); has {
					_, actual, exists := strings.Cut(version, "+")
					match = match && exists && strings.EqualFold(actual, local)
				}
			}
			if c.operator == "!=" {
				match = !match
			}
		case ">=":
			match = cmp >= 0
		case "<=":
			match = cmp <= 0
		case ">":
			match = cmp > 0 && !(c.version.post == -1 && value.post != -1 && sameRelease(value, c.version))
		case "<":
			match = cmp < 0 && !(!c.version.prerelease() && value.prerelease() && sameRelease(value, c.version))
		case "~=":
			match = cmp >= 0 && prefixRelease(value, c.version.release[:len(c.version.release)-1], c.version.epoch)
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}
