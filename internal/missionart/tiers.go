package missionart

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Resolution tiers (charter.offering.resolver.ResolutionTier names).
const (
	TierOverride       = "OVERRIDE"
	TierLegacy         = "LEGACY"
	TierOrg            = "ORG"
	TierGlobalMission  = "GLOBAL_MISSION"
	TierGlobal         = "GLOBAL"
	TierPackageDefault = "PACKAGE_DEFAULT"
)

// TemplateResolution is a ResolutionResult. Path is the filesystem path, or
// for TierPackageDefault the embedded asset path
// (packs/built-in/missions/<mission>/<subdir>/<name>).
type TemplateResolution struct {
	Tier    string
	Path    string
	Content []byte
}

func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ResolveAssetTier is charter.offering.resolver._resolve_asset(name, subdir,
// root, mission): mission-scoped then global override, legacy, org packs,
// ~/.kittify mission and global, then the packaged default.
func ResolveAssetTier(name, subdir, root, mission string) (*TemplateResolution, bool) {
	kittify := filepath.Join(root, ".kittify")
	try := func(tier, p string) (*TemplateResolution, bool) {
		if !isRegular(p) {
			return nil, false
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, false
		}
		return &TemplateResolution{Tier: tier, Path: p, Content: b}, true
	}
	for _, c := range []struct{ tier, path string }{
		{TierOverride, filepath.Join(kittify, "overrides", "missions", mission, subdir, name)},
		{TierOverride, filepath.Join(kittify, "overrides", subdir, name)},
		{TierLegacy, filepath.Join(kittify, subdir, name)},
	} {
		if r, ok := try(c.tier, c.path); ok {
			return r, true
		}
	}
	for _, org := range charter.ResolvePackChain(root) {
		if r, ok := try(TierOrg, filepath.Join(org, "missions", mission, subdir, name)); ok {
			return r, true
		}
	}
	home := project.GlobalKittify()
	if r, ok := try(TierGlobalMission, filepath.Join(home, "missions", mission, subdir, name)); ok {
		return r, true
	}
	if r, ok := try(TierGlobal, filepath.Join(home, subdir, name)); ok {
		return r, true
	}
	ap := "packs/built-in/missions/" + mission + "/" + subdir + "/" + name
	if b, err := assets.ReadFile(ap); err == nil {
		return &TemplateResolution{Tier: TierPackageDefault, Path: ap, Content: b}, true
	}
	return nil, false
}
