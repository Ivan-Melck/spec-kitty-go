package workflow

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/autorebase"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

// mergeDriverAttributeLines are the _MERGE_DRIVERS attributes lines.
var mergeDriverAttributeLines = []string{
	"kitty-specs/**/status.events.jsonl merge=spec-kitty-event-log",
	"kitty-specs/**/decisions.events.jsonl merge=spec-kitty-event-log",
	"kitty-specs/**/meta.json merge=spec-kitty-meta",
	"kitty-specs/**/traces/*.md merge=spec-kitty-traces",
	"kitty-specs/**/acceptance-matrix.json merge=spec-kitty-acceptance-matrix",
	"kitty-specs/**/issue-matrix.json merge=spec-kitty-issue-matrix",
	"kitty-specs/**/tasks/*/review-cycle-*.md merge=spec-kitty-review-cycle",
	"kitty-specs/**/decisions/index.json merge=spec-kitty-decision-index",
}

// withMergeDriverActivation is lanes.consolidation
// ._ephemeral_merge_driver_activation around fn: the custom driver git-config
// definitions are ensured (persistent) and the driver patterns are seeded
// into $GIT_COMMON_DIR/info/attributes for the merges, then exactly the
// seeded lines are torn down again.
func withMergeDriverActivation(root string, fn func() error) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return fn()
	}
	autorebase.EnsureDriverConfig(root)
	added := seedInfoAttributes(root)
	defer removeInfoAttributes(root, added)
	return fn()
}

func infoAttributesPath(root string) string {
	common, err := gitx.CommonDir(root)
	if err != nil || common == "" {
		return ""
	}
	return filepath.Join(common, "info", "attributes")
}

func splitLinesKeep(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}

// seedInfoAttributes is _ensure_info_attributes (returns the appended lines).
func seedInfoAttributes(root string) []string {
	p := infoAttributesPath(root)
	if p == "" {
		return nil
	}
	b, _ := os.ReadFile(p)
	existing := splitLinesKeep(string(b))
	have := map[string]bool{}
	for _, l := range existing {
		have[l] = true
	}
	var missing []string
	for _, l := range mergeDriverAttributeLines {
		if !have[l] {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	text := strings.TrimRight(strings.Join(append(existing, missing...), "\n"), "\n") + "\n"
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return nil
	}
	return missing
}

// removeInfoAttributes is _remove_info_attributes.
func removeInfoAttributes(root string, added []string) {
	if len(added) == 0 {
		return
	}
	p := infoAttributesPath(root)
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	drop := map[string]bool{}
	for _, l := range added {
		drop[l] = true
	}
	var remaining []string
	for _, l := range splitLinesKeep(string(b)) {
		if !drop[l] {
			remaining = append(remaining, l)
		}
	}
	if len(remaining) == 0 {
		os.Remove(p)
		return
	}
	os.WriteFile(p, []byte(strings.TrimRight(strings.Join(remaining, "\n"), "\n")+"\n"), 0o644)
}
