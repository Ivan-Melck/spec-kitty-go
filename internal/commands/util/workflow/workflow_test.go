package workflow

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestPyPath(t *testing.T) {
	for in, want := range map[string]string{"": ".", ".": ".", "out/": "out", "./a//b/": "a/b", "/x/../y": "/x/../y", "a/./b": "a/b"} {
		if got := PyPath(in); got != want {
			t.Errorf("PyPath(%q)=%q want %q", in, got, want)
		}
	}
}

func validate(t *testing.T, doc string) string {
	t.Helper()
	raw, err := pyyaml.SafeLoad(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(raw); err != nil {
		return err.Error()
	}
	return "OK"
}

// Expected strings are str(pydantic.ValidationError) from the upstream
// install (pydantic 2.14).
func TestValidateMatchesPydantic(t *testing.T) {
	got := validate(t, "workflow_id: 5\ndescription: d\nactions: []\ninitial: s\nversion: 0\nextra: 1\n")
	want := `3 validation errors for WorkflowSequence
workflow_id
  Input should be a valid string [type=string_type, input_value=5, input_type=int]
    For further information visit https://errors.pydantic.dev/2.14/v/string_type
version
  Input should be greater than or equal to 1 [type=greater_than_equal, input_value=0, input_type=int]
    For further information visit https://errors.pydantic.dev/2.14/v/greater_than_equal
extra
  Extra inputs are not permitted [type=extra_forbidden, input_value=1, input_type=int]
    For further information visit https://errors.pydantic.dev/2.14/v/extra_forbidden`
	if got != want {
		t.Errorf("got:\n%s", got)
	}
	got = validate(t, "workflow_id: x\ndescription: d\nversion: \"2\"\ninitial: s\nactions:\n  - action_name: s\n    description: dd\n    next: [t]\n  - 5\n  - action_name: t\n    next: t\n    terminal: maybe\n    outputs: {a: 1}\n")
	want = `5 validation errors for WorkflowSequence
actions.1
  Input should be a valid dictionary or instance of ActionStep [type=model_type, input_value=5, input_type=int]
    For further information visit https://errors.pydantic.dev/2.14/v/model_type
actions.2.next
  Input should be a valid list [type=list_type, input_value='t', input_type=str]
    For further information visit https://errors.pydantic.dev/2.14/v/list_type
actions.2.description
  Field required [type=missing, input_value={'action_name': 't', 'nex...e', 'outputs': {'a': 1}}, input_type=dict]
    For further information visit https://errors.pydantic.dev/2.14/v/missing
actions.2.terminal
  Input should be a valid boolean, unable to interpret input [type=bool_parsing, input_value='maybe', input_type=str]
    For further information visit https://errors.pydantic.dev/2.14/v/bool_parsing
actions.2.outputs.a
  Input should be a valid string [type=string_type, input_value=1, input_type=int]
    For further information visit https://errors.pydantic.dev/2.14/v/string_type`
	if got != want {
		t.Errorf("got:\n%s", got)
	}
	got = validate(t, "workflow_id: x\ndescription: d\nversion: 1\ninitial: s\nactions:\n  - {action_name: s, description: dd, next: [t]}\n  - {action_name: t, description: dd, next: [s]}\n")
	want = `1 validation error for WorkflowSequence
  Value error, cycle detected at action 's' [type=value_error, input_value={'workflow_id': 'x', 'des...: 'dd', 'next': ['s']}]}, input_type=dict]
    For further information visit https://errors.pydantic.dev/2.14/v/value_error`
	if got != want {
		t.Errorf("got:\n%s", got)
	}
	got = validate(t, "- a\n")
	if !strings.HasPrefix(got, "1 validation error for WorkflowSequence\n  Input should be a valid dictionary or instance of WorkflowSequence [type=model_type, input_value=['a'], input_type=list]") {
		t.Errorf("got:\n%s", got)
	}
	if got := validate(t, "workflow_id: x\ndescription: d\nversion: true\ninitial: s\nactions:\n  - {action_name: s, description: dd, terminal: 'yes'}\n"); got != "OK" {
		t.Errorf("got:\n%s", got)
	}
}

const goodWF = `workflow_id: team-flow
description: Team flow
version: 1
initial: specify
actions:
  - action_name: specify
    description: Specify
    next: [plan]
  - action_name: plan
    description: Plan
    terminal: true
`

func wfFixtures(s *testkit.Sandbox) {
	s.WriteFile(".kittify/config.yaml", "agents:\n  available: [claude]\n")
	s.WriteFile(".kittify/overrides/workflows/team-flow.yaml", goodWF)
	s.WriteFile(".kittify/overrides/workflows/mismatch.yaml", strings.Replace(goodWF, "team-flow", "other-id", 1))
	s.WriteFile(".kittify/overrides/workflows/broken.workflow.yaml", "workflow_id: broken\n")
	s.WriteFile(".kittify/overrides/workflows/Bad_Name.yaml", strings.Replace(goodWF, "team-flow", "Bad_Name", 1))
	s.WriteFile("incoming/new-flow.yaml", strings.Replace(goodWF, "team-flow", "new-flow", 1))
	s.WriteFile("incoming/invalid.yaml", "workflow_id: x\ndescription: d\nversion: 1.5\ninitial: q\nactions:\n  - action_name: s\n    description: dd\n")
	s.WriteFile("incoming/badid.yaml", strings.Replace(goodWF, "team-flow", "Bad Id", 1))
	s.WriteFile("incoming/notutf8.yaml", "workflow_id: \xff\n")
	s.WriteFile("incoming/broken-yaml.yaml", "a: [b\n")
	s.WriteFile("outdir/.keep", "")
}

func TestWorkflowParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(wfFixtures)
	p.Compare(true, "workflow", "list")
	p.Compare(true, "workflow", "list", "--project-root", "nowhere")
	p.Compare(true, "workflow", "export", "software-dev-default", "outdir")
	p.Compare(true, "workflow", "export", "our-team-design-first", "outdir/")
	p.Compare(true, "workflow", "export", "team-flow", "./out//nested/tf.yaml")
	p.Compare(true, "workflow", "export", "team-flow", "outdir/team-flow.yaml")
	p.Compare(true, "workflow", "export", "team-flow", "outdir", "--force")
	p.Compare(true, "workflow", "export", "Bad_Name", "outdir")
	p.Compare(true, "workflow", "export", "mismatch", "outdir")
	p.Compare(true, "workflow", "export", "broken", "outdir")
	p.Compare(true, "workflow", "export", "software-dev-default")
	p.Compare(true, "workflow", "import", "incoming/new-flow.yaml")
	p.Compare(true, "workflow", "import", "incoming/new-flow.yaml")
	p.Compare(true, "workflow", "import", "./incoming/new-flow.yaml", "--force")
	p.Compare(true, "workflow", "import", "incoming/invalid.yaml")
	p.Compare(true, "workflow", "import", "incoming/badid.yaml")
	p.Compare(true, "workflow", "import", "incoming/missing.yaml")
	p.Compare(true, "workflow", "import", "incoming")
	p.Compare(true, "workflow", "import", "incoming/notutf8.yaml")
	p.Compare(true, "workflow", "import", "incoming/new-flow.yaml", "--project-root", "sub/proj")
	p.Compare(true, "workflow", "list")
	p.CompareTree("outdir", true)
	p.CompareTree("out", true)
	p.CompareTree(".kittify", true)
	p.CompareTree("sub", true)

	// Messages that embed upstream-only text (PyYAML parser errors, the
	// wheel's site-packages path) are compared up to that text.
	prefix := func(args []string, upto string) {
		t.Helper()
		g, o := p.Compare(false, args...)
		gi, oi := strings.Index(g.Stderr, upto)+len(upto), strings.Index(o.Stderr, upto)+len(upto)
		if gi < len(upto) || oi < len(upto) || p.Go.Normalize(g.Stderr[:gi]) != p.Oracle.Normalize(o.Stderr[:oi]) {
			t.Errorf("%v stderr:\n go: %q\n oracle: %q", args, g.Stderr, o.Stderr)
		}
	}
	prefix([]string{"workflow", "export", "nope", "outdir"}, "nope.yaml', '")
	prefix([]string{"workflow", "import", "incoming/broken-yaml.yaml"}, "is not valid: ")
}

func TestWorkflowE2E(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	wfFixtures(s)
	r := s.Run("workflow", "list")
	if r.Code != 0 || r.Stdout != "our-team-design-first\nsoftware-dev-default\nteam-flow\n" {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("workflow", "export", "software-dev-default", "outdir")
	if r.Code != 0 || r.Stdout != "outdir/software-dev-default.yaml\n" || !strings.Contains(s.ReadFile("outdir/software-dev-default.yaml"), "workflow_id: software-dev-default") {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("workflow", "import", "incoming/invalid.yaml")
	if r.Code != 1 || !strings.Contains(r.Stderr, "int_from_float") {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
}
