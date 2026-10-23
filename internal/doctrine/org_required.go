package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// OrgRegistryReader supplies the charter registry's strict schema and path
// diagnostics without making the offering package depend on charter.
var OrgRegistryReader func(string, bool) ([]OrgPack, error)

type OrgCharterDoc struct {
	Declared, Member string
	Raw              *jsonx.Object
}

func orgFloorError(message string) error {
	return &ConfigError{message + "\nThe org required floor cannot be read, so nothing was changed. Fix the org pack, or run `spec-kitty doctor charter-packs`."}
}

func OrgCharterDocs(root string, strict bool) ([]OrgCharterDoc, error) {
	packs := OrgPacks(root)
	if OrgRegistryReader != nil {
		var err error
		packs, err = OrgRegistryReader(root, strict)
		if err != nil {
			return nil, orgFloorError("The org pack registry in .kittify/config.yaml cannot be read: " + err.Error())
		}
	}
	docs := map[string]*jsonx.Object{}
	for _, p := range packs {
		if st, err := os.Stat(p.Root); strict && (err != nil || !st.IsDir()) {
			return nil, orgFloorError(fmt.Sprintf("org pack %s is declared but its path is not an existing directory (%s); run `spec-kitty charter fetch --pack %s`.", PyRepr(p.Name), p.Root, p.Name))
		}
		path := filepath.Join(p.Root, "org-charter.yaml")
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		var raw any
		if err == nil {
			raw, err = yamlx.Parse(b)
		}
		if err != nil {
			if strict {
				return nil, orgFloorError(fmt.Sprintf("cannot read %s of org pack %s: %v", path, PyRepr(p.Name), err))
			}
			continue
		}
		o, ok := raw.(*jsonx.Object)
		if !ok {
			if strict {
				return nil, orgFloorError(fmt.Sprintf("%s of org pack %s is not a YAML mapping.", path, PyRepr(p.Name)))
			}
			continue
		}
		if docs[p.Name] == nil {
			docs[p.Name] = o
		}
	}
	var out []OrgCharterDoc
	for _, p := range packs {
		if docs[p.Name] == nil {
			continue
		}
		var chain []string
		var problem string
		for name := p.Name; name != ""; {
			if slices.Contains(chain, name) {
				problem = "Cycle detected in extends: chain: " + strings.Join(append(chain, name), " → ")
				break
			}
			o := docs[name]
			if o == nil {
				problem = fmt.Sprintf("Base layer %s not found. Chain: %s", PyRepr(name), strings.Join(chain, " → "))
				break
			}
			chain = append(chain, name)
			v, _ := o.Get("extends")
			if v == nil || v == "" || v == false {
				name = ""
			} else {
				name = pyStr(v)
			}
		}
		if problem != "" {
			if strict {
				return nil, orgFloorError(fmt.Sprintf("the extends: chain of org pack %s is invalid: %s", PyRepr(p.Name), problem))
			}
			chain = []string{p.Name}
		}
		for _, member := range chain {
			out = append(out, OrgCharterDoc{p.Name, member, docs[member]})
		}
	}
	return out, nil
}

type RequiringPack struct{ Pack, Via string }

func (r RequiringPack) Describe() string {
	if r.Via == "" {
		return PyRepr(r.Pack)
	}
	return PyRepr(r.Pack) + " (via " + PyRepr(r.Via) + ")"
}

func OrgRequiredProvenance(root string, k Kind, strict bool, ix *IdentityIndex) (map[string][]RequiringPack, error) {
	docs, err := OrgCharterDocs(root, strict)
	if err != nil {
		return nil, err
	}
	out := map[string][]RequiringPack{}
	for _, doc := range docs {
		ids, err := orgRequiredValues(doc, k.Plural(), strict)
		if err != nil {
			return nil, err
		}
		r := RequiringPack{Pack: doc.Member}
		if doc.Declared != doc.Member {
			r.Via = doc.Declared
		}
		for _, id := range ids {
			stem := ix.Stem(k, id)
			if !slices.Contains(out[stem], r) {
				out[stem] = append(out[stem], r)
			}
		}
	}
	return out, nil
}

func orgRequiredValues(doc OrgCharterDoc, plural string, strict bool) ([]string, error) {
	field := "required_" + plural
	v, _ := doc.Raw.Get(field)
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	var out []string
	for _, item := range list {
		s, isString := item.(string)
		if !isString && strict {
			ok = false
			break
		}
		if !isString {
			s = pyStr(item)
		}
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if !ok {
		if strict {
			return nil, orgFloorError(fmt.Sprintf("`%s` in the org-charter.yaml of org pack %s must be a list of ids.", field, PyRepr(doc.Member)))
		}
		return nil, nil
	}
	return out, nil
}

func OrgRequiredTokens(root, plural string, strict bool) ([]string, error) {
	docs, err := OrgCharterDocs(root, strict)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, doc := range docs {
		ids, err := orgRequiredValues(doc, plural, strict)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out, nil
}
