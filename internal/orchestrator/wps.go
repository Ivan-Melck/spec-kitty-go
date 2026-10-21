package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// WPEntry is one wps.yaml work package (core.wps_manifest.WorkPackageEntry).
type WPEntry struct {
	ID              string
	Title           string
	Dependencies    []string
	OwnedFiles      []string
	RequirementRefs []string
	Subtasks        []string
	PromptFile      *string
}

// WpsManifest is the parsed wps.yaml outline.
type WpsManifest struct{ WorkPackages []*WPEntry }

// ErrManifestInvalid marks an unreadable / schema-invalid wps.yaml.
var ErrManifestInvalid = errors.New("invalid wps.yaml")

var (
	wpIDRe      = regexp.MustCompile(`^WP\d{2}$`)
	concernRefR = regexp.MustCompile(`^IC-[0-9]{2}$`)
)

// LoadWpsManifest is load_wps_manifest(dir): nil when wps.yaml is absent.
func LoadWpsManifest(dir string) (*WpsManifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "wps.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, ErrManifestInvalid
	}
	return ParseWpsManifest(b)
}

// ParseWpsManifest validates wps.yaml content against the canonical schema.
func ParseWpsManifest(b []byte) (*WpsManifest, error) {
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, ErrManifestInvalid
	}
	o := jsonx.ToObject(v)
	if o == nil {
		return nil, ErrManifestInvalid
	}
	raw, ok := o.Get("work_packages")
	list, isList := raw.([]any)
	if !ok || !isList {
		return nil, ErrManifestInvalid
	}
	m := &WpsManifest{}
	for _, item := range list {
		eo := jsonx.ToObject(item)
		if eo == nil {
			return nil, ErrManifestInvalid
		}
		e := &WPEntry{}
		id, ok1 := getStr(eo, "id")
		title, ok2 := getStr(eo, "title")
		if !ok1 || !ok2 || !wpIDRe.MatchString(id) {
			return nil, ErrManifestInvalid
		}
		e.ID, e.Title = id, title
		var bad bool
		e.Dependencies, bad = strListField(eo, "dependencies")
		if bad {
			return nil, ErrManifestInvalid
		}
		for _, d := range e.Dependencies {
			if !wpIDRe.MatchString(d) {
				return nil, ErrManifestInvalid
			}
		}
		for _, f := range []struct {
			k string
			p *[]string
		}{{"owned_files", &e.OwnedFiles}, {"requirement_refs", &e.RequirementRefs}, {"subtasks", &e.Subtasks}} {
			if *f.p, bad = strListField(eo, f.k); bad {
				return nil, ErrManifestInvalid
			}
		}
		refs, bad := strListField(eo, "plan_concern_refs")
		if bad {
			return nil, ErrManifestInvalid
		}
		for _, r := range refs {
			if !concernRefR.MatchString(r) {
				return nil, ErrManifestInvalid
			}
		}
		if pv, ok := eo.Get("prompt_file"); ok && pv != nil {
			s, isS := pv.(string)
			if !isS {
				return nil, ErrManifestInvalid
			}
			e.PromptFile = &s
		}
		if cv, ok := eo.Get("cross_cutting"); ok {
			if _, isB := cv.(bool); !isB {
				return nil, ErrManifestInvalid
			}
		}
		m.WorkPackages = append(m.WorkPackages, e)
	}
	return m, nil
}

func getStr(o *jsonx.Object, k string) (string, bool) {
	v, ok := o.Get(k)
	s, isS := v.(string)
	return s, ok && isS
}

// strListField reads an optional list[str] field (bad=true on a type error).
func strListField(o *jsonx.Object, k string) ([]string, bool) {
	v, ok := o.Get(k)
	if !ok {
		return []string{}, false
	}
	l, isL := v.([]any)
	if !isL {
		return nil, true
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		s, isS := x.(string)
		if !isS {
			return nil, true
		}
		out = append(out, s)
	}
	return out, false
}
