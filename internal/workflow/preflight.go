package workflow

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// Charter preflight advisory warnings (charter_runtime.preflight).
const (
	freshProjectMissingCharter = "project charter is not initialized; run `spec-kitty charter generate` when this project is ready for charter-governed workflows"
	legacyCharterBundle        = "a legacy charter.md-only bundle was detected (.kittify/charter/charter.md exists but .kittify/charter/charter.yaml does not); this project has governance intent that predates the charter.yaml bundle format — run `spec-kitty charter generate --no-from-interview` to migrate it forward"
)

// preflightEnabled reads preflight.enabled from .kittify/config.yaml.
func preflightEnabled(root string) bool {
	cfg := mission.ProjectConfig(root)
	if cfg == nil {
		return true
	}
	sec := cfg.GetObject("preflight")
	if sec == nil {
		return true
	}
	v, ok := sec.Get("enabled")
	if !ok || v == nil {
		return true
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

// CharterPreflightWarning returns the advisory warning text the charter
// preflight surfaces for root ("" when none). Freshness of an existing
// charter bundle is not evaluated (area C).
func CharterPreflightWarning(root string) string {
	if !preflightEnabled(root) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, ".kittify", "charter", "charter.yaml")); err == nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, ".kittify", "charter", "charter.md")); err == nil {
		return legacyCharterBundle
	}
	return freshProjectMissingCharter
}

// EmitCharterPreflight prints the scoped ambient warning on stderr
// (run_preflight_or_abort's advisory channel).
func EmitCharterPreflight(errw io.Writer, root, consumer string) {
	if w := CharterPreflightWarning(root); w != "" {
		fmt.Fprintf(errw, "Warning: %s (scope: charter preflight, consumer '%s', repo root '%s'; advisory only — the command continues; shown once per command run)\n", w, consumer, root)
	}
}
