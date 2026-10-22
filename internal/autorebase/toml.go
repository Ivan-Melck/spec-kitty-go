package autorebase

import "github.com/spec-kitty/go-kitty3/internal/pytoml"

// ValidateTOML is the classifier's tomllib.loads(full_file_text) check:
// nil when src is valid TOML, else the TOMLDecodeError (exact tomllib text).
func ValidateTOML(src string) error {
	_, err := pytoml.Loads(src)
	return err
}
