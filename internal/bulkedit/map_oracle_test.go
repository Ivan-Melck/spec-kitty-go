package bulkedit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestOccurrenceGateMatchesOracle(t *testing.T) {
	b, err := os.ReadFile(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	python := strings.TrimPrefix(strings.SplitN(string(b), "\n", 2)[0], "#!")
	s := testkit.NewSandbox(t)
	template := string(assets.MustRead("charter/offering/templates/occurrence-map-template.yaml"))
	valid := strings.Replace(template, "<term-to-rename>", "old-token", 1)
	cases := []string{"", "{}", "target: {}\n", "target: null\ncategories: []\n", "target: {term: 1, operation: wrong}\ncategories: {code_symbols: null}\n", valid}
	// Every optional declaration and every narrowness boundary is exercised
	// through the public readiness seam, before any workflow can mutate state.
	for _, extra := range []string{"exceptions: 1", "exceptions: [1, {}, {path: '', action: wrong, field_path: ''}]", "moves: 1", "moves: [1, {}, {from: [1], to: ''}]", "structural_targets: 1", "structural_targets: [1, {}]", "structural_targets: [{path: src/**/*.py, reason: broad}]", "structural_targets: [{path: src/*.py, reason: narrow}]", "structural_targets: [{path: file.*, reason: broad}]", "unexpected: true"} {
		// The starter already declares optional sections; avoid duplicate keys.
		base := "target: {term: old-token, operation: rename}\ncategories:\n"
		for _, cat := range vocabulary()["standard_category"] {
			base += "  " + cat + ": {action: do_not_change}\n"
		}
		cases = append(cases, base+extra+"\n")
	}
	var actual []any
	for i, content := range cases {
		dir := fmt.Sprintf("cases/%d", i)
		s.WriteFile(dir+"/meta.json", `{"change_mode":"bulk_edit"}`)
		if content != "" {
			s.WriteFile(dir+"/occurrence_map.yaml", content)
		}
		r, err := Ready(filepath.Join(s.Dir, dir))
		if err != nil {
			actual = append(actual, jsonx.NewObject("exception", err.Error()))
			continue
		}
		actual = append(actual, jsonx.NewObject("passed", r.Passed, "bulk", r.BulkEdit, "errors", r.Errors, "warnings", r.Warnings))
	}
	script := fmt.Sprintf("import json\nfrom pathlib import Path\nfrom specify_cli.bulk_edit.gate import ensure_occurrence_classification_ready\nout=[]\nfor i in range(%d):\n try:\n  r=ensure_occurrence_classification_ready(Path.cwd()/'cases'/str(i))\n  out.append(dict(passed=r.passed,bulk=r.change_mode=='bulk_edit',errors=r.errors,warnings=r.warnings))\n except Exception as e: out.append(dict(exception=str(e)))\nprint(json.dumps(out,sort_keys=True,indent=2))", len(cases))
	oracle := s.Exec("", python, "-c", script)
	if oracle.Code != 0 {
		t.Fatal(oracle.Stderr)
	}
	got := jsonx.Dumps(actual, jsonx.SortedPretty) + "\n"
	if got != oracle.Stdout {
		t.Error(testkit.Diff(oracle.Stdout, got))
	}
}
