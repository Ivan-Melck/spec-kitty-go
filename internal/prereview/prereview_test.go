package prereview

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAssessBudgetIdentity(t *testing.T) {
	a := AssessBudget(nil, 300)
	if a.IdentityValue != "budget-v1:sha256:9e7dd2415b61b9fceb1d40f67f6359d3cf4895920d1facc716150e5b38db9b00" || a.Classification != BudgetUnknown {
		t.Fatalf("unexpected %+v", a)
	}
	if AssessBudget([]string{"./tests/architectural/", "x"}, 300).Classification != BudgetOversized {
		t.Fatal("oversized rule")
	}
	if AssessBudget([]string{"tests/review/test_gate_budget.py::test_identity_matches_pinned_vector"}, 300).Classification != BudgetBounded {
		t.Fatal("bounded rule")
	}
}

func TestBuildShellCommand(t *testing.T) {
	got := BuildShellCommand("pytest --junitxml={output_file} '{output_file}'", map[string]string{"output_file": "/tmp/a b/o.xml"})
	want := []string{"sh", "-c", `export SPEC_KITTY_CMD_OUTPUT_FILE='/tmp/a b/o.xml'; pytest --junitxml="${SPEC_KITTY_CMD_OUTPUT_FILE}" ''"${SPEC_KITTY_CMD_OUTPUT_FILE}"''`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if !reflect.DeepEqual(BuildShellCommand("make test", map[string]string{"output_file": "x"}), []string{"sh", "-c", "make test"}) {
		t.Fatal("no substitution")
	}
	if ShlexValid(`echo "unterminated`) || !ShlexValid(`echo 'a"b'`) {
		t.Fatal("shlex validity")
	}
}

func TestParseResultsTextAndWholeRun(t *testing.T) {
	s := &DeclaredSource{Root: t.TempDir()}
	f, _ := s.ParseResults(&Raw{ReturnCode: 1, Stdout: "FAIL a::b: msg\nok\n", Stderr: "FAIL c\n"})
	want := []Failure{{Test: "a", Error: ":b: msg", File: "unknown"}, {Test: "c", Error: "failed", File: "unknown"}}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("got %+v", f)
	}
	f, _ = s.ParseResults(&Raw{ReturnCode: 2, Stderr: "boom\nlast line\n"})
	if len(f) != 1 || f[0].Test != "<declared-command>" || f[0].Error != "last line" {
		t.Fatalf("got %+v", f)
	}
	if f, _ := s.ParseResults(&Raw{}); f != nil {
		t.Fatalf("clean run: %+v", f)
	}
}

func TestParseJUnitAndDiff(t *testing.T) {
	p := filepath.Join(t.TempDir(), "j.xml")
	os.WriteFile(p, []byte(`<testsuites><testsuite><testcase classname="m" name="a" file="f.py" line="2"><failure message="x&#10;y"/></testcase><testcase name="b"><error/></testcase><testcase name="c"><skipped/></testcase><testcase name="d"/></testsuite></testsuites>`), 0o644)
	total, passed, failed, skipped, fs, err := ParseJUnit(p)
	if err != nil || total != 4 || passed != 1 || failed != 2 || skipped != 1 {
		t.Fatalf("%d %d %d %d %v", total, passed, failed, skipped, err)
	}
	if fs[0] != (Failure{Test: "m.a", Error: "x", File: "f.py:2"}) || fs[1] != (Failure{Test: "b", Error: "Unknown error", File: "unknown:?"}) {
		t.Fatalf("%+v", fs)
	}
	pre, newF, fixed := Diff(&Baseline{Failures: []Failure{{Test: "m.a"}, {Test: "z"}}}, fs)
	if len(pre) != 1 || len(newF) != 1 || !reflect.DeepEqual(fixed, []string{"z"}) {
		t.Fatalf("%v %v %v", pre, newF, fixed)
	}
}

func TestTranslateBlockAndTerminal(t *testing.T) {
	nf := &Verdict{Outcome: NewFailures, RunState: RunCompleted, NewFailures: []Failure{{Test: "t"}}}
	e := Translate([]*Verdict{nf}, true, false)
	if !e.Blocked || !e.ShouldExit || BlockMessage(e.Representative) == "" {
		t.Fatal("block")
	}
	if e := Translate([]*Verdict{nf}, true, true); e.Blocked || e.Metadata.GetBool("force_bypassed") != true {
		t.Fatal("force bypass")
	}
	to := &Verdict{Outcome: TimedOut, RunState: RunTimedOut, Reason: strp("x")}
	if e := Translate([]*Verdict{nf, to}, false, false); !e.Terminal || e.Representative != to {
		t.Fatal("terminal")
	}
	if _, ok := ConsoleWarning(NotDeclaredVerdict(), false); ok {
		t.Fatal("quiet skip")
	}
}
