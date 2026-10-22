package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// AnalysisFreshness is analysis_report.AnalysisFreshness.
type AnalysisFreshness struct {
	OK, Stale, Missing bool
	Path, Reason       string
	Mismatches         []string // sorted input names
}

var (
	checkboxRe   = regexp.MustCompile(`(?m)^(\s*[-*]\s*)\[[ xX]\]`)
	pipeStatusRe = regexp.MustCompile(`\|(\s*)\[[ xXDP]\](\s*)\|`)
)

// normalizeTasksMD strips checkbox / pipe-status churn (#1764, #2493.1).
func normalizeTasksMD(text string) string {
	text = checkboxRe.ReplaceAllString(text, "${1}[ ]")
	for {
		n := pipeStatusRe.ReplaceAllString(text, "|${1}[ ]${2}|")
		if n == text {
			return n
		}
		text = n
	}
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// inputHashes is collect_input_artifact_hashes (sha256 only).
func inputHashes(featureDir, root string) map[string]string {
	out := map[string]string{}
	for _, n := range []string{"spec.md", "plan.md", "tasks.md"} {
		b, err := os.ReadFile(filepath.Join(featureDir, n))
		if err != nil {
			out[n] = ""
			continue
		}
		if n == "tasks.md" {
			out[n] = sha256Hex([]byte(normalizeTasksMD(string(b))))
		} else {
			out[n] = sha256Hex(b)
		}
	}
	out["charter"] = ""
	for _, c := range []string{".kittify/charter/charter.yaml", ".kittify/charter/charter.md"} {
		if b, err := os.ReadFile(filepath.Join(root, c)); err == nil {
			out["charter"] = sha256Hex(b)
			break
		}
	}
	return out
}

// CheckAnalysisReport is check_analysis_report_current for the mission's
// primary directory.
func CheckAnalysisReport(root, slug string) AnalysisFreshness {
	dir := project.Specs(root, slug)
	p := filepath.Join(dir, "analysis-report.md")
	if _, err := os.Stat(p); err != nil {
		return AnalysisFreshness{Path: p, Missing: true, Reason: "missing_analysis_report"}
	}
	d, err := fm.Read(p)
	if err != nil {
		return AnalysisFreshness{Path: p, Stale: true, Reason: "invalid_analysis_report_frontmatter: " + err.Error()}
	}
	if d.Meta.GetString("schema") == "analysis-findings/v1" {
		return AnalysisFreshness{Path: p, Stale: true, Reason: "carrier_format_not_wrapped"}
	}
	if d.Meta.GetString("artifact_type") != "spec-kitty.analysis-report" {
		return AnalysisFreshness{Path: p, Stale: true, Reason: "invalid_analysis_report_artifact_type"}
	}
	saved := d.Meta.GetObject("input_artifacts")
	if saved == nil {
		return AnalysisFreshness{Path: p, Stale: true, Reason: "missing_input_artifacts"}
	}
	if tok, ok := d.Meta.Get("report_transaction"); ok {
		if !reportIsQualified(root, p, tok) {
			return AnalysisFreshness{Path: p, Stale: true, Reason: "unqualified_report_transaction"}
		}
	}
	// current input hashes (sha256 or nil per key)
	cur := map[string]any{}
	for k, v := range inputHashes(dir, root) {
		if v == "" {
			cur[k] = nil
		} else {
			cur[k] = v
		}
	}
	keys := []string{"spec.md", "plan.md", "tasks.md", "charter"}
	if mv, material := d.Meta.Get("material_manifest_version"); material {
		if n, isInt := mv.(int); !isInt || n != 1 {
			return AnalysisFreshness{Path: p, Stale: true, Reason: "invalid_material_inputs: Unsupported material manifest version"}
		}
		mi, err := missionart.CollectMaterialInputs(dir, root)
		if err != nil {
			return AnalysisFreshness{Path: p, Stale: true, Reason: "invalid_material_inputs: " + err.Error()}
		}
		for _, kv := range mi.Pairs() {
			var h any
			if e, ok := kv.Value.(*jsonx.Object); ok {
				h, _ = e.Get("sha256")
			}
			cur[kv.Key] = h
		}
		set := map[string]bool{}
		for k := range cur {
			set[k] = true
		}
		for _, k := range saved.Keys() {
			set[k] = true
		}
		keys = keys[:0]
		for k := range set {
			keys = append(keys, k)
		}
	}
	var mism []string
	for _, k := range keys {
		entry := saved.GetObject(k)
		var savedHash any
		if entry != nil {
			savedHash, _ = entry.Get("sha256")
		}
		curHash, inCur := cur[k]
		if !saved.Has(k) || !inCur || savedHash != curHash {
			mism = append(mism, k)
		}
	}
	sort.Strings(mism)
	if len(mism) > 0 {
		return AnalysisFreshness{Path: p, Stale: true, Reason: "stale_analysis_report", Mismatches: mism}
	}
	return AnalysisFreshness{OK: true, Path: p}
}

// AnalysisVerdict reduces freshness to the runtime verdict fact.
func (f AnalysisFreshness) Verdict() *jsonx.Object {
	switch {
	case f.OK:
		return jsonx.NewObject("status", "current")
	case f.Missing:
		return jsonx.NewObject("status", "missing")
	}
	var names []any
	for _, m := range f.Mismatches {
		names = append(names, m)
	}
	if len(names) == 0 && f.Reason != "" {
		names = append(names, f.Reason)
	}
	return jsonx.NewObject("status", "stale", "stale_inputs", names)
}
