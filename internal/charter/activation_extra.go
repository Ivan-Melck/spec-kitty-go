package charter

import (
	"os"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// ConfigIDIndex resolves DRG ids to config stems (exported alias).
type ConfigIDIndex = configIDIndex

// ActiveURNs is deactivate._active_urns: the DRG URN of every explicitly
// activated artifact (kinds with an absent key contribute nothing).
func ActiveURNs(root string) map[string]bool {
	out := map[string]bool{}
	activated, present, err := ListActivated(root)
	if err != nil {
		return out
	}
	lr, chain := dct.LayerRoots(root), dct.OrgRoots(root)
	for tok, ids := range activated {
		if !present[tok] || tok == dct.MissionTypeToken {
			continue
		}
		k, err := dct.ParseKind(tok)
		if err != nil {
			continue
		}
		stems := dct.StemsByDeclaredID(k, lr, chain)
		byStem := map[string]string{}
		for id, stem := range stems {
			byStem[stem] = id
		}
		for _, cid := range ids {
			if id, ok := byStem[cid]; ok {
				out[string(k)+":"+id] = true
			} else if _, ok := stems[cid]; ok {
				out[string(k)+":"+cid] = true
			}
		}
	}
	return out
}

// RecompileCatalog is activate.recompile_catalog: a no-op until
// .kittify/charter/charter.yaml exists, else the generate compile seam.
func RecompileCatalog(root string) error {
	if _, err := os.Stat(Path(root, CharterYAML)); err != nil {
		return nil
	}
	return recompileCompiledCharter(root)
}

var _ = strings.TrimSpace
