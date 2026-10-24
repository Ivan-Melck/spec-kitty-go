package bulkedit

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffNotesAndRefusalRenderingMatchOracle(t *testing.T) {
	b, err := os.ReadFile(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	python := strings.TrimPrefix(strings.SplitN(string(b), "\n", 2)[0], "#!")
	s := testkit.NewSandbox(t)
	// One blocking row, one manual-review row, one bypass, a field pin,
	// and enough added paths to exercise the 12-path informational limit.
	r := DiffResult{Passed: false, Assessments: []Assessment{
		{Path: "src/protected.py", Category: "code_symbols", Source: "path-heuristic", Action: "do_not_change", Violation: true, Reason: "Protected occurrence"},
		{Path: "docs/review.md", Category: "user_facing_strings", Source: "path-heuristic", Action: "manual_review"},
		{Path: "src/structural.go", Source: "structural-target", Action: "structural_edit"},
	}, Errors: []string{"src/protected.py: Protected occurrence"}, Warnings: []string{"docs/review.md: field-path exception pins title as do_not_change — verify only those fields were left untouched"}}
	for i := 0; i < 15; i++ {
		r.Assessments = append(r.Assessments, Assessment{Path: fmt.Sprintf("new/file-%02d.py", i), Source: "added"})
	}
	var rows []any
	optional := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	for _, a := range r.Assessments {
		rows = append(rows, jsonx.NewObject("path", a.Path, "category", optional(a.Category), "source", a.Source, "action", optional(a.Action), "violation", a.Violation, "reason", a.Reason, "field_path_pins", []any{}))
	}
	s.WriteFile("result.json", jsonx.Dumps(jsonx.NewObject("passed", r.Passed, "assessments", rows, "errors", r.Errors, "warnings", r.Warnings), jsonx.Line))
	for _, failure := range []bool{false, true} {
		var buf bytes.Buffer
		c := rich.CliConsoleFor(&buf, 160)
		print := func(v any) { c.Print(&buf, v) }
		if failure {
			RenderDiffFailure(r, print)
		} else {
			RenderDiffWarnings(r, print)
		}
		script := "import json\nfrom pathlib import Path\nfrom types import SimpleNamespace\nfrom dataclasses import replace\nfrom rich.console import Console\nfrom specify_cli.bulk_edit.diff_check import DiffCheckResult,FileAssessment\nfrom specify_cli.bulk_edit.gate import render_diff_check_failure\nd=json.loads(Path('result.json').read_text());d['assessments']=[FileAssessment(**a) for a in d['assessments']]\nr=DiffCheckResult(**d)\nc=Console(width=160,force_terminal=False,color_system=None)\n"
		if failure {
			script += "render_diff_check_failure(r,c)\n"
		} else {
			script += "from specify_cli.cli.commands.agent.workflow import _enforce_bulk_edit_diff_compliance\nr=replace(r,passed=True)\n_enforce_bulk_edit_diff_compliance(feature_dir=Path.cwd(),main_repo_root=Path.cwd(),mission_slug='own',target_branch='main',review_workspace=SimpleNamespace(branch_name='HEAD'),check_review_diff_compliance=lambda **kw:r,render_diff_check_failure=render_diff_check_failure,rich_console=c)\n"
		}

		oracle := s.Exec("", python, "-c", script)
		if oracle.Code != 0 {
			t.Fatal(oracle.Stderr)
		}
		if buf.String() != oracle.Stdout {
			t.Errorf("failure=%t: %s", failure, testkit.Diff(oracle.Stdout, buf.String()))
		}
	}
}

func TestInferenceMatchesOracle(t *testing.T) {
	b, err := os.ReadFile(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	python := strings.TrimPrefix(strings.SplitN(string(b), "\n", 2)[0], "#!")
	s := testkit.NewSandbox(t)
	texts := []string{"", "update change modify refactor", "rename", "rename migrate", "rename across", "bulk edit", "codemod", "find-and-replace", "find and replace", "replace everywhere", "terminology migration", "rename all occurrences", "replace all", "across the codebase", "updated_at unchanged renamed_at", "sed globAlly refactor", "élised", "migrate横", "rename across migrate update", "search and replace old-token"}
	var actual []any
	for _, text := range texts {
		r := Infer(text)
		matched := []any{}
		for _, p := range r.Matched {
			matched = append(matched, []any{p.Text, p.Weight})
		}
		actual = append(actual, jsonx.NewObject("score", r.Score, "threshold", r.Threshold, "triggered", r.Triggered, "matched_phrases", matched))
	}
	script := "import json,dataclasses\nfrom specify_cli.bulk_edit.inference import score_spec_for_bulk_edit\nprint(json.dumps([dataclasses.asdict(score_spec_for_bulk_edit(t)) for t in " + jsonx.Dumps(texts, jsonx.Line) + "],sort_keys=True,indent=2))"
	oracle := s.Exec("", python, "-c", script)
	if oracle.Code != 0 {
		t.Fatal(oracle.Stderr)
	}
	got := jsonx.Dumps(actual, jsonx.SortedPretty) + "\n"
	if got != oracle.Stdout {
		t.Error(testkit.Diff(oracle.Stdout, got))
	}
}
