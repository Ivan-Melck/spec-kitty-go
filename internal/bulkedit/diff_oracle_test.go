package bulkedit

import (
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffClassificationMatchesOracle(t *testing.T) {
	b, err := os.ReadFile(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	python := strings.TrimPrefix(strings.SplitN(string(b), "\n", 2)[0], "#!")
	s := testkit.NewSandbox(t)
	paths := []string{"src/a.py", "src/a.go", "src/a.erb", "src/a.css", "src/a.scala", "src/a.svg", "src/a.png", "src/a.PNG", "src/archive.png.gz", "src/a.bin", "src/a.db", "src/a.woff2", "tests/cli/commands/a.py", "src/a_test.py", "src/a.test.ts", "testing/a.go", "src/cli/commands/a.py", "bin/script", "README", "Dockerfile", "compose.dev.yml", "src/config.yaml", "src/x.json", "src/x.toml", "src/x.txt", "Gemfile", "Gemfile.lock", "go.sum", ".gitkeep", "assets/raw.dat", "new-file.unknown", "kitty-specs/own/status.json", "kitty-specs/own/issue-matrix.json", "kitty-specs/own/tasks/review-cycle-1.md", "kitty-specs/own/tasks/notes.md", "kitty-specs/own/meta.json", "kitty-specs/own/spec.md", "kitty-specs/other/status.json", "kitty-specs/other/tasks/notes.md", "kitty-specs/other/new-file.unknown"}
	var all []any
	for _, policy := range []string{"rename", "manual_review", "do_not_change"} {
		raw := "target: {term: old-token, operation: rename}\ncategories:\n"
		for _, cat := range vocabulary()["standard_category"] {
			raw += "  " + cat + ": {action: " + policy + "}\n"
		}
		for _, extra := range []string{"", "exceptions:\n- {path: 'src/**/*.py', action: rename}\n- {path: 'src/*.py', field_path: refs, action: do_not_change}\n- {path: '*.png', action: do_not_change}\n- {path: '*.db', action: manual_review}\n", "moves:\n- {from: [assets], to: new-assets}\nstructural_targets:\n- {path: 'src/*.go', reason: refactor}\n- {path: 'src/**/*.scala', reason: broad}\n"} {
			s.WriteFile("occurrence_map.yaml", raw+extra)
			m, err := Load(s.Dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, status := range []string{"", "A", "M", "D", "R"} {
				statuses := map[string]string{}
				for _, p := range paths {
					statuses[p] = status
				}
				r := CheckDiff(paths, m, "kitty-specs/own", statuses)
				var assessments []any
				for _, a := range r.Assessments {
					optional := func(v string) any {
						if v == "" {
							return nil
						}
						return v
					}
					assessments = append(assessments, jsonx.NewObject("path", a.Path, "category", optional(a.Category), "source", a.Source, "action", optional(a.Action), "violation", a.Violation, "reason", a.Reason, "field_path_pins", a.Pins))
				}
				all = append(all, jsonx.NewObject("passed", r.Passed, "assessments", assessments, "errors", r.Errors, "warnings", r.Warnings))
			}
		}
	}
	s.WriteFile("paths.json", jsonx.Dumps(paths, jsonx.Line))
	script := `import dataclasses,json
from pathlib import Path
from specify_cli.bulk_edit.occurrence_map import load_occurrence_map,STANDARD_CATEGORIES
from specify_cli.bulk_edit.diff_check import check_diff_compliance
paths=json.loads(Path('paths.json').read_text());out=[]
extras=['',"exceptions:\n- {path: 'src/**/*.py', action: rename}\n- {path: 'src/*.py', field_path: refs, action: do_not_change}\n- {path: '*.png', action: do_not_change}\n- {path: '*.db', action: manual_review}\n","moves:\n- {from: [assets], to: new-assets}\nstructural_targets:\n- {path: 'src/*.go', reason: refactor}\n- {path: 'src/**/*.scala', reason: broad}\n"]
for policy in ['rename','manual_review','do_not_change']:
 raw='target: {term: old-token, operation: rename}\ncategories:\n'+''.join('  '+c+': {action: '+policy+'}\n' for c in sorted(STANDARD_CATEGORIES))
 for extra in extras:
  Path('occurrence_map.yaml').write_text(raw+extra);m=load_occurrence_map(Path.cwd())
  for status in ['', 'A','M','D','R']:
   out.append(dataclasses.asdict(check_diff_compliance(paths,m,'kitty-specs/own')))
print(json.dumps(out,sort_keys=True,indent=2))`
	oracle := s.Exec("", python, "-c", script)
	if oracle.Code != 0 {
		t.Fatal(oracle.Stderr)
	}
	actual := jsonx.Dumps(all, jsonx.SortedPretty) + "\n"
	if actual != oracle.Stdout {
		t.Error(testkit.Diff(oracle.Stdout, actual))
	}
}
