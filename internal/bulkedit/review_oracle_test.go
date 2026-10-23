package bulkedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestReviewDiffHistoryMatchesOracle(t *testing.T) {
	b, err := os.ReadFile(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	python := strings.TrimPrefix(strings.SplitN(string(b), "\n", 2)[0], "#!")
	s := testkit.NewGitSandbox(t)
	const dir = "kitty-specs/own"
	s.WriteFile(dir+"/meta.json", `{"change_mode":"bulk_edit"}`)
	raw := "target: {term: old-token, operation: rename}\ncategories:\n"
	for _, cat := range vocabulary()["standard_category"] {
		raw += "  " + cat + ": {action: do_not_change}\n"
	}
	s.WriteFile(dir+"/occurrence_map.yaml", raw)
	s.WriteFile("src/old.py", "old-token\n")
	s.WriteFile("docs/guide.md", "original\n")
	s.Git("add", "-A")
	s.Git("commit", "-qm", "base")
	s.Git("tag", "base")
	s.Git("mv", "src/old.py", "src/new.py")
	s.WriteFile("docs/guide.md", "changed\n")
	s.WriteFile("src/new file.txt", "created\n")
	s.WriteFile(dir+"/status.json", "{}\n")
	s.Git("add", "-A")
	s.Git("commit", "-qm", "head")
	for _, refs := range [][2]string{{"base", "HEAD"}, {"base", "base"}, {"missing", "HEAD"}, {"base", "missing"}, {"", "HEAD"}} {
		r, err := ReviewDiff(filepath.Join(s.Dir, dir), s.Dir, refs[0], refs[1])
		if err != nil {
			t.Fatal(err)
		}
		paths, sources := []string{}, []string{}
		for _, a := range r.Assessments {
			paths = append(paths, a.Path)
			sources = append(sources, a.Source)
		}
		actual := jsonx.Dumps(jsonx.NewObject("passed", r.Passed, "errors", r.Errors, "warnings", r.Warnings, "paths", paths, "sources", sources), jsonx.SortedPretty) + "\n"
		script := "import json\nfrom pathlib import Path\nfrom specify_cli.bulk_edit.gate import check_review_diff_compliance\nr=check_review_diff_compliance(Path.cwd()/'kitty-specs/own',Path.cwd()," + jsonx.Dumps(refs[0], jsonx.Line) + "," + jsonx.Dumps(refs[1], jsonx.Line) + ")\nprint(json.dumps(dict(passed=r.passed,errors=r.errors,warnings=r.warnings,paths=[a.path for a in r.assessments],sources=[a.source for a in r.assessments]),sort_keys=True,indent=2))"
		oracle := s.Exec("", python, "-c", script)
		if oracle.Code != 0 {
			t.Fatal(oracle.Stderr)
		}
		if actual != oracle.Stdout {
			t.Errorf("%v: %s", refs, testkit.Diff(oracle.Stdout, actual))
		}
	}
}
