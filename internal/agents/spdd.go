package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

var reLeadingDigits = regexp.MustCompile(`^(\d+)`)

// IsSPDDActive is charter.offering.spdd_reasons.activation.is_spdd_reasons_active:
// an absent activated_<kind> key means "all built-ins available" (active).
// root=="" means no project (inactive).
func IsSPDDActive(root string) (bool, error) {
	if root == "" || !kittify.ConfigExists(root) {
		return false, nil
	}
	cfg, err := kittify.LoadConfig(root)
	if err != nil {
		return false, err
	}
	src := cfg
	if p, ok := cfg.Get("charter"); ok {
		if s, isS := p.(string); isS {
			path := s
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return false, fmt.Errorf(".kittify/config.yaml 'charter:' pointer names %s, which does not exist.", path)
			}
			v, err := yamlx.Parse(b)
			if err != nil {
				return false, fmt.Errorf("Invalid YAML in %s: %v", path, err)
			}
			if v == nil {
				src = &jsonx.Object{}
			} else if o, isO := v.(*jsonx.Object); isO {
				src = o
			} else {
				return false, fmt.Errorf("%s root must be a mapping.", path)
			}
		}
	}
	read := func(key string) (map[string]bool, bool, error) {
		raw, ok := src.Get(key)
		if !ok || raw == nil {
			return nil, false, nil
		}
		l, isL := raw.([]any)
		if !isL {
			return nil, false, fmt.Errorf("Activation key '%s' must be a list.", key)
		}
		out := map[string]bool{}
		for _, x := range l {
			out[fmt.Sprint(x)] = true
		}
		return out, true, nil
	}
	par, hasPar, err := read("activated_paradigms")
	if err != nil {
		return false, err
	}
	tac, hasTac, err := read("activated_tactics")
	if err != nil {
		return false, err
	}
	dir, hasDir, err := read("activated_directives")
	if err != nil {
		return false, err
	}
	if !hasPar || par["structured-prompt-driven-development"] {
		return true, nil
	}
	if !hasTac || tac["reasons-canvas-fill"] || tac["reasons-canvas-review"] {
		return true, nil
	}
	if !hasDir {
		return true, nil
	}
	for d := range dir {
		if d == "DIRECTIVE_038" || strings.EqualFold(d, "DIRECTIVE_038") {
			return true, nil
		}
		if m := reLeadingDigits.FindStringSubmatch(d); m != nil {
			n := m[1]
			for len(n) < 3 {
				n = "0" + n
			}
			if n == "038" {
				return true, nil
			}
		}
	}
	return false, nil
}
