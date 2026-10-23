package charter

import (
	"os"
	"path/filepath"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const charterSoundnessRemediation = "Remediation: fix the YAML in the charter file named above, or re-run `spec-kitty charter generate --from-interview` to regenerate it."

// AssertCharterYAMLSound runs before synthesis can seed, rename, or promote
// content. A valid empty activation store is not a substantive charter.
func AssertCharterYAMLSound(root string) error {
	cfg, err := dct.LoadConfig(root)
	if err != nil {
		return err
	}
	pointer := dct.CharterPointer(root, cfg)
	p := pointer
	if p == "" {
		p = filepath.Join(root, CharterYAML)
	}
	display := filepath.ToSlash(displayPath(p, root))
	fail := func(message string) error {
		return &dct.ConfigError{Body: message + "\nNothing was written.\n" + charterSoundnessRemediation}
	}
	content, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		if pointer != "" {
			return fail(".kittify/config.yaml 'charter:' pointer names " + display + ", which does not exist.")
		}
		return nil
	}
	if err != nil {
		return fail("Invalid YAML in " + display + ": " + err.Error())
	}
	if _, err := pyjson.DecodeUTF8(content); err != nil {
		return fail("Invalid YAML in " + display + ": " + err.Error())
	}
	data, err := yamlx.Parse(content)
	if err != nil {
		return fail("Invalid YAML in " + display + ": " + err.Error())
	}
	if data == nil {
		data = &jsonx.Object{}
	}
	mapping, ok := data.(*jsonx.Object)
	if !ok {
		return fail(display + " root must be a mapping.")
	}
	if mapping.Len() == 0 {
		return fail(display + " is not a sound charter: it has no content (an empty mapping).")
	}
	return nil
}
