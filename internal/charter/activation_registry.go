package charter

import (
	"fmt"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func init() { dct.OrgRegistryReader = activationOrgRegistry }

func activationOrgRegistry(root string, strict bool) ([]dct.OrgPack, error) {
	if strict {
		cfg, err := dct.LoadConfig(root)
		if err != nil {
			return nil, fmt.Errorf("Failed to read .kittify/config.yaml: %s", err)
		}
		for _, path := range [][]string{{"charter_packs"}, {"charter_packs", "org"}} {
			cur := cfg
			for _, key := range path[:len(path)-1] {
				cur = cur.GetObject(key)
				if cur == nil {
					break
				}
			}
			if cur == nil {
				continue
			}
			v, ok := cur.Get(path[len(path)-1])
			if !ok || v == nil {
				continue
			}
			if _, ok := v.(*jsonx.Object); !ok {
				return nil, fmt.Errorf("Invalid org-pack config in .kittify/config.yaml: `%s` must be a mapping, got %s", strings.Join(path, "."), rtTypeName(v))
			}
		}
	}
	reg := LoadPackRegistry(root)
	if strict && reg.Warning != "" {
		w := strings.TrimPrefix(reg.Warning, "Invalid org-pack config; ignoring org layer: ")
		return nil, fmt.Errorf("Invalid org-pack config in .kittify/config.yaml: %s", w)
	}
	var out []dct.OrgPack
	for _, p := range reg.Packs {
		r, err := p.EffectiveRoot(root)
		if err != nil {
			if strict {
				return nil, err
			}
			return nil, nil
		}
		out = append(out, dct.OrgPack{Name: p.Name, Root: r})
	}
	return out, nil
}
