package kittify

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

// GitattributesEntries is the ordered list init/upgrade ensure in
// .gitattributes (merge drivers, then GitHub diff collapsing).
var GitattributesEntries = []string{
	"kitty-specs/**/status.events.jsonl merge=spec-kitty-event-log",
	"kitty-specs/**/decisions.events.jsonl merge=spec-kitty-event-log",
	"kitty-specs/**/meta.json merge=spec-kitty-meta",
	"kitty-specs/**/traces/*.md merge=spec-kitty-traces",
	"kitty-specs/**/acceptance-matrix.json merge=spec-kitty-acceptance-matrix",
	"kitty-specs/**/issue-matrix.json merge=spec-kitty-issue-matrix",
	"kitty-specs/**/tasks/*/review-cycle-*.md merge=spec-kitty-review-cycle",
	"kitty-specs/**/decisions/index.json merge=spec-kitty-decision-index",
	"kitty-specs/**/issue-matrix.md merge=spec-kitty-issue-matrix",
	"kitty-specs/**/status.json linguist-generated=true",
	"kitty-specs/**/status.events.jsonl linguist-generated=true",
	"kitty-specs/**/lanes.json linguist-generated=true",
	"kitty-specs/**/mission-events.jsonl linguist-generated=true",
	"kitty-specs/**/snapshot-latest.json linguist-generated=true",
	"kitty-specs/**/acceptance-matrix.json linguist-generated=true",
	"kitty-specs/**/occurrence_map.yaml linguist-generated=true",
	"kitty-specs/**/tasks/** linguist-generated=true",
	"kitty-specs/**/research/evidence-log.csv linguist-generated=true",
	"kitty-specs/**/research/source-register.csv linguist-generated=true",
	"kitty-specs/**/test-transcripts/** linguist-generated=true",
	"kitty-specs/**/baseline/** linguist-generated=true",
	"kitty-specs/**/canary-evidence/** linguist-generated=true",
	".kittify/workspaces/** linguist-generated=true",
	".kittify/workspaces/** -diff",
	".kittify/migrations/** linguist-generated=true",
	".kittify/migrations/** -diff",
}

// EnsureGitattributes appends missing entries (init._ensure_event_log_merge_attributes).
func EnsureGitattributes(root string) (bool, error) {
	p := filepath.Join(root, ".gitattributes")
	var lines []string
	if b, err := os.ReadFile(p); err == nil {
		lines = splitLinesPy(string(b))
	}
	have := map[string]bool{}
	for _, l := range lines {
		have[l] = true
	}
	var missing []string
	for _, e := range GitattributesEntries {
		if !have[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}
	lines = append(lines, missing...)
	content := strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n\v\f") + "\n"
	return true, os.WriteFile(p, []byte(content), 0o644)
}

func splitLinesPy(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// MergeDriver is one registered git merge driver.
type MergeDriver struct {
	Key, Name, Command string
	Superseded         []string
}

// MergeDrivers is lanes.consolidation._MERGE_DRIVERS (deduplicated by key).
var MergeDrivers = []MergeDriver{
	{Key: "spec-kitty-event-log", Name: "Spec Kitty event log union merge", Command: "spec-kitty merge-driver-event-log %O %A %B"},
	{Key: "spec-kitty-meta", Name: "Spec Kitty mission meta field merge", Command: "spec-kitty merge-driver-meta %O %A %B"},
	{Key: "spec-kitty-traces", Name: "Spec Kitty mission traces union merge", Command: "spec-kitty merge-driver-traces %O %A %B"},
	{Key: "spec-kitty-acceptance-matrix", Name: "Spec Kitty acceptance matrix filled-side merge", Command: "spec-kitty merge-driver-acceptance-matrix %O %A %B"},
	{Key: "spec-kitty-issue-matrix", Name: "Spec Kitty issue matrix row-aware merge", Command: "spec-kitty merge-driver-issue-matrix %O %A %B", Superseded: []string{"Spec Kitty issue matrix filled-side merge"}},
	{Key: "spec-kitty-review-cycle", Name: "Spec Kitty review-cycle verdict collision refusal", Command: "spec-kitty merge-driver-review-cycle %O %A %B"},
	{Key: "spec-kitty-decision-index", Name: "Spec Kitty decision-index entry union merge", Command: "spec-kitty merge-driver-decision-index %O %A %B"},
}

func isCheckoutRoot(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

func gitConfigGet(root, key string) (string, bool) {
	r, err := gitx.Exec(root, nil, nil, "config", "--local", "--get", key)
	if err != nil || r.Code != 0 {
		return "", false
	}
	return strings.TrimRight(r.Stdout, "\n"), true
}

// InstallMergeDriverConfig is install_merge_driver_config: returns
// "installed", "present" or "customized" plus the customized keys.
func InstallMergeDriverConfig(root string) (string, []string, error) {
	if !isCheckoutRoot(root) {
		return "present", nil, nil
	}
	changed := false
	var customized []string
	for _, d := range MergeDrivers {
		for _, kv := range [][3]string{{"merge." + d.Key + ".name", d.Name, strings.Join(append([]string{d.Name}, d.Superseded...), "\x00")}, {"merge." + d.Key + ".driver", d.Command, d.Command}} {
			cur, ok := gitConfigGet(root, kv[0])
			accepted := false
			for _, a := range strings.Split(kv[2], "\x00") {
				accepted = accepted || cur == a
			}
			if ok && !accepted {
				customized = append(customized, kv[0])
				continue
			}
			if ok && cur == kv[1] {
				continue
			}
			if _, err := gitx.Run(root, "config", "--local", kv[0], kv[1]); err != nil {
				return "", nil, err
			}
			changed = true
		}
	}
	if len(customized) > 0 {
		return "customized", customized, nil
	}
	if changed {
		return "installed", nil, nil
	}
	return "present", nil, nil
}
