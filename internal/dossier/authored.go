package dossier

import (
	"os"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ReadAuthoredWP is status.read_authored_wp_frontmatter: the WP file's
// frontmatter parsed like FrontmatterManager.read and validated against the
// strict WPMetadata schema. It returns the static projection (which carries
// the validated work_package_id); ok=false covers every failure upstream
// raises (FrontmatterError, ValidationError, UnicodeDecodeError, OSError).
func ReadAuthoredWP(path string) (*jsonx.Object, bool) {
	b, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(b) {
		return nil, false
	}
	src, meta, ok := readFrontmatter(path)
	if !ok {
		return nil, false
	}
	proj, err := validateWP(meta, timestampKeys(src))
	if err != nil {
		return nil, false
	}
	return proj, true
}
