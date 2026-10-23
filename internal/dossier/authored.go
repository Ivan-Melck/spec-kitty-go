package dossier

import (
	"errors"
	"os"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// PyTypeError is a TypeError that escapes WPMetadata validation (it is not
// a ValidationError, so callers that only catch those abort).
type PyTypeError struct{ Msg string }

func (e *PyTypeError) Error() string { return e.Msg }

// WPProjectionHash is the dossier indexer's _hash_wp_projection over a WP
// file: the static-projection hash (ok=true), a schema failure (verr set:
// the artifact fails closed), or ok=false when the file has no parseable
// frontmatter (callers fall back to the raw-byte hash).
func WPProjectionHash(path string) (hash string, verr error, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(b) {
		return "", nil, false
	}
	src, meta, parsed := readFrontmatter(path)
	if !parsed {
		return "", nil, false
	}
	if te := WPMetadataTypeError(src, meta); te != "" {
		return "", &PyTypeError{te}, false
	}
	h, err := StaticProjectionHash(src, meta)
	if err != nil {
		if msg := WPMetadataValidationError(src, meta); msg != "" {
			return "", errors.New(msg), false
		}
		return "", err, false
	}
	return h, nil, true
}

// ReadAuthoredWP is status.read_authored_wp_frontmatter: the WP file's
// frontmatter parsed like FrontmatterManager.read and validated against the
// strict WPMetadata schema. It returns the static projection (which carries
// the validated work_package_id); ok=false covers every failure upstream
// raises (FrontmatterError, ValidationError, UnicodeDecodeError, OSError).
//
// A *PyTypeError (the shell_pid coercion's TypeError, which upstream does
// not catch) is returned as err.
func ReadAuthoredWP(path string) (*jsonx.Object, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(b) {
		return nil, false, nil
	}
	src, meta, ok := readFrontmatter(path)
	if !ok {
		return nil, false, nil
	}
	if te := WPMetadataTypeError(src, meta); te != "" {
		return nil, false, &PyTypeError{te}
	}
	proj, err := validateWP(meta, timestampKeys(src))
	if err != nil {
		return nil, false, nil
	}
	return proj, true, nil
}

// AuthoredWPValidationError is the error read_authored_wp_frontmatter raises
// for a WP file whose frontmatter parses: str(pydantic ValidationError) for
// a schema failure, or a *PyTypeError (the shell_pid coercion's TypeError).
// "" and nil when WPMetadata accepts it or the frontmatter does not parse
// (callers report that through their own frontmatter reader).
func AuthoredWPValidationError(path string) (string, error) {
	src, meta, ok := readFrontmatter(path)
	if !ok {
		return "", nil
	}
	if te := WPMetadataTypeError(src, meta); te != "" {
		return "", &PyTypeError{te}
	}
	return WPMetadataValidationError(src, meta), nil
}
