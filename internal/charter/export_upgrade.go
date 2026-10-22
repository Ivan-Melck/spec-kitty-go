package charter

import "github.com/spec-kitty/go-kitty3/internal/jsonx"

// DefaultGovernance is GovernanceConfig().model_dump(mode="json",
// exclude_none=True): the governance block a freshly composed charter.yaml
// carries (used by the upgrade migrations that compose charter.yaml).
func DefaultGovernance() *jsonx.Object { return defaultGovernance() }

// HashContent is charter.hasher.hash_content (BOM/newline-agnostic
// "sha256:<hex>").
func HashContent(content string) string { return hashContent(content) }
