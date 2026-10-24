package kittify

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// DefaultPresetPath is the built-in pack's default activation preset.
const DefaultPresetPath = "packs/built-in/presets/default.yaml"

// DefaultPresetError is DefaultPresetMissingError (code DEFAULT_PRESET_MISSING).
type DefaultPresetError struct{ Body string }

func (e *DefaultPresetError) Error() string { return e.Body }

// Code is the stable error code.
func (e *DefaultPresetError) Code() string { return "DEFAULT_PRESET_MISSING" }

// DefaultPresetMissionTypes reads the default preset's mission types.
func DefaultPresetMissionTypes() ([]string, error) {
	b, err := assets.ReadFile(DefaultPresetPath)
	if err != nil {
		return nil, &DefaultPresetError{fmt.Sprintf("Built-in default preset %s does not exist", DefaultPresetPath)}
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return nil, &DefaultPresetError{fmt.Sprintf("Built-in default preset %s is malformed (%v)", DefaultPresetPath, err)}
	}
	var out []string
	for _, x := range o.GetList("mission_type_activations") {
		out = append(out, fmt.Sprint(x))
	}
	if len(out) == 0 {
		return nil, &DefaultPresetError{fmt.Sprintf("Built-in default preset %s declares no non-empty 'mission_type_activations' list", DefaultPresetPath)}
	}
	return out, nil
}

// ProvisionDefaultMissionTypeActivations seeds mission_type_activations
// when the key is absent (provisioning.default_charter). Returns whether
// config.yaml changed.
func ProvisionDefaultMissionTypeActivations(root string) (bool, error) {
	types, err := DefaultPresetMissionTypes()
	if err != nil {
		return false, err
	}
	cfg := ReadConfig(root)
	if cfg.Has("mission_type_activations") {
		return false, nil
	}
	l := make([]any, len(types))
	for i, t := range types {
		l[i] = t
	}
	cfg.Set("mission_type_activations", l)
	return true, SaveConfig(root, cfg)
}
