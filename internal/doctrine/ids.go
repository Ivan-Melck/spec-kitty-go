package doctrine

import (
	"regexp"
	"strings"
)

var (
	reDirectiveCanon = regexp.MustCompile(`^DIRECTIVE_\d+$`)
	reLeadingDigits  = regexp.MustCompile(`^(\d+)`)
)

// NormalizeDirectiveID maps "024-locality-of-change", "24", "DIRECTIVE_024"
// and slug hub ids ("use-c4-model-techniques") to the canonical id.
func NormalizeDirectiveID(raw string) string {
	if reDirectiveCanon.MatchString(raw) {
		return raw
	}
	if m := reLeadingDigits.FindStringSubmatch(raw); m != nil {
		n := m[1]
		for len(n) < 3 {
			n = "0" + n
		}
		return "DIRECTIVE_" + n
	}
	return strings.ReplaceAll(strings.ToUpper(raw), "-", "_")
}

// URN builds "<kind>:<id>" (directive ids normalised).
func URN(k Kind, id string) string {
	if k == Directive {
		return "directive:" + NormalizeDirectiveID(id)
	}
	return string(k) + ":" + id
}

// SplitURN splits "<kind>:<id>".
func SplitURN(urn string) (string, string) {
	k, id, _ := strings.Cut(urn, ":")
	return k, id
}
