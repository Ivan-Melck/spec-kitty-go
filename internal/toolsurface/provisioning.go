package toolsurface

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Provisioning is charter.activation.compiler._PreparedMissionTypeActivations:
// the exact activation-authority bytes that seed mission_type_activations
// from the built-in default preset when the key is absent.
type Provisioning struct {
	Target        string // absolute
	Rel           string // root-relative POSIX
	Before        []byte // nil when absent
	BeforeState   FileState
	Desired       []byte
	Mode          int
	AbsentParents []string // root-relative POSIX, outermost first
	Reason        string   // key_present | key_missing
}

// Changed reports whether applying the preparation writes bytes.
func (p *Provisioning) Changed() bool {
	return p.Before == nil || string(p.Before) != string(p.Desired)
}

// PrepareProvisioning is prepare_mission_type_activations.
func PrepareProvisioning(root string) (*Provisioning, error) {
	t, err := charter.LoadWriteTarget(root)
	if err != nil {
		return nil, err
	}
	present := t.Data.Has("mission_type_activations")
	reason := "key_missing"
	var values []any
	if present {
		reason = "key_present"
		l, ok := t.Data.Get("mission_type_activations")
		list, isList := l.([]any)
		if !ok || !isList {
			return nil, errors.New("mission_type_activations must be a list of strings")
		}
		for _, v := range list {
			if _, isStr := v.(string); !isStr {
				return nil, errors.New("mission_type_activations must be a list of strings")
			}
		}
		values = list
	} else {
		types, err := kittify.DefaultPresetMissionTypes()
		if err != nil {
			return nil, err
		}
		for _, x := range types {
			values = append(values, x)
		}
	}
	p := &Provisioning{Target: t.Path, Reason: reason}
	p.Rel = relPosix(root, t.Path)
	if !t.IsCharter {
		before, desired, err := kittify.RenderConfigUpdate(root, func(cfg *jsonx.Object) {
			if !present {
				cfg.Set("mission_type_activations", values)
			}
		})
		if err != nil {
			return nil, err
		}
		if present && before != nil {
			desired = before
		}
		p.Before, p.Desired = before, desired
	} else {
		before, err := os.ReadFile(t.Path)
		if err != nil {
			return nil, err
		}
		cur, _ := yamlx.ParseObject(before)
		if cur == nil {
			cur = &jsonx.Object{}
		}
		if !present {
			cur.Set("mission_type_activations", values)
		}
		p.Before = before
		p.Desired = []byte(charter.RenderYAMLDocument(before, cur, 4096))
	}
	st, err := observeNode(t.Path)
	if err != nil {
		return nil, err
	}
	p.BeforeState = st
	p.Mode = 0o644
	if st.Mode >= 0 {
		p.Mode = st.Mode
	}
	for _, d := range parentsOf(p.Rel) {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(d))); os.IsNotExist(err) {
			p.AbsentParents = append(p.AbsentParents, d)
		}
	}
	return p, nil
}

// Effects is PreparedUpgradeRepairs.provisioning_effects.
func (p *Provisioning) Effects(root Root) []Effect {
	if p == nil || !p.Changed() {
		return nil
	}
	proof := []Proof{{"managed_path", "charter.activation.compiler:mission_type_activations"}}
	var out []Effect
	for _, d := range p.AbsentParents {
		out = append(out, NewEffect("provisioning", "provisioning", root, d, "create", Absent(), DirOf(0o755), "Activation target parent", proof, []string{"project"}, nil))
	}
	before := Absent()
	action := "create"
	if p.Before != nil {
		before = FileOf(Digest(p.Before), p.Mode)
		action = "update"
	}
	return append(out, NewEffect("provisioning", "provisioning", root, p.Rel, action, before, FileOf(Digest(p.Desired), p.Mode), p.Reason, proof, []string{"project"}, nil))
}

// IsConfigError reports a charter/doctrine configuration error.
func IsConfigError(err error) bool {
	var ce *dct.ConfigError
	return errors.As(err, &ce)
}
