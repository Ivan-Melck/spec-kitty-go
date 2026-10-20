// Package mission implements mission identity (mission_id / mid8 / slug
// rules), meta.json persistence, `--mission` handle resolution and mission
// listing, mirroring upstream specify_cli.mission_metadata,
// lanes/branch_naming.py and context/mission_resolver.py.
package mission

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

var (
	// KebabPattern validates a create-time slug (KEBAB_CASE_PATTERN).
	KebabPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9]*(-[a-z0-9]+)*$`)
	// Mid8Pattern is the canonical mid8 grammar (Crockford base32).
	Mid8Pattern     = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{8}$`)
	numericPrefixRe = regexp.MustCompile(`^\d{3}-(.+)$`)
	missionNumberRe = regexp.MustCompile(`^(\d+)-`)
	prefix3Re       = regexp.MustCompile(`^(\d{3})-`)
	resolverULIDRe  = regexp.MustCompile(`^[0-9A-Z]{26}$`)
	resolverMid8Re  = regexp.MustCompile(`^[0-9A-Z]{8}$`)
	safeSegmentRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// NewMissionID mints a fresh mission ULID.
func NewMissionID() string { return ulid.New() }

// ValidSlug reports whether s is a valid create-time kebab-case slug.
func ValidSlug(s string) bool { return KebabPattern.MatchString(s) }

// StripNumericPrefix strips exactly one leading "NNN-" (non-empty remainder).
func StripNumericPrefix(slug string) string {
	if m := numericPrefixRe.FindStringSubmatch(slug); m != nil && m[1] != "" {
		return m[1]
	}
	return slug
}

// Mid8FromSlug returns an embedded 8-char Crockford tail ("" when absent).
func Mid8FromSlug(slug string) string {
	i := strings.LastIndexByte(slug, '-')
	if i < 0 {
		return ""
	}
	if tail := slug[i+1:]; Mid8Pattern.MatchString(tail) {
		return tail
	}
	return ""
}

// ResolveMid8 derives the mid8 from a declared mission_id (resolve_mid8):
// "" when no mission_id is known.
func ResolveMid8(slug, missionID string) string {
	if len(missionID) >= 8 {
		return missionID[:8]
	}
	return ""
}

// DirName is mission_dir_name(slug, mid8): idempotent on an embedded mid8,
// otherwise NNN- stripped and "-<mid8>" appended.
func DirName(slug, mid8 string) string {
	if strings.HasSuffix(slug, "-"+mid8) {
		return slug
	}
	return StripNumericPrefix(slug) + "-" + mid8
}

// CoordMissionDirName is the verbatim coordination grammar (no NNN- strip).
func CoordMissionDirName(slug, mid8 string) string {
	if mid8 == "" {
		return slug
	}
	if strings.HasSuffix(slug, "-"+mid8) {
		return slug
	}
	return slug + "-" + mid8
}

// NumberFromSlug extracts a leading "<digits>-" mission number.
func NumberFromSlug(slug string) (int, bool) {
	m := missionNumberRe.FindStringSubmatch(strings.TrimSpace(slug))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// DefaultFriendlyName is default_mission_display_name: words of the input slug.
func DefaultFriendlyName(slug string) string { return strings.Join(strings.Split(slug, "-"), " ") }

// SafePathSegment reports whether v is a single safe path segment
// (assert_safe_path_segment).
func SafePathSegment(v string) bool {
	s := strings.TrimSpace(v)
	if s == "" || s != v || strings.Contains(s, "..") || strings.HasPrefix(s, ".") ||
		strings.ContainsAny(s, `/\`) {
		return false
	}
	return safeSegmentRe.MatchString(s)
}
