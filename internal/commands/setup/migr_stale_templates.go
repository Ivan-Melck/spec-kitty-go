package setup

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
)

type staleTemplateCandidate struct {
	Rel, Key string
	Override bool
}

func staleTemplateScan(root string) (candidates []staleTemplateCandidate, refused []string) {
	for _, tree := range []string{".kittify/missions", ".kittify/overrides/missions"} {
		for _, key := range staleTemplateKeys {
			mission, name, _ := strings.Cut(key, "/")
			rel := tree + "/" + mission + "/templates/" + name
			current, safe := "", true
			for _, part := range strings.Split(path.Dir(rel), "/") {
				current = path.Join(current, part)
				st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(current)))
				if os.IsNotExist(err) {
					safe = false
					break
				}
				if err != nil {
					safe = false
					break
				}
				if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
					if !contains(refused, current) {
						refused = append(refused, current)
					}
					safe = false
					break
				}
			}
			if safe && lexists(filepath.Join(root, filepath.FromSlash(rel))) {
				candidates = append(candidates, staleTemplateCandidate{rel, key, strings.Contains(tree, "/overrides/")})
			}
		}
	}
	return
}

func staleTemplatePack(key string) ([]byte, bool) {
	mission, name, _ := strings.Cut(key, "/")
	content, err := assets.ReadFile("packs/built-in/missions/" + mission + "/templates/" + name)
	return content, err == nil
}

func staleTemplateFingerprint(content []byte) string {
	return toolsurface.Digest(bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n")))
}
func staleTemplateCurrent(root string, c staleTemplateCandidate, pack []byte, exists bool) bool {
	if !exists {
		return false
	}
	p := filepath.Join(root, filepath.FromSlash(c.Rel))
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	content, err := os.ReadFile(p)
	return err == nil && staleTemplateFingerprint(content) == staleTemplateFingerprint(pack)
}
func staleTemplateProver(key string) prover {
	return func(p, root string) *ownershipProof {
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil || !contains(staleTemplateHashes[key], staleTemplateFingerprint(content)) {
			return nil
		}
		return &ownershipProof{kind: "canonical_content", detail: "known-fork:" + displayRel(p, root)}
	}
}
func init() {
	migrationImpls["4.0.0rc8_retire_stale_mission_template_forks"] = migrationImpl{
		detect: func(root string) bool {
			candidates, refused := staleTemplateScan(root)
			if len(refused) > 0 {
				return true
			}
			for _, c := range candidates {
				pack, ok := staleTemplatePack(c.Key)
				if !staleTemplateCurrent(root, c, pack, ok) {
					return true
				}
			}
			return false
		},
		apply: func(root string, dry bool) migrationResult {
			candidates, refused := staleTemplateScan(root)
			r := migrationResult{Success: true}
			for _, rel := range refused {
				r.Warnings = append(r.Warnings, "Stale mission template cleanup skipped "+rel+": it is a symlink or not a directory; nothing there was changed")
			}
			for _, c := range candidates {
				pack, ok := staleTemplatePack(c.Key)
				if staleTemplateCurrent(root, c, pack, ok) {
					continue
				}
				p := filepath.Join(root, filepath.FromSlash(c.Rel))
				pr := staleTemplateProver(c.Key)
				kept := func() {
					r.Warnings = append(r.Warnings, "Kept "+c.Rel+": it differs from every version Spec Kitty shipped, so it is treated as your edit. Delete it to use the template Spec Kitty ships now.")
				}
				var err error
				if c.Override || !ok {
					var verdict ownershipVerdict
					verdict, err = guardDestructiveRemoval(p, root, pr, false, "", dry)
					if err == nil {
						if verdict.owned {
							r.Changes = append(r.Changes, verdict.diagnostic)
						} else {
							kept()
						}
					}
				} else {
					verdict := guardDestructiveOverwrite(p, root, true, false, pr, "")
					if !verdict.proceed {
						kept()
						continue
					}
					if dry {
						r.Changes = append(r.Changes, "Would replace stale "+c.Rel+" with the shipped template")
					} else {
						err = atomicfile.Write(p, pack)
						if err == nil {
							r.Changes = append(r.Changes, "Replaced stale "+c.Rel+" with the shipped template")
						}
					}
				}
				if err != nil {
					r.Errors = append(r.Errors, fmt.Sprintf("Could not repair %s: %s", c.Rel, err))
				}
			}
			r.Success = len(r.Errors) == 0
			return r
		},
	}
}
