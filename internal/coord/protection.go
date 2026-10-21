package coord

import (
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// HatchEnv is the operator escape hatch for protected-branch commits.
const HatchEnv = "SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS"

// ProtectedRemedy is REMEDY_PROTECTED_PRIMARY.
const ProtectedRemedy = "--start-branch <topic-branch> or SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS=1"

func hatchActive() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(HatchEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ProtectedBranches resolves the protection set: protection.protected_branches
// when configured (exactly), else {main, master} plus the remote default.
func ProtectedBranches(root string) map[string]bool {
	out := map[string]bool{}
	cfg := mission.ProjectConfig(root)
	if p := cfg.GetObject("protection"); p != nil {
		if v, ok := p.Get("protected_branches"); ok {
			if l, ok := v.([]any); ok {
				for _, x := range l {
					if s, ok := x.(string); ok {
						out[s] = true
					}
				}
				return out
			}
		}
	}
	out["main"], out["master"] = true, true
	if ref, err := gitx.Run(root, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(ref), "refs/remotes/origin/"); b != "" {
			out[b] = true
		}
	}
	return out
}

// IsProtected is ProtectionPolicy.resolve_for_mission(root, slug).is_protected(branch).
func IsProtected(root, slug, branch string) bool {
	if hatchActive() {
		return false
	}
	if slug != "" {
		meta := mission.ReadMetaOrEmpty(project.Specs(root, slug))
		if v, ok := meta.Get("commit_to_target"); ok {
			if b, ok := v.(bool); ok && b && branch == meta.GetString("target_branch") {
				return false
			}
		}
	}
	return ProtectedBranches(root)[branch]
}
