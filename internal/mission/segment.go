package mission

import (
	"fmt"
	"strings"
)


// UnsafeSegmentReason returns core.paths.assert_safe_path_segment's error
// text for v, or "" when v is a safe single path segment.
func UnsafeSegmentReason(v string) string {
	s := strings.TrimSpace(v)
	r := PyRepr(v)
	switch {
	case s == "":
		return fmt.Sprintf("Not a safe path segment: %s — value must not be empty or whitespace-only.", r)
	case s != v:
		return fmt.Sprintf("Not a safe path segment: %s — value must not contain leading or trailing whitespace.", r)
	case strings.Contains(s, ".."):
		return fmt.Sprintf("Not a safe path segment: %s — value must not contain '..' (traversal guard).", r)
	case strings.HasPrefix(s, "."):
		return fmt.Sprintf("Not a safe path segment: %s — value must not begin with '.' (traversal guard).", r)
	case strings.ContainsAny(s, `/\`):
		return fmt.Sprintf("Not a safe path segment: %s — value must not contain path separators.", r)
	case !safeSegmentRe.MatchString(s):
		return fmt.Sprintf("Not a safe path segment: %s — value must match the canonical segment grammar "+
			"(ASCII alphanumerics, hyphens, underscores, and interior dots only; must begin with an alphanumeric character).", r)
	}
	return ""
}
